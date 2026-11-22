// ##############################################################################
// Script Name: internal/commands/domainsadd/util.go
// Description: Shared helpers for domains-add, domains-delete, domains-dns and domains-dnssec.
// Usage: n/a
// Author: Stefan Pejcic
// Created: 20.08.2024
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

package domainsadd

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"opencli/internal/conf"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

// ownerOf is `opencli domains-whoowns D | awk -F "Owner of 'D': " '{print $2}'`
func ownerOf(domain string) string {
	out, _ := sh.Opencli("domains-whoowns", domain).Out()
	return awkSecondField(out, "Owner of '"+domain+"': ")
}

// awkSecondField is awk -F sep '{print $2}' over all lines, sep is a regex like in awk, trailing newlines trimmed
func awkSecondField(out, sep string) string {
	re, err := regexp.Compile(sep)
	if err != nil {
		return ""
	}
	var res []string
	for _, l := range strings.Split(out, "\n") {
		parts := re.Split(l, -1)
		if len(parts) > 1 {
			res = append(res, parts[1])
		} else {
			res = append(res, "")
		}
	}
	return strings.TrimRight(strings.Join(res, "\n"), "\n")
}

// envValue is the grep ^KEY= | head -n1 | awk -F= '{print $2}' | tr -d space | strip quotes chain
func envValue(ctx, key string) string {
	for _, l := range conf.Lines("/home/" + ctx + "/.env") {
		if strings.HasPrefix(l, key+"=") {
			f := strings.Split(l, "=")
			v := ""
			if len(f) > 1 {
				v = f[1]
			}
			v = strings.Join(strings.Fields(v), "")
			if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
				v = v[1 : len(v)-1]
			}
			return v
		}
	}
	return ""
}

// curlGet is `curl --silent --max-time N -4/-6 url`, any http answer counts, ok is false on transport errors
func curlGet(url string, timeout time.Duration, network string) (string, bool) {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{},
	}
	c := &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return strings.TrimRight(string(b), "\n"), err == nil
}

// wgetGet is `wget --timeout=N --tries=1 -qO- url`, only a 2xx answer counts
func wgetGet(url string, timeout time.Duration) (string, bool) {
	c := &http.Client{Timeout: timeout}
	resp, err := c.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", false
	}
	b, err := io.ReadAll(resp.Body)
	return strings.TrimRight(string(b), "\n"), err == nil
}

// ipGlobal is `ip addr show | awk '/inet / && /global/{print $2; exit}' | cut -d/ -f1`, v6 looks for inet6
func ipGlobal(v6 bool) string {
	needle := "inet "
	if v6 {
		needle = "inet6 "
	}
	for _, l := range strings.Split(sh.C("ip", "addr", "show").NoErr().Str(), "\n") {
		if strings.Contains(l, needle) && strings.Contains(l, "global") {
			f := strings.Fields(l)
			if len(f) < 2 {
				return ""
			}
			ip, _, _ := strings.Cut(f[1], "/")
			return ip
		}
	}
	return ""
}

// ipJSON is jq -r '.ip // empty' on a user's ip.json
func ipJSON(path string) string {
	o, err := ojson.ParseObject(conf.Read(path))
	if err != nil {
		return ""
	}
	v, ok := o.Get("ip")
	if !ok || v == nil {
		return ""
	}
	if b, isBool := v.(bool); isBool && !b {
		return ""
	}
	return ojson.Text(v)
}

// touch creates the file when missing and bumps its mtime
func touch(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	f.Close()
	now := time.Now()
	os.Chtimes(path, now, now)
}

// appendFile is `echo ... >> file`
func appendFile(path, s string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(s)
	return err
}

// replaceAll runs the sed s|a|b|g expressions one after another
func replaceAll(s string, pairs ...string) string {
	for i := 0; i+1 < len(pairs); i += 2 {
		s = strings.ReplaceAll(s, pairs[i], pairs[i+1])
	}
	return s
}

// idnEncode is the bash idn_encode, python's idna codec for anything that isn't plain ascii
func idnEncode(d string) string {
	if !strings.ContainsFunc(d, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-')
	}) {
		return d
	}
	if !sh.Has("python3") {
		return d
	}
	out, err := sh.C("python3", "-c", `import sys; print(sys.argv[1].encode("idna").decode("ascii"))`, d).NoErr().Raw()
	if err != nil {
		return d
	}
	return strings.TrimSuffix(out, "\n")
}

// idnDecode is the bash idn_decode, punycode to unicode
func idnDecode(d string) string {
	if !sh.Has("python3") {
		return d
	}
	out, err := sh.C("python3", "-c", `import sys; print(sys.argv[1].encode("ascii").decode("idna"))`, d).NoErr().Raw()
	if err != nil {
		return d
	}
	return strings.TrimSuffix(out, "\n")
}

// cutField2 is `cut -d: -f2`, a line without the delimiter comes back whole
func cutField2(s, delim string) string {
	if !strings.Contains(s, delim) {
		return s
	}
	f := strings.Split(s, delim)
	return f[1]
}

var dkimRe = regexp.MustCompile(`^mail\._domainkey[[:space:]]+IN`)

// dkimBlock is the "\n" + sed'ed mail.txt + "\n" appended to a zone file
func dkimBlock(dkimFile string) string {
	b, _ := os.ReadFile(dkimFile)
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		lines[i] = dkimRe.ReplaceAllLiteralString(l, "mail._domainkey\t14400\tIN")
	}
	return "\n" + strings.Join(lines, "\n") + "\n"
}

// wgetFile is `wget --timeout=5 --tries=N -q --inet4-only -O dest url`, dest is left behind on failure like wget does
func wgetFile(url, dest string, tries int) bool {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp4", addr)
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	}
	c := &http.Client{Transport: tr, Timeout: 120 * time.Second}
	for i := 0; i < tries; i++ {
		resp, err := c.Get(url)
		if err != nil {
			continue
		}
		f, ferr := os.Create(dest)
		if ferr != nil {
			resp.Body.Close()
			return false
		}
		_, cerr := io.Copy(f, resp.Body)
		resp.Body.Close()
		f.Close()
		if cerr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return true
		}
	}
	return false
}

// firstHostIP is `hostname -I | awk '{print $1}'`
func firstHostIP() string {
	f := strings.Fields(sh.C("hostname", "-I").NoErr().Str())
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// statUID is `stat -c %u`
func statUID(path string) (int, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(s.Uid), true
}

// silence runs fn with stdout and stderr thrown away, like `fn >/dev/null 2>&1`
func silence(fn func()) {
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		fn()
		return
	}
	defer null.Close()
	so, se := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = null, null
	defer func() { os.Stdout, os.Stderr = so, se }()
	fn()
}

// readLoopLines are the lines a `while read` loop sees, a last line without newline is dropped
func readLoopLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := string(b)
	i := strings.LastIndex(s, "\n")
	if i < 0 {
		return nil
	}
	return strings.Split(s[:i], "\n")
}

// sedRegexLiteral turns a value used raw inside a sed/grep regex into a Go regex, dots keep matching anything
func sedRegexLiteral(s string) string {
	return strings.ReplaceAll(regexp.QuoteMeta(s), `\.`, ".")
}

// writeLines is what `sed -i` leaves behind, a new file with the lines and a final newline
func writeLines(path string, lines []string) {
	s := strings.Join(lines, "\n")
	if len(lines) > 0 {
		s += "\n"
	}
	conf.WriteFileKeep(path, s, false)
}
