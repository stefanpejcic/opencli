// ##############################################################################
// Script Name: internal/commands/userreport/report_dns.go
// Description: Prints a health report for a user: what is working and what needs attention.
// Usage: opencli user-report <USERNAME|--all> [--section <name,...>] [--json]
// Author: Stefan Pejcic
// Created: 02.10.2026
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

package userreport

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"opencli/internal/conf"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

type dnsResult struct{ apex, ns, status, a, spf, dkim, dmarc string }

var statusRe = regexp.MustCompile(`status: (\w+)`)

// dnsLookup does all lookups for one domain at the same time, only NS has to wait for the zone apex
func dnsLookup(d string) dnsResult {
	var r dnsResult
	var wg sync.WaitGroup
	wg.Add(5)
	go func() {
		defer wg.Done()
		// the zone that answers for this name is the SOA owner, which is the parent zone for names that aren't delegated
		out := sh.C("dig", "+noall", "+answer", "+authority", "+time=2", "+tries=1", "@"+dnsResolver, "SOA", d).NoErr().Str()
		for _, l := range strings.Split(out, "\n") {
			if fieldN(l, 4) == "SOA" {
				r.apex = strings.TrimSuffix(fieldN(l, 1), ".")
				break
			}
		}
		if r.apex != "" {
			var ns []string
			for _, l := range strings.Split(pdig(r.apex, "NS"), "\n") {
				ns = append(ns, strings.TrimSuffix(l, "."))
			}
			sort.Strings(ns)
			r.ns = strings.Join(ns, " ")
		}
	}()
	go func() {
		defer wg.Done()
		full := sh.C("dig", "+time=2", "+tries=1", "@"+dnsResolver, "A", d).NoErr().Str()
		var st, a []string
		for _, l := range strings.Split(full, "\n") {
			for _, m := range statusRe.FindAllStringSubmatch(l, -1) {
				st = append(st, m[1])
			}
			if fieldN(l, 4) == "A" {
				a = append(a, fieldN(l, 5))
			}
		}
		r.status = strings.Join(st, "\n")
		r.a = strings.Join(a, " ")
	}()
	go func() {
		defer wg.Done()
		for _, l := range strings.Split(pdig(d, "TXT"), "\n") {
			if strings.HasPrefix(strings.ToLower(l), "v=spf1") {
				r.spf = l
				break
			}
		}
	}()
	go func() {
		defer wg.Done()
		r.dkim = strings.SplitN(pdig("mail._domainkey."+d, "TXT"), "\n", 2)[0]
	}()
	go func() {
		defer wg.Done()
		for _, l := range strings.Split(pdig("_dmarc."+d, "TXT"), "\n") {
			if strings.HasPrefix(strings.ToLower(l), "v=dmarc1") {
				r.dmarc = l
				break
			}
		}
	}()
	wg.Wait()
	// the script kept these as KEY=value lines and read back the first line of each
	r.status = strings.SplitN(r.status, "\n", 2)[0]
	return r
}

var (
	zoneRecRe  = regexp.MustCompile(`^[^;$]*[[:space:]]IN[[:space:]]`)
	updatedRe  = regexp.MustCompile(`updated=(\S+)`)
	dkimQuoted = regexp.MustCompile(`"[^"]*"`)
)

