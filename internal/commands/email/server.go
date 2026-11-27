// ##############################################################################
// Script Name: internal/commands/email/server.go
// Description: Manage mailserver
// Usage: opencli email-server <status|config|install|uninstall|start|stop|restart|queue|flush|view|unhold|delete|fail2ban|ports|postconf|logs|login|supervisor|postfwd|pflogsumm|update-check|update-packages|versions> [args] [--debug]
// Author: Stefan Pejcic
// Created: 18.08.2024
// Last Modified: 23.11.2026
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

package email

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/ojson"
	"opencli/internal/podman"
	"opencli/internal/requirement"
	"opencli/internal/sh"
	"opencli/internal/text"
	wm "opencli/internal/webmail"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "email-server",
		Desc:  "Manage mailserver",
		Usage: "opencli email-server <status|config|install|uninstall|start|stop|restart|queue|flush|view|unhold|delete|fail2ban|ports|postconf|logs|login|supervisor|postfwd|pflogsumm|update-check|update-packages|versions> [args] [--debug]",
		Docs:  "https://docs.openpanel.com",
		Run:   server,
	})
}

const (
	srvRepo       = "https://github.com/stefanpejcic/openmail"
	srvEnv        = mailDir + "/mailserver.env"
	srvCompose    = mailDir + "/compose.yml"
	srvDefaultDom = "example.net"
)

