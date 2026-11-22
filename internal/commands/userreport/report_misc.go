// ##############################################################################
// Script Name: internal/commands/userreport/report_misc.go
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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"opencli/internal/conf"
	"opencli/internal/redis"
	"opencli/internal/sh"
)

var cronAliases = map[string]string{"@yearly": "0 0 0 1 1 *", "@annually": "0 0 0 1 1 *", "@monthly": "0 0 0 1 * *", "@weekly": "0 0 0 * * 0",
	"@daily": "0 0 0 * * *", "@midnight": "0 0 0 * * *", "@hourly": "0 0 * * * *"}

// pyInt is python's int() on a cron field
func pyInt(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return n, err == nil
}

func cronParse(f string, lo, hi int) (map[int]bool, bool) {
	out := map[int]bool{}
	for _, part := range strings.Split(f, ",") {
		step := 1
		if strings.Contains(part, "/") {
			p := strings.Split(part, "/")
			if len(p) != 2 {
				return nil, false
			}
			var ok bool
			if step, ok = pyInt(p[1]); !ok || step == 0 {
				return nil, false
			}
			part = p[0]
		}
		var a, b int
		switch {
		case part == "*" || part == "?":
			a, b = lo, hi
		case strings.Contains(part, "-"):
			p := strings.Split(part, "-")
			if len(p) != 2 {
				return nil, false
			}
			var ok1, ok2 bool
			a, ok1 = pyInt(p[0])
			b, ok2 = pyInt(p[1])
			if !ok1 || !ok2 {
				return nil, false
			}
		default:
			var ok bool
			if a, ok = pyInt(part); !ok {
				return nil, false
			}
			b = a
			if step > 1 {
				b = hi
			}
		}
		if step > 0 {
			for x := a; x < b+1; x += step {
				out[x] = true
			}
		} else {
			for x := a; x > b+1; x += step {
				out[x] = true
			}
		}
	}
	return out, true
}

// cronNextRun is the next run of a 6 field (seconds first) or @descriptor schedule in the cron's timezone, empty when it can't tell
func cronNextRun(specS, tz string) string {
	spec := strings.Fields(specS)
	if len(spec) > 0 && spec[0] == "@every" {
		if len(spec) < 2 {
			return ""
		}
		return "every " + spec[1]
	}
	if len(spec) > 0 {
		if a, ok := cronAliases[spec[0]]; ok {
			spec = strings.Fields(a)
		}
	}
	if len(spec) != 6 {
		return ""
	}
	sec, ok1 := cronParse(spec[0], 0, 59)
	mins, ok2 := cronParse(spec[1], 0, 59)
	hrs, ok3 := cronParse(spec[2], 0, 23)
	dom, ok4 := cronParse(spec[3], 1, 31)
	mon, ok5 := cronParse(spec[4], 1, 12)
	dowRaw, ok6 := cronParse(spec[5], 0, 7)
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) {
		return ""
	}
	dow := map[int]bool{}
	for d := range dowRaw {
		dow[((d%7)+7)%7] = true
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	// naive wall clock math like python's datetime
	n := time.Now().In(loc)
	t := time.Date(n.Year(), n.Month(), n.Day(), n.Hour(), n.Minute(), n.Second(), 0, time.UTC).Add(time.Second)
	end := t.AddDate(0, 0, 400)
	var secs []int
	for x := range sec {
		secs = append(secs, x)
	}
	sort.Ints(secs)
	for t.Before(end) {
		if mon[int(t.Month())] && dom[t.Day()] && dow[int(t.Weekday())] && hrs[t.Hour()] && mins[t.Minute()] {
			for _, x := range secs {
				if x >= t.Second() {
					return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), x, 0, time.UTC).Format("2006-01-02 15:04:05")
				}
			}
		}
		t = t.Add(time.Minute)
		t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	}
	return ""
}

