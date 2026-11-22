// ##############################################################################
// Script Name: internal/commands/admin/admin.go
// Description: Manage OpenAdmin service and Administrators.
// Usage: opencli admin <command> [options]
// Author: Stefan Pejcic
// Created: 01.11.2023
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

package admin

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/requirement"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "admin",
		Desc:  "Manage OpenAdmin service and Administrators.",
		Usage: "opencli admin <command> [options]",
		Run:   admin,
	})
}

const (
	green = "\033[0;32m"
	red   = "\033[0;31m"
	reset = "\033[0m"

	errorLog       = "/var/log/openpanel/admin/error.log"
	accessLog      = "/var/log/openpanel/admin/access.log"
	apiLog         = "/var/log/openpanel/admin/api.log"
	loginLog       = "/var/log/openpanel/admin/login.log"
	failedLoginLog = "/var/log/openpanel/admin/failed_login.log"
	cronLog        = "/var/log/openpanel/admin/cron.log"
	notifFile      = "/etc/openpanel/openadmin/config/notifications.ini"
	dbFile         = "/etc/openpanel/openadmin/users.db"
	enterpriseLink = "https://my.openpanel.com/index.php?rp=/store/openpanel/enterprise-license"
)

func usage() {
	fmt.Print(`Usage: opencli admin <command> [options]

Commands:
  on                                            Enable and start the OpenAdmin service.
  off                                           Stop and disable the OpenAdmin service.
  port [new_port]                               Display or update OpenAdmin port.
  log                                           Display the last 25 lines of the OpenAdmin error log.
  logs                                          Display live logs for all OpenAdminn services.
  list                                          List all current admin users.
  new <user> <pass> [--reseller|--super] [--plan=<name>] Add a new user, optionally as a reseller (on a reseller plan) or super admin.
  password <user> <pass>                        Reset the password for the specified admin user.
  update <user> --plan=<name> --allowed_plans=[] --max_accounts=<int> --max_disk_blocks=1000000 --logo_url=<url> Assign a reseller plan, limits, and branding for reseller.
  rename <old> <new>                            Change the admin username.
  suspend <user>                                Suspend admin user and the accounts a reseller owns.
  unsuspend <user>                              Unsuspend admin user and the accounts suspended along with it.
  delete <user> [--with-users]                  Delete admin user, a reseller's accounts move to admin unless --with-users deletes them too.
  notifications <command> <param> [value]       Control notification preferences.

  Notifications Commands:
    check                                       Check and write notifications.
    get <param>                                 Get the value of the specified notification parameter.
    update <param> <value>                      Update the specified notification parameter with the new value.

Examples:
  opencli admin on
  opencli admin off
  opencli admin port
  opencli admin port 8443
  opencli admin log
  opencli admin logs
  opencli admin list
  opencli admin new stefan SuperStrong1
  opencli admin new stefan SuperStrong1 --reseller
  opencli admin new stefan SuperStrong1 --reseller --plan=reseller_basic
  opencli admin update stefan --plan=reseller_pro
  opencli admin new stefan SuperStrong1 --super
  opencli admin password stefan SuperStrong2
  opencli admin rename stefan pejcic
  opencli admin suspend pejcic
  opencli admin unsuspend pejcic
  opencli admin notifications check
  opencli admin notifications get reboot
  opencli admin notifications update reboot yes
  opencli admin notifications update load 10
  opencli admin notifications update cpu 90
  opencli admin notifications update ram 95
  opencli admin notifications update du 99
  opencli admin notifications update swap 95
  opencli admin notifications update webhook_url https://discord.com/api/webhooks/XXXX/YYYYYYY
  opencli admin notifications update webhook_url "https://discord.com/api/webhooks/XXXX/YYYYYYY https://hooks.slack.com/services/XXX"
`)
	cli.Exit(1)
}

