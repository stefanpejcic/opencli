// ##############################################################################
// Script Name: internal/passwd/passwd.go
// Description: Password strength scoring and the weakpass common password check.
// Usage: imported by command packages
// Author: Stefan Pejcic
// Created: 20.11.2026
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

package passwd

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opencli/internal/cli"
	"opencli/internal/conf"
)

// Score is the 0-100 six check rubric, keep in sync with the panel's validators and password-strength.js
func Score(p string) int {
	if p == "" {
		return 0
	}
	score := 0
	n := utf8.RuneCountInString(p)
	if n >= 8 {
		score++
	}
	if n >= 12 {
		score++
	}
	if strings.ContainsFunc(p, func(r rune) bool { return r >= 'a' && r <= 'z' }) {
		score++
	}
	if strings.ContainsFunc(p, func(r rune) bool { return r >= 'A' && r <= 'Z' }) {
		score++
	}
	if strings.ContainsFunc(p, func(r rune) bool { return r >= '0' && r <= '9' }) {
		score++
	}
	// glibc C.UTF-8 counts every unicode punctuation and symbol as [[:punct:]], e.g. €
	if strings.ContainsFunc(p, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) }) {
		score++
	}
	return (score*100 + 3) / 6
}

// Threshold is password_strength from openpanel.config clamped to 1..100, default 50
func Threshold() int {
	raw, _ := conf.Raw(conf.Panel, "password_strength")
	raw = conf.StripQuotes(raw)
	v, err := strconv.Atoi(raw)
	if err != nil || strings.TrimLeft(raw, "0123456789") != "" || raw == "" {
		v = 50
	}
	if v < 1 {
		v = 1
	}
	if v > 100 {
		v = 100
	}
	return v
}

// RequireStrength exits with an error when the password scores below the threshold
func RequireStrength(p string) {
	s, t := Score(p), Threshold()
	if s < t {
		fmt.Printf("ERROR: Password is too weak (strength %d/100, minimum required is %d/100).\n", s, t)
		cli.Exit(1)
	}
}

const (
	weakDict = "/tmp/weakpass_top1m.txt"
	weakURL  = "https://weakpass.com/download/50/10_million_password_list_top_1000000.txt.gz"
)

// WeakEnabled is on unless the admin set weakpass=no
func WeakEnabled() bool {
	v, _ := conf.Raw(conf.Panel, "weakpass")
	return conf.StripQuotes(v) != "no"
}

// UpdateDict downloads the list if missing or older than 7 days, keeps the old copy on failure
func UpdateDict() {
	// find -mtime +7 only matches files 8 full days old
	if st, err := os.Stat(weakDict); err == nil && st.Size() > 0 && time.Since(st.ModTime()) < 8*24*time.Hour {
		return
	}
	tmp := fmt.Sprintf("%s.tmp.%d", weakDict, os.Getpid())
	if download(tmp) {
		os.Rename(tmp, weakDict)
		return
	}
	os.Remove(tmp)
	fmt.Fprintln(os.Stderr, "[!] Warning: Could not fetch weak-password dictionary.")
}

func download(dst string) bool {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(weakURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return false
	}
	f, err := os.Create(dst)
	if err != nil {
		return false
	}
	n, err := io.Copy(f, gz)
	f.Close()
	return err == nil && n > 0
}

// IsCommon is true when the check is on and the password is in the list, case insensitive whole line
func IsCommon(p string) bool {
	if !WeakEnabled() {
		return false
	}
	UpdateDict()
	f, err := os.Open(weakDict)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if strings.EqualFold(sc.Text(), p) {
			return true
		}
	}
	return false
}

// RequireNotCommon exits with an error for a common password
func RequireNotCommon(p string) {
	if IsCommon(p) {
		fmt.Println("ERROR: Password is in the list of common passwords. Use a stronger password or disable the check with: opencli config update weakpass no")
		cli.Exit(1)
	}
}
