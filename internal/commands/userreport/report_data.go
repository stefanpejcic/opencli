// ##############################################################################
// Script Name: internal/commands/userreport/report_data.go
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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/sh"
	"opencli/internal/text"
)

// future is a background job, get waits for it like _fetch waited for the job's file
type future struct {
	fn   func() string
	once sync.Once
	done chan struct{}
	val  string
}

func newFuture(fn func() string) *future { return &future{fn: fn, done: make(chan struct{})} }

func goFuture(fn func() string) *future { f := newFuture(fn); f.start(); return f }

func (f *future) start() {
	f.once.Do(func() {
		go func() {
			defer close(f.done)
			defer func() { recover() }()
			f.val = f.fn()
		}()
	})
}

func (f *future) get() string {
	f.start()
	select {
	case <-f.done:
		return f.val
	case <-time.After(120 * time.Second):
		return ""
	}
}

var titleRe = regexp.MustCompile(`== ([^=]+) ==`)

// padR is printf %-Ns, bash pads by bytes
func padR(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// padL is printf %Ns
func padL(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return strings.Repeat(" ", n-len(s)) + s
}

// runeCut is ${s:0:n}, bash counts characters
func runeCut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// cutC is cut -cN-M style first n bytes of every line, cut -c counts bytes
func cutC(s string, n int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if len(l) > n {
			lines[i] = l[:n]
		}
	}
	return strings.Join(lines, "\n")
}

func jsonNum(s string) json.Number { return json.Number(s) }

var awkNumRe = regexp.MustCompile(`^[ \t\n]*[-+]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][-+]?[0-9]+)?`)

// awkNum is how awk turns a string into a number, the leading numeric part or 0
func awkNum(s string) float64 {
	m := awkNumRe.FindString(s)
	if m == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.TrimSpace(m), 64)
	return f
}

// awkD is awk's %d, truncated toward zero
func awkD(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Sprint(int64(f))
	}
	return strconv.FormatInt(int64(f), 10)
}

// humanKB is human_kb, KB to a readable size
func humanKB(s string) string {
	if s == "" {
		s = "0"
	}
	k := awkNum(s)
	switch {
	case k >= 1048576:
		return fmt.Sprintf("%.2f GB", k/1048576)
	case k >= 1024:
		return fmt.Sprintf("%.1f MB", k/1024)
	default:
		return awkD(k) + " KB"
	}
}

// humanBytes is human_bytes, bytes to a readable size
func humanBytes(s string) string {
	if s == "" {
		s = "0"
	}
	b := awkNum(s)
	switch {
	case b >= 1073741824:
		return fmt.Sprintf("%.2f GB", b/1073741824)
	case b >= 1048576:
		return fmt.Sprintf("%.0f MB", b/1048576)
	case b >= 1024:
		return fmt.Sprintf("%.0f KB", b/1024)
	default:
		return awkD(b) + " B"
	}
}

// limitLabel is limit_label, 0 or empty means unlimited
func limitLabel(v string) string {
	if v == "" || v == "0" {
		return "unlimited"
	}
	return v
}

