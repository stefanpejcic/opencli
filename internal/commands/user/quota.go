// ##############################################################################
// Script Name: internal/commands/user/quota.go
// Description: Report or set disk and inodes for users.
// Usage: opencli user-quota [--update <username|--all>]
// Author: Stefan Pejcic
// Created: 16.11.2023
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

package user

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/mail"
	"opencli/internal/ojson"
	"opencli/internal/redis"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-quota",
		Desc:  "Report or set disk and inodes for users.",
		Usage: "opencli user-quota [--update <username|--all>]",
		Run:   quota,
	})
}

const (
	quotaGBBlocks    = 1024000
	quotaReportFile  = "/etc/openpanel/openpanel/quota_report.json"
	quotaDiskNotify  = 85
	quotaInodeNotify = 95
)

func quotaUsage() {
	fmt.Print(`Usage:
    opencli user-quota                     Reloads cached data for UI
    opencli user-quota --update <username> Set quota for a specific user
    opencli user-quota --update --all      Set quota for all active users

Arguments:
    username    Set quota for specific user
    --all       Set quota for all active users

Description:
    This script enforces and recalculates disk and inode quotas for users
    based on their plan limits stored in the database.
    - Running without arguments will reload cached data for UI.
    - Any quota-setting operation must start with the '--update' keyword.

Examples:
    opencli user-quota                      # reload cached information for UI 
    opencli user-quota --update stefan      # sets quota for user 'stefan'
    opencli user-quota --update --all       # sets quota for all active users
`)
}

func quotaStamp() string { return "[" + time.Now().Format("2006-01-02 15:04:05") + "]" }

func quotaLog(msg string) { fmt.Println(quotaStamp() + " " + msg) }

func quotaLogErr(msg string) { eprintln(quotaStamp() + " ERROR: " + msg) }

type quotaPlan struct {
	files string
	disk  string
}

func quotaPlanLimits(username string) (quotaPlan, bool) {
	if username == "" {
		quotaLogErr("Username parameter is required")
		return quotaPlan{}, false
	}
	q := "SELECT p.inodes_limit, p.disk_limit\n           FROM users u\n           JOIN plans p ON u.plan_id = p.id\n           WHERE u.username = '" + db.Escape(username) + "'"
	res, _ := db.Cmd(q, "-N", "-B").NoErr().Out()
	if res == "" {
		quotaLogErr("No plan found for user: " + username)
		return quotaPlan{}, false
	}
	f := strings.Fields(res)
	p := quotaPlan{}
	if len(f) > 0 {
		p.files = f[0]
		rest := strings.TrimLeft(res, " \t\n")
		rest = strings.TrimLeft(rest[len(f[0]):], " \t")
		p.disk = strings.TrimRight(strings.SplitN(rest, "\n", 2)[0], " \t")
	}
	if p.files == "" || p.disk == "" {
		quotaLogErr("Invalid plan limits retrieved for user: " + username)
		return quotaPlan{}, false
	}
	return p, true
}

func quotaProcessUser(username string) bool {
	quotaLog("Processing user: " + username)
	p, ok := quotaPlanLimits(username)
	if !ok {
		return false
	}
	if !sh.C("id", username).Quiet().OK() {
		quotaLogErr("User does not exist: " + username)
		return false
	}
	n, _ := strconv.Atoi(strings.ReplaceAll(p.disk, " GB", ""))
	blocks := strconv.Itoa(n * quotaGBBlocks)
	if sh.C("setquota", "-u", username, blocks, blocks, p.files, p.files, "/").Run() == nil {
		quotaLog(fmt.Sprintf("Quota set for user %s: %s blocks (%s) and %s inodes", username, blocks, p.disk, p.files))
		return true
	}
	quotaLogErr("Failed to set quota for user: " + username)
	return false
}

func quotaActiveUsers() (string, bool) {
	out, _ := sh.Opencli("user-list", "--local", "--json").NoErr().Out()
	var users []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "SUSPENDED") || !strings.Contains(l, "username") {
			continue
		}
		f := strings.Split(l, `"`)
		if len(f) > 3 {
			users = append(users, f[3])
		} else {
			users = append(users, "")
		}
	}
	joined := strings.TrimRight(strings.Join(users, "\n"), "\n")
	if joined == "" || joined == "No users." {
		quotaLogErr("No active users found in the database")
		return "", false
	}
	return joined, true
}

func quotaProcessAll() bool {
	quotaLog("Fetching list of active users...")
	users, ok := quotaActiveUsers()
	if !ok {
		return false
	}
	list := strings.Fields(users)
	quotaLog(fmt.Sprintf("Found %d active users to process", len(list)))
	var failed []string
	for i, u := range list {
		fmt.Printf("Processing user: %s (%d/%d)\n", u, i+1, len(list))
		if !quotaProcessUser(u) {
			failed = append(failed, u)
		}
		fmt.Println("------------------------------")
	}
	if len(failed) == 0 {
		quotaLog(fmt.Sprintf("Successfully processed all %d users", len(list)))
		return true
	}
	quotaLogErr(fmt.Sprintf("Failed to process %d users: %s", len(failed), strings.Join(failed, " ")))
	return false
}

