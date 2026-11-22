// ##############################################################################
// Script Name: internal/commands/useracct/transfer.go
// Description: Transfers a single user account from this server to another.
// Usage: opencli user-transfer --account <OPENPANEL_USER> --host <DESTINATION_IP> --username <DESTINATION_SSH_USERNAME> --password <DESTINATION_SSH_PASSWORD> [--port <SSH_PORT>] [--force] [--live-transfer]
// Author: Stefan Pejcic
// Created: 28.06.2025
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

package useracct

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
		Name:  "user-transfer",
		Desc:  "Transfers a single user account from this server to another.",
		Usage: "opencli user-transfer --account <OPENPANEL_USER> --host <DESTINATION_IP> --username <DESTINATION_SSH_USERNAME> --password <DESTINATION_SSH_PASSWORD> [--port <SSH_PORT>] [--force] [--live-transfer]",
		Run:   transfer,
	})
}

type transferJob struct {
	username, host, user, port, pass, identity, knownHosts string
	force, live                                            bool
	start                                                  time.Time
	logFile                                                string
	rsyncCmd, sshCmd                                       []string
	currentIP, remoteKey, keyValue                         string
	context, planID                                        string
	finalSync, startMail                                   bool
	nameservers                                            []string
}

// bashHeredoc expands an unquoted heredoc body: \$ \` \\ escapes, line continuations and $VAR/${VAR} from vars
func bashHeredoc(tpl string, vars map[string]string) string {
	s := strings.ReplaceAll(strings.TrimPrefix(tpl, "\n"), "´", "`")
	var b strings.Builder
	isName := func(c byte) bool {
		return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && strings.IndexByte("$`\\", s[i+1]) >= 0:
			b.WriteByte(s[i+1])
			i++
		case c == '\\' && i+1 < len(s) && s[i+1] == '\n':
			i++
		case c == '$' && i+1 < len(s) && s[i+1] == '{':
			end := strings.IndexByte(s[i:], '}')
			b.WriteString(vars[s[i+2:i+end]])
			i += end
		case c == '$' && i+1 < len(s) && isName(s[i+1]) && !(s[i+1] >= '0' && s[i+1] <= '9'):
			j := i + 1
			for j < len(s) && isName(s[j]) {
				j++
			}
			b.WriteString(vars[s[i+1:j]])
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// bashQuote is printf %q for the plain text the notification uses
func bashQuote(s string) string {
	if s == "" {
		return "''"
	}
	var b strings.Builder
	for i, r := range s {
		switch {
		case strings.ContainsRune(" \t'\"\\|&;()<>!{}*[?]^$`", r), (r == '~' || r == '#') && i == 0:
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (t *transferJob) log(msg string) {
	line := "[" + time.Now().Format("2006-01-02 15:04:05") + "] " + msg + "\n"
	fmt.Print(line)
	appendFile(t.logFile, line)
}

// logpipe logs every line of r with two spaces in front, a last line without newline is dropped like read did
func (t *transferJob) logpipe(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			return
		}
		t.log("  " + strings.TrimSuffix(l, "\n"))
	}
}

// runLogged runs c with stdout and stderr through logpipe, like `cmd 2>&1 | logpipe`
func (t *transferJob) runLogged(c *sh.Cmd) {
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() { t.logpipe(pr); close(done) }()
	c.Stdout(pw).Stderr(pw).Run()
	pw.Close()
	<-done
}

func (t *transferJob) ssh(args ...string) *sh.Cmd {
	return sh.C(t.sshCmd[0], append(append([]string{}, t.sshCmd[1:]...), args...)...).StdinReader(os.Stdin)
}

func (t *transferJob) rsync(args ...string) *sh.Cmd {
	return sh.C(t.rsyncCmd[0], append(append([]string{}, t.rsyncCmd[1:]...), args...)...).StdinReader(os.Stdin)
}

func (t *transferJob) remote(path string) string { return t.user + "@" + t.host + ":" + path }

func (t *transferJob) q(sql string) string {
	out, _ := db.Cmd(sql, "-N", "-s").Out()
	return out
}

func transfer(args []string) int {
	t := &transferJob{user: "root", port: "22", start: time.Now()}
	for i := 0; i < len(args); {
		a := args[i]
		val := cli.Arg(args, i+1)
		switch a {
		case "-h", "--host":
			t.host = val
		case "-u", "--username":
			t.user = val
		case "--account":
			t.username = val
		case "--password":
			t.pass = val
		case "--port":
			t.port = val
		case "--identity":
			t.identity = val
		case "--known-hosts":
			t.knownHosts = val
		case "--force":
			t.force = true
			i++
			continue
		case "--live-transfer":
			t.live = true
			i++
			continue
		default:
			cli.Fail("[ERROR] Unknown option: %s", a)
		}
		// shift 2 without a value looped forever, a missing value just ends parsing now
		i += 2
	}
	if t.host == "" || t.username == "" {
		cli.Fail("Usage: opencli user-transfer --account <OPENPANEL_USER> --host <DESTINATION_IP> --username <OPENPANEL_USERNAME> --password <DESTINATION_SSH_PASSWORD> [--live-transfer]")
	}
	os.Setenv("SSHPASS", t.pass)

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logDir := "/var/log/openpanel/admin/transfers"
	os.MkdirAll(logDir, 0o777)
	t.logFile = logDir + "/" + filepath.Base(t.username) + "_" + t.host + "_" + timestamp + ".log"
	fmt.Println("Import started, log file: " + t.logFile)

	var lines []string
	for _, l := range conf.Lines(conf.Panel) {
		if strings.HasPrefix(l, "key=") {
			lines = append(lines, l[4:])
		}
	}
	t.keyValue = strings.Join(lines, "\n")

	// MAIN
	db.Require()
	sh.C("ssh-keygen", "-f", "/root/.ssh/known_hosts", "-R", t.host).NoOut().Run()
	t.formatCommands() // creates rsync and sshpass commands, installs sshpass if missing
	t.log("Log file: " + t.logFile)
	t.log("PID: " + strconv.Itoa(os.Getpid()))

	t.serverIPv4()
	t.usersCountOnDestination()

	if text.Atoi(t.usernameExists()) > 0 {
		if !t.force {
			t.log("[✘] Error: Username '" + t.username + "' is already taken on destination server.")
			return 1
		}
		t.log("[!] Warning: Username '" + t.username + "' is already taken on destination server but will be overwritten due to the --force flag.")
	}
	t.domainsLimitOnDestination()
	t.resolveContext()

	t.exportMySQL()
	t.runLogged(t.ssh("bash", "-s").Stdin(t.importMySQLScript()))
	if text.Atoi(t.usernameExists()) < 1 {
		t.log("[✘] ERROR: User " + t.username + " was not created in the destination panel database, aborting.")
		return 1
	}
	t.copyFeatureSet()
	t.copyUserAccount()
	t.remoteNameservers()
	t.rsyncFiles()
	t.log("Importing sites ...")
	t.runLogged(t.ssh("bash", "-s").Stdin(bashHeredoc(trImportSites, map[string]string{"mysql_database": db.Database, "USERNAME": t.username})))
	t.setupRemotePodman()
	t.restoreFTP()
	t.ssh("systemctl daemon-reload").Run()
	t.ssh("opencli user-quota --update " + t.username + " >/dev/null 2>&1").Run()
	t.ssh("mkdir -p /var/log/caddy/stats/ /var/log/caddy/domlogs/ /var/log/caddy/coraza_waf/ /etc/openpanel/caddy/domains/ /etc/bind/zones/ /etc/openpanel/caddy/ssl/certs/ /etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/ /etc/openpanel/caddy/ssl/custom/ /etc/openpanel/openpanel/core/users/").Run()

	// https://github.com/stefanpejcic/opencli/issues/159
	if t.keyValue != "" {
		t.syncMail()
	}

	// logs and stuff
	core := conf.UsersDir + "/" + t.username
	t.rsync(core+"/", t.remote(core)).Run()

	t.storeContainers() // export running containers on source and copy to dest

	if t.live {
		var liveDomains []string
		for _, l := range text.Lines(sh.Opencli("domains-user", t.username).NoErr().Str()) {
			if f := text.Field(l, 1); f != "" {
				liveDomains = append(liveDomains, f)
			}
		}
		// containers have to be stopped first, otherwise the copy misses late writes and live database files are inconsistent
		t.log("Stopping the account on this server for the final sync ...")
		// a failure from here on must not leave the account down on the source
		func() {
			defer func() {
				if r := recover(); r != nil {
					sh.Opencli("user-unsuspend", t.username).Quiet().Run()
					panic(r)
				}
			}()
			if sh.Opencli("user-suspend", t.username, "-y").Quiet().Run() != nil {
				t.log("[!] Warning: suspending " + t.username + " reported an error")
			}
			t.log("Final sync ...")
			t.finalSync = true
			t.rsyncFiles()
		}()
		// suspend replaced the vhosts with its own page, visitors still arriving here go to the new server until dns catches up
		// https://github.com/stefanpejcic/OpenPanel/issues/897
		rp := regexp.MustCompile(`reverse_proxy (https?://)[^ ]+`)
		for _, d := range liveDomains {
			cfile := "/etc/openpanel/caddy/domains/" + d + ".conf"
			if susp := "/etc/openpanel/caddy/suspended_domains/" + d + ".conf"; conf.IsFile(susp) {
				cp(susp, cfile)
				sedFile(cfile, subRe(rp, `reverse_proxy \1`+t.host, true))
			}
			// the copy lets a cancelled move put the zone back
			if zone := "/etc/bind/zones/" + d + ".zone"; conf.IsFile(zone) {
				cp(zone, zone+".pre-move")
				fmt.Println("[LIVE] Updating DNS zone for " + d + " locally")
				if re, err := regexp.Compile(t.currentIP); err == nil {
					sedFile(zone, subRe(re, t.host, true))
				}
				t.updateZoneFile(zone)
			}
		}
		sh.C("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").Quiet().Run()
	}
	os.Remove("/tmp/" + t.context + "_uid_map.txt")
	t.restoreContainers()
	t.restartServicesOnTarget()
	t.log("Recalculating disk and inodes usage for all users on " + t.host + " ...")
	t.ssh("opencli user-quota >/dev/null 2>&1").Run()
	t.notifyDestination()

	elapsed := int64(time.Since(t.start) / time.Second)
	t.log(fmt.Sprintf("Elapsed time: %dh %dm %ds", elapsed/3600, elapsed%3600/60, elapsed%60))
	t.log("SUCCESS: Transfer process for user " + t.username + " completed.")
	return 0
}

func (t *transferJob) formatCommands() {
	opts := []string{"-azHAX", "--numeric-ids"}
	port := t.port
	switch {
	case t.pass != "":
		// a password goes through sshpass for rsync and ssh
		requirement.Command("sshpass")
		t.rsyncCmd = append(append([]string{"sshpass", "-e", "rsync"}, opts...), "-e", "ssh -p "+port+" -o StrictHostKeyChecking=no")
		t.sshCmd = []string{"sshpass", "-e", "ssh", "-p", port, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR", t.user + "@" + t.host}
	case t.identity != "" && t.knownHosts != "":
		// the host key came from the destination itself over the cluster's pinned connection
		t.rsyncCmd = append(append([]string{"rsync"}, opts...), "-e", "ssh -p "+port+" -i "+t.identity+" -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="+t.knownHosts+" -o LogLevel=ERROR")
		t.sshCmd = []string{"ssh", "-p", port, "-i", t.identity, "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + t.knownHosts, "-o", "LogLevel=ERROR", t.user + "@" + t.host}
	case t.identity != "":
		t.rsyncCmd = append(append([]string{"rsync"}, opts...), "-e", "ssh -p "+port+" -i "+t.identity+" -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR")
		t.sshCmd = []string{"ssh", "-p", port, "-i", t.identity, "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR", t.user + "@" + t.host}
	default:
		t.rsyncCmd = append(append([]string{"rsync"}, opts...), "-e", "ssh -p "+port+" -o StrictHostKeyChecking=no")
		t.sshCmd = []string{"ssh", "-p", port, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR", t.user + "@" + t.host} // for ssh keys!
	}

	// csf
	sh.C("csf", "-ta", t.host).Quiet().Run()

	t.log("Testing SSH connection to " + t.user + "@" + t.host + "...")
	if t.ssh("echo 'SSH connection established, starting transfer process..'").Quiet().OK() {
		t.log("SSH connection established, starting transfer process..")
		return
	}
	t.log("[✘] SSH connection to " + t.host + " failed. Please check credentials or SSH keys.")
	t.log("Command attempted:")
	t.log(strings.Join(t.sshCmd, " ") + " echo 'SSH connection established, starting transfer process..'")
	cli.Exit(1)
}

func (t *transferJob) serverIPv4() {
	if ip, ok := get4("https://ip.openpanel.com", time.Second); ok {
		t.currentIP = ip
	} else if ip, ok := get4("https://ifconfig.me", time.Second); ok {
		t.currentIP = ip
	}
	if t.currentIP == "" {
		t.currentIP = ipAddrGlobal()
	}
	ip := t.currentIP
	valid := regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}$`).MatchString(ip) &&
		!regexp.MustCompile(`^10\.`).MatchString(ip) &&
		!regexp.MustCompile(`^172\.(1[6-9]|2[0-9]|3[0-1])\.`).MatchString(ip) &&
		!regexp.MustCompile(`^192\.168\.`).MatchString(ip)
	if !valid {
		t.log("Invalid or private IPv4 address: " + ip + ". OpenPanel requires a public IPv4 address to bind Nginx configuration files.")
	}
}

func (t *transferJob) remoteSQL(sql string) string {
	return "mariadb --defaults-extra-file=" + db.ConfigFile + " -D " + db.Database + " -e \"" + sql + "\" -sN"
}

func (t *transferJob) usersCountOnDestination() {
	key, err := t.ssh("grep '^key=' " + conf.Panel + " | cut -d'=' -f2-").Stdin("").Out()
	if err != nil {
		t.log("[✘] ERROR: Unable to read the license from remote server.")
		cli.Exit(1)
	}
	t.remoteKey = key
	count, err := t.ssh(t.remoteSQL("SELECT COUNT(*) FROM users")).Out()
	if err != nil {
		t.log("[✘] ERROR: Unable to check users from remote server. Is OpenPanel installed?")
		cli.Exit(1)
	}
	// limit is skipped only when both servers have a key
	if !(t.keyValue != "" && t.remoteKey != "") && text.Atoi(count) > 2 {
		t.log("[✘] ERROR: OpenPanel Community edition has a limit of 3 user accounts - which should be enough for private use.")
		t.log("If you require more than 3 accounts, please consider purchasing the Enterprise version that allows unlimited number of users and domains/websites.")
		cli.Exit(1)
	}
}

// destination's license decides here, --force drops the account's domains there first
func (t *transferJob) domainsLimitOnDestination() {
	if t.remoteKey != "" {
		return
	}
	other, err := t.ssh(t.remoteSQL("SELECT d.domain_url FROM domains d JOIN users u ON d.user_id = u.id WHERE u.username <> '" + t.username + "'")).Stdin("").Out()
	if err != nil {
		t.log("[✘] ERROR: Unable to check domains on remote server. Is mariadb running?")
		cli.Exit(1)
	}
	local := t.q("SELECT d.domain_url FROM domains d JOIN users u ON d.user_id = u.id WHERE u.username = '" + t.username + "';")
	set := map[string]bool{}
	otherCount := 0
	for _, l := range strings.Split(other, "\n") {
		set[l] = true
		if l != "" {
			otherCount++
		}
	}
	newCount := 0
	for _, l := range strings.Split(local, "\n") {
		if l != "" && !set[l] {
			newCount++
		}
	}
	if otherCount+newCount > 50 {
		t.log(fmt.Sprintf("[✘] ERROR: OpenPanel Community edition on the destination has a limit of 50 domains, this transfer would bring it to %d.", otherCount+newCount))
		t.log("If you require more than 50 domains, please consider purchasing the Enterprise version that allows unlimited number of users and domains/websites.")
		cli.Exit(1)
	}
}

// "context" is the users.server column (system user / home dir / docker context / ftp context), not necessarily the same as the username
func (t *transferJob) resolveContext() {
	t.context = t.q("SELECT server FROM users WHERE username = '" + t.username + "';")
	if t.context == "" {
		t.log("[✘] ERROR: Could not resolve context (server) for user '" + t.username + "'. Aborting.")
		cli.Exit(1)
	}
	t.log("Resolved context for " + t.username + ": " + t.context)
}

// usernameExists ran in a $() subshell, so on failure the logged line is what came back
func (t *transferJob) usernameExists() string {
	out, err := t.ssh(t.remoteSQL("SELECT COUNT(*) FROM users WHERE username = '" + t.username + "'")).Out()
	if err != nil {
		msg := "[" + time.Now().Format("2006-01-02 15:04:05") + "] [✘] Error: Unable to check username existence in the database. Is mariadb running?"
		appendFile(t.logFile, msg+"\n")
		return msg
	}
	return out
}

func (t *transferJob) exportMySQL() {
	tmp := fmt.Sprintf("/tmp/export_%s_%d", t.username, rand.Intn(32768))
	os.MkdirAll(tmp, 0o777)
	defer os.RemoveAll(tmp)

	userID := t.q("SELECT id FROM users WHERE username = '" + t.username + "';")
	if userID == "" {
		t.log("[ERROR] No user found with username '" + t.username + "'")
		cli.Exit(1)
	}
	t.planID = t.q("SELECT plan_id FROM users WHERE id = " + userID + ";")

	planTSV, _ := db.Cmd(`
  SELECT name, description, domains_limit, websites_limit, email_limit, ftp_limit,
         disk_limit, inodes_limit, db_limit, cpu, ram, bandwidth, feature_set,
         max_email_quota, max_hourly_email
  FROM plans WHERE id = `+t.planID+`;`, "-N", "-s").Raw()
	os.WriteFile(tmp+"/plan.tsv", []byte(planTSV), 0o666)
	os.WriteFile(tmp+"/plan_"+t.username+"_autoinc.sql", []byte(awkInsert(planTSV,
		"INSERT INTO plans (name, description, domains_limit, websites_limit, email_limit, ftp_limit, disk_limit, inodes_limit, db_limit, cpu, ram, bandwidth, feature_set, max_email_quota, max_hourly_email) VALUES",
		"('%s', '%s', %s, %s, %s, %s, '%s', %s, %s, '%s', '%s', %s, '%s', '%s', %s),\n", 15)), 0o666)

	userTSV, _ := db.Cmd(`
  SELECT username, password, email, owner, user_domains, twofa_enabled, otp_secret,
         plan, registered_date, server, plan_id
  FROM users WHERE id = `+userID+`;`, "-N", "-s").Raw()
	os.WriteFile(tmp+"/user.tsv", []byte(userTSV), 0o666)
	os.WriteFile(tmp+"/user_"+t.username+"_autoinc.sql", []byte(awkInsert(userTSV,
		"INSERT INTO users (username, password, email, owner, user_domains, twofa_enabled, otp_secret, plan, registered_date, server, plan_id) VALUES",
		"('%s', '%s', '%s', '%s', '%s', %s, '%s', '%s', '%s', '%s', %s),\n", 11)), 0o666)

	// sites as tsv, domain_url included so the destination can resolve its own domain_id
	sites, _ := db.Cmd(`
  SELECT s.site_name, d.domain_url, s.admin_email, s.version, s.type, s.ports, s.path, s.container
  FROM sites s JOIN domains d ON s.domain_id = d.domain_id
  WHERE d.user_id = `+userID+`;`, "-N", "-s").Raw()
	sitesFile := tmp + "/sites_" + t.username + ".tsv"
	os.WriteFile(sitesFile, []byte(sites), 0o666)

	// per-user tables the panel only creates on first use, ship the ddl too so a fresh destination has them
	exportUserTables(tmp, userID, t.q, func(q string) string {
		out, _ := db.Cmd(q, "-N", "-s", "-r").Raw()
		return out
	})

	dst := t.remote("/tmp/user_import_" + t.username + "/")
	t.ssh("mkdir -p /tmp/user_import_" + t.username + "/").Run()
	glob := func(p string) []string {
		m, _ := filepath.Glob(p)
		if len(m) == 0 {
			return []string{p}
		}
		return m
	}
	files := append(glob(tmp+"/*.ddl"), glob(tmp+"/*.rows.sql")...)
	t.rsync(append(files, dst)...).NoErr().Run()
	t.rsync(tmp+"/plan_"+t.username+"_autoinc.sql", dst).Run()
	t.rsync(tmp+"/user_"+t.username+"_autoinc.sql", dst).Run()
	if nonEmptyFile(sitesFile) {
		t.rsync(sitesFile, dst).Run()
	}
}

func (t *transferJob) importMySQLScript() string {
	return bashHeredoc(trImportMySQL, map[string]string{"mysql_database": db.Database, "USERNAME": t.username, "FORCE": map[bool]string{true: "1", false: "0"}[t.force]})
}

func (t *transferJob) copyFeatureSet() {
	fs := t.q("SELECT feature_set FROM plans WHERE id = " + t.planID + ";")
	file := fs + ".txt"
	dir := "/etc/openpanel/openpanel/features"
	t.log("Listing features on remote server ...")
	remoteFiles := t.ssh("ls -1 \"" + dir + "\" 2>/dev/null").Str()
	for _, l := range strings.Split(remoteFiles, "\n") {
		if l == file {
			t.log("Feature set '" + file + "' already exists on remote server")
			return
		}
	}
	t.log("Copying feature set '" + file + "' to remote server")
	t.rsync(dir+"/"+file, t.remote(dir+"/")).Run()
}

func (t *transferJob) copyUserAccount() {
	ctx := t.context
	tmp, _ := os.MkdirTemp("", "tmp.")
	t.log("Creating system user (" + ctx + ") on remote server ...")
	captureSystemUser(ctx, tmp)
	if !conf.Exists("/etc/shadow") {
		fmt.Fprintln(os.Stderr, "grep: /etc/shadow: No such file or directory")
	}
	// per-account dir on the destination, bulk transfers run two of these at once
	t.rsync(tmp+"/passwd.user", tmp+"/group.user", tmp+"/shadow.user", tmp+"/subuid.user", tmp+"/subgid.user", t.remote("/root/transfer_"+ctx+"/")).Run()
	os.RemoveAll(tmp)

	t.ssh().Stdin(bashHeredoc(trCopyUser, map[string]string{"CONTEXT": ctx})).Run()

	// Fetch the UID map file locally and remove it from the remote side
	if f, err := os.Create("/tmp/" + ctx + "_uid_map.txt"); err == nil {
		t.ssh("cat /root/" + ctx + "_uid_map.txt").Stdout(f).Run()
		f.Close()
	}
	t.ssh("rm -f /root/" + ctx + "_uid_map.txt").Run()
}

func (t *transferJob) remoteNameservers() {
	if !t.live {
		return
	}
	cfg := "/etc/openpanel/openpanel/conf/openpanel.config"
	fmt.Println("Checking NS configuration on remote server...")
	if !t.ssh("[ -f '" + cfg + "' ]").OK() {
		fmt.Println("[ERROR] Configuration file not found on remote: " + cfg)
		cli.Exit(1)
	}
	ns := func(k string) string { return t.ssh("grep '^" + k + "=' '" + cfg + "' | cut -d'=' -f2").Str() }
	ns1, ns2, ns3, ns4 := ns("ns1"), ns("ns2"), ns("ns3"), ns("ns4")
	if ns1 == "" || ns2 == "" {
		fmt.Println("[ERROR] ns1 and ns2 are not set on remote serverm - Live transfer will not forward DNS!")
		return
	}
	t.nameservers = []string{ns1, ns2}
	if ns3 != "" {
		t.nameservers = append(t.nameservers, ns3)
	}
	if ns4 != "" {
		t.nameservers = append(t.nameservers, ns4)
	}
	fmt.Println("[INFO] Remote NS: " + strings.Join(t.nameservers, " "))
}

func (t *transferJob) updateZoneFile(zone string) {
	// without nameservers from the destination this would just strip every NS record
	if len(t.nameservers) == 0 {
		return
	}
	if !conf.IsFile(zone) {
		fmt.Println("[WARNING] Zone file not found: " + zone)
		return
	}
	fmt.Println("[INFO] Updating NS records in: " + zone)
	nsRe := regexp.MustCompile(`^[^;].*IN[[:space:]]*NS[[:space:]]*`)
	var out []string
	for _, l := range conf.Lines(zone) {
		if !nsRe.MatchString(l) {
			out = append(out, l)
		}
	}
	for _, n := range t.nameservers {
		out = append(out, "@ IN NS "+n+".")
	}
	conf.WriteInPlace(zone, linesText(out))
}

func (t *transferJob) rsyncFiles() {
	ctx := t.context
	t.log("Syncing files for user " + t.username + " (context: " + ctx + ") ...")
	out, err := t.rsync("--include=/"+ctx+"/docker-data/volumes/", "--exclude=/"+ctx+"/docker-data/*", "--exclude=/"+ctx+"/sockets/*/*.sock", "--exclude=/"+ctx+"/sockets/*/*.pid", "/home/"+ctx, t.remote("/home/")).Combined()
	t.log(out)
	if err != nil {
		t.log("[ERROR] Rsync failed! Output:")
		t.log(out)
		cli.Exit(1)
	}
	mapping := "/tmp/" + ctx + "_uid_map.txt"
	if conf.IsFile(mapping) {
		var ml []string
		for _, l := range conf.Lines(mapping) {
			if strings.HasPrefix(l, ctx+":") {
				ml = append(ml, l)
			}
		}
		if line := strings.Join(ml, "\n"); line != "" {
			f := strings.SplitN(text.FirstLine(line), ":", 5)
			for len(f) < 4 {
				f = append(f, "")
			}
			oldUID, newUID, gid := f[1], f[2], f[3]
			if oldUID != newUID {
				t.log("UID changed for " + ctx + " (from " + oldUID + " to " + newUID + "), performing chown on remote host ...")
				t.ssh("chown -R " + newUID + ":" + gid + " /home/" + ctx + "; [ -f /home/" + ctx + "/backup.env ] && chown -h root:root /home/" + ctx + "/backup.env; [ -f /home/" + ctx + "/.env ] && chgrp -h 33 /home/" + ctx + "/.env && chmod 640 /home/" + ctx + "/.env").Run()
			}
		}
	} else {
		t.log("[WARNING] UID mapping file not found for " + ctx)
	}

	// the final pass only copies files again, the domains below were imported by the first one
	if t.finalSync {
		return
	}
	if stats := "/var/log/caddy/stats/" + t.username; conf.IsDir(stats) {
		t.rsync(stats, t.remote("/var/log/caddy/stats/")).Run()
	}
	if b := "/etc/openpanel/caddy/deny/" + ctx + ".ips"; conf.IsFile(b) {
		t.ssh("mkdir -p /etc/openpanel/caddy/deny/").Run()
		t.rsync(b, t.remote("/etc/openpanel/caddy/deny/")).Run()
	}

	all := sh.Opencli("domains-user", t.username, "--docroot", "--php_version").Str()
	if strings.Contains(all, "No domains found for user '"+t.username+"'") {
		t.log("No domains found for user " + t.username + ". Skipping.")
		return
	}
	dkim := "/usr/local/mail/openmail/docker-data/dms/config/opendkim"
	for _, l := range strings.Split(all, "\n") {
		f := strings.FieldsFunc(l, func(c rune) bool { return c == ' ' || c == '\t' })
		for len(f) < 3 {
			f = append(f, "")
		}
		domain, docroot, php := f[0], f[1], strings.Join(f[2:], " ")
		owner := whoOwns(t.ssh("opencli domains-whoowns "+domain).Stdin("").Str(), domain)
		if owner == "" {
			// add domain on remote
			lf, _ := os.OpenFile(t.logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
			err := t.ssh("opencli domains-add " + domain + " " + t.username + " --docroot " + docroot + " --php_version " + php + " --skip_caddy --skip_vhost --skip_containers --skip_dns").Stdin("").Stdout(lf).Stderr(lf).Run()
			lf.Close()
			if err != nil {
				t.log("[✘] ERROR: Failed to import domain " + domain)
				cli.Exit(1)
			}
			if c := "/etc/openpanel/caddy/domains/" + domain + ".conf"; conf.IsFile(c) {
				t.rsync(c, t.remote("/etc/openpanel/caddy/domains/")).Run()
			}
			if c := "/var/log/caddy/domlogs/" + domain; conf.IsFile(c) {
				t.rsync(c, t.remote("/var/log/caddy/domlogs/")).Run()
			}
			if c := "/var/log/caddy/coraza_waf/" + domain + ".log"; conf.IsFile(c) {
				t.rsync(c, t.remote("/var/log/caddy/coraza_waf/")).Run()
			}
			if c := "/etc/openpanel/caddy/suspended_domains/" + domain + ".conf"; conf.IsFile(c) {
				t.ssh("mkdir -p /etc/openpanel/caddy/suspended_domains/").Run()
				t.rsync(c, t.remote("/etc/openpanel/caddy/suspended_domains/")).Run()
			}
			if c := "/etc/bind/zones/" + domain + ".zone"; conf.IsFile(c) {
				t.rsync(c, t.remote("/etc/bind/zones/")).Run()
				t.ssh("sed -i 's/" + t.currentIP + "/" + t.host + "/g' /etc/bind/zones/" + domain + ".zone").Run()
				t.ssh().Stdin(bashHeredoc(trNamedZone, map[string]string{"domain": domain})).Quiet().Run()
			}
			if c := "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/" + domain; conf.IsDir(c) {
				t.rsync(c, t.remote("/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/")).Run()
			}
			if c := "/etc/openpanel/caddy/ssl/custom/" + domain; conf.IsDir(c) {
				t.rsync(c, t.remote("/etc/openpanel/caddy/ssl/custom/")).Run()
			}
		}
		if conf.IsDir(dkim + "/keys/" + domain) {
			t.ssh("mkdir -p " + dkim + "/keys/").Run()
			t.rsync(dkim+"/keys/"+domain, t.remote(dkim+"/keys/")).Run()
			// keys alone aren't used until the domain is listed in the opendkim tables
			for _, table := range []string{"KeyTable", "SigningTable", "TrustedHosts"} {
				if !conf.IsFile(dkim + "/" + table) {
					continue
				}
				for _, line := range dkimLines(dkim+"/"+table, domain) {
					dst := dkim + "/" + table
					t.ssh("touch " + dst + "; grep -qxF '" + line + "' " + dst + " || echo '" + line + "' >> " + dst).Stdin("").Run()
				}
			}
		}
	}
	t.ssh("cd /root && podman-compose up -d bind9 >/dev/null 2>&1; podman exec openpanel_dns rndc reconfig >/dev/null 2>&1").Run()
	if t.live {
		sh.C("podman", "exec", "caddy", "caddy", "reload").Quiet().Run()
	}
}

func (t *transferJob) setupRemotePodman() {
	ctx := t.context
	// context resolution is dynamic per user uid under podman, nothing to register, ~/.config/containers rides along with the home dir
	if !conf.IsDir("/home/" + ctx + "/.config/containers") {
		t.log("No .config/containers directory for " + ctx + " on source!")
		cli.Exit(1)
	}
	uid := t.ssh("stat -c '%u' /home/" + ctx).NoErr().Str()
	if uid == "" {
		t.log("FATAL ERROR: Failed to get UID for user " + ctx + " on remote server")
		cli.Exit(1)
	}
	t.log("Enabling rootless podman for " + ctx + " on destination ...")
	if !t.ssh("loginctl enable-linger " + ctx).Quiet().OK() {
		t.log("Failed to enable linger for " + ctx)
	}
	// wait for user@uid before systemctl --user, enable-linger returns before it's up
	t.ssh("for i in $(seq 30); do systemctl is-active user@" + uid + ".service >/dev/null 2>&1 && break; sleep 1; done; systemctl --user -M " + ctx + "@ daemon-reload; systemctl --user -M " + ctx + "@ reset-failed podman.socket; systemctl --user -M " + ctx + "@ enable --now podman.socket").Quiet().Run()
	if !t.ssh("systemctl --user -M " + ctx + "@ is-active podman.socket").Quiet().OK() {
		t.log("[!] Warning: podman.socket is not active for " + ctx + " on destination, containers may not start")
	}
}

func (t *transferJob) restoreFTP() {
	dir := "/etc/openpanel/ftp/users/" + t.context
	if !conf.IsFile(dir + "/users.list") {
		t.log("No FTP accounts found for context " + t.context + ", skipping FTP restore.")
		return
	}
	t.log("Restoring FTP accounts for " + t.username + " (context: " + t.context + ") ...")
	t.ssh("mkdir -p '" + dir + "'").Run()
	t.rsync(dir+"/", t.remote(dir+"/")).Run()

	// passwords are already hashed in users.list, the gid comes from the remote home in case the uid got remapped
	out, err := t.ssh("bash", "-s").Stdin(bashHeredoc(trFTP, map[string]string{"CONTEXT": t.context})).Combined()
	if out != "" {
		out += "\n"
	}
	t.logpipe(strings.NewReader(out))
	if err == nil {
		t.log("FTP accounts restored for context " + t.context + ".")
	} else {
		t.log(fmt.Sprintf("[!] Warning: FTP restore reported an error for context %s (exit %d)", t.context, sh.Code(err)))
	}
}

func (t *transferJob) syncMail() {
	t.log("Syncing mail data ...")
	dms := "/usr/local/mail/openmail/docker-data/dms/config"
	postfwd := "/usr/local/mail/openmail/postfwd/postfwd.cf"
	var domains []string
	all := sh.Opencli("domains-user", t.username, "--docroot", "--php_version").NoErr().Str()
	if !strings.Contains(all, "No domains found") && all != "" {
		for _, l := range strings.Split(all, "\n") {
			if d := firstWord(l); d != "" {
				domains = append(domains, d)
			}
		}
	}
	if len(domains) > 0 {
		// one alternative per domain, anchored so example.com doesn't also grab blog.example.com
		alt := mailDomainAlt(domains)
		dp := regexp.MustCompile("@(" + alt + ")([|: ]|$)")
		rp := regexp.MustCompile("@(" + alt + ")/")
		tmp, _ := os.MkdirTemp("", "tmp.")
		for _, cf := range []string{"postfix-accounts.cf", "postfix-virtual.cf", "dovecot-quotas.cf", "postfix-receive-access.cf", "postfix-send-access.cf"} {
			grepFile(dms+"/"+cf, tmp+"/"+cf, dp)
		}
		grepFile(dms+"/postfix-regex.cf", tmp+"/postfix-regex.cf", rp)
		os.WriteFile(tmp+"/postfwd.cf", nil, 0o666)
		if conf.IsFile(postfwd) {
			os.WriteFile(tmp+"/postfwd.cf", []byte(postfwdFor(postfwd, domains)), 0o666)
		}
		// merge into the destination files instead of overwriting them, other accounts already live there
		t.ssh("mkdir -p " + dms + " /usr/local/mail/openmail/postfwd/ /tmp/mail_import_" + t.username + "/").Run()
		t.rsync(tmp+"/", t.remote("/tmp/mail_import_"+t.username+"/")).Run()
		t.ssh("bash", "-s").Stdin(bashHeredoc(trMail, map[string]string{"USERNAME": t.username, "DMS_CONFIG": dms})).Run()
		os.RemoveAll(tmp)
		t.startMail = true
	}

	// Physical maildir sync
	store, _ := conf.Raw(conf.AdminINI, "email_storage_location")
	store = text.Xargs(store)
	remoteStore := t.ssh("grep -E '^email_storage_location=' /etc/openpanel/openadmin/config/admin.ini 2>/dev/null | cut -d'=' -f2- | xargs").NoErr().Str()
	switch {
	case strings.HasPrefix(store, "/") && conf.IsDir(store):
		// shared store keeps one dir per domain, only copy this user's domains
		if !strings.HasPrefix(remoteStore, "/") {
			remoteStore = store
		}
		ls, rs := strings.TrimSuffix(store, "/"), strings.TrimSuffix(remoteStore, "/")
		for _, d := range domains {
			if conf.IsDir(ls + "/" + d) {
				t.log("Syncing maildir " + ls + "/" + d + " → " + rs + "/" + d + " ...")
				t.ssh("mkdir -p " + rs).Run()
				t.rsync(ls+"/"+d, t.remote(rs+"/")).Run()
				t.startMail = true
			}
		}
	case conf.IsDir("/home/" + t.context + "/mail/"):
		t.log("Maildir is inside /home/" + t.context + "/mail/, already synced with the home directory.")
	default:
		t.log("[!] No maildir found for " + t.username + ", skipping.")
	}
}

func (t *transferJob) containersFile() string {
	return "/tmp/docker_containers_names_" + t.username + ".txt"
}

func (t *transferJob) storeContainers() {
	out := t.containersFile()
	os.WriteFile(out, nil, 0o666)
	if conf.IsFile(podman.ComposeFile(t.context)) {
		t.log("Checking podman context ....")
		names := podman.User(t.context, "ps", "--format", "{{.Names}}").NoErr().Str()
		if names != "" {
			appendFile(out, t.context+": "+strings.TrimSuffix(strings.ReplaceAll(names+"\n", "\n", " "), " ")+"\n")
		} else {
			appendFile(out, t.context+": no containers\n")
		}
	}
	t.rsync(out, t.user+"@"+t.host+":"+out).Run()
}

func (t *transferJob) restoreContainers() {
	for _, l := range readLines(t.containersFile()) {
		ctx, containers, _ := strings.Cut(l, ":")
		ctx = text.Xargs(ctx)
		if ctx == "" || strings.Contains(containers, "no containers") {
			t.log("No containers running for user")
			continue
		}
		t.log("Starting containers inside podman context on remote server ...")
		t.ssh("remote_uid=$(stat -c '%u' /home/" + ctx + "); CONTAINER_HOST=unix:///hostfs/run/user/${remote_uid}/podman/podman.sock podman-compose -f /home/" + ctx + "/docker-compose.yml down >/dev/null 2>&1; CONTAINER_HOST=unix:///hostfs/run/user/${remote_uid}/podman/podman.sock podman-compose -f /home/" + ctx + "/docker-compose.yml up -d " + containers + " >/dev/null 2>&1").Run()
	}
}

func (t *transferJob) restartServicesOnTarget() {
	t.log("Reloading services on " + t.host + " server ...")
	t.ssh("cd /root && podman-compose up -d openpanel bind9 caddy >/dev/null 2>&1; podman exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1; systemctl restart admin >/dev/null 2>&1").Run()
	if t.startMail {
		t.log("Reloading mailserver and webmail on " + t.host + " server ...")
		t.ssh("cd /usr/local/mail/openmail && podman-compose up -d mailserver roundcube >/dev/null 2>&1").Run()
	}
}

// what got moved, shown on the destination under the user_transfer notification
func (t *transferJob) notifyDestination() {
	var list []string
	for _, l := range text.Lines(sh.Opencli("domains-user", t.username).NoErr().Str()) {
		if strings.Contains(l, "No domains found") {
			continue
		}
		if f := text.Field(l, 1); f != "" {
			list = append(list, f)
		}
	}
	sites, _ := db.Cmd("SELECT COUNT(*) FROM sites s JOIN domains d ON s.domain_id=d.domain_id JOIN users u ON d.user_id=u.id WHERE u.username='"+t.username+"';", "-N", "-s").NoErr().Out()
	mails := "0"
	accounts := "/usr/local/mail/openmail/docker-data/dms/config/postfix-accounts.cf"
	if len(list) > 0 && conf.IsFile(accounts) {
		mails = strconv.Itoa(len(grepLines(accounts, regexp.MustCompile("@("+mailDomainAlt(list)+`)\|`))))
	}
	ftps := ""
	if usersList := "/etc/openpanel/ftp/users/" + t.context + "/users.list"; conf.IsFile(usersList) {
		ftps = strconv.Itoa(len(grepLines(usersList, regexp.MustCompile(`\|`))))
	}
	containers := 0
	for _, l := range text.Lines(conf.Read(t.containersFile())) {
		_, c, _ := strings.Cut(l, ":")
		containers += len(strings.Fields(c))
	}
	or := func(s, d string) string {
		if s == "" {
			return d
		}
		return s
	}
	elapsed := int64(time.Since(t.start) / time.Second)
	from := or(t.currentIP, "another server")
	title := "User " + t.username + " transferred from " + from
	msg := fmt.Sprintf("Account %s was transferred from %s in %dm %ds. Domains: %d. Websites: %s. Email accounts: %s. FTP accounts: %s. Containers started: %d.", t.username, from, elapsed/60, elapsed%60, len(list), or(sites, "0"), mails, or(ftps, "0"), containers)
	if t.live {
		msg += " Live transfer: the account was suspended on the source server and DNS points here."
	}
	t.ssh("nohup opencli sentinel --action=user_transfer --title=" + bashQuote(title) + " --message=" + bashQuote(msg) + " >/dev/null 2>&1 &").Stdin("").Run()
}
