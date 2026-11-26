// ##############################################################################
// Script Name: internal/commands/server/migrate.go
// Description: Migrates all data from this server to another.
// Usage: opencli server-migrate -h <DESTINATION_IP> --user root --password <DESTINATION_PASSWORD> [--force] [--no-throttle] [--exclude-* options]
// Author: Stefan Pejcic
// Created: 26.06.2025
// Last Modified: 20.11.2026
// Company: openpanel.com
// Copyright (c) openpanel.com
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.
// ##############################################################################

package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/podman"
	"opencli/internal/requirement"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "server-migrate",
		Desc:  "Migrates all data from this server to another.",
		Usage: "opencli server-migrate -h <DESTINATION_IP> --user root --password <DESTINATION_PASSWORD> [--force] [--no-throttle] [--exclude-* options]",
		Run:   migrate,
	})
}

type migration struct {
	host, user, pass                             string
	exHome, exLogs, exMail, exBind, exCSF        bool
	exOpenpanel, exMysql, exStack, exPostupdate  bool
	exUsers, exContexts, force, composeStartMail bool
	noThrottle                                   bool

	rsync     []string
	currentIP string
	remoteIP  string
	usedBytes int64
	errors    []string
	start     time.Time
	logFile   string
}

func migrate(args []string) int {
	m := &migration{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		// shift 2 on a last arg looped forever in bash, a missing value now ends up as the usage error
		val := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			fmt.Println("Usage: opencli server-migrate -h <remote_host> -u <remote_user> [--password <password>] [--exclude-* options]")
			cli.Exit(1)
			return ""
		}
		switch a {
		case "-h", "--host":
			m.host = val()
		case "-u", "--user":
			m.user = val()
		case "--password":
			m.pass = val()
		case "--exclude-home":
			m.exHome = true
		case "--exclude-logs":
			m.exLogs = true
		case "--exclude-csf":
			m.exCSF = true
		case "--exclude-mail":
			m.exMail = true
		case "--exclude-bind":
			m.exBind = true
		case "--exclude-openpanel":
			m.exOpenpanel = true
		case "--exclude-mysql":
			m.exMysql = true
		case "--exclude-stack":
			m.exStack = true
		case "--exclude-postupdate":
			m.exPostupdate = true
		case "--exclude-users":
			m.exUsers = true
		case "--exclude-contexts":
			m.exContexts = true
		case "--force":
			m.force = true
		case "--no-throttle":
			m.noThrottle = true
		default:
			fmt.Printf("Unknown option: %s\n", a)
			return 1
		}
	}
	if m.host == "" || m.user == "" {
		fmt.Println("Usage: opencli server-migrate -h <remote_host> -u <remote_user> [--password <password>] [--exclude-* options]")
		return 1
	}

	os.Setenv("SSHPASS", m.pass)

	// persistent log, openadmin's /tmp/server_migrate.log gets overwritten on every run
	m.start = time.Now()
	logDir := "/var/log/openpanel/admin/migrations"
	os.MkdirAll(logDir, 0o755)
	m.logFile = fmt.Sprintf("%s/%s_%s.log", logDir, m.host, time.Now().Format("2006-01-02_15-04-05"))
	stop := migrateTee(m.logFile)
	defer stop()
	fmt.Printf("Migration started, log file: %s\n", m.logFile)
	// openadmin reads the pid from line 2 and SUCCESS:/FATAL ERROR: from the last line for the log list status
	fmt.Printf("PID: %d\n", os.Getpid())

	m.run()
	return 0
}

// migrateTee sends fd 1 and 2 through a pipe to the terminal and the log, like exec > >(tee -a log) 2>&1
func migrateTee(logFile string) func() {
	orig, err := syscall.Dup(1)
	if err != nil {
		return func() {}
	}
	r, w, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	lf, _ := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	out := os.NewFile(uintptr(orig), "stdout")
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				out.Write(buf[:n])
				if lf != nil {
					lf.Write(buf[:n])
				}
			}
			if err != nil {
				break
			}
		}
		close(done)
	}()
	origErr, _ := syscall.Dup(2)
	syscall.Dup3(int(w.Fd()), 1, 0)
	syscall.Dup3(int(w.Fd()), 2, 0)
	w.Close()
	var once sync.Once
	return func() {
		once.Do(func() {
			syscall.Dup3(orig, 1, 0)
			syscall.Dup3(origErr, 2, 0)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
			if lf != nil {
				lf.Close()
			}
		})
	}
}

