// ##############################################################################
// Script Name: internal/commands/domains/ssl.go
// Description: Check SSL for domain, add custom certificate, view files.
// Usage: opencli domains-ssl <DOMAIN_NAME> [status|info|logs|auto|selfsigned|custom] [path/to/fullchain.pem path/to/key.pem] | opencli domains-ssl --notify
// Author: Stefan Pejcic
// Created: 22.03.2025
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

package domains

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/mail"
	"opencli/internal/ojson"
	"opencli/internal/sh"
	"opencli/internal/tenantfs"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-ssl",
		Desc:  "Check SSL for domain, add custom certificate, view files.",
		Usage: "opencli domains-ssl <DOMAIN_NAME> [status|info|logs|auto|selfsigned|custom] [path/to/fullchain.pem path/to/key.pem] | opencli domains-ssl --notify",
		Run:   ssl,
	})
}

const (
	sslGreen  = "\033[0;32m"
	sslYellow = "\033[0;33m"
	sslRed    = "\033[0;31m"
	sslReset  = "\033[0m"
)

var (
	sslCustomRe  = regexp.MustCompile(`tls\s+/.*?/fullchain\.pem\s+/.*?/key\.pem`)
	sslTlsPath   = regexp.MustCompile(`^[ \t]*tls[ \t]+/[^ \t]*fullchain\.pem[ \t]+/[^ \t]*key\.pem[ \t]*$`)
	sslTlsOpen   = regexp.MustCompile(`^[ \t]*tls[ \t]*\{[ \t]*$`)
	sslTlsClose  = regexp.MustCompile(`^[ \t]*\}[ \t]*$`)
	sslLoggerRe  = regexp.MustCompile(`tls|acme`)
	sslMsgRe     = regexp.MustCompile(`(?i)certificate|renew|obtain|challenge`)
	sslIPRe      = regexp.MustCompile(`^[0-9.]+$`)
	sslIPInJSON  = regexp.MustCompile(`[0-9]+(\.[0-9]+){3}`)
	sslEndDateFm = "Jan _2 15:04:05 2006 MST"
)

func sslUsage() {
	fmt.Println("Usage:")
	fmt.Println("  opencli domains-ssl <DOMAIN>                 - Display command examples for this domain.")
	fmt.Printf("  opencli domains-ssl <DOMAIN> %sstatus%s          - Display current status for the domain.\n", sslGreen, sslReset)
	fmt.Printf("  opencli domains-ssl <DOMAIN> %sinfo%s            - Display certificate files.\n", sslGreen, sslReset)
	fmt.Printf("  opencli domains-ssl <DOMAIN> %slogs%s [%s1000%s|%s-f%s]  - View caddy SSL-related logs for the domain.\n", sslGreen, sslReset, sslYellow, sslReset, sslYellow, sslReset)
	fmt.Printf("  opencli domains-ssl <DOMAIN> %scustom%s %s<cert_path>%s %s<key_path>%s - Switch to custom SSL for the domain.\n", sslGreen, sslReset, sslYellow, sslReset, sslYellow, sslReset)
	fmt.Printf("  opencli domains-ssl <DOMAIN> %sauto%s            - Switch back to AutoSSL for the domain.\n", sslGreen, sslReset)
	fmt.Printf("  opencli domains-ssl <DOMAIN> %sselfsigned%s      - AutoSSL with a self-signed fallback until the domain points to this server.\n", sslGreen, sslReset)
	fmt.Printf("  opencli domains-ssl %s--notify%s                - Email users about SSL certificates that expire soon or fail to renew.\n", sslGreen, sslReset)
}

