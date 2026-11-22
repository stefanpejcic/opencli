// ##############################################################################
// Script Name: internal/commands/general/report.go
// Description: Generate a system report and send it to OpenPanel support team.
// Usage: opencli report [--non-interactive] [--user <USERNAME>]
// Author: Stefan Pejcic
// Created: 07.10.2023
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

package general

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "report",
		Desc:  "Generate a system report and send it to OpenPanel support team.",
		Usage: "opencli report [--non-interactive] [--user <USERNAME>]",
		Run:   report,
	})
}

const (
	reportTimeout  = 10 * time.Second
	reportLogLines = 50
	reportSep      = "# ======================================================================"
)

func reportUsage() {
	fmt.Println("Usage: opencli report [--non-interactive] [--user <USERNAME>]")
	fmt.Println("")
	fmt.Println("  --non-interactive           no progress messages, screen clearing or colors")
	fmt.Println("  --user <USERNAME>           also collect containers, logs and config for this user")
}

var (
	redactRe = regexp.MustCompile(`^[[:space:]"-]*[a-z0-9_.]*(pass|secret|token|key|pwd)[a-z0-9_.]*"?[[:space:]]*[=:][[:space:]]*`)
	plainRe  = regexp.MustCompile(`^"?(yes|no|on|off|true|false|[0-9]+)?"?$`)
)

func init() { redactRe.Longest() }

// masks values of password/secret/token/key lines but keeps plain yes/no/number values so settings stay readable
func redact(data string) string {
	var b strings.Builder
	data = strings.TrimSuffix(data, "\n")
	if data == "" {
		return ""
	}
	for _, l := range strings.Split(data, "\n") {
		lower := strings.ToLower(l)
		if loc := redactRe.FindStringIndex(lower); loc != nil {
			val := l[loc[1]:]
			if val != "" && !plainRe.MatchString(strings.ToLower(val)) {
				b.WriteString(l[:loc[1]] + "***REDACTED***\n")
				continue
			}
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}

type reportSection struct{ bytes.Buffer }

func (s *reportSection) section(name string) { s.WriteString("=== " + name + " ===\n") }

func (s *reportSection) run(cmd, label string) {
	s.WriteString("# " + label + ":\n$ " + cmd + "\n")
	if file, ok := strings.CutPrefix(cmd, "redact < "); ok {
		if b, err := os.ReadFile(file); err == nil {
			s.WriteString(redact(string(b)))
		} else {
			s.WriteString("bash: line 1: " + file + ": No such file or directory\n")
		}
	} else {
		var mu sync.Mutex
		w := &lockedWriter{w: &s.Buffer, mu: &mu}
		sh.C("bash", "-c", cmd).Stdout(w).Stderr(w).Timeout(reportTimeout).Run()
	}
	s.WriteString(reportSep + "\n\n")
}

type lockedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func pct(s string) float64 {
	m := regexp.MustCompile(`^[+-]?[0-9]*\.?[0-9]*`).FindString(strings.TrimSpace(s))
	f, _ := strconv.ParseFloat(m, 64)
	return f
}

// df output rows over 90% use
func reportDf(flag, format string) []string {
	out := sh.C("df", flag, "-x", "tmpfs", "-x", "devtmpfs", "-x", "overlay", "-x", "squashfs").NoErr().Str()
	var res []string
	for i, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if i == 0 || len(f) == 0 {
			continue
		}
		get := func(n int) string {
			if n < len(f) {
				return f[n]
			}
			return ""
		}
		if pct(get(4)) >= 90 {
			res = append(res, fmt.Sprintf(format, get(5), get(4)))
		}
	}
	return res
}

func reportQuick(s *reportSection) {
	s.section("Quick Checks (possible problems)")
	var out []string
	out = append(out, reportDf("-hP", "[!] disk %s is %s full")...)
	out = append(out, reportDf("-iP", "[!] inodes on %s are %s used")...)

	var total, avail float64
	for _, l := range conf.Lines("/proc/meminfo") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		if strings.Contains(l, "MemTotal") {
			total, _ = strconv.ParseFloat(f[1], 64)
		}
		if strings.Contains(l, "MemAvailable") {
			avail, _ = strconv.ParseFloat(f[1], 64)
		}
	}
	if total != 0 && avail != 0 && avail*100/total < 10 {
		out = append(out, fmt.Sprintf("[!] only %d%% RAM available", int64(avail*100/total)))
	}

	if f := strings.Fields(conf.Read("/proc/loadavg")); len(f) > 1 {
		c := runtime.NumCPU()
		if l5, _ := strconv.ParseFloat(f[1], 64); l5 > float64(c*2) {
			out = append(out, fmt.Sprintf("[!] 5min load %s is high for %d cpus", f[1], c))
		}
	}

	for _, l := range strings.Split(sh.C("systemctl", "--failed", "--no-legend", "--plain").NoErr().Str(), "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			out = append(out, "[!] failed unit: "+f[0])
		} else if l != "" {
			out = append(out, "[!] failed unit: ")
		}
	}
	for _, svc := range []string{"admin", "podman.socket", "csf"} {
		if !sh.C("systemctl", "list-unit-files", svc+"*").Quiet().OK() {
			continue
		}
		if !sh.C("systemctl", "is-active", "--quiet", svc).OK() {
			out = append(out, "[!] service "+svc+" is not active")
		}
	}
	ps, _ := sh.C("podman", "ps", "-a", "--format", "{{.Names}} {{.State}}").NoErr().Timeout(reportTimeout).Out()
	for _, l := range strings.Split(ps, "\n") {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		state := ""
		if len(f) > 1 {
			state = f[1]
		}
		if state != "running" {
			out = append(out, "[!] container "+f[0]+" is "+state)
		}
	}
	oom := 0
	kern := sh.C("journalctl", "-k", "--since", "-7 days", "--no-pager").NoErr().Str()
	oomRe := regexp.MustCompile(`(?i)out of memory|oom-kill`)
	for _, l := range strings.Split(kern, "\n") {
		if oomRe.MatchString(l) {
			oom++
		}
	}
	if oom > 0 {
		out = append(out, fmt.Sprintf("[!] %d OOM kills in the last 7 days", oom))
	}
	if !conf.IsFile("/root/docker-compose.yml") {
		out = append(out, "[!] /root/docker-compose.yml is missing")
	}
	if !conf.IsFile(conf.Panel) {
		out = append(out, "[!] openpanel.config is missing")
	}
	var kept []string
	for _, l := range out {
		if l != "" {
			kept = append(kept, l)
		}
	}
	if len(kept) > 0 {
		s.WriteString(strings.Join(kept, "\n") + "\n")
	} else {
		s.WriteString("No problems detected.\n")
	}
	s.WriteString("\n")
}

