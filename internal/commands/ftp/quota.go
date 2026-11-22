// ##############################################################################
// Script Name: internal/commands/ftp/quota.go
// Description: Show or set the disk quota for an FTP sub-user.
// Usage: opencli ftp-quota <username> [<quota>] <openpanel_username> [--debug]
// Author: Stefan Pejcic
// Created: 14.10.2026
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

package ftp

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "ftp-quota",
		Desc:  "Show or set the disk quota for an FTP sub-user.",
		Usage: "opencli ftp-quota <username> [<quota>] <openpanel_username> [--debug]",
		Docs:  "https://docs.openpanel.com/",
		Run:   quota,
	})
}

var (
	quotaDigits = regexp.MustCompile(`^[0-9]+$`)
	quotaOpRe   = regexp.MustCompile(`^[a-z0-9_-]+$`)
)

func quota(args []string) int {
	if len(args) < 2 || len(args) > 4 {
		fmt.Println("Usage: opencli ftp-quota <username> [<quota>] <openpanel_username> [--debug]")
		fmt.Println("       <quota> is in MB (500), or with a unit (500M, 2G), 0 or 'unlimited' removes the quota.")
		return 1
	}
	debug := false
	var a []string
	for _, x := range args {
		if x == "--debug" {
			debug = true
		} else {
			a = append(a, x)
		}
	}
	var q, opUser string
	switch len(a) {
	case 3:
		q, opUser = a[1], a[2]
	case 2:
		opUser = a[1]
	default:
		fmt.Println("Usage: opencli ftp-quota <username> [<quota>] <openpanel_username> [--debug]")
		return 1
	}
	username := strings.ToLower(a[0])
	validName(username)
	if !quotaOpRe.MatchString(opUser) {
		cli.Fail("ERROR: Invalid OpenPanel username.")
	}
	ctx := contextFor(opUser, username)

	list := usersDir + ctx + "/users.list"
	usage := usersDir + ctx + "/quota.usage"
	if !hasUser(list, username) {
		cli.Fail("ERROR: FTP User '%s' does not exist.", username)
	}
	if q == "" {
		quotaShow(list, usage, username)
		return 0
	}
	mb := quotaParse(q)
	quotaSet(ctx, list, username, mb, debug)
	return 0
}

// awkField is awk -F'|' '$1 == user { print $n }' joined by newlines
func awkField(file, user string, n int) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	var out []string
	for _, l := range splitLines(string(b)) {
		f := strings.Split(l, "|")
		if f[0] == user {
			v := ""
			if n <= len(f) {
				v = f[n-1]
			}
			out = append(out, v)
		}
	}
	return strings.Join(out, "\n")
}

func quotaParse(raw string) int {
	q := strings.ToUpper(raw)
	mb := -1
	switch {
	case q == "UNLIMITED":
		mb = 0
	case strings.HasSuffix(q, "G"):
		if s := strings.TrimSuffix(q, "G"); quotaDigits.MatchString(s) {
			n, _ := strconv.Atoi(s)
			mb = n * 1024
		}
	case strings.HasSuffix(q, "M"):
		if s := strings.TrimSuffix(q, "M"); quotaDigits.MatchString(s) {
			mb, _ = strconv.Atoi(s)
		}
	default:
		if quotaDigits.MatchString(q) {
			mb, _ = strconv.Atoi(q)
		}
	}
	if mb < 0 {
		cli.Fail("ERROR: Invalid quota '%s'. Use a number of MB (500), a value with a unit (500M, 2G), or 'unlimited'.", raw)
	}
	return mb
}

func humanMB(n int) string {
	if n >= 1024 && n%1024 == 0 {
		return fmt.Sprintf("%dG", n/1024)
	}
	return fmt.Sprintf("%dM", n)
}

func quotaShow(list, usage, user string) {
	cur := awkField(list, user, 6)
	usedKB := awkField(usage, user, 2)
	n, err := strconv.Atoi(cur)
	if !quotaDigits.MatchString(cur) || err != nil || n == 0 {
		fmt.Printf("FTP user '%s' has no quota (unlimited).\n", user)
		return
	}
	kb, _ := strconv.Atoi(usedKB)
	fmt.Printf("FTP user '%s' quota: %s, used: %dM\n", user, humanMB(n), kb/1024)
}

func quotaSet(ctx, list, user string, mb int, debug bool) {
	b, _ := os.ReadFile(list)
	var out strings.Builder
	for _, l := range splitLines(string(b)) {
		f := strings.Split(l, "|")
		if f[0] == user {
			if mb == 0 {
				if len(f) > 5 {
					f = f[:5]
				}
			} else {
				for len(f) < 5 {
					f = append(f, "")
				}
				if len(f) < 6 {
					f = append(f, "")
				}
				f[5] = strconv.Itoa(mb)
			}
			l = strings.Join(f, "|")
		}
		out.WriteString(l + "\n")
	}
	if err := conf.WriteInPlace(list, out.String()); err != nil {
		cli.Fail("ERROR: Failed to update %s", list)
	}

	sh.Opencli("ftp-users").Quiet().Run()

	// applies the block or unblock right away instead of on the next check
	chk, err := ftpExec("/bin/start_vsftpd.sh", "--check-quotas").Combined()
	os.WriteFile("/tmp/ftp-quota-check.log", []byte(chk+nl(chk)), 0o644)
	if err != nil {
		if debug {
			fmt.Print(chk + nl(chk))
		}
		fmt.Println("WARNING: Quota saved, but the FTP service could not check it now, it will apply on the next check.")
	}

	if mb == 0 {
		fmt.Printf("Success: Quota removed for FTP user '%s'.\n", user)
	} else {
		fmt.Printf("Success: FTP quota for user '%s' set to %s.\n", user, humanMB(mb))
	}
}

func nl(s string) string {
	if s == "" {
		return ""
	}
	return "\n"
}