var (
	srvDebug  bool
	srvIPRe   = regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}$`)
	srvPrivRe = regexp.MustCompile(`^(10\.|172\.(1[6-9]|2[0-9]|3[0-1])\.|192\.168\.)`)
)

func srvRun(c *sh.Cmd) {
	if srvDebug {
		c.Run()
	} else {
		c.Quiet().Run()
	}
}

func srvSection(title string) {
	if srvDebug {
		fmt.Println("")
		fmt.Printf("----------------- %s ------------------\n", title)
		fmt.Println("")
	}
}

func srvDbg(a ...any) {
	if srvDebug {
		fmt.Println(a...)
	}
}

func srvExec(args ...string) *sh.Cmd {
	return sh.C("podman", append([]string{"exec", container}, args...)...)
}

func srvExecIT(args ...string) int {
	return sh.Code(sh.C("podman", append([]string{"exec", "-it", container}, args...)...).StdinReader(os.Stdin).Run())
}

func srvDMSVersion() string {
	out, _ := srvExec("bash", "-c", `cat /VERSION 2>/dev/null || printf "%s" "$DMS_RELEASE"`).Out()
	return out
}

func srvPorts() string {
	out, _ := sh.C("podman", "port", container).Out()
	return out
}

func srvRequireCmds() {
	for _, c := range []string{"cat", "cut", "podman", "podman-compose", "fold", "printf", "sed", "tail", "tput", "tr"} {
		if !sh.Has(c) {
			cli.Errf("Error: '%s' not found.", c)
			cli.Errf("")
			cli.Exit(1)
		}
	}
	if !sh.C("podman-compose", "version").Quiet().OK() {
		cli.Errf("Error: 'podman-compose' not available.")
		cli.Errf("")
		cli.Exit(1)
	}
}

// srvPrint is execute_cmd_and_print, the value is folded and indented under the label
func srvPrint(label, val string) {
	const indent = 14
	spaces := strings.Repeat(" ", indent)
	tc := sh.C("tput", "cols").NoErr().Str()
	cols := text.Atoi(tc)
	if !text.IsInt(tc) || cols <= 40 {
		cols = 80
	}
	folded, _ := sh.C("fold", "-s", "-w", fmt.Sprint(cols-16)).Stdin(val).Raw()
	var b strings.Builder
	for _, l := range strings.SplitAfter(folded, "\n") {
		if l != "" {
			b.WriteString(spaces + l)
		}
	}
	status := strings.TrimRight(b.String(), "\n")
	if len(status) >= indent {
		status = status[indent:]
	} else {
		status = ""
	}
	fmt.Printf("%-*s%s\n", indent, label+":", status)
}

func server(args []string) int {
	requirement.Command("git")
	// community edition check
	requireEnterprise(msgUnlimited, 1)
	srvRequireCmds()
	for _, a := range args {
		if a == "--debug" {
			srvDebug = true
			fmt.Println("--debug flag provided: displaying verbose information.")
		} else if a == "-x" {
			srvDebug = true
			sh.Trace = true
			fmt.Println("-x flag provided: display each command as it execute.")
		}
	}
	a1 := sa(args, 0)
	if a1 != "" && a1 != "install" && a1 != "status" && a1 != "start" && a1 != "stop" && a1 != "restart" {
		if !podman.IsRunning(container) {
			cli.Errf("Error: Container '%s' is not up.\n", container)
			return 1
		}
	}
	rest := []string{}
	if len(args) > 1 {
		rest = args[1:]
	}

	switch {
	case a1 == "install":
		fmt.Println("Installing the mailserver...")
		srvInstall()
	case a1 == "pflogsumm":
		fmt.Println("Generating reports...")
		srvPflogsumm()
	case a1 == "status":
		srvStatus()
	case a1 == "config":
		srvExec("cat", "/etc/dms-settings").Run()
	case a1 == "start":
		fmt.Println("Starting mailserver...")
		if !srvStart() {
			return 1
		}
	case a1 == "stop":
		fmt.Println("Stopping the mailserver...")
		if !srvStop() {
			return 1
		}
	case a1 == "restart":
		fmt.Println("Restarting the mailserver...")
		if !srvStop() {
			return 1
		}
		if !srvStart() {
			return 1
		}
	case a1 == "uninstall":
		fmt.Println("Uninstalling the mailserver...")
		srvUninstall()
	case a1 == "postfwd":
		if code, done := srvPostfwd(sa(args, 1)); done {
			return code
		}
	case a1 == "queue":
		srvExec("postqueue", "-p").Run()
	case a1 == "flush":
		srvExec("postqueue", "-f").Run()
		fmt.Println("Queue flushed.")
	case a1 == "unhold":
		if sa(args, 1) == "" {
			fmt.Println("Error: Queue ID missing")
		} else {
			srvExec(append([]string{"postsuper"}, flagEach("-H", rest)...)...).Run()
		}
	case a1 == "view":
		if sa(args, 1) == "" {
			cli.Errf("Error: Queue ID missing.")
		} else {
			srvExec("postcat", "-q", args[1]).Run()
		}
	case a1 == "delete":
		if sa(args, 1) == "" {
			fmt.Println("Error: Queue ID missing.")
		} else {
			srvExec(append([]string{"postsuper"}, flagEach("-d", rest)...)...).Run()
		}
	case strings.HasPrefix(a1, "fail"):
		srvExec(append([]string{"fail2ban"}, rest...)...).Run()
	case a1 == "ports":
		fmt.Println("Published ports:")
		fmt.Println()
		fmt.Println(srvPorts())
	case strings.HasPrefix(a1, "postc"):
		srvExec(append([]string{"postconf"}, rest...)...).Run()
	case a1 == "logs":
		if sa(args, 1) == "-f" {
			sh.Run("podman", "logs", "-f", container)
		} else {
			sh.Run("podman", "logs", container)
		}
	case a1 == "login":
		srvExecIT("bash")
	case strings.HasPrefix(a1, "super"):
		srvExecIT(append([]string{"supervisorctl"}, rest...)...)
	case strings.HasPrefix(a1, "update-c"):
		srvExecIT("bash", "-c", "apt update && echo && apt list --upgradable")
	case strings.HasPrefix(a1, "update-p"):
		srvExecIT("bash", "-c", "apt update && echo && apt-get upgrade")
	case strings.HasPrefix(a1, "version"):
		fmt.Printf("%-15s%s\n\n", "Mailserver:", srvDMSVersion())
		for _, p := range []string{"amavisd-new", "clamav", "dovecot-core", "fail2ban", "fetchmail", "getmail6", "rspamd", "opendkim", "opendmarc", "postfix", "spamassassin", "supervisor"} {
			fmt.Printf("%-15s", p+":")
			srvExec("bash", "-c", "set -o pipefail; dpkg -s "+p+" 2>/dev/null | grep ^Version | cut -d' ' -f2 || echo 'Package not installed.'").Run()
		}
	default:
		fmt.Print(srvUsage)
	}
	fmt.Println()
	return 0
}

func flagEach(flag string, ids []string) []string {
	var out []string
	for _, i := range ids {
		out = append(out, flag, i)
	}
	return out
}

func srvStatus() {
	if !podman.CanExec(container) {
		// podman can say running here too, with nothing left to exec into
		st, err := sh.C("podman", "inspect", container, "--format", "{{.State.Status}}").NoErr().Out()
		if err != nil {
			st = "down"
		}
		fmt.Printf("Container: %s (not responding)\n", st)
		return
	}
	// uptime
	ps, _ := sh.C("podman", "ps", "--no-trunc", "--filter", "name=^"+container+"$", "--format", "{{.Status}}").Out()
	srvPrint("Container", ps)
	fmt.Println()

	srvPrint("Version", srvDMSVersion())
	fmt.Println()

	// Fail2ban status
	if srvExec("ls", "/var/run/fail2ban/fail2ban.sock").Quiet().OK() {
		f, _ := srvExec("fail2ban").Out()
		srvPrint("Fail2ban", f)
	}
	fmt.Println()

	// got updates?
	pk, _ := srvExec("bash", "-c", `apt -q update 2>/dev/null | grep "All packages are up to date" || echo "Updates available"`).Out()
	srvPrint("Packages", pk)
	fmt.Println()

	srvPrint("Ports", srvPorts())
	fmt.Println()

	// Postfix mail queue
	q, _ := srvExec("postqueue", "-p").Raw()
	q = strings.TrimSuffix(q, "\n")
	last := q
	if i := strings.LastIndex(q, "\n"); i >= 0 {
		last = q[i+1:]
	}
	postfix := last
	if strings.Contains(last, " ") {
		f := strings.Split(last, " ")
		postfix = ""
		if len(f) > 4 {
			postfix = f[4]
		}
	}
	if postfix == "" {
		postfix = "Mail queue is empty"
	} else {
		postfix += " mail(s) queued"
	}
	srvPrint("Postfix", postfix)
	fmt.Println()

	// Supervisor
	sup, _ := srvExec("supervisorctl", "status").Raw()
	sorted, _ := sh.C("sort", "-b", "-k2,2").Stdin(sup).Out()
	srvPrint("Supervisor", sorted)
}

// srvServices adds or removes an entry in OpenAdmin > Services > Services Status
func srvService(action, name, typ, realName string) {
	const file = "/etc/openpanel/openadmin/config/services.json"
	if !conf.IsFile(file) {
		fmt.Printf("File not found: %s\n", file)
		return
	}
	raw := conf.Read(file)
	v, perr := ojson.Parse(raw)
	arr, _ := v.([]any)
	exists := false
	for _, e := range arr {
		if o, ok := e.(*ojson.Object); ok && o.Str("name") == name {
			if n, _ := o.Get("name"); n == name {
				exists = true
			}
		}
	}
	write := func(a []any) {
		if perr != nil {
			return
		}
		tmp := file + ".tmp"
		if os.WriteFile(tmp, []byte(ojson.Pretty(a)+"\n"), 0o644) == nil {
			os.Rename(tmp, file)
		}
	}
	switch action {
	case "add":
		if exists {
			fmt.Printf("%s already exists in %s.\n", name, file)
			return
		}
		write(append(arr, ojson.New().Set("name", name).Set("type", typ).Set("real_name", realName)))
		fmt.Printf("%s added successfully.\n", name)
	case "remove":
		if !exists {
			fmt.Printf("%s not found in %s.\n", name, file)
			return
		}
		var keep []any
		for _, e := range arr {
			if o, ok := e.(*ojson.Object); ok {
				if n, _ := o.Get("name"); n == name {
					continue
				}
			}
			keep = append(keep, e)
		}
		if keep == nil {
			keep = []any{}
		}
		write(keep)
		fmt.Printf("%s removed successfully.\n", name)
	}
}

func srvEnableEmails() {
	srvSection("CHECKING ENABLED MODULES")
	v, _ := conf.Raw(conf.Panel, "enabled_modules")
	if i := strings.Index(v, "="); i >= 0 {
		v = v[:i]
	}
	if strings.Contains(v, "emails") {
		fmt.Println("'emails' module is already in enabled modules.")
		return
	}
	newModules := v + ",emails"
	fmt.Println("'emails' module is not enabled. Enabling..")
	conf.SedFile(conf.Panel, func(lines []string) []string {
		for i, l := range lines {
			if strings.HasPrefix(l, "enabled_modules=") {
				lines[i] = "enabled_modules=" + newModules
			}
		}
		return lines
	})
	fmt.Println("Restarting OpenPanel container to enable email pages..")
	if podman.IsRunning("openpanel") {
		srvRun(sh.C("podman", "restart", "openpanel"))
	} else {
		podman.EnsureRunning("openpanel", "/root", "openpanel", 0)
	}
}

func srvPflogsumm() {
	const repo = "https://github.com/stefanpejcic/PFLogSumm-HTML-GUI.git"
	if !conf.IsDir("/tmp") {
		cli.Errf("Error: Failed to enter /tmp in order to generate a summary report.")
		cli.Exit(1)
	}
	tmp := func(name string, a ...string) *sh.Cmd { return sh.C(name, a...).Dir("/tmp").Quiet() }
	os.RemoveAll("/tmp/PFLogSumm-HTML-GUI")
	tmp("git", "clone", "--branch", "podman", "--single-branch", repo).Run()
	os.Symlink(srvEnv, mailDir+"/.env")
	tmp("podman", "cp", "PFLogSumm-HTML-GUI/pflogsummUIReport.sh", container+":/opt/pflogsummUIReport.sh").Run()
	fmt.Println("Generating email statistics reports.. This can take a while.")
	tmp("podman", "exec", container, "sh", "-c", "mkdir -p /usr/local/admin/static/reports").Run()
	tmp("podman", "cp", "/usr/local/admin/templates/emails/data", container+":/usr/local/admin/static/reports/data").Run()
	sh.C("podman", "exec", container, "sh", "-c", "bash /opt/pflogsummUIReport.sh").Dir("/tmp").Run()
	fmt.Println("Done, adding reports to OpenAdmin interface")
	os.MkdirAll("/usr/local/admin/static/reports", 0o755)
	os.MkdirAll("/usr/local/admin/templates/emails", 0o755)
	tmp("podman", "cp", container+":/usr/local/admin/static/reports/reports.html", "/usr/local/admin/templates/emails/reports.html").Run()
	tmp("podman", "cp", container+":/usr/local/admin/static/reports/data", "/usr/local/admin/templates/emails/").Run()
	tmp("podman", "exec", container, "sh", "-c", "rm -rf /usr/local/admin/static/reports").Run()
	tmp("systemctl", "reload", "admin").Run()
	fmt.Println("Completed")
	os.RemoveAll("/tmp/PFLogSumm-HTML-GUI")
}

// srvSedKey is sed -i "s|KEY=.*|KEY=value|" on a file, only the first match in a line
func srvSedKey(file, key, val string) {
	conf.SedFile(file, func(lines []string) []string {
		for i, l := range lines {
			if j := strings.Index(l, key+"="); j >= 0 {
				lines[i] = l[:j] + key + "=" + val
			}
		}
		return lines
	})
}

// srvSedLine is sed -i '/^KEY=/c\KEY=value', or a delete when del is set
func srvSedLine(file, key, val string, del bool) {
	conf.SedFile(file, func(lines []string) []string {
		var out []string
		for _, l := range lines {
			if strings.HasPrefix(l, key+"=") {
				if del {
					continue
				}
				l = key + "=" + val
			}
			out = append(out, l)
		}
		return out
	})
}

func srvAppend(file, line string) {
	if f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
		f.WriteString(line + "\n")
		f.Close()
	}
}

func srvHasLine(file, prefix string) bool {
	for _, l := range strings.Split(conf.Read(file), "\n") {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func srvComposeKeys(host, port, smtpHost, smtpPort string) {
	if !conf.IsFile(srvCompose) {
		return
	}
	srvSedKey(srvCompose, "ROUNDCUBEMAIL_DEFAULT_HOST", host)
	srvSedKey(srvCompose, "ROUNDCUBEMAIL_DEFAULT_PORT", port)
	srvSedKey(srvCompose, "ROUNDCUBEMAIL_SMTP_SERVER", smtpHost)
	srvSedKey(srvCompose, "ROUNDCUBEMAIL_SMTP_PORT", smtpPort)
	// golub verifies the certificate, so with a hostname it connects to it over implicit TLS
	golubHost, golubTLS := "mailserver", "none"
	if h, ok := strings.CutPrefix(host, "ssl://"); ok {
		golubHost, golubTLS = h, "implicit"
	}
	for _, k := range []string{"GOLUB_IMAP_HOST", "GOLUB_SMTP_HOST"} {
		srvSedKey(srvCompose, k, golubHost)
	}
	for _, k := range []string{"GOLUB_IMAP_TLS", "GOLUB_SMTP_TLS"} {
		srvSedKey(srvCompose, k, golubTLS)
	}
}

func srvSetSSL() {
	cur, _ := sh.Opencli("domain").Out()
	if cur == srvDefaultDom || (srvIPRe.MatchString(cur) && !srvPrivRe.MatchString(cur)) {
		fmt.Println("Configuring mailserver for plain-text authentication with IP")
		// used by roundcube to detect if SSL should be used
		srvSedLine(srvEnv, "OVERRIDE_HOSTNAME", "", false)
		srvSedLine(srvEnv, "SSL_TYPE", "", false)
		srvSedLine(srvEnv, "SSL_CERT_PATH", "", true)
		srvSedLine(srvEnv, "SSL_KEY_PATH", "", true)
		// SRS needs a hostname to send forwarded mail from
		srvSedLine(srvEnv, "ENABLE_SRS", "0", false)
		srvSedLine(srvEnv, "SRS_DOMAINNAME", "", true)
		// podman-compose doesn't interpolate ${VAR:+alt} conditionals like docker compose does, so hardcode the resolved roundcube values in the compose file too
		srvComposeKeys("mailserver", "", "mailserver", "")
	} else {
		fmt.Println("Configuring mailserver for TLS/SSL authentication with domain")
		le := "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/" + cur + "/" + cur
		custom := "/etc/openpanel/caddy/ssl/custom/" + cur + "/" + cur
		cert, key := le+".crt", le+".key"
		if conf.IsFile(cert) && conf.IsFile(key) {
			fmt.Printf("Using Let's Encrypt certs for %s\n", cur)
		} else if conf.IsFile(custom+".crt") && conf.IsFile(custom+".key") {
			fmt.Printf("Using custom certs for %s\n", cur)
			cert, key = custom+".crt", custom+".key"
		} else {
			fmt.Printf("WARNING: SSL files don’t exist for domain %s, will not be configured for mailserver!\n", cur)
			return
		}
		// used by roundcube to detect if SSL should be used
		srvSedKey(srvEnv, "OVERRIDE_HOSTNAME", cur)
		srvSedLine(srvEnv, "SSL_TYPE", "manual", false)
		if srvHasLine(srvEnv, "SSL_CERT_PATH=") {
			srvSedLine(srvEnv, "SSL_CERT_PATH", cert, false)
		} else {
			srvAppend(srvEnv, "SSL_CERT_PATH="+cert)
		}
		if srvHasLine(srvEnv, "SSL_KEY_PATH=") {
			srvSedLine(srvEnv, "SSL_KEY_PATH", key, false)
		} else {
			srvAppend(srvEnv, "SSL_KEY_PATH="+key)
		}
		// SRS: forwarded mail goes out as SRS0=...@hostname, so it passes SPF at the receiver and bounces come back here
		if strings.Contains(conf.Read(mailDir+"/openpanel/accounts.sh"), "_create_srs_exclude_map") {
			srvSedLine(srvEnv, "ENABLE_SRS", "1", false)
			if srvHasLine(srvEnv, "SRS_DOMAINNAME=") {
				srvSedLine(srvEnv, "SRS_DOMAINNAME", cur, false)
			} else {
				srvAppend(srvEnv, "SRS_DOMAINNAME="+cur)
			}
		}
		srvComposeKeys("ssl://"+cur, "993", "ssl://"+cur, "465")
	}
	// new containers resolve the mail hostname through this file
	sh.Opencli("docker-hosts").Quiet().Run()
}

// readChar is `read -t 10 -n 1 -r`, ok is false on timeout or EOF
func readChar() (string, bool) {
	tty := false
	if st, err := os.Stdin.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
		tty = true
	}
	var saved string
	if tty {
		saved, _ = sh.C("stty", "-g").StdinReader(os.Stdin).NoErr().Out()
		sh.C("stty", "-icanon", "min", "1").StdinReader(os.Stdin).Quiet().Run()
	}
	ch := make(chan string, 1)
	go func() {
		b := make([]byte, 1)
		n, err := os.Stdin.Read(b)
		if n == 1 {
			ch <- string(b)
		} else if err != nil {
			close(ch)
		}
	}()
	var res string
	ok := false
	select {
	case v, got := <-ch:
		res, ok = v, got
	case <-time.After(10 * time.Second):
	}
	if tty && saved != "" {
		sh.C("stty", saved).StdinReader(os.Stdin).Quiet().Run()
	}
	return res, ok
}

func srvConfirmClean() {
	const target = mailDir
	os.Remove(target) // rmdir, only works when empty
	if conf.IsDir(target) {
		fmt.Printf("Warning: %s already exists (leftover from a previous install attempt).\n", target)
		fmt.Println("It must be removed before continuing. Remove it now? (y/n)")
		in, ok := readChar()
		if !ok {
			fmt.Println("")
			fmt.Println("No response received. Aborting installation.")
			cli.Exit(1)
		}
		fmt.Println("")
		if in != "y" && in != "Y" {
			fmt.Println("Installation aborted.")
			cli.Exit(1)
		}
		os.RemoveAll(target)
	}
}

func srvOpenCSF(port string) {
	const csf = "/etc/csf/csf.conf"
	b, err := os.ReadFile(csf)
	if err != nil {
		cli.Errf("grep: %s: No such file or directory", csf)
	}
	if regexp.MustCompile(`TCP_IN = .*` + port).MatchString(string(b)) {
		srvDbg("Port " + port + " is already open in CSF.")
		return
	}
	if err == nil {
		re := regexp.MustCompile(`TCP_IN = "(.*)"`)
		conf.WriteFileKeep(csf, re.ReplaceAllString(string(b), `TCP_IN = "${1},`+port+`"`), false)
	}
	srvDbg("Port " + port + " opened in CSF.")
}

func srvInstall() {
	srvSection("INSTALLING MAILSERVER")
	srvDbg("Downloading from " + srvRepo)
	os.MkdirAll("/usr/local/mail/", 0o755)
	srvConfirmClean()
	srvRun(sh.C("git", "clone", srvRepo).Dir("/usr/local/mail/"))

	for _, f := range []string{srvCompose, srvEnv} {
		if !conf.IsFile(f) {
			cli.Errf("Error: download from github failed, required file missing after clone: %s", f)
			cli.Exit(1)
		}
	}

	// https://github.com/stefanpejcic/OpenPanel/discussions/1186
	os.MkdirAll(mailDir+"/docker-data/dms/config/", 0o755)
	if err := os.Rename(mailDir+"/dovecot.cf", mailDir+"/docker-data/dms/config/dovecot.cf"); err != nil {
		cli.Errf("mv: cannot stat '%s/dovecot.cf': No such file or directory", mailDir)
	}

	srvSetSSL()
	os.MkdirAll("/etc/openpanel/email/snappymail", 0o755)
	os.Remove(mailDir + "/.env")
	os.Symlink(srvEnv, mailDir+"/.env")
	srvRun(sh.C("podman-compose", "up", "-d", "mailserver", wm.Service()).Dir(mailDir).Timeout(60 * time.Second))

	// https://github.com/stefanpejcic/OpenPanel/issues/1041#issuecomment-5123435885
	srvService("add", "Dovecot & Postfix", "docker", "openadmin_mailserver")
	srvService("add", wm.Name(wm.Service()), "docker", wm.Container())

	srvEnableEmails()
	srvSection("CONFIGURING FIREWALL")
	if sh.Has("csf") {
		for _, p := range []string{"25", "143", "465", "587", "993"} {
			srvOpenCSF(p)
		}
	} else {
		fmt.Println("Error: CSF is not installed. make sure ports 25 143 465 587 and 993 are opened on external firewall, or email will not work.")
	}

	srvSection("ENABLE MAIL FOR EXISTING USERS")
	srvStart()

	// apply CSF port changes after all mailserver networks have been created
	if sh.Has("csf") {
		srvSection("RELOADING FIREWALL")
		srvRun(sh.C("csf", "-r"))
	}
}

// srvOwner is `echo "$out" | awk -F "Owner of 'X': " '{print $2}'`, the separator is a regex
func srvOwner(out, base string) string {
	re, err := regexp.Compile("Owner of '" + base + "': ")
	if err != nil {
		return ""
	}
	var res []string
	for _, l := range strings.Split(out, "\n") {
		parts := re.Split(l, -1)
		if len(parts) > 1 {
			res = append(res, parts[1])
		} else {
			res = append(res, "")
		}
	}
	return strings.TrimRight(strings.Join(res, "\n"), "\n")
}

func srvStart() bool {
	const configDir = "/etc/openpanel/caddy/domains"
	newVolumes := "    volumes:\n      - ./docker-data/dms/mail-state/:/var/mail-state/\n      - ./var/log/mail/:/var/log/mail/\n      - ./docker-data/dms/config/:/tmp/docker-mailserver/\n      - /etc/openpanel/caddy/ssl/:/etc/openpanel/caddy/ssl/:ro\n"

	if b, err := os.ReadFile(srvCompose); err == nil {
		os.WriteFile(srvCompose+".bak", b, 0o644)
	}
	v, _ := conf.Raw(conf.AdminINI, "email_storage_location")
	store := text.Xargs(v)

	if store != "/var/mail/" {
		if strings.HasPrefix(store, "/") {
			newVolumes += "      - " + store + ":/var/mail/\n"
			srvSection("STORAGE SETTINGS")
			srvDbg("Using a custom storage location for all domains:")
			srvDbg("")
			srvDbg("- MAIL STORAGE LOCATION: " + store)
			srvDbg("- MAIL SETTINGS FILE:    " + srvCompose)
		} else { // todo: this is a fallback for <1.7.3
			srvSection("MOUNT USERS HOME DIRECTORIES")
			srvDbg("Re-mounting mail directories for all domains:")
			srvDbg("")
			srvDbg("- DOMAINS DIRECTORY:     " + configDir)
			srvDbg("- MAIL SETTINGS FILE:    " + srvCompose)
			if srvDebug {
				fmt.Print("- DEFAULT VOLUMES:\n" + newVolumes)
			}
			files, _ := filepath.Glob(configDir + "/*.conf")
			sort.Strings(files)
			if len(files) == 0 {
				files = []string{configDir + "/*.conf"}
			}
			for _, file := range files {
				fmt.Printf("Processing file: %s\n", file)
				if st, err := os.Lstat(file); err == nil && st.Mode()&os.ModeSymlink != 0 {
					continue
				}
				base := strings.TrimSuffix(filepath.Base(file), ".conf")
				who, _ := sh.Opencli("domains-whoowns", base).Out()
				owner := srvOwner(who, base)
				if owner != "" {
					fmt.Printf("Domain %s skipped. No user.\n", base)
				} else {
					dir := "/home/" + owner + "/mail/" + base + "/"
					newVolumes += "      - " + dir + ":/var/mail/" + base + "/\n"
					fmt.Printf("Mount point added: - %s:/var/mail/%s/\n", dir, base)
				}
			}
		}

		srvSection("EMAIL DIRECTORIES")
		if srvDebug {
			fmt.Print("- DEFAULT VOLUMES + CONFIGURATION:\n" + newVolumes)
		}
		srvSection("UPDATE COMPOSE")
		srvRewriteCompose(srvCompose+".bak", srvCompose, newVolumes)
	}

	state := podman.State(container)
	// compose up leaves a container stuck like this alone
	if state != "" && state != "exited" && state != "stopped" && !podman.CanExec(container) {
		podman.ForceRemove(container, wm.Container())
	}
	services := []string{"mailserver"}
	// a stuck mailserver can only be removed together with the webmail
	if !sh.C("podman", "container", "exists", wm.Container()).Quiet().OK() {
		services = append(services, wm.Service())
	}
	sh.C("podman-compose", append([]string{"up", "-d"}, services...)...).Dir(mailDir).Timeout(120 * time.Second).Quiet().Run()
	if !podman.IsRunning(container) {
		cli.Errf("Error: MailServer did not start, check: opencli email-server logs")
		return false
	}
	fmt.Println("MailServer started successfully.")
	return true
}

// srvRewriteCompose swaps the volumes of the mailserver service for newVolumes, reading from the .bak copy
func srvRewriteCompose(src, dst, newVolumes string) {
	lines := recLines(conf.Read(src))
	var out strings.Builder
	in := false
	mailRe := regexp.MustCompile(`^  [a-z]`)
	homeRe := regexp.MustCompile(`^ {6}-[ ]+/home`)
	itemRe := regexp.MustCompile(`^ {6}-`)
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(l, "  mailserver:") {
			in = true
			out.WriteString(l + "\n")
			continue
		}
		if mailRe.MatchString(l) {
			in = false
		}
		if !in {
			out.WriteString(l + "\n")
			continue
		}
		if f := awkFields(l); len(f) > 0 && f[0] == "volumes:" {
			out.WriteString(newVolumes + "\n")
			for i+1 < len(lines) {
				i++
				n := lines[i]
				if homeRe.MatchString(n) {
					continue
				}
				if !itemRe.MatchString(n) {
					out.WriteString(n + "\n")
					break
				}
			}
			in = false
		} else {
			out.WriteString(l + "\n")
		}
	}
	conf.WriteInPlace(dst, out.String())
}

func srvStop() bool {
	srvSection("STOP MAILSERVER")
	// has to outlast stop_grace_period in compose.yml (1m), killing compose mid stop can leave the container stuck stopping
	srvRun(sh.C("podman-compose", "down", "mailserver").Dir(mailDir).Timeout(90 * time.Second))

	state := podman.State(container)
	if state != "" && state != "exited" && state != "stopped" {
		srvDbg("Container is still '" + state + "' after compose down, removing it.")
		if !podman.ForceRemove(container, wm.Container()) {
			cli.Errf("Error: MailServer is stuck in state '%s' and could not be removed.", state)
			return false
		}
	}
	if !srvDebug {
		fmt.Println("MailServer stopped successfully.")
	}
	return true
}

func srvUninstall() {
	srvSection("UNINSTALL MAILSERVER")
	fmt.Println("Are you sure you want to uninstall the MailServer and remove all its configuration? (y/n)")
	in, ok := readChar()
	if !ok {
		fmt.Println("")
		fmt.Println("No response received. Aborting uninstallation.")
		return
	}
	if in != "yes" && in != "Y" && in != "y" {
		fmt.Println("")
		fmt.Println("Uninstallation aborted.")
		return
	}
	// https://github.com/stefanpejcic/OpenPanel/issues/1041#issuecomment-5123435885
	srvService("remove", "Dovecot & Postfix", "", "")
	srvService("remove", "Roundcube (Webmail)", "", "")

	srvRun(sh.C("podman-compose", "down").Dir(mailDir).Timeout(30 * time.Second))
	os.RemoveAll(mailDir)
	if !srvDebug {
		fmt.Println("MailServer uninstalled successfully.")
	}
}

// the image ignores command and starts from these, keep_rates so a HUP doesn't reset the hourly counters and no request cache since cached answers aren't counted
const srvPostfwdEnv = `    environment:
      - CACHE=0
      - EXTRA=--summary=600 --noidlestats --keep_rates