func reportOS(s *reportSection) {
	s.section("System")
	s.run("date; timedatectl 2>/dev/null | grep -E 'Time zone|synchronized'", "Date and time")
	s.run("hostname -f", "Hostname")
	s.run("grep -E '^(PRETTY_NAME|VERSION_ID)=' /etc/os-release; uname -srm", "OS and kernel")
	s.run("systemd-detect-virt", "Virtualization")
	s.run("nproc; grep -m1 'model name' /proc/cpuinfo", "CPU")
	s.run("uptime", "Uptime and load")
	s.run("free -h", "Memory and swap")
	s.run("df -hT -x tmpfs -x devtmpfs -x overlay -x squashfs", "Disk usage")
	s.run("df -i -x tmpfs -x devtmpfs -x overlay -x squashfs", "Inode usage")
	s.run("ps -eo pid,user,%cpu,%mem,etime,comm --sort=-%cpu | head -15", "Top processes by CPU")
	s.run("ps -eo pid,user,%cpu,%mem,etime,comm --sort=-%mem | head -15", "Top processes by memory")
	s.run("journalctl -k --since '-7 days' --no-pager | grep -iE 'out of memory|oom-kill' | tail -20", "OOM kills (last 7 days)")
	s.run("systemctl --failed --no-pager", "Failed systemd units")
}

func reportNetwork(s *reportSection) {
	s.section("Network")
	s.run("ip -br addr", "IP addresses")
	s.run("ss -tulpn", "Listening ports")
	s.run("grep -v '^#' /etc/resolv.conf", "DNS resolvers")
	s.run("getent hosts openpanel.com || echo 'DNS resolution failed'", "DNS lookup test")
	s.run("curl -sS -o /dev/null -m 5 -w '%{http_code} in %{time_total}s\\n' https://hub.docker.com", "Outbound HTTPS test")
}