// awkNum is awk's string to number, the longest numeric prefix
func awkNum(s string) float64 {
	m := regexp.MustCompile(`^[ \t]*[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?`).FindString(s)
	f, _ := strconv.ParseFloat(strings.TrimSpace(m), 64)
	return f
}

func quotaGenerateReport() {
	tmp := quotaReportFile + ".tmp"
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	repOut, repErr := sh.C("repquota", "-u", "/").NoErr().Out()
	uidMap := map[string]string{}
	for _, l := range conf.Lines("/etc/passwd") {
		f := strings.Split(l, ":")
		if len(f) >= 3 && awkNum(f[2]) >= 1000 {
			uidMap[f[0]] = f[2]
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "{\n  \"timestamp\": \"%s\",\n  \"users\": [\n", stamp)
	first := true
	re := regexp.MustCompile(`^[-+][-+]$`)
	for _, l := range strings.Split(repOut, "\n") {
		if l == "" || l[0] == '#' {
			continue
		}
		f := strings.Fields(l)
		if len(f) < 5 || !re.MatchString(f[1]) {
			continue
		}
		user := f[0]
		uid, ok := uidMap[user]
		if !ok {
			continue
		}
		nf := len(f)
		if !first {
			b.WriteString(",\n")
		}
		first = false
		fmt.Fprintf(&b, "    {\"username\":\"%s\",\"uid\":%s,\"home_path\":\"/home/%s/\",\"disk_used\":%s,\"disk_soft\":%s,\"disk_hard\":%s,\"inodes_used\":%s,\"inodes_soft\":%s,\"inodes_hard\":%s}",
			user, uid, user, f[2], f[3], f[4], f[nf-3], f[nf-2], f[nf-1])
	}
	b.WriteString("\n  ]\n}\n")
	os.WriteFile(tmp, []byte(b.String()), 0o644)
	// pipefail plus set -e, a failing repquota stops the script before the mv
	if repErr != nil {
		cli.Exit(sh.Code(repErr))
	}
	os.Rename(tmp, quotaReportFile)
}

type quotaOver struct {
	user           string
	dp, ip         int
	du, dh, iu, ih string
}

func quotaNum(o *ojson.Object, k string) (string, float64, bool) {
	v, _ := o.Get(k)
	switch t := v.(type) {
	case nil:
		return "null", 0, true
	case interface{ String() string }:
		s := t.String()
		f, err := strconv.ParseFloat(s, 64)
		return s, f, err == nil
	}
	return "", 0, false
}

func quotaOverUsers() []quotaOver {
	v, err := ojson.Parse(conf.Read(quotaReportFile))
	if err != nil {
		return nil
	}
	root, ok := v.(*ojson.Object)
	if !ok {
		return nil
	}
	us, _ := root.Get("users")
	arr, ok := us.([]any)
	if !ok {
		return nil
	}
	var out []quotaOver
	for _, it := range arr {
		o, ok := it.(*ojson.Object)
		if !ok {
			return out
		}
		duS, du, ok1 := quotaNum(o, "disk_used")
		dhS, dh, ok2 := quotaNum(o, "disk_hard")
		iuS, iu, ok3 := quotaNum(o, "inodes_used")
		ihS, ih, ok4 := quotaNum(o, "inodes_hard")
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return out
		}
		dp, ip := 0, 0
		if dh > 0 {
			dp = int(math.Floor(du * 100 / dh))
		}
		if ih > 0 {
			ip = int(math.Floor(iu * 100 / ih))
		}
		if dp >= quotaDiskNotify || ip >= quotaInodeNotify {
			un, _ := o.Get("username")
			out = append(out, quotaOver{ojson.Text(un), dp, ip, duS, dhS, iuS, ihS})
		}
	}
	return out
}

func quotaGB(b string) string {
	return fmt.Sprintf("%.2f", awkNum(b)/quotaGBBlocks)
}

