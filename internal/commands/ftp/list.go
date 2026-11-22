// ##############################################################################
// Script Name: internal/commands/ftp/list.go
// Description: List FTP sub-users for openpanel user.
// Usage: opencli ftp-list [OPENPANEL_USERNAME] [--json]
// Author: Stefan Pejcic
// Created: 10.09.2024
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
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/db"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "ftp-list",
		Desc:  "List FTP sub-users for openpanel user.",
		Usage: "opencli ftp-list [OPENPANEL_USERNAME] [--json]",
		Docs:  "https://docs.openpanel.com",
		Run:   list,
	})
}

type ftpRow struct{ user, pass, dir, uid, gid, quota string }

// readRows is `while IFS='|' read -r ...`, the last line is dropped without a trailing newline
func readRows(file string) []ftpRow {
	b, _ := os.ReadFile(file)
	s := string(b)
	var rows []ftpRow
	for {
		i := strings.Index(s, "\n")
		if i < 0 {
			break
		}
		line := s[:i]
		s = s[i+1:]
		f := strings.SplitN(line, "|", 6)
		for len(f) < 6 {
			f = append(f, "")
		}
		rows = append(rows, ftpRow{f[0], f[1], f[2], f[3], f[4], f[5]})
	}
	return rows
}

func usedKB(dir, user string) string {
	b, err := os.ReadFile(dir + "/quota.usage")
	if err != nil {
		return ""
	}
	var out []string
	for _, l := range splitLines(string(b)) {
		f := strings.Split(l, "|")
		if f[0] == user && len(f) > 1 {
			out = append(out, f[1])
		}
	}
	return strings.Join(out, "\n")
}

func quotaText(q string) string {
	if n, err := strconv.Atoi(q); err == nil && quotaDigits.MatchString(q) && n > 0 {
		return q + "M"
	}
	return "unlimited"
}

func jsonEsc(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
}

func jsonRow(opUser, dir string, r ftpRow) string {
	q := r.quota
	if q == "" {
		q = "0"
	}
	return `{"openpanel_user":"` + jsonEsc(opUser) + `","username":"` + jsonEsc(r.user) + `","password":"` + jsonEsc(r.pass) + `","directory":"` + jsonEsc(r.dir) + `","uid":"` + jsonEsc(r.uid) + `","gid":"` + jsonEsc(r.gid) + `","quota_mb":"` + jsonEsc(q) + `","used_kb":"` + jsonEsc(usedKB(dir, r.user)) + `"}`
}

func list(args []string) int {
	asJSON := false
	var a []string
	for _, x := range args {
		if x == "--json" {
			asJSON = true
		} else {
			a = append(a, x)
		}
	}
	if len(a) == 0 || a[0] == "" {
		ents, _ := filepath.Glob(usersDir + "*")
		sort.Strings(ents)
		var parts []string
		for _, c := range ents {
			st, err := os.Stat(c)
			if err != nil || !st.IsDir() {
				continue
			}
			name := filepath.Base(c)
			f := c + "/users.list"
			if fi, err := os.Stat(f); err != nil || !fi.Mode().IsRegular() {
				continue
			}
			if asJSON {
				for _, r := range readRows(f) {
					if r.user != "" {
						parts = append(parts, jsonRow(name, c, r))
					}
				}
			} else {
				fmt.Printf("FTP sub-users for '%s':\n", name)
				for _, r := range readRows(f) {
					fmt.Printf("%s | %s | %s\n", r.user, r.dir, quotaText(r.quota))
				}
				fmt.Println()
			}
		}
		if asJSON {
			fmt.Println("[" + strings.Join(parts, ",") + "]")
		}
		return 0
	}

	name := a[0]
	db.Require()
	ctx, _ := db.Cmd("SELECT server FROM users WHERE username = '"+db.Escape(name)+"' LIMIT 1;", "-N").NoErr().Out()
	if ctx == "" {
		ctx = name
	}
	dir := usersDir + ctx
	f := dir + "/users.list"
	if fi, err := os.Stat(f); err != nil || !fi.Mode().IsRegular() {
		if asJSON {
			fmt.Println("[]")
		} else {
			fmt.Printf("No FTP sub-users for OpenPanel user: '%s'.\n", name)
		}
		return 1
	}
	if asJSON {
		var parts []string
		for _, r := range readRows(f) {
			if r.user != "" {
				parts = append(parts, jsonRow(name, dir, r))
			}
		}
		fmt.Println("[" + strings.Join(parts, ",") + "]")
	} else {
		fmt.Printf("FTP sub-users for '%s':\n", ctx)
		for _, r := range readRows(f) {
			fmt.Printf("%s | %s | %s\n", r.user, r.dir, quotaText(r.quota))
		}
	}
	return 0
}
