// ##############################################################################
// Script Name: internal/commands/domaincmd/domain.go
// Description: View and set domain/ip for accessing panels.
// Usage: opencli domain [set <domain_name> | ip] [--debug] [--no-restart]
// Author: Stefan Pejcic
// Created: 09.02.2025
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

package domaincmd

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

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/redis"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domain",
		Desc:  "View and set domain/ip for accessing panels.",
		Usage: "opencli domain [set <domain_name> | ip] [--debug] [--no-restart]",
		Run:   run,
	})
	cli.Register(&cli.Command{
		Name:   "domain-bg",
		Desc:   "Internal helper for domain.",
		Usage:  "opencli domain-bg <cert-wait|mail-restart> ARGS...",
		Hidden: true,
		Run:    bgRun,
	})
}

const (
	caddyFile         = "/etc/openpanel/caddy/Caddyfile"
	domainsDir        = "/etc/openpanel/caddy/domains/"
	mailserverEnv     = "/usr/local/mail/openmail/mailserver.env"
	mailserverCompose = "/usr/local/mail/openmail/compose.yml"
	redirectsConf     = "/etc/openpanel/caddy/redirects.conf"
	defaultDomain     = "example.net"
)

var (
	ipv4Re      = regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}$`)
	ten         = regexp.MustCompile(`^10\.`)
	one72       = regexp.MustCompile(`^172\.(1[6-9]|2[0-9]|3[0-1])\.`)
	one92       = regexp.MustCompile(`^192\.168\.`)
	domainRe    = regexp.MustCompile(`^([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$`)
	ipv4StrictR = regexp.MustCompile(`^((25[0-5]|2[0-4][0-9]|1?[0-9]{1,2})\.){3}(25[0-5]|2[0-4][0-9]|1?[0-9]{1,2})$`)
	domainLine  = regexp.MustCompile(`^([A-Za-z0-9.-]*)[[:space:]]*\{`)
	ipLine      = regexp.MustCompile(`^([0-9]{1,3}(\.[0-9]{1,3}){3})[[:space:]]*\{`)
)

// state shared by the steps, same as the globals in the bash script
type state struct {
	debug         bool
	currentDomain string
	newHostname   string
	serverIP      string
}

func (s *state) logDebug(msg string) {
	if s.debug {
		fmt.Println(msg)
	}
}

func logError(msg string) { fmt.Fprintln(os.Stderr, "Error: "+msg) }

func isValidIPv4(ip string) bool {
	return ipv4Re.MatchString(ip) && !ten.MatchString(ip) && !one72.MatchString(ip) && !one92.MatchString(ip)
}

// getServerIPv4 prints the public IPv4, ok is false when none was found
func getServerIPv4() (string, bool) {
	body, ok := curlGet("https://ip.openpanel.com", time.Second)
	if !ok {
		body, _ = curlGet("https://ifconfig.me/ip", time.Second)
	}
	ip := strings.TrimRight(body, "\n")
	if isValidIPv4(ip) {
		return ip, true
	}
	body, _ = curlGet("https://ifconfig.me", time.Second)
	ip = strings.TrimRight(body, "\n")
	if isValidIPv4(ip) {
		return ip, true
	}
	if sh.Has("ip") {
		for _, l := range strings.Split(sh.C("ip", "addr").NoErr().Str(), "\n") {
			if strings.Contains(l, "inet ") && strings.Contains(l, "global") {
				if f := strings.Fields(l); len(f) > 1 {
					ip, _, _ = strings.Cut(f[1], "/")
				}
				break
			}
		}
	} else if f := strings.Fields(sh.C("hostname", "-I").NoErr().Str()); len(f) > 0 {
		ip = f[0]
	}
	if isValidIPv4(ip) {
		return ip, true
	}
	logError("Invalid or private IPv4 address. Please contact support.")
	return "", false
}

// curlGet is `curl --silent --max-time N -4 url`, any http answer counts
func curlGet(url string, timeout time.Duration) (string, bool) {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
		},
	}
	c := &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err == nil
}

func isValidDomain(in string) bool {
	return domainRe.MatchString(in) || ipv4StrictR.MatchString(in)
}

// sedRange is the first line group of `sed -n '/start/,/end/p'`
func sedRange(lines []string, start, end string) []string {
	var res []string
	in := false
	for _, l := range lines {
		if !in {
			if strings.Contains(l, start) {
				in = true
				res = append(res, l)
			}
			continue
		}
		res = append(res, l)
		if strings.Contains(l, end) {
			in = false
		}
	}
	return res
}

func firstGroup(lines []string, re *regexp.Regexp) string {
	for _, l := range lines {
		if m := re.FindStringSubmatch(l); m != nil {
			return m[1]
		}
	}
	return ""
}

// getCurrentDomain reads the current domain from the Caddyfile
func getCurrentDomain() (string, bool) {
	if !conf.IsFile(caddyFile) {
		logError("Caddyfile not found at " + caddyFile)
		return "", false
	}
	lines := conf.Lines(caddyFile)
	domain := firstGroup(sedRange(lines, "# START USERPANEL DOMAIN #", "# END USERPANEL DOMAIN #"), domainLine)
	if domain == "" {
		domain = firstGroup(sedRange(lines, "# START HOSTNAME DOMAIN #", "# END HOSTNAME DOMAIN #"), domainLine)
	}
	if domain == "" {
		domain = firstGroup(sedRange(lines, "# START HOSTNAME IP #", "# END HOSTNAME IP #"), ipLine)
	}
	return domain, true
}

func (s *state) updateCaddyfile() bool {
	s.logDebug("Updating Caddyfile with new domain/IP: " + s.newHostname)
	if !conf.IsFile(caddyFile) {
		logError("Caddyfile not found at " + caddyFile)
		return false
	}
	ok := false
	if ipv4Re.MatchString(s.newHostname) {
		if strings.Contains(conf.Read(caddyFile), "# START HOSTNAME IP #") {
			podman.SedInPlace(caddyFile, `/# START HOSTNAME IP #/,/# END HOSTNAME IP #/ s/[0-9]\{1,3\}\.[0-9]\{1,3\}\.[0-9]\{1,3\}\.[0-9]\{1,3\}/`+s.newHostname+`/`)
		} else {
			adminPort, _ := sh.Opencli("admin", "port").Out()
			ipBlock := `# START HOSTNAME IP #\n` + s.newHostname + ` {\n  tls {\n    reuse_private_keys\n    issuer acme {\n      profile shortlived\n    }\n  }\n  reverse_proxy localhost:` + adminPort + `\n}\n# END HOSTNAME IP #\n`
			podman.SedInPlace(caddyFile, "s|# START HOSTNAME DOMAIN #|"+ipBlock+"# START HOSTNAME DOMAIN #|")
		}
		ok = podman.SedInPlace(caddyFile, "s/"+s.currentDomain+"/"+defaultDomain+"/g")
	} else {
		ok = podman.SedInPlace(caddyFile, "s/"+s.currentDomain+"/"+s.newHostname+"/g")
	}
	return ok
}

// createMvFile creates or moves the domain configuration file
func (s *state) createMvFile() bool {
	s.logDebug("Managing domain configuration file")
	os.MkdirAll(domainsDir, 0o755)
	if s.newHostname == defaultDomain {
		// nothing to do when already on IP
		os.Remove(domainsDir + s.currentDomain + ".conf")
		return true
	}
	if conf.IsFile(domainsDir + s.currentDomain + ".conf") {
		os.Rename(domainsDir+s.currentDomain+".conf", domainsDir+s.newHostname+".conf")
	}
	f := domainsDir + s.newHostname + ".conf"
	if conf.Exists(f) {
		now := time.Now()
		return os.Chtimes(f, now, now) == nil
	}
	fh, err := os.Create(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "touch: cannot touch '%s': No such file or directory\n", f)
		return false
	}
	fh.Close()
	return true
}

