// ##############################################################################
// Script Name: internal/commands/email/ratelimit.go
// Description: Configure rate-limiting using postfwd for domains and users.
// Usage: opencli email-ratelimit [--username=<user>] [--domain=<domain>] [--all-users] [--delete-user=<user>] [--delete-domain=<domain>] [--skip-reload] [--notify]
// Author: Stefan Pejcic
// Created: 03.12.2025
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
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/mail"
	"opencli/internal/ojson"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "email-ratelimit",
		Desc:  "Configure rate-limiting using postfwd for domains and users. ",
		Usage: "opencli email-ratelimit [--username=<user>] [--domain=<domain>] [--all-users] [--delete-user=<user>] [--delete-domain=<domain>] [--skip-reload] [--notify]",
		Run:   ratelimit,
	})
}

const rlOutput = "/usr/local/mail/openmail/postfwd/postfwd.cf"

func rlUsage() {
	fmt.Println("Usage: opencli email-ratelimit [--username=<user>] [--domain=<domain>] [--all-users] [--delete-user=<user>] [--delete-domain=<domain>] [--notify]")
	fmt.Println("")
	fmt.Println("  (no args)                Show current rules file")
	fmt.Println("  --username=<user>        Remove all rules for user, re-fetch their domains, re-add")
	fmt.Println("  --domain=<domain>        Add domain rule if missing, or update if exists")
	fmt.Println("  --all-users              Wipe file and regenerate rules for all users")
	fmt.Println("  --delete-user=<user>     Remove all rules for the given user")
	fmt.Println("  --delete-domain=<domain> Remove the rule for the given domain")
	fmt.Println("  --skip-reload            Optional flag to NOT reload postfix (used on plan edit)")
	fmt.Println("  --notify                 Email users whose account reached the hourly limit in the last 30 minutes, once a day")
	cli.Exit(1)
}

// rate() counts per attribute value and per rule id, so the first rule tags the request with the account and every domain of it uses the same limit_<user>_rate id to share one counter
func rlBuildRule(username, limit, domain string) string {
	sender := "sender=~.+@" + domain + "$ ; protocol_state==RCPT"
	return fmt.Sprintf("id=%s ; %s\n                action=set(op_account=%s)\nid=limit_%s_rate ; %s\n                action=rate(op_account/%s/3600/450 4.7.1 sorry, OpenPanel account reached limit of %s emails per hour)\n\n",
		rlRuleID(username, domain), sender, username, username, sender, limit, limit)
}

func rlRuleID(username, domain string) string {
	return "limit_" + username + "_" + strings.ReplaceAll(domain, ".", "_")
}

// rlRemove drops the blocks whose id= line has the prefix, up to the next empty line or the next rule that doesn't match
func rlRemove(file, prefix string) string {
	return rlRemoveIf(file, func(l string) bool { return strings.HasPrefix(l, prefix) })
}

func rlRemoveIf(file string, match func(string) bool) string {
	var out strings.Builder
	skip := false
	for _, l := range recLines(conf.Read(file)) {
		if strings.HasPrefix(l, "id=") {
			skip = match(l)
		}
		if skip && l == "" {
			skip = false
			continue
		}
		if !skip {
			out.WriteString(l + "\n")
		}
	}
	return out.String()
}

// rlRemoveDomain drops every rule for the domain, the rate rule id is shared by the account so it goes by the sender
func rlRemoveDomain(file, domain string) string {
	return rlRemoveIf(file, func(l string) bool {
		return strings.Contains(l, " ; sender=~.+@"+domain+" ;") || strings.Contains(l, " ; sender=~.+@"+domain+"$ ;")
	})
}

func rlTmp(content string) string {
	f, err := os.CreateTemp("", "tmp.")
	if err != nil {
		return ""
	}
	f.WriteString(content)
	f.Close()
	return f.Name()
}

func rlRows(q string) [][]string {
	out, _ := db.Plain(q, "-N", "-s").Out()
	return db.Split(out)
}

func rlLines(file string) int { return countNL(conf.Read(file)) }

