// ##############################################################################
// Script Name: internal/phplisten/phplisten.go
// Description: How a user's webserver reaches php-fpm: TCP port 9000 (default), a unix socket, or caddy talking to the socket directly.
// Usage: imported by command packages
// Author: Stefan Pejcic
// Created: 01.11.2026
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

package phplisten

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"opencli/internal/conf"
)

// SocketDir is the socket path inside the php-fpm and webserver containers, the per-version dir keeps the php-fpm-X.Y/ naming
const SocketDir = "/run/php"

// EnvValue reads a key from the user's .env with quotes and whitespace removed
func EnvValue(ctx, key string) string { return conf.Env("/home/"+ctx+"/.env", key) }

// Mode is port (default), socket, or caddy when caddy talks to php-fpm itself
func Mode(ctx string) string {
	if EnvValue(ctx, "WEB_SERVER") == "caddy" {
		return "caddy"
	}
	if EnvValue(ctx, "PHP_FPM_LISTEN") == "socket" {
		return "socket"
	}
	return "port"
}

// HostSocket is the host path of a php version's socket, what caddy dials in caddy mode
func HostSocket(ctx, version string) string {
	return "/home/" + ctx + "/sockets/php/php-fpm-" + version + "/www.sock"
}

var (
	toSockApache = regexp.MustCompile(`proxy:fcgi://(php-fpm-[0-9.]+):9000`)
	toSockNginx  = regexp.MustCompile(`fastcgi_pass([[:space:]]+)(php-fpm-[0-9.]+):9000;`)
	toPortApache = regexp.MustCompile(`proxy:unix:` + SocketDir + `/(php-fpm-[0-9.]+)/www\.sock\|fcgi://localhost`)
	toPortNginx  = regexp.MustCompile(`fastcgi_pass([[:space:]]+)unix:` + SocketDir + `/(php-fpm-[0-9.]+)/www\.sock;`)
)

// VhostTransport rewrites a vhost's php-fpm upstreams to port or socket, apache SetHandler and nginx fastcgi_pass
func VhostTransport(mode, vhost string) string {
	if mode == "socket" {
		vhost = toSockApache.ReplaceAllString(vhost, "proxy:unix:"+SocketDir+"/${1}/www.sock|fcgi://localhost")
		return toSockNginx.ReplaceAllString(vhost, "fastcgi_pass${1}unix:"+SocketDir+"/${2}/www.sock;")
	}
	vhost = toPortApache.ReplaceAllString(vhost, "proxy:fcgi://${1}:9000")
	return toPortNginx.ReplaceAllString(vhost, "fastcgi_pass${1}${2}:9000;")
}

var (
	reverseProxy = regexp.MustCompile(`^\s*reverse_proxy\s+https?://`)
	indent       = regexp.MustCompile(`^(\s*)`)
)

// CaddyConfDirect comments out the domain's reverse_proxy blocks and puts php_fastcgi on the user's socket instead
func CaddyConfDirect(confPath, ctx, docroot, php string) error {
	sock := HostSocket(ctx, php)
	rel := "/" + strings.TrimLeft(docroot, "/")
	if strings.HasPrefix(docroot, "/var/www/html") {
		rel = docroot[len("/var/www/html"):]
	}
	hostRoot := strings.TrimRight(fmt.Sprintf("/home/%s/docker-data/volumes/%s_html_data/_data%s", ctx, ctx, rel), "/")
	b, err := os.ReadFile(confPath)
	if err != nil {
		return err
	}
	lines := undoDirect(strings.Split(string(b), "\n"))
	ctRoot := strings.TrimRight(docroot, "/")
	if ctRoot == "" {
		ctRoot = "/var/www/html"
	}
	var out []string
	for i := 0; i < len(lines); {
		l := lines[i]
		if reverseProxy.MatchString(l) {
			ind := indent.FindStringSubmatch(l)[1]
			out = append(out, ind+"#php-direct-begin", ind+"root * "+hostRoot, ind+"php_fastcgi unix/"+sock+" {",
				ind+"  root "+ctRoot, ind+"}", ind+"file_server", ind+"#php-direct-end")
			depth := 0
			for i < len(lines) {
				depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
				out = append(out, "#php-proxy# "+lines[i])
				i++
				if depth <= 0 {
					break
				}
			}
			continue
		}
		out = append(out, l)
		i++
	}
	return os.WriteFile(confPath, []byte(strings.Join(out, "\n")), 0o644)
}