func usageNotifications() {
	fmt.Print(`Usage: opencli admin notifications <get|update> <what> <value>

 Commands:
   get <param>                                 Get the value of the specified notification parameter.
   update <param> <value>                      Update the specified notification parameter with the new value.

Examples:
  opencli admin notifications get reboot        - if 'yes' you will receive notification when server is restarted.
  opencli admin notifications get login         - if 'yes' you will receive notification for new logins to OpenAdmin.
  opencli admin notifications update update yes - receive notification when new OpenPanel update is available.
  opencli admin notifications update cpu 90     - receive notification when server CPU usage is over 90%.
  opencli admin notifications update du 95      - receive notification when server disk usage is over 95%.
  opencli admin notifications update webhook_url https://discord.com/api/webhooks/XXXX/YYYYYYY

List of all available options: https://openpanel.com/docs/opencli/admin/
`)
	cli.Exit(1)
}

// sq runs sqlite3 on users.db, stderr goes to the terminal like in the script
func sq(sqls ...string) string {
	return sh.C("sqlite3", append([]string{dbFile}, sqls...)...).Str()
}

// sqlite doesn't choke on the string literal once single quotes are doubled
func esc(s string) string { return strings.ReplaceAll(s, "'", "''") }

func sentinel(action, title, message string) {
	sh.Opencli("sentinel", "--action="+action, "--title="+title, "--message="+message).Quiet().Background()
}

// licenseKey is grep "^key=" openpanel.config | cut -d'=' -f2-
func licenseKey() string {
	var v []string
	for _, l := range conf.Lines(conf.Panel) {
		if strings.HasPrefix(l, "key=") {
			v = append(v, l[4:])
		}
	}
	return strings.TrimRight(strings.Join(v, "\n"), "\n")
}

func between(start, end string) []string {
	var out []string
	flag := false
	for _, l := range conf.Lines(conf.Caddyfile) {
		if strings.Contains(l, start) {
			flag = true
			continue
		}
		if strings.Contains(l, end) {
			flag = false
		}
		if flag {
			out = append(out, l)
		}
	}
	return out
}

var (
	blankRe   = regexp.MustCompile(`^\s*$`)
	commentRe = regexp.MustCompile(`^\s*#`)
	braceRe   = regexp.MustCompile(`[[:space:]]*\{`)
	portRe    = regexp.MustCompile(`localhost:([0-9]+)`)
)

func firstHost(block []string) string {
	for _, l := range block {
		if blankRe.MatchString(l) || commentRe.MatchString(l) {
			continue
		}
		if loc := braceRe.FindStringIndex(l); loc != nil {
			l = l[:loc[0]] + l[loc[1]:]
		}
		return text.Xargs(l)
	}
	return ""
}

func adminPort() string {
	if conf.Exists("/root/disable_2087_port") {
		return "443"
	}
	for _, l := range between("# START HOSTNAME DOMAIN #", "# END HOSTNAME DOMAIN #") {
		if m := portRe.FindStringSubmatch(l); m != nil {
			return m[1]
		}
	}
	return ""
}

func isFile(p string) bool { return conf.IsFile(p) }

func adminURL() string {
	domain := firstHost(between("# START HOSTNAME DOMAIN #", "# END HOSTNAME DOMAIN #"))
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimPrefix(domain, "https://")
	port := adminPort()
	le := "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/"
	if domain != "" && domain != "example.net" {
		cust := "/etc/openpanel/caddy/ssl/custom/" + domain + "/" + domain
		if isFile(le+domain+"/"+domain+".crt") && isFile(le+domain+"/"+domain+".key") || isFile(cust+".crt") && isFile(cust+".key") {
			return "https://" + domain + ":" + port + "/"
		}
		return "http://" + domain + ":" + port + "/"
	}
	ip := firstHost(between("# START HOSTNAME IP #", "# END HOSTNAME IP #"))
	if ip != "" {
		// Let's Encrypt shortlived
		if isFile(le+ip+"/"+ip+".crt") && isFile(le+ip+"/"+ip+".key") {
			return "https://" + ip + ":" + port + "/"
		}
		return "http://" + ip + ":" + port + "/"
	}
	return "http://" + publicIP() + ":" + port + "/"
}

