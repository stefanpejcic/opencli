// ##############################################################################
// Script Name: internal/commands/domains/hsts.go
// Description: Manage HSTS for a domain
// Usage: opencli domains-hsts <domain> [enable|disable]
// Author: Stefan Pejcic
// Created: 22.05.2025
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
	"fmt"
	"os"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-hsts",
		Desc:  "Manage HSTS for a domain",
		Usage: "opencli domains-hsts <domain> [enable|disable]",
		Run:   hsts,
	})
}

var (
	hstsTLSOpen   = regexp.MustCompile(`^[[:space:]]*tls[[:space:]]*\{`)
	hstsBlockEnd  = regexp.MustCompile(`^[[:space:]]*\}`)
	hstsTLSCustom = regexp.MustCompile(`^[[:space:]]*tls[[:space:]]+[^ {]`)
)

func hstsUsage() int {
	fmt.Println("Usage: opencli domains-hsts <domain> [options]")
	fmt.Println("")
	fmt.Println("Examples:")
	fmt.Println("  opencli domains-hsts pejcic.rs          - Check HSTS status for domain pejcic.rs")
	fmt.Println("  opencli domains-hsts pejcic.rs enable   - Enable HSTS for domain pejcic.rs")
	fmt.Println("  opencli domains-hsts pejcic.rs disable  - Disable HSTS for domain pejcic.rs")
	return 1
}

func hasHSTS(file string) bool {
	return strings.Contains(strings.ToLower(conf.Read(file)), "strict-transport-security")
}

func hstsAwk(content string) string {
	block := []string{"", "  # HSTS", "  header {", `    Strict-Transport-Security "max-age=2592000; preload"`, "  }"}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if content == "" {
		lines = nil
	}
	var out []string
	inTLS := false
	for _, l := range lines {
		out = append(out, l)
		if hstsTLSOpen.MatchString(l) {
			inTLS = true
		} else if inTLS && hstsBlockEnd.MatchString(l) {
			out = append(out, block...)
			inTLS = false
		} else if hstsTLSCustom.MatchString(l) {
			out = append(out, block...)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

func hsts(args []string) int {
	domain, action := cli.Arg(args, 0), cli.Arg(args, 1)
	if domain == "" {
		return hstsUsage()
	}
	file := "/etc/openpanel/caddy/domains/" + domain + ".conf"

	if action == "" {
		if !conf.IsFile(file) {
			fmt.Println("Domain not found!")
			return 1
		}
		if hasHSTS(file) {
			fmt.Println("Strict-Transport-Security header is set for domain " + domain)
		} else {
			fmt.Println("Strict-Transport-Security header is NOT set for domain " + domain)
		}
		return 0
	}

	if action != "enable" && action != "disable" {
		fmt.Println("Invalid action: " + action)
		return hstsUsage()
	}
	if !conf.IsFile(file) {
		fmt.Println("Domain not found!")
		return 1
	}
	changed := true
	if action == "enable" {
		if hasHSTS(file) {
			fmt.Println("HSTS already enabled for " + domain)
			changed = false
		} else {
			tmp := file + ".HSTS.tmp"
			if os.WriteFile(tmp, []byte(hstsAwk(conf.Read(file))), 0o644) == nil {
				os.Rename(tmp, file)
			}
			fmt.Println("HSTS enabled for " + domain)
		}
	} else {
		// sed '/# HSTS/,+3d'
		conf.SedFile(file, func(lines []string) []string {
			var out []string
			skip := 0
			for _, l := range lines {
				if skip > 0 {
					skip--
					continue
				}
				if strings.Contains(l, "# HSTS") {
					skip = 3
					continue
				}
				out = append(out, l)
			}
			return out
		})
		fmt.Println("HSTS disabled for " + domain)
	}
	if changed {
		sh.C("podman", "exec", "caddy", "sh", "-c", "caddy validate && caddy reload").Quiet().Background()
	}
	sh.Opencli("sentinel", "--action=domains_hsts", "--title=HSTS "+action+"d for domain "+domain, "--message=HSTS status has been changed to:'"+action+"d' for domain: '"+domain+"'.").Quiet().Background()
	return 0
}