func sslShowExamples(domain string) {
	fmt.Printf("Usage examples for domain %s%s%s:\n", sslYellow, domain, sslReset)
	fmt.Println("")
	fmt.Println("- Check current SSL status for domain (AutoSSL, CustomSSL or No SSL):")
	fmt.Printf("  opencli domains-ssl %s%s%s %sstatus%s\n", sslYellow, domain, sslReset, sslGreen, sslReset)
	fmt.Println("- Display fullchain and key files for the domain:")
	fmt.Printf("  opencli domains-ssl %s%s%s %sinfo%s\n", sslYellow, domain, sslReset, sslGreen, sslReset)
	fmt.Println("- Set AutoSSL for the domain (default):")
	fmt.Printf("  opencli domains-ssl %s%s%s %sauto%s\n", sslYellow, domain, sslReset, sslGreen, sslReset)
	fmt.Println("- Use a self-signed certificate until the domain points to this server, then Let's Encrypt:")
	fmt.Printf("  opencli domains-ssl %s%s%s %sselfsigned%s\n", sslYellow, domain, sslReset, sslGreen, sslReset)
	fmt.Println("- Add custom certificate for the domain:")
	fmt.Printf("  opencli domains-ssl %s%s%s %scustom%s %s/var/www/html/fullchain.pem /var/www/html/key.pem%s\n", sslYellow, domain, sslReset, sslGreen, sslReset, sslRed, sslReset)
	fmt.Println("- View SSL-related lines for the domain from Caddy logs:")
	fmt.Printf("  opencli domains-ssl %s%s%s %slogs%s\n", sslYellow, domain, sslReset, sslGreen, sslReset)
}

func ssl(args []string) int {
	if cli.Arg(args, 0) == "--notify" {
		sslNotify()
		return 0
	}
	if cli.Arg(args, 0) == "" {
		fmt.Println("ERROR: Domain name is required!")
		sslUsage()
		return 1
	}
	domain := args[0]
	configFile := "/etc/openpanel/caddy/domains/" + domain + ".conf"
	if !conf.IsFile(configFile) {
		fmt.Printf("%sDomain %s does not exist.%s\n", sslRed, domain, sslReset)
		return 1
	}
	s := &sslJob{domain: domain, configFile: configFile,
		hostfsTLSDir: "/etc/openpanel/caddy/ssl/custom/" + domain,
		tlsDir:       "/data/caddy/certificates/custom/" + domain}

	action := cli.Arg(args, 1)
	if action == "" {
		sslShowExamples(domain)
		return 0
	}
	s.getUser()
	s.getUserContext()

	switch {
	case action == "info":
		s.catCertificateFiles()
		return 0
	case action == "status":
		fmt.Println(s.status())
		return 0
	case action == "auto":
		// replace custom ssl paths or the self-signed fallback with plain on_demand
		if !s.setTLSBlock("on_demand") {
			return 1
		}
		sh.C("podman", "exec", "caddy", "sh", "-c", "caddy validate && caddy reload").Quiet().Background()
		sh.Opencli("sentinel", "--action=domains_ssl", "--title=AutoSSL set for domain", "--message=AutoSSL is set for domain name: '"+domain+"' owned by OpenPanel user '"+s.user+"'.").Quiet().Background()
		fmt.Printf("Updated %s to use AutoSSL.\n", domain)
		return 0
	case action == "selfsigned":
		// Let's Encrypt first, Caddy's local CA when that fails, every renewal tries Let's Encrypt again
		if !s.setTLSBlock("on_demand\nissuer acme\nissuer internal") {
			return 1
		}
		sh.C("podman", "exec", "caddy", "sh", "-c", "caddy validate --config /etc/caddy/Caddyfile && caddy reload --config /etc/caddy/Caddyfile").Quiet().Run()
		s.triggerCertificate()
		sh.Opencli("sentinel", "--action=domains_ssl", "--title=Self-signed SSL fallback set for domain", "--message=AutoSSL with a self-signed fallback is set for domain name: '"+domain+"' owned by OpenPanel user '"+s.user+"'.").Quiet().Background()
		fmt.Printf("Updated %s to use a self-signed certificate until Let's Encrypt can issue one.\n", domain)
		return 0
	case action == "custom" && cli.Arg(args, 2) != "" && cli.Arg(args, 3) != "":
		return s.useTLS(args[2], args[3])
	case action == "logs":
		sslShowLogs(domain, cli.Arg(args, 2))
		return 0
	}
	fmt.Println("ERROR: Invalid arguments provided for domain!")
	sslUsage()
	return 1
}

type sslJob struct {
	domain, configFile, hostfsTLSDir, tlsDir string
	user, userID, context                    string
}

func (s *sslJob) getUser() {
	out, _ := sh.Opencli("domains-whoowns", s.domain).Out()
	s.user = awkSecondField(out, "Owner of '"+s.domain+"': ")
	if s.user == "" {
		fmt.Fprintf(os.Stderr, "Failed to determine the owner of the domain '%s'.\n", s.domain)
		cli.Exit(1)
	}
}