func (m *migration) dest() string { return m.user + "@" + m.host }

func (m *migration) ssh(remote string) *sh.Cmd {
	return sh.C("sshpass", "-e", "ssh", "-o", "StrictHostKeyChecking=no", m.dest(), remote)
}

// rsyncCmd runs at low priority so the copy doesn't slow this server down, --no-throttle skips that
func (m *migration) rsyncCmd(args ...string) *sh.Cmd {
	all := append(append([]string{}, m.rsync[1:]...), args...)
	if m.noThrottle {
		return sh.C(m.rsync[0], all...)
	}
	return sh.LowPrio(m.rsync[0], all...)
}

func (m *migration) checkInstallSshpass() {
	// If a password is provided, use sshpass for rsync/scp
	if m.pass != "" {
		requirement.Command("sshpass")
		m.rsync = []string{"sshpass", "-e", "rsync", "-azHAX", "--numeric-ids", "-e", "ssh -o StrictHostKeyChecking=no"}
	} else {
		m.rsync = []string{"rsync", "-azHAX", "--numeric-ids"}
	}
}

func (m *migration) checkDiskUsedOnSource() {
	out := sh.C("df", "--output=used", "/home").Str()
	lines := strings.Split(out, "\n")
	n, _ := strconv.ParseInt(strings.TrimSpace(lines[len(lines)-1]), 10, 64)
	m.usedBytes = n * 1024 // 1K blocks
}

var migrateDigits = regexp.MustCompile(`^[0-9]+$`)

func (m *migration) checkIfDestHasSpace() {
	fmt.Println("Checking available disk on destination server ...")
	avail := m.ssh("df --output=avail /home | tail -n 1").StdinReader(os.Stdin).Str()
	// empty means ssh itself failed, don't report that as a full disk
	if !migrateDigits.MatchString(avail) {
		fmt.Printf("FATAL ERROR: Could not connect to %s@%s. Check the SSH user, password and that SSH is reachable.\n", m.user, m.host)
		cli.Exit(1)
	}
	n, _ := strconv.ParseInt(avail, 10, 64)
	availBytes := n * 1024 // 1K blocks
	if availBytes >= m.usedBytes {
		fmt.Println("There is enough disk space on destination server.")
	} else {
		fmt.Printf("Available: %d bytes - Needed: %d bytes\n", availBytes, m.usedBytes)
		fmt.Println("FATAL ERROR: Not enough disk space on destination.")
		cli.Exit(1)
	}
}

// migrateCurl is curl --silent -4 --max-time, ok is false when curl itself would fail
func migrateCurl(url string, timeout time.Duration) (string, bool) {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
		},
		TLSClientConfig: &tls.Config{},
	}
	c := &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return strings.TrimRight(string(b), "\n"), false
	}
	return strings.TrimRight(string(b), "\n"), true
}

var (
	ipv4Re      = regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}$`)
	privateIPRe = regexp.MustCompile(`^(10\.|172\.(1[6-9]|2[0-9]|3[0-1])\.|192\.168\.)`)
)

func (m *migration) getServerIPv4() {
	ip, ok := migrateCurl("https://ip.openpanel.com", time.Second)
	if !ok {
		ip, _ = migrateCurl("https://ifconfig.me/ip", time.Second)
	}
	if ip == "" {
		for _, l := range strings.Split(sh.C("ip", "addr").Str(), "\n") {
			if strings.Contains(l, "inet ") && strings.Contains(l, "global") {
				ip, _, _ = strings.Cut(text.Field(l, 2), "/")
				break
			}
		}
	}
	m.currentIP = ip
	if !ipv4Re.MatchString(ip) || privateIPRe.MatchString(ip) {
		fmt.Printf("Invalid or private IPv4 address: %s. OpenPanel requires a public IPv4 address to bind Nginx configuration files.\n", ip)
	}
}

func (m *migration) getRemoteIPv4() {
	m.remoteIP = m.ssh(`curl --silent --max-time 3 -4 https://ip.openpanel.com || curl --silent --max-time 3 -4 https://ifconfig.me/ip || hostname -I | awk '{print $1}'`).Str()
	if !ipv4Re.MatchString(m.remoteIP) {
		fmt.Printf("[!] Could not detect the public IPv4 of %s, using %s as is.\n", m.host, m.host)
		m.remoteIP = m.host
	}
}