func status() {
	if sh.C("systemctl", "is-active", "--quiet", "admin").OK() {
		fmt.Println(green + "●" + reset + " OpenAdmin is running and is available on: " + adminURL())
	} else if conf.Exists("/root/openadmin_is_disabled") {
		fmt.Println(red + "×" + reset + " OpenAdmin is disabled. To enable it run 'opencli admin on' ")
	} else {
		fmt.Println(red + "×" + reset + " OpenAdmin is not running. To enable it run 'opencli admin on' ")
	}
}

// https://github.com/stefanpejcic/OpenPanel/issues/993
// TODO: remove after 1.8+
func ensureTOTP() {
	if !conf.IsFile(dbFile) {
		return
	}
	cols := map[string]bool{}
	for _, l := range strings.Split(sh.C("sqlite3", dbFile, "PRAGMA table_info(user);").NoErr().Str(), "\n") {
		if f := strings.Split(l, "|"); len(f) > 1 {
			cols[f[1]] = true
		}
	}
	if !cols["totp_secret"] {
		sh.C("sqlite3", dbFile, "ALTER TABLE user ADD COLUMN totp_secret TEXT;").NoErr().Run()
	}
	if !cols["totp_enabled"] {
		sh.C("sqlite3", dbFile, "ALTER TABLE user ADD COLUMN totp_enabled BOOLEAN DEFAULT 0 NOT NULL;").NoErr().Run()
	}
}

func getConfig(param string) {
	re := keyRe(param)
	var vals []string
	exists := false
	for _, l := range conf.Lines(notifFile) {
		if loc := re.FindStringIndex(l); loc != nil {
			exists = true
			vals = append(vals, l[loc[1]:])
		}
	}
	switch v := strings.TrimRight(strings.Join(vals, "\n"), "\n"); {
	case v != "":
		fmt.Println(v)
	case exists:
		fmt.Printf("Parameter %s has no value.\n", param)
	default:
		fmt.Printf("Parameter %s does not exist. Docs: https://openpanel.com/docs/opencli/admin/\n", param)
	}
}

// the name is a regex anchored at line start, like grep and sed had it
func keyRe(param string) *regexp.Regexp {
	re, err := regexp.Compile("^" + param + "=")
	if err != nil {
		re = regexp.MustCompile("^" + regexp.QuoteMeta(param) + "=")
	}
	return re
}

var paramRe = regexp.MustCompile(`^[a-z_]+$`)

func updateConfig(param, value string) {
	re := keyRe(param)
	lines := conf.Lines(notifFile)
	found := false
	for _, l := range lines {
		if re.MatchString(l) {
			found = true
		}
	}
	if found {
		// the value goes in literally so & or | in a webhook URL don't mangle it
		full := regexp.MustCompile(re.String() + ".*")
		conf.SedFile(notifFile, func(lines []string) []string {
			for i, l := range lines {
				if loc := full.FindStringIndex(l); loc != nil {
					lines[i] = param + "=" + value + l[loc[1]:]
				}
			}
			return lines
		})
		fmt.Printf("Updated %s to %s\n", param, value)
		return
	}
	// newer alerts aren't in older files, add them under the section OpenAdmin reads them from
	if !paramRe.MatchString(param) {
		fmt.Println("Invalid parameter name: " + param)
		return
	}
	section := "ACTIONS"
	switch param {
	case "reboot", "attack", "ratelimit", "limit", "backup", "update", "dns", "login", "ssh", "services", "load", "cpu", "ram", "du", "swap", "mail", "mail_bounce", "mail_defer", "rbl", "webhook_url":
		section = "DEFAULT"
	case "max_total_conn", "max_conn_per_ip":
		section = "HTTP"
	}
	has := false
	for _, l := range lines {
		if strings.HasPrefix(l, "["+section+"]") {
			has = true
		}
	}
	if has {
		conf.SedFile(notifFile, func(lines []string) []string {
			var out []string
			for _, l := range lines {
				out = append(out, l)
				if strings.HasPrefix(l, "["+section+"]") {
					out = append(out, param+"="+value)
				}
			}
			return out
		})
	} else if f, err := os.OpenFile(notifFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
		fmt.Fprintf(f, "\n[%s]\n%s=%s\n", section, param, value)
		f.Close()
	}
	fmt.Printf("Added %s=%s\n", param, value)
}