// the hostname used to be pinned to 169.254.1.2 in the host's own /etc/hosts, which the host itself can't reach, so the line goes away
func updateContainerHostsEntry(domain string) {
	if domain == "" {
		return
	}
	re := regexp.MustCompile(`^169\.254\.1\.2[[:space:]]+(.*[[:space:]])?` + regexp.QuoteMeta(domain) + `([[:space:]]|$)`)
	conf.SedFile("/etc/hosts", func(lines []string) []string {
		var out []string
		for _, l := range lines {
			if !re.MatchString(l) {
				out = append(out, l)
			}
		}
		return out
	})
	sh.Opencli("docker-hosts").Quiet().Run()
}

// subLines is sed -i "s|re|repl|" per line, first match only
func subLines(file string, re *regexp.Regexp, repl string) {
	conf.SedFile(file, func(lines []string) []string {
		for i, l := range lines {
			if loc := re.FindStringIndex(l); loc != nil {
				lines[i] = l[:loc[0]] + repl + l[loc[1]:]
			}
		}
		return lines
	})
}

func subKey(file, key, val string) {
	subLines(file, regexp.MustCompile(regexp.QuoteMeta(key)+`=.*`), key+"="+val)
}

// changeLine is sed -i '/^KEY=/c\TEXT'
func changeLine(file, prefix, text string) {
	conf.SedFile(file, func(lines []string) []string {
		for i, l := range lines {
			if strings.HasPrefix(l, prefix) {
				lines[i] = text
			}
		}
		return lines
	})
}

