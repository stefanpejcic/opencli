// ##############################################################################
// Script Name: internal/commands/general/helpers.go
// Description: Small helpers shared by the general commands.
// Usage: imported by the general commands
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

package general

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"opencli/internal/conf"
)

// sedFile edits a file like sed -i, prints sed's error and returns false when it can't be read
func sedFile(path string, edit func(lines []string) []string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sed: can't read %s: No such file or directory\n", path)
		return false
	}
	s := string(b)
	nl := strings.HasSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	var lines []string
	if s != "" || nl {
		lines = strings.Split(s, "\n")
	}
	lines = edit(lines)
	out := strings.Join(lines, "\n")
	if nl && len(lines) > 0 {
		out += "\n"
	}
	writeKeep(path, out)
	return true
}

func writeKeep(path, content string) { conf.WriteFileKeep(path, content, false) }

// sedRange applies fn to lines inside sed's /start/,/end/ address ranges
func sedRange(lines []string, start, end *regexp.Regexp, fn func(string) string) []string {
	in := false
	for i, l := range lines {
		if !in {
			if start.MatchString(l) {
				in = true
				lines[i] = fn(l)
			}
			continue
		}
		lines[i] = fn(l)
		if end.MatchString(l) {
			in = false
		}
	}
	return lines
}

// curlGet is `curl --silent --max-time N -4 url`, the body read so far comes back even on failure
func curlGet(url string, maxTime time.Duration) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), maxTime)
	defer cancel()
	dialer := &net.Dialer{}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", addr)
		},
		TLSClientConfig: &tls.Config{},
	}
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", false
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err == nil
}

var ipv4Re = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$`)

// publicIP asks ip.openpanel.com, then ifconfig.me, then falls back to the first local address
func publicIP() string {
	ip, ok := curlGet("https://ip.openpanel.com", time.Second)
	if !ok {
		ip2, _ := curlGet("https://ifconfig.me/ip", time.Second)
		ip += ip2
	}
	ip = strings.TrimRight(ip, "\n")
	if ip == "" || !ipv4Re.MatchString(ip) {
		ip = firstHostIP()
	}
	return ip
}

// firstHostIP is `hostname -I | awk '{print $1}'`
func firstHostIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var v4, v6 []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			if ipn.IP.To4() != nil {
				v4 = append(v4, ipn.IP.String())
			} else {
				v6 = append(v6, ipn.IP.String())
			}
		}
	}
	if all := append(v4, v6...); len(all) > 0 {
		return all[0]
	}
	return ""
}

// between returns the lines between two marker lines, like awk '/START/{f=1;next} /END/{f=0} f'
func between(lines []string, start, end string) []string {
	var out []string
	flag := false
	for _, l := range lines {
		if strings.Contains(l, start) {
			flag = true
			continue
		}
		if strings.Contains(l, end) {
			flag = false
		}
		if flag {
			out = append(out, l)
		}
	}
	return out
}

var blankRe = regexp.MustCompile(`^\s*$`)
var commentRe = regexp.MustCompile(`^\s*#`)

// firstContentLine is `sed '/^\s*$/d' | grep -v '^\s*#' | head -n1`
func firstContentLine(lines []string) string {
	for _, l := range lines {
		if blankRe.MatchString(l) || commentRe.MatchString(l) {
			continue
		}
		return l
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// hasCert checks the letsencrypt and custom cert pairs caddy uses for a domain
func hasCert(domain string) bool {
	le := "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/" + domain + "/" + domain
	custom := "/etc/openpanel/caddy/ssl/custom/" + domain + "/" + domain
	return fileExists(le+".crt") && fileExists(le+".key") || fileExists(custom+".crt") && fileExists(custom+".key")
}

// subFirst is sed's s/re/repl/ without g, repl is literal
func subFirst(re *regexp.Regexp, s, repl string) string {
	loc := re.FindStringIndex(s)
	if loc == nil {
		return s
	}
	return s[:loc[0]] + repl + s[loc[1]:]
}

// grepCut is `grep "^key=" file | cut -d= -f2-` with every match kept
func grepCut(path, key string) string {
	var vals []string
	for _, l := range conf.Lines(path) {
		if strings.HasPrefix(l, key+"=") {
			vals = append(vals, l[len(key)+1:])
		}
	}
	return strings.TrimRight(strings.Join(vals, "\n"), "\n")
}