func reportVersions(s *reportSection) {
	s.section("Versions")
	s.run("opencli --version", "OpenPanel version")
	s.run("podman --version; podman-compose --version 2>&1 | tail -1", "Podman versions")
	s.run("mariadb --protocol=tcp --version", "MariaDB client version")
	s.run("csf -v", "Sentinel Firewall (CSF) version")
	s.run("podman images --format 'table {{.Repository}}:{{.Tag}} {{.ID}} {{.Created}}'", "Host images")
}

func reportServices(s *reportSection) {
	s.section("Services")
	s.run("cd /root && podman-compose ps", "OpenPanel stack")
	s.run("podman ps -a --format 'table {{.Names}}\\t{{.Image}}\\t{{.Status}}\\t{{.Ports}}'", "Host containers")
	s.run("podman stats --no-stream", "Host container resource usage")
	s.run("systemctl status admin --no-pager -n 20", "OpenAdmin service")
	s.run("systemctl status podman.socket --no-pager -n 10", "Podman socket")
	s.run("systemctl status csf --no-pager -n 10", "Sentinel Firewall (CSF)")
}

func reportSettings(s *reportSection) {
	s.section("Configuration (secrets redacted)")
	s.run("redact < /etc/openpanel/openpanel/conf/openpanel.config", "openpanel.config")
	s.run("redact < /etc/openpanel/openadmin/config/admin.ini", "admin.ini")
	s.run("redact < /etc/openpanel/caddy/Caddyfile", "Caddyfile")
	s.run("redact < /root/.env", "/root/.env")
}

func reportLogs(s *reportSection) {
	n := strconv.Itoa(reportLogLines)
	s.section("Logs (last " + n + " lines)")
	s.run("tail -n "+n+" /var/log/openpanel/admin/error.log", "OpenAdmin error log")
	s.run("tail -n "+n+" /var/log/openpanel/admin/notifications.log", "Notifications")
	for _, c := range []string{"openpanel", "caddy", "openpanel_mysql", "openpanel_redis"} {
		s.run("podman logs --tail "+n+" "+c, "Container "+c)
	}
	s.run(`f=$(ls -t /var/log/openpanel/updates/*.log | head -1) && echo "$f" && tail -n `+n+` "$f"`, "Latest update log")
}

func dirUID(p string) string {
	st, err := os.Stat(p)
	if err != nil {
		return ""
	}
	return strconv.Itoa(int(st.Sys().(*syscall.Stat_t).Uid))
}

func reportUsers(s *reportSection) {
	s.section("Users")
	s.run("opencli user-list --local --total", "Number of users")
	s.WriteString("# Containers per user (running/total):\n")
	dirs, _ := filepath.Glob("/home/*")
	for _, dir := range dirs {
		if !conf.IsFile(dir + "/docker-compose.yml") {
			continue
		}
		user := filepath.Base(dir)
		sock := "/hostfs/run/user/" + dirUID(dir) + "/podman/podman.sock"
		if st, err := os.Stat(sock); err != nil || st.Mode()&os.ModeSocket == 0 {
			s.WriteString(user + ": no podman socket at " + sock + "\n")
			continue
		}
		out, _ := sh.C("podman", "--remote", "ps", "-a", "--format", "{{.Names}} {{.State}}").Env("CONTAINER_HOST=unix://" + sock).
			Timeout(5 * time.Second).ErrToOut().Out()
		r, t, bad := 0, 0, ""
		if out != "" {
			for _, l := range strings.Split(out, "\n") {
				t++
				f := strings.Fields(l)
				get := func(n int) string {
					if n < len(f) {
						return f[n]
					}
					return ""
				}
				if get(1) == "running" {
					r++
				} else {
					bad += " " + get(0) + "(" + get(1) + ")"
				}
			}
		}
		if bad != "" {
			bad = " not running:" + bad
		}
		fmt.Fprintf(s, "%s: %d/%d%s\n", user, r, t, bad)
	}
	s.WriteString(reportSep + "\n\n")
}