// bashInt is how bash arithmetic reads a plain value, 0 for empty
func bashInt(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

// printfD is bash printf %d, a leading 0 means octal and junk stops the number
func printfD(s string) int64 {
	s = strings.TrimSpace(s)
	base, digits := 10, "0123456789"
	if len(s) > 1 && s[0] == '0' {
		base, digits = 8, "01234567"
		s = s[1:]
	}
	end := 0
	for end < len(s) && strings.IndexByte(digits, s[end]) >= 0 {
		end++
	}
	n, _ := strconv.ParseInt(s[:end], base, 64)
	return n
}

var reDigits = regexp.MustCompile(`^[0-9]+$`)

// cronLabel is "daily at 04:45" for simple schedules, the raw expression otherwise
func cronLabel(expr string) string {
	f := bread(expr, " \t\n", 5)
	m, h, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	if reDigits.MatchString(m) && reDigits.MatchString(h) && mon == "*" {
		at := fmt.Sprintf("%02d:%02d", printfD(h), printfD(m))
		if dom == "*" && dow == "*" {
			return "daily at " + at
		}
		if dom == "*" && regexp.MustCompile(`^[0-7]$`).MatchString(dow) {
			days := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
			return "weekly on " + days[dow[0]-'0'] + " at " + at
		}
		if reDigits.MatchString(dom) && dow == "*" {
			return "monthly on day " + dom + " at " + at
		}
	}
	return "cron " + expr
}

// humanAge is "1h 5m" style age from seconds
func humanAge(sec int64) string {
	switch {
	case sec >= 86400:
		return fmt.Sprintf("%dd %dh", sec/86400, sec%86400/3600)
	case sec >= 3600:
		return fmt.Sprintf("%dh %dm", sec/3600, sec%3600/60)
	case sec >= 60:
		return fmt.Sprintf("%dm", sec/60)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}

var (
	composeSvcRe = regexp.MustCompile(`^ +[A-Za-z0-9_.-]+:`)
	leadSpaces   = regexp.MustCompile(`^ +`)
)

// composeServices lists service names under the top-level services: key, whatever the indent
func composeServices(file string) string {
	var out []string
	f, lvl := false, 0
	for _, l := range awkLines(readFile(file)) {
		if strings.HasPrefix(l, "services:") {
			f = true
			continue
		}
		if f && l != "" && l[0] != ' ' && l[0] != '#' {
			break
		}
		if f && composeSvcRe.MatchString(l) {
			n := len(leadSpaces.FindString(l))
			if lvl == 0 {
				lvl = n
			}
			if n == lvl {
				name := strings.TrimLeft(l, " ")
				if i := strings.IndexByte(name, ':'); i >= 0 {
					name = name[:i]
				}
				out = append(out, name)
			}
		}
	}
	return strings.Join(out, "\n")
}

// tsvField formats a value the way jq's @tsv does
func tsvField(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`).Replace(t)
	default:
		return ojson.Text(v)
	}
}

// loadPanelConfig reads openpanel.config once, first value of a key wins like grep -m1 did
func (r *report) loadPanelConfig() {
	r.pconf = map[string]string{}
	keyRe := regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	for _, l := range readLines(readFile(rpPanelConfig)) {
		f := bread(l, "=", 2)
		k, v := f[0], f[1]
		if !keyRe.MatchString(k) {
			continue
		}
		v = strings.TrimSuffix(v, `"`)
		v = strings.TrimPrefix(v, `"`)
		if _, ok := r.pconf[k]; !ok {
			r.pconf[k] = v
		}
	}
}

// panelConfig is a value from openpanel.config, quotes stripped, def when empty
func (r *report) panelConfig(key string, def ...string) string {
	if v := r.pconf[key]; v != "" {
		return v
	}
	if len(def) > 0 {
		return def[0]
	}
	return ""
}

func (r *report) modulesHave(m string) bool {
	return strings.Contains(","+r.panelConfig("enabled_modules")+",", ","+m+",")
}

// sql runs a panel db query with a 10s cap, errors dropped
func sql(q string) string {
	out, _ := db.Cmd(q, "-N", "-B").Timeout(10 * time.Second).NoErr().Out()
	return out
}

var ipv4Re = regexp.MustCompile(`^[0-9]+(\.[0-9]+){3}$`)

// curlGet is curl -s with a max time, -4 when v4, -k when insecure
func curlGet(url string, maxTime time.Duration, v4, insecure bool, hdr map[string]string) (string, error) {
	d := &net.Dialer{}
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if v4 {
		tr.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) { return d.DialContext(ctx, "tcp4", addr) }
	}
	if insecure {
		tr.TLSClientConfig = insecureTLS()
	}
	c := &http.Client{Timeout: maxTime, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

// hostnameI is the first address `hostname -I` prints
func hostnameI() string {
	ifs, _ := net.Interfaces()
	var v6 string
	for _, i := range ifs {
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			if ipn.IP.To4() != nil {
				return ipn.IP.String()
			}
			if v6 == "" {
				v6 = ipn.IP.String()
			}
		}
	}
	return v6
}

func (r *report) getServerIP() string {
	if r.serverIP == "" {
		ip, _ := curlGet("https://ip.openpanel.com", 2*time.Second, true, false, nil)
		ip = trimNL(ip)
		if !ipv4Re.MatchString(ip) {
			ip = hostnameI()
		}
		r.serverIP = ip
	}
	return r.serverIP
}

// rootful containers: name, state, status, ports
func jobRootPS() string {
	return sh.C("podman", "ps", "-a", "--format", "{{.Names}}\t{{.State}}\t{{.Status}}\t{{.Ports}}").Timeout(10 * time.Second).NoErr().Str()
}

func (r *report) needRootPS() string {
	r.rootPSOnce.Do(func() { r.rootPS = r.gRootPS.get() })
	return r.rootPS
}

// rootState is the state of a rootful container, empty when it doesn't exist
func (r *report) rootState(name string) string {
	for _, l := range strings.Split(r.needRootPS(), "\n") {
		f := strings.Split(l, "\t")
		if f[0] == name {
			return idxS(f, 1)
		}
	}
	return ""
}

func idxS(a []string, i int) string {
	if i < len(a) {
		return a[i]
	}
	return ""
}

var (
	opencliVerRe = regexp.MustCompile(`(?m)^readonly OPENCLI_VERSION="([^"]+)`)
	aboutVerRe   = regexp.MustCompile(`Version:\s+(\S+)`)
	ansiRe       = regexp.MustCompile("\x1b\\[[0-9;]*m")
	availRe      = regexp.MustCompile(`available on: (\S+)`)
)

// panel version, urls and os, the same for every user, as key=value lines
func (r *report) jobServerFacts() string {
	var b strings.Builder
	b.WriteString("version=" + sh.Opencli("version").Timeout(5*time.Second).NoErr().Str() + "\n")
	ver := ""
	if m := opencliVerRe.FindStringSubmatch(readFile("/usr/local/opencli/opencli")); m != nil {
		ver = m[1]
	} else if m := aboutVerRe.FindStringSubmatch(sh.C(sh.Self()).NoErr().Str()); m != nil {
		// the bash entry script is gone after the go port, the binary knows its own version
		ver = m[1]
	}
	b.WriteString("opencli=" + ver + "\n")
	if regexp.MustCompile(`(?m)^key=enterprise`).MatchString(readFile(rpPanelConfig)) {
		b.WriteString("license=Enterprise\n")
	} else {
		b.WriteString("license=Community\n")
	}
	b.WriteString("panel_url=" + text.FirstLine(sh.Opencli("domain").Timeout(5*time.Second).NoErr().Str()) + "\n")
	host, err := sh.C("hostname", "-f").NoErr().Out()
	if err != nil {
		host, _ = os.Hostname()
	}
	b.WriteString("hostname=" + host + "\n")
	b.WriteString("os=" + osPrettyName() + "\n")
	if sh.C("systemctl", "is-active", "--quiet", "admin").Quiet().OK() {
		b.WriteString("admin=running\n")
		out := ansiRe.ReplaceAllString(sh.Opencli("admin").Timeout(5*time.Second).NoErr().Str(), "")
		url := ""
		if m := availRe.FindStringSubmatch(out); m != nil {
			url = m[1]
		}
		b.WriteString("admin_url=" + url + "\n")
	} else if conf.IsFile("/root/openadmin_is_disabled") {
		b.WriteString("admin=disabled\n")
	} else {
		b.WriteString("admin=not running\n")
	}
	return b.String()
}

// osPrettyName is PRETTY_NAME from /etc/os-release
func osPrettyName() string {
	v := ""
	for _, l := range strings.Split(readFile("/etc/os-release"), "\n") {
		if strings.HasPrefix(l, "PRETTY_NAME=") {
			v = conf.Unquote(l[len("PRETTY_NAME="):])
		}
	}
	return v
}

func (r *report) needFacts() map[string]string {
	r.factsOnce.Do(func() {
		r.facts = map[string]string{}
		for _, l := range readLines(r.gFacts.get()) {
			f := bread(l, "=", 2)
			if f[0] != "" {
				r.facts[f[0]] = f[1]
			}
		}
	})
	return r.facts
}

// first line is the report time, then "user<TAB>disk_used<TAB>disk_hard<TAB>inodes_used<TAB>inodes_hard"
func jobQuota() string {
	if !conf.IsFile(rpQuotaFile) {
		return ""
	}
	v, err := ojson.Parse(readFile(rpQuotaFile))
	if err != nil {
		return ""
	}
	o, ok := v.(*ojson.Object)
	if !ok {
		return ""
	}
	var b strings.Builder
	ts, _ := o.Get("timestamp")
	if ts == nil || ts == false {
		ts = ""
	}
	b.WriteString(ojson.Text(ts) + "\n")
	users, _ := o.Get("users")
	if arr, ok := users.([]any); ok {
		for _, u := range arr {
			uo, ok := u.(*ojson.Object)
			if !ok {
				continue
			}
			var f []string
			for _, k := range []string{"username", "disk_used", "disk_hard", "inodes_used", "inodes_hard"} {
				x, _ := uo.Get(k)
				f = append(f, tsvField(x))
			}
			b.WriteString(strings.Join(f, "\t") + "\n")
		}
	}
	return b.String()
}

func (r *report) needQuota() (string, string) {
	r.quotaOnce.Do(func() {
		out := trimNL(r.gQuota.get())
		r.quotaTS = text.FirstLine(out)
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			r.quotaTSV = out[i+1:]
		}
	})
	return r.quotaTS, r.quotaTSV
}

