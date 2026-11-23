// ##############################################################################
// Script Name: internal/commands/sentinel/sentinel_mail.go
// Description: Check system services, traffic and resource usage, and log/send custom notifications on request.
// Usage: opencli sentinel [--startup] [--report] [--action=<name> --title=<title> --message=<msg> [--severity=<level>] [--details=<json> [--merge-key=<key>]] [--no-send]]
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

package sentinel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/mail"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

// jqStr is how jq string interpolation prints a value
func jqStr(v any) string {
	if v == nil {
		return "null"
	}
	switch v.(type) {
	case string, json.Number, bool:
		return ojson.Text(v)
	}
	return ojson.Compact(v)
}

func getKey(v any, k string) any {
	if o, ok := v.(*ojson.Object); ok {
		x, _ := o.Get(k)
		return x
	}
	return nil
}

// per domain list of 429s for an email or the panel notification
func ratelimitLines(hits []any) string {
	var out []string
	for _, h := range hits {
		s := fmt.Sprintf("- %s: %s of %s requests blocked", jqStr(getKey(h, "domain")), jqStr(getKey(h, "blocked")), jqStr(getKey(h, "requests")))
		if tb, ok := getKey(h, "top_blocked").([]any); ok && len(tb) > 0 {
			s += fmt.Sprintf(", most from %s (%s)", jqStr(getKey(tb[0], "ip")), jqStr(getKey(tb[0], "count")))
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

func containerRunning(name string) bool {
	return tmo(20, "podman", "inspect", "-f", "{{.State.Running}}", name).NoErr().Str() == "true"
}

// parseStream reads one json value per line like jq -s
func parseStream(s string) ([]*ojson.Object, bool) {
	var out []*ojson.Object
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	for d.More() {
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return nil, false
		}
		o, err := ojson.ParseObject(string(raw))
		if err != nil {
			o = ojson.New()
		}
		out = append(out, o)
	}
	return out, true
}

// a big queue from one local sender is usually a stolen mailbox password or an abused contact form
func (r *run) checkMailQueue() {
	title, container := "Large mail queue!", "openadmin_mailserver"
	if mailQueueMax == 0 {
		r.warn++
		r.printf("%s Mail queue checks are disabled.\n", cWarn)
		return
	}
	if !containerRunning(container) {
		r.pass++
		r.printf("%s Mail server is not running, skipping mail queue check.\n", cOK)
		return
	}
	queue, err := tmo(60, "podman", "exec", container, "postqueue", "-j").NoErr().Out()
	items, ok := parseStream(queue)
	if err != nil || !ok {
		r.warn++
		r.printf("%s Could not read the mail queue.\n", cWarn)
		return
	}
	var active []*ojson.Object
	heldBy := map[string]int{}
	for _, it := range items {
		if it.Str("queue_name") != "hold" {
			active = append(active, it)
		} else {
			heldBy[it.Str("sender")]++
		}
	}
	total := len(active)
	if total < mailQueueMax {
		r.pass++
		r.printf("%s Mail queue %d < threshold %d\n", cOK, total, mailQueueMax)
		// the queue only shrank because sentinel held the mail, that's not resolved until the admin releases or deletes it
		if sender := readTrim(mailHoldFile); sender != "" {
			if n := heldBy[sender]; n > 0 {
				r.printf("%s %d email(s) from %s are still on hold, waiting for the admin.\n", cWarn, n, sender)
				return
			}
			os.Remove(mailHoldFile)
		}
		// a bit of slack so a queue hovering around the threshold doesn't resolve and re-alert every run
		if total < mailQueueMax*8/10 {
			r.resolveNotification(title, false)
		}
		return
	}
	r.fail++
	r.status = 2
	owners := db.Rows("SELECT d.domain_url, u.username FROM domains d JOIN users u ON u.id = d.user_id")
	counts := map[string]int{}
	for _, it := range active {
		s := it.Str("sender")
		if s == "" {
			s = "<>"
		}
		counts[s]++
	}
	type sc struct {
		n int
		s string
	}
	var list []sc
	for s, n := range counts {
		list = append(list, sc{n, s})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].s > list[j].s
	})
	if len(list) > 5 {
		list = list[:5]
	}
	lns, top, topCount := "", "", 0
	for _, e := range list {
		owner := ""
		if e.s != "<>" {
			dom := strings.ToLower(e.s[strings.LastIndex(e.s, "@")+1:])
			for _, o := range owners {
				if len(o) > 1 && strings.ToLower(o[0]) == dom {
					owner = o[1]
					break
				}
			}
		}
		s := e.s
		if strings.HasPrefix(s, "<>") {
			s = "<> (bounces)" + s[2:]
		}
		lns += fmt.Sprintf("- %s: %d queued", s, e.n)
		if owner != "" {
			lns += " (user " + owner + ")"
		}
		lns += "\n"
		// only local accounts are held, bounces and relayed mail are left alone
		if top == "" && owner != "" {
			top, topCount = e.s, e.n
		}
	}
	note, held := "", ""
	if holdSpam == "yes" && top != "" && topCount*2 >= mailQueueMax {
		var ids []string
		for _, it := range active {
			if it.Str("sender") == top {
				ids = append(ids, it.Str("queue_id"))
			}
		}
		out, _ := tmo(60, "podman", "exec", "-i", container, "postsuper", "-h", "-").Stdin(strings.Join(ids, "\n") + "\n").Combined()
		var nums []string
		for _, m := range regexp.MustCompile(`[0-9]+ message`).FindAllString(out, -1) {
			nums = append(nums, regexp.MustCompile(`[0-9]+`).FindString(m))
		}
		held = strings.Join(nums, "\n")
		if held != "" {
			os.WriteFile(mailHoldFile, []byte(top+"\n"), 0o644)
			note = fmt.Sprintf(" Sentinel put %s email(s) from %s on hold. Review them with: opencli email-server queue, then release them all with: opencli email-server unhold ALL or delete one with: opencli email-server delete QUEUE_ID.", held, top)
		} else {
			note = " Sentinel failed to put the emails from " + top + " on hold."
		}
	}
	r.printf("%s Mail queue %d > threshold %d.%s\n", cFail, total, mailQueueMax, note)
	heldSender := ""
	if held != "" {
		heldSender = top
	}
	r.writeNotification("warning", "security", title, fmt.Sprintf("%d emails are waiting in the mail queue (threshold %d). Top senders:\n%s%s If you didn't expect this, a mailbox password may be stolen or a website form abused by spammers.", total, mailQueueMax, lns, strings.TrimPrefix(note, " ")),
		ojson.Compact(ojson.New().Set("kind", "mail_queue").Set("total", total).Set("top_senders", lns).Set("held_sender", heldSender)))
}

