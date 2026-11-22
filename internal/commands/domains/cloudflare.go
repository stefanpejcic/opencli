// ##############################################################################
// Script Name: internal/commands/domains/cloudflare.go
// Description: Enable/disable/check Cloudflare-only access to domains.
// Usage: opencli domains-cloudflare <enable|disable|status> <DOMAIN_NAME [DOMAIN_NAME...]|--all> [-y]
// Author: Stefan Pejcic
// Created: 30.09.2026
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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-cloudflare",
		Desc:  "Enable/disable/check Cloudflare-only access to domains.",
		Usage: "opencli domains-cloudflare <enable|disable|status> <DOMAIN_NAME [DOMAIN_NAME...]|--all> [-y]",
		Run:   cloudflare,
	})
}

const (
	cfRed         = "\033[1;31m"
	cfGreen       = "\033[1;32m"
	cfNC          = "\033[0m"
	cfTemplateDir = "/etc/openpanel/caddy/templates"
	cfOutput      = cfTemplateDir + "/cloudflare.only"
	cfDomainDir   = "/etc/openpanel/caddy/domains"
	cfDomainTpl   = cfTemplateDir + "/domain.conf"
)

var (
	cfImportRe  = regexp.MustCompile(`^[[:space:]]*import[[:space:]]+cloudflare-only[[:space:]]*$`)
	cfUsedRe    = regexp.MustCompile(`^[[:space:]]*import[[:space:]]+cloudflare-only([[:space:]]|$)`)
	cfServerRe2 = regexp.MustCompile(`^[[:space:]]*https?:\/\/.*\{[[:space:]]*$`)
	cfIPv4Re    = regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}$|^[0-9a-fA-F:]*:[0-9a-fA-F:]+$`)
	cfBlankRe   = regexp.MustCompile(`^[[:space:]]*$`)
)

func cloudflare(args []string) int {
	action := cli.Arg(args, 0)
	var rest []string
	if len(args) > 0 {
		rest = args[1:]
	}
	var targets []string
	autoYes := ""
	for _, a := range rest {
		if a == "-y" {
			autoYes = "-y"
		} else {
			targets = append(targets, a)
		}
	}
	target := ""
	if len(targets) > 0 {
		target = targets[0]
	}

	if action != "enable" && action != "disable" && action != "status" && action != "" {
		fmt.Println("Usage:")
		fmt.Println("  opencli domains-cloudflare")
		fmt.Println("  opencli domains-cloudflare enable <domain [domain...]|--all> [-y]")
		fmt.Println("  opencli domains-cloudflare disable <domain [domain...]|--all> [-y]")
		fmt.Println("  opencli domains-cloudflare status [domain|--all]")
		return 1
	}

	if action == "enable" || action == "disable" {
		if target == "" {
			fmt.Println("ERROR: Target is required.")
			fmt.Printf("Usage: opencli domains-cloudflare %s <domain [domain...]|--all> [-y]\n", action)
			return 1
		}
		for _, t := range targets {
			if t == "--all" && len(targets) > 1 {
				fmt.Println("ERROR: --all can't be combined with domain names.")
				return 1
			}
			if t != "--all" && strings.HasPrefix(t, "-") {
				fmt.Println("ERROR: Invalid target: " + t)
				fmt.Printf("Usage: opencli domains-cloudflare %s <domain [domain...]|--all> [-y]\n", action)
				return 1
			}
		}
	}

	if action == "status" && target == "" {
		target = "--all"
	}

	switch action {
	case "status":
		return cfStatus(target)
	case "enable", "disable":
		if target == "--all" {
			return cfAll(action, autoYes)
		}
		return cfDomains(action, targets)
	}

	// no action, refresh the template when some domain uses it
	used := 0
	files, _ := filepath.Glob(cfDomainDir + "/*.conf")
	for _, f := range files {
		if cfEnabled(f) {
			used++
		}
	}
	if used == 0 {
		fmt.Println("Cloudflare-only template is not in use. Skipping.")
		return 0
	}
	fmt.Printf("Cloudflare-only setting is used by %d domains.\n", used)
	cfUpdateTemplate()
	return 0
}

func cfEnabled(file string) bool {
	for _, l := range conf.Lines(file) {
		if cfUsedRe.MatchString(l) {
			return true
		}
	}
	return false
}

func cfStrip(lines []string) []string {
	var out []string
	for _, l := range lines {
		if !cfImportRe.MatchString(l) {
			out = append(out, l)
		}
	}
	return out
}

func cfPatch(file string) {
	conf.SedFile(file, cfStrip)
	var b strings.Builder
	for _, l := range conf.Lines(file) {
		b.WriteString(l + "\n")
		if cfServerRe2.MatchString(l) {
			b.WriteString("  import cloudflare-only\n")
		}
	}
	tmp := file + ".tmp"
	os.WriteFile(tmp, []byte(b.String()), 0o644)
	os.Rename(tmp, file)
	fmt.Println("Enabled: " + file)
}

func cfRemove(file string) {
	conf.SedFile(file, cfStrip)
	fmt.Println("Disabled: " + file)
}

func cfReload(mode string) bool {
	fmt.Println("Reloading Caddy to apply the setting...")
	var c *sh.Cmd
	if mode == "restart" {
		c = sh.C("podman", "restart", "caddy")
	} else {
		c = sh.C("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile")
	}
	out, err := c.Combined()
	if err == nil {
		fmt.Println("SUCCESS: Caddy reloaded successfully.")
		return true
	}
	fmt.Println("WARNING: Failed to reload Caddy.")
	if out != "" {
		fmt.Println(out)
	}
	return false
}

func cfStatus(target string) int {
	if target == "--all" {
		files, _ := filepath.Glob(cfDomainDir + "/*.conf")
		if len(files) == 0 {
			fmt.Println("No existing domain configs found.")
			return 0
		}
		fmt.Println("Cloudflare-only status for all domains:")
		fmt.Println("----------------------------------------")
		// ((count++)) returns 1 on the first domain and set -e ends the script there
		name := strings.TrimSuffix(filepath.Base(files[0]), ".conf")
		if cfEnabled(files[0]) {
			fmt.Printf("  %s: %sENABLED%s\n", name, cfGreen, cfNC)
		} else {
			fmt.Printf("  %s: %sDISABLED%s\n", name, cfRed, cfNC)
		}
		return 1
	}
	if !domainNameRe.MatchString(target) || strings.Contains(target, "..") {
		fmt.Println("ERROR: Invalid domain name: " + target)
		return 1
	}
	file := cfDomainDir + "/" + target + ".conf"
	if !conf.IsFile(file) {
		fmt.Println("ERROR: Domain config not found: " + file)
		return 1
	}
	if cfEnabled(file) {
		fmt.Printf("Cloudflare-only mode for %s is %sENABLED%s.\n", target, cfGreen, cfNC)
	} else {
		fmt.Printf("Cloudflare-only mode for %s is %sDISABLED%s.\n", target, cfRed, cfNC)
	}
	return 0
}

func cfAll(action, autoYes string) int {
	if autoYes != "-y" {
		if action == "enable" {
			fmt.Printf("WARNING: This will %sENABLE%s Cloudflare-only mode globally (for all current & new domains).\n", cfGreen, cfNC)
			fmt.Println("Ensure all domains are proxied via Cloudflare, or they will return a 403 error.")
		} else {
			fmt.Printf("WARNING: This will %sDISABLE%s Cloudflare-only mode globally (for all current & new domains).\n", cfRed, cfNC)
			fmt.Println("Direct server traffic will be permitted for all domains.")
		}
		if isTTY(os.Stdin) {
			fmt.Fprint(os.Stderr, "Proceed? (y/N) [15s]: ")
		}
		confirm := readLineTimeout(15 * time.Second)
		fmt.Println("")
		if confirm != "y" && confirm != "Y" {
			fmt.Println("Operation cancelled or timed out.")
			return 1
		}
	}

	files, _ := filepath.Glob(cfDomainDir + "/*.conf")
	if len(files) > 0 {
		for _, f := range files {
			if action == "enable" {
				cfPatch(f)
			} else {
				cfRemove(f)
			}
		}
		fmt.Printf("Total domains %sd: %d\n", action, len(files))
	} else {
		fmt.Println("No existing domain configs found.")
	}

	// only --all modifies the template.
	if !conf.IsFile(cfDomainTpl) {
		fmt.Println("ERROR: Domain template not found: " + cfDomainTpl)
		return 1
	}
	if action == "enable" {
		cfPatch(cfDomainTpl)
		cfUpdateTemplate()
	}
	if !cfReload("restart") {
		return 1
	}
	return 0
}

func cfDomains(action string, targets []string) int {
	failed := 0
	changed := false
	templateChecked := false
	for _, t := range targets {
		if !domainNameRe.MatchString(t) || strings.Contains(t, "..") {
			fmt.Println("ERROR: Invalid domain name: " + t)
			failed = 1
			continue
		}
		file := cfDomainDir + "/" + t + ".conf"
		if !conf.IsFile(file) {
			fmt.Println("ERROR: Domain config not found: " + file)
			failed = 1
			continue
		}
		if action == "enable" {
			if !templateChecked {
				templateChecked = true
				if !conf.IsFile(cfOutput) {
					fmt.Println("Cloudflare-only template missing. Generating it before enabling...")
					cfUpdateTemplate()
				} else if tpl := conf.Read(cfOutput); !strings.Contains(tpl, "route @not_cloudflare") || !strings.Contains(tpl, "openpanel api") || !strings.Contains(tpl, "server ip") {
					// old snippets used a plain respond that never blocked, or lack the openpanel api ip or the server ip
					fmt.Println("Cloudflare-only template is outdated. Regenerating it before enabling...")
					cfUpdateTemplate()
				}
			}
			cfPatch(file)
		} else {
			// keep cloudflare.only, the Caddyfile imports it unconditionally
			cfRemove(file)
		}
		changed = true
	}
	if changed && !cfReload("reload") {
		failed = 1
	}
	return failed
}

// cfFetch is curl -fsSL, a failure ends the script with curl's exit code since set -e is on
func cfFetch(url string) string {
	resp, err := http.Get(url)
	if err != nil {
		code, msg := 1, err.Error()
		var dns *net.DNSError
		if errors.As(err, &dns) {
			code, msg = 6, "Could not resolve host: "+dns.Name
		}
		fmt.Fprintf(os.Stderr, "curl: (%d) %s\n", code, msg)
		cli.Exit(code)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "curl: (22) The requested URL returned error: %d\n", resp.StatusCode)
		cli.Exit(22)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "curl: (18) %s\n", err)
		cli.Exit(18)
	}
	return strings.TrimRight(string(b), "\n")
}

func nonBlank(s string) []string {
	var res []string
	for _, l := range strings.Split(s, "\n") {
		if !cfBlankRe.MatchString(l) {
			res = append(res, l)
		}
	}
	return res
}

func cfUpdateTemplate() {
	fmt.Println("Updating list of Cloudflare IP ranges...")

	raw4 := cfFetch("https://www.cloudflare.com/ips-v4")
	raw6 := cfFetch("https://www.cloudflare.com/ips-v6")
	if raw4 == "" || raw6 == "" {
		fmt.Println("ERROR: Failed to retrieve Cloudflare IP ranges.")
		cli.Exit(1)
	}
	v4, v6 := nonBlank(raw4), nonBlank(raw6)

	fmt.Printf("Retrieved %d IPv4 Cloudflare ranges:\n", len(v4))
	for _, ip := range v4 {
		fmt.Println("  " + ip)
	}
	fmt.Printf("Retrieved %d IPv6 Cloudflare ranges:\n", len(v6))
	for _, ip := range v6 {
		fmt.Println("  " + ip)
	}

	// screenshots and temporary links services fetch sites from the api.openpanel.com box
	addrs, err := net.LookupIP("api.openpanel.com")
	if err != nil {
		// getent failing under pipefail ends the bash script here
		cli.Exit(2)
	}
	seen := map[string]bool{}
	var opIPs []string
	for _, a := range addrs {
		if s := a.String(); !seen[s] {
			seen[s] = true
			opIPs = append(opIPs, s)
		}
	}
	sort.Strings(opIPs)
	if len(opIPs) == 0 {
		opIPs = []string{"185.119.90.100"}
	}
	fmt.Println("Allowing OpenPanel API IPs:")
	for _, ip := range opIPs {
		fmt.Println("  " + ip)
	}

	// server's own ips so wp-cron, loopback and other local requests to its sites still work
	body, ok := curlGet("https://ip.openpanel.com", 3*time.Second)
	if !ok {
		body, _ = curlGet("https://ifconfig.me/ip", 3*time.Second)
	}
	cand := strings.Split(body, "\n")
	cand = append(cand, "")
	for _, f := range strings.Fields(sh.C("hostname", "-I").NoErr().Str()) {
		cand = append(cand, f)
	}
	seen = map[string]bool{}
	var serverIPs []string
	for _, l := range cand {
		if cfIPv4Re.MatchString(l) && !strings.HasPrefix(l, "fe80") && !seen[l] {
			seen[l] = true
			serverIPs = append(serverIPs, l)
		}
	}
	sort.Strings(serverIPs)
	if len(serverIPs) > 0 {
		fmt.Println("Allowing server IPs:")
		for _, ip := range serverIPs {
			fmt.Println("  " + ip)
		}
	}

	var ips []string
	seen = map[string]bool{}
	for _, group := range [][]string{{raw4}, {raw6}, opIPs, serverIPs} {
		for _, chunk := range group {
			for _, l := range nonBlank(chunk) {
				if !seen[l] {
					seen[l] = true
					ips = append(ips, l)
				}
			}
		}
	}

	var b strings.Builder
	b.WriteString("# opencli domains-cloudflare\n")
	b.WriteString("(cloudflare-only) {\n")
	b.WriteString("    # cloudflare ranges + openpanel api (screenshots, temporary links) + server ip\n")
	b.WriteString("    @not_cloudflare {\n")
	b.WriteString("        not remote_ip \\\n")
	for i, ip := range ips {
		if i == len(ips)-1 {
			fmt.Fprintf(&b, "            %s\n", ip)
		} else {
			fmt.Fprintf(&b, "            %s \\\n", ip)
		}
	}
	b.WriteString("    }\n")
	b.WriteString("\n")
	// own route so it runs before the domain's route/reverse_proxy, plain respond is ordered after it
	b.WriteString("    route @not_cloudflare {\n")
	b.WriteString("        respond \"Access allowed only through Cloudflare\" 403\n")
	b.WriteString("    }\n")
	b.WriteString("}\n")

	tmp := cfOutput + ".tmp"
	os.WriteFile(tmp, []byte(b.String()), 0o644)
	os.Rename(tmp, cfOutput)

	fmt.Println("Updated: " + cfOutput)
}