// quotaRow is the quota tsv row of a user, split like read does
func (r *report) quotaRow(user string) []string {
	_, tsv := r.needQuota()
	for _, l := range strings.Split(tsv, "\n") {
		f := strings.Split(l, "\t")
		if f[0] == user {
			return f
		}
	}
	return nil
}

func jobEmailList() string {
	return sh.Opencli("email-setup", "email", "list").Timeout(20 * time.Second).NoErr().Str()
}

func jobAliasList() string {
	return sh.Opencli("email-setup", "alias", "list").Timeout(20 * time.Second).NoErr().Str()
}

var fromRe = regexp.MustCompile(`from=<([^>]+)`)

func jobMailRejects() string {
	logs, _ := sh.C("podman", "logs", "--since", "24h", "openadmin_mailserver").Timeout(60 * time.Second).Combined()
	var out []string
	for _, l := range strings.Split(logs, "\n") {
		if !strings.Contains(l, "emails per hour") {
			continue
		}
		for _, m := range fromRe.FindAllStringSubmatch(l, -1) {
			out = append(out, m[1])
		}
	}
	return strings.Join(out, "\n")
}

func jobWebmail() string {
	return text.FirstLine(sh.Opencli("email-webmail").Timeout(10 * time.Second).NoErr().Str())
}

