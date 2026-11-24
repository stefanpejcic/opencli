// ##############################################################################
// Script Name: internal/commands/domains/ratelimit.go
// Description: Set the per-ip and per-domain request rate limits in Caddy, server default or per domain.
// Usage: opencli domains-ratelimit [status|stats|default|trusted|<targets>] [--json]
// Author: Stefan Pejcic
// Created: 15.10.2026
// Last Modified: 23.11.2026
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
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-ratelimit",
		Desc:  "Set the per-ip and per-domain request rate limits in Caddy, server default or per domain.",
		Usage: "opencli domains-ratelimit [status|stats|default|trusted|<targets>] [--json]",
		Run:   ratelimit,
	})
}

// limits are requests per minute; 0 turns that zone off
const (
	rlCaddyDir     = "/etc/openpanel/caddy"
	rlCaddyfile    = rlCaddyDir + "/Caddyfile"
	rlConf         = rlCaddyDir + "/ratelimit.conf"
	rlDomainDir    = rlCaddyDir + "/domains"
	rlCaddyImage   = "docker.io/openpanel/caddy-coraza:latest"
	rlCaddyfileCnt = "/etc/caddy/Caddyfile"
)

var (
	rlLineRe     = regexp.MustCompile(`^[[:space:]]*(import[[:space:]]+ratelimit(_ip|_site)?([[:space:]].*)?|# ratelimit off)[[:space:]]*$`)
	rlMarkerRe   = regexp.MustCompile(`^[[:space:]]*# rate limit \(opencli domains-ratelimit\)[[:space:]]*$`)
	rlDefaultRe  = regexp.MustCompile(`^[[:space:]]*import[[:space:]]+ratelimit[[:space:]]`)
	rlCustomRe   = regexp.MustCompile(`^[[:space:]]*import[[:space:]]+ratelimit_(ip|site)[[:space:]]`)
	rlRedirectRe = regexp.MustCompile(`import /etc/openpanel/caddy/redirects.conf`)
	rlRedirectAw = regexp.MustCompile(`import /etc/openpanel/caddy/redirects\.conf`)
	rlServersRe  = regexp.MustCompile(`^[[:space:]]*servers([[:space:]]|\{)`)
	rlTrustedRe  = regexp.MustCompile(`^[[:space:]]*(trusted_proxies|client_ip_headers)[[:space:]]`)
	rlGlobalRe   = regexp.MustCompile(`^\{[[:space:]]*$`)
	rlDigitsRe   = regexp.MustCompile(`^[0-9]+$`)
	rlUserRe     = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	rlTsRe       = regexp.MustCompile(`"ts":[0-9.]+`)
	rlStatusRe   = regexp.MustCompile(`"size":[0-9]+,"status":[0-9]+`)
	rlIPRe       = regexp.MustCompile(`"client_ip":"[^"]*"`)
)

func rlUsage() {
	fmt.Println("Usage:")
	fmt.Println("  opencli domains-ratelimit [status] [--json]                         show the server default and every domain")
	fmt.Println("  opencli domains-ratelimit default <per_ip> <per_site>               server default, used by domains set to 'default'")
	fmt.Println("  opencli domains-ratelimit <targets> <per_ip> <per_site>             custom limits")
	fmt.Println("  opencli domains-ratelimit <targets> default|off                     use the server default, or no limits")
	fmt.Println("  opencli domains-ratelimit stats [targets] [--hours 24] [--json]     requests, 429s and peaks from the access logs")
	fmt.Println("  opencli domains-ratelimit trusted [--json]                          visitor IPs that skip the limits")
	fmt.Println("  opencli domains-ratelimit trusted add|remove <ip|cidr>...           manage the manual trusted IPs")
	fmt.Println("  opencli domains-ratelimit trusted sync [--quiet]                    re-read csf.allow and csf.ignore")
	fmt.Println("")
	fmt.Println("Targets: one or more domains, --all, or --user <username>, mixed freely.")
	fmt.Println("Limits are requests per minute, per visitor IP and per domain; 0 turns that limit off.")
	fmt.Println("IPs in csf.allow and csf.ignore, loopback and the manual trusted list skip both limits.")
	fmt.Println("Example: opencli domains-ratelimit --user john shop.com 600 20000")
	cli.Exit(1)
}

