// ##############################################################################
// Script Name: internal/commands/domains/unsuspend.go
// Description: Unsuspend a domain name
// Usage: opencli domains-unsuspend <DOMAIN-NAME> [--by=admin|user]
// Author: Stefan Pejcic
// Created: 04.11.2024
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
		Name:  "domains-unsuspend",
		Desc:  "Unsuspend a domain name",
		Usage: "opencli domains-unsuspend <DOMAIN-NAME> [--by=admin|user]",
		Run:   unsuspend,
	})
}

var suspendedByAdminRe = regexp.MustCompile(`(?m)^# suspended_by: admin`)

func unsuspend(args []string) int {
	if len(args) < 1 || len(args) > 2 {
		fmt.Println("Usage: opencli domains-unsuspend <domain_name> [--by=admin|user]")
		return 1
	}
	domain := args[0]
	// goes straight into the caddy file path
	if !domainNameRe.MatchString(domain) || strings.Contains(domain, "..") {
		fmt.Println("ERROR: Invalid domain name: " + domain)
		return 1
	}
	by := "user"
	if a := cli.Arg(args, 1); a != "" {
		switch a {
		case "--by=admin", "--by=user":
			by = strings.TrimPrefix(a, "--by=")
		default:
			fmt.Println("Unknown option: " + a)
			return 1
		}
	}

	vhost := "/etc/openpanel/caddy/domains/" + domain + ".conf"
	suspendedVhost := "/etc/openpanel/caddy/suspended_domains/" + domain + ".conf"

	if !conf.IsFile(suspendedVhost) {
		fmt.Printf("Domain %s is not suspended.\n", domain)
		return 0
	}
	current := conf.Read(vhost)
	// while the account is suspended that copy is the only real vhost, don't touch it
	if suspUserRe.MatchString(current) {
		fmt.Printf("ERROR: account owning %s is suspended, unsuspend it first\n", domain)
		return 1
	}
	// a copy left behind by an older unsuspend must not overwrite a live, unsuspended vhost
	if !suspendedRe.MatchString(current) {
		os.Remove(suspendedVhost)
		fmt.Printf("Domain %s is not suspended.\n", domain)
		return 0
	}
	if by != "admin" && suspendedByAdminRe.MatchString(current) {
		fmt.Printf("ERROR: Domain %s was suspended by the administrator and can only be unsuspended by them.\n", domain)
		return 1
	}

	// moved so the next suspend doesn't think it's still suspended
	os.Rename(suspendedVhost, vhost)

	sh.C("podman", "exec", "caddy", "sh", "-c", "caddy validate && caddy reload").Quiet().Background()

	sh.Opencli("sentinel", "--action=domains_status", "--title=Domain name "+domain+" unsuspended", "--message=Domain name "+domain+" has been unsuspended.").Quiet().Background()

	fmt.Println("Domain unsuspended successfully.")
	return 0
}
