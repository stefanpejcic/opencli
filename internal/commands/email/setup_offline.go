// ##############################################################################
// Script Name: internal/commands/email/setup_offline.go
// Description: Offline fallbacks of email-setup that edit the mailserver config files directly.
// Usage: internal helper
// Author: Stefan Pejcic
// Created: 18.08.2024
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
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"opencli/internal/conf"
	"opencli/internal/sh"
)

const (
	suConfigDir   = mailDir + "/docker-data/dms/config"
	suAccounts    = suConfigDir + "/postfix-accounts.cf"
	suQuotas      = suConfigDir + "/dovecot-quotas.cf"
	suComposeFile = mailDir + "/compose.yml"
)

var suEmailRe = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

func suEprintf(format string, a ...any) { fmt.Fprintf(os.Stderr, format, a...) }

// suHasKey is grep -q "^key<sep>" with dots escaped, so a plain prefix match
func suHasKey(file, key, sep string, ci bool) bool {
	b, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	p := key + sep
	if ci {
		p = strings.ToLower(p)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if ci {
			l = strings.ToLower(l)
		}
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// suEdit is sed -i over a file, fn gets each line without the newline and says whether to keep it and its new text
func suEdit(file string, fn func(l string) (string, bool)) {
	b, err := os.ReadFile(file)
	if err != nil {
		return
	}
	var out strings.Builder
	for _, l := range strings.SplitAfter(string(b), "\n") {
		if l == "" {
			continue
		}
		nl := ""
		t := l
		if strings.HasSuffix(l, "\n") {
			nl, t = "\n", strings.TrimSuffix(l, "\n")
		}
		if n, keep := fn(t); keep {
			out.WriteString(n + nl)
		}
	}
	conf.WriteFileKeep(file, out.String(), false)
}

func suMailserverImage() string {
	in := false
	for _, l := range recLines(conf.Read(suComposeFile)) {
		if strings.HasPrefix(l, "  mailserver:") {
			in = true
			continue
		}
		if len(l) > 2 && l[:2] == "  " && (l[2] >= 'a' && l[2] <= 'z' || l[2] >= 'A' && l[2] <= 'Z') {
			in = false
		}
		if in && regexp.MustCompile(`^ *image:`).MatchString(l) {
			f := awkFields(l)
			if len(f) > 1 {
				return f[1]
			}
			return ""
		}
	}
	return ""
}

func suHash(email, password string) (string, bool) {
	image := suMailserverImage()
	if image == "" {
		suEprintf("Error: could not determine mailserver image from '%s'\n", suComposeFile)
		return "", false
	}
	out, _ := sh.C("podman", "run", "--rm", "--entrypoint", "doveadm", image, "pw", "-s", "SHA512-CRYPT", "-u", email, "-p", password).Timeout(30 * time.Second).NoErr().Out()
	return out, true
}

func suTouch(file string) {
	if f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
	}
	os.Chtimes(file, time.Now(), time.Now())
}

func suAppend(file, line string) {
	if f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
		f.WriteString(line + "\n")
		f.Close()
	}
}

func suEmailAdd(args []string) int {
	email, password := "", ""
	if len(args) > 0 {
		email = strings.ToLower(args[0])
	}
	if len(args) > 1 {
		password = args[1]
	}
	if !suEmailRe.MatchString(email) {
		suEprintf("Error: '%s' is not a valid email address\n", email)
		return 1
	}
	os.MkdirAll(suConfigDir, 0o755)
	suTouch(suAccounts)
	if suHasKey(suAccounts, email, "|", true) {
		suEprintf("Error: '%s' already exists\n", email)
		return 1
	}
	hash, ok := suHash(email, password)
	if !ok || hash == "" {
		suEprintf("Error: failed to hash password for '%s'\n", email)
		return 1
	}
	suAppend(suAccounts, email+"|"+hash)
	return 0
}

func suEmailUpdate(args []string) int {
	email, password := "", ""
	if len(args) > 0 {
		email = args[0]
	}
	if len(args) > 1 {
		password = args[1]
	}
	if !suEmailRe.MatchString(email) {
		suEprintf("Error: '%s' is not a valid email address\n", email)
		return 1
	}
	if !suHasKey(suAccounts, email, "|", true) {
		suEprintf("Error: '%s' does not exist\n", email)
		return 1
	}
	hash, ok := suHash(email, password)
	if !ok || hash == "" {
		suEprintf("Error: failed to hash password for '%s'\n", email)
		return 1
	}
	suEdit(suAccounts, func(l string) (string, bool) {
		if strings.HasPrefix(l, email+"|") {
			return email + "|" + hash, true
		}
		return l, true
	})
	return 0
}

func suEmailDel(args []string) int {
	maildel := false
	if len(args) > 0 {
		switch args[0] {
		case "-y", "-Y":
			maildel, args = true, args[1:]
		case "-n", "-N":
			args = args[1:]
		}
	}
	if !conf.IsFile(suAccounts) {
		return 0
	}
	for _, email := range args {
		low := strings.ToLower(email + "|")
		suEdit(suAccounts, func(l string) (string, bool) { return l, !strings.HasPrefix(strings.ToLower(l), low) })
		if conf.IsFile(suQuotas) {
			q := strings.ToLower(email + ":")
			suEdit(suQuotas, func(l string) (string, bool) { return l, !strings.HasPrefix(strings.ToLower(l), q) })
		}
		if maildel {
			user, domain := email, email
			if i := strings.LastIndex(email, "@"); i >= 0 {
				user = email[:i]
			}
			if i := strings.Index(email, "@"); i >= 0 {
				domain = email[i+1:]
			}
			os.RemoveAll("/var/mail/" + domain + "/" + user)
		}
	}
	return 0
}

func suEmailList(w io.Writer) int {
	if st, err := os.Stat(suAccounts); err != nil || st.Size() == 0 {
		return 0
	}
	skip := regexp.MustCompile(`^\s*($|#)`)
	for _, l := range recLines(conf.Read(suAccounts)) {
		if skip.MatchString(l) {
			continue
		}
		login := strings.SplitN(l, "|", 2)[0]
		if login != "" {
			fmt.Fprintf(w, "* %s\n\n", login)
		}
	}
	return 0
}

func suQuotaSet(args []string) int {
	email, quota := "", ""
	if len(args) > 0 {
		email = args[0]
	}
	if len(args) > 1 {
		quota = args[1]
	}
	if !suEmailRe.MatchString(email) {
		suEprintf("Error: '%s' is not a valid email address\n", email)
		return 1
	}
	if !suHasKey(suAccounts, email, "|", true) {
		suEprintf("Error: '%s' does not exist\n", email)
		return 1
	}
	suTouch(suQuotas)
	if suHasKey(suQuotas, email, ":", true) {
		suEdit(suQuotas, func(l string) (string, bool) {
			if strings.HasPrefix(l, email+":") {
				return email + ":" + quota, true
			}
			return l, true
		})
	} else {
		suAppend(suQuotas, email+":"+quota)
	}
	return 0
}

func suQuotaDel(args []string) int {
	email := ""
	if len(args) > 0 {
		email = args[0]
	}
	if !conf.IsFile(suQuotas) {
		return 1
	}
	q := strings.ToLower(email + ":")
	suEdit(suQuotas, func(l string) (string, bool) { return l, !strings.HasPrefix(strings.ToLower(l), q) })
	return 0
}