func (r *report) needEmailData() {
	r.emailOnce.Do(func() {
		r.emailList = r.gEmails.get()
		r.aliasList = r.gAliases.get()
		r.rejects = trimNL(r.gRejects.get())
	})
}

const dnsResolver = "1.1.1.1"

// pdig is one public lookup, quotes of split TXT chunks joined back
func pdig(name, typ string) string {
	out := sh.C("dig", "+short", "+time=2", "+tries=1", "@"+dnsResolver, typ, name).NoErr().Str()
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, ";") {
			continue
		}
		l = strings.ReplaceAll(l, `" "`, "")
		l = strings.TrimPrefix(l, `"`)
		l = strings.TrimSuffix(l, `"`)
		lines = append(lines, l)
	}
	return trimNL(strings.Join(lines, "\n"))
}

// nameservers from the panel settings as "ns<TAB>ip", ip empty when it doesn't resolve
func (r *report) jobNS() string {
	var b strings.Builder
	for _, n := range []string{"ns1", "ns2", "ns3", "ns4"} {
		if v := r.panelConfig(n); v != "" {
			fmt.Fprintf(&b, "%s\t%s\n", v, text.FirstLine(pdig(v, "A")))
		}
	}
	return b.String()
}

func (r *report) needNameservers() {
	r.nsOnce.Do(func() {
		for _, l := range readLines(r.gNS.get()) {
			f := bread(l, "\t", 2)
			if f[0] == "" {
				continue
			}
			r.nsConf = append(r.nsConf, f[0])
			if f[1] == "" {
				r.nsBroken = append(r.nsBroken, f[0])
			}
		}
	})
}

// caddy's recent tls/acme errors as "domain<TAB>error", only read when a domain has a problem since caddy's log is big
func jobTLS() string {
	tlsAcme := regexp.MustCompile(`tls|acme`)
	pick := func(s string) []string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			if strings.Contains(l, `"level":"error"`) && tlsAcme.MatchString(l) {
				out = append(out, l)
			}
		}
		return out
	}
	// newest 30k lines read backwards, scanning a whole day of a busy caddy log takes seconds
	j, _ := sh.C("journalctl", "CONTAINER_NAME=caddy", "-r", "-n", "30000", "-o", "cat").Timeout(60 * time.Second).NoErr().Out()
	lines := pick(j)
	for i, k := 0, len(lines)-1; i < k; i, k = i+1, k-1 {
		lines[i], lines[k] = lines[k], lines[i]
	}
	if len(lines) == 0 {
		l, _ := sh.C("podman", "logs", "--tail", "30000", "caddy").Timeout(60 * time.Second).Combined()
		lines = pick(l)
	}
	var b strings.Builder
	for _, l := range lines {
		v, err := ojson.Parse(l)
		if err != nil {
			continue
		}
		o, ok := v.(*ojson.Object)
		if !ok {
			continue
		}
		alt := func(keys ...string) any {
			for _, k := range keys {
				if x, _ := o.Get(k); x != nil && x != false {
					return x
				}
			}
			return ""
		}
		fmt.Fprintf(&b, "%s\t%s\n", tsvField(alt("identifier", "server_name")), tsvField(alt("error", "msg")))
	}
	return b.String()
}