var (
	cronSvcRe = regexp.MustCompile(`^ +cron:$`)
	svcKeyRe  = regexp.MustCompile(`^ +[A-Za-z0-9_.-]+:$`)
	tzRe      = regexp.MustCompile(`\bTZ[=:]\s*(\S+)`)
	tzVarRe   = regexp.MustCompile(`^\$\{([A-Za-z0-9_]+)(:-([^}]*))?\}$`)
)

// cronTimezone follows the Cron Jobs page: TZ in the cron service's environment, else the host's when /etc/localtime is mounted, else UTC
func (s *sec) cronTimezone() string {
	var block []string
	f, lvl := false, 0
	for _, l := range awkLines(readFile("/home/" + s.u.context + "/docker-compose.yml")) {
		if cronSvcRe.MatchString(l) {
			f, lvl = true, len(leadSpaces.FindString(l))
			continue
		}
		if f && svcKeyRe.MatchString(l) && len(leadSpaces.FindString(l)) <= lvl {
			break
		}
		if f {
			block = append(block, l)
		}
	}
	b := strings.Join(block, "\n")
	tz := ""
	for _, l := range block {
		if m := tzRe.FindStringSubmatch(l); m != nil {
			tz = strings.NewReplacer(`"`, "", "'", "").Replace(m[1])
			break
		}
	}
	if m := tzVarRe.FindStringSubmatch(tz); m != nil {
		tz = or(s.u.userEnv(m[1]), m[3])
	}
	switch {
	case tz != "":
		return tz
	case strings.Contains(b, "/etc/localtime"):
		if out, err := sh.C("timedatectl", "show", "-p", "Timezone", "--value").NoErr().Out(); err == nil {
			return out
		}
		if c, err := os.ReadFile("/etc/timezone"); err == nil {
			return trimNL(string(c))
		}
		return "UTC"
	}
	return "UTC"
}

var (
	cronCommentLine = regexp.MustCompile(`^[ \t]*[#;][ \t]*`)
	cronJobHead     = regexp.MustCompile(`^\[job-[a-z-]+ +"[^"]*"\]`)
	cronJobPrefix   = regexp.MustCompile(`^\[job-[a-z-]+ +"`)
	cronJobSuffix   = regexp.MustCompile(`"\].*`)
	cronKey         = func(k string) *regexp.Regexp { return regexp.MustCompile(`^[ \t]*` + k + `[ \t]*=`) }
	cronSchedRe     = cronKey("schedule")
	cronCtrRe       = cronKey("container")
	cronCmdRe       = cronKey("command")
	cronValRe       = regexp.MustCompile(`^[^=]*=[ \t]*`)
)

// cronJobs parses [job-exec "name"] blocks, a disabled job has every line commented out
func cronJobs(content string) []string {
	var out []string
	name, dis, sched, ctr, cmd := "", "", "", "", ""
	flush := func() {
		if name != "" {
			out = append(out, name+"\t"+dis+"\t"+sched+"\t"+ctr+"\t"+cmd)
		}
		name, sched, ctr, cmd = "", "", "", ""
	}
	for _, line := range awkLines(content) {
		d := "0"
		if loc := cronCommentLine.FindStringIndex(line); loc != nil {
			line, d = line[loc[1]:], "1"
		}
		if cronJobHead.MatchString(line) {
			flush()
			name = cronJobSuffix.ReplaceAllString(sedFirst(cronJobPrefix, line, ""), "")
			dis = d
			continue
		}
		if name == "" {
			continue
		}
		switch {
		case cronSchedRe.MatchString(line):
			sched = sedFirst(cronValRe, line, "")
		case cronCtrRe.MatchString(line):
			ctr = sedFirst(cronValRe, line, "")
		case cronCmdRe.MatchString(line):
			cmd = sedFirst(cronValRe, line, "")
		}
	}
	flush()
	return out
}

func tzDate(tz, layout, dateFmt string) string {
	if loc, err := time.LoadLocation(tz); err == nil {
		return time.Now().In(loc).Format(layout)
	}
	return sh.C("date", dateFmt).Env("TZ=" + tz).NoErr().Str()
}