`

const srvPostfwdBlock = `  postfwd:
    image: postfwd/postfwd:stable
    container_name: postfwd
` + srvPostfwdEnv + `    restart: always
    networks:
      - network
    volumes:
      - ./postfwd:/etc/postfwd
    command: >
      /usr/sbin/postfwd --config /etc/postfwd/postfwd.cf --daemon --foreground --port 10040
`

func srvFetch(url, dst string) {
	c := &http.Client{}
	resp, err := c.Get(url)
	if err != nil {
		cli.Errf("curl: %v", err)
		return
	}
	defer resp.Body.Close()
	if f, err := os.Create(dst); err == nil {
		io.Copy(f, resp.Body)
		f.Close()
	}
}

const (
	srvPostfwdPolicy = "check_policy_service inet:postfwd:10040"
	srvPostfwdMarker = "# added by: opencli email-server postfwd enable"
)

// srvCFKey is the parameter name of a "key = value" main.cf line
func srvCFKey(l string) string {
	if strings.HasPrefix(strings.TrimSpace(l), "#") {
		return ""
	}
	if i := strings.Index(l, "="); i > 0 {
		return strings.TrimSpace(l[:i])
	}
	return ""
}

// srvStripPolicy drops the postfwd check from a comma separated restriction list
func srvStripPolicy(v string) string {
	var keep []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" && p != srvPostfwdPolicy {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, ", ")
}

// srvCFSet replaces a key in postfix-main.cf, or appends it under our marker
func srvCFSet(file, key, val string) {
	lines := recLines(conf.Read(file))
	found := false
	for i, l := range lines {
		if srvCFKey(l) == key {
			lines[i] = key + " = " + val
			found = true
		}
	}
	if !found {
		lines = append(lines, srvPostfwdMarker, key+" = "+val)
	}
	out := strings.Join(lines, "\n") + "\n"
	if conf.WriteFileKeep(file, out, true) != nil {
		os.WriteFile(file, []byte(out), 0o644)
	}
}

// srvPostfwdComposeEnv puts our env vars in the postfwd service and drops the mailserver's depends_on postfwd, that one blocks recreating postfwd and postfix is fine without it (DUNNO)
// returns the new compose and true when the postfwd env changed
func srvPostfwdComposeEnv(c string) (string, bool) {
	lines := recLines(c)
	var out []string
	svc, envChanged := "", false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		if strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   ") {
			svc = strings.TrimSuffix(t, ":")
		}
		if svc == "postfwd" && (t == "environment:" || strings.HasPrefix(t, "- EXTRA=") || strings.HasPrefix(t, "- CACHE=")) {
			continue
		}
		if svc == "mailserver" && t == "depends_on:" {
			var keep []string
			for i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "- ") && strings.HasPrefix(lines[i+1], "      ") {
				i++
				if strings.TrimSpace(lines[i]) != "- postfwd" {
					keep = append(keep, lines[i])
				}
			}
			if len(keep) > 0 {
				out = append(out, l)
				out = append(out, keep...)
			}
			continue
		}
		out = append(out, l)
		if svc == "postfwd" && t == "image: postfwd/postfwd:stable" {
			out = append(out, strings.Split(strings.TrimSuffix(srvPostfwdEnv, "\n"), "\n")...)
		}
	}
	res := strings.Join(out, "\n") + "\n"
	envChanged = !strings.Contains(c, srvPostfwdEnv) && strings.Contains(res, srvPostfwdEnv)
	return res, envChanged
}

// srvPostfwdRecreate makes compose create postfwd again so env changes apply
func srvPostfwdRecreate() {
	sh.C("podman", "rm", "-f", "openadmin_postfwd").Quiet().Run()
	if out, err := sh.C("podman", "rm", "-f", "postfwd").Combined(); err != nil && strings.Contains(out, "dependent containers") {
		// the mailserver was created with depends_on postfwd, only removing them together drops that link, email is down for about a minute
		fmt.Println("postfwd keeps its old settings, the mailserver was created depending on it. To recreate them: podman rm -f --depend postfwd && cd " + mailDir + " && podman-compose up -d")
		return
	}
	sh.C("podman-compose", "up", "-d", "--no-deps", "postfwd").Dir(mailDir).Quiet().Run()
}

// srvPostfwd returns the exit code and true when the script ends there without the trailing blank line
func srvPostfwd(action string) (int, bool) {
	disabledCF := mailDir + "/postfwd/postfix-main.cf"
	activeCF := mailDir + "/docker-data/dms/config/postfix-main.cf"
	rulesCF := mailDir + "/postfwd/postfwd.cf"

	// TODO: remove afer 1.8
	if !conf.IsDir(mailDir + "/postfwd") {
		os.MkdirAll(mailDir+"/postfwd", 0o755)
		srvFetch("https://raw.githubusercontent.com/stefanpejcic/OpenMail/refs/heads/main/postfwd/postfix-main.cf", disabledCF)
		srvFetch("https://raw.githubusercontent.com/stefanpejcic/OpenMail/refs/heads/main/postfwd/postfwd.cf", rulesCF)
	}

	if !regexp.MustCompile(`(?m)^[[:space:]]*postfwd:`).MatchString(conf.Read(srvCompose)) {
		var out strings.Builder
		inserted := false
		for _, l := range recLines(conf.Read(srvCompose)) {
			if strings.HasPrefix(l, "services:") && !inserted {
				// the awk ends the block with a bare print, which repeats this line
				out.WriteString(l + "\n" + srvPostfwdBlock + l + "\n")
				inserted = true
				continue
			}
			out.WriteString(l + "\n")
		}
		tmp := srvCompose + ".tmp"
		if os.WriteFile(tmp, []byte(out.String()), 0o644) == nil {
			os.Rename(tmp, srvCompose)
		}
	}

	// older compose files have postfwd without these env vars
	orig := conf.Read(srvCompose)
	c, recreate := srvPostfwdComposeEnv(orig)
	if c != orig {
		tmp := srvCompose + ".tmp"
		if os.WriteFile(tmp, []byte(c), 0o644) == nil {
			os.Rename(tmp, srvCompose)
		}
	}

	// postfix-main.cf also holds other overrides (message_size_limit..), so only the policy check means it's on
	enabled := strings.Contains(conf.Read(activeCF), srvPostfwdPolicy)

	if recreate && enabled && action != "disable" {
		srvPostfwdRecreate()
		recreate = false
	}

	if action == "" {
		if enabled {
			fmt.Println("Rate-limiting is currently ENABLED.")
		} else {
			fmt.Println("Rate-limiting is currently DISABLED.")
		}
		return 0, true
	}

	switch action {
	case "view":
		if conf.IsFile(rulesCF) {
			fmt.Print(conf.Read(rulesCF))
		}
	case "edit":
		sh.C("nano", rulesCF).StdinReader(os.Stdin).Run()
		sh.C("podman", "kill", "--signal", "HUP", "postfwd").Quiet().Run()
	case "enable":
		if enabled {
			fmt.Println("Rate-limiting is already enabled.")
			break
		}
		// merge into the live value so dms defaults (policyd-spf, dovecot quota..) stay in
		cur, _ := srvExec("postconf", "-h", "smtpd_recipient_restrictions").Out()
		cur = strings.TrimSpace(cur)
		if cur == "" {
			cli.Errf("Error: '%s' is not running, start it with: opencli email-server start", container)
			return 1, true
		}
		val := srvPostfwdPolicy + ", " + cur
		if recreate {
			// podman-compose doesn't recreate on changes
			srvPostfwdRecreate()
		} else {
			sh.C("podman-compose", "up", "-d", "--no-deps", "postfwd").Dir(mailDir).Quiet().Run()
		}
		srvCFSet(activeCF, "smtpd_policy_service_default_action", "DUNNO")
		srvCFSet(activeCF, "smtpd_recipient_restrictions", val)
		// apply live, a podman restart skips dms setup and the file is only read on recreate
		srvExec("postconf", "-e", "smtpd_policy_service_default_action=DUNNO", "smtpd_recipient_restrictions="+val).Quiet().Run()
		srvExec("postfix", "reload").Quiet().Run()
		fmt.Println("Rate-limiting has been ENABLED.")
	case "disable":
		if !enabled {
			fmt.Println("Rate-limiting is already disabled.")
			break
		}
		var live []string
		conf.SedFile(activeCF, func(lines []string) []string {
			var out []string
			for i := 0; i < len(lines); i++ {
				l := lines[i]
				owned := false
				if l == srvPostfwdMarker && i+1 < len(lines) {
					i++
					l, owned = lines[i], true
				}
				k := srvCFKey(l)
				if k == "smtpd_policy_service_default_action" || strings.HasPrefix(l, "postfwd:10040_time_limit") {
					continue
				}
				if strings.Contains(l, srvPostfwdPolicy) {
					v := srvStripPolicy(l[strings.Index(l, "=")+1:])
					live = append(live, k+"="+v)
					// a line we added goes away so dms keeps managing that setting
					if owned {
						continue
					}
					l = k + " = " + v
				} else if owned {
					out = append(out, srvPostfwdMarker)
				}
				out = append(out, l)
			}
			return out
		})
		srvExec(append([]string{"postconf", "-e"}, live...)...).Quiet().Run()
		srvExec("postconf", "-X", "smtpd_policy_service_default_action").Quiet().Run()
		srvExec("postfix", "reload").Quiet().Run()
		sh.C("podman-compose", "down", "postfwd").Dir(mailDir).Quiet().Background()
		fmt.Println("Rate-limiting has been DISABLED.")
	default:
		fmt.Printf("Error: Unknown action '%s'. Use 'enable' or 'disable'.\n", action)
	}
	return 0, false
}

const srvUsage = `Usage:

