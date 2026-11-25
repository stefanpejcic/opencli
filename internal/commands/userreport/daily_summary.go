// ##############################################################################
// Script Name: internal/commands/userreport/daily_summary.go
// Description: Save yesterday's summary for every user and email it to those with the Summary feature who turned it on.
// Usage: opencli user-daily_summary <USERNAME> [--date YYYY-MM-DD] [--dry-run] OR opencli user-daily_summary --all [--date YYYY-MM-DD] [--dry-run]
// Author: Stefan Pejcic
// Created: 16.10.2026
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
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/mail"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-daily_summary",
		Desc:  "Save yesterday's summary for every user and email it to those with the Summary feature who turned it on.",
		Usage: "opencli user-daily_summary <USERNAME> [--date YYYY-MM-DD] [--dry-run] OR opencli user-daily_summary --all [--date YYYY-MM-DD] [--dry-run]",
		Run:   dailySummary,
	})
}

func dailyUsage() {
	fmt.Println("Usage: opencli user-daily_summary <USERNAME> [--date YYYY-MM-DD] [--dry-run] OR opencli user-daily_summary --all [--date YYYY-MM-DD] [--dry-run]")
	fmt.Println("")
	fmt.Println("Saves the day's summary (yesterday by default) for every user while the Summary module is on,")
	fmt.Println("OpenAdmin's daily summary adds them up. Users with Summary in their plan see it in")
	fmt.Println("OpenPanel > Statistics > Daily Summary, and get it by email if they turned on")
	fmt.Println("'Daily summary' in Account > Notifications, at most once a day.")
	fmt.Println("--dry-run prints the email instead of saving and sending it.")
	cli.Exit(1)
}

var dailyDateRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

type daily struct {
	date    string
	dryRun  bool
	baseURL string
}

func dailySummary(args []string) int {
	d := &daily{}
	target := ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--dry-run":
			d.dryRun = true
		case "--date":
			if !dailyDateRe.MatchString(cli.Arg(args, i+1)) {
				fmt.Println("ERROR: --date needs a date like 2026-10-15.")
				return 1
			}
			d.date = args[i+1]
			i++
		case "-h", "--help":
			dailyUsage()
		default:
			if target != "" {
				dailyUsage()
			}
			target = a
		}
	}
	if target == "" {
		dailyUsage()
	}

	if !enabledModule("summary") {
		fmt.Println("The Summary module is not enabled, nothing to do.")
		return 0
	}

	// the panel always serves https, with a self-signed certificate when the domain has none
	d.baseURL = "https://" + sh.Opencli("domain").NoErr().Str() + ":" + sh.Opencli("port").NoErr().Str() + "/"

	if target == "--all" {
		users := activeUsers()
		if len(users) == 0 {
			fmt.Println("No users found in the database.")
			return 0
		}
		rc := 0
		for _, u := range users {
			fmt.Println("Checking " + u)
			rc = d.user(u)
		}
		return rc
	}
	return d.user(target)
}

func (d *daily) user(username string) int {
	if !safeName.MatchString(username) {
		fmt.Println("  Invalid username: " + username)
		return 1
	}
	args := []string{"exec", "openpanel", "/openpanel", "daily-summary", username, "--base-url", d.baseURL}
	if d.date != "" {
		args = append(args, "--date", d.date)
	}
	if d.dryRun {
		args = append(args, "--no-save")
	}

	// the panel builds and saves the summary, same data as the Statistics pages
	out, err := sh.C("podman", args...).Timeout(600 * time.Second).NoErr().Out()
	o, ok := jqValid(out)
	if err != nil || !ok {
		fmt.Printf("  Could not build the summary for %s.\n", username)
		return 1
	}
	// every account's summary is saved for OpenAdmin, allowed is whether the user sees it (Summary in the plan), so only those get the email
	// an older panel leaves out generated and only builds for allowed users
	allowed := jqStr(o, "allowed") == "true"
	if jqStr(o, "generated") != "true" && !allowed {
		fmt.Printf("  %s doesn't have the Summary module.\n", username)
		return 0
	}
	saved := ", saved"
	if d.dryRun {
		saved = ""
	}
	line := fmt.Sprintf("Summary for %s: %s need attention, %s worth a look", jqStr(o, "date"), jqStr(o, "attention"), jqStr(o, "look"))
	fmt.Printf("  %s%s.\n", trimNL(line), saved)
	if !allowed {
		fmt.Printf("  %s's plan doesn't have the Summary feature, not emailed.\n", username)
		return 0
	}

	if d.dryRun {
		fmt.Println("  Subject: " + trimNL(jqStr(o, "subject")))
		fmt.Print(sedPrefix(jqStr(o, "body")+"\n", "  "))
		return 0
	}

	// off by default, so a missing key means no
	if !hasLine(conf.UsersDir+"/"+username+"/notifications.yaml", "notify_daily_summary=1") {
		return 0
	}

	state := conf.UsersDir + "/" + username + "/.daily_summary_sent"
	if trimNL(conf.Read(state)) == trimNL(jqStr(o, "date")) {
		fmt.Printf("  %s already got this summary.\n", username)
		return 0
	}
	usage := "[]"
	if v, ok := o.Get("usage"); ok && v != nil && v != false {
		usage = ojson.Compact(v)
	}
	// disk and inode use come along as bars, drawn by OpenAdmin's email template
	if mail.SendUser(username, trimNL(jqStr(o, "subject")), trimNL(jqStr(o, "body")), "notify_daily_summary", usage, "", "",
		"To stop these emails, turn off 'Daily summary' in Account > Notifications. The summaries stay in Statistics > Daily Summary either way.") {
		os.WriteFile(state, []byte(jqStr(o, "date")+"\n"), 0o644)
		fmt.Printf("  %s notified by email.\n", username)
	} else {
		fmt.Printf("  Could not email %s.\n", username)
	}
	return 0
}
