// ##############################################################################
// Script Name: internal/commands/waf/waf.go
// Description: Manage CorazaWAF
// Usage: opencli waf <status|enable|disable|domain|user|plugins|tags|ids|update|stats|count> [options]
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

package waf

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/requirement"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "waf",
		Desc:  "Manage CorazaWAF",
		Usage: "opencli waf <status|enable|disable|domain|user|plugins|tags|ids|update|stats|count> [options]",
		Run:   waf,
	})
}

const (
	domainsDir  = "/etc/openpanel/caddy/domains"
	crsDir      = "/etc/openpanel/caddy/coreruleset"
	customImage = `CADDY_IMAGE="openpanel/caddy-coraza"`
	auditLog    = "/var/log/caddy/coraza_audit.log"
)

func usage() {
	fmt.Print(`Usage: opencli waf <command> [options]

Commands:
  status                                       Check if CorazaWAF is enabled for new domains and users.
  enable                                       Enable module and use WAF on new domains.
  disable                                      Disable module and dont use WAF on new domains.
  domain                                       Check if CorazaWAF is enabled for a domain.
  domain DOMAIN_NAME enable                    Enable CorazaWAF for a domain.
  domain DOMAIN_NAME disable                   Disable CorazaWAF for a domain.
  domain DOMAIN_NAME monitor                   Monitor only: log attacks for a domain without blocking them.
  domain DOMAIN_NAME plugins                   Show protection level and app profiles (CRS plugins) for a domain.
  user USERNAME                                Check if CorazaWAF is enabled for a user's domains.
  user USERNAME enable                         Enable CorazaWAF for all current and new domains of a user.
  user USERNAME disable                        Disable CorazaWAF for all current and new domains of a user.
  plugins                                      List installed CRS plugins and how many domains use each.
  plugins install                              Download missing CRS plugins.
  tags                                         Display all tags from enabled sets.
  ids                                          Display all rule IDs from enabled sets.
  update                                       Update OWASP CRS and CRS plugins.
  update log                                   Show OWASP CRS update history (git log).
  stats <country|agent|hourly|ip|request|path> Display top requests by country, ip, path, etc.
  count                                         Display total number of audit log records.

Examples:
  opencli waf status
  opencli waf enable
  opencli waf disable -y
  opencli waf domain pcx3.com
  opencli waf domain pcx3.com enable
  opencli waf domain pcx3.com disable
  opencli waf domain pcx3.com monitor
  opencli waf domain pcx3.com plugins
  opencli waf user stefan
  opencli waf user stefan disable
  opencli waf plugins
  opencli waf plugins install
  opencli waf stats ip
  opencli waf stats hourly
`)
	cli.Exit(1)
}

func domainFile(domain string) string { return domainsDir + "/" + domain + ".conf" }

var engineRe = map[string]*regexp.Regexp{
	"On":            regexp.MustCompile(`(?i)^[[:space:]]*SecRuleEngine[[:space:]]+On`),
	"Off":           regexp.MustCompile(`(?i)^[[:space:]]*SecRuleEngine[[:space:]]+Off`),
	"DetectionOnly": regexp.MustCompile(`(?i)^[[:space:]]*SecRuleEngine[[:space:]]+DetectionOnly`),
}

