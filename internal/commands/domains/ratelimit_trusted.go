// ##############################################################################
// Script Name: internal/commands/domains/ratelimit_trusted.go
// Description: Visitor IPs that skip the Caddy rate limits: manual entries plus csf.allow and csf.ignore.
// Usage: opencli domains-ratelimit trusted [add|remove <ip|cidr>...|sync] [--json] [--quiet]
// Author: Stefan Pejcic
// Created: 23.11.2026
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
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"opencli/internal/conf"
)

// both limits skip these, the firewall's allow and ignore lists don't reach caddy on their own (#1366)
var (
	rlTrustedConf = rlCaddyDir + "/ratelimit_trusted.conf"
	rlCSFLists    = []string{"/etc/csf/csf.allow", "/etc/csf/csf.ignore"}
)

// loopback is always in, client_ip needs at least one range and an empty not {} would exempt everyone
var rlTrustedAlways = []string{"127.0.0.1/32", "::1/128"}

const (
	rlManualPrefix = "# manual:"
	rlCSFPrefix    = "# from csf.allow and csf.ignore:"
)

// rlTrustedEntry normalizes an IP or CIDR, false for anything else
func rlTrustedEntry(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if strings.Contains(s, "/") {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return "", false
		}
		return n.String(), true
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return "", false
	}
	return ip.String(), true
}

// rlCSFEntries reads plain IPs and CIDRs from csf.allow and csf.ignore; advanced filters (tcp|in|d=22|s=...) only allow some ports, so they're left out
func rlCSFEntries() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range rlCSFLists {
		for _, l := range conf.Lines(f) {
			if i := strings.Index(l, "#"); i >= 0 {
				l = l[:i]
			}
			fields := strings.Fields(l)
			if len(fields) == 0 || strings.EqualFold(fields[0], "Include") || strings.Contains(fields[0], "|") {
				continue
			}
			if e, ok := rlTrustedEntry(fields[0]); ok && !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	sort.Strings(out)
	return out
}

// rlReadManual is the '# manual:' line of the trusted file
func rlReadManual() []string {
	for _, l := range conf.Lines(rlTrustedConf) {
		if strings.HasPrefix(l, rlManualPrefix) {
			var out []string
			for _, f := range strings.Fields(strings.TrimPrefix(l, rlManualPrefix)) {
				if e, ok := rlTrustedEntry(f); ok {
					out = append(out, e)
				}
			}
			return out
		}
	}
	return nil
}

func rlUniq(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, e := range l {
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	return out
}

func rlTrustedContent(manual, csf []string) string {
	var b strings.Builder
	b.WriteString("# opencli domains-ratelimit trusted\n")
	b.WriteString("# visitor IPs that skip the per-IP and per-domain rate limits, written by opencli, don't edit by hand\n")
	b.WriteString(strings.TrimSpace(rlManualPrefix+" "+strings.Join(manual, " ")) + "\n")
	b.WriteString(strings.TrimSpace(rlCSFPrefix+" "+strings.Join(csf, " ")) + "\n")
	b.WriteString("client_ip " + strings.Join(rlUniq(rlTrustedAlways, manual, csf), " ") + "\n")
	return b.String()
}

// rlWriteTrusted rewrites the trusted file with fresh csf entries, true when it changed
func rlWriteTrusted(manual []string) bool {
	content := rlTrustedContent(manual, rlCSFEntries())
	if conf.Read(rlTrustedConf) == content {
		return false
	}
	rlWriteTmpMv(rlTrustedConf, content)
	return true
}

// rlConfReady: the snippets exist and already skip trusted IPs
func rlConfReady() (setUp, skipsTrusted bool) {
	for _, l := range conf.Lines(rlConf) {
		if strings.HasPrefix(l, "(ratelimit_ip)") {
			setUp = true
		}
		if strings.Contains(l, "@ratelimit_ip_counted") {
			skipsTrusted = true
		}
	}
	return
}

func rlTrusted(args []string, jsonOut, quiet bool) int {
	action := "list"
	if len(args) > 0 {
		action = args[0]
	}
	manual := rlReadManual()

	switch action {
	case "list":
		csf := rlCSFEntries()
		if jsonOut {
			b, _ := json.Marshal(map[string][]string{"manual": nonNil(manual), "csf": nonNil(csf), "always": rlTrustedAlways})
			fmt.Println(string(b))
			return 0
		}
		fmt.Println("Visitor IPs that skip the rate limits:")
		fmt.Printf("  manual:           %s\n", orNone(manual))
		fmt.Printf("  csf.allow/ignore: %s\n", orNone(csf))
		fmt.Printf("  always:           %s\n", strings.Join(rlTrustedAlways, " "))
		return 0
	case "add", "remove":
		if len(args) < 2 {
			rlUsage()
		}
		var entries []string
		for _, a := range args[1:] {
			e, ok := rlTrustedEntry(a)
			if !ok {
				fmt.Printf("ERROR: '%s' is not an IP address or CIDR range.\n", a)
				return 1
			}
			entries = append(entries, e)
		}
		if action == "add" {
			manual = rlUniq(manual, entries)
		} else {
			drop := map[string]bool{}
			for _, e := range entries {
				drop[e] = true
			}
			var kept []string
			for _, m := range manual {
				if !drop[m] {
					kept = append(kept, m)
				}
			}
			manual = kept
		}
	case "sync":
	default:
		rlUsage()
	}

	setUp, skipsTrusted := rlConfReady()
	if !setUp {
		// nothing imports the list yet, it's used once rate limiting is turned on
		rlWriteTrusted(manual)
		if !quiet {
			fmt.Println("Saved. Rate limiting isn't set up on this server yet, the list applies once it is.")
		}
		return 0
	}
	if skipsTrusted && conf.Read(rlTrustedConf) == rlTrustedContent(manual, rlCSFEntries()) {
		if !quiet {
			fmt.Println("Nothing changed.")
		}
		return 0
	}
	rlPrepare()
	rlWriteTrusted(manual)
	if !quiet {
		fmt.Printf("Trusted IPs: %d manual, %d from csf.allow and csf.ignore.\n", len(manual), len(rlCSFEntries()))
	}
	rlApply()
	return 0
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "-"
	}
	return strings.Join(s, " ")
}
