// ##############################################################################
// Script Name: internal/commands/websites/secure.go
// Description: WP Manager security rules for domain.
// Usage: opencli websites-secure <DOMAIN> [--rules='RULE1 RULE2' | --disable-all | --list-active-rules]
// Author: Stefan Pejcic
// Created: 13.03.2026
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

package websites

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "websites-secure",
		Desc:  "WP Manager security rules for domain.",
		Usage: "opencli websites-secure <DOMAIN> [--rules='RULE1 RULE2' | --disable-all | --list-active-rules]",
		Run:   secure,
	})
}

const (
	secVhostDir = "/etc/openpanel/caddy/domains"
	secRules    = "/etc/openpanel/caddy/templates/wp.rules"
)

var (
	secRuleName   = regexp.MustCompile(`^\(([a-z0-9_]+)`)
	secImportDel  = regexp.MustCompile(`^[[:space:]]*import[[:space:]]+wp_manager_[a-z0-9_]+$`)
	secImportList = regexp.MustCompile(`^[[:space:]]*import[[:space:]]+wp_manager_[a-z0-9_]+[[:space:]]*$`)
	secMarker     = regexp.MustCompile(`^[[:space:]]*# modsecurity`)
)

func secUsage() int {
	fmt.Println("Usage:")
	fmt.Println("opencli websites-secure <domain>")
	fmt.Println("opencli websites-secure --list-available-rules")
	fmt.Println("opencli websites-secure <domain> --rules='rule1 rule2'")
	fmt.Println("opencli websites-secure <domain> --disable-all")
	fmt.Println("opencli websites-secure <domain> --list-active-rules")
	return 1
}

// secAvailable is grep -oP '^\(\K[a-z0-9_]+' on the rules file
func secAvailable() []string {
	var out []string
	for _, l := range conf.Lines(secRules) {
		if m := secRuleName.FindStringSubmatch(l); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

// secAwkLines splits like awk does, the last line gets a newline anyway
func secAwkLines(file string) []string {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" && len(b) == 0 {
		return nil
	}
	return strings.Split(s, "\n")
}

// secReplace is `awk ... > $(mktemp); mv`, so the file ends up with mktemp's 0600
func secReplace(file string, lines []string) {
	tmp, err := os.CreateTemp("", "tmp.")
	if err != nil {
		return
	}
	for _, l := range lines {
		tmp.WriteString(l + "\n")
	}
	tmp.Close()
	if os.Rename(tmp.Name(), file) != nil {
		sh.Quiet("mv", tmp.Name(), file)
	}
}

func secEmpty(file string) {
	var out []string
	for _, l := range secAwkLines(file) {
		if secMarker.MatchString(l) {
			out = append(out, l)
			continue
		}
		if secImportDel.MatchString(l) {
			continue
		}
		out = append(out, l)
	}
	secReplace(file, out)
}

func secActive(file string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range secAwkLines(file) {
		if secImportList.MatchString(l) {
			f := strings.Fields(l)
			if !seen[f[1]] {
				seen[f[1]] = true
				out = append(out, f[1])
			}
		}
	}
	return out
}

func secReload() {
	sh.C("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").Quiet().Background()
}

func secure(args []string) int {
	if len(args) < 1 {
		return secUsage()
	}

	if !conf.IsFile(secRules) {
		// for <1.7.47
		caddyfile := "/etc/openpanel/caddy/Caddyfile"
		ruleLine := "import " + secRules
		b, err := os.ReadFile(caddyfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "grep: %s: No such file or directory\n", caddyfile)
		}
		found := false
		for _, l := range strings.Split(string(b), "\n") {
			if l == ruleLine {
				found = true
			}
		}
		if !found {
			if err != nil {
				fmt.Fprintf(os.Stderr, "sed: can't read %s: No such file or directory\n", caddyfile)
			} else {
				var out []string
				for _, l := range conf.Lines(caddyfile) {
					if strings.Contains(l, "# import all sites") {
						out = append(out, ruleLine)
					}
					out = append(out, l)
				}
				s := strings.Join(out, "\n")
				if strings.HasSuffix(string(b), "\n") && len(out) > 0 {
					s += "\n"
				}
				conf.WriteInPlace(caddyfile, s)
			}
		}

		if resp, err := http.Get("https://raw.githubusercontent.com/stefanpejcic/openpanel-configuration/refs/heads/main/caddy/templates/wp.rules"); err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			os.WriteFile(secRules, body, 0o666&^umask())
		}
		if !conf.IsFile(secRules) {
			fmt.Println("Error: rules file not found: " + secRules)
			return 1
		}
	}

	rules := ""
	deleteAll, listActive := false, false
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--rules="):
			rules = a[len("--rules="):]
		case a == "--disable-all":
			deleteAll = true
		case a == "--list-active-rules":
			listActive = true
		case a == "--list-available-rules":
			for _, r := range secAvailable() {
				fmt.Println(r)
			}
			return 0
		}
	}

	domain := args[0]
	if !strings.Contains(domain, ".") {
		fmt.Printf("ERROR: '%s' is not a valid domain name.\n", domain)
		return 1
	}

	file := secVhostDir + "/" + domain + ".conf"
	if !conf.IsFile(file) {
		fmt.Printf("Error: domain '%s' does not exist.\n", domain)
		return 1
	}
	if !strings.Contains(conf.Read(file), "# modsecurity") {
		fmt.Printf("Error: '# modsecurity' marker not found in %s\n", file)
		fmt.Println("Aborting to avoid corrupting config.")
		return 1
	}

	if deleteAll {
		secEmpty(file)
		secReload()
		fmt.Println("All rules removed for " + domain)
		return 0
	}

	if listActive {
		active := secActive(file)
		if len(active) > 0 {
			fmt.Println("Active rules for " + domain + ":")
			fmt.Println(strings.Join(active, "\n"))
		} else {
			fmt.Println("No active rules for " + domain + ".")
		}
		return 0
	}

	if rules == "" {
		active := secActive(file)
		if len(active) > 0 {
			fmt.Println("Status for " + domain + ": rules enabled")
			fmt.Println(strings.Join(active, "\n"))
		} else {
			fmt.Println("Status for " + domain + ": no rules enabled")
		}
		return 0
	}

	var valid []string
	for _, r := range secAvailable() {
		if strings.HasPrefix(r, "wp_manager_") {
			valid = append(valid, r)
		}
	}
	validText := strings.Join(valid, "\n") + "\n"
	filtered := ""
	for _, rule := range strings.Fields(rules) {
		if sh.C("grep", "-qw", "--", rule).Stdin(validText).Quiet().OK() {
			filtered += rule + " "
		} else {
			fmt.Printf("Removing invalid rule: '%s'\n", rule)
		}
	}
	rules = text.Xargs(filtered)

	if rules == "" {
		fmt.Println("ERROR: no valid rules provided.")
		return 0
	}
	secEmpty(file)
	var out []string
	for _, l := range secAwkLines(file) {
		if strings.Contains(l, "# modsecurity") {
			for _, r := range strings.Fields(rules) {
				out = append(out, "    import "+r)
			}
		}
		out = append(out, l)
	}
	secReplace(file, out)
	secReload()
	fmt.Println("Rules updated for " + domain)
	return 0
}