func anyLine(lines []string, re *regexp.Regexp) bool {
	for _, l := range lines {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

func checkDomain(domain string) {
	file := domainFile(domain)
	if !conf.IsFile(file) {
		fmt.Println("Domain not found!")
		cli.Exit(1)
	}
	lines := conf.Lines(file)
	switch {
	case anyLine(lines, engineRe["On"]):
		fmt.Printf("SecRuleEngine is set to On for domain %s\n", domain)
	case anyLine(lines, engineRe["Off"]):
		fmt.Printf("SecRuleEngine is set to Off for domain %s\n", domain)
	case anyLine(lines, engineRe["DetectionOnly"]):
		fmt.Printf("SecRuleEngine is set to DetectionOnly (monitor only) for domain %s\n", domain)
	default:
		fmt.Printf("SecRuleEngine is not set for domain %s\n", domain)
	}
}

// grep -qi "waf" on the whole openpanel.config
func wafModule() bool {
	return strings.Contains(strings.ToLower(conf.Read(conf.Panel)), "waf")
}

func checkStatus() {
	image := false
	for _, l := range conf.Lines(conf.RootEnv) {
		if strings.HasPrefix(l, customImage) {
			image = true
		}
	}
	module := wafModule()
	switch {
	case image && module:
		fmt.Println("CorazaWAF is ENABLED")
	case image:
		fmt.Println("CorazaWAF is DISABLED: 'waf' module is not enabled in OpenAdmin > Settings > Modules.)")
	case module:
		fmt.Println("CorazaWAF is DISABLED: 'waf' module is enabled, but Caddy is not using " + customImage)
	default:
		fmt.Println("CorazaWAF is DISABLED")
	}
}

func reloadCaddyNow() {
	sh.C("podman", "exec", "caddy", "sh", "-c", "caddy validate && caddy reload").Quiet().Background()
}

func sentinel(action, title, message string) {
	sh.Opencli("sentinel", "--action="+action, "--title="+title, "--message="+message).Quiet().Background()
}

var engineAny = regexp.MustCompile(`SecRuleEngine .*`)

// sed -i "s/SecRuleEngine .*/SecRuleEngine $value/"
func setEngine(file, value string) bool {
	return conf.SedFile(file, func(lines []string) []string {
		for i, l := range lines {
			if loc := engineAny.FindStringIndex(l); loc != nil {
				lines[i] = l[:loc[0]] + "SecRuleEngine " + value
			}
		}
		return lines
	})
}

func setDomain(domain, action string) {
	file := domainFile(domain)
	if !conf.IsFile(file) {
		fmt.Println("Domain not found!")
		cli.Exit(1)
	}
	value := map[string]string{"enable": "On", "disable": "Off", "monitor": "DetectionOnly"}[action]
	if value == "" {
		fmt.Printf("Invalid action: %s. Use 'enable', 'disable' or 'monitor'.\n", action)
		cli.Exit(1)
	}
	if setEngine(file, value) {
		reloadCaddyNow()
		fmt.Printf("SecRuleEngine %s is now set for domain %s\n", value, domain)
	} else {
		fmt.Printf("Failed setting SecRuleEngine %s - please contact Administrator.\n", value)
		cli.Exit(1)
	}
	done := action + "d"
	if action == "monitor" {
		done = "set to monitor only"
	}
	sentinel("waf_domain", "WAF "+action+" for domain", "CorazaWAF has been "+done+" for domain '"+domain+"'.")
}

// userContext returns id and context, suspended accounts included
func userContext(username string) (string, string) {
	db.Require()
	esc := db.Escape(username)
	row, _ := db.Cmd("SELECT id, server FROM users WHERE username = '"+esc+"' OR (username REGEXP '^SUSPENDED_[0-9]{14}_' AND SUBSTRING(username, 26) = '"+esc+"') ORDER BY username = '"+esc+"' DESC LIMIT 1;", "-N", "-B").NoErr().Out()
	f := strings.Fields(strings.SplitN(row, "\n", 2)[0])
	id, ctx := "", ""
	if len(f) > 0 {
		id = f[0]
	}
	if len(f) > 1 {
		ctx = strings.Join(f[1:], " ")
	}
	if id == "" || ctx == "" {
		fmt.Printf("User %s not found!\n", username)
		cli.Exit(1)
	}
	return id, ctx
}

func setUser(username, action string) {
	id, ctx := userContext(username)
	flag := "/home/" + ctx + "/waf.disabled"
	out, _ := db.Cmd("SELECT domain_url FROM domains WHERE user_id = '"+id+"';", "-N", "-B").NoErr().Out()
	domains := strings.Fields(out)

	var value string
	switch action {
	case "":
		if conf.IsFile(flag) {
			fmt.Printf("CorazaWAF is disabled for new domains of user %s\n", username)
		} else {
			fmt.Printf("CorazaWAF is enabled for new domains of user %s\n", username)
		}
		for _, d := range domains {
			checkDomain(d)
		}
		return
	case "enable":
		os.Remove(flag)
		value = "On"
		// without the module domains-add sets Off anyway, so leave existing domains alone
		if !wafModule() {
			fmt.Printf("CorazaWAF will be used for new domains of user %s once the 'waf' module is enabled.\n", username)
			return
		}
	case "disable":
		touch(flag)
		value = "Off"
	default:
		fmt.Printf("Invalid action: %s. Use 'enable' or 'disable'.\n", action)
		cli.Exit(1)
	}
	for _, d := range domains {
		// suspended domains keep the real vhost in suspended_domains, update both
		for _, f := range []string{domainFile(d), "/etc/openpanel/caddy/suspended_domains/" + d + ".conf"} {
			if conf.IsFile(f) {
				setEngine(f, value)
			}
		}
		fmt.Printf("- SecRuleEngine %s is now set for domain %s\n", value, d)
	}
	if out != "" {
		reloadCaddyNow()
	}
	fmt.Printf("CorazaWAF is now %sd for all current and new domains of user %s\n", action, username)
	sentinel("waf_domain", "WAF "+action+" for user", "CorazaWAF has been "+action+"d for all domains of user '"+username+"'.")
}

func touch(p string) {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "touch: cannot touch '%s': No such file or directory\n", p)
		return
	}
	f.Close()
}

var ctxRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// domains of users that turned WAF off with 'opencli waf user USERNAME disable'
func wafDisabledDomains() []string {
	flags, _ := filepath.Glob("/home/*/waf.disabled")
	var out []string
	for _, f := range flags {
		if !conf.IsFile(f) {
			continue
		}
		ctx := filepath.Base(filepath.Dir(f))
		if !ctxRe.MatchString(ctx) {
			continue
		}
		rows, _ := db.Cmd("SELECT d.domain_url FROM domains d JOIN users u ON u.id = d.user_id WHERE u.server = '"+ctx+"';", "-N", "-B").NoErr().Out()
		if rows != "" {
			out = append(out, strings.Split(rows, "\n")...)
		}
	}
	return out
}

func waf(args []string) int {
	requirement.Command("git")
	a1, a2, a3 := cli.Arg(args, 0), cli.Arg(args, 1), cli.Arg(args, 2)
	switch a1 {
	case "status":
		checkStatus()
	case "domain":
		if a2 == "" {
			fmt.Println("Domain name is required.")
			usage()
		}
		switch a3 {
		case "enable", "disable", "monitor":
			setDomain(a2, a3)
		case "plugins":
			listDomainPlugins(a2)
		case "":
			checkDomain(a2)
		default:
			fmt.Println("Invalid action for domain: " + a3)
			usage()
		}
	case "user":
		if a2 == "" {
			fmt.Println("Username is required.")
			usage()
		}
		setUser(a2, a3)
	case "enable":
		return enable()
	case "disable":
		disable(a2)
		return 0
	case "update":
		switch a2 {
		case "":
			updateRules()
		case "log":
			if os.Chdir(crsDir) == nil {
				sh.C("git", "log", "--oneline").Run()
			} else {
				fmt.Fprintf(os.Stderr, "bash: line 1: cd: %s/: No such file or directory\n", crsDir)
			}
		default:
			fmt.Println("Invalid action, available: opencli waf update and opencli waf update log")
			return 1
		}
	case "plugins":
		switch a2 {
		case "install":
			installPlugins()
		case "":
			listPlugins()
		default:
			fmt.Println("Invalid action, available: opencli waf plugins and opencli waf plugins install")
			return 1
		}
	case "stats":
		stats(a2)
	case "count":
		count()
	case "tags":
		listTags()
	case "ids":
		listIDs()
	case "help":
		usage()
	default:
		fmt.Println("Invalid option: " + a1)
		usage()
	}
	return 0
}
