// ##############################################################################
// Script Name: internal/commands/email/default_sync.go
// Description: Writes the default (catch-all) email addresses to docker-mailserver's postfix-regexp.cf.
// Usage: opencli email-default_sync
// Author: Stefan Pejcic
// Created: 02.11.2026
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
	"os"
	"regexp"
	"strings"
	"syscall"

	"opencli/internal/cli"
	"opencli/internal/conf"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "email-default_sync",
		Desc:  "Writes the default (catch-all) email addresses to docker-mailserver's postfix-regexp.cf.",
		Usage: "opencli email-default_sync",
		Docs:  "https://docs.openpanel.com",
		Run:   defaultSync,
	})
}

const (
	dsConfigDir = "/usr/local/mail/openmail/docker-data/dms/config"
	dsBegin     = "# BEGIN opencli email-default_sync"
	dsEnd       = "# END opencli email-default_sync"
)

var (
	dsSkipRe = regexp.MustCompile(`^[ \t]*(#|$)`)
	dsDomRe  = regexp.MustCompile(`^/\*@[a-z0-9.-]+/$`)
	dsDestRe = regexp.MustCompile(`^[a-z0-9._%+-]+@[a-z0-9.-]+$`)
)

// dsEsc backslashes every byte that isn't a-z or 0-9
func dsEsc(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteByte(c)
		} else {
			b.WriteByte('\\')
			b.WriteByte(c)
		}
	}
	return b.String()
}

func dsRead(name string) string {
	b, _ := os.ReadFile(dsConfigDir + "/" + name)
	return string(b)
}

func defaultSync(args []string) int {
	if !conf.IsDir(dsConfigDir) {
		return 0
	}
	// the panel and email-setup both run this, one at a time so an older read never wins
	dir, err := os.Open(dsConfigDir)
	if err != nil {
		return 1
	}
	defer dir.Close()
	if syscall.Flock(int(dir.Fd()), syscall.LOCK_EX) != nil {
		return 1
	}
	out := dsConfigDir + "/postfix-regexp.cf"
	tmp, err := os.CreateTemp(dsConfigDir, ".postfix-regexp.cf.")
	if err != nil {
		return 1
	}
	defer os.Remove(tmp.Name())

	names := map[string]string{}
	box := map[string]bool{}
	known := func(a string) {
		a = asciiLower(a)
		at := strings.Index(a, "@")
		if at < 1 {
			return
		}
		d := a[at+1:]
		box[a] = true
		l := dsEsc(a[:at])
		if prev, ok := names[d]; ok {
			l = prev + "|" + l
		}
		names[d] = l
	}

	var res strings.Builder
	had, skip := false, false
	for _, l := range recLines(dsRead("postfix-regexp.cf")) {
		if l == dsBegin {
			skip, had = true, true
		}
		if !skip {
			res.WriteString(l + "\n")
		}
		if l == dsEnd {
			skip = false
		}
	}
	for _, l := range recLines(dsRead("postfix-accounts.cf")) {
		if dsSkipRe.MatchString(l) {
			continue
		}
		known(strings.SplitN(l, "|", 2)[0])
	}
	for _, l := range recLines(dsRead("postfix-virtual.cf")) {
		if dsSkipRe.MatchString(l) {
			continue
		}
		if f := awkFields(l); len(f) > 0 {
			known(f[0])
		}
	}
	rules := ""
	seen := map[string]bool{}
	for _, l := range recLines(dsRead("postfix-regex.cf")) {
		if dsSkipRe.MatchString(l) {
			continue
		}
		f := awkFields(l)
		var d, t string
		if len(f) > 0 {
			d = asciiLower(f[0])
		}
		if len(f) > 1 {
			t = asciiLower(f[1])
		}
		if !dsDomRe.MatchString(d) || !dsDestRe.MatchString(t) {
			continue
		}
		d = d[3 : len(d)-1]
		if seen[d] {
			continue
		}
		seen[d] = true
		// docker-mailserver takes mail only for a domain with a mailbox or alias here, postfix applies the rules to every recipient
		if _, ok := names[d]; !ok {
			continue
		}
		// a destination on a domain hosted here has to exist, or its mail would be accepted and then bounced
		td := t[strings.Index(t, "@")+1:]
		if _, ok := names[td]; !box[t] && ok {
			continue
		}
		rules += "/^(?!(?:" + names[d] + ")(?:\\+[^@]*)?@)[^@]+@" + dsEsc(d) + "$/ " + t + "\n"
	}
	if rules != "" || had {
		res.WriteString(dsBegin + "\n" + rules + dsEnd + "\n")
	}
	tmp.WriteString(res.String())
	tmp.Close()

	// rewritten, never deleted: docker-mailserver keeps the rules of a removed file
	if res.Len() > 0 {
		os.Chmod(tmp.Name(), 0o644)
		os.Rename(tmp.Name(), out)
	}
	return 0
}