// undoDirect strips a previous direct block so a docroot or version change can just run again
func undoDirect(lines []string) []string {
	var out []string
	skip := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "#php-direct-begin" {
			skip = true
			continue
		}
		if t == "#php-direct-end" {
			skip = false
			continue
		}
		if !skip {
			out = append(out, strings.TrimPrefix(l, "#php-proxy# "))
		}
	}
	return out
}

// CaddyConfProxy undoes CaddyConfDirect, the original reverse_proxy blocks come back
func CaddyConfProxy(confPath string) error {
	b, err := os.ReadFile(confPath)
	if err != nil {
		return err
	}
	// same as sed '/#php-direct-begin/,/#php-direct-end/d' plus dropping the #php-proxy# prefix
	var out []string
	skip := false
	for _, l := range strings.SplitAfter(string(b), "\n") {
		if l == "" {
			continue
		}
		if !skip && strings.Contains(l, "#php-direct-begin") {
			skip = true
			continue
		}
		if skip {
			if strings.Contains(l, "#php-direct-end") {
				skip = false
			}
			continue
		}
		out = append(out, strings.TrimPrefix(l, "#php-proxy# "))
	}
	return conf.WriteInPlace(confPath, strings.Join(out, ""))
}

// ComposePatch adds the socket mount and PHPFPM_LISTEN env to a compose file, returns "patched" or "unchanged"
func ComposePatch(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(b), "\n")
	const mount = "./sockets/php:/run/php"
	const env = "PHPFPM_LISTEN=${PHP_FPM_LISTEN:-port}"
	webservers := map[string]bool{"nginx": true, "apache": true, "openresty": true}
	// indent of the first key under services:, yaml dumpers use 4 instead of 2
	u := 2
	for i, l := range lines {
		if regexp.MustCompile(`^services:\s*$`).MatchString(l) {
			for _, n := range lines[i+1:] {
				if strings.TrimSpace(n) != "" && !strings.HasPrefix(strings.TrimLeft(n, " \t"), "#") {
					u = len(n) - len(strings.TrimLeft(n, " "))
					break
				}
			}
			break
		}
	}
	svcRe := regexp.MustCompile(`^` + strings.Repeat(" ", u) + `([A-Za-z0-9_.-]+):\s*$`)
	endRe := regexp.MustCompile(fmt.Sprintf(`^ {0,%d}\S`, u))
	key := strings.Repeat(" ", 2*u)
	item := strings.Repeat(" ", 3*u) + "- "
	blockHas := func(start int, match func(string) bool) bool {
		for j := start + 1; j < len(lines) && !endRe.MatchString(lines[j]); j++ {
			if match(lines[j]) {
				return true
			}
		}
		return false
	}
	svcStart := map[string]int{}
	for idx, l := range lines {
		if m := svcRe.FindStringSubmatch(l); m != nil {
			svcStart[m[1]] = idx
		}
	}
	envKey := regexp.MustCompile("^" + key + `environment:`)
	var out []string
	svc := ""
	changed := false
	for _, l := range lines {
		if m := svcRe.FindStringSubmatch(l); m != nil {
			svc = m[1]
		} else if regexp.MustCompile(`^\S`).MatchString(l) {
			svc = ""
		}
		out = append(out, l)
		if svc == "" {
			continue
		}
		isPHP := strings.HasPrefix(svc, "php-fpm-")
		if !isPHP && !webservers[svc] {
			continue
		}
		// php services without an environment: block get one right before volumes:
		if isPHP && l == key+"volumes:" && !blockHas(svcStart[svc], envKey.MatchString) {
			out = append(out[:len(out)-1], key+"environment:", item+env, l)
			changed = true
		}
		if strings.TrimRight(l, " \t\r\n") == key+"volumes:" && !blockHas(svcStart[svc], func(s string) bool { return strings.Contains(s, mount) }) {
			out = append(out, item+mount)
			changed = true
		}
		if isPHP && strings.TrimRight(l, " \t\r\n") == key+"environment:" && !blockHas(svcStart[svc], func(s string) bool { return strings.Contains(s, "PHPFPM_LISTEN=") }) {
			out = append(out, item+env)
			changed = true
		}
	}
	if !changed {
		return "unchanged", nil
	}
	return "patched", os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644)
}
