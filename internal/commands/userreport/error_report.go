// ##############################################################################
// Script Name: internal/commands/userreport/error_report.go
// Description: Email users the errors found in their websites' logs in the last 24 hours.
// Usage: opencli user-error_report <USERNAME> [--dry-run] OR opencli user-error_report --all [--dry-run]
// Author: Stefan Pejcic
// Created: 15.10.2026
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
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/mail"
	"opencli/internal/ojson"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-error_report",
		Desc:  "Email users the errors found in their websites' logs in the last 24 hours.",
		Usage: "opencli user-error_report <USERNAME> [--dry-run] OR opencli user-error_report --all [--dry-run]",
		Run:   errorReport,
	})
}

const errReportHours = 24

func errReportUsage() {
	fmt.Println("Usage: opencli user-error_report <USERNAME> [--dry-run] OR opencli user-error_report --all [--dry-run]")
	fmt.Println("")
	fmt.Println("Emails each user who turned on 'Daily error report' in OpenPanel > Account > Notifications")
	fmt.Printf("the errors found in their websites' logs in the last %d hours, at most once a day.\n", errReportHours)
	fmt.Println("--dry-run prints the email instead of sending it.")
	cli.Exit(1)
}

func errorReport(args []string) int {
	target, dryRun := "", false
	for _, a := range args {
		switch a {
		case "--dry-run":
			dryRun = true
		case "-h", "--help":
			errReportUsage()
		default:
			if target != "" {
				errReportUsage()
			}
			target = a
		}
	}
	if target == "" {
		errReportUsage()
	}

	if !enabledModule("error_logs") {
		fmt.Println("The Error Logs module is not enabled, nothing to send.")
		return 0
	}

	if target == "--all" {
		users := activeUsers()
		if len(users) == 0 {
			fmt.Println("No users found in the database.")
			return 0
		}
		rc := 0
		for _, u := range users {
			fmt.Println("Checking " + u)
			rc = errReportUser(u, dryRun)
		}
		return rc
	}
	return errReportUser(target, dryRun)
}

// jq's "\(.x)" for a value
func jqInterp(o *ojson.Object, key string) string {
	v, _ := o.Get(key)
	return ojson.Text(v)
}

// jq truthiness, only null and false are false
func jqTruthy(v any) bool { return v != nil && v != false }

func errReportUser(username string, dryRun bool) int {
	if !safeName.MatchString(username) {
		fmt.Println("  Invalid username: " + username)
		return 1
	}

	// off by default, so unlike most notifications a missing key means no
	if !hasLine(conf.UsersDir+"/"+username+"/notifications.yaml", "notify_error_logs=1") {
		fmt.Printf("  %s has the daily error report turned off.\n", username)
		return 0
	}

	today := time.Now().Format("2006-01-02")
	state := conf.UsersDir + "/" + username + "/.error_report_sent"
	if !dryRun && trimNL(conf.Read(state)) == today {
		fmt.Printf("  %s already got today's report.\n", username)
		return 0
	}

	// the panel collects the errors, same parsing as the Error Logs page
	digest, err := sh.C("podman", "exec", "openpanel", "/openpanel", "error-logs-digest", username, "--hours", fmt.Sprint(errReportHours)).
		Timeout(300 * time.Second).NoErr().Out()
	o, ok := jqValid(digest)
	if err != nil || !ok {
		fmt.Printf("  Could not read the error logs for %s.\n", username)
		return 1
	}

	if trimNL(jqStr(o, "allowed")) != "true" {
		fmt.Printf("  %s doesn't have the Error Logs feature.\n", username)
		return 0
	}

	total := trimNL(jqStr(o, "total"))
	if total == "0" {
		fmt.Printf("  No errors for %s in the last %d hours.\n", username, errReportHours)
		return 0
	}

	var websites, entries []string
	if arr, ok := jsonArr(o, "websites"); ok {
		for _, w := range arr {
			wo, _ := w.(*ojson.Object)
			if wo == nil {
				continue
			}
			name := jqInterp(wo, "website")
			if v, _ := wo.Get("website"); v == "" {
				name = "other services"
			}
			websites = append(websites, "- "+name+": "+jqInterp(wo, "count"))
		}
	}
	nEntries := 0
	if arr, ok := jsonArr(o, "entries"); ok {
		nEntries = len(arr)
		for _, e := range arr {
			eo, _ := e.(*ojson.Object)
			if eo == nil {
				continue
			}
			t := []rune(jqStr(eo, "time"))
			if len(t) > 16 {
				t = t[:16]
			}
			site := ""
			if v, _ := eo.Get("website"); jqTruthy(v) {
				site = " (" + ojson.Text(v) + ")"
			}
			entries = append(entries, "- "+strings.Replace(string(t), "T", " ", 1)+" UTC ["+jqInterp(eo, "level")+"] "+jqInterp(eo, "service")+site+": "+jqInterp(eo, "message"))
		}
	}

	body := fmt.Sprintf("In the last %d hours, %s error(s) were found in the logs of account %s.\n\n", errReportHours, total, username)
	body += "By website:\n"
	body += trimNL(strings.Join(websites, "\n")) + "\n\n"
	body += "Latest errors:\n"
	body += trimNL(strings.Join(entries, "\n"))
	if n := text.Atoi(total); n > nEntries {
		body += fmt.Sprintf("\n- and %d more", n-nEntries)
	}
	tips := "Open Error Logs in OpenPanel to see the full messages and stack traces, filter by website and copy them for your developer or hosting support. To stop these emails, turn off 'Daily error report' in Account > Notifications."

	subject := "Daily error report for account " + username
	if dryRun {
		fmt.Println("  To: " + mail.UserAddress(username))
		fmt.Println("  Subject: " + subject)
		fmt.Print(sedPrefix(body+"\n", "  "))
		return 0
	}

	if mail.SendUser(username, subject, body, "notify_error_logs", "", "", "", tips) {
		os.WriteFile(state, []byte(today+"\n"), 0o644)
		fmt.Printf("  %s notified by email about %s error(s).\n", username, total)
	} else {
		fmt.Printf("  Could not email %s.\n", username)
	}
	return 0
}

// jsonArr returns o[key] as an array
func jsonArr(o *ojson.Object, key string) ([]any, bool) {
	v, _ := o.Get(key)
	a, ok := v.([]any)
	return a, ok
}