func (r *report) tlsErrorsLoad() string {
	r.tlsOnce.Do(func() { r.tlsErrors = trimNL(r.gTLS.get()) })
	return r.tlsErrors
}

func (r *report) tlsErrorFor(d string) string {
	res := ""
	for _, l := range strings.Split(r.tlsErrorsLoad(), "\n") {
		f := strings.Split(l, "\t")
		if f[0] == d || f[0] == "www."+d {
			res = idxS(f, 1)
		}
	}
	return cutC(res, 200)
}

const rpDefaultCompose = "/etc/openpanel/docker/compose/1.0/docker-compose.yml"

func (r *report) defaultServices() string {
	r.defSvcOnce.Do(func() { r.defSvc = composeServices(rpDefaultCompose) })
	return r.defSvc
}

// panel database, everyone's rows with --all, just this user's otherwise
func (r *report) dbPrefetch(where string) {
	r.dbPlans = sql(`SELECT p.id, p.name, p.websites_limit, p.domains_limit, p.db_limit, p.email_limit, p.ftp_limit, p.disk_limit, p.inodes_limit,
                           IFNULL(p.cpu, ''), IFNULL(p.ram, ''), p.max_email_quota, p.max_hourly_email, IFNULL(up.name, ''), IFNULL(p.upsell_url, '')
                    FROM plans p LEFT JOIN plans up ON up.id = p.upsell_plan_id`)
	// installs without the upsell columns
	if r.dbPlans == "" {
		r.dbPlans = sql(`SELECT id, name, websites_limit, domains_limit, db_limit, email_limit, ftp_limit, disk_limit, inodes_limit,
                                                   IFNULL(cpu, ''), IFNULL(ram, ''), max_email_quota, max_hourly_email, '', '' FROM plans`)
	}
	r.dbDomains = sql("SELECT user_id, domain_url, docroot, IFNULL(php_version, '') FROM domains " + where + " ORDER BY domain_url")
	r.dbSites = sql(`SELECT d.user_id, s.site_name, s.type, IFNULL(s.version, ''), IFNULL(s.container, ''), d.domain_url
                    FROM sites s JOIN domains d ON d.domain_id = s.domain_id ` + strings.ReplaceAll(where, "user_id", "d.user_id") + ` ORDER BY s.site_name`)
	r.dbPass = sql("SELECT user_id, COUNT(*), IFNULL(MAX(last_used_at), '') FROM user_passkeys " + where + " GROUP BY user_id")
	r.dbMCP = sql(`SELECT user_id, name, token_prefix, IF(read_only, 'read-only', 'read-write'), IFNULL(last_used_at, 'never'), IFNULL(expires_at, '')
                  FROM mcp_tokens ` + where + ` ORDER BY created_at`)
}

const rpUserCols = `SELECT u.id, u.username, u.email, IFNULL(NULLIF(u.owner, ''), 'root'), u.server, u.registered_date, IFNULL(u.twofa_enabled, 0), u.plan_id, IFNULL(p.name, ''), IFNULL(p.feature_set, '')
                    FROM users u LEFT JOIN plans p ON p.id = u.plan_id`

func (r *report) dbPrefetchAll() {
	r.dbUsers = sql(rpUserCols + " ORDER BY u.username")
	r.dbPrefetch("")
	r.dbAll = true
}

// userRows picks the current user's rows of a prefetched table, without the user_id column
func userRows(table, id string) string {
	var out []string
	for _, l := range strings.Split(table, "\n") {
		f := strings.SplitN(l, "\t", 2)
		if awkEq(f[0], id) {
			if len(f) == 2 {
				out = append(out, f[1])
			} else {
				out = append(out, l)
			}
		}
	}
	return strings.Join(out, "\n")
}

var awkNumStr = regexp.MustCompile(`^[ \t]*[-+]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][-+]?[0-9]+)?[ \t]*$`)