opencli email-server status                           Show status
opencli email-server config                           Show configuration
opencli email-server install                          Install the email server  
opencli email-server start                            Start the email server
opencli email-server stop                             Stop the email server
opencli email-server restart                          Restart the email server
opencli email-server uninstall                        Uninstall the email server
opencli email-server queue                            Show mail queue
opencli email-server flush                            Flush mail queue
opencli email-server view   <queue id>                Show mail by queue id
opencli email-server unhold <queue id> [<queue id>]   Release mail that was put "on hold" (marked with '!')
opencli email-server unhold ALL                       Release all mails that were put "on hold" (marked with '!')
opencli email-server delete <queue id> [<queue id>]   Delete mail from queue
opencli email-server delete ALL                       Delete all mails from queue
opencli email-server fail2ban [<ban|unban> <IP>]      Interact with fail2ban
opencli email-server fail2ban log                     Show fail2ban log
opencli email-server ports                            Show published ports
opencli email-server postconf                         Show postfix configuration
opencli email-server logs [-f]                        Show logs. Use -f to 'follow' the logs
opencli email-server login                            Run container shell
opencli email-server supervisor                       Interact with supervisorctl
opencli email-server postfwd                          Enable/disable postfwd rate-limiting and edit limits.
opencli email-server pflogsumm                        Generate email summary reports.
opencli email-server update-check                     Check for container package updates
opencli email-server update-packages                  Update container packages
opencli email-server versions                         Show versions
`
