// ##############################################################################
// Script Name: internal/commands/email/usage.go
// Description: Refresh the cached email accounts and quota usage file for users.
// Usage: opencli email-usage <USERNAME|--all>
// Author: Stefan Pejcic
// Created: 25.09.2026
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

package email

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/mail"
	"opencli/internal/ojson"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "email-usage",
		Desc:  "Refresh the cached email accounts and quota usage file for users.",
		Usage: "opencli email-usage <USERNAME|--all>",
		Run:   usage,
	})
}

const quotaNotifyPercent = 90

var usageLineRe = regexp.MustCompile(`^\* ([^ ]+) \( ([^ ]+) / ([^ ]+) \) \[([0-9]+)%\].*`)

func usageHelp() {
	fmt.Println("Usage: opencli email-usage <USERNAME|--all>")
	fmt.Println("Docs: https://openpanel.com/docs/articles/opencli/email")
	cli.Exit(1)
}

func usage(args []string) int {
	emailGate()
	if !podman.IsRunning(container) {
		cli.Fail("Error: '%s' is not running, start it with: opencli email-server start", container)
	}
	if len(args) != 1 {
		usageHelp()
	}
	// one list for all users, it includes usage and quota for each mailbox
	allEmails, err := sh.C("podman", "exec", container, "setup", "email", "list").Timeout(60 * time.Second).Out()
	if err != nil {
		cli.Fail("Error: failed to list email accounts from '%s'.", container)
	}

	if args[0] == "--all" {
		dirs, _ := filepath.Glob(conf.UsersDir + "/*/")
		for _, d := range dirs {
			if conf.IsDir(d) {
				usageRefresh(filepath.Base(d), allEmails)
			}
		}
		return 0
	}
	if strings.HasPrefix(args[0], "-") {
		usageHelp()
	}
	if !usageRefresh(args[0], allEmails) {
		return 1
	}
	return 0
}

func usageRefresh(username, allEmails string) bool {
	dir := conf.UsersDir + "/" + username
	file := dir + "/emails.yml"
	if !conf.IsDir(dir) {
		fmt.Printf("Error: no data directory for user '%s' in %s\n", username, conf.UsersDir)
		return false
	}
	domains, _ := sh.Opencli("domains-user", username).Out()
	// skip so we don't wipe the file for suspended or deleted users
	if strings.HasPrefix(domains, "User '") && strings.HasSuffix(domains, "' not found in the database.") {
		fmt.Printf("Skipped %s: user not found in the database.\n", username)
		return false
	}
	if strings.HasPrefix(domains, "No domains found for user") {
		domains = ""
	}

	// write to a temp file first so the UI never reads a half-written file
	tmp, err := os.CreateTemp(dir, "emails.yml.")
	if err != nil {
		return false
	}
	if st, err := os.Stat(file); err == nil {
		tmp.Chmod(st.Mode().Perm())
		chownLike(tmp.Name(), st)
	} else {
		tmp.Chmod(0o644)
	}
	for _, d := range strings.Split(domains, "\n") {
		if d != "" {
			for _, l := range grepAt(allEmails, d) {
				fmt.Fprintln(tmp, l)
			}
		}
	}
	tmp.Close()
	os.Rename(tmp.Name(), file)

	n := 0
	for _, l := range recLines(conf.Read(file)) {
		if strings.HasPrefix(l, "*") {
			n++
		}
	}
	fmt.Printf("Updated %s (%d email accounts)\n", file, n)
	usageNotify(username, file)
	return true
}

// usageNotify mails the user (if they have notify_email_quota_limit on) about mailboxes at 90%+ of their quota, once until usage drops again
func usageNotify(username, file string) {
	flagPrefix := "/tmp/" + username + "_notify_email_quota_limit_"
	var over []string
	var items []any

	for _, l := range recLines(conf.Read(file)) {
		m := usageLineRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		email, used, quota := m[1], m[2], m[3]
		pct, _ := strconv.Atoi(m[4])
		if quota == "~" {
			continue
		}
		if pct < quotaNotifyPercent {
			continue
		}
		over = append(over, email)
		flag := flagPrefix + email
		if conf.IsFile(flag) {
			continue
		}
		items = append(items, ojson.New().Set("title", email).Set("used", used).Set("total", quota).Set("percent", pct).Set("limit", quotaNotifyPercent))
		if f, err := os.OpenFile(flag, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			f.Close()
		} else {
			os.Chtimes(flag, time.Now(), time.Now())
		}
	}

	if len(items) > 0 {
		upgradePlan, upgradeText := "", ""
		// offer the upsell plan only when it allows bigger mailboxes than the current plan
		if up := mail.Upsell(username); len(up) > 6 && mail.LimitRaised(up[5], up[6], "size") {
			upgradePlan = up[0]
			upgradeText = "The " + up[0] + " plan lets you set mailboxes up to " + mail.PlanSizeLabel(up[6]) + " instead of " + mail.PlanSizeLabel(up[5]) + ". After upgrading, raise the quota on the Email Accounts page."
		}
		mail.SendUser(username, "Email accounts on "+username+" are almost full",
			"These email accounts on "+username+" are using "+strconv.Itoa(quotaNotifyPercent)+"% or more of their quota, checked "+time.Now().Format("2006-01-02 15:04:05 MST")+".\n\nOnce a mailbox is full, new emails sent to it are not delivered and bounce back to the sender.",
			"notify_email_quota_limit", ojson.Compact(items), upgradePlan, upgradeText,
			"To free up space, delete old emails and empty the Trash and Spam folders in webmail, or raise the mailbox quota on the Email Accounts page in OpenPanel.")
	}

	// mailboxes back under the limit (or deleted) get notified again the next time they cross it
	ents, _ := os.ReadDir("/tmp")
	for _, e := range ents {
		name := e.Name()
		if !strings.HasPrefix(name, strings.TrimPrefix(flagPrefix, "/tmp/")) || !conf.IsFile("/tmp/"+name) {
			continue
		}
		email := strings.TrimPrefix(name, strings.TrimPrefix(flagPrefix, "/tmp/"))
		keep := false
		for _, o := range over {
			if o == email {
				keep = true
			}
		}
		if !keep {
			os.Remove("/tmp/" + name)
		}
	}
}