// awkEq is awk's == between a field and a -v var, numeric when both look like numbers
func awkEq(a, b string) bool {
	if awkNumStr.MatchString(a) && awkNumStr.MatchString(b) {
		return awkNum(a) == awkNum(b)
	}
	return a == b
}

// ruser is everything the script kept in U_* globals for the current user
type ruser struct {
	r                                                    *report
	id, dbname, email, owner, context, registered, twofa string
	planID, plan, features, name, suspended              string
	domainRows, domains, siteRows                        string
	planRow                                              map[string]string
	env                                                  map[string]string
	svcOnce, psOnce, duOnce                              sync.Once
	services, ps, running, du                            string
	psOK                                                 bool
	fPS, fDU                                             *future
	futMu                                                sync.Mutex
	futs                                                 map[string]*future
}

var suspRe = regexp.MustCompile(`^SUSPENDED_([0-9]{4})([0-9]{2})([0-9]{2})([0-9]{2})([0-9]{2}).*`)
var suspPrefixRe = regexp.MustCompile(`^SUSPENDED_[0-9]+_$`)

// loadUser looks a user up, exact name first, else SUSPENDED_<timestamp>_<name>
func (r *report) loadUser(name string) *ruser {
	line := ""
	if r.dbAll {
		susp := ""
		found := false
		for _, l := range strings.Split(r.dbUsers, "\n") {
			f2 := awkPart(l, "\t", 2)
			if awkEq(f2, name) {
				line, found = l, true
				break
			}
			if susp == "" && name != "" && len(f2) > 25 && f2[25:] == name && suspPrefixRe.MatchString(f2[:25]) {
				susp = l
			}
		}
		if !found && susp != "" {
			line = susp
		}
	} else {
		u := db.Escape(name)
		line = sql(rpUserCols + `
                    WHERE u.username = '` + u + `' OR (u.username REGEXP '^SUSPENDED_[0-9]{14}_' AND SUBSTRING(u.username, 26) = '` + u + `')
                    ORDER BY u.username = '` + u + `' DESC LIMIT 1`)
	}
	if line == "" {
		return nil
	}
	// read -ra on tabs collapses empty columns, kept as is
	row := breadArray(line, "\t")
	g := func(i int) string { return idxS(row, i) }
	ux := &ruser{r: r, id: g(0), dbname: g(1), email: g(2), owner: g(3), context: g(4), registered: g(5), twofa: g(6),
		planID: g(7), plan: g(8), features: g(9), futs: map[string]*future{}}
	ux.name = ux.dbname
	if strings.HasPrefix(ux.dbname, "SUSPENDED_") {
		ux.name = ux.dbname[strings.LastIndexByte(ux.dbname, '_')+1:]
		ux.suspended = suspRe.ReplaceAllString(ux.dbname, "$1-$2-$3 $4:$5")
	}
	ux.fPS = newFuture(ux.jobPS)
	ux.fDU = newFuture(ux.jobDU)
	return ux
}

func (r *report) userDataReset(u *ruser) {
	if !r.dbAll {
		r.dbPrefetch("WHERE user_id = '" + db.Escape(u.id) + "'")
	}
	u.domainRows = userRows(r.dbDomains, u.id)
	var doms []string
	for _, l := range strings.Split(u.domainRows, "\n") {
		if d := strings.SplitN(l, "\t", 2)[0]; d != "" {
			doms = append(doms, d)
		}
	}
	u.domains = strings.Join(doms, "\n")
	u.siteRows = userRows(r.dbSites, u.id)
	u.planRow = map[string]string{}
	var rows []string
	for _, l := range strings.Split(r.dbPlans, "\n") {
		if awkEq(strings.SplitN(l, "\t", 2)[0], u.planID) {
			rows = append(rows, l)
		}
	}
	if row := strings.Join(rows, "\n"); row != "" {
		f := bread(strings.SplitN(row, "\n", 2)[0], "\t", 15)
		for i, k := range []string{"id", "name", "websites", "domains", "dbs", "emails", "ftp", "disk", "inodes", "cpu", "ram", "mailquota", "hourly", "upsell", "upsell_url"} {
			u.planRow[k] = f[i]
		}
	}
}

