// ##############################################################################
// Script Name: internal/commands/user/block_ip.go
// Description: View, add, or remove blocked IP addresses for a user's domains.
// Usage: opencli user-block_ip <username> [--list='ip_here another_ip' | --delete-all]
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

package user

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-block_ip",
		Desc:  "View, add, or remove blocked IP addresses for a user's domains.",
		Usage: "opencli user-block_ip <username> [--list='ip_here another_ip' | --delete-all]",
		Run:   blockIP,
	})
}

var (
	blockIPRe   = regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}(/([0-9]|[1-2][0-9]|3[0-2]))?$`)
	blockListRe = regexp.MustCompile(`remote_ip\s+([0-9./]+)`)
)

func blockValid(ip string) bool {
	if !blockIPRe.MatchString(ip) {
		return false
	}
	only, _, _ := strings.Cut(ip, "/")
	for _, o := range strings.Split(only, ".") {
		// leading zero is octal in bash arithmetic, three digits can't pass 255 that way
		if len(o) > 1 && o[0] == '0' {
			continue
		}
		if n, _ := strconv.Atoi(o); n > 255 {
			return false
		}
	}
	return true
}

func blockIP(args []string) int {
	if len(args) == 0 || len(args) > 2 {
		fmt.Println("Usage: opencli user-block_ip <username> [--list='IP_ADDRESS ANOTHER_IP' | --delete-all]")
		fmt.Println("")
		fmt.Println("Examples:")
		fmt.Println("opencli user-block_ip <username>")
		fmt.Println("opencli user-block_ip <username> --list='11.22.33.44 124.64.23.0/24'")
		fmt.Println("opencli user-block_ip <username> --delete-all")
		return 1
	}
	db.Require()
	username := args[0]
	deleteAll, showIPs, list := false, false, ""
	second := cli.Arg(args, 1)
	if second == "" {
		showIPs = true
	} else if v, ok := strings.CutPrefix(second, "--list="); ok {
		list = v
	} else if second == "--delete-all" {
		deleteAll = true
	} else {
		fmt.Println("Unknown argument: " + second)
	}

	id, ctx := userIDCtx(username)
	if id == "" {
		cli.Fail("FATAL ERROR: user %s does not exist.", username)
	}
	if ctx == "" {
		cli.Fail("FATAL ERROR: docker context for user %s does not exist.", username)
	}
	denyFile := "/etc/openpanel/caddy/deny/" + ctx + ".ips"

	if deleteAll {
		os.WriteFile(denyFile, nil, 0o644)
		reloadCaddyBg()
		return 0
	}
	if showIPs {
		if conf.IsFile(denyFile) {
			for _, l := range conf.Lines(denyFile) {
				for _, m := range blockListRe.FindAllStringSubmatch(l, -1) {
					fmt.Println(m[1])
				}
			}
		}
		return 0
	}
	if list == "" {
		return 0
	}

	var valid []string
	for _, ip := range strings.FieldsFunc(strings.ReplaceAll(list, "\n", " "), func(r rune) bool { return r == ' ' }) {
		if blockValid(ip) {
			fmt.Printf("Blocking IP %s for user %s...\n", ip, username)
			valid = append(valid, ip)
		} else {
			fmt.Println("Warning: Invalid IP or CIDR skipped: " + ip)
		}
	}
	os.MkdirAll(filepath.Dir(denyFile), 0o755) // <1.7.47
	var b strings.Builder
	b.WriteString("@blocked {\n")
	for _, ip := range valid {
		b.WriteString("    remote_ip " + ip + "\n")
	}
	b.WriteString("}\n\nrespond @blocked \"Access Denied\" 403\n")
	os.WriteFile(denyFile, []byte(b.String()), 0o644)

	domains, _ := sh.Opencli("domains-user", username).Out()
	hasDomains := false
	imp, impErr := regexp.Compile("import " + denyFile)
	for _, d := range strings.Split(domains, "\n") {
		if !strings.Contains(d, ".") {
			continue
		}
		domainFile := caddyVhostDir + "/" + d + ".conf"
		if !fileExists(domainFile) {
			fmt.Println("Warning: No caddy file for domain " + d)
			continue
		}
		hasDomains = true
		content := conf.Read(domainFile)
		applied := strings.Contains(content, "import "+denyFile)
		if impErr == nil {
			applied = false
			for _, l := range strings.Split(content, "\n") {
				if imp.MatchString(l) {
					applied = true
					break
				}
			}
		}
		if applied {
			fmt.Printf("Blocklist already applied to domain '%s' - skipping.\n", d)
			continue
		}
		lines := strings.Split(content, "\n")
		var out []string
		for i, l := range lines {
			out = append(out, l)
			if strings.Contains(l, "route {") && !(i == len(lines)-1 && l == "") {
				out = append(out, "    # ip-blocker", "    import "+denyFile)
			}
		}
		conf.WriteFileKeep(domainFile, strings.Join(out, "\n"), false)
		fmt.Printf("Blocklist applied for domain '%s'\n", d)
	}
	if hasDomains {
		reloadCaddyBg()
	}
	return 0
}