func secDNS(s *sec) {
	u, r := s.u, s.r
	s.section("DNS")

	r.needNameservers()
	nsConf := r.nsConf
	if len(nsConf) > 0 {
		s.row("Nameservers", strings.Join(nsConf, " ")+" ")
		for _, ns := range r.nsBroken {
			s.warn("Nameserver " + ns + " doesn't resolve, domains using it won't work.")
		}
	} else {
		s.row("Nameservers", "not set")
		s.warn("No nameservers are set (ns1/ns2), new zones get no NS records.")
	}

	ip := ipJSON(u.name)
	if ip == "" {
		ip = r.serverIP
	}
	s.row("Expected IP", ip)
	dnsState := r.rootState("openpanel_dns")
	s.row("DNS server", or(dnsState, "not installed"))
	if dnsState != "running" {
		s.err("DNS server container openpanel_dns isn't running, zones on this server aren't served.")
	}

	if u.domains == "" {
		s.row("Zones", "none, no domains")
		return
	}
	domains := hereLines(u.domains)
	res := make([]dnsResult, len(domains))
	var wg sync.WaitGroup
	for i, d := range domains {
		wg.Add(1)
		go func(i int, d string) { defer wg.Done(); res[i] = dnsLookup(d) }(i, d)
	}
	wg.Wait()

	s.p("\n  %s%s %s %s %s %s %s %s %s%s\n", r.cDim, padR("Domain", 36), padR("Zone", 8), padR("Records", 8), padR("NS", 10), padR("Points to", 10), padR("SPF", 9), padR("DKIM", 9), "DMARC", r.cReset)
	var dyn, badMail, notOurs, notHere []string
	for i, d := range domains {
		zone := "/etc/bind/zones/" + d + ".zone"
		records, zstate := "-", "missing"
		if conf.IsFile(zone) {
			zc := readFile(zone)
			records = grepCount(zone, zoneRecRe.MatchString)
			zstate = "ok"
			if dnsState == "running" {
				check, err := sh.C("podman", "exec", "openpanel_dns", "named-checkzone", d, zone).Timeout(10 * time.Second).Combined()
				if err != nil {
					zstate = "invalid"
					first := ""
					for _, l := range strings.Split(check, "\n") {
						if !strings.HasPrefix(l, "zone") {
							first = l
							break
						}
					}
					s.err("Zone for " + d + " has a syntax error: " + cutC(first, 120))
				}
			}
			// dynamic dns entries carry a webcall token comment
			for _, l := range awkLines(zc) {
				if !strings.Contains(l, "; webcall=") || l == "" {
					continue
				}
				var up []string
				for _, m := range updatedRe.FindAllStringSubmatch(l, -1) {
					up = append(up, m[1])
				}
				dyn = append(dyn, fieldN(l, 1)+"."+d+" "+fieldN(l, 4)+" "+fieldN(l, 5)+" (updated "+strings.Join(up, "\n")+")")
			}
		} else {
			// subdomains usually live in the parent's zone
			parent := ""
			for _, p := range domains {
				if strings.HasSuffix(d, "."+p) {
					parent = p
				}
			}
			if parent != "" {
				zstate = "parent"
			} else {
				s.warn(d + " has no DNS zone on this server.")
			}
		}

		x := res[i]
		// our NS if any configured nameserver is among the live ones
		nsState := "other"
		if x.status == "NXDOMAIN" || (x.apex == "" && x.a == "") {
			nsState = "none"
		} else {
			for _, n := range nsConf {
				if strings.Contains(" "+x.ns+" ", " "+strings.TrimSuffix(n, ".")+" ") {
					nsState = "ours"
				}
			}
		}
		aState := ""
		switch {
		case x.a == "":
			aState = "nothing"
		case strings.Contains(" "+x.a+" ", " "+ip+" "):
			aState = "this"
		default:
			aState = strings.SplitN(x.a, " ", 2)[0]
		}

		// same rules as the Email Deliverability page
		spfS := "missing"
		if x.spf != "" {
			spfS = "wrong_ip"
			if strings.Contains(x.spf, "ip4:"+ip) {
				spfS = "ok"
			}
		}
		expected := strings.Join(dkimQuoted.FindAllString(readFile("/usr/local/mail/openmail/docker-data/dms/config/opendkim/keys/"+d+"/mail.txt"), -1), "")
		expected = strings.NewReplacer(`"`, "", "\n", "").Replace(expected)
		dkimS := "missing"
		if x.dkim != "" {
			dkimS = "mismatch"
			if expected != "" && strings.ReplaceAll(x.dkim, " ", "") == strings.ReplaceAll(expected, " ", "") {
				dkimS = "ok"
			}
		}
		dmarcS := "missing"
		if x.dmarc != "" {
			dmarcS = "ok"
		}

		s.p("  %s %s %s %s %s %s %s %s\n", padR(d, 36), padR(zstate, 8), padR(records, 8), padR(nsState, 10), padR(aState, 10), padR(spfS, 9), padR(dkimS, 9), dmarcS)

		switch nsState {
		case "none":
			s.warn(d + " doesn't exist in public DNS, it isn't registered or has no nameservers.")
		case "other":
			notOurs = append(notOurs, or(x.apex, d))
		}
		if aState != "this" && aState != "nothing" {
			notHere = append(notHere, d+" ("+x.a+")")
		}
		if spfS+dkimS+dmarcS != "okokok" {
			badMail = append(badMail, d)
		}
	}

	s.line("")
	// grouped by the zone that really answers, e.g. "14 in openpanel.org"
	if len(notOurs) > 0 {
		var parts []string
		for _, c := range uniqCSorted(notOurs) {
			parts = append(parts, fmt.Sprintf("%d in zone %s", bashInt(c[0]), fieldN(c[1], 1)))
		}
		s.info(strings.Join(parts, ", ") + " answered by other nameservers, edits to these zones here have no effect.")
	}
	if len(notHere) > 0 {
		s.warn("Pointing to another IP, websites aren't served from this server: " + strings.Join(notHere, ", ") + ".")
	}
	if len(badMail) > 0 {
		if len(badMail) > 3 {
			s.warn(fmt.Sprintf("%d domains have SPF/DKIM/DMARC problems, emails from them may land in spam.", len(badMail)))
		} else {
			s.warn("SPF/DKIM/DMARC problems on " + strings.Join(badMail, " ") + ", emails from them may land in spam.")
		}
	}
	if len(dyn) > 0 {
		label := "Dynamic DNS"
		for _, e := range dyn {
			s.row(label, e)
			label = ""
		}
	} else {
		s.row("Dynamic DNS", "none")
	}
	s.dim("Live records checked through " + dnsResolver + ".")
}