// deleteLine is sed -i '/^KEY=/d'
func deleteLine(file, prefix string) {
	conf.SedFile(file, func(lines []string) []string {
		var out []string
		for _, l := range lines {
			if !strings.HasPrefix(l, prefix) {
				out = append(out, l)
			}
		}
		return out
	})
}

func (s *state) updateRedirects() bool {
	redirectURL := "https://" + s.newHostname
	if s.newHostname == defaultDomain {
		redirectURL = "http://" + s.serverIP
		s.logDebug("Updating redirects to use IP address")
	} else {
		s.logDebug("Updating redirects to use custom domain")
	}
	if !conf.IsFile(redirectsConf) {
		logError("Redirects configuration not found at " + redirectsConf)
		return false
	}
	for _, name := range []string{"openpanel", "openadmin"} {
		subLines(redirectsConf, regexp.MustCompile(`redir @`+name+` https?://[^:]+:`), "redir @"+name+" "+redirectURL+":")
	}
	s.updateWebmailRedirect()
	s.updateMcpProxy()
	return true
}

var webmailCurRe = regexp.MustCompile(`^redir @webmail (https?://[^ ]+)`)

// /webmail follows the hostname only while it's on the default (ip:8080 or webmail.<old hostname>), a custom one from opencli email-webmail stays
func (s *state) updateWebmailRedirect() {
	cur := firstGroup(conf.Lines(redirectsConf), webmailCurRe)
	if cur == "" {
		return
	}
	cur = strings.TrimSuffix(cur, "/")
	if cur != "http://"+s.serverIP+":8080" && cur != "https://webmail."+s.currentDomain && cur != "https://webmail."+defaultDomain {
		s.logDebug("Webmail redirect " + cur + " is custom, leaving it")
		return
	}
	var repl string
	if s.newHostname == defaultDomain {
		repl = "http://" + s.serverIP + ":8080/"
	} else if hasWebmailBlock(s.newHostname) {
		repl = "https://webmail." + s.newHostname + "/"
	} else {
		return
	}
	re := regexp.MustCompile(`^redir @webmail https?://[^ ]+`)
	subLines(redirectsConf, re, "redir @webmail "+repl)
	redis.DropPattern("openpanel_cache_get_webmail_domain:*")
	s.logDebug("Webmail redirect changed from " + cur + " to " + repl)
}

