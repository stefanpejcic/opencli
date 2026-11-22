// ##############################################################################
// Script Name: internal/commands/user/ip.go
// Description: Assign or remove dedicated IP to a user.
// Usage: opencli user-ip <USERNAME> <IP | DELETE> [-y] [--debug]
// Author: Stefan Pejcic
// Created: 23.11.2023
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

package user

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/ojson"
	"opencli/internal/redis"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-ip",
		Desc:  "Assign or remove dedicated IP to a user.",
		Usage: "opencli user-ip <USERNAME> <IP | DELETE> [-y] [--debug]",
		Run:   userIP,
	})
}

const (
	ipJSONBase = "/etc/openpanel/openpanel/core/users"
	ipZoneDir  = "/etc/bind/zones"
)

type ipCtx struct {
	username  string
	confirm   string
	debug     bool
	serverIP  string
	allowed   string
	curIPFile string
}

func (c *ipCtx) currentIP() string {
	if conf.IsFile(c.curIPFile) {
		return ipFromFile(c.curIPFile)
	}
	return c.serverIP
}

// jq -r '.ip'
func ipFromFile(path string) string {
	o, err := ojson.ParseObject(conf.Read(path))
	if err != nil {
		return ""
	}
	v, ok := o.Get("ip")
	if !ok || v == nil {
		return "null"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ojson.Compact(v)
}

func (c *ipCtx) deleteConfig() {
	if conf.IsFile(c.curIPFile) {
		os.Remove(c.curIPFile)
		fmt.Printf("IP configuration deleted for user %s.\n", c.username)
	}
}

func (c *ipCtx) checkValidity(ip string) {
	re, err := regexp.Compile(`(?:^|[^A-Za-z0-9_])(?:` + ip + `)(?:[^A-Za-z0-9_]|$)`)
	if err != nil || !re.MatchString(c.allowed) {
		cli.Fail("Error: The provided IP address is not allowed. Must be one of: %s", c.allowed)
	}
}

func (c *ipCtx) checkUsage(ip string) {
	entries, _ := os.ReadDir(ipJSONBase)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, user := range names {
		if user == c.username {
			continue
		}
		f := ipJSONBase + "/" + user + "/ip.json"
		if conf.IsFile(f) && ipFromFile(f) == ip && c.confirm != "-y" {
			fmt.Printf("Error: IP %s already assigned to user %s.\n", ip, user)
			ans := readPrompt("Are you sure you want to continue? (y/n): ")
			if ans != "y" {
				fmt.Println("Script aborted.")
				cli.Exit(1)
			}
		}
	}
}

var ipBindLine = regexp.MustCompile(`^[[:space:]]*bind `)

// sed keeps a missing final newline as it was
func splitLines(s string) ([]string, bool) {
	if s == "" {
		return nil, false
	}
	nl := strings.HasSuffix(s, "\n")
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n"), nl
}

func joinLines(lines []string, nl bool) string {
	if len(lines) == 0 {
		return ""
	}
	s := strings.Join(lines, "\n")
	if nl {
		s += "\n"
	}
	return s
}

func ipReadLines(file string) ([]string, bool, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		cli.Errf("sed: can't read %s: No such file or directory", file)
		return nil, false, false
	}
	l, nl := splitLines(string(b))
	return l, nl, true
}

// header lines are matched as a literal prefix, the sed n command eats the following line
func ipBlockEdit(file, header string, apply func(prev, next string) []string) {
	lines, nl, ok := ipReadLines(file)
	if !ok {
		return
	}
	var out []string
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], header) || i+1 >= len(lines) {
			out = append(out, lines[i])
			continue
		}
		out = append(out, apply(lines[i], lines[i+1])...)
		i++
	}
	conf.WriteFileKeep(file, joinLines(out, nl), false)
}

func ipUpdateBind(file, header, ip string) {
	lines, nl, ok := ipReadLines(file)
	if !ok {
		return
	}
	found := false
	for i := 0; i+1 < len(lines); i++ {
		if strings.HasPrefix(lines[i], header) {
			if ipBindLine.MatchString(lines[i+1]) {
				found = true
			}
			i++
		}
	}
	if found {
		ipBlockEdit(file, header, func(prev, next string) []string {
			if ipBindLine.MatchString(next) {
				return []string{prev, "    bind " + ip}
			}
			return []string{prev, next}
		})
		return
	}
	var out []string
	for _, l := range lines {
		out = append(out, l)
		if strings.HasPrefix(l, header) {
			out = append(out, "    bind "+ip)
		}
	}
	conf.WriteFileKeep(file, joinLines(out, nl), false)
}

func ipRemoveBind(file, header string) {
	ipBlockEdit(file, header, func(prev, next string) []string {
		if ipBindLine.MatchString(next) {
			return []string{prev}
		}
		return []string{prev, next}
	})
}