// uniqCSorted is sort | uniq -c, in sorted order
func uniqCSorted(items []string) [][2]string {
	a := append([]string{}, items...)
	sort.Strings(a)
	var out [][2]string
	for i := 0; i < len(a); {
		j := i
		for j < len(a) && a[j] == a[i] {
			j++
		}
		out = append(out, [2]string{strconv.Itoa(j - i), a[i]})
		i = j
	}
	return out
}

const acmeDir = "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory"

var issuerRe = regexp.MustCompile(`(?:O = |O=)([^,/]+)`)

func certEnd(end string) int64 {
	if t, err := time.Parse("Jan _2 15:04:05 2006 MST", end); err == nil {
		return t.Unix()
	}
	if out, err := sh.C("date", "-d", end, "+%s").NoErr().Out(); err == nil {
		return bashInt(out)
	}
	return 0
}

func secSSL(s *sec) {
	u, r := s.u, s.r
	s.section("SSL")
	if u.domains == "" {
		s.row("Certificates", "none, no domains")
		return
	}
	now := time.Now().Unix()
	s.p("  %s%s %s %s %s %s%s\n", r.cDim, padR("Domain", 36), padR("Type", 8), padR("Issuer", 22), padR("Expires", 12), "Days left", r.cReset)
	nOK := 0
	for _, d := range hereLines(u.domains) {
		c := readFile("/etc/openpanel/caddy/domains/" + d + ".conf")
		cert, typ := "", "none"
		// same detection as 'opencli domains-ssl'
		if strings.Contains(c, "fullchain.pem") {
			typ, cert = "custom", "/etc/openpanel/caddy/ssl/custom/"+d+"/fullchain.pem"
		} else if strings.Contains(c, "on_demand") {
			typ, cert = "auto", acmeDir+"/"+d+"/"+d+".crt"
			// no Let's Encrypt one yet, caddy serves its own self-signed
			if !conf.IsFile(cert) && conf.IsFile("/etc/openpanel/caddy/ssl/local/"+d+"/"+d+".crt") {
				typ, cert = "self", "/etc/openpanel/caddy/ssl/local/"+d+"/"+d+".crt"
			}
		}

		reason := ""
		if cert == "" || !conf.IsFile(cert) {
			if typ == "auto" {
				reason = r.tlsErrorFor(d)
			}
			s.p("  %s %s %s %s %s\n", padR(d, 36), padR(typ, 8), padR("-", 22), padR("-", 12), "-")
			if typ == "auto" {
				m := d + " has no SSL certificate yet, AutoSSL hasn't issued one"
				if reason != "" {
					m += ": " + reason
				}
				s.err(m + ".")
			} else {
				s.err("No SSL certificate is configured for " + d + ".")
			}
			continue
		}

		issuer := ""
		if m := issuerRe.FindStringSubmatch(sh.C("openssl", "x509", "-issuer", "-noout", "-in", cert).NoErr().Str()); m != nil {
			issuer = m[1]
		}
		end := awkPart(sh.C("openssl", "x509", "-enddate", "-noout", "-in", cert).NoErr().Str(), "=", 2)
		endSecs := certEnd(end)
		days := (endSecs - now) / 86400
		endDay := time.Unix(endSecs, 0).Format("2006-01-02")
		s.p("  %s %s %s %s %d\n", padR(d, 36), padR(typ, 8), padR(runeCut(issuer, 22), 22), padR(endDay, 12), days)

		// same thresholds as the SSL page
		if (days <= 14 || typ == "self") && typ != "custom" {
			reason = r.tlsErrorFor(d)
		}
		switch {
		case endSecs < now:
			m := "SSL certificate for " + d + " expired on " + endDay
			if reason != "" {
				m += ", renewal error: " + reason
			}
			s.err(m + ".")
		case days <= 3:
			m := fmt.Sprintf("SSL certificate for %s expires in %d days", d, days)
			if reason != "" {
				m += ", renewal error: " + reason
			}
			s.err(m + ".")
		case days <= 14:
			m := fmt.Sprintf("SSL certificate for %s expires in %d days", d, days)
			if typ == "auto" {
				m += ", AutoSSL should have renewed it already"
			}
			if reason != "" {
				m += ": " + reason
			}
			s.warn(m + ".")
		case typ == "self":
			m := d + " uses a self-signed certificate, browsers show a security warning"
			if reason != "" {
				m += ". AutoSSL error: " + reason
			}
			s.warn(m + ".")
		default:
			nOK++
		}
	}
	s.line("")
	s.row("Valid", fmt.Sprintf("%d of %d", nOK, countLines(u.domains)))
}