func rlIsLimit(s string) bool { return rlDigitsRe.MatchString(s) }

// rlPositive is (( n > 0 )) for a digits only string
func rlPositive(s string) bool { return strings.Trim(s, "0") != "" }

// rlReadDefault reads "<per_ip> <per_site>" from the '# default:' line, 0 0 when missing
func rlReadDefault() (string, string) {
	line := ""
	for _, l := range conf.Lines(rlConf) {
		if strings.HasPrefix(l, "# default:") {
			line = strings.TrimPrefix(l, "# default:")
			break
		}
	}
	if line == "" {
		line = "0 0"
	}
	f := readFields(line, 2)
	if !rlIsLimit(f[0]) {
		f[0] = "0"
	}
	if !rlIsLimit(f[1]) {
		f[1] = "0"
	}
	return f[0], f[1]
}

// rlLimitLines are the import lines for a set of limits, indented like the redirects import above them
func rlLimitLines(indent, ip, site, domain string) []string {
	var res []string
	if rlPositive(ip) {
		res = append(res, fmt.Sprintf("%simport ratelimit_ip %s %s", indent, ip, domain))
	}
	if rlPositive(site) {
		res = append(res, fmt.Sprintf("%simport ratelimit_site %s %s", indent, site, domain))
	}
	if !rlPositive(ip) && !rlPositive(site) {
		res = append(res, indent+"# ratelimit off")
	}
	return res
}

func rlWriteTmpMv(path, content string) {
	tmp := path + ".tmp"
	os.WriteFile(tmp, []byte(content), 0o644)
	os.Rename(tmp, path)
}

func rlWriteConf(ip, site string) {
	var b strings.Builder
	w := func(s string) { b.WriteString(s + "\n") }
	w("# opencli domains-ratelimit")
	w("# requests per minute, per visitor IP and per domain, for domains with `import ratelimit <domain>`; 0 = off")
	w("# default: " + ip + " " + site)
	w("#")
	w("# zone names carry the domain because caddy-ratelimit shares a zone's state by name across all sites, so a per-domain override would change everyone's limit")
	w("# and the limit, because raising a limit on a live zone keeps blocking until its oldest hit leaves the window (SetMaxEvents resets the cursor)")
	w("")
	w("# IPs in ratelimit_trusted.conf (csf.allow, csf.ignore and 'opencli domains-ratelimit trusted add') skip both limits")
	w("(ratelimit_ip) {")
	w("    @ratelimit_ip_counted {")
	w("        not {")
	w("            import " + rlTrustedConf)
	w("        }")
	w("    }")
	w("    rate_limit @ratelimit_ip_counted {")
	w("        zone ip_{args[1]}_{args[0]} {")
	w("            key {client_ip}")
	w("            events {args[0]}")
	w("            window 1m")
	w("        }")
	w("    }")
	w("}")
	w("")
	w("(ratelimit_site) {")
	w("    @ratelimit_site_counted {")
	w("        not {")
	w("            import " + rlTrustedConf)
	w("        }")
	w("    }")
	w("    rate_limit @ratelimit_site_counted {")
	w("        zone site_{args[1]}_{args[0]} {")
	w("            key {host}")
	w("            events {args[0]}")
	w("            window 1m")
	w("        }")
	w("    }")
	w("}")
	w("")
	w("# server default, args[0] = domain")
	w("(ratelimit) {")
	for _, l := range rlLimitLines("    ", ip, site, "{args[0]}") {
		if !strings.Contains(l, "# ratelimit off") {
			w(l)
		}
	}
	w("}")
	rlWriteTmpMv(rlConf, b.String())
}

// rlDomainSetting is "default", "off" or "<per_ip> <per_site>" for one domain file
func rlDomainSetting(file string) string {
	lines := conf.Lines(file)
	for _, l := range lines {
		if rlDefaultRe.MatchString(l) {
			return "default"
		}
	}
	custom := false
	for _, l := range lines {
		if rlCustomRe.MatchString(l) {
			custom = true
			break
		}
	}
	if !custom {
		return "off"
	}
	ip, site := "", ""
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "import" {
			if f[1] == "ratelimit_ip" && ip == "" && len(f) >= 3 {
				ip = f[2]
			}
		}
	}
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "import" {
			if f[1] == "ratelimit_site" && site == "" && len(f) >= 3 {
				site = f[2]
			}
		}
	}
	if ip == "" {
		ip = "0"
	}
	if site == "" {
		site = "0"
	}
	return ip + " " + site
}