func secCrons(s *sec) {
	u := s.u
	s.section("CRONS")
	tz := s.cronTimezone()
	s.row("Timezone", tz)
	s.row("Server time", tzDate(tz, "2006-01-02 15:04:05 MST", "+%Y-%m-%d %H:%M:%S %Z"))

	jobs := cronJobs(readFile("/home/" + u.context + "/crons.ini"))
	if len(jobs) == 0 {
		s.row("Jobs", "none")
		return
	}
	state := u.psField("cron", 2)
	s.row("Cron service", or(state, "not created"))
	logs := ""
	if state != "" {
		logs, _ = u.pod(20*time.Second, "logs", "--since", "168h", "--tail", "5000", "cron").Combined()
	}

	s.p("\n  %s%s %s %s %s %s %s%s\n", s.r.cDim, padR("Job", 24), padR("Schedule", 18), padR("Container", 14), padR("Last run", 20), padR("Next run", 20), "Result", s.r.cReset)
	nOn := 0
	var bad []string
	for _, j := range jobs {
		f := bread(j, "\t", 5)
		name, dis, sched, ctr := f[0], f[1], f[2], f[3]
		// ofelia logs [Job "name" (id)] Started, then Finished in ..., failed: true|false
		jlog := grepLines(logs, func(l string) bool { return strings.Contains(l, `[Job "`+name+`"`) })
		lastStarted, lastFinished := "", ""
		for _, l := range jlog {
			if strings.Contains(l, "Started") {
				lastStarted = l
			}
			if strings.Contains(l, "Finished") {
				lastFinished = l
			}
		}
		last := ""
		if fl := strings.Fields(lastStarted); len(fl) > 0 && !strings.HasPrefix(lastStarted, " ") && !strings.HasPrefix(lastStarted, "\t") {
			last = strings.ReplaceAll(cutC(fl[0], 19), "T", " ")
		}
		result := "-"
		if lastFinished != "" {
			result = "ok"
			if strings.Contains(lastFinished, "failed: true") {
				result = "failed"
			}
		}
		next := ""
		if dis == "1" {
			next = "disabled"
		} else {
			nOn++
			next = cronNextRun(sched, tz)
			// same check as the Cron Jobs page, ofelia reads 5 fields seconds first so "0 * * * *" runs every minute, not hourly
			fields := len(strings.Fields(sched))
			if !strings.HasPrefix(sched, "@") && fields == 5 {
				next = cronNextRun(sched+" *", tz)
				s.warn(`Cron job "` + name + `" schedule "` + sched + `" has 5 fields, cron reads the first one as seconds, so it doesn't run when a normal 5 field cron would. Add a seconds field first.`)
			} else if !strings.HasPrefix(sched, "@") && fields != 6 {
				next = "invalid"
				s.err(`Cron job "` + name + `" schedule "` + sched + `" is invalid, it needs 6 fields (seconds first) or an @daily style descriptor.`)
			}
		}
		s.p("  %s %s %s %s %s %s\n", padR(runeCut(name, 24), 24), padR(runeCut(sched, 18), 18), padR(runeCut(ctr, 14), 14), padR(or(last, "-"), 20), padR(or(next, "-"), 20), result)
		if result == "failed" {
			bad = append(bad, name)
		}
		if dis != "1" && ctr != "" && !lineIn(u.running, ctr) {
			s.warn(`Cron job "` + name + `" runs in ` + ctr + `, which isn't running, so it fails.`)
		}
	}

	s.line("")
	s.row("Jobs", fmt.Sprintf("%d (%d enabled)", len(jobs), nOn))
	if nOn > 0 && state != "running" {
		s.err(fmt.Sprintf("Cron service isn't running, none of the %d enabled job(s) run.", nOn))
	}
	if len(bad) > 0 {
		s.warn("Last run failed for: " + strings.Join(bad, " ") + ".")
	}

	// same check as the Cron Jobs page, last 200 log lines
	ll := strings.Split(logs, "\n")
	ll = ll[max(0, len(ll)-200):]
	var errs []string
	for _, l := range ll {
		if errWordLine(l) && !strings.Contains(l, "failed: false") {
			errs = append(errs, l)
		}
	}
	if len(errs) > 0 {
		s.warn(fmt.Sprintf("%d cron log entries contain errors, last: %s", countLines(strings.Join(errs, "\n")), cutC(errs[len(errs)-1], 120)))
	}
}