// loadEnv reads the user's .env once, first value wins, quotes and trailing spaces stripped
func (u *ruser) loadEnv() {
	u.env = map[string]string{}
	keyRe := regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	for _, l := range readLines(readFile("/home/" + u.context + "/.env")) {
		f := bread(l, "=", 2)
		k, v := f[0], f[1]
		if !keyRe.MatchString(k) {
			continue
		}
		v = strings.TrimRight(v, " \t\n\r\v\f")
		if v != "" && (v[0] == '"' || v[0] == '\'') {
			v = v[1:]
		}
		if v != "" && (v[len(v)-1] == '"' || v[len(v)-1] == '\'') {
			v = v[:len(v)-1]
		}
		if _, ok := u.env[k]; !ok {
			u.env[k] = v
		}
	}
}

func (u *ruser) userEnv(k string) string { return u.env[k] }

func (u *ruser) needServices() string {
	u.svcOnce.Do(func() { u.services = composeServices("/home/" + u.context + "/docker-compose.yml") })
	return u.services
}

// pod is upodman, podman against the user's rootless instance with a timeout
func (u *ruser) pod(t time.Duration, args ...string) *sh.Cmd {
	st, err := os.Stat("/home/" + u.context)
	if err != nil {
		return sh.C("false").Quiet()
	}
	uid := st.Sys().(*syscall.Stat_t).Uid
	return sh.C("podman", append([]string{"--remote"}, args...)...).
		Env(fmt.Sprintf("CONTAINER_HOST=unix:///hostfs/run/user/%d/podman/podman.sock", uid)).Timeout(t).NoErr()
}

func (u *ruser) sock() string {
	st, err := os.Stat("/home/" + u.context)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("unix:///hostfs/run/user/%d/podman/podman.sock", st.Sys().(*syscall.Stat_t).Uid)
}

// the user's containers: name, state, status, id, last line says whether podman answered
func (u *ruser) jobPS() string {
	out, err := u.pod(20*time.Second, "ps", "-a", "--format", "{{.Names}}\t{{.State}}\t{{.Status}}\t{{.ID}}").Raw()
	if err == nil {
		out += "__PS_OK\n"
	}
	return trimNL(out)
}

func (u *ruser) needPS() {
	u.psOnce.Do(func() {
		u.ps = u.fPS.get()
		var keep []string
		ok := false
		for _, l := range strings.Split(u.ps, "\n") {
			if l == "__PS_OK" {
				ok = true
			} else {
				keep = append(keep, l)
			}
		}
		if ok {
			u.psOK = true
			u.ps = trimNL(strings.Join(keep, "\n"))
		}
		var run []string
		for _, l := range strings.Split(u.ps, "\n") {
			f := strings.Split(l, "\t")
			if idxS(f, 1) == "running" {
				run = append(run, f[0])
			}
		}
		u.running = strings.Join(run, "\n")
	})
}

// psField is awk -F'\t' '$1 == name { print $col }' over the user's ps
func (u *ruser) psField(name string, col int) string {
	u.needPS()
	var out []string
	for _, l := range strings.Split(u.ps, "\n") {
		f := strings.Split(l, "\t")
		if f[0] == name {
			out = append(out, idxS(f, col-1))
		}
	}
	return strings.Join(out, "\n")
}

// one du for the volumes and the folders in /var/www/html, "KB<TAB>path"
func (u *ruser) jobDU() string {
	vols := "/home/" + u.context + "/docker-data/volumes"
	if !conf.IsDir(vols) {
		return ""
	}
	return sh.C("du", "-k", "--max-depth=3", vols).Timeout(60 * time.Second).NoErr().Str()
}

func (u *ruser) needDU() string {
	u.duOnce.Do(func() { u.du = u.fDU.get() })
	return u.du
}

// duOf is the KB of a path from the shared du, empty when du didn't reach that deep
func (u *ruser) duOf(p string) string {
	p = strings.TrimSuffix(p, "/")
	for _, l := range strings.Split(u.du, "\n") {
		f := strings.Split(l, "\t")
		if idxS(f, 1) == p {
			return f[0]
		}
	}
	return ""
}

// fut is uasync/ufetch for jobs keyed by name
func (u *ruser) fut(key string, fn func() string) *future {
	u.futMu.Lock()
	defer u.futMu.Unlock()
	f, ok := u.futs[key]
	if !ok {
		f = newFuture(fn)
		u.futs[key] = f
	}
	return f
}

// sec is one running section, its text, json rows and issues
type sec struct {
	r      *report
	u      *ruser
	buf    strings.Builder
	cur    string
	rows   strings.Builder
	issues strings.Builder

	lSites, lDomains, lDBs, lEmails, lFTP, lDisk, lInodes string
}