func reportUserDetails(s *reportSection, user string) {
	if user == "" {
		return
	}
	uid := dirUID("/home/" + user)
	host := "CONTAINER_HOST=unix:///hostfs/run/user/" + uid + "/podman/podman.sock"
	s.section("User: " + user)
	s.run("opencli user-list --local --json | jq '.data[] | select(.username==\""+user+"\")'", "Account info")
	s.run("opencli domains-user "+user+" --docroot", "Domains")
	s.run("id "+user+"; loginctl show-user "+user+" -p State -p Linger", "System user and linger")
	s.run("systemctl status user@"+uid+" --no-pager -n 10", "User systemd instance")
	s.run(host+" podman --remote ps -a --format 'table {{.Names}}\\t{{.Image}}\\t{{.Status}}\\t{{.Ports}}'", "Containers")
	s.run(host+" podman --remote stats --no-stream", "Container resource usage")
	s.run("redact < /home/"+user+"/.env", ".env (secrets redacted)")
	s.run("redact < /home/"+user+"/docker-compose.yml", "docker-compose.yml (secrets redacted)")
	names, _ := sh.C("podman", "--remote", "ps", "-a", "--format", "{{.Names}}").Env("CONTAINER_HOST=unix:///hostfs/run/user/" + uid + "/podman/podman.sock").
		Timeout(5 * time.Second).NoErr().Out()
	for _, c := range strings.Fields(names) {
		s.run(host+" podman --remote logs --tail "+strconv.Itoa(reportLogLines)+" "+c, "Container "+c+" logs")
	}
}

func report(args []string) int {
	lock, err := os.OpenFile("/tmp/opencli_report.lock", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err == nil {
		defer lock.Close()
		if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			fmt.Println("Error: Another instance of the report script is already running. Exiting.")
			return 0
		}
	}
	// every exit inside the flock subshell still ended the script with exit 0
	nonInteractive, user := false, ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--non-interactive":
			nonInteractive = true
		// kept so older commands still work, reports are not uploaded anymore
		case a == "--public" || a == "--link" || a == "--upload":
		case a == "--user":
			user = cli.Arg(args, i+1)
			i++
		case strings.HasPrefix(a, "--user="):
			user = strings.TrimPrefix(a, "--user=")
		case a == "-h" || a == "--help":
			reportUsage()
			return 0
		default:
			fmt.Println("Unknown option: " + a)
			reportUsage()
			return 0
		}
	}
	if user != "" && !conf.IsDir("/home/"+user) {
		fmt.Printf("Error: user %s does not exist (no /home/%s).\n", user, user)
		return 0
	}

	outDir := "/var/log/openpanel/admin/reports"
	os.MkdirAll(outDir, 0o700)
	os.Chmod(outDir, 0o700)
	outFile := outDir + "/system_info_" + time.Now().Format("20060102150405") + ".txt"
	os.WriteFile(outFile, nil, 0o600)

	if !nonInteractive {
		fmt.Println("Collecting system information...")
	}
	head := "OpenPanel system report\nGenerated: " + time.Now().UTC().Format("2006-01-02 15:04:05") + " UTC\nCommand: opencli report " + strings.Join(args, " ") + "\n\n"

	// order here is the order sections appear in the report
	funcs := []func(*reportSection){
		reportQuick,
		reportVersions,
		reportOS,
		reportServices,
		func(s *reportSection) { reportUserDetails(s, user) },
		reportUsers,
		reportLogs,
		reportNetwork,
		reportSettings,
	}
	parts := make([]reportSection, len(funcs))
	var wg sync.WaitGroup
	for i, f := range funcs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f(&parts[i])
		}()
	}
	wg.Wait()
	body := head
	for i := range parts {
		body += parts[i].String()
	}
	appendFile(outFile, body)

	if st, err := os.Stat(outFile); err != nil || st.Size() == 0 {
		fmt.Println("Information not collected! report file does not exist: " + outFile)
		return 0
	}
	if !nonInteractive {
		sh.C("clear").Run()
	}
	// reports are no longer uploaded anywhere, the admin sends the file to support
	fmt.Println("Information collected successfully. Please provide content of the following file to the support team:\n" + outFile)
	return 0
}