func (c *ipCtx) editDomainFiles(ip string) {
	domains, _ := sh.Opencli("domains-user", c.username).Out()
	cur := c.currentIP()
	caddyChanged, bindChanged := false, false
	for _, d := range strings.Fields(domains) {
		cf := caddyVhostDir + "/" + d + ".conf"
		if conf.IsFile(cf) {
			h1 := "http://" + d + ", http://*." + d + " {"
			h2 := "https://" + d + ", https://*." + d + " {"
			if ip == c.serverIP {
				// caddy binding the main ip opens a second listener that takes every connection to it, so other domains get an empty 200
				ipRemoveBind(cf, h1)
				ipRemoveBind(cf, h2)
			} else {
				ipUpdateBind(cf, h1, ip)
				ipUpdateBind(cf, h2, ip)
			}
			if c.debug {
				fmt.Printf("- Updated Caddy configuration for %s to %s\n", d, ip)
			}
			caddyChanged = true
		}
		zf := ipZoneDir + "/" + d + ".zone"
		if conf.IsFile(zf) {
			if cur != "" {
				if re, err := regexp.Compile(strings.ReplaceAll(regexp.QuoteMeta(cur), `\.`, ".")); err == nil {
					conf.WriteFileKeep(zf, re.ReplaceAllLiteralString(conf.Read(zf), ip), false)
				}
			}
			if c.debug {
				fmt.Printf("- Updated DNS zone file %s with IP %s\n", zf, ip)
			}
			bindChanged = true
		}
	}
	if caddyChanged {
		sh.C("podman", "exec", "caddy", "sh", "-c", "caddy validate && caddy reload").Quiet().Run()
		if c.debug {
			fmt.Println("- Reloaded webserver")
		}
	}
	if bindChanged {
		// rndc reload rereads the changed zone files, reconfig would only pick up new zones
		sh.C("podman", "exec", "openpanel_dns", "rndc", "reload").Quiet().Run()
		if c.debug {
			fmt.Println("- Reloaded DNS server")
		}
	}
}

func (c *ipCtx) dropCache() {
	// an IP change only invalidates the cached IP lookup (used on dashboard, temporary links, ftp and emails), not the rest of the app's cache
	redis.DropKey("openpanel_cache_get_cached_ip_for_user_or_public_ipv4:" + c.username)
}

func (c *ipCtx) done(ipToUse string) {
	sentinelBg("--action=user_ip", "--title=User account IP changed", "--message=IP address for user account '"+c.username+"' has been set to: '"+ipToUse+"'.")
	c.dropCache()
}

func userIP(args []string) int {
	c := &ipCtx{username: cli.Arg(args, 0), confirm: cli.Arg(args, 2), debug: cli.HasFlag(args, "--debug")}
	action := cli.Arg(args, 1)
	hn, _ := sh.C("hostname", "-I").NoErr().Raw()
	c.serverIP = text.Field(strings.TrimRight(hn, "\n"), 1)
	raw, _ := sh.C("hostname", "-I").Raw()
	var kept []string
	parts := strings.Split(strings.ReplaceAll(raw, " ", "\n"), "\n")
	if len(parts) > 0 {
		parts = parts[:len(parts)-1]
	}
	for _, p := range parts {
		if !strings.HasPrefix(p, "172.") {
			kept = append(kept, p)
		}
	}
	if len(kept) > 0 {
		c.allowed = strings.Join(kept, " ") + " "
	}
	if c.username == "" {
		fmt.Println("Usage: opencli user-ip <USERNAME> <ACTION> [ -y ] [--debug]")
		fmt.Println("")
		fmt.Println("Assign Dedicated IP to a user: opencli user-ip <USERNAME> <IP_ADDRESS> [ -y ] [--debug]")
		fmt.Println("Remove Dedicated IP from user: opencli user-ip <USERNAME> delete [ -y ] [--debug]")
		return 1
	}
	c.curIPFile = ipJSONBase + "/" + c.username + "/ip.json"

	var ipToUse, ip string
	if action == "delete" {
		c.deleteConfig()
		ipToUse = c.serverIP
	} else {
		ip = action
		if ip == "" {
			fmt.Println(c.currentIP())
			return 0
		}
		c.checkValidity(ip)
		if ip == c.serverIP {
			// the main ip is the shared one, not a dedicated ip
			action = "delete"
			c.deleteConfig()
		} else {
			c.checkUsage(ip)
		}
		ipToUse = ip
	}
	c.editDomainFiles(ipToUse)

	if action == "delete" {
		os.RemoveAll(c.curIPFile)
		fmt.Printf("IP successfully changed for user %s to shared IP address: %s\n", c.username, ipToUse)
		c.done(ipToUse)
		return 0
	}
	os.WriteFile(c.curIPFile, []byte("{ \"ip\": \""+ip+"\" }\n"), 0o644)
	if c.debug {
		fmt.Printf("Created IP file %s with IP %s\n", c.curIPFile, ip)
	}
	fmt.Printf("IP successfully changed for user %s to dedicated IP address: %s\n", c.username, ipToUse)
	c.done(ipToUse)
	return 0
}
