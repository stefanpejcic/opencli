// ##############################################################################
// Script Name: internal/commands/email/webmail.go
// Description: Display or update the domain used for accessing webmail.
// Usage: opencli email-webmail [--debug] [domain <domain>]
// Author: Stefan Pejcic
// Created: 18.08.2024
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

package email

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "email-webmail",
		Desc:  "Display or update the domain used for accessing webmail.",
		Usage: "opencli email-webmail [--debug] [domain <domain>]",
		Docs:  "https://docs.openpanel.com",
		Run:   webmail,
	})
}

const (
	wmProxyFile = "/etc/openpanel/caddy/redirects.conf"
	wmDomainDir = "/etc/openpanel/caddy/domains/"
	wmPort      = "8080"
)

var (
	wmDomainRe = regexp.MustCompile(`^([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$`)
	wmIPRe     = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$`)
	wmRedirRe  = regexp.MustCompile(`^redir @webmail https?://[^ ]+`)
	wmTCPRe    = regexp.MustCompile(`TCP_IN = "(.*)"`)
)

func wmUsage() {
	fmt.Println("Usage: opencli email-webmail [--debug] [domain <domain>]")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  opencli email-webmail                            # Display webmail domain.")
	fmt.Println("  opencli email-webmail domain webmail.example.com # Set webmail.example.com as domain.")
	fmt.Println("")
	cli.Exit(1)
}

func webmail(args []string) int {
	if len(args) > 2 {
		wmUsage()
	}
	debug := false
	// todo: 8080 should be disabled and instead allow domain proxy only
	requireEnterprise(msgUnlimited, 1)

	for len(args) > 0 {
		switch args[0] {
		case "--debug":
			fmt.Println("")
			fmt.Println("----------------- DISPLAY DEBUG INFORMATION ------------------")
			fmt.Println("")
			debug = true
		case "domain":
			args = args[1:]
			if len(args) == 0 || args[0] == "" {
				fmt.Println("Error: No domain specified after 'domain'.")
				fmt.Println("Usage: opencli email-webmail domain mail.example.com")
				return 1
			}
			wmUpdate(args[0], debug)
		default:
			fmt.Printf("Invalid option: %s\n", args[0])
			wmUsage()
		}
		if len(args) > 0 {
			args = args[1:]
		}
	}

	if !conf.IsFile(wmProxyFile) {
		fmt.Printf("Configuration file not found at %s\n", wmProxyFile)
		return 1
	}
	// grab the domain out of the /webmail redir line
	var doms []string
	for _, l := range recLines(conf.Read(wmProxyFile)) {
		if !strings.HasPrefix(l, "redir @webmail") {
			continue
		}
		d := ""
		if f := awkFields(l); len(f) > 2 {
			d = f[2]
		}
		d = strings.Replace(d, "https://", "", 1)
		d = strings.Replace(d, "http://", "", 1)
		if i := strings.Index(d, ":"); i >= 0 {
			d = d[:i]
		}
		doms = append(doms, d)
	}
	domain := strings.TrimRight(strings.Join(doms, "\n"), "\n")
	if domain == "" {
		fmt.Println("No domain configured for /webmail")
		return 0
	}
	fmt.Println(domain)

	if debug {
		fmt.Println("")
		fmt.Println("----------------- OPENING PORT 8080 ON FIREWALL ------------------")
	}
	if sh.Has("csf") {
		wmOpenCSF(wmPort, debug)
	} else if debug {
		fmt.Println("Warning: CSF is not installed. In order for Webmail to work, make sure port 8080 is opened on external firewall..")
	}
	return 0
}

func wmOpenCSF(port string, debug bool) {
	const csf = "/etc/csf/csf.conf"
	b, err := os.ReadFile(csf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "grep: %s: No such file or directory\n", csf)
	}
	opened := regexp.MustCompile(`TCP_IN = .*` + port).MatchString(string(b))
	if opened {
		if debug {
			fmt.Printf("Port %s is already open in CSF.\n", port)
		}
		return
	}
	if debug {
		fmt.Println("")
		fmt.Println("Opening port on Sentinel Firewall")
		fmt.Println("")
	}
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "sed: can't read %s: No such file or directory\n", csf)
		}
	} else {
		conf.WriteFileKeep(csf, wmTCPRe.ReplaceAllString(string(b), `TCP_IN = "${1},`+port+`"`), false)
	}
	if debug {
		fmt.Println("")
	}
}