func (s *sec) p(format string, a ...any) { fmt.Fprintf(&s.buf, format, a...) }

func (s *sec) line(t string) { s.buf.WriteString(t + "\n") }

func (s *sec) section(name string) {
	s.cur = name
	s.p("\n%s== %s ==%s\n", s.r.cBold, name, s.r.cReset)
}

// subsection is a heading inside a section, issues get tagged "SECTION > SUB"
func (s *sec) subsection(name string) {
	base := s.cur
	if i := strings.Index(base, " > "); i >= 0 {
		base = base[:i]
	}
	s.cur = base + " > " + name
	s.p("\n  %s-- %s --%s\n", s.r.cBold, name, s.r.cReset)
}

// jsonRow keeps every row as "group<TAB>label<TAB>value" in json mode
func (s *sec) jsonRow(label, value string) {
	if !s.r.jsonMode {
		return
	}
	group := ""
	if i := strings.Index(s.cur, " > "); i >= 0 {
		group = s.cur[i+3:]
	}
	fmt.Fprintf(&s.rows, "%s\t%s\t%s\n", group, label, strings.ReplaceAll(value, "\n", " "))
}

func (s *sec) row(label, value string) {
	l := ""
	if label != "" {
		l = label + ":"
	}
	s.p("  %s %s\n", padR(l, 24), value)
	s.jsonRow(label, value)
}

// rowList is a long comma list wrapped under its label
func (s *sec) rowList(label string, items ...string) {
	t := ""
	for _, i := range items {
		t += i + ", "
	}
	t = strings.TrimSuffix(t, ", ")
	if t == "" {
		t = "-"
	}
	s.jsonRow(label, t)
	for i, l := range foldS(t+"\n", 52) {
		l = strings.TrimRight(l, " ")
		if i == 0 {
			l = "  " + padR(label+":", 24) + " " + l
		} else {
			l = "                           " + l
		}
		s.line(l)
	}
}

// foldS is fold -s -w width, returns the output lines
func foldS(in string, width int) []string {
	var out []string
	var line []byte
	col := 0
	adjust := func(c byte, col int) int {
		switch c {
		case '\b':
			if col > 0 {
				return col - 1
			}
			return 0
		case '\r':
			return 0
		case '\t':
			return col + 8 - col%8
		}
		return col + 1
	}
	recount := func() {
		col = 0
		for _, c := range line {
			col = adjust(c, col)
		}
	}
	for i := 0; i < len(in); i++ {
		c := in[i]
		if c == '\n' {
			out = append(out, string(line))
			line, col = line[:0], 0
			continue
		}
	rescan:
		if adjust(c, col) > width {
			end := -1
			for j := len(line) - 1; j >= 0; j-- {
				if line[j] == ' ' || line[j] == '\t' {
					end = j
					break
				}
			}
			if end >= 0 {
				out = append(out, string(line[:end+1]))
				line = append([]byte{}, line[end+1:]...)
				recount()
				goto rescan
			}
			if len(line) == 0 {
				line = append(line, c)
				col = adjust(c, col)
				continue
			}
			out = append(out, string(line))
			line, col = line[:0], 0
			goto rescan
		}
		col = adjust(c, col)
		line = append(line, c)
	}
	if len(line) > 0 {
		out = append(out, string(line))
	}
	return out
}

func (s *sec) ok(msg string) {
	s.p("  %s[OK]%s    %s\n", s.r.cGreen, s.r.cReset, msg)
}

func (s *sec) issue(sev, color, tag, msg string) {
	fmt.Fprintf(&s.issues, "%s\t%s\t%s\n", sev, s.cur, msg)
	s.p("  %s%s%s %s\n", color, padR(tag, 7), s.r.cReset, msg)
}

func (s *sec) err(msg string)  { s.issue("error", s.r.cRed, "[ERROR]", msg) }
func (s *sec) warn(msg string) { s.issue("warning", s.r.cYellow, "[WARN]", msg) }
func (s *sec) info(msg string) { s.issue("info", s.r.cCyan, "[INFO]", msg) }

// dim prints a dimmed note line
func (s *sec) dim(msg string) { s.line("  " + s.r.cDim + msg + s.r.cReset) }

var (
	regexpFirstFrac = regexp.MustCompile(`\.[0-9]+`)
	regexpTZName    = regexp.MustCompile(` [A-Z]+$`)
)