const wafLogDir = "/var/log/caddy/coraza_waf"

// wafProfileForType maps a site type to its waf profile key and name, same catalog as the panel's WAF page
func wafProfileForType(t string) string {
	switch strings.ToLower(t) {
	case "wordpress":
		return "wordpress WordPress"
	case "drupal":
		return "drupal Drupal"
	case "nextcloud":
		return "nextcloud Nextcloud"
	case "dokuwiki":
		return "dokuwiki DokuWiki"
	case "phpbb":
		return "phpbb phpBB"
	}
	return ""
}

// jsonStream decodes json values one after another, stopping at the first bad one like jq does
func jsonStream(data string) []any {
	d := json.NewDecoder(strings.NewReader(data))
	d.UseNumber()
	var out []any
	for {
		var raw json.RawMessage
		if err := d.Decode(&raw); err == io.EOF || err != nil {
			return out
		}
		v, err := ojson.Parse(string(raw))
		if err != nil {
			return out
		}
		out = append(out, v)
	}
}

// jsonStreamOK is jsonStream plus whether it reached the end cleanly
func jsonStreamOK(data string) ([]any, bool) {
	d := json.NewDecoder(strings.NewReader(data))
	d.UseNumber()
	var out []any
	for {
		var raw json.RawMessage
		err := d.Decode(&raw)
		if err == io.EOF {
			return out, true
		}
		if err != nil {
			return out, false
		}
		v, err := ojson.Parse(string(raw))
		if err != nil {
			return out, false
		}
		out = append(out, v)
	}
}