func wmUpdate(newDomain string, debug bool) {
	os.MkdirAll(wmDomainDir, 0o755)
	if !wmDomainRe.MatchString(newDomain) {
		// log_error is not defined in the bash script
		fmt.Fprintln(os.Stderr, "/usr/local/opencli/email/webmail.sh: line 89: log_error: command not found")
		wmUsage()
	}
	if !conf.IsFile(wmProxyFile) {
		cli.Fail("Error: Caddy file not found at %s", wmProxyFile)
	}
	const caddyfile = "/etc/openpanel/caddy/Caddyfile"
	re := regexp.MustCompile(newDomain)
	// not used by system domains
	if conf.IsFile(caddyfile) && re.MatchString(conf.Read(caddyfile)) {
		cli.Fail("Error: Rejecting to save domain '%s' as it is already used for another system service.", newDomain)
	}
	// and not in user domains
	used := false
	filepath.WalkDir(wmDomainDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && re.MatchString(conf.Read(p)) {
			used = true
			return filepath.SkipAll
		}
		return nil
	})
	if used {
		cli.Fail("Error: Aborting - domain '%s' is already associated with an existing user. Remove the domain from that user before continuing.", newDomain)
	}

	proxy := conf.Read(wmProxyFile)
	has := false
	for _, l := range strings.Split(proxy, "\n") {
		if strings.HasPrefix(l, "redir @webmail") {
			has = true
		}
	}
	if !has {
		cli.Fail("Error: No redir @webmail line found in %s", wmProxyFile)
	}
	if debug {
		fmt.Printf("Updating webmail redirect domain to: %s\n", newDomain)
	}
	proto := "https"
	if wmIPRe.MatchString(newDomain) {
		proto = "http"
	}
	lines := strings.SplitAfter(proxy, "\n")
	for i, l := range lines {
		if loc := wmRedirRe.FindStringIndex(l); loc != nil {
			lines[i] = "redir @webmail " + proto + "://" + newDomain + l[loc[1]:]
		}
	}
	conf.WriteFileKeep(wmProxyFile, strings.Join(lines, ""), false)

	// so caddy can generate le
	touch := wmDomainDir + newDomain + ".conf"
	if f, err := os.OpenFile(touch, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
	}
	os.Chtimes(touch, nowT(), nowT())

	if conf.IsFile(caddyfile) {
		cf := conf.Read(caddyfile)
		if strings.Contains(cf, "# START WEBMAIL DOMAIN #") {
			if debug {
				fmt.Printf("Updating Caddyfile webmail block to: %s\n", newDomain)
			}
			out := wmCaddyBlock(cf, newDomain)
			conf.WriteInPlace(caddyfile, out)
		} else {
			fmt.Printf("Warning: Webmail domain block not found in %s\n", caddyfile)
		}
	} else {
		fmt.Printf("Warning: %s does not exist\n", caddyfile)
	}
	fmt.Printf("Webmail domain updated to %s://%s\n", proto, newDomain)
	podman.CaddyReload()
	cli.Exit(0)
}

// wmCaddyBlock is sed '/# START WEBMAIL DOMAIN #/,/# END WEBMAIL DOMAIN #/c\...'
func wmCaddyBlock(cf, domain string) string {
	repl := "# START WEBMAIL DOMAIN #\n" + domain + " {\n    reverse_proxy localhost:8080\n}\n# END WEBMAIL DOMAIN #\n"
	var out strings.Builder
	in := false
	for _, l := range strings.SplitAfter(cf, "\n") {
		if l == "" {
			continue
		}
		t := strings.TrimSuffix(l, "\n")
		if !in {
			if strings.Contains(t, "# START WEBMAIL DOMAIN #") {
				in = true
				continue
			}
			out.WriteString(l)
			continue
		}
		if strings.Contains(t, "# END WEBMAIL DOMAIN #") {
			in = false
			out.WriteString(repl)
		}
	}
	return out.String()
}