func quotaNotify() {
	over := quotaOverUsers()
	for _, o := range over {
		if o.user == "" {
			continue
		}
		level := "close"
		if o.dp >= 100 || o.ip >= 100 {
			level = "over"
		}
		// already notified at this level, going from close to over notifies again
		flag := "/tmp/" + o.user + "_notify_disk_limit"
		if conf.IsFile(flag) && (strings.TrimRight(conf.Read(flag), "\n") == "over" || level == "close") {
			continue
		}
		txt := ""
		if o.dp >= quotaDiskNotify {
			txt += fmt.Sprintf("disk %d%% (%s of %s GB) ", o.dp, quotaGB(o.du), quotaGB(o.dh))
		}
		if o.ip >= quotaInodeNotify {
			txt += fmt.Sprintf("inodes %d%% (%s of %s) ", o.ip, o.iu, o.ih)
		}
		// both are always sent so the user sees the whole picture, OpenAdmin marks the one over its limit
		usage := ojson.Compact([]any{
			ojson.New().Set("title", "Disk space").Set("used", quotaGB(o.du)+" GB").Set("total", quotaGB(o.dh)+" GB").Set("percent", o.dp).Set("limit", quotaDiskNotify),
			ojson.New().Set("title", "Inodes (files and folders)").Set("used", o.iu).Set("total", o.ih+" files").Set("percent", o.ip).Set("limit", quotaInodeNotify),
		})
		// offer the upsell plan only when it raises a limit that's actually running out
		upgradePlan, upgradeText := "", ""
		if up := mail.Upsell(o.user); len(up) >= 5 {
			upName, curDisk, upDisk, curInodes, upInodes := up[0], up[1], up[2], up[3], up[4]
			var raised []string
			if o.dp >= quotaDiskNotify && mail.LimitRaised(curDisk, upDisk, "size") {
				raised = append(raised, mail.PlanSizeLabel(upDisk)+" of disk space instead of "+mail.PlanSizeLabel(curDisk))
			}
			if o.ip >= quotaInodeNotify && mail.LimitRaised(curInodes, upInodes, "count") {
				ui := upInodes
				if ui == "0" {
					ui = "unlimited"
				}
				raised = append(raised, ui+" inodes instead of "+curInodes)
			}
			if len(raised) > 0 {
				upgradePlan = upName
				upgradeText = "The " + upName + " plan gives you " + strings.Join(raised, " and ") + "."
			}
		}
		var subject, intro, title, message, severity string
		now := time.Now().Format("2006-01-02 15:04:05 MST")
		trimmed := strings.TrimSuffix(txt, " ")
		if level == "over" {
			subject = "Account " + o.user + " is over its disk limit"
			intro = "Account " + o.user + " is over its hosting plan limit, checked " + now + ".\n\nWebsites and email can stop working, and new files, uploads and emails will fail until you free up space."
			title = "User " + o.user + " is over the disk limit"
			message = "OpenPanel user '" + o.user + "' is over the hosting plan limit: " + trimmed + "."
			severity = "warning"
		} else {
			subject = "Account " + o.user + " is almost out of disk space"
			intro = "Account " + o.user + " is close to its hosting plan limit, checked " + now + ".\n\nOnce the limit is reached, websites and email can stop working, and new files, uploads and emails will fail."
			title = "User " + o.user + " is close to the disk limit"
			message = "OpenPanel user '" + o.user + "' is close to the hosting plan limit: " + trimmed + "."
			severity = "info"
		}
		mail.SendUser(o.user, subject, intro, "notify_disk_limit", usage, upgradePlan, upgradeText,
			"To free up space, find the biggest folders on the Disk Usage and Inodes Explorer pages in OpenPanel, and delete old backups, logs and files you no longer need. If you need more space, ask your hosting provider for a bigger plan.")
		sentinelBg("--action=user_quota", "--title="+title, "--message="+message, "--severity="+severity)
		os.WriteFile(flag, []byte(level+"\n"), 0o644)
	}
	// users back under both thresholds get notified again the next time they cross one
	flags, _ := filepath.Glob("/tmp/*_notify_disk_limit")
	for _, f := range flags {
		if !conf.IsFile(f) {
			continue
		}
		u := strings.TrimSuffix(filepath.Base(f), "_notify_disk_limit")
		found := false
		for _, o := range over {
			if o.user == u {
				found = true
				break
			}
		}
		if !found {
			os.Remove(f)
		}
	}
}

func quota(args []string) int {
	if len(args) == 0 {
		quotaGenerateReport()
		fmt.Print(conf.Read(quotaReportFile))
		quotaNotify()
		return 0
	}
	// a bare username used to only print the report
	if args[0] != "--update" {
		quotaLogErr("You must specify '--update' as the first argument to modify quotas")
		quotaUsage()
		return 1
	}
	args = args[1:]
	db.Require()

	exitCode := 0
	switch cli.Arg(args, 0) {
	case "", "help":
		quotaUsage()
		return 1
	case "--all":
		if len(args) != 1 {
			quotaLogErr("Invalid number of arguments")
			quotaUsage()
			return 1
		}
		if quotaProcessAll() {
			quotaLog("DONE: All users processed successfully")
		} else {
			exitCode = 1
		}
	default:
		if len(args) != 1 {
			quotaLogErr("Invalid number of arguments")
			quotaUsage()
			return 1
		}
		if !quotaProcessUser(args[0]) {
			exitCode = 1
		}
	}
	quotaGenerateReport()
	quotaNotify()
	// flush cache on OpenPanel UI
	redis.DropKey("openpanel_cache__load_quota_report")
	redis.DropPattern("openpanel_cache_get_disk_and_inodes_for_user:*")
	return exitCode
}
