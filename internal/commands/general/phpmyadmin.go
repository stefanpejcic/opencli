// ##############################################################################
// Script Name: internal/commands/general/phpmyadmin.go
// Description: View and set domain/ip for phpmyadmin access.
// Usage: opencli phpmyadmin [set <domain_name> | ip]
// Author: Stefan Pejcic
// Created: 30.03.2026
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

package general

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "phpmyadmin",
		Desc:  "View and set domain/ip for phpmyadmin access.",
		Usage: "opencli phpmyadmin [set <domain_name> | ip]",
		Run:   phpmyadmin,
	})
}

const (
	pmaStart = "# START PHPMYADMIN DOMAIN #"
	pmaEnd   = "# END PHPMYADMIN DOMAIN #"
)

var (
	pmaDomainRe = regexp.MustCompile(`^([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$`)
	pmaURIRe    = regexp.MustCompile(`^PMA_ABSOLUTE_URI=.*`)
	pmaPortRe   = regexp.MustCompile(`^.*"([0-9]+):.*$`)
)

// grep -A1 "START PHPMYADMIN DOMAIN" | tail -n1 | awk '{print $1}'
func pmaGetDomain() {
	b, err := os.ReadFile(conf.Caddyfile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "grep: %s: No such file or directory\n", conf.Caddyfile)
		return
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	last := -1
	for i, l := range lines {
		if strings.Contains(l, "START PHPMYADMIN DOMAIN") {
			last = i
		}
	}
	if last < 0 {
		return
	}
	if last+1 < len(lines) {
		last++
	}
	f := strings.Fields(lines[last])
	if len(f) > 0 {
		fmt.Println(f[0])
	} else {
		fmt.Println()
	}
}

func pmaHasBlock() bool {
	s := conf.Read(conf.Caddyfile)
	return strings.Contains(s, pmaStart) && strings.Contains(s, pmaEnd)
}

func pmaRemoveBlock() {
	if !pmaHasBlock() {
		return
	}
	sedFile(conf.Caddyfile, func(lines []string) []string {
		var out []string
		in := false
		for _, l := range lines {
			if !in && strings.Contains(l, pmaStart) {
				in = true
				continue
			}
			if in {
				if strings.Contains(l, pmaEnd) {
					in = false
				}
				continue
			}
			out = append(out, l)
		}
		return out
	})
}

// sets PMA_ABSOLUTE_URI in one .env, the same s|^PMA_ABSOLUTE_URI=.*|...| sed did
func pmaSetURI(file, value string) {
	sedFile(file, func(lines []string) []string {
		for i, l := range lines {
			lines[i] = pmaURIRe.ReplaceAllLiteralString(l, "PMA_ABSOLUTE_URI="+value)
		}
		return lines
	})
}

func pmaExistingUsers(domain string) {
	fmt.Println("Updating PMA_ABSOLUTE_URI for all existing users...")
	files, _ := filepath.Glob("/home/*/.env")
	if len(files) == 0 {
		files = []string{"/home/*/.env"}
	}
	for _, file := range files {
		context := filepath.Base(filepath.Dir(file))
		if domain == "disable" {
			pmaSetURI(file, `""`)
		} else {
			var ports []string
			for _, l := range conf.Lines(file) {
				if strings.HasPrefix(l, "PMA_PORT=") {
					if m := pmaPortRe.FindStringSubmatch(l); m != nil {
						l = m[1]
					}
					ports = append(ports, l)
				}
			}
			if !conf.Exists(file) {
				fmt.Fprintf(os.Stderr, "grep: %s: No such file or directory\n", file)
			}
			port := strings.TrimRight(strings.Join(ports, "\n"), "\n")
			if port != "" {
				pmaSetURI(file, `"https://`+domain+"/"+port+`/"`)
			}
		}
		svcs, _ := podman.ComposeUser(context, "ps", "--services").NoErr().Out()
		found := false
		for _, s := range strings.Split(svcs, "\n") {
			if s == "phpmyadmin" {
				fmt.Println(s)
				found = true
			}
		}
		if found {
			fmt.Println("Restarting running phpmyadmin service for podman context: " + context)
			podman.ComposeUser(context, "down", "phpmyadmin").Quiet().Run()
			podman.ComposeUser(context, "up", "-d", "phpmyadmin").Quiet().Run()
		}
	}
}