func ratelimit(args []string) int {
	requireEnterprise("Error: OpenPanel Community edition does not support emails. Please consider purchasing the Enterprise version that allows email management.", 0)

	mode, val := "show", ""
	skipReload := false
	for _, a := range args {
		switch {
		case a == "--all-users":
			mode = "all-users"
		case strings.HasPrefix(a, "--username="):
			mode, val = "username", strings.TrimPrefix(a, "--username=")
		case strings.HasPrefix(a, "--domain="):
			mode, val = "domain", strings.TrimPrefix(a, "--domain=")
		case strings.HasPrefix(a, "--delete-user="):
			mode, val = "delete-user", strings.TrimPrefix(a, "--delete-user=")
		case strings.HasPrefix(a, "--delete-domain="):
			mode, val = "delete-domain", strings.TrimPrefix(a, "--delete-domain=")
		case a == "--notify":
			mode, skipReload = "notify", true
		case a == "--skip-reload":
			skipReload = true
		case a == "--help" || a == "-h":
			rlUsage()
		default:
			fmt.Printf("Unknown argument: %s\n", a)
			rlUsage()
		}
	}

	need := func(flag string) {
		if val == "" {
			fmt.Printf("ERROR: --%s= value is empty\n", flag)
			rlUsage()
		}
	}
	switch mode {
	case "show":
		rlShow()
	case "notify":
		rlNotify()
	case "all-users":
		rlAllUsers()
	case "username":
		need("username")
		rlUsername(val)
	case "domain":
		need("domain")
		rlDomain(val)
	case "delete-user":
		need("delete-user")
		rlDeleteUser(val)
	case "delete-domain":
		need("delete-domain")
		rlDeleteDomain(val)
	}

	if !skipReload {
		// reload conf, keep counters
		sh.C("podman", "kill", "--signal=HUP", "postfwd").Quiet().Background()
	}
	return 0
}

func rlShow() {
	if !conf.IsFile(rlOutput) {
		cli.Fail("File not found: %s", rlOutput)
	}
	fmt.Printf("=== %s ===\n", rlOutput)
	fmt.Print(conf.Read(rlOutput))
	fmt.Println("---")
	fmt.Printf("%d lines total\n", rlLines(rlOutput))
}

func rlAllUsers() {
	fmt.Println("Regenerating all rules...")
	var tmp strings.Builder
	rows := rlRows("SELECT u.username, p.max_hourly_email, d.domain_url FROM users u JOIN plans p ON u.plan_id = p.id JOIN domains d ON d.user_id = u.id WHERE p.max_hourly_email IS NOT NULL AND p.max_hourly_email > 0 ORDER BY u.username")
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		tmp.WriteString(rlBuildRule(r[0], r[1], r[2]))
		fmt.Printf("OK: %s limit=%s/hr domain=%s\n", r[0], r[1], r[2])
	}
	f := rlTmp(tmp.String())
	mvFile(f, rlOutput)
	os.Chmod(rlOutput, 0o644)
	fmt.Println("---")
	fmt.Printf("Generated %d lines written to %s\n", rlLines(rlOutput), rlOutput)
}

func rlTouch() {
	if !conf.IsFile(rlOutput) {
		if f, err := os.OpenFile(rlOutput, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			f.Close()
		}
		os.Chmod(rlOutput, 0o644)
	}
}

func rlUsername(user string) {
	fmt.Printf("Updating rules for user: %s\n", user)
	rlTouch()
	clean := rlRemove(rlOutput, "id=limit_"+user+"_")
	var rules strings.Builder
	rows := rlRows("SELECT p.max_hourly_email, d.domain_url FROM users u JOIN plans p ON u.plan_id = p.id JOIN domains d ON d.user_id = u.id WHERE u.username = '" + db.Escape(user) + "' AND p.max_hourly_email IS NOT NULL AND p.max_hourly_email > 0")
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		rules.WriteString(rlBuildRule(user, r[0], r[1]))
		fmt.Printf("OK: %s limit=%s/hr domain=%s\n", user, r[0], r[1])
	}
	conf.WriteInPlace(rlOutput, clean+rules.String())
	os.Chmod(rlOutput, 0o644)
	fmt.Println("---")
	fmt.Printf("Done. %d lines in %s\n", rlLines(rlOutput), rlOutput)
}

