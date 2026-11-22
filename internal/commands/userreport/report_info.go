// ##############################################################################
// Script Name: internal/commands/userreport/report_info.go
// Description: Prints a health report for a user: what is working and what needs attention.
// Usage: opencli user-report <USERNAME|--all> [--section <name,...>] [--json]
// Author: Stefan Pejcic
// Created: 02.10.2026
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

package userreport

import (
	"fmt"
	"os"
	osuser "os/user"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opencli/internal/conf"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

// ipJSON is jq -r '.ip // empty' on the user's ip.json
func ipJSON(name string) string {
	o, err := ojson.ParseObject(readFile(rpUsersCore + "/" + name + "/ip.json"))
	if err != nil {
		return ""
	}
	v, _ := o.Get("ip")
	if v == nil || v == false {
		return ""
	}
	return ojson.Text(v)
}

func secInfo(s *sec) {
	u := s.u
	s.section("INFORMATION")
	s.row("Username", u.name)
	if u.suspended != "" {
		s.row("Status", "Suspended (since "+u.suspended+")")
		s.warn("Account is suspended, its websites and services are offline.")
	} else {
		s.row("Status", "Active")
	}
	s.row("Owner", u.owner)
	s.row("Email", or(u.email, "-"))
	s.row("Created", u.registered)
	s.row("Context", u.context)

	if _, err := osuser.Lookup(u.context); err != nil {
		s.err("System user " + u.context + " does not exist, the account is broken.")
	} else if !conf.IsDir("/home/" + u.context) {
		s.err("Home directory /home/" + u.context + " is missing.")
	}

	ipType := "shared"
	ip := ipJSON(u.name)
	if ip != "" {
		ipType = "dedicated"
	} else {
		ip = s.r.serverIP
	}
	s.row("IP address", or(ip, "-")+" ("+ipType+")")

	infoServer(s)
	infoPlan(s)
	infoFeatures(s)
	infoOpenpanel(s)
	infoContainers(s)
	infoResources(s)
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func unameField(f func(*syscall.Utsname) [65]int8) string {
	var u syscall.Utsname
	syscall.Uname(&u)
	a := f(&u)
	var b []byte
	for _, c := range a {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

// uptimeP is uptime -p without the "up "
func uptimeP() string {
	out := sh.C("uptime", "-p").NoErr().Str()
	return strings.TrimPrefix(out, "up ")
}

func meminfo(key string) string {
	for _, l := range strings.Split(readFile("/proc/meminfo"), "\n") {
		if strings.HasPrefix(l, key+":") {
			f := strings.Fields(l)
			if len(f) > 1 {
				return f[1]
			}
		}
	}
	return ""
}

func infoServer(s *sec) {
	s.subsection("SERVER")
	f := s.r.needFacts()
	s.row("Hostname", f["hostname"])
	s.row("OS", or(f["os"], "unknown")+", kernel "+unameField(func(u *syscall.Utsname) [65]int8 { return u.Release }))
	s.row("Time", time.Now().Format("2006-01-02 15:04:05 MST (UTC-07:00)")+", up "+uptimeP())

	// flagged when the whole server is under pressure
	cores := countCPUs()
	cpuModel := ""
	for _, l := range strings.Split(readFile("/proc/cpuinfo"), "\n") {
		if strings.Contains(l, "model name") {
			if i := strings.IndexByte(l, ':'); i >= 0 {
				cpuModel = strings.TrimLeft(l[i+1:], " ")
			}
			break
		}
	}
	la := bread(strings.SplitN(readFile("/proc/loadavg"), "\n", 2)[0], " \t\n", 4)
	mc := ""
	if cpuModel != "" {
		mc = ", " + cpuModel
	}
	s.row("CPU", fmt.Sprintf("%d cores, %s%s", cores, unameField(func(u *syscall.Utsname) [65]int8 { return u.Machine }), mc))
	s.row("Load", la[0]+", "+la[1]+", "+la[2]+" (1, 5, 15 min)")
	l5 := awkNum(la[1])
	if l5 >= float64(cores*2) {
		s.err(fmt.Sprintf("Server load (%s) is over twice the CPU cores (%d).", la[1], cores))
	} else if l5 >= float64(cores) {
		s.warn(fmt.Sprintf("Server load (%s) is above the CPU cores (%d).", la[1], cores))
	}

	total, avail := meminfo("MemTotal"), meminfo("MemAvailable")
	if total != "" && avail != "" {
		t, a := bashInt(total), bashInt(avail)
		pct := (t - a) * 100 / t
		s.row("RAM", fmt.Sprintf("%s used of %s (%d%%)", humanKB(fmt.Sprint(t-a)), humanKB(total), pct))
		if pct >= 95 {
			s.err(fmt.Sprintf("Server RAM is %d%% used.", pct))
		} else if pct >= 90 {
			s.warn(fmt.Sprintf("Server RAM is %d%% used.", pct))
		}
	}

	var st syscall.Statfs_t
	if syscall.Statfs("/home", &st) == nil {
		// same numbers df -Pk prints
		bs := uint64(st.Frsize)
		tot := st.Blocks * bs / 1024
		free := st.Bavail * bs / 1024
		used := (st.Blocks - st.Bfree) * bs / 1024
		pct := uint64(0)
		if used+free > 0 {
			pct = (used*100 + used + free - 1) / (used + free)
		}
		s.row("Disk", fmt.Sprintf("%s used of %s, %s free (%d%%)", humanKB(fmt.Sprint(used)), humanKB(fmt.Sprint(tot)), humanKB(fmt.Sprint(free)), pct))
		if pct >= 95 {
			s.err(fmt.Sprintf("Server disk is %d%% full.", pct))
		} else if pct >= 90 {
			s.warn(fmt.Sprintf("Server disk is %d%% full.", pct))
		}
	}
}

// countCPUs is nproc, the cpus this process may run on
func countCPUs() int {
	var set [16]uint64
	if _, _, e := syscall.RawSyscall(syscall.SYS_SCHED_GETAFFINITY, 0, uintptr(len(set)*8), uintptr(unsafePtr(&set))); e != 0 {
		return 1
	}
	n := 0
	for _, w := range set {
		for ; w != 0; w &= w - 1 {
			n++
		}
	}
	if n == 0 {
		n = 1
	}
	return n
}

func infoPlan(s *sec) {
	s.subsection("PLAN")
	p := s.u.planRow
	if p["id"] == "" {
		s.row("Plan", or(s.u.plan, "none"))
		s.err("Plan (id " + or(s.u.planID, "none") + ") not found, limits can't be applied.")
		return
	}
	s.lSites, s.lDomains, s.lDBs, s.lEmails, s.lFTP, s.lDisk, s.lInodes = p["websites"], p["domains"], p["dbs"], p["emails"], p["ftp"], p["disk"], p["inodes"]
	s.row("Plan", s.u.plan)
	s.row("CPU / RAM", limitLabel(p["cpu"])+" cores / "+limitLabel(p["ram"]))
	s.row("Max mailbox quota", limitLabel(p["mailquota"]))
	s.row("Max emails per hour", limitLabel(p["hourly"]))
}

func infoFeatures(s *sec) {
	s.subsection("FEATURES")
	u := s.u
	// same lookup as LoadUserFeatures in the panel: user's features.txt, else the plan's set, else default.txt
	var file, src string
	if conf.IsFile("/home/" + u.context + "/features.txt") {
		file, src = "/home/"+u.context+"/features.txt", "user override"
	} else if conf.IsFile("/etc/openpanel/openpanel/features/" + u.features + ".txt") {
		file, src = "/etc/openpanel/openpanel/features/"+u.features+".txt", "plan feature set "+u.features
	} else {
		file, src = "/etc/openpanel/openpanel/features/default.txt", "default"
		s.warn("Feature set file for '" + u.features + "' is missing, the user gets default.txt.")
	}
	mods := s.r.panelConfig("enabled_modules")
	modules := breadArray(mods, ",")
	var allowed, offServer []string
	for _, l := range strings.Split(readFile(file), "\n") {
		m := strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' || r == '\r' || r == '\v' || r == '\f' {
				return -1
			}
			return r
		}, l)
		if m == "" {
			continue
		}
		if strings.Contains(","+mods+",", ","+m+",") {
			allowed = append(allowed, m)
		} else {
			offServer = append(offServer, m)
		}
	}
	s.row("Features from", file+" ("+src+")")
	if len(allowed) == len(modules) {
		s.row("User modules", fmt.Sprintf("all %d modules enabled on the server", len(modules)))
	} else {
		var missing []string
		joined := " " + strings.Join(allowed, " ") + " "
		for _, m := range modules {
			if !strings.Contains(joined, " "+m+" ") {
				missing = append(missing, m)
			}
		}
		s.row("User modules", fmt.Sprintf("%d of %d enabled on the server", len(allowed), len(modules)))
		// whichever list is shorter
		if len(allowed) <= len(missing) {
			s.rowList("Allowed for user", allowed...)
		} else {
			s.rowList("Not allowed for user", missing...)
		}
	}
	if len(offServer) > 0 {
		s.rowList("Off on server", offServer...)
	}
}

func infoOpenpanel(s *sec) {
	s.subsection("OPENPANEL")
	f := s.r.needFacts()
	s.row("OpenPanel", or(f["version"], "unknown")+" ("+f["license"]+")")
	s.row("OpenCLI", or(f["opencli"], "unknown"))
	if f["panel_url"] != "" {
		s.row("Panel domain", f["panel_url"])
	}
	// same check as 'opencli admin'
	switch f["admin"] {
	case "running":
		v := "running"
		if f["admin_url"] != "" {
			v += " on " + f["admin_url"]
		}
		s.row("OpenAdmin", v)
	case "disabled":
		s.row("OpenAdmin", "disabled")
	default:
		s.row("OpenAdmin", "not running")
		s.warn("OpenAdmin is not running but wasn't disabled, start it with 'opencli admin on'.")
	}
}

var hostPortRe = regexp.MustCompile(`:[0-9]+(-[0-9]+)?->`)

func infoContainers(s *sec) {
	s.subsection("SYSTEM CONTAINERS")
	// rootful containers that every account depends on
	ps := s.r.needRootPS()
	if ps == "" {
		s.err("No system containers found, podman isn't responding.")
		return
	}
	s.p("  %s%s %s %s%s\n", s.r.cDim, padR("Container", 24), padR("Status", 26), "Ports", s.r.cReset)
	for _, l := range hereLines(ps) {
		c := strings.Split(l, "\t")
		f := bread(idxS(c, 0)+"\t"+idxS(c, 2)+"\t"+idxS(c, 3), "\t", 3)
		name, status, ports := f[0], f[1], f[2]
		// just the published host ports, e.g. "25,110,143"
		pl := uniqNumSorted(hostPortRe.FindAllString(ports, -1))
		s.p("  %s %s %s\n", padR(name, 24), padR(status, 26), or(pl, "-"))
		if !strings.HasPrefix(status, "Up") {
			s.err("System container " + name + " is not running (" + status + ").")
		} else if strings.Contains(status, "unhealthy") {
			s.warn("System container " + name + " is unhealthy.")
		}
	}
}

// uniqNumSorted is sed 's/^://; s/->$//' | sort -un | paste -sd,
func uniqNumSorted(ms []string) string {
	type it struct {
		s string
		n float64
	}
	var items []it
	for _, m := range ms {
		p := strings.TrimSuffix(strings.TrimPrefix(m, ":"), "->")
		items = append(items, it{p, awkNum(p)})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].n < items[j].n })
	var out []string
	for i, x := range items {
		if i > 0 && x.n == items[i-1].n {
			continue
		}
		out = append(out, x.s)
	}
	return strings.Join(out, ",")
}