func tailLines(path string, n int) {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tail: cannot open '%s' for reading: No such file or directory\n", path)
		return
	}
	s := string(b)
	lines := strings.SplitAfter(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	fmt.Print(strings.Join(lines, ""))
}

func multitailLogs() {
	requirement.Command("multitail")
	key := licenseKey()
	files := []string{errorLog, accessLog, loginLog, failedLoginLog, cronLog}
	all := []string{errorLog, accessLog, apiLog, loginLog, failedLoginLog, cronLog}
	if key == "" {
		files = append(files, apiLog)
	}
	for _, f := range files {
		if !conf.Exists(f) {
			if fh, err := os.OpenFile(f, os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
				fh.Close()
			}
		}
	}
	args := []string{errorLog, accessLog, loginLog, failedLoginLog, cronLog}
	if key == "" {
		args = all
	}
	sh.C("multitail", args...).StdinReader(os.Stdin).Run()
}

var digitsRe = regexp.MustCompile(`^[0-9]+$`)

func setPort(newPort, flag string) {
	if !digitsRe.MatchString(newPort) || !(len(strings.TrimLeft(newPort, "0")) > 3 || text.Atoi(newPort) > 443) {
		fmt.Println("Error: OpenAdmin port number must be greater than 443.")
		cli.Exit(1)
	}
	fmt.Println("Changing port to: " + newPort)
	podman.SedInPlace(conf.Caddyfile,
		"-e", `/# START HOSTNAME DOMAIN #/,/# END HOSTNAME DOMAIN #/ s/\(reverse_proxy localhost:\)[0-9]\+/\1`+newPort+`/`,
		"-e", `/# START HOSTNAME IP #/,/# END HOSTNAME IP #/ s/\(reverse_proxy localhost:\)[0-9]\+/\1`+newPort+`/`)
	os.RemoveAll("/root/disable_2087_port")
	num := regexp.MustCompile(`:[0-9]+`)
	for i := 0; i < 2; i++ {
		conf.SedFile("/etc/openpanel/caddy/redirects.conf", func(lines []string) []string {
			for j, l := range lines {
				if strings.Contains(l, "redir @openadmin") {
					lines[j] = num.ReplaceAllLiteralString(l, ":"+newPort)
				}
			}
			return lines
		})
	}
	conf.SedFile("/etc/openpanel/nginx/vhosts/openpanel_proxy.conf", func(lines []string) []string {
		in := false
		for j, l := range lines {
			if !in {
				if strings.Contains(l, "# openadmin") {
					in = true
					lines[j] = num.ReplaceAllLiteralString(l, ":"+newPort)
				}
				continue
			}
			lines[j] = num.ReplaceAllLiteralString(l, ":"+newPort)
			if strings.Contains(l, "# roundcube") {
				in = false
			}
		}
		return lines
	})
	// opening the port and reloading caddy aren't restarts, OpenAdmin passes --no-restart and still needs both
	csf := "/etc/csf/csf.conf"
	if conf.IsFile(csf) && !regexp.MustCompile(`TCP_IN = .*`+newPort).MatchString(conf.Read(csf)) {
		fmt.Println("Opening port on firewall..")
		tcp := regexp.MustCompile(`TCP_IN = "(.*)"`)
		conf.SedFile(csf, func(lines []string) []string {
			for j, l := range lines {
				if m := tcp.FindStringSubmatchIndex(l); m != nil {
					lines[j] = l[:m[0]] + `TCP_IN = "` + l[m[2]:m[3]] + "," + newPort + `"` + l[m[1]:]
				}
			}
			return lines
		})
		sh.C("csf", "-r").Quiet().Background()
	}
	fmt.Println("Reloading Caddy..")
	podman.CaddyReload()
	if flag != "--no-restart" {
		fmt.Println("Restarting OpenAdmin..")
		sh.C("systemctl", "restart", "admin").Run()
	} else {
		fmt.Println("Restart OpenAdmin service to apply new port.")
	}
	sentinel("admin_port", "OpenAdmin port changed", "Port for administrator-level panel has been changed to '"+newPort+"'.")
	fmt.Println("Done")
}