func (s *sslJob) getUserContext() {
	info, _ := db.Plain("SELECT id, server FROM users WHERE username = '"+s.user+"';", "-s").Out()
	s.userID = awkField(info, 1)
	s.context = awkField(info, 2)
	if s.userID == "" {
		fmt.Printf("FATAL ERROR: user %s does not exist.\n", s.user)
		cli.Exit(1)
	}
}

func (s *sslJob) confHas(sub string) bool { return strings.Contains(conf.Read(s.configFile), sub) }

func (s *sslJob) status() string {
	switch {
	case s.confHas("fullchain.pem"):
		return "Custom SSL"
	case s.confHas("issuer internal"):
		return "AutoSSL with self-signed fallback"
	case s.confHas("on_demand"):
		return "AutoSSL"
	}
	return "Unknown"
}

func sslCat(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cat: %s: No such file or directory\n", path)
		return
	}
	os.Stdout.Write(b)
}

func (s *sslJob) catCertificateFiles() {
	if s.confHas("fullchain.pem") && s.confHas("key.pem") {
		sslCat(s.hostfsTLSDir + "/fullchain.pem")
		sslCat(s.hostfsTLSDir + "/key.pem")
		return
	}
	cert := "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/" + s.domain + "/" + s.domain + ".crt"
	key := "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/" + s.domain + "/" + s.domain + ".key"
	// no Let's Encrypt certificate yet, show the self-signed one from Caddy's local CA if there is one
	if !conf.IsFile(cert) {
		cert = "/etc/openpanel/caddy/ssl/local/" + s.domain + "/" + s.domain + ".crt"
		key = "/etc/openpanel/caddy/ssl/local/" + s.domain + "/" + s.domain + ".key"
	}
	if conf.IsFile(cert) {
		sslCat(cert)
	}
	if conf.IsFile(key) {
		sslCat(key)
	}
}

// setTLSBlock replaces the domain's tls block (custom paths, plain on_demand or with fallback issuers) with the given lines, keeping its indentation
func (s *sslJob) setTLSBlock(body string) bool {
	raw, err := os.ReadFile(s.configFile)
	if err != nil {
		fmt.Println("No tls block found in " + s.configFile)
		return false
	}
	text := strings.TrimSuffix(string(raw), "\n")
	var lines []string
	if len(raw) > 0 {
		lines = strings.Split(text, "\n")
	}
	var b strings.Builder
	block := func(line string) {
		ind := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		b.WriteString(ind + "tls {\n")
		for _, r := range strings.Split(body, "\n") {
			b.WriteString(ind + "  " + r + "\n")
		}
		b.WriteString(ind + "}\n")
	}
	done, skip := false, false
	for _, l := range lines {
		// files made from a template saved through a browser have CRLF line endings
		l = strings.TrimSuffix(l, "\r")
		switch {
		case !done && sslTlsPath.MatchString(l):
			block(l)
			done = true
		case !done && sslTlsOpen.MatchString(l):
			block(l)
			skip = true
		case skip:
			if sslTlsClose.MatchString(l) {
				skip = false
				done = true
			}
		default:
			b.WriteString(l + "\n")
		}
	}
	if !done {
		fmt.Println("No tls block found in " + s.configFile)
		return false
	}
	conf.WriteInPlace(s.configFile, b.String())
	return true
}