// sizeToBytes turns a limit like 100M or 1G into bytes
func sizeToBytes(v string) string {
	v = strings.ToUpper(v)
	n := awkNum(v)
	m := 1.0
	if v != "" {
		switch v[len(v)-1] {
		case 'K':
			m = 1024
		case 'M':
			m = 1048576
		case 'G':
			m = 1073741824
		}
	}
	return fmt.Sprintf("%.0f", n*m)
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

func secStats(s *sec) {
	u, r := s.u, s.r
	s.section("STATISTICS")

	// 'opencli domains-stats' builds the goaccess pages, from cron
	sched := cronD("domains-stats", cronCommentWS)
	if sched != "" {
		s.row("Stats generated", cronLabel(sched))
	} else {
		s.row("Stats generated", "never")
	}
	if sched == "" && r.modulesHave("goaccess") {
		s.warn("No cron runs 'opencli domains-stats', visitor statistics are never updated.")
	}

	rot, limit := r.panelConfig("logrotate_enable", "yes"), r.panelConfig("logrotate_size_limit", "100M")
	limitB := bashInt(sizeToBytes(limit))
	switch {
	case rot == "yes" && conf.IsFile("/etc/logrotate.d/caddy-logs"):
		s.row("Log rotation", "at "+limit+", keeps "+r.panelConfig("logrotate_retention", "10")+" files for "+r.panelConfig("logrotate_keep_days", "30")+" days")
	case rot == "yes":
		s.row("Log rotation", "on in settings, not installed")
		s.warn("Log rotation is on but /etc/logrotate.d/caddy-logs is missing, run 'opencli server-logrotate'.")
	default:
		s.row("Log rotation", "off")
	}

	if u.domains == "" {
		s.row("Domains", "none")
		return
	}
	s.p("\n  %s%s %s %s %s %s%s\n", r.cDim, padR("Domain", 36), padR("Access log", 11), padR("Last request", 18), padR("WAF log", 11), "Stats page", r.cReset)
	var total int64
	var big []string
	for _, d := range hereLines(u.domains) {
		log, waf, html := "/var/log/caddy/domlogs/"+d+"/access.log", "/var/log/caddy/coraza_waf/"+d+".log", "/var/log/caddy/stats/"+u.name+"/"+d+".html"
		size, wsize := fileSize(log), fileSize(waf)
		total += size + wsize
		last := "-"
		if size > 0 {
			last = mtimeFmt(log)
		}
		stat := "none"
		if conf.IsFile(html) {
			stat = mtimeFmt(html)
		}
		s.p("  %s %s %s %s %s\n", padR(d, 36), padR(humanBytes(fmt.Sprint(size)), 11), padR(last, 18), padR(humanBytes(fmt.Sprint(wsize)), 11), stat)
		// rotation should keep logs under the limit, twice over means it isn't running
		if limitB > 0 && (size > limitB*2 || wsize > limitB*2) {
			big = append(big, d)
		}
	}
	s.line("")
	s.row("Total log size", humanBytes(fmt.Sprint(total)))
	if len(big) > 0 {
		s.warn("Logs over twice the " + limit + " rotation limit, rotation isn't working for: " + strings.Join(big, " ") + ".")
	}
}

// padRunes is awk's printf %-Ns, gawk pads by characters
func padRunes(s string, n int) string {
	if c := utf8.RuneCountInString(s); c < n {
		return s + strings.Repeat(" ", n-c)
	}
	return s
}

var (
	lastLoginRe = regexp.MustCompile(`^IP: ([^ ]+) - Country: ([^ ]*) - Login Time: (.*)`)
	loginIPRe   = regexp.MustCompile(`^IP: (\S+)`)
	denyIPRe    = regexp.MustCompile(`remote_ip\s+([\d./:a-fA-F]+)`)
	cfOnlyRe    = regexp.MustCompile(`^\s*import[ \t]+cloudflare-only([ \t]|$)`)
	sshKeyRe    = regexp.MustCompile(`^(ssh|ecdsa)-`)
	shellOffRe  = regexp.MustCompile(`(nologin|false)$`)
)

// passwdShell is getent passwd <user> | cut -d: -f7
func passwdShell(user string) string {
	for _, l := range strings.Split(readFile("/etc/passwd"), "\n") {
		f := strings.Split(l, ":")
		if f[0] == user && len(f) >= 7 {
			return f[6]
		}
	}
	return ""
}

func secSecurity(s *sec) {
	u, r := s.u, s.r
	s.section("SECURITY")

	s.subsection("LOGIN")
	if u.twofa == "1" {
		s.row("Two-factor auth", "on")
	} else {
		s.row("Two-factor auth", "off")
		if r.panelConfig("twofa_enforce", "no") == "yes" {
			s.warn("Two-factor authentication is enforced on this server but not set up for this account.")
		} else {
			s.info("Two-factor authentication is off, a stolen password is enough to log in.")
		}
	}
	pk := or(userRows(r.dbPass, u.id), "0\t")
	cnt, lastUsed := pk, ""
	if i := strings.IndexByte(pk, '\t'); i >= 0 {
		cnt, lastUsed = pk[:i], pk[i+1:]
	}
	if lastUsed != "" {
		cnt += ", last used " + lastUsed
	}
	s.row("Passkeys", cnt)

	// .lastlogin keeps "IP: x - Country: y - Login Time: z", newest last
	ll := rpUsersCore + "/" + u.name + "/.lastlogin"
	if fileSize(ll) > 0 {
		c := readFile(ll)
		s.row("Last login", lastLoginRe.ReplaceAllString(lastLine(c), "$3 from $1 ($2)"))
		var ips []string
		for _, l := range awkLines(c) {
			if m := loginIPRe.FindStringSubmatch(l); m != nil {
				ips = append(ips, m[1])
			}
		}
		counts := uniqC(ips)
		s.row("Logins kept", fmt.Sprintf("%s, from %d IP(s)", grepCount(ll, func(l string) bool { return l != "" }), len(counts)))
		for _, x := range head2(counts, 5) {
			s.p("    %s %s\n", padRunes(x[0]+"x", 6), fieldN(x[1], 1))
		}
	} else {
		s.row("Last login", "never")
	}

	// live sessions are redis hashes session:<user id>:<token>, all read in one call
	sess := nonEmpty(redis.CLI("EVAL", "local r = {} for _, k in ipairs(redis.call('KEYS', ARGV[1])) do local v = redis.call('HMGET', k, 'created_at', 'ip_address') r[#r + 1] = (v[1] or '') .. ' ' .. (v[2] or '') end return r", "0", "session:"+u.id+":*").NoErr().Str())
	s.row("Active sessions", strconv.Itoa(len(sess)))
	if len(sess) > 0 {
		var out []string
		for _, l := range sess {
			f := strings.Fields(l)
			a, b := "", ""
			if len(f) > 0 {
				a = string([]rune(f[0])[:min(19, utf8.RuneCountInString(f[0]))])
			}
			if len(f) > 1 {
				b = f[1]
			}
			out = append(out, strings.ReplaceAll("    "+a+"  "+b, "T", " "))
		}
		sort.Sort(sort.Reverse(sort.StringSlice(out)))
		for _, l := range head(out, 5) {
			s.line(l)
		}
	}

	s.subsection("ACCESS")
	var blocked []string
	for _, m := range denyIPRe.FindAllStringSubmatch(readFile("/etc/openpanel/caddy/deny/"+u.context+".ips"), -1) {
		blocked = append(blocked, m[1])
	}
	s.row("Blocked IPs", strconv.Itoa(len(blocked)))
	if len(blocked) > 0 {
		s.rowList("", blocked...)
	}

	// domains that only accept traffic through cloudflare
	var cf []string
	for _, d := range hereLines(u.domains) {
		for _, l := range strings.Split(readFile("/etc/openpanel/caddy/domains/"+d+".conf"), "\n") {
			if cfOnlyRe.MatchString(l) {
				cf = append(cf, d)
				break
			}
		}
	}
	if len(cf) > 0 {
		s.row("Cloudflare only", strings.Join(cf, " "))
	} else {
		s.row("Cloudflare only", "none")
	}

	if r.panelConfig("api", "off") == "on" {
		s.row("API", "on (server)")
	} else {
		s.row("API", "off (server)")
	}
	mcp := userRows(r.dbMCP, u.id)
	s.row("MCP tokens", strconv.Itoa(countLines(mcp)))
	if mcp != "" {
		now := time.Now().Format("2006-01-02 15:04:05")
		for _, l := range hereLines(mcp) {
			f := bread(l, "\t", 5)
			s.p("    %s %s %s last used %s\n", padR(runeCut(f[0], 20), 20), padR(f[1]+"…", 12), padR(f[2], 11), f[3])
			if f[4] != "" && f[4] != "NULL" && f[4] < now {
				s.info("MCP token " + f[0] + " expired on " + f[4] + ", it can be removed.")
			}
		}
	}

	// shell login on the host, openpanel users normally have no ssh
	shell := passwdShell(u.context)
	if shellOffRe.MatchString(shell) || shell == "" {
		s.row("Shell access", "off")
	} else {
		keys := grepCount("/home/"+u.context+"/.ssh/authorized_keys", sshKeyRe.MatchString)
		s.row("Shell access", shell+", "+or(keys, "0")+" SSH key(s)")
		if bashInt(keys) > 0 {
			s.info("System user " + u.context + " has a login shell and SSH keys, it can log in to the server over SSH.")
		}
	}

	// same check as the Activity page, 100+ actions from one ip in one minute
	c := map[string]int{}
	for _, l := range awkLines(readFile(rpUsersCore + "/" + u.name + "/activity.log")) {
		f := strings.Fields(l)
		k := idxS(f, 2) + " " + string([]rune(idxS(f, 0) + " " + idxS(f, 1))[:min(16, utf8.RuneCountInString(idxS(f, 0)+" "+idxS(f, 1)))])
		c[k]++
	}
	var bots []string
	for k, n := range c {
		if n >= 100 {
			bots = append(bots, strings.Fields(k + " x")[0])
		}
	}
	sort.Strings(bots)
	bots = uniq(bots)
	if len(bots) > 0 {
		s.err("Suspicious activity from " + strings.Join(bots, ", ") + " (100+ actions in one minute).")
	}
}

func secActivity(s *sec) {
	u := s.u
	s.section("ACTIVITY")
	log := rpUsersCore + "/" + u.name + "/activity.log"
	lines := nonEmpty(readFile(log))
	if len(lines) == 0 {
		s.row("Activity", "none recorded")
		return
	}
	today := time.Now().Format("2006-01-02")
	nToday := 0
	for _, l := range lines {
		if strings.HasPrefix(l, today) {
			nToday++
		}
	}
	s.row("Entries", fmt.Sprintf("%d, %d today", len(lines), nToday))
	s.row("First / last", cutC(lines[0], 19)+" / "+cutC(lines[len(lines)-1], 19))
	s.dim("Last 15:")
	// "2026-10-02 10:00:00  1.2.3.4 User name action", the user name is the same on every line
	for _, l := range lines[max(0, len(lines)-15):] {
		f := strings.Fields(l)
		t := idxS(f, 0) + " " + idxS(f, 1)
		ip := idxS(f, 2)
		rest := ""
		if len(f) > 5 {
			rest = strings.Join(f[5:], " ")
		}
		rr := []rune(rest)
		if len(rr) > 80 {
			rest = string(rr[:80])
		}
		s.p("    %s  %s %s\n", t, padRunes(ip, 15), rest)
	}
}