func pmaNewUsers(domain string) {
	fmt.Println("Updating PMA_ABSOLUTE_URI in template for new users..")
	file := "/etc/openpanel/docker/compose/1.0/.env"
	if domain == "disable" {
		pmaSetURI(file, `""`)
	} else {
		pmaSetURI(file, `"https://`+domain+`/{PMA_PORT}/"`)
	}
	// the "ip" path passes "disable" here, so it touches disable.conf like the script always did
	touch("/etc/openpanel/caddy/domains/" + domain + ".conf")
	podman.CaddyReload()
}

func touch(p string) {
	if f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
		f.Close()
	} else {
		fmt.Fprintf(os.Stderr, "touch: cannot touch '%s': No such file or directory\n", p)
		return
	}
	now := time.Now()
	os.Chtimes(p, now, now)
}

func pmaSetDomain(domain string) {
	if domain == "" {
		fmt.Println("Usage: opencli phpmyadmin set <domain>")
		cli.Exit(1)
	}
	if domain == "ip" {
		pmaRemoveBlock()
		pmaExistingUsers("disable")
		pmaNewUsers("disable")
		fmt.Println("Done")
		return
	}
	if !pmaDomainRe.MatchString(domain) {
		fmt.Println("ERROR: Invalid domain format. Please provide a valid domain.")
		cli.Exit(1)
	}
	owner, _ := sh.Opencli("domains-whoowns", domain).Out()
	if strings.Contains(owner, "not found in the database.") {
		if conf.IsFile(conf.Caddyfile) {
			re := regexp.MustCompile(`^\s*` + domain + `\s*\{`)
			for _, l := range conf.Lines(conf.Caddyfile) {
				if re.MatchString(l) {
					fmt.Printf("ERROR: %s is already configured for another system service and can not be set for phpMyAdmin access.\n", domain)
					cli.Exit(1)
				}
			}
		}
	} else {
		fmt.Printf("ERROR: Domain %s is already in used by an OpenPanel account and can not be set for phpMyAdmin access.\n", domain)
		cli.Exit(1)
	}

	block := pmaStart + "\n" + domain + `: {
    @dynamicPort path_regexp port ^/(\d+)(/.*)?$
    handle @dynamicPort {
        uri strip_prefix /{http.regexp.port.1}
        reverse_proxy localhost:{http.regexp.port.1}
    }

    handle {
        respond "Please use your unique phpmyadmin path shown on OpenPanel > MySQL > phpMyAdmin page."
    }
}
` + pmaEnd

	fmt.Printf("Creating proxy for the %s domain..\n", domain)
	if pmaHasBlock() {
		// the c\ text isn't backslash continued so sed rejects this script and leaves the file as is, kept as the script had it
		sh.C("sed", "-i", "/"+pmaStart+"/,/"+pmaEnd+"/c\\\n"+block+"\n", conf.Caddyfile).Run()
		fmt.Println("Updated existing PHPMYADMIN domain block.")
	} else {
		if f, err := os.OpenFile(conf.Caddyfile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
			f.WriteString("\n" + block + "\n")
			f.Close()
		}
		fmt.Println("Added new PHPMYADMIN domain block.")
	}
	pmaExistingUsers(domain)
	pmaNewUsers(domain)
	fmt.Println("Done")
}

func phpmyadmin(args []string) int {
	switch {
	case len(args) == 0:
		pmaGetDomain()
	case args[0] == "set":
		pmaSetDomain(cli.Arg(args, 1))
	default:
		fmt.Println("Usage:")
		fmt.Println("  opencli phpmyadmin               # Show current phpMyAdmin link")
		fmt.Println("  opencli phpmyadmin set <domain>  # Set https://DOMAIN/PORT/ for phpMyAdmin access.")
		fmt.Println("  opencli phpmyadmin set ip        # Set http://IP:PORT       for phpMyAdmin access.")
	}
	return 0
}