func wafRecent(v any, since int64) bool {
	return numOf(jqPath(v, "transaction", "unix_timestamp"))/1e9 >= float64(since)
}

var (
	ruleEngineRe = regexp.MustCompile(`^\s*SecRuleEngine\s+(On|Off|DetectionOnly)`)
	paranoiaRe   = regexp.MustCompile(`blocking_paranoia_level=(\d+)`)
	inboundRe    = regexp.MustCompile(`inbound_anomaly_score_threshold=(\d+)`)
	includeRe    = regexp.MustCompile(`^\s*Include`)
	profileRe    = regexp.MustCompile(`/([a-z0-9]+)-rule-exclusions-plugin/plugins/`)
	ruleIDRe     = regexp.MustCompile(`^\[id "(\d+)"\] \[rev "[^"]*"\] \[msg "[^"]*"`)
	ruleMsgRe    = regexp.MustCompile(`^\[msg "([^"]*)`)
)

// ruleMatches is grep -oP '\[id "\K\d+(?=...)|\[msg "\K[^"]*' on one line
func ruleMatches(l string) []string {
	var out []string
	for i := 0; i < len(l); i++ {
		if l[i] != '[' {
			continue
		}
		if m := ruleIDRe.FindStringSubmatch(l[i:]); m != nil {
			out = append(out, m[1])
			i += len(`[id "`) + len(m[1]) - 1
			continue
		}
		if m := ruleMsgRe.FindStringSubmatch(l[i:]); m != nil {
			if m[1] != "" {
				out = append(out, m[1])
			}
			i += len(m[0]) - 1
		}
	}
	return out
}