func rlDomain(domain string) {
	fmt.Printf("Updating rule for domain: %s\n", domain)
	rlTouch()
	row, _ := db.Plain("SELECT u.username, p.max_hourly_email FROM users u JOIN plans p ON u.plan_id = p.id JOIN domains d ON d.user_id = u.id WHERE d.domain_url = '"+db.Escape(domain)+"' AND p.max_hourly_email IS NOT NULL AND p.max_hourly_email > 0 LIMIT 1", "-N", "-s").Out()
	if row == "" {
		cli.Fail("ERROR: No user/plan found for domain '%s' (or limit is 0/NULL). Nothing changed.", domain)
	}
	f := strings.Split(row, "\t")
	user, limit := f[0], ""
	if len(f) > 1 {
		limit = f[1]
	}
	id := rlRuleID(user, domain)
	exists := false
	for _, l := range strings.Split(conf.Read(rlOutput), "\n") {
		if strings.HasPrefix(l, "id="+id+" ") {
			exists = true
		}
	}
	if exists {
		fmt.Printf("Updating: %s\n", id)
		tmp := rlTmp(rlRemoveDomain(rlOutput, domain) + rlBuildRule(user, limit, domain))
		mvFile(tmp, rlOutput)
	} else {
		fmt.Printf("Adding: %s\n", id)
		if fh, err := os.OpenFile(rlOutput, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			fh.WriteString(rlBuildRule(user, limit, domain))
			fh.Close()
		}
	}
	os.Chmod(rlOutput, 0o644)
	fmt.Printf("OK: %s limit=%s/hr domain=%s\n", user, limit, domain)
	fmt.Println("---")
}

func rlDeleteUser(user string) {
	fmt.Printf("Deleting all rules for user: %s\n", user)
	if !conf.IsFile(rlOutput) {
		cli.Fail("ERROR: File not found: %s", rlOutput)
	}
	found := false
	for _, l := range strings.Split(conf.Read(rlOutput), "\n") {
		if strings.HasPrefix(l, "id=limit_"+user+"_") {
			found = true
		}
	}
	if !found {
		fmt.Printf("No rules found for user '%s'. Nothing changed.\n", user)
		cli.Exit(0)
	}
	mvFile(rlTmp(rlRemove(rlOutput, "id=limit_"+user+"_")), rlOutput)
	os.Chmod(rlOutput, 0o644)
	fmt.Printf("Removed all rules for user '%s'.\n", user)
	fmt.Println("---")
	fmt.Printf("Done. %d lines in %s\n", rlLines(rlOutput), rlOutput)
}

func rlDeleteDomain(domain string) {
	fmt.Printf("Deleting rule for domain: %s\n", domain)
	if !conf.IsFile(rlOutput) {
		cli.Fail("ERROR: File not found: %s", rlOutput)
	}
	key := strings.ReplaceAll(domain, ".", "_")
	re, err := regexp.Compile("^id=limit_[^_]*_" + key + " ")
	id := ""
	if err == nil {
		for _, l := range strings.Split(conf.Read(rlOutput), "\n") {
			if re.MatchString(l) {
				id = strings.TrimPrefix(l, "id=")
				if i := strings.Index(id, " "); i >= 0 {
					id = id[:i]
				}
				break
			}
		}
	}
	if id == "" {
		fmt.Printf("No rule found for domain '%s'. Nothing changed.\n", domain)
		cli.Exit(0)
	}
	mvFile(rlTmp(rlRemoveDomain(rlOutput, domain)), rlOutput)
	os.Chmod(rlOutput, 0o644)
	fmt.Printf("Removed rule: %s\n", id)
	fmt.Println("---")
	fmt.Printf("Done. %d lines in %s\n", rlLines(rlOutput), rlOutput)
}

var rlRejectRe = regexp.MustCompile(`^([0-9T:.-]+)[^ ]* .*reached limit of ([0-9]+) emails per hour.* from=<([^>]+)>.*`)

type rlSummary struct {
	user, limit, first, last string
	count                    int
	senders                  []string
	sent                     map[string]int
}

