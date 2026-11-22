// ##############################################################################
// Script Name: internal/commands/ftp/util.go
// Description: Shared helpers for the ftp commands.
// Usage: internal helper
// Author: Stefan Pejcic
// Created: 22.05.2024
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
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strings"
	"syscall"
	"unicode/utf8"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/passwd"
	"opencli/internal/sh"
)

const usersDir = "/etc/openpanel/ftp/users/"

var ftpNameRe = regexp.MustCompile(`^[A-Za-z0-9._@-]+$`)

// validName is the username check shared by delete, password, path and quota
func validName(u string) {
	if u == "" || strings.Count(u, "@") != 1 || strings.HasPrefix(u, "@") || strings.HasSuffix(u, "@") || !ftpNameRe.MatchString(u) {
		cli.Fail("ERROR: Invalid FTP username '%s'.", u)
	}
}

// contextFor checks the panel user owns the domain of the ftp username and returns the server (context)
func contextFor(opUser, ftpUser string) string {
	domain := ftpUser
	if i := strings.Index(domain, "@"); i >= 0 {
		domain = domain[i+1:]
	}
	q := "SELECT u.server FROM users u WHERE u.username='" + db.Escape(opUser) + "' AND EXISTS (SELECT 1 FROM domains d WHERE d.domain_url = '" + db.Escape(domain) + "' AND d.user_id = u.id);"
	ctx, _ := db.Plain(q, "-N").Out()
	if ctx == "" {
		cli.Fail("ERROR: No context found for user '%s' - or does not own the domain name. Aborting!", opUser)
	}
	return ctx
}

func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}

func sentinelBg(args ...string) {
	sh.Opencli(append([]string{"sentinel"}, args...)...).Quiet().Background()
}

func ftpExec(args ...string) *sh.Cmd {
	return sh.C("podman", append([]string{"exec", "openadmin_ftp"}, args...)...)
}

func mkUsersList(ctx string) string {
	os.MkdirAll(usersDir+ctx, 0o755)
	f := usersDir + ctx + "/users.list"
	if fh, err := os.OpenFile(f, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fh.Close()
	}
	return f
}

// hasUser is grep -Fq "user|"
func hasUser(file, user string) bool {
	b, err := os.ReadFile(file)
	return err == nil && strings.Contains(string(b), user+"|")
}

func checkPassword(pw, pre string) {
	docs := pre == "" // add prints a docs line after each error
	fail := func(msg string) {
		fmt.Println("ERROR: " + msg)
		if docs {
			fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#ftp")
		}
		cli.Exit(1)
	}
	p := "Password"
	if !docs {
		p = "New password"
	}
	if utf8.RuneCountInString(pw) < 8 {
		fail(p + " is too short. It must be at least 8 characters long.")
	}
	if !regexp.MustCompile(`[A-Z]`).MatchString(pw) {
		fail(p + " must contain at least one uppercase letter.")
	}
	if !regexp.MustCompile(`[a-z]`).MatchString(pw) {
		fail(p + " must contain at least one lowercase letter.")
	}
	if !regexp.MustCompile(`[0-9]`).MatchString(pw) {
		fail(p + " must contain at least one digit.")
	}
	if !regexp.MustCompile("[!-/:-@\\[-`{-~]").MatchString(pw) {
		fail(p + " must contain at least one special character.")
	}
	passwd.RequireStrength(pw)
}

// chmodPlusRX is chmod +rx, which respects the umask
func chmodPlusRX(path string) {
	um := syscall.Umask(0)
	syscall.Umask(um)
	st, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chmod: cannot access '%s': %v\n", path, err)
		return
	}
	os.Chmod(path, st.Mode().Perm()|os.FileMode(0o555&^um))
}

const b64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// sha512Crypt is crypt(3) with the $6$ method
func sha512Crypt(pw, salt string) string {
	if len(salt) > 16 {
		salt = salt[:16]
	}
	pwb, sb := []byte(pw), []byte(salt)
	hsum := func(parts ...[]byte) []byte {
		h := sha512.New()
		for _, p := range parts {
			h.Write(p)
		}
		return h.Sum(nil)
	}
	repeat := func(b []byte, n int) []byte {
		out := make([]byte, 0, n)
		for len(out) < n {
			out = append(out, b...)
		}
		return out[:n]
	}
	b := hsum(pwb, sb, pwb)
	actx := append(append([]byte{}, pwb...), sb...)
	actx = append(actx, repeat(b, len(pwb))...)
	for n := len(pwb); n > 0; n >>= 1 {
		if n&1 == 1 {
			actx = append(actx, b...)
		} else {
			actx = append(actx, pwb...)
		}
	}
	a := hsum(actx)
	var dp []byte
	for i := 0; i < len(pwb); i++ {
		dp = append(dp, pwb...)
	}
	p := repeat(hsum(dp), len(pwb))
	var ds []byte
	for i := 0; i < 16+int(a[0]); i++ {
		ds = append(ds, sb...)
	}
	s := repeat(hsum(ds), len(sb))
	c := a
	for i := 0; i < 5000; i++ {
		var parts [][]byte
		if i&1 == 1 {
			parts = append(parts, p)
		} else {
			parts = append(parts, c)
		}
		if i%3 != 0 {
			parts = append(parts, s)
		}
		if i%7 != 0 {
			parts = append(parts, p)
		}
		if i&1 == 1 {
			parts = append(parts, c)
		} else {
			parts = append(parts, p)
		}
		c = hsum(parts...)
	}
	var sb2 strings.Builder
	put := func(b2, b1, b0 byte, n int) {
		v := uint(b2)<<16 | uint(b1)<<8 | uint(b0)
		for ; n > 0; n-- {
			sb2.WriteByte(b64[v&0x3f])
			v >>= 6
		}
	}
	order := [][3]int{{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48}, {28, 49, 7}, {50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13}, {56, 14, 35}, {15, 36, 57}, {37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41}}
	for _, o := range order {
		put(c[o[0]], c[o[1]], c[o[2]], 4)
	}
	put(0, 0, c[63], 2)
	return "$6$" + salt + "$" + sb2.String()
}

// hashPassword mirrors the python snippet: 16 salt chars, then crypt
func hashPassword(pw string, base64Salt bool) string {
	buf := make([]byte, 16)
	rand.Read(buf)
	var salt string
	if base64Salt {
		salt = base64.StdEncoding.EncodeToString(buf[:12])[:16]
	} else {
		const al = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		sb := make([]byte, 16)
		for i := range sb {
			sb[i] = al[int(buf[i])%len(al)]
		}
		salt = string(sb)
	}
	return sha512Crypt(pw, salt)
}