// rlSetDomain replaces the domain's rate limit lines with the new set, placed right after each redirects import
func rlSetDomain(file, mode, ip, site string) {
	domain := strings.TrimSuffix(filepath.Base(file), ".conf")
	content := conf.Read(file)
	if !rlRedirectRe.MatchString(content) {
		fmt.Printf("SKIPPED: %s (no redirects import to place the limits after, suspended or custom vhost?)\n", domain)
		return
	}
	var out []string
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	for _, l := range lines {
		if rlLineRe.MatchString(l) || rlMarkerRe.MatchString(l) {
			continue
		}
		out = append(out, l)
		if rlRedirectAw.MatchString(l) {
			indent := l[:len(l)-len(strings.TrimLeft(l, " \t\n\v\f\r"))]
			out = append(out, "", indent+"# rate limit (opencli domains-ratelimit)")
			if mode == "default" {
				out = append(out, indent+"import ratelimit "+domain)
			} else {
				out = append(out, rlLimitLines(indent, ip, site, domain)...)
			}
		}
	}
	// cat -s
	var b strings.Builder
	prevEmpty := false
	for _, l := range out {
		if l == "" {
			if prevEmpty {
				continue
			}
			prevEmpty = true
		} else {
			prevEmpty = false
		}
		b.WriteString(l + "\n")
	}
	rlWriteTmpMv(file, b.String())

	switch {
	case mode == "default":
		fmt.Printf("Updated: %s uses the server default\n", domain)
	case !rlPositive(ip) && !rlPositive(site):
		fmt.Printf("Updated: %s has no rate limits\n", domain)
	default:
		fmt.Printf("Updated: %s %s/min per IP, %s/min per domain\n", domain, ip, site)
	}
}

// caddy bind-mounts the Caddyfile as a single file, so it has to keep its inode
func rlWriteInPlace(content, dest string) {
	conf.WriteInPlace(dest, content)
}

// the limits key on {client_ip}, so behind cloudflare it has to be the visitor and not the edge, otherwise one CF ip limits everyone
func rlEnsureTrustedProxies() {
	content := conf.Read(rlCaddyfile)
	lines := conf.Lines(rlCaddyfile)
	if strings.Contains(content, "# START TRUSTED PROXIES #") {
		return
	}
	for _, l := range lines {
		if rlTrustedRe.MatchString(l) {
			return
		}
	}

	var parts []string
	for _, u := range []string{"https://www.cloudflare.com/ips-v4", "https://www.cloudflare.com/ips-v6"} {
		c := &http.Client{Timeout: 10 * time.Second}
		resp, err := c.Get(u)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			continue
		}
		parts = append(parts, string(b))
	}
	ranges := ""
	for _, l := range nonBlank(strings.Join(parts, "\n")) {
		ranges += l + " "
	}
	if ranges == "" {
		ranges = "173.245.48.0/20 103.21.244.0/22 103.22.200.0/22 103.31.4.0/22 141.101.64.0/18 108.162.192.0/18 190.93.240.0/20 188.114.96.0/20 197.234.240.0/22 198.41.128.0/17 162.158.0.0/15 104.16.0.0/13 104.24.0.0/14 172.64.0.0/13 131.0.72.0/22 2400:cb00::/32 2606:4700::/32 2803:f800::/32 2405:b500::/32 2405:8100::/32 2a06:98c0::/29 2c0f:f248::/32"
	}

	for _, l := range lines {
		if rlServersRe.MatchString(l) {
			fmt.Printf("WARNING: %s already has a servers block, add 'trusted_proxies static <cloudflare ranges>' and 'client_ip_headers CF-Connecting-IP' to it manually.\n", rlCaddyfile)
			return
		}
	}

	fmt.Printf("Adding Cloudflare as trusted proxies to %s...\n", rlCaddyfile)
	ranges = strings.TrimSuffix(ranges, " ")
	var b strings.Builder
	done := false
	for _, l := range lines {
		if !done && rlGlobalRe.MatchString(l) {
			b.WriteString(l + "\n")
			b.WriteString("\n")
			b.WriteString("    # START TRUSTED PROXIES #\n")
			b.WriteString("    # {client_ip} is the real visitor behind cloudflare, so per-ip rate limits don't lump everyone on one edge ip together\n")
			b.WriteString("    servers {\n")
			b.WriteString("        trusted_proxies static " + ranges + "\n")
			b.WriteString("        client_ip_headers CF-Connecting-IP\n")
			b.WriteString("    }\n")
			b.WriteString("    # END TRUSTED PROXIES #\n")
			done = true
			continue
		}
		b.WriteString(l + "\n")
	}
	rlWriteInPlace(b.String(), rlCaddyfile)
}