// restrictedList turns "a b c" into 'a','b','c'
func restrictedList(v string) string {
	var out []string
	for _, f := range strings.Fields(v) {
		out = append(out, "'"+f+"'")
	}
	return strings.Join(out, ",")
}

func isSocket(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

// grepCount is grep -c pattern file, "" when the file can't be read
func grepCount(file string, match func(string) bool) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	n := 0
	for _, l := range awkLines(string(b)) {
		if match(l) {
			n++
		}
	}
	return strconv.Itoa(n)
}

func infoResources(s *sec) {
	s.subsection("RESOURCES")
	u := s.u
	// counted the same way as the dashboard
	mainD, subD := 0, 0
	if u.domains != "" {
		d := strings.Split(u.domains, "\n")
		for i := range d {
			sub := false
			for j := range d {
				if i != j && strings.HasSuffix(d[i], "."+d[j]) {
					sub = true
					break
				}
			}
			if sub {
				subD++
			} else {
				mainD++
			}
		}
	}
	sites := countLines(u.siteRows)
	sock := "/home/" + u.context + "/sockets/mysqld/mysqld.sock"
	dbs := ""
	if !isSocket(sock) {
		dbs = "0"
	} else {
		restricted := restrictedList(s.r.panelConfig("mysql_restricted_databases", "information_schema performance_schema mysql phpmyadmin sys mariadb.sys"))
		// --defaults-file, not -extra-file, so /etc/my.cnf's tcp host doesn't send this to the panel db
		dbs = sh.C("mariadb", "--defaults-file=/home/"+u.context+"/my.cnf", "--socket="+sock, "-N", "-B", "-e",
			"SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name NOT IN ("+restricted+")").Timeout(5 * time.Second).NoErr().Str()
	}
	emails := grepCount(rpUsersCore+"/"+u.name+"/emails.yml", func(l string) bool { return strings.HasPrefix(l, "*") })
	ftp := grepCount("/etc/openpanel/ftp/users/"+u.context+"/users.list", func(l string) bool { return l != "" })

	q := s.r.quotaRow(u.context)
	diskUsed, inodesUsed := "", ""
	if q != nil {
		f := bread(idxS(q, 1)+" "+idxS(q, 3), " \t\n", 2)
		diskUsed, inodesUsed = f[0], f[1]
	}

	s.p("  %s%s %s %s%s\n", s.r.cDim, padR("Resource", 24), padR("Used", 12), padR("Limit", 12), s.r.cReset)
	var reached []string
	for _, l := range []string{"Websites|" + strconv.Itoa(sites) + "|" + s.lSites, "Domains|" + strconv.Itoa(mainD) + "|" + s.lDomains,
		"Databases|" + or(dbs, "?") + "|" + s.lDBs, "Email accounts|" + or(emails, "0") + "|" + s.lEmails, "FTP accounts|" + or(ftp, "0") + "|" + s.lFTP} {
		f := bread(l, "|", 3)
		name, used, limit := f[0], f[1], f[2]
		s.p("  %s %s %s\n", padR(name, 24), padR(used, 12), padR(limitLabel(limit), 12))
		if reDigits.MatchString(used) && reDigits.MatchString(limit) && bashInt(limit) > 0 && bashInt(used) >= bashInt(limit) {
			reached = append(reached, name)
			s.warn(name + " limit reached (" + used + " of " + limit + ").")
		}
	}
	s.p("  %s %s %s\n", padR("Subdomains", 24), padR(strconv.Itoa(subD), 12), padR("-", 12))
	du := "-"
	if diskUsed != "" {
		du = humanKB(diskUsed)
	}
	s.p("  %s %s %s\n", padR("Disk", 24), padR(du, 12), padR(limitLabel(s.lDisk), 12))
	s.p("  %s %s %s\n", padR("Inodes", 24), padR(or(inodesUsed, "-"), 12), padR(limitLabel(s.lInodes), 12))
	if !isSocket(sock) {
		s.dim("MySQL isn't running, databases counted as 0.")
	} else if dbs == "" {
		s.dim("MySQL didn't answer in 5 seconds, databases not counted.")
	}

	if len(reached) > 0 && regexp.MustCompile(`(?m)^key=enterprise`).MatchString(readFile(rpPanelConfig)) {
		upsell := ""
		if u.planRow["upsell_url"] != "" {
			upsell = u.planRow["upsell"]
		}
		if upsell != "" {
			s.info("Upgrade to the " + upsell + " plan for higher limits.")
		}
	}
}