func hasWebmailBlock(host string) bool {
	re := regexp.MustCompile(`^webmail\.` + strings.ReplaceAll(regexp.QuoteMeta(host), `\.`, `\.`) + ` \{`)
	for _, l := range conf.Lines(caddyFile) {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

var mcpPortRe = regexp.MustCompile(`reverse_proxy @mcp https?://127\.0\.0\.1:([0-9]+)`)

// panel only serves tls when it has a cert for its domain, so the /mcp upstream scheme follows the domain
func (s *state) updateMcpProxy() {
	port := "2083"
	for _, l := range conf.Lines(redirectsConf) {
		if m := mcpPortRe.FindStringSubmatch(l); m != nil {
			port = m[1]
			break
		}
	}
	hasStart := false
	for _, l := range conf.Lines(redirectsConf) {
		if strings.HasPrefix(l, "# START MCP #") {
			hasStart = true
			break
		}
	}
	if !hasStart {
		if f, err := os.OpenFile(redirectsConf, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			f.WriteString("\n# MCP: every domain's /mcp is proxied to the panel, opencli domain/port keep this block in sync\n# START MCP #\n# END MCP #\n")
			f.Close()
		}
	}
	var b strings.Builder
	skip := false
	for _, l := range conf.Lines(redirectsConf) {
		if strings.HasPrefix(l, "# START MCP #") {
			b.WriteString(l + "\n")
			b.WriteString("@mcp path /mcp /mcp/*\n")
			if s.newHostname == defaultDomain {
				b.WriteString("reverse_proxy @mcp http://127.0.0.1:" + port + "\n")
			} else {
				b.WriteString("reverse_proxy @mcp https://127.0.0.1:" + port + " {\n")
				b.WriteString("    transport http {\n")
				b.WriteString("        tls_server_name " + s.newHostname + "\n")
				b.WriteString("    }\n")
				b.WriteString("}\n")
			}
			skip = true
			continue
		}
		if strings.HasPrefix(l, "# END MCP #") {
			skip = false
		}
		if !skip {
			b.WriteString(l + "\n")
		}
	}
	conf.WriteInPlace(redirectsConf, b.String())
}

// roundcube connects to ssl://<hostname>, so it's pinned to the public IP in the compose file
func (s *state) setRoundcubeExtraHost(host string) {
	conf.SedFile(mailserverCompose, func(lines []string) []string {
		var out []string
		skip := 0
		for _, l := range lines {
			if skip > 0 {
				skip--
				continue
			}
			if strings.HasSuffix(l, "extra_hosts: # opencli domain") {
				skip = 1
				continue
			}
			out = append(out, l)
		}
		return out
	})
	if host == "" || s.serverIP == "" {
		return
	}
	conf.SedFile(mailserverCompose, func(lines []string) []string {
		var out []string
		for _, l := range lines {
			out = append(out, l)
			if strings.Contains(l, "container_name: openadmin_roundcube") {
				out = append(out, "    extra_hosts: # opencli domain", `      - "`+host+":"+s.serverIP+`"`)
			}
		}
		return out
	})
}

// caddy requests the cert right after the reload, it usually lands within seconds
func (s *state) waitForHostnameCert(crt, key string) bool {
	s.logDebug("Waiting up to 60s for Caddy to issue SSL certificate for " + s.newHostname)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: s.newHostname},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:443")
		},
	}
	c := &http.Client{Timeout: 5 * time.Second, Transport: tr}
	if resp, err := c.Get("https://" + s.newHostname + "/"); err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	for i := 0; i < 30; i++ {
		if conf.IsFile(crt) && conf.IsFile(key) {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

func (s *state) configureMailserver() {
	if !conf.IsFile(mailserverEnv) {
		return
	}
	hasCompose := conf.IsFile(mailserverCompose)

	if s.newHostname == defaultDomain || isValidIPv4(s.newHostname) {
		s.logDebug("Configuring mailserver for plain-text authentication with IP")

		// used by roundcube to detect if SSL should be used
		changeLine(mailserverEnv, "OVERRIDE_HOSTNAME=", "OVERRIDE_HOSTNAME=")
		changeLine(mailserverEnv, "SSL_TYPE=", "SSL_TYPE=")
		deleteLine(mailserverEnv, "SSL_CERT_PATH=")
		deleteLine(mailserverEnv, "SSL_KEY_PATH=")

		// SRS needs a hostname to send forwarded mail from
		changeLine(mailserverEnv, "ENABLE_SRS=", "ENABLE_SRS=0")
		deleteLine(mailserverEnv, "SRS_DOMAINNAME=")

		// podman-compose doesn't interpolate ${VAR:+alt} conditionals like docker compose does, so hardcode the resolved roundcube values in the compose file too
		if hasCompose {
			subKey(mailserverCompose, "ROUNDCUBEMAIL_DEFAULT_HOST", "mailserver")
			subKey(mailserverCompose, "ROUNDCUBEMAIL_DEFAULT_PORT", "")
			subKey(mailserverCompose, "ROUNDCUBEMAIL_SMTP_SERVER", "mailserver")
			subKey(mailserverCompose, "ROUNDCUBEMAIL_SMTP_PORT", "")
			s.setRoundcubeExtraHost("")
		}
	} else {
		s.logDebug("Configuring mailserver for TLS/SSL authentication with domain")
		h := s.newHostname
		cert := "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/" + h + "/" + h + ".crt"
		key := "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/" + h + "/" + h + ".key"
		// custom paths!
		fbCert := "/etc/openpanel/caddy/ssl/custom/" + h + "/" + h + ".crt"
		fbKey := "/etc/openpanel/caddy/ssl/custom/" + h + "/" + h + ".key"

		switch {
		case conf.IsFile(cert) && conf.IsFile(key):
			s.logDebug("Using Let's Encrypt certs for " + h)
		case conf.IsFile(fbCert) && conf.IsFile(fbKey):
			s.logDebug("Using custom certs for " + h)
			cert, key = fbCert, fbKey
		case s.waitForHostnameCert(cert, key):
			s.logDebug("Let's Encrypt certs for " + h + " issued")
		default:
			// caddy may still get it later (dns propagation, rate limits), so finish the mail setup once it does
			fmt.Printf("SSL certificate for %s is not issued yet, mailserver will switch to it once Caddy gets one.\n", h)
			sh.Opencli("domain-bg", "cert-wait", h, cert, key).Quiet().Background()
			return
		}

		// used by roundcube to detect if SSL should be used
		subKey(mailserverEnv, "OVERRIDE_HOSTNAME", h)

		changeLine(mailserverEnv, "SSL_TYPE=", "SSL_TYPE=manual")
		envSetOrAppend("SSL_CERT_PATH", cert)
		envSetOrAppend("SSL_KEY_PATH", key)

		// forwarded mail goes out as SRS0=...@hostname, only with an accounts.sh that keeps the users' own mail out of SRS
		if strings.Contains(conf.Read("/usr/local/mail/openmail/openpanel/accounts.sh"), "_create_srs_exclude_map") {
			changeLine(mailserverEnv, "ENABLE_SRS=", "ENABLE_SRS=1")
			envSetOrAppend("SRS_DOMAINNAME", h)
		}

		if hasCompose {
			subKey(mailserverCompose, "ROUNDCUBEMAIL_DEFAULT_HOST", "ssl://"+h)
			subKey(mailserverCompose, "ROUNDCUBEMAIL_DEFAULT_PORT", "993")
			subKey(mailserverCompose, "ROUNDCUBEMAIL_SMTP_SERVER", "ssl://"+h)
			subKey(mailserverCompose, "ROUNDCUBEMAIL_SMTP_PORT", "465")
			s.setRoundcubeExtraHost(h)
		}
	}

	s.logDebug("Restarting mailserver and webmail to apply new configuration")
	sh.Opencli("domain-bg", "mail-restart").Quiet().Background()
}

// envSetOrAppend replaces ^KEY=.* in the mailserver env or appends the line
func envSetOrAppend(key, val string) {
	found := false
	for _, l := range conf.Lines(mailserverEnv) {
		if strings.HasPrefix(l, key+"=") {
			found = true
			break
		}
	}
	if found {
		changeLinePrefix(key, val)
		return
	}
	if f, err := os.OpenFile(mailserverEnv, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		f.WriteString(key + "=" + val + "\n")
		f.Close()
	}
}

func changeLinePrefix(key, val string) {
	subLines(mailserverEnv, regexp.MustCompile(`^`+regexp.QuoteMeta(key)+`=.*`), key+"="+val)
}

// OpenAdmin's SMTP follows the hostname only when it was pointing at the old one (or the IP), a custom relay or an empty setting is left alone
func (s *state) updateAdminSMTPServer() {
	const cfg = "/etc/openpanel/openpanel/conf/openpanel.config"
	if !conf.IsFile(cfg) {
		return
	}
	newVal := s.newHostname
	if s.newHostname == defaultDomain {
		newVal = s.serverIP
	}
	cur := ""
	in := false
	for _, l := range conf.Lines(cfg) {
		if strings.HasPrefix(l, "[SMTP]") {
			in = true
			continue
		}
		if strings.HasPrefix(l, "[") {
			in = false
		}
		if in {
			f := strings.Split(l, "=")
			if f[0] == "mail_server" {
				if len(f) > 1 {
					cur = f[1]
				}
				break
			}
		}
	}
	if cur == "" || cur == newVal {
		return
	}
	if (cur == s.currentDomain && s.currentDomain != defaultDomain) || (s.serverIP != "" && cur == s.serverIP) {
		conf.SedFile(cfg, func(lines []string) []string {
			in := false
			for i, l := range lines {
				if in {
					if strings.HasPrefix(l, "mail_server=") {
						lines[i] = "mail_server=" + newVal
					}
					if strings.HasPrefix(l, "[") {
						in = false
					}
				} else if strings.HasPrefix(l, "[SMTP]") {
					in = true
				}
			}
			return lines
		})
		s.logDebug("OpenAdmin SMTP server changed from " + cur + " to " + newVal)
	}
}

func (s *state) restartServices(args []string) bool {
	podman.CaddyReload()
	for _, a := range args {
		if a == "--no-restart" {
			s.logDebug("Skipping service restart due to --no-restart flag")
			return true
		}
	}
	s.logDebug("Restarting OpenPanel and Caddy containers")
	if os.Chdir("/root") != nil {
		return false
	}
	return sh.C("podman", "restart", "openpanel").Quiet().OK()
}

func (s *state) updateDomain(args []string) int {
	cd, ok := getCurrentDomain()
	if !ok {
		return 1
	}
	s.currentDomain = cd
	ip, ok := getServerIPv4()
	if !ok {
		return 1
	}
	s.serverIP = ip

	s.logDebug("Current domain: " + s.currentDomain)
	s.logDebug("Server IP: " + s.serverIP)
	s.logDebug("New hostname: " + s.newHostname)

	if !s.updateCaddyfile() || !s.createMvFile() {
		return 1
	}
	updateContainerHostsEntry(s.newHostname)
	if !s.updateRedirects() {
		return 1
	}
	redis.DropKey("openpanel_cache_app.get_openpanel_domain")
	if !s.restartServices(args) {
		return 1
	}
	s.configureMailserver()
	s.updateAdminSMTPServer()
	// users with base_hosts_file get the new domain from here, the rest still from /etc/hosts
	sh.Opencli("docker-hosts").Quiet().Run()
	display := s.newHostname
	if s.newHostname == defaultDomain {
		display = s.serverIP
	}
	fmt.Println(display + " is now set for accessing the OpenPanel and OpenAdmin interfaces.")
	return 0
}

func showUsage() {
	fmt.Println("Usage:")
	fmt.Println("")
	fmt.Println("opencli domain                        - displays current URL")
	fmt.Println("opencli domain set example.net        - set domain name for access")
	fmt.Println("opencli domain ip                     - set IP for access")
	fmt.Println("")
	fmt.Println("Options:")
	fmt.Println("  --debug                             - enable verbose output")
	fmt.Println("  --no-restart                        - skip service restart")
}

func (s *state) checkDomainNotAddedByUser() {
	out, _ := sh.Opencli("domains-whoowns", s.newHostname).Out()
	if strings.Contains(out, "not found in the database.") {
		const cfg = "/etc/openpanel/openpanel/conf/domain_restriction.txt"
		if conf.IsFile(cfg) {
			s.logDebug("Checking domain against forbidden_domains list")
			for _, f := range strings.Split(strings.TrimSuffix(conf.Read(cfg), "\n"), "\n") {
				if f == s.newHostname {
					fmt.Printf("ERROR: %s is a forbidden domain.\n", s.newHostname)
					cli.Exit(1)
				}
			}
		}
		return
	}
	fmt.Printf("ERROR: Domain %s is already in used by an OpenPanel account.\n", s.newHostname)
	cli.Exit(1)
}

func run(args []string) int {
	s := &state{}
	for _, a := range args {
		if a == "--debug" {
			s.debug = true
			s.logDebug("'--debug' flag provided: displaying verbose information.")
			break
		}
	}

	switch cli.Arg(args, 0) {
	case "":
		domain, _ := getCurrentDomain()
		if domain == defaultDomain {
			ip, ok := getServerIPv4()
			if !ok {
				return 1
			}
			fmt.Println(ip)
			return 0
		}
		fmt.Println(domain)
		return 0
	case "set":
		if cli.Arg(args, 1) == "" {
			logError("Domain name required for 'set' command")
			showUsage()
			return 1
		}
		if !isValidDomain(args[1]) {
			logError("Invalid domain format. Please provide a valid domain.")
			showUsage()
			return 1
		}
		s.newHostname = args[1]
		s.checkDomainNotAddedByUser()
		s.logDebug("Setting domain to: " + s.newHostname)
		return s.updateDomain(args)
	case "ip":
		s.newHostname = defaultDomain
		s.logDebug("Setting IP address for accessing panels")
		return s.updateDomain(args)
	}
	showUsage()
	return 1
}