func admin(args []string) int {
	requirement.Command("jq")
	ensureTOTP()
	a := func(i int) string { return cli.Arg(args, i) }
	switch a(0) {
	case "on":
		fmt.Println("Enabling the OpenAdmin...")
		os.Remove("/root/openadmin_is_disabled")
		sh.C("systemctl", "enable", "--now", "admin").Quiet().Run()
		status()
		sentinel("admin_status", "OpenAdmin enabled", "Administrator-level panel has been enabled.")
	case "log":
		fmt.Println("Restarting OpenAdmin service:")
		sh.C("systemctl", "enable", "--now", "admin").Quiet().Run()
		fmt.Println("tail -f 25 " + errorLog)
		fmt.Println("")
		sh.C("systemctl", "restart", "admin").Run()
		tailLines(errorLog, 25)
		fmt.Println("")
	case "logs":
		multitailLogs()
	case "off":
		fmt.Println("Disabling the OpenAdmin...")
		sentinel("admin_status", "OpenAdmin disabled", "Administrator-level panel has been disabled.")
		sh.C("systemctl", "disable", "--now", "admin").Quiet().Run()
		if f, err := os.OpenFile("/root/openadmin_is_disabled", os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			f.Close()
		}
		status()
	case "help":
		usage()
	case "password":
		if !conf.Exists(dbFile) {
			fmt.Println("Error: File " + dbFile + " does not exist, password not changed for user.")
		} else if a(2) != "" {
			validatePassword(a(2))
			updatePassword(a(1), a(2))
		}
	case "rename":
		validateName(a(2), "New Username")
		rename(a(1), a(2))
	case "list":
		fmt.Println(sq("SELECT username, role, is_active FROM user;"))
	case "update":
		if !updateReseller(args[1:]) {
			return 1
		}
	case "suspend", "unsuspend":
		manageUser(a(0), a(1))
	case "port":
		if a(1) != "" {
			setPort(a(1), a(2))
		} else {
			fmt.Println(adminPort())
		}
	case "new":
		newUser(a(1), a(2), a(3), a(4))
	case "notifications":
		cmd, param := a(1), a(2)
		if cmd != "check" && (cmd == "" || param == "") {
			usageNotifications()
		}
		switch cmd {
		case "check":
			sh.Opencli("sentinel").Run()
			return 0
		case "get":
			getConfig(param)
		case "update":
			if len(args) != 4 {
				fmt.Println("ERROR: Usage: opencli admin notifications update <parameter_name> <new_value>")
				return 1
			}
			updateConfig(param, a(3))
		default:
			fmt.Println("ERROR: Invalid command.")
			usage()
		}
	case "delete":
		if !deleteUser(a(1), a(2)) {
			return 1
		}
	default:
		if a(0) == "" {
			status()
		} else {
			fmt.Printf("ERROR: Unknown command: '%s'\n", a(0))
			usage()
		}
	}
	return 0
}
