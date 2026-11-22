// ##############################################################################
// Script Name: internal/commands/email/util.go
// Description: Shared helpers for the email commands.
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
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
)

const (
	container      = "openadmin_mailserver"
	mailDir        = "/usr/local/mail/openmail"
	enterpriseLink = "https://my.openpanel.com/index.php?rp=/store/openpanel/enterprise-license"
)

// requireEnterprise is the community edition check, msg differs a bit between scripts
func requireEnterprise(msg string, code int) {
	if v, _ := conf.Raw(conf.Panel, "key"); v == "" {
		fmt.Println(msg)
		fmt.Println(enterpriseLink)
		cli.Exit(code)
	}
}

const msgUnlimited = "Error: OpenPanel Community edition does not support emails. Please consider purchasing the Enterprise version that allows unlimited number of email addresses."

func emailGate() { requireEnterprise(msgUnlimited, 1) }

func sentinelBg(args ...string) {
	sh.Opencli(append([]string{"sentinel"}, args...)...).Quiet().Background()
}

// awkFields splits like awk's default FS, blanks and newlines only
func awkFields(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' })
}

// recLines is awk's record split, the final newline doesn't make an extra empty record
func recLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// grepAt is `grep "@${domain}"` where the dots stay regex wildcards
func grepAt(text, domain string) []string {
	re, err := regexp.Compile("@" + strings.ReplaceAll(regexp.QuoteMeta(domain), `\.`, "."))
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if re.MatchString(l) {
			out = append(out, l)
		}
	}
	return out
}

// mvFile is mv, falling back to copy when the rename crosses filesystems
func mvFile(src, dst string) {
	if os.Rename(src, dst) == nil {
		return
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return
	}
	os.Remove(dst)
	os.WriteFile(dst, b, 0o600)
	os.Remove(src)
}

func countNL(s string) int { return strings.Count(s, "\n") }

// chownLike is chown --reference
func chownLike(path string, st os.FileInfo) {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		os.Lchown(path, int(s.Uid), int(s.Gid))
	}
}

func nowT() time.Time { return time.Now() }