// every remote step would hit this server instead, so --force doesn't skip this one
func (m *migration) refuseMigratingToSelf() {
	localIPs := " " + m.currentIP + " " + sh.Str("hostname", "-I") + " "
	h := m.host
	hn, _ := os.Hostname()
	if h == "localhost" || strings.HasPrefix(h, "127.") || h == "::1" || h == hn ||
		h == sh.Str("hostname", "-f") || strings.Contains(localIPs, " "+h+" ") || strings.Contains(localIPs, " "+m.remoteIP+" ") {
		fmt.Printf("FATAL ERROR: %s is this server. Set the IP of the destination server.\n", m.host)
		cli.Exit(1)
	}
}

func (m *migration) getUsersCountOnDestination() {
	out, err := m.ssh(fmt.Sprintf(`mariadb --defaults-extra-file=%s -D %s -e "SELECT COUNT(*) FROM users" -sN`, db.ConfigFile, db.Database)).StdinReader(os.Stdin).Out()
	if err != nil {
		fmt.Println("FATAL ERROR: Unable to check users from remote server. Is OpenPanel installed?")
		cli.Exit(1)
	}
	if n, e := strconv.Atoi(out); e == nil && n > 0 {
		fmt.Println("FATAL ERROR: Migration is possible only to a freshly installed OpenPanel with no existing users.")
		cli.Exit(1)
	}
}

// migrateUIDRange is awk -F: '$3 >= 1000 && $3 < 65534'
func migrateUIDRange(line string) bool {
	f := strings.Split(line, ":")
	if len(f) < 3 {
		return false
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(f[2]), 64)
	return err == nil && n >= 1000 && n < 65534
}