// once a day: yesterday's totals from pflogsumm, alert only when bounces, deferrals or rejects are high or the mailserver is down
func (r *run) checkMailReport() {
	title, container := "Mail delivery problems", "openadmin_mailserver"
	if optMail == "no" {
		r.warn++
		r.printf("%s Mail report checks are disabled.\n", cWarn)
		return
	}
	if !isFile("/usr/local/mail/openmail/compose.yml") {
		r.pass++
		r.printf("%s Mail server is not installed, skipping mail report check.\n", cOK)
		return
	}
	today := time.Now().Format("2006-01-02")
	if readTrim(lockFileForMailReport) == today {
		r.pass++
		r.printf("%s Mail report already checked today.\n", cOK)
		return
	}
	os.MkdirAll(filepath.Dir(lockFileForMailReport), 0o755)
	os.WriteFile(lockFileForMailReport, []byte(today+"\n"), 0o644)

	if !containerRunning(container) {
		r.fail++
		r.status = 2
		r.printf("%s Mail server is not running.\n", cFail)
		r.writeNotification("warning", "email", title, "The mail server ("+container+") is not running, mail is not being sent or received. Start it from OpenAdmin > Services or with: cd /usr/local/mail/openmail && podman-compose up -d mailserver", `{"kind":"mail_report","down":true}`)
		return
	}
	var tl []string
	in := false
	for _, l := range strings.Split(tmo(300, "podman", "exec", container, "pflogsumm", "-d", "yesterday", "--zero_fill", "/var/log/mail/mail.log").NoErr().Str(), "\n") {
		if strings.HasPrefix(l, "Grand Totals") {
			in = true
		}
		if in {
			tl = append(tl, l)
			if strings.HasPrefix(l, "Per-Hour") {
				in = false
			}
		}
	}
	totals := strings.TrimRight(strings.Join(tl, "\n"), "\n")
	if totals == "" {
		r.warn++
		r.printf("%s Could not read yesterday's mail log.\n", cWarn)
		return
	}
	num := func(k string) string {
		for _, l := range strings.Split(totals, "\n") {
			if field(l, 2) == k {
				return field(l, 1)
			}
		}
		return ""
	}
	or0 := func(s string) string {
		if s == "" {
			return "0"
		}
		return s
	}
	received, delivered, deferred, bounced, rejected := or0(num("received")), or0(num("delivered")), or0(num("deferred")), or0(num("bounced")), or0(num("rejected"))
	var dl, rp []string
	for _, m := range regexp.MustCompile(`deferred +\( *[0-9]+`).FindAllString(totals, -1) {
		dl = append(dl, regexp.MustCompile(`[0-9]+$`).FindString(m))
	}
	for _, m := range regexp.MustCompile(`rejected \([0-9]+%`).FindAllString(totals, -1) {
		rp = append(rp, regexp.MustCompile(`[0-9]+`).FindString(m))
	}
	deferrals, rejectPct := strings.Join(dl, "\n"), strings.Join(rp, "\n")
	d, b, df := atoi(delivered), atoi(bounced), atoi(deferred)
	bouncePct, deferPct := 0, 0
	if d+b > 0 {
		bouncePct = b * 100 / (d + b)
	}
	if d+df > 0 {
		deferPct = df * 100 / (d + df)
	}
	problems := ""
	if bouncePct >= mailBouncePct && b >= 10 {
		problems += fmt.Sprintf("- %s bounced (%d%% of outgoing, threshold %d%%): check for spam from a hacked mailbox or website, or a blacklisted server IP\n", bounced, bouncePct, mailBouncePct)
	}
	if deferPct >= mailDeferPct && df >= 10 {
		dd := deferrals
		if dd == "" {
			dd = "?"
		}
		problems += fmt.Sprintf("- %s deferred (%d%%, %s retries, threshold %d%%): receiving servers are refusing or rate limiting mail from this server\n", deferred, deferPct, dd, mailDeferPct)
	}
	day := sh.C("date", "-d", "yesterday", "+%b %-d").NoErr().Str()
	if problems == "" {
		r.pass++
		r.printf("%s Mail report for %s: %s delivered, %d%% bounced, %d%% deferred.\n", cOK, day, delivered, bouncePct, deferPct)
		r.resolveNotification(title, false)
		return
	}
	r.warn++
	r.printf("%s Mail report for %s has problems.\n", cWarn, day)
	rj := ""
	if rejectPct != "" {
		rj = " (" + rejectPct + "%)"
	}
	details := ""
	isNum := regexp.MustCompile(`^-?[0-9]+$`)
	if isNum.MatchString(received) && isNum.MatchString(delivered) && isNum.MatchString(deferred) && isNum.MatchString(bounced) && isNum.MatchString(rejected) {
		details = ojson.Compact(ojson.New().Set("kind", "mail_report").Set("day", day).Set("received", json.Number(received)).Set("delivered", json.Number(delivered)).
			Set("deferred", json.Number(deferred)).Set("bounced", json.Number(bounced)).Set("rejected", json.Number(rejected)).Set("bounce_pct", bouncePct).Set("defer_pct", deferPct))
	}
	r.writeNotification("warning", "email", title, fmt.Sprintf("Mail log for %s: %s received, %s delivered, %s rejected%s.\n%sDetails in OpenAdmin > Emails > Summary reports and > Queue.", day, received, delivered, rejected, rj, problems), details)
}