// key|default|title|parent, same list and defaults as notificationDefs in the panel
const notificationDefs = `notify_account_login|1|New login|
notify_account_login_for_known_netblock|0|Also for known IP addresses|notify_account_login
notify_account_login_notification_disabled|1|Email if login alerts get turned off|notify_account_login
notify_password_change|1|Password changed|
notify_password_change_notification_disabled|1|Email if this alert gets turned off|notify_password_change
notify_contact_address_change|1|Contact email changed|
notify_contact_address_change_notification_disabled|1|Email if this alert gets turned off|notify_contact_address_change
notify_twofactorauth_change|1|Two-factor authentication changed|
notify_twofactorauth_change_notification_disabled|1|Email if this alert gets turned off|notify_twofactorauth_change
notify_passkey_change|1|Passkey added or removed|
notify_passkey_change_notification_disabled|1|Email if this alert gets turned off|notify_passkey_change
notify_api_token_change|1|API token created or revoked|
notify_api_token_change_notification_disabled|1|Email if this alert gets turned off|notify_api_token_change
notify_ssl_expiry|1|SSL certificate problem|
notify_malware_found|1|Malware found|
notify_wp_vulnerability|1|Vulnerable WordPress site|
notify_disk_limit|1|Disk space running out|
notify_email_quota_limit|1|Mailbox almost full|
notify_email_ratelimit|1|Hourly email limit reached|
notify_service_failed|1|Service stopped|
notify_ratelimit|0|Visitors blocked by rate limit|
notify_daily_summary|0|Daily summary|
notify_error_logs|0|Daily error report|`

func noSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f' {
			return -1
		}
		return r
	}, s)
}

var (
	htmlTagRe = regexp.MustCompile(`<[^>]*>`)
	spaceRun  = regexp.MustCompile(`[ \t\n\r\v\f]+`)
)

func secAccount(s *sec) {
	u := s.u
	s.section("ACCOUNT")

	// same chain as the panel: user's locale file, else the server default, else en
	defLocale := noSpace(readFile("/etc/openpanel/openpanel/default_locale"))
	locale, src := noSpace(readFile("/home/"+u.context+"/locale")), "user"
	if locale == "" {
		locale, src = or(defLocale, "en"), "server default"
	}
	s.row("Locale", locale+" ("+src+")")
	if locale != "en" && !conf.IsFile("/etc/openpanel/openpanel/translations/"+locale+"/LC_MESSAGES/messages.po") {
		s.warn("Locale " + locale + " is not installed, the panel falls back to the browser language or English.")
	}

	// extras the admin may have set for this user
	notes := ""
	for _, l := range awkLines(readFile("/home/" + u.context + "/notes.txt")) {
		if l != "" {
			notes = cutC(l, 60)
			break
		}
	}
	s.row("Admin notes", or(notes, "none"))
	// custom_message.html is shown on the user's dashboard, first bit of its text without the html
	msgFile := rpUsersCore + "/" + u.name + "/custom_message.html"
	if st, err := os.Stat(msgFile); err == nil && st.Size() > 0 {
		t := htmlTagRe.ReplaceAllString(readFile(msgFile), " ")
		t = spaceRun.ReplaceAllString(t, " ")
		t = strings.TrimPrefix(t, " ")
		t = trimNL(cutC(t, 60))
		s.row("Custom message", "set: "+or(t, "(no text)"))
	} else {
		s.row("Custom message", "none")
	}

	s.subsection("NOTIFICATIONS")
	// send_user_email in lib/email.sh needs all of these to deliver anything
	canSend := true
	if u.email == "" {
		s.warn("No contact email is set, the user gets no notification emails.")
		canSend = false
	}
	if !s.r.modulesHave("notifications") {
		s.warn("Notifications module is disabled on the server, no notification emails are sent.")
		canSend = false
	}
	if !sh.C("systemctl", "is-active", "--quiet", "admin").Quiet().OK() {
		s.warn("OpenAdmin is not running, notification emails can't be sent until it is.")
		canSend = false
	}
	s.row("Sent to", or(u.email, "-"))
	s.row("Mail server", or(s.r.panelConfig("mail_server"), "localhost (built-in mail server)"))
	if canSend {
		s.ok("Notification emails can be delivered.")
	}

	prefs := rpUsersCore + "/" + u.name + "/notifications.yaml"
	if !conf.IsFile(prefs) {
		s.dim("No preferences saved yet, defaults apply.")
	}
	prefText := readFile(prefs)
	s.p("  %s%s %s%s\n", s.r.cDim, padR("Alert", 44), "Email", s.r.cReset)
	var off []string
	for _, l := range strings.Split(notificationDefs, "\n") {
		f := bread(l, "|", 4)
		key, def, title, parent := f[0], f[1], f[2], f[3]
		re := regexp.MustCompile(`^[[:space:]]*` + key + `[[:space:]]*=`)
		value := ""
		for _, pl := range strings.Split(prefText, "\n") {
			if re.MatchString(pl) {
				value = noSpace(awkPart(pl, "=", 2))
				break
			}
		}
		if value != "0" && value != "1" {
			value = def
		}
		label := s.r.cDim + "off" + s.r.cReset
		if value == "1" {
			label = s.r.cGreen + "on" + s.r.cReset
		}
		if parent != "" {
			s.p("    %s %s\n", padR(title, 42), label)
		} else {
			s.p("  %s %s\n", padR(title, 44), label)
			if value == "0" {
				off = append(off, title)
			}
		}
	}
	if len(off) > 0 {
		s.info("Turned off by the user: " + strings.Join(off, ", ") + ".")
	}

	s.subsection("FAVORITES")
	fav := rpUsersCore + "/" + u.name + "/favorites.json"
	max := s.r.panelConfig("favorites_items", "10")
	if st, err := os.Stat(fav); err != nil || st.Size() == 0 {
		s.row("Favorites", "none")
		return
	}
	v, err := ojson.Parse(readFile(fav))
	if err != nil {
		s.err("Favorites file " + fav + " is not valid JSON, favorites won't load.")
		return
	}
	s.row("Favorites", jqLength(v)+" of "+max)
	if arr, ok := v.([]any); ok {
		for _, it := range arr {
			o, ok := it.(*ojson.Object)
			if !ok {
				continue
			}
			t, _ := o.Get("title")
			lk, _ := o.Get("link")
			f := bread(ojson.Text(t)+"\t"+ojson.Text(lk), "\t", 2)
			s.p("  %s %s\n", padR(f[0], 24), f[1])
		}
	}
}

// jqLength is jq 'length'
func jqLength(v any) string {
	switch t := v.(type) {
	case []any:
		return strconv.Itoa(len(t))
	case *ojson.Object:
		return strconv.Itoa(t.Len())
	case string:
		return strconv.Itoa(len([]rune(t)))
	case nil:
		return "0"
	default:
		return ojson.Text(v)
	}
}