func secWAF(s *sec) {
	u, r := s.u, s.r
	s.section("WAF")
	if conf.IsFile("/home/" + u.context + "/waf.disabled") {
		s.row("New domains", "WAF off")
		s.info("WAF is off by default for new domains on this account.")
	} else {
		s.row("New domains", "WAF on")
	}
	if u.domains == "" {
		s.row("Domains", "none")
		return
	}

	// apps found per domain, for recommending profiles
	detected := map[string]string{}
	for _, l := range hereLines(u.siteRows) {
		f := strings.Split(l, "\t")
		x := bread(idxS(f, 4)+"\t"+idxS(f, 1), "\t", 2)
		if p := wafProfileForType(x[1]); p != "" {
			detected[x[0]] += p + "\n"
		}
	}
	since := time.Now().Unix() - 86400

	s.p("  %s%s %s %s %s %s %s%s\n", r.cDim, padR("Domain", 36), padR("WAF", 10), padR("Level", 14), padR("Profiles", 22), padR("Matched", 10), "Blocked (24h)", r.cReset)
	var off, monitor, allLogs []string
	for _, d := range hereLines(u.domains) {
		confFile := "/etc/openpanel/caddy/domains/" + d + ".conf"
		if !conf.IsFile(confFile) {
			s.p("  %s %s\n", padR(d, 36), padR("no config", 10))
			continue
		}
		c := readFile(confFile)
		status := ""
		for _, l := range strings.Split(c, "\n") {
			if m := ruleEngineRe.FindStringSubmatch(l); m != nil {
				status = m[1]
				break
			}
		}
		status = or(status, "Unknown")

		// same level names as the WAF page, no SecAction line means standard
		level := "standard"
		for _, l := range strings.Split(c, "\n") {
			if strings.Contains(l, "blocking_paranoia_level=") {
				par, inb := "", ""
				if m := paranoiaRe.FindStringSubmatch(l); m != nil {
					par = m[1]
				}
				if m := inboundRe.FindStringSubmatch(l); m != nil {
					inb = m[1]
				}
				switch par + "/" + inb {
				case "1/10":
					level = "compatibility"
				case "1/5":
					level = "standard"
				case "2/5":
					level = "strict"
				default:
					level = "custom"
				}
				break
			}
		}
		var profs []string
		for _, l := range strings.Split(c, "\n") {
			if includeRe.MatchString(l) {
				for _, m := range profileRe.FindAllStringSubmatch(l, -1) {
					profs = append(profs, m[1])
				}
			}
		}
		sort.Strings(profs)
		profiles := strings.Join(uniq(profs), ",")

		checks, blocks := "0", "0"
		logf := wafLogDir + "/" + d + ".log"
		if st, err := os.Stat(logf); err == nil && st.Size() > 0 {
			allLogs = append(allLogs, logf)
			if vals, ok := jsonStreamOK(readFile(logf)); ok {
				n, b := 0, 0
				for _, v := range vals {
					if wafRecent(v, since) {
						n++
						if jqTruthy(jqPath(v, "transaction", "is_interrupted")) {
							b++
						}
					}
				}
				checks, blocks = strconv.Itoa(n), strconv.Itoa(b)
			}
		}
		s.p("  %s %s %s %s %s %s\n", padR(d, 36), padR(status, 10), padR(level, 14), padR(or(profiles, "-"), 22), padR(checks, 10), blocks)

		switch status {
		case "Off":
			off = append(off, d)
		case "DetectionOnly":
			monitor = append(monitor, d)
		}
		// recommend profiles for apps found on the domain that aren't turned on
		lines := strings.Split(detected[d], "\n")
		sort.Strings(lines)
		for _, l := range uniq(lines) {
			f := bread(l, " \t\n", 2)
			if f[0] == "" {
				continue
			}
			if !strings.Contains(","+profiles+",", ","+f[0]+",") {
				s.info(f[1] + " found on " + d + ", turn on the " + f[1] + " WAF profile to avoid false blocks.")
			}
		}
	}

	// same wording as the WAF page toast
	if len(off) == 1 {
		s.warn("WAF is disabled for " + off[0] + ".")
	} else if len(off) > 1 {
		s.warn(fmt.Sprintf("WAF is disabled for %d domains: %s.", len(off), strings.Join(off, " ")))
	}
	if len(monitor) > 0 {
		s.info("WAF only monitors " + strings.Join(monitor, " ") + ", nothing is blocked there.")
	}

	// account-wide top rules and IPs from blocked requests
	if len(allLogs) == 0 {
		return
	}
	var blocked []any
	for _, f := range allLogs {
		for _, v := range jsonStream(readFile(f)) {
			if wafRecent(v, since) && jqTruthy(jqPath(v, "transaction", "is_interrupted")) {
				blocked = append(blocked, v)
			}
		}
	}
	if len(blocked) == 0 {
		return
	}
	s.subsection("TOP BLOCKED (24h)")
	s.dim("Rules:")
	// skip the 949/959/980 score summary rules, they fire on every block
	var matches []string
	for _, b := range blocked {
		msgs, _ := jqPath(b, "messages").([]any)
		for _, m := range msgs {
			em := jqPath(m, "error_message")
			if em == nil || em == false {
				continue
			}
			for _, l := range strings.Split(ojson.Text(em), "\n") {
				matches = append(matches, ruleMatches(l)...)
			}
		}
	}
	var pairs []string
	skip := regexp.MustCompile(`^(949|959|980)[0-9]+`)
	for i := 0; i < len(matches); i += 2 {
		p := matches[i] + "\t"
		if i+1 < len(matches) {
			p += matches[i+1]
		}
		if !skip.MatchString(p) {
			pairs = append(pairs, p)
		}
	}
	for _, x := range head2(uniqC(pairs), 5) {
		f := bread(x[1], " \t\n", 2)
		s.p("  %s  %s %s\n", padL(x[0], 7), padR(f[0], 8), runeCut(f[1], 70))
	}
	s.dim("IPs:")
	var ips []string
	for _, b := range blocked {
		if v := jqPath(b, "transaction", "client_ip"); v != nil && v != false {
			ips = append(ips, ojson.Text(v))
		}
	}
	for _, x := range head2(uniqC(ips), 5) {
		s.p("  %s  %s\n", padL(x[0], 7), fieldN(x[1], 1))
	}
}