func rlEnsureCaddyfileImport() {
	imp := "import " + rlConf
	content := conf.Read(rlCaddyfile)
	if strings.Contains(content, imp) {
		return
	}
	fmt.Printf("Adding the rate limit snippets import to %s...\n", rlCaddyfile)
	nl := strings.HasSuffix(content, "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "import /etc/openpanel/caddy/domains/*") {
			lines[i] = "# import rate limit snippets (opencli domains-ratelimit)\n" + imp + "\n\n" + l
		}
	}
	out := strings.Join(lines, "\n")
	if nl {
		out += "\n"
	}
	rlWriteInPlace(out, rlCaddyfile)
}

func rlHasModule() bool {
	out := sh.C("podman", "exec", "caddy", "caddy", "list-modules").NoErr().Str()
	for _, l := range strings.Split(out, "\n") {
		if l == "http.handlers.rate_limit" {
			return true
		}
	}
	return false
}

// caddy images before the rate limit module fail to load any config that uses it
func rlEnsureCaddyModule() bool {
	if rlHasModule() {
		return true
	}
	fmt.Println("The running Caddy has no rate limit module, pulling the new image and recreating the caddy container...")
	sh.C("podman", "pull", rlCaddyImage).NoOut().Run()
	sh.C("podman-compose", "up", "-d", "--no-deps", "--force-recreate", "caddy").Dir("/root").Quiet().Run()
	for i := 0; i < 30; i++ {
		if rlHasModule() {
			return true
		}
		time.Sleep(time.Second)
	}
	fmt.Println("ERROR: Caddy still has no rate limit module, check 'podman logs caddy'.")
	return false
}

// cp -a for a single file, an existing destination is written in place so its inode stays
func rlCpA(src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if conf.Exists(dst) {
		if err := conf.WriteInPlace(dst, string(b)); err != nil {
			return err
		}
	} else if err := os.WriteFile(dst, b, st.Mode().Perm()); err != nil {
		return err
	}
	os.Chmod(dst, st.Mode().Perm())
	chownLike(dst, st)
	os.Chtimes(dst, st.ModTime(), st.ModTime())
	return nil
}

var rlBackupDir string

// snapshot of everything this script may touch, put back if caddy rejects the result
func rlBackup() {
	dir, err := os.MkdirTemp("", "tmp.")
	if err != nil {
		cli.Exit(1)
	}
	rlBackupDir = dir
	if err := rlCpA(rlCaddyfile, dir+"/Caddyfile"); err != nil {
		fmt.Fprintf(os.Stderr, "cp: cannot stat '%s': No such file or directory\n", rlCaddyfile)
		cli.Exit(1)
	}
	if conf.IsFile(rlConf) {
		rlCpA(rlConf, dir+"/ratelimit.conf")
	}
	if conf.IsFile(rlTrustedConf) {
		rlCpA(rlTrustedConf, dir+"/ratelimit_trusted.conf")
	}
	os.MkdirAll(dir+"/domains", 0o755)
	files, _ := filepath.Glob(rlDomainDir + "/*.conf")
	for _, f := range files {
		rlCpA(f, dir+"/domains/"+filepath.Base(f))
	}
}

func rlRestore() {
	rlCpA(rlBackupDir+"/Caddyfile", rlCaddyfile)
	if conf.IsFile(rlBackupDir + "/ratelimit.conf") {
		rlCpA(rlBackupDir+"/ratelimit.conf", rlConf)
	}
	if conf.IsFile(rlBackupDir + "/ratelimit_trusted.conf") {
		rlCpA(rlBackupDir+"/ratelimit_trusted.conf", rlTrustedConf)
	} else {
		os.Remove(rlTrustedConf)
	}
	files, _ := filepath.Glob(rlBackupDir + "/domains/*.conf")
	for _, f := range files {
		rlCpA(f, rlDomainDir+"/"+filepath.Base(f))
	}
}

func rlApply() {
	out, err := sh.C("podman", "exec", "caddy", "caddy", "validate", "--config", rlCaddyfileCnt).Combined()
	if err != nil {
		fmt.Println("ERROR: Caddy rejected the new config, nothing was changed:")
		var hits []string
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(strings.ToLower(l), "error") {
				hits = append(hits, l)
			}
		}
		// grep finding nothing fails the pipeline and set -e ends the script before the restore
		if len(hits) == 0 {
			cli.Exit(1)
		}
		if len(hits) > 5 {
			hits = hits[len(hits)-5:]
		}
		for _, l := range hits {
			fmt.Println(l)
		}
		rlRestore()
		os.RemoveAll(rlBackupDir)
		cli.Exit(1)
	}
	out, err = sh.C("podman", "exec", "caddy", "caddy", "reload", "--config", rlCaddyfileCnt).Combined()
	if err == nil {
		fmt.Println("SUCCESS: Caddy reloaded.")
	} else {
		fmt.Println("WARNING: Failed to reload Caddy.")
		lines := strings.Split(out, "\n")
		if len(lines) > 5 {
			lines = lines[len(lines)-5:]
		}
		fmt.Println(strings.Join(lines, "\n"))
	}
	os.RemoveAll(rlBackupDir)
}

// every change needs the snippets file, the import and the module in place before caddy sees it
func rlPrepare() {
	rlBackup()
	// the snippets import the trusted list, so it has to exist first
	if !conf.IsFile(rlTrustedConf) {
		rlWriteTrusted(rlReadManual())
	}
	// the 2.0.16 update only writes an empty default and snippets before 2.0.29 don't skip trusted IPs, both get rewritten here
	if has, skips := rlConfReady(); !has || !skips {
		ip, site := rlReadDefault()
		rlWriteConf(ip, site)
	}
	rlEnsureCaddyfileImport()
	rlEnsureTrustedProxies()
	if !rlEnsureCaddyModule() {
		rlRestore()
		os.RemoveAll(rlBackupDir)
		cli.Exit(1)
	}
}

func rlStatus(jsonOut bool) {
	dIP, dSite := rlReadDefault()
	files, _ := filepath.Glob(rlDomainDir + "/*.conf")
	if jsonOut {
		var b strings.Builder
		fmt.Fprintf(&b, `{"default":{"per_ip":%s,"per_site":%s},"domains":[`, dIP, dSite)
		for i, f := range files {
			mode, ip, site := "", "", ""
			switch s := rlDomainSetting(f); s {
			case "default":
				mode, ip, site = "default", dIP, dSite
			case "off":
				mode, ip, site = "off", "0", "0"
			default:
				mode = "custom"
				p := readFields(s, 2)
				ip, site = p[0], p[1]
			}
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"domain":"%s","mode":"%s","per_ip":%s,"per_site":%s}`, strings.TrimSuffix(filepath.Base(f), ".conf"), mode, ip, site)
		}
		b.WriteString("]}\n")
		fmt.Print(b.String())
		return
	}
	if !rlPositive(dIP) && !rlPositive(dSite) {
		fmt.Println("Server default: off")
	} else {
		fmt.Printf("Server default: %s/min per IP, %s/min per domain (0 = off)\n", dIP, dSite)
	}
	fmt.Println("----------------------------------------")
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".conf")
		switch s := rlDomainSetting(f); s {
		case "default", "off":
			fmt.Printf("  %s: %s\n", name, s)
		default:
			p := readFields(s, 2)
			fmt.Printf("  %s: %s/min per IP, %s/min per domain\n", name, p[0], p[1])
		}
	}
}

type rlStat struct {
	requests, blocked, peak, peakIP int
	top                             []rlTop
}

type rlTop struct {
	ip    string
	count int
}

// rlLogFiles are the access logs inside the window, oldest first
func rlLogFiles(dir string, since int64) []string {
	type ent struct {
		mt   int64
		name string
	}
	var ents []ent
	for _, pat := range []string{"access*.log", "access*.log.gz"} {
		files, _ := filepath.Glob(dir + "/" + pat)
		for _, f := range files {
			st, err := os.Stat(f)
			if err != nil {
				continue
			}
			if f == dir+"/access.log" || st.ModTime().Unix() >= since {
				ents = append(ents, ent{st.ModTime().Unix(), f})
			}
		}
	}
	sort.SliceStable(ents, func(i, j int) bool {
		if ents[i].mt != ents[j].mt {
			return ents[i].mt < ents[j].mt
		}
		return ents[i].name < ents[j].name
	})
	var res []string
	for _, e := range ents {
		res = append(res, e.name)
	}
	return res
}

func rlParseTs(s string) float64 {
	for len(s) > 0 {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v
		}
		s = s[:len(s)-1]
	}
	return 0
}

func rlDomainStats(domain string, hours int) rlStat {
	dir := "/var/log/caddy/domlogs/" + domain
	since := time.Now().Unix() - int64(hours)*3600
	var st rlStat
	var lastm int64
	cnt := 0
	pip := map[string]int{}
	bip := map[string]int{}
	handle := func(line string) {
		m := rlTsRe.FindString(line)
		if m == "" {
			return
		}
		ts := rlParseTs(m[5:])
		if ts < float64(since) {
			return
		}
		sm := rlStatusRe.FindString(line)
		if sm == "" {
			return
		}
		status := sm[strings.Index(sm, `"status":`)+9:]
		ip := ""
		if im := rlIPRe.FindString(line); im != "" {
			ip = im[13 : len(im)-1]
		}
		mn := int64(ts / 60)
		if mn != lastm {
			cnt = 0
			pip = map[string]int{}
			lastm = mn
		}
		st.requests++
		cnt++
		if cnt > st.peak {
			st.peak = cnt
		}
		pip[ip]++
		if pip[ip] > st.peakIP {
			st.peakIP = pip[ip]
		}
		if status == "429" {
			st.blocked++
			bip[ip]++
		}
	}

	var data strings.Builder
	for _, f := range rlLogFiles(dir, since) {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		var r io.Reader = fh
		if strings.HasSuffix(f, ".gz") {
			gz, err := gzip.NewReader(fh)
			if err != nil {
				fh.Close()
				continue
			}
			r = gz
		}
		b, _ := io.ReadAll(r)
		data.Write(b)
		fh.Close()
	}
	sc := bufio.NewScanner(strings.NewReader(data.String()))
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		handle(sc.Text())
	}

	for k, v := range bip {
		st.top = append(st.top, rlTop{k, v})
	}
	sort.Slice(st.top, func(i, j int) bool {
		if st.top[i].count != st.top[j].count {
			return st.top[i].count > st.top[j].count
		}
		return st.top[i].ip < st.top[j].ip
	})
	if len(st.top) > 5 {
		st.top = st.top[:5]
	}
	return st
}

func rlStats(jsonOut bool, hours int, domains []string) {
	if jsonOut {
		var b strings.Builder
		b.WriteString("[")
		for i, d := range domains {
			if i > 0 {
				b.WriteString(",")
			}
			s := rlDomainStats(d, hours)
			fmt.Fprintf(&b, `{"domain":"%s","hours":%d,"requests":%d,"blocked":%d,"peak_minute":%d,"peak_ip_minute":%d,"top_blocked":[`, d, hours, s.requests, s.blocked, s.peak, s.peakIP)
			for n, t := range s.top {
				if n > 0 {
					b.WriteString(",")
				}
				fmt.Fprintf(&b, `{"ip":"%s","count":%d}`, t.ip, t.count)
			}
			b.WriteString("]}")
		}
		b.WriteString("]\n")
		fmt.Print(b.String())
		return
	}
	fmt.Printf("%-40s %10s %8s %12s %14s\n", fmt.Sprintf("Domain (last %dh)", hours), "Requests", "429s", "Peak/min", "Peak/min/IP")
	for _, d := range domains {
		s := rlDomainStats(d, hours)
		fmt.Printf("%-40s %10d %8d %12d %14d\n", d, s.requests, s.blocked, s.peak, s.peakIP)
	}
}

// rlResolve turns domain names, --all and --user <username> into domains that have a caddy config
func rlResolve(args []string) []string {
	var res []string
	for i := 0; i < len(args); i++ {
		t := args[i]
		switch {
		case t == "--all":
			files, _ := filepath.Glob(rlDomainDir + "/*.conf")
			for _, f := range files {
				res = append(res, strings.TrimSuffix(filepath.Base(f), ".conf"))
			}
		case t == "--user":
			u := ""
			if i+1 < len(args) {
				u = args[i+1]
			}
			i++
			if !rlUserRe.MatchString(u) {
				fmt.Println("ERROR: --user needs a username.")
				cli.Exit(1)
			}
			found := false
			out, _ := sh.Opencli("domains-user", u).NoErr().Out()
			for _, l := range strings.Split(out, "\n") {
				d := strings.TrimSpace(l)
				if domainNameRe.MatchString(d) && conf.IsFile(rlDomainDir+"/"+d+".conf") {
					res = append(res, d)
					found = true
				}
			}
			if !found {
				fmt.Printf("ERROR: No domains found for user %s.\n", u)
				cli.Exit(1)
			}
		case strings.HasPrefix(t, "-"):
			rlUsage()
		default:
			// a domain name never has a slash, this also keeps the path inside the domains dir
			if !domainNameRe.MatchString(t) {
				fmt.Println("ERROR: Invalid domain: " + t)
				cli.Exit(1)
			}
			if conf.IsFile(rlDomainDir + "/" + t + ".conf") {
				res = append(res, t)
			} else {
				fmt.Printf("ERROR: Domain config not found: %s/%s.conf\n", rlDomainDir, t)
			}
		}
	}
	// same domain twice, e.g. a name plus --user
	seen := map[string]bool{}
	var uniq []string
	for _, d := range res {
		if d != "" && !seen[d] {
			seen[d] = true
			uniq = append(uniq, d)
		}
	}
	return uniq
}

func ratelimit(args []string) int {
	jsonOut, quiet := false, false
	hours := 24
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--quiet":
			quiet = true
		case "--hours":
			v := ""
			if i+1 < len(args) {
				v = args[i+1]
			}
			i++
			n, err := strconv.Atoi(v)
			if !rlIsLimit(v) || err != nil || n <= 0 || n > 720 {
				fmt.Println("ERROR: --hours must be 1-720.")
				return 1
			}
			hours = n
		default:
			rest = append(rest, args[i])
		}
	}

	action := "status"
	if len(rest) > 0 {
		action = rest[0]
	}

	switch action {
	case "status":
		rlStatus(jsonOut)
		return 0
	case "trusted":
		return rlTrusted(rest[1:], jsonOut, quiet)
	case "stats":
		targets := rest[1:]
		if len(targets) == 0 {
			targets = []string{"--all"}
		}
		rlStats(jsonOut, hours, rlResolve(targets))
		return 0
	case "default":
		if !(len(rest) == 3 && rlIsLimit(rest[1]) && rlIsLimit(rest[2])) {
			rlUsage()
		}
		rlPrepare()
		rlWriteConf(rest[1], rest[2])
		fmt.Printf("Server default set to %s/min per IP, %s/min per domain (0 = off).\n", rest[1], rest[2])
		rlApply()
		return 0
	}

	// <target...> then default, off or <per_ip> <per_site>
	if len(rest) < 2 {
		rlUsage()
	}
	last := rest[len(rest)-1]
	var mode, ip, site string
	var targets []string
	if last == "default" || last == "off" {
		mode, ip, site = last, "0", "0"
		targets = rest[:len(rest)-1]
	} else {
		if !(len(rest) >= 3 && rlIsLimit(rest[len(rest)-2]) && rlIsLimit(last)) {
			rlUsage()
		}
		mode, ip, site = "custom", rest[len(rest)-2], last
		targets = rest[:len(rest)-2]
	}

	domains := rlResolve(targets)
	if len(domains) == 0 {
		fmt.Println("ERROR: No domains to change.")
		return 1
	}
	rlPrepare()
	for _, d := range domains {
		rlSetDomain(rlDomainDir+"/"+d+".conf", mode, ip, site)
	}
	rlApply()
	return 0
}