func migrateFilter(path string, keep func(string) bool) string {
	var b strings.Builder
	for _, l := range conf.Lines(path) {
		if keep(l) {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

const migrateRemoteUsers = `USER_PASSWD="/root/passwd.users"
USER_GROUP="/root/group.users"
USER_SHADOW="/root/shadow.users"

# Add groups
cut -d: -f1,3 "$USER_GROUP" | while IFS=: read -r group gid; do
    if ! getent group "$group" > /dev/null; then
        groupadd -g "$gid" "$group"
    fi
done

# Add users
cut -d: -f1,3,4,5,6,7 "$USER_PASSWD" | while IFS=: read -r user uid gid comment home shell; do
    if ! id "$user" &>/dev/null; then
        useradd -u "$uid" -g "$gid" -c "$comment" -d "$home" -s "$shell" "$user"
    fi
done

# Set passwords from shadow file
cut -d: -f1,2 "$USER_SHADOW" | while IFS=: read -r user hash; do
    if [ -n "$hash" ]; then
        usermod -p "$hash" "$user"
    fi
done

for f in subuid subgid; do
    [ -s "/root/$f.users" ] || continue
    cut -d: -f1 "/root/$f.users" | while read -r user; do sed -i "/^$user:/d" "/etc/$f"; done
    cat "/root/$f.users" >> "/etc/$f"
done

rm -rf "$USER_PASSWD" "$USER_GROUP" "$USER_SHADOW" /root/subuid.users /root/subgid.users

`

func (m *migration) copyUserAccounts() {
	tmp, err := os.MkdirTemp("", "tmp.")
	if err != nil {
		return
	}
	users := map[string]bool{}
	passwd := migrateFilter("/etc/passwd", migrateUIDRange)
	for _, l := range text.Lines(passwd) {
		name, _, _ := strings.Cut(l, ":")
		users[name] = true
	}
	inUsers := func(l string) bool { name, _, _ := strings.Cut(l, ":"); return users[name] }
	files := map[string]string{
		"passwd.users": passwd,
		"group.users":  migrateFilter("/etc/group", migrateUIDRange),
		"shadow.users": migrateFilter("/etc/shadow", inUsers),
		// useradd hands out subuid ranges in creation order, keep the source ones so volume files keep their owners
		"subuid.users": migrateFilter("/etc/subuid", inUsers),
		"subgid.users": migrateFilter("/etc/subgid", inUsers),
	}
	var paths []string
	for _, n := range []string{"passwd.users", "group.users", "shadow.users", "subuid.users", "subgid.users"} {
		p := filepath.Join(tmp, n)
		os.WriteFile(p, []byte(files[n]), 0o644)
		paths = append(paths, p)
	}
	m.rsyncCmd(append(paths, m.dest()+":/root/")...).Run()
	os.RemoveAll(tmp)

	sh.C("sshpass", "-e", "ssh", "-q", "-o", "LogLevel=ERROR", "-o", "StrictHostKeyChecking=no", m.dest()).Stdin(migrateRemoteUsers).Quiet().Run()
}

func migrateHomes() []string {
	dirs, _ := filepath.Glob("/home/*")
	sort.Strings(dirs)
	return dirs
}

const migrateContainersFile = "/tmp/docker_containers_names.txt"

func (m *migration) storeRunningContainersForUsers() {
	var b strings.Builder
	for _, dir := range migrateHomes() {
		if !conf.IsDir(dir) {
			continue
		}
		username := filepath.Base(dir)
		if !conf.IsFile(dir + "/docker-compose.yml") {
			continue
		}
		fmt.Printf("Checking podman context for user: %s\n", username)
		containers := podman.User(username, "ps", "--format", "{{.Names}}").NoErr().Str()
		if containers != "" {
			b.WriteString(username + ": " + strings.TrimSuffix(strings.ReplaceAll(containers, "\n", " "), " ") + "\n")
		} else {
			b.WriteString(username + ": no containers\n")
		}
	}
	os.WriteFile(migrateContainersFile, []byte(b.String()), 0o644)
	m.rsyncCmd(migrateContainersFile, m.dest()+":"+migrateContainersFile).Run()
}

func (m *migration) restoreRunningContainersForAllUsers() {
	data := conf.Read(migrateContainersFile)
	total := strings.Count(data, "\n")
	current := 0
	for _, line := range text.Lines(data) {
		current++
		username, containers, _ := strings.Cut(line, ":")
		username = text.Xargs(username)
		if username == "" || strings.Contains(containers, "no containers") {
			continue
		}
		fmt.Printf("Starting containers for context: %s (%d/%d)...\n", username, current, total)
		m.ssh("remote_uid=$(stat -c '%u' /home/" + username + "); CONTAINER_HOST=unix:///hostfs/run/user/${remote_uid}/podman/podman.sock podman-compose -f /home/" + username + "/docker-compose.yml down >/dev/null 2>&1; CONTAINER_HOST=unix:///hostfs/run/user/${remote_uid}/podman/podman.sock podman-compose -f /home/" + username + "/docker-compose.yml up -d " + containers).StdinReader(os.Stdin).Run()
	}
}

func (m *migration) setupRemotePodmanForAllUsers() {
	// context resolution is dynamic per user uid under podman -- nothing to register, no per-user AppArmor (that was rootlesskit, podman doesn't use it), no /run/user/* to rsync (ephemeral, recreated by systemd), and ~/.config/containers/*.conf already rides along with the home dir rsync elsewhere in this script
	var b strings.Builder
	for _, l := range conf.Lines("/etc/passwd") {
		if migrateUIDRange(l) {
			f := strings.Split(l, ":")
			b.WriteString(f[0] + ":" + f[2] + "\n")
		}
	}
	os.WriteFile("/tmp/userlist.txt", []byte(b.String()), 0o644)
	lines := text.Lines(b.String())
	total := len(lines)
	for i, l := range lines {
		username, _, _ := strings.Cut(l, ":")
		if conf.IsDir("/home/" + username + "/.config/containers") {
			fmt.Printf("Setting linger for: %s\n", username)
			if m.ssh("loginctl enable-linger "+username).Quiet().Run() != nil {
				fmt.Printf("Failed to enable linger for %s\n", username)
			}
			fmt.Printf("Enabling rootless podman for: %s (%d/%d) ...\n", username, i+1, total)
			// wait for user@uid before systemctl --user, enable-linger returns before it's up
			u := username
			if m.ssh("uid=$(id -u "+u+"); for i in $(seq 30); do systemctl is-active user@$uid.service >/dev/null 2>&1 && break; sleep 1; done; systemctl --user -M "+u+"@ daemon-reload; systemctl --user -M "+u+"@ reset-failed podman.socket; systemctl --user -M "+u+"@ enable --now podman.socket; systemctl --user -M "+u+"@ is-active podman.socket").Quiet().Run() != nil {
				fmt.Printf("Failed to enable podman.socket for %s\n", username)
			}
		} else {
			fmt.Printf("No .config/containers directory for %s, skipping.\n", username)
		}
		fmt.Printf("[OK] Context %s processed\n", username)
		fmt.Println("")
	}
}

func (m *migration) restartServicesOnTarget() {
	fmt.Printf("Restarting services on %s server ...\n", m.host)
	// same root services as on this server
	out := sh.C("podman", "ps", "--filter", "label=io.podman.compose.project=root", "--format", `{{ index .Labels "com.docker.compose.service" }}`).NoErr().Str()
	seen := map[string]bool{}
	var svcs []string
	for _, s := range strings.Split(out, "\n") {
		if !seen[s] && out != "" {
			seen[s] = true
			svcs = append(svcs, s)
		}
	}
	sort.Strings(svcs)
	rootServices := ""
	for _, s := range svcs {
		rootServices += s + " "
	}
	if rootServices == "" {
		rootServices = "openpanel_mysql openpanel_redis openpanel bind9 caddy openadmin_ftp"
	}
	// the synced compose file changes the config hash, podman-compose then downs the whole stack itself and
	// openpanel's removal can leave a storage-only container behind that blocks the name, so down + clean up first
	script := `cd /root && podman-compose down >/dev/null 2>&1
for d in /var/lib/containers/storage/overlay/*/merged; do
    mountpoint -q "$d" || [ -z "$(ls -A "$d" 2>/dev/null)" ] || rm -rf "$d"
done
podman ps -a --external --format '{{.ID}} {{.Status}}' | awk '$2=="Storage" {print $1}' | xargs -r -n1 podman rm -f --storage >/dev/null 2>&1
podman-compose up -d ` + rootServices + ` >/dev/null 2>&1
sleep 5
podman exec openpanel_dns rndc reconfig >/dev/null 2>&1
podman exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1
systemctl restart admin >/dev/null 2>&1
for c in ` + rootServices + `; do
    n=$(podman ps -a --filter label=com.docker.compose.service=$c --filter label=io.podman.compose.project=root --format '{{.Names}} {{.State}}' | head -1)
    [[ "$n" == *running* ]] || echo "[!] Service $c is not running on destination: ${n:-missing}"
done
`
	restartOut, _ := sh.C("sshpass", "-e", "ssh", "-o", "StrictHostKeyChecking=no", m.dest(), "bash", "-s").Stdin(script).Combined()
	if restartOut != "" {
		fmt.Println(restartOut)
	}
	for _, l := range strings.Split(restartOut, "\n") {
		if strings.HasPrefix(l, "[!] ") {
			m.errors = append(m.errors, strings.TrimPrefix(l, "[!] "))
		}
	}

	if m.composeStartMail {
		fmt.Printf("Starting mailserver and webmail on %s server ...\n", m.host)
		m.ssh("cd /usr/local/mail/openmail && podman-compose up -d mailserver roundcube >/dev/null 2>&1").StdinReader(os.Stdin).Run()
	}

	//todo: ftp, clamav
}

func (m *migration) refreshQuotas() {
	fmt.Printf("Recalculating disk and inodes usage for all users on %s ...\n", m.host)
	m.ssh("opencli user-quota >/dev/null 2>&1").StdinReader(os.Stdin).Run()
}

// whole ip only, a plain s/1.2.3.4/ also hits 11.2.3.45 and 1x2x3x4
func (m *migration) ipSedExpr() string {
	return fmt.Sprintf(`s/(^|[^0-9.])%s\b/\1%s/g`, strings.ReplaceAll(m.currentIP, ".", `\.`), m.remoteIP)
}

// shellQuote quotes for the remote shell, it stands in for bash's printf %q
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (m *migration) replaceIPInZones() {
	if m.currentIP == "" || m.currentIP == m.remoteIP {
		return
	}
	script := `for ZONE_CONF in /etc/bind/zones/*.zone; do
    if [ -f "$ZONE_CONF" ]; then
        domain=$(basename "$ZONE_CONF" .zone)
        sed -E -i ` + shellQuote(m.ipSedExpr()) + ` "$ZONE_CONF"
        echo "Updated DNS zone for domain $domain - $ZONE_CONF"
    fi
done
`
	sh.C("sshpass", "-e", "ssh", "-o", "StrictHostKeyChecking=no", m.dest(), "bash", "-s").Stdin(script).Run()
}

// synced configs still bind to the source ip, sentinel then fails to recreate the stack on the new server
func (m *migration) replaceIPInConfigs() {
	if m.currentIP == "" || m.currentIP == m.remoteIP {
		return
	}
	fmt.Printf("Replacing %s with %s in stack and Caddy configuration ...\n", m.currentIP, m.remoteIP)
	m.ssh("for f in /root/docker-compose.yml /root/.env /etc/openpanel/caddy/Caddyfile /etc/openpanel/caddy/redirects.conf /etc/openpanel/openpanel/conf/openpanel.config; do [ -f $f ] && sed -E -i " + shellQuote(m.ipSedExpr()) + " $f; done").Run()
}

func (m *migration) run() {
	db.Require()

	sh.C("ssh-keygen", "-f", "/root/.ssh/known_hosts", "-R", m.host).Quiet().Run()
	m.checkInstallSshpass()
	m.getServerIPv4()
	m.getRemoteIPv4()
	m.refuseMigratingToSelf()
	m.checkDiskUsedOnSource()
	m.checkIfDestHasSpace()

	if !m.force {
		m.getUsersCountOnDestination()
	}

	if !m.exUsers {
		fmt.Println("Creating system users on remote server ...")
		m.copyUserAccounts()
	}

	if !m.exHome {
		fmt.Println("Syncing files (/home directory) ...")
		// podman image/container state points at the source's shared image store layers and won't start on a new server, containers get recreated from volumes
		out, err := m.rsyncCmd("--include=/*/docker-data/volumes/", "--exclude=/*/docker-data/*", "--exclude=/*/sockets/*/*.sock", "--exclude=/*/sockets/*/*.pid", "/home/", m.dest()+":/home/").Combined()
		fmt.Println(out)
		if err == nil {
			fmt.Println("[OK] Files have been copied to the remote server.")
			fmt.Println("")
		} else {
			m.errors = append(m.errors, "home directory rsync failed")
			fmt.Println("[ERROR] Rsync failed! Output:")
			fmt.Println(out)
			fmt.Printf("FATAL ERROR: Syncing /home to %s failed.\n", m.host)
			cli.Exit(1)
		}
	}

	if !m.exContexts {
		fmt.Println("Enabling rootless podman for all users ...")
		m.setupRemotePodmanForAllUsers()
	}

	m.ssh("systemctl daemon-reload").StdinReader(os.Stdin).Run()

	if !m.exLogs {
		fmt.Println("Syncing /var/log/openpanel ...")
		m.rsyncCmd("/var/log/openpanel/", m.dest()+":/var/log/openpanel/").Run()

		fmt.Println("Syncing /var/log/caddy/ ...")
		m.rsyncCmd("/var/log/caddy/", m.dest()+":/var/log/caddy/").Run()
	}

	if !m.exMail {
		var keys []string
		for _, l := range conf.Lines(conf.Panel) {
			if strings.HasPrefix(l, "key=") {
				keys = append(keys, l[4:])
			}
		}
		if strings.Join(keys, "\n") != "" {
			if conf.IsDir("/usr/local/mail/openmail") {
				fmt.Println("Syncing /usr/local/mail/openmail ...")
				m.rsyncCmd("/usr/local/mail/openmail/", m.dest()+":/usr/local/mail/openmail/").Run()
				m.composeStartMail = true
			}

			var locs []string
			for _, l := range conf.Lines(conf.AdminINI) {
				if strings.HasPrefix(l, "email_storage_location=") {
					locs = append(locs, strings.TrimPrefix(l, "email_storage_location="))
				}
			}
			store := text.Xargs(strings.Join(locs, "\n"))
			if strings.HasPrefix(store, "/") && conf.IsDir(store) {
				s := strings.TrimSuffix(store, "/")
				fmt.Printf("Syncing mailboxes from %s/ ...\n", s)
				m.ssh("mkdir -p " + s).StdinReader(os.Stdin).Run()
				m.rsyncCmd(s+"/", m.dest()+":"+s+"/").Run()
				m.composeStartMail = true
			}
		}
	}

	if !m.exCSF {
		fmt.Println("Syncing /etc/csf/ ...")
		m.rsyncCmd("/etc/csf/", m.dest()+":/etc/csf/").Run()
		m.ssh("csf -a " + m.currentIP + " > /dev/null && csf -r >/dev/null && systemctl restart lfd").StdinReader(os.Stdin).Run()
	}

	if !m.exBind {
		fmt.Println("Syncing /etc/bind ...")
		m.rsyncCmd("/etc/bind/", m.dest()+":/etc/bind/").Run()
		m.replaceIPInZones()
	}

	if !m.exOpenpanel {
		fmt.Println("Syncing /etc/openpanel ...")
		// the cluster identity stays here, a copy would make the new server a second master with the same key
		m.rsyncCmd("--exclude=openadmin/cluster/", "/etc/openpanel/", m.dest()+":/etc/openpanel/").Run()
		m.replaceIPInConfigs()

		fmt.Println("Syncing system cronjobs...")
		m.rsyncCmd("/etc/cron.d/openpanel", m.dest()+":/etc/cron.d/").Run()
	}

	if !m.exMysql {
		// dump + import instead of rsyncing the live volume, copying innodb files under a running server gives a corrupt copy
		fmt.Println("Exporting panel databases ...")
		dumpFile := "/root/openpanel_migrate_dump.sql"
		dumped := false
		if f, err := os.Create(dumpFile); err == nil {
			ef, _ := os.Create("/tmp/migrate_dump.err")
			c := sh.C("podman", "exec", "openpanel_mysql", "sh", "-c", `mariadb-dump -uroot -p"$MYSQL_ROOT_PASSWORD" --all-databases --single-transaction --routines --events --triggers --flush-privileges`).Stdout(f)
			if ef != nil {
				c.Stderr(ef)
			} else {
				c.NoErr()
			}
			dumped = c.Run() == nil
			f.Close()
			if ef != nil {
				ef.Close()
			}
		}
		if dumped {
			m.rsyncCmd(dumpFile, m.dest()+":"+dumpFile).Run()
			fmt.Printf("Importing panel databases on %s ...\n", m.host)
			if m.ssh("podman exec -i openpanel_mysql sh -c 'mariadb -uroot -p\"$MYSQL_ROOT_PASSWORD\"' < "+dumpFile+"; rc=$?; rm -f "+dumpFile+"; exit $rc").StdinReader(os.Stdin).Run() == nil {
				fmt.Println("[OK] Panel databases imported.")
			} else {
				m.errors = append(m.errors, "panel database import failed")
				fmt.Println("[ERROR] Importing panel databases on destination failed.")
			}
		} else {
			m.errors = append(m.errors, "panel database export failed")
			fmt.Printf("[ERROR] Could not dump panel databases: %s\n", strings.TrimRight(conf.Read("/tmp/migrate_dump.err"), "\n"))
		}
		os.Remove(dumpFile)
		os.Remove("/tmp/migrate_dump.err")
	}

	if !m.exStack {
		fmt.Println("Syncing /root/docker-compose.yml and /root/.env ...")
		m.rsyncCmd("/root/docker-compose.yml", m.dest()+":/root/").Run()
		m.rsyncCmd("/root/.env", m.dest()+":/root/").Run()
	}

	m.replaceIPInConfigs()

	if !m.exPostupdate {
		if conf.Exists("/root/openpanel_run_after_update") {
			fmt.Println("Syncing /root/openpanel_run_after_update ...")
			m.rsyncCmd("/root/openpanel_run_after_update", m.dest()+":/root/").Run()
		}
	}

	// quotas come from the plans in the panel db, so only after it's imported
	fmt.Println("Restoring user quotas ...")
	m.ssh("opencli user-quota --update --all").StdinReader(os.Stdin).Run()

	m.storeRunningContainersForUsers()      // export running contianers on source and copy to dest
	m.restoreRunningContainersForAllUsers() // start containers per context on dest
	m.restartServicesOnTarget()             // restart openpanel, webserver and admin on dest
	m.refreshQuotas()                       // recalculate users usage on dest

	m.sendMigrationSummary()
	fmt.Println("[OK] Sync complete")
	fmt.Printf("SUCCESS: Migration to %s completed.\n", m.host)
}

// what got moved, for the log and the destination's notifications
func (m *migration) sendMigrationSummary() {
	q := func(sql string) string { return db.Cmd(sql, "-N", "-s").NoErr().Str() }
	users := q("SELECT GROUP_CONCAT(username ORDER BY username SEPARATOR ', ') FROM users;")
	userCount := q("SELECT COUNT(*) FROM users;")
	domainCount := q("SELECT COUNT(*) FROM domains;")
	siteCount := q("SELECT COUNT(*) FROM sites;")

	// grep -c || echo 0 prints 0 twice when the file exists without a match, kept as is
	mailCount := "0"
	if b, err := os.ReadFile("/usr/local/mail/openmail/docker-data/dms/config/postfix-accounts.cf"); err == nil {
		n := 0
		for _, l := range text.Lines(string(b)) {
			if strings.Contains(l, "|") {
				n++
			}
		}
		mailCount = strconv.Itoa(n)
		if n == 0 {
			mailCount = "0\n0"
		}
	}
	ftp := 0
	lists, _ := filepath.Glob("/etc/openpanel/ftp/users/*/users.list")
	var all strings.Builder
	for _, f := range lists {
		all.WriteString(conf.Read(f))
	}
	for _, l := range text.Lines(all.String()) {
		if strings.Contains(l, "|") {
			ftp++
		}
	}
	homeSize, _, _ := strings.Cut(sh.C("du", "-sh", "/home").NoErr().Str(), "\t")
	elapsed := int(time.Since(m.start).Seconds())

	var skipped []string
	add := func(on bool, s string) {
		if on {
			skipped = append(skipped, s)
		}
	}
	add(m.exUsers, "system users")
	add(m.exHome, "home directories")
	add(m.exContexts, "podman contexts")
	add(m.exLogs, "logs")
	add(m.exMail, "mail")
	add(m.exCSF, "csf")
	add(m.exBind, "dns zones")
	add(m.exOpenpanel, "/etc/openpanel")
	add(m.exMysql, "panel database")
	add(m.exStack, "docker stack")
	add(m.exPostupdate, "post-update script")

	status := "completed"
	if len(m.errors) > 0 {
		status = fmt.Sprintf("completed with %d error(s)", len(m.errors))
	}
	title := "Server migrated from " + m.currentIP
	or := func(v, d string) string {
		if v == "" {
			return d
		}
		return v
	}
	message := fmt.Sprintf("Migration from %s %s in %dm %ds. Users (%s): %s. Domains: %s. Websites: %s. Email accounts: %s. FTP accounts: %d. Home directories: %s.",
		m.currentIP, status, elapsed/60, elapsed%60, userCount, or(users, "none"), or(domainCount, "0"), or(siteCount, "0"), mailCount, ftp, homeSize)
	if len(skipped) > 0 {
		message += " Skipped: " + strings.Join(skipped, ", ") + "."
	}
	if len(m.errors) > 0 {
		message += " Errors: " + strings.Join(m.errors, "; ") + "."
	}

	fmt.Println("")
	fmt.Println(message)
	fmt.Printf("Log file: %s\n", m.logFile)

	// quoted for the remote shell, the message has spaces and parentheses
	m.ssh("nohup opencli sentinel --action=user_transfer --title=" + shellQuote(title) + " --message=" + shellQuote(message) + " >/dev/null 2>&1 &").Run()
}