// emails users whose account hit the hourly limit in the last 30 minutes (the cron interval), at most once a day per user
func rlNotify() {
	const since = "30m"
	if !podman.IsRunning(container) {
		fmt.Printf("'%s' is not running, nothing to check.\n", container)
		return
	}
	// postfwd rejects every message over the limit with "OpenPanel account reached limit of N emails per hour", see build_rule
	logs, _ := sh.C("podman", "logs", "--timestamps", "--since", since, container).Timeout(60 * time.Second).Combined()
	var rejects [][]string
	for _, l := range strings.Split(logs, "\n") {
		if m := rlRejectRe.FindStringSubmatch(l); m != nil {
			rejects = append(rejects, m[1:])
		}
	}
	if len(rejects) == 0 {
		fmt.Printf("No emails rejected by the hourly limit in the last %s.\n", since)
		return
	}

	// domain -> username for every domain, the limit is per account so rejections from all of its domains add up
	owner := map[string]string{}
	ownersOut, _ := db.Cmd("SELECT d.domain_url, u.username FROM domains d JOIN users u ON u.id = d.user_id", "-N", "-B").NoErr().Out()
	for _, r := range db.Split(ownersOut) {
		if len(r) > 1 {
			owner[asciiLower(r[0])] = r[1]
		}
	}
	var order []string
	sum := map[string]*rlSummary{}
	for _, r := range rejects {
		sender := asciiLower(r[2])
		dom := sender
		if i := strings.LastIndex(dom, "@"); i >= 0 {
			dom = dom[i+1:]
		}
		user := owner[dom]
		if user == "" {
			continue
		}
		s := sum[user]
		if s == nil {
			s = &rlSummary{user: user, sent: map[string]int{}}
			sum[user] = s
			order = append(order, user)
			s.first = ts(r[0])
		}
		s.limit = r[1]
		s.count++
		s.last = ts(r[0])
		if s.sent[sender] == 0 {
			s.senders = append(s.senders, sender)
		}
		s.sent[sender]++
	}

	today := time.Now().Format("2006-01-02")
	for _, u := range order {
		s := sum[u]
		state := conf.UsersDir + "/" + s.user + "/.email_ratelimit_notified"
		if b, _ := os.ReadFile(state); strings.TrimRight(string(b), "\n") == today {
			fmt.Printf("%s: %d email(s) rejected, already notified today.\n", s.user, s.count)
			continue
		}
		var lines strings.Builder
		for _, sd := range s.senders {
			lines.WriteString("- " + sd + ": " + strconv.Itoa(s.sent[sd]) + " rejected\n")
		}
		usage := ojson.Compact([]any{ojson.New().Set("title", "Emails sent in the last hour").Set("used", s.limit).Set("total", s.limit+" emails").Set("percent", 100).Set("limit", 100)})

		upgradePlan, upgradeText := "", ""
		if up := mail.Upsell(s.user); len(up) > 8 && mail.LimitRaised(up[7], up[8], "count") {
			upgradePlan = up[0]
			n := "up to " + up[8]
			if up[8] == "0" {
				n = "unlimited"
			}
			upgradeText = "The " + up[0] + " plan lets you send " + n + " emails per hour instead of " + up[7] + "."
		}
		body := "Account " + s.user + " reached its limit of " + s.limit + " emails per hour, so " + strconv.Itoa(s.count) + " email(s) were not sent between " + s.first + " and " + s.last + ".\n\nRejected emails by sender:\n" + lines.String() + "\nSending works again on its own once fewer than " + s.limit + " emails were sent in the last hour. The rejected emails are not sent later, send them again if they're still needed."
		if mail.SendUser(s.user, "Account "+s.user+" reached its hourly email limit", body, "notify_email_ratelimit", usage, upgradePlan, upgradeText,
			"If you didn't send this many emails, a mailbox password may be stolen or a contact form on your website may be abused by spammers. Change the password of the mailbox above and check your website's forms.") {
			os.WriteFile(state, []byte(today+"\n"), 0o644)
			fmt.Printf("%s: %d email(s) rejected, user notified.\n", s.user, s.count)
		} else {
			fmt.Printf("%s: %d email(s) rejected, no email sent (notifications off or no email address).\n", s.user, s.count)
		}
	}
}

// ts is substr($1, 12, 5)
func ts(s string) string {
	if len(s) < 12 {
		return ""
	}
	e := 16
	if len(s) < e {
		e = len(s)
	}
	return s[11:e]
}