// makes Caddy request the certificate now, so the first visitor doesn't wait on it, through the host since DNS may not point here yet
func (s *sslJob) triggerCertificate() bool {
	hostIP := ""
	if addrs, err := net.LookupIP("host.containers.internal"); err == nil && len(addrs) > 0 {
		hostIP = addrs[0].String()
	}
	if hostIP == "" {
		hostIP = "127.0.0.1"
	}
	for i := 0; i < 3; i++ {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: s.domain},
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(hostIP, "443"))
			},
		}
		c := &http.Client{Timeout: 20 * time.Second, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := c.Get("https://" + s.domain + "/")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

func (s *sslJob) useTLS(fullCert, fullKey string) int {
	const prefix = "/var/www/html/"
	// 1. validate and format paths
	if !strings.HasPrefix(fullCert, prefix) || !strings.HasPrefix(fullKey, prefix) || strings.Contains(fullCert, "..") || strings.Contains(fullKey, "..") {
		fmt.Println("ERROR: Paths must be inside /var/www/html/ directory.")
		return 1
	}
	certPath := strings.TrimPrefix(fullCert, prefix)
	keyPath := strings.TrimPrefix(fullKey, prefix)
	base := "/home/" + s.context + "/docker-data/volumes/" + s.context + "_html_data/_data/"
	realCert, realKey := base+certPath, base+keyPath

	// 2. copy the files out of the user's volume without following symlinks, then check the copies so nothing can change between check and use
	htmlRoot := tenantfs.Volume(s.context, "html_data")
	os.MkdirAll(s.hostfsTLSDir, 0o755)
	newCert, newKey := s.hostfsTLSDir+"/fullchain.pem.new", s.hostfsTLSDir+"/key.pem.new"
	certData, err1 := tenantfs.Read(htmlRoot, realCert)
	os.WriteFile(newCert, certData, 0o644)
	var keyData []byte
	var err2 error
	if err1 == nil {
		keyData, err2 = tenantfs.Read(htmlRoot, realKey)
		os.WriteFile(newKey, keyData, 0o644)
	}
	if err1 != nil || err2 != nil {
		os.Remove(newCert)
		os.Remove(newKey)
		fmt.Printf("Error: %s and %s must be regular files inside /var/www/html/\n", certPath, keyPath)
		return 1
	}

	// 3. check if cert is valid and the key is really a key
	if !sh.C("openssl", "x509", "-noout", "-checkend", "0", "-in", newCert).Quiet().OK() {
		os.Remove(newCert)
		os.Remove(newKey)
		fmt.Printf("Error: %s is not valid or has expired!\n", certPath)
		return 1
	}
	if !sh.C("openssl", "pkey", "-noout", "-in", newKey).Quiet().OK() {
		os.Remove(newCert)
		os.Remove(newKey)
		fmt.Printf("Error: %s is not a valid private key!\n", keyPath)
		return 1
	}
	os.Rename(newCert, s.hostfsTLSDir+"/fullchain.pem")
	os.Rename(newKey, s.hostfsTLSDir+"/key.pem")

	// 4. update caddyfile
	already := false
	for _, l := range conf.Lines(s.configFile) {
		if sslCustomRe.MatchString(l) {
			already = true
			break
		}
	}
	if already {
		fmt.Printf("Custom SSL already configured for %s. Updating certificate and key..\n", s.domain)
	} else {
		fmt.Println("Adding custom certificate..")
		s.sedTLSChange("tls " + s.tlsDir + "/fullchain.pem " + s.tlsDir + "/key.pem")
	}

	// 5. reload caddy
	sh.C("podman", "exec", "caddy", "sh", "-c", "caddy validate && caddy reload").Quiet().Background()

	// 6. notify
	sh.Opencli("sentinel", "--action=domains_ssl", "--title=Custom SSL set for domain", "--message=Custom SSL is set for domain name: '"+s.domain+"' owned by OpenPanel user '"+s.user+"'.").Quiet().Background()

	fmt.Printf("Updated %s to use custom SSL.\n", s.domain)
	return 0
}

// sedTLSChange is sed -i '/tls {/,/}/c\TEXT', the text is printed once the range closes
func (s *sslJob) sedTLSChange(text string) {
	conf.SedFile(s.configFile, func(lines []string) []string {
		var out []string
		in := false
		for _, l := range lines {
			if in {
				if strings.Contains(l, "}") {
					in = false
					out = append(out, text)
				}
				continue
			}
			if strings.Contains(l, "tls {") {
				in = true
				continue
			}
			out = append(out, l)
		}
		return out
	})
}

// lineFilter turns a byte stream into lines for a callback
type lineFilter struct {
	mu   sync.Mutex
	buf  []byte
	line func(string)
}

func (w *lineFilter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.line(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func (w *lineFilter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.line(string(w.buf))
		w.buf = nil
	}
}

func sslShowLogs(domain, third string) {
	lines := "1000"
	follow := false
	if rlIsLimit(third) {
		lines = third
	} else if third == "-f" {
		follow = true
	}
	fmt.Println("Showing SSL-related log lines for " + domain)
	fmt.Println("-------------------------------------------------------")
	// straight from podman, the container's LogPath is gone on newer podman and is empty with the journald driver
	w := &lineFilter{line: func(l string) {
		o := sslParseObj(l)
		if o == nil {
			return
		}
		n := sslLogMatches(o, domain)
		for i := 0; i < n; i++ {
			fmt.Println(ojson.Compact(o))
		}
	}}
	var c *sh.Cmd
	if follow {
		c = sh.C("podman", "logs", "-f", "--tail", lines, "caddy")
	} else {
		c = sh.C("podman", "logs", "--tail", lines, "caddy").Timeout(120 * time.Second)
	}
	c.Stdout(w).ErrToOut().Run()
	w.flush()
}

func sslParseObj(l string) *ojson.Object {
	v, err := ojson.Parse(l)
	if err != nil {
		return nil
	}
	o, _ := v.(*ojson.Object)
	return o
}

// sslPath walks keys like jq's .a.b? does, ok is false when jq would raise (suppressed by the ?)
func sslPath(o *ojson.Object, keys ...string) (any, bool) {
	var v any = o
	for _, k := range keys {
		switch t := v.(type) {
		case *ojson.Object:
			v, _ = t.Get(k)
		case nil:
			v = nil
		default:
			return nil, false
		}
	}
	return v, true
}

func sslTruthy(v any) bool {
	if v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return true
}

func sslEq(v any, s string) bool {
	str, ok := v.(string)
	return ok && str == s
}

// sslLogMatches is how many times the jq select in `domains-ssl logs` emits the object, a generator in the or chain can emit it more than once
func sslLogMatches(o *ojson.Object, domain string) int {
	single := func(keys ...string) func() []bool {
		return func() []bool {
			v, ok := sslPath(o, keys...)
			if !ok {
				return nil
			}
			return []bool{sslEq(v, domain)}
		}
	}
	idents := func() []bool {
		v, ok := sslPath(o, "identifiers")
		if !ok {
			return nil
		}
		var res []bool
		switch t := v.(type) {
		case []any:
			for _, e := range t {
				res = append(res, sslEq(e, domain))
			}
		case *ojson.Object:
			for _, k := range t.Keys() {
				e, _ := t.Get(k)
				res = append(res, sslEq(e, domain))
			}
		}
		return res
	}
	errStarts := func() []bool {
		v, _ := sslPath(o, "error")
		s := ""
		if sslTruthy(v) {
			str, ok := v.(string)
			if !ok {
				return nil
			}
			s = str
		}
		return []bool{strings.HasPrefix(s, "["+domain+"]") || strings.HasPrefix(s, "[www."+domain+"]")}
	}
	www := func() []bool {
		v, ok := sslPath(o, "identifier")
		if !ok {
			return nil
		}
		return []bool{sslEq(v, "www."+domain)}
	}
	or := func(l func() []bool, r func() []bool) func() []bool {
		return func() []bool {
			var res []bool
			for _, x := range l() {
				if x {
					res = append(res, true)
				} else {
					res = append(res, r()...)
				}
			}
			return res
		}
	}
	chain := or(or(or(or(or(or(single("request", "host"), single("request", "tls", "server_name")), single("server_name")), single("identifier")), www), idents), errStarts)
	n := 0
	for _, x := range chain() {
		if x {
			n++
		}
	}
	if n == 0 || !sslLogOK(o) {
		return 0
	}
	return n
}

func sslLogOK(o *ojson.Object) bool {
	lg, _ := o.Get("logger")
	ls := ""
	if sslTruthy(lg) {
		str, ok := lg.(string)
		if !ok {
			return false
		}
		ls = str
	}
	if sslLoggerRe.MatchString(ls) {
		return true
	}
	m, _ := o.Get("msg")
	ms := ""
	if sslTruthy(m) {
		str, ok := m.(string)
		if !ok {
			return false
		}
		ms = str
	}
	return sslMsgRe.MatchString(ms)
}

// tsv is jq's @tsv escaping for one field
func sslTSV(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return strings.ReplaceAll(s, "\r", `\r`)
}

func sslFirst(o *ojson.Object, keys ...string) (string, bool) {
	for _, k := range keys {
		v, _ := o.Get(k)
		if sslTruthy(v) {
			switch t := v.(type) {
			case string:
				return t, true
			case bool:
				return "true", true
			default:
				if _, isObj := v.(*ojson.Object); isObj {
					return "", false
				}
				if _, isArr := v.([]any); isArr {
					return "", false
				}
				return ojson.Text(v), true
			}
		}
	}
	return "", true
}

func sslTLSErrors() string {
	out, _ := sh.C("podman", "logs", "--since", "24h", "caddy").Timeout(60 * time.Second).Combined()
	var res []string
	for _, l := range strings.Split(out, "\n") {
		o := sslParseObj(l)
		if o == nil {
			continue
		}
		lv, _ := o.Get("level")
		if !sslEq(lv, "error") {
			continue
		}
		lg, _ := o.Get("logger")
		ls := ""
		if sslTruthy(lg) {
			str, ok := lg.(string)
			if !ok {
				continue
			}
			ls = str
		}
		if !sslLoggerRe.MatchString(ls) {
			continue
		}
		id, ok1 := sslFirst(o, "identifier", "server_name")
		er, ok2 := sslFirst(o, "error", "msg")
		if !ok1 || !ok2 {
			continue
		}
		res = append(res, sslTSV(id)+"\t"+sslTSV(er))
	}
	return strings.TrimRight(strings.Join(res, "\n"), "\n")
}

// sslNotify emails users about AutoSSL certificates with 7 days or less left (renewal is failing, Caddy renews ~30 days before) and any certificate with 1 day or less left, once per certificate and alert
func sslNotify() {
	const acmeDir = "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory"
	now := time.Now().Unix()

	// only domains that point to this server, a parked or externally hosted domain never gets AutoSSL here
	body, _ := curlGet("https://ip.openpanel.com", 3*time.Second)
	serverIPs := strings.TrimRight(body, "\n") + " " + sh.C("hostname", "-I").NoErr().Str() + " "
	files, _ := filepath.Glob("/etc/openpanel/openpanel/core/users/*/ip.json")
	var ipJSON []string
	for _, f := range files {
		for _, l := range conf.Lines(f) {
			ipJSON = append(ipJSON, sslIPInJSON.FindAllString(l, -1)...)
		}
	}
	serverIPs += strings.Join(ipJSON, "\n")
	ipSet := map[string]bool{}
	for _, l := range strings.Split(strings.ReplaceAll(serverIPs, " ", "\n"), "\n") {
		if l != "" {
			ipSet[l] = true
		}
	}

	// latest TLS error per domain from the last day, so the email can say why renewal fails
	tlsErrors := sslTLSErrors()

	rowsOut, _ := db.Cmd("SELECT u.username, d.domain_url FROM domains d JOIN users u ON u.id = d.user_id WHERE u.username NOT LIKE 'SUSPENDED%' ORDER BY u.username", "-N", "-B").NoErr().Out()

	body2 := map[string]string{}
	tipsAuto := map[string]bool{}
	tipsCustom := map[string]bool{}
	pending := map[string]string{}
	for _, row := range strings.Split(rowsOut, "\n") {
		f := readTabs(row, 2)
		username, domain := f[0], f[1]
		if domain == "" {
			continue
		}
		cfile := "/etc/openpanel/caddy/domains/" + domain + ".conf"
		if !conf.IsFile(cfile) {
			continue
		}
		content := conf.Read(cfile)
		var typ, cert string
		if strings.Contains(content, "fullchain.pem") {
			typ, cert = "custom", "/etc/openpanel/caddy/ssl/custom/"+domain+"/fullchain.pem"
		} else if strings.Contains(content, "on_demand") {
			typ, cert = "auto", acmeDir+"/"+domain+"/"+domain+".crt"
		} else {
			continue
		}
		if !conf.IsFile(cert) {
			continue
		}

		endDate := ""
		if o, err := sh.C("openssl", "x509", "-enddate", "-noout", "-in", cert).NoErr().Out(); err == nil || o != "" {
			if i := strings.Index(o, "="); i >= 0 {
				endDate = o[i+1:]
			}
		}
		var endSecs int64
		if endDate == "" {
			// date -d "" is midnight today
			t := time.Now()
			endSecs = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local).Unix()
		} else if t, err := time.Parse(sslEndDateFm, strings.Join(strings.Fields(endDate), " ")); err == nil {
			endSecs = t.Unix()
		} else {
			continue
		}
		days := (endSecs - now) / 86400

		level := 0
		if days <= 1 {
			level = 1
		} else if typ == "auto" && days <= 7 {
			level = 7
		}
		if level == 0 {
			continue
		}

		matched := false
		for _, l := range strings.Split(sh.C("dig", "+short", "A", domain).NoErr().Str(), "\n") {
			if sslIPRe.MatchString(l) && ipSet[l] {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}

		// same certificate and same or worse alert already sent
		state := "/etc/openpanel/openpanel/core/users/" + username + "/.ssl_notified"
		sent := ""
		for _, l := range conf.Lines(state) {
			p := strings.Fields(l)
			if len(p) >= 2 && p[0] == domain && p[1] == strconv.FormatInt(endSecs, 10) {
				if len(p) > 2 {
					sent = p[2]
				}
				break
			}
		}
		if sent != "" {
			if n, err := strconv.Atoi(sent); err == nil && n <= level {
				continue
			}
		}

		label := "custom"
		if typ == "auto" {
			label = "AutoSSL"
		}
		when := time.Unix(endSecs, 0).UTC().Format("2006-01-02 15:04") + " UTC"
		var line string
		if endSecs < now {
			line = fmt.Sprintf("- %s: the %s certificate expired on %s.", domain, label, when)
		} else {
			left := fmt.Sprintf("%d day(s)", days)
			if days < 1 {
				left = "less than a day"
			}
			line = fmt.Sprintf("- %s: the %s certificate expires on %s (%s left).", domain, label, when, left)
		}
		if typ == "auto" {
			line += " It should have been renewed automatically, so renewal is failing."
			reason := ""
			for _, l := range strings.Split(tlsErrors, "\n") {
				if p := strings.Split(l, "\t"); p[0] == domain {
					reason = ""
					if len(p) > 1 {
						reason = p[1]
					}
				}
			}
			if r := []rune(reason); len(r) > 300 {
				reason = string(r[:300])
			}
			if reason != "" {
				line += " Last error: " + reason
			}
			tipsAuto[username] = true
		} else {
			tipsCustom[username] = true
		}
		body2[username] += line + "\n"
		// written to the state file only once the email is sent, so a failed send is retried next run
		pending[username] += fmt.Sprintf("%s %d %d\n", domain, endSecs, level)
	}

	var users []string
	for u := range body2 {
		users = append(users, u)
	}
	sort.Strings(users)
	for _, username := range users {
		tips := ""
		if tipsAuto[username] {
			tips += "For AutoSSL, make sure the domain's A and AAAA records point to this server, no CAA record blocks Let's Encrypt, and no proxy or firewall blocks /.well-known/acme-challenge/. "
		}
		if tipsCustom[username] {
			tips += "For a custom certificate, upload a renewed one on the Domains > SSL Certificates page in OpenPanel, or switch the domain to AutoSSL to get free certificates that renew on their own."
		}
		count := len(nonBlank(body2[username]))
		msg := "These SSL certificates on account " + username + " need attention, without a valid certificate visitors see a security warning instead of the website:\n\n" + body2[username] + "\nChecked: " + time.Now().Format("2006-01-02 15:04:05 MST")
		if mail.SendUser(username, "SSL certificate problem on account "+username, msg, "notify_ssl_expiry", "", "", "", tips) {
			state := "/etc/openpanel/openpanel/core/users/" + username + "/.ssl_notified"
			// certificates that expired over 30 days ago are dropped, a replaced one never matches again
			lvl := map[string]int{}
			var keys []string
			add := func(l string) {
				p := strings.Fields(l)
				if len(p) < 3 {
					return
				}
				n, _ := strconv.Atoi(p[2])
				k := p[0] + " " + p[1]
				if cur, ok := lvl[k]; !ok {
					keys = append(keys, k)
					lvl[k] = n
				} else if n < cur {
					lvl[k] = n
				}
			}
			for _, l := range conf.Lines(state) {
				p := strings.Fields(l)
				if len(p) == 3 {
					if e, err := strconv.ParseFloat(p[1], 64); err == nil && e > float64(now-2592000) {
						add(l)
					}
				}
			}
			for _, l := range strings.Split(pending[username], "\n") {
				add(l)
			}
			sort.Strings(keys)
			var sb strings.Builder
			for _, k := range keys {
				fmt.Fprintf(&sb, "%s %d\n", k, lvl[k])
			}
			tmp := state + ".tmp"
			if os.WriteFile(tmp, []byte(sb.String()), 0o644) == nil {
				os.Rename(tmp, state)
			}
			fmt.Printf("%s: notified about %d certificate(s).\n", username, count)
		} else {
			fmt.Printf("%s: %d certificate(s) need attention, no email sent (notifications off or no email address).\n", username, count)
		}
	}
	if len(body2) == 0 {
		fmt.Println("No SSL certificates need attention.")
	}
}