// zones used when rbl.txt is missing
var rblDefaultZones = []string{"zen.spamhaus.org", "bl.spamcop.net", "b.barracudacentral.org", "psbl.surriel.com", "bl.mailspike.net", "dnsbl-1.uceprotect.net", "ix.dnsbl.manitu.net", "dnsbl.dronebl.org", "all.s5h.net"}

// rblZones is the list from rbl.txt without comments and blank lines, the defaults when the file is missing or empty
func rblZones() []string {
	if isFile(rblListFile) {
		valid := regexp.MustCompile(`^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
		seen := map[string]bool{}
		var z []string
		for _, l := range lines(conf.Read(rblListFile)) {
			l, _, _ = strings.Cut(l, "#")
			l = strings.Join(strings.Fields(l), "")
			if valid.MatchString(l) && !seen[l] {
				seen[l] = true
				z = append(z, l)
			}
		}
		sort.Strings(z)
		if len(z) > 0 {
			return z
		}
	}
	return rblDefaultZones
}

// rblLookup is the first IPv4 answer for name, through dig when it's there and the system resolver otherwise
func rblLookup(name string) string {
	ip := regexp.MustCompile(`^[0-9.]+$`)
	if sh.Has("dig") {
		for _, l := range strings.Split(sh.C("dig", "+short", "+time=3", "+tries=1", name, "A").NoErr().Str(), "\n") {
			if ip.MatchString(l) {
				return l
			}
		}
		return ""
	}
	return field(text1(tmo(5, "getent", "ahostsv4", name).NoErr().Str()), 1)
}

// mailSentByThisServer is true when the mailserver runs, has mailboxes and no relay host takes the outgoing mail
func mailSentByThisServer() (bool, string) {
	if !isFile("/usr/local/mail/openmail/compose.yml") {
		return false, "Mail server is not installed"
	}
	if !containerRunning("openadmin_mailserver") {
		return false, "Mail server is not running"
	}
	if regexp.MustCompile(`(?m)^(DEFAULT_RELAY_HOST|RELAY_HOST)=[^[:space:]]`).MatchString(conf.Read("/usr/local/mail/openmail/mailserver.env")) {
		return false, "Outgoing mail goes through a relay host"
	}
	if !strings.Contains(conf.Read("/usr/local/mail/openmail/docker-data/dms/config/postfix-accounts.cf"), "|") {
		return false, "No email accounts"
	}
	return true, ""
}

var ipv4PartsRe = regexp.MustCompile(`^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$`)

// once a day: is the server's IPv4 on a mail blocklist, only when this server delivers its own mail
func (r *run) checkRBL() {
	title := "Server IP is on a mail blocklist"
	if optRBL == "no" {
		r.warn++
		r.printf("%s Blocklist checks are disabled.\n", cWarn)
		return
	}
	today := time.Now().Format("2006-01-02")
	if readTrim(lockFileForRBL) == today {
		r.pass++
		r.printf("%s Blocklists already checked today.\n", cOK)
		return
	}
	if ok, skip := mailSentByThisServer(); !ok {
		r.pass++
		r.printf("%s %s, skipping blocklist check.\n", cOK, skip)
		r.resolveNotification(title, false)
		return
	}
	ip := getPublicIP()
	m := ipv4PartsRe.FindStringSubmatch(ip)
	if m == nil {
		r.warn++
		r.printf("%s Could not get the server's IPv4 address for the blocklist check.\n", cWarn)
		return
	}
	reversed := m[4] + "." + m[3] + "." + m[2] + "." + m[1]
	os.MkdirAll(filepath.Dir(lockFileForRBL), 0o755)
	os.MkdirAll(rblDataDir, 0o755)
	os.WriteFile(lockFileForRBL, []byte(today+"\n"), 0o644)

	checked := 0
	listed, refused := []any{}, []any{}
	lns := ""
	for _, zone := range rblZones() {
		if zone == "" {
			continue
		}
		checked++
		answer := rblLookup(reversed + "." + zone)
		if !strings.HasPrefix(answer, "127.") {
			answer = ""
		}
		switch {
		case answer == "":
		// 127.255.255.x is the list refusing the query, usually because the server uses a public resolver
		case strings.HasPrefix(answer, "127.255.255."):
			refused = append(refused, zone)
		default:
			listed = append(listed, ojson.New().Set("zone", zone).Set("answer", answer))
			lns += "- " + zone + " (" + answer + ")\n"
		}
	}
	os.WriteFile(rblDataDir+"/"+today+".json", []byte(ojson.Pretty(ojson.New().Set("day", today).Set("ip", ip).Set("checked", checked).Set("listed", listed).Set("refused", refused).Set("checked_at", time.Now().Format("2006-01-02 15:04:05")))+"\n"), 0o644)
	old, _ := filepath.Glob(rblDataDir + "/*.json")
	for _, f := range old {
		if st, err := os.Stat(f); err == nil && time.Since(st.ModTime()) >= 91*24*time.Hour {
			os.Remove(f)
		}
	}
	if len(listed) == 0 {
		r.pass++
		r.printf("%s Server IP %s is not on any of %d mail blocklists.\n", cOK, ip, checked)
		r.resolveNotification(title, false)
		return
	}
	r.warn++
	r.printf("%s Server IP %s is on %d of %d mail blocklists.\n", cWarn, ip, len(listed), checked)
	r.writeNotification("warning", "email", title, fmt.Sprintf("The server IP %s is listed on %d of %d mail blocklists, mail sent from this server may be rejected or go to spam:\n%sLook up the reason and the removal form on each list's website, or at https://mxtoolbox.com/SuperTool.aspx?action=blacklist%%3a%s . Usually a hacked mailbox or website sent spam, check Emails > Queue first. The list of blocklists checked is in %s.", ip, len(listed), checked, lns, ip, rblListFile),
		ojson.Compact(ojson.New().Set("kind", "rbl").Set("ip", ip).Set("listed", listed).Set("checked", checked)))
}

func numVal(v any) float64 {
	if n, ok := v.(json.Number); ok {
		f, _ := n.Float64()
		return f
	}
	return 0
}

// every 6h: domains whose visitors got 429s from the rate limits, admin first, then each owner who opted in
func (r *run) checkRatelimitHits() {
	if optRatelimit == "no" {
		r.warn++
		r.printf("%s Rate limit checks are disabled.\n", cWarn)
		return
	}
	if age, ok := ageSecs(lockFileForRatelimit); ok && age < ratelimitCheckHrs*3600 {
		r.pass++
		r.printf("%s Rate limit check skipped (last run %dh %dm ago).\n", cOK, age/3600, age%3600/60)
		return
	}
	touch(lockFileForRatelimit)

	stats := sh.Opencli("domains-ratelimit", "stats", "--all", "--hours", strconv.Itoa(ratelimitCheckHrs), "--json").Timeout(500 * time.Second).NoErr().Str()
	v, err := ojson.Parse(strings.TrimSpace(stats))
	var all []any
	switch t := v.(type) {
	case []any:
		all = t
	case *ojson.Object:
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			all = append(all, x)
		}
	default:
		err = fmt.Errorf("bad")
	}
	if err != nil {
		r.warn++
		r.printf("%s Could not read rate limit stats from the domain logs.\n", cWarn)
		return
	}
	var hits []any
	for _, h := range all {
		if numVal(getKey(h, "blocked")) > 0 {
			hits = append(hits, h)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return numVal(getKey(hits[i], "blocked")) > numVal(getKey(hits[j], "blocked")) })
	if len(hits) == 0 {
		r.pass++
		r.printf("%s No visitors blocked by rate limits in the last %dh.\n", cOK, ratelimitCheckHrs)
		return
	}
	r.warn++
	r.printf("%s Rate limits blocked visitors on %d domain(s) in the last %dh.\n", cWarn, len(hits), ratelimitCheckHrs)
	top10, top20 := hits, hits
	if len(top10) > 10 {
		top10 = top10[:10]
	}
	if len(top20) > 20 {
		top20 = top20[:20]
	}
	var doms []any
	for _, h := range top20 {
		o := ojson.New()
		for _, k := range []string{"domain", "requests", "blocked", "peak_minute", "peak_ip_minute", "top_blocked"} {
			o.Set(k, getKey(h, k))
		}
		doms = append(doms, o)
	}
	// count goes in the body, a changing title would send a new email instead of bumping the unread one
	r.writeNotification("warning", "traffic", "Rate limit reached", fmt.Sprintf("Visitors got 429 Too Many Requests on %d domain(s) in the last %dh:\n%s", len(hits), ratelimitCheckHrs, ratelimitLines(top10)),
		ojson.Compact(ojson.New().Set("hours", ratelimitCheckHrs).Set("domains", doms)))

	byUser := map[string][]string{}
	var order []string
	safe := regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	for _, h := range hits {
		domain := jqStr(getKey(h, "domain"))
		if !safe.MatchString(domain) {
			continue
		}
		username := db.Cmd("SELECT u.username FROM domains d JOIN users u ON u.id = d.user_id WHERE d.domain_url = '"+domain+"' LIMIT 1", "-N", "-B").NoErr().Str()
		if username == "" || strings.HasPrefix(username, "SUSPENDED_") {
			continue
		}
		// opt-in, a missing key means no
		optin := false
		for _, l := range conf.Lines("/etc/openpanel/openpanel/core/users/" + username + "/notifications.yaml") {
			if l == "notify_ratelimit=1" {
				optin = true
			}
		}
		if !optin {
			continue
		}
		if _, ok := byUser[username]; !ok {
			order = append(order, username)
		}
		byUser[username] = append(byUser[username], domain)
	}
	for _, username := range order {
		var mine []any
		for _, h := range hits {
			for _, d := range byUser[username] {
				if jqStr(getKey(h, "domain")) == d {
					mine = append(mine, h)
					break
				}
			}
		}
		if mail.SendUser(username, "Visitors blocked by rate limit on account "+username,
			fmt.Sprintf("In the last %d hours some visitors to your websites were blocked for sending too many requests per minute, and got a 429 Too Many Requests error:\n\n%s\n\nChecked: %s", ratelimitCheckHrs, ratelimitLines(mine), time.Now().Format("2006-01-02 15:04:05 MST")),
			"notify_ratelimit", "", "", "",
			"One IP with most of the blocks is usually a bot or scraper, and blocking it is what the limit is for. If real visitors are being blocked, for example during a sale or after a newsletter, ask your hosting provider to raise the rate limit for your domain.") {
			r.printf("%s: notified about rate limited domains.\n", username)
		}
	}
}

func cgUsage(path string) (int64, bool) {
	for _, l := range conf.Lines(path) {
		if strings.HasPrefix(l, "usage_usec") {
			n, err := strconv.ParseInt(field(l, 2), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// top 3 users by cpu or ram, read from their user-UID.slice cgroup since plan limits live there too
func topUsers(kind string) string {
	ctxOf := map[string]string{}
	homes, _ := filepath.Glob("/home/*")
	sort.Strings(homes)
	for _, h := range homes {
		if uid := sh.C("stat", "-c", "%u", h).NoErr().Str(); uid != "" {
			ctxOf[uid] = filepath.Base(h)
		}
	}
	c1 := map[string]int64{}
	cgs, _ := filepath.Glob("/sys/fs/cgroup/user.slice/user-*.slice")
	for _, cg := range cgs {
		uid := strings.TrimSuffix(cg[strings.LastIndex(cg, "/user-")+6:], ".slice")
		if ctxOf[uid] == "" {
			continue
		}
		v, _ := cgUsage(cg + "/cpu.stat")
		c1[uid] = v
	}
	if len(c1) == 0 {
		return ""
	}
	ncpu := int64(atoi(sh.C("nproc").Str()))
	if ncpu == 0 {
		ncpu = 1
	}
	time.Sleep(time.Second)
	type row struct {
		cpu, mem int64
		io       int
		name     string
		line     string
	}
	var rows []row
	for uid, v1 := range c1 {
		cg := "/sys/fs/cgroup/user.slice/user-" + uid + ".slice"
		v2, _ := cgUsage(cg + "/cpu.stat")
		mem, _ := strconv.ParseInt(readTrim(cg+"/memory.current"), 10, 64)
		io := 0
		for _, l := range conf.Lines(cg + "/io.pressure") {
			if strings.HasPrefix(l, "some") {
				f, _ := strconv.ParseFloat(strings.TrimPrefix(field(l, 2), "avg10="), 64)
				io = int(f)
				break
			}
		}
		cpu := (v2 - v1) / (10000 * ncpu)
		rw := row{cpu, mem, io, ctxOf[uid], ""}
		rw.line = fmt.Sprintf("%d %d %d %s", cpu, mem, io, ctxOf[uid])
		rows = append(rows, rw)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].cpu, rows[j].cpu
		if kind == "ram" {
			a, b = rows[i].mem, rows[j].mem
		}
		if a != b {
			return a > b
		}
		return rows[i].line < rows[j].line
	})
	if len(rows) > 3 {
		rows = rows[:3]
	}
	var out []string
	for _, rw := range rows {
		out = append(out, fmt.Sprintf("%s (%d%% CPU, %s RAM, %d%% IO wait)", rw.name, rw.cpu, humanKB(rw.mem/1024), rw.io))
	}
	return strings.Join(out, ", ")
}

func (r *run) checkSystemLoad() {
	title := "High System Load!"
	raw := field(conf.Read("/proc/loadavg"), 1)
	ld, _, _ := strings.Cut(raw, ".")
	load := atoi(ld)
	if load > loadThreshold {
		if !r.overThresholdStreak("load") {
			r.warn++
			r.atLeast1()
			r.printf("%s Load %d > threshold %d (%d/%d checks), alerting if it stays high.\n", cWarn, load, loadThreshold, r.streak, streakRuns)
			return
		}
		r.fail++
		r.status = 2
		r.printf("%s Load %d > threshold %d for %d checks in a row. Generating crash report.\n", cFail, load, loadThreshold, r.streak)
		if isUnreadMessagePresent(title) {
			return
		}
		report := generateCrashlogReport()
		users := topUsers("cpu")
		tu := ""
		if users != "" {
			tu = " Top users: " + users + "."
		}
		r.writeNotification("warning", "resources", title, fmt.Sprintf("Load average is %s (threshold %d) for %d checks in a row.%s", raw, loadThreshold, r.streak, tu),
			ojson.Compact(ojson.New().Set("kind", "load").Set("load", raw).Set("crashlog", report).Set("top_users", users)))
		return
	}
	clearStreak("load")
	r.pass++
	r.printf("%s Load %d < threshold %d.\n", cOK, load, loadThreshold)
	r.resolveNotification(title, false)
}

// freeLine is the fields of free -m's Mem: or Swap: line
func freeLine(prefix string) []int {
	for _, l := range strings.Split(sh.C("free", "-m").Str(), "\n") {
		if strings.HasPrefix(l, prefix) {
			var out []int
			for _, f := range fields(l)[1:] {
				out = append(out, atoi(f))
			}
			for len(out) < 3 {
				out = append(out, 0)
			}
			return out
		}
	}
	return []int{0, 0, 0}
}

func (r *run) checkRAMUsage() {
	title := "High Memory Usage!"
	m := freeLine("Mem:")
	total, used := m[0], m[1]
	pct := 0
	if total != 0 {
		pct = used * 100 / total
	}
	if pct > ramThreshold {
		if !r.overThresholdStreak("ram") {
			r.warn++
			r.atLeast1()
			r.printf("%s RAM %d%% > threshold %d%% (%d/%d checks), alerting if it stays high.\n", cWarn, pct, ramThreshold, r.streak, streakRuns)
			return
		}
		if isUnreadMessagePresent(title) {
			r.warn++
			r.printf("%s Unread RAM notification. Skipping.\n", cWarn)
			return
		}
		r.fail++
		r.status = 2
		r.printf("%s RAM %d%% > threshold %d%%\n", cFail, pct, ramThreshold)
		procs := topProcesses("mem")
		users := topUsers("ram")
		tu := ""
		if users != "" {
			tu = " Top users: " + users + "."
		}
		r.writeNotification("warning", "resources", title, fmt.Sprintf("RAM usage is %d%% (%dMB of %dMB, threshold %d%%).%s", pct, used, total, ramThreshold, tu),
			ojson.Compact(ojson.New().Set("kind", "ram").Set("percent", pct).Set("used_mb", used).Set("total_mb", total).Set("processes", procs).Set("top_users", users)))
		return
	}
	clearStreak("ram")
	r.pass++
	r.printf("%s RAM %d%% < threshold %d%%\n", cOK, pct, ramThreshold)
	r.resolveNotification(title, false)
}

func cpuFields() []int64 {
	for _, l := range lines(conf.Read("/proc/stat")) {
		if strings.HasPrefix(l, "cpu ") {
			var out []int64
			for _, f := range fields(l)[1:] {
				n, _ := strconv.ParseInt(f, 10, 64)
				out = append(out, n)
			}
			return out
		}
	}
	return nil
}

// cpuPct samples /proc/stat twice
func cpuPct(wait time.Duration) int {
	f1 := cpuFields()
	time.Sleep(wait)
	f2 := cpuFields()
	if len(f1) < 4 || len(f2) < 4 {
		return 0
	}
	var t1, t2 int64
	for _, v := range f1 {
		t1 += v
	}
	for _, v := range f2 {
		t2 += v
	}
	d := t2 - t1
	if d <= 0 {
		return 0
	}
	return int(100 * (d - (f2[3] - f1[3])) / d)
}

func (r *run) checkCPUUsage() {
	title := "High CPU Usage!"
	// 1s sample so a single busy moment doesn't count, runs in parallel with the other checks anyway
	pct := cpuPct(time.Second)
	if pct > cpuThreshold {
		if !r.overThresholdStreak("cpu") {
			r.warn++
			r.atLeast1()
			r.printf("%s CPU %d%% > threshold %d%% (%d/%d checks), alerting if it stays high.\n", cWarn, pct, cpuThreshold, r.streak, streakRuns)
			return
		}
		r.fail++
		r.status = 2
		r.printf("%s CPU %d%% > threshold %d%%\n", cFail, pct, cpuThreshold)
		procs := topProcesses("cpu")
		users := topUsers("cpu")
		tu := ""
		if users != "" {
			tu = " Top users: " + users + "."
		}
		r.writeNotification("warning", "resources", title, fmt.Sprintf("CPU usage is %d%% (threshold %d%%).%s", pct, cpuThreshold, tu),
			ojson.Compact(ojson.New().Set("kind", "cpu").Set("percent", pct).Set("processes", procs).Set("top_users", users)))
		return
	}
	clearStreak("cpu")
	r.pass++
	r.printf("%s CPU %d%% < threshold %d%%\n", cOK, pct, cpuThreshold)
	r.resolveNotification(title, false)
}
