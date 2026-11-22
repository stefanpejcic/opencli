// ##############################################################################
// Script Name: internal/commands/domains/suspend.go
// Description: Suspend a domain name
// Usage: opencli domains-suspend <DOMAIN-NAME> [--comment="<COMMENT>"] [--by=admin|user] [--template=<NAME>]
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
		Name:  "domains-suspend",
		Desc:  "Suspend a domain name",
		Usage: "opencli domains-suspend <DOMAIN-NAME> [--comment=\"<COMMENT>\"] [--by=admin|user] [--template=<NAME>]",
		Run:   suspend,
	})
}

var (
	domainNameRe    = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	templateNameRe  = regexp.MustCompile(`^[a-z0-9_-]+$`)
	templateTitleRe = regexp.MustCompile(`^<!-- name: (.*) -->$`)
	suspendedRe     = regexp.MustCompile(`(?m)^# suspended_by:|suspended_domain\.html`)
	suspUserRe      = regexp.MustCompile(`suspended_user.html`)
)

// trCntrl is tr -d '[:cntrl:]'
func trCntrl(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}

func suspend(args []string) int {
	if len(args) < 1 || len(args) > 4 {
		fmt.Println("Usage: opencli domains-suspend <domain_name> [--comment='reason'] [--by=admin|user] [--template=name]")
		return 1
	}
	domain := args[0]
	// goes straight into the caddy file path
	if !domainNameRe.MatchString(domain) || strings.Contains(domain, "..") {
		fmt.Println("ERROR: Invalid domain name: " + domain)
		return 1
	}
	comment, by, template, reason := "", "user", "", ""
	for _, arg := range args[1:] {
		switch {
		case strings.HasPrefix(arg, "--comment="):
			comment = strings.TrimPrefix(arg, "--comment=")
		case strings.HasPrefix(arg, "--by="):
			by = strings.TrimPrefix(arg, "--by=")
		case strings.HasPrefix(arg, "--template="):
			template = strings.TrimPrefix(arg, "--template=")
		default:
			fmt.Println("Unknown option: " + arg)
			return 1
		}
	}
	if by != "admin" && by != "user" {
		fmt.Println("ERROR: --by must be admin or user")
		return 1
	}
	// comment is written into the caddy file, so newlines would let callers inject directives
	comment = trCntrl(comment)

	vhost := "/etc/openpanel/caddy/domains/" + domain + ".conf"
	suspendedDir := "/etc/openpanel/caddy/suspended_domains/"
	confTemplate := "/etc/openpanel/caddy/templates/suspended.conf"
	page := "/suspended_domain.html"

	if template != "" {
		if !templateNameRe.MatchString(template) {
			fmt.Println("ERROR: invalid template name: " + template)
			return 1
		}
		tfile := "/etc/openpanel/caddy/templates/suspended/" + template + ".html"
		if !conf.IsFile(tfile) {
			fmt.Printf("ERROR: suspension template '%s' does not exist\n", template)
			return 1
		}
		page = "/suspended/" + template + ".html"
		// display name sits in the first line as <!-- name: ... -->, users see it as the reason
		first := strings.SplitN(conf.Read(tfile), "\n", 2)[0]
		if m := templateTitleRe.FindStringSubmatch(first); m != nil {
			reason = trCntrl(m[1])
		}
		if reason == "" {
			reason = template
		}
	}

	if !conf.IsFile(vhost) {
		fmt.Printf("ERROR: vhost file for domain %s does not exist\n", domain)
		return 1
	}
	current := conf.Read(vhost)
	// copying the account-suspended vhost would overwrite the real one in suspended_dir
	if suspUserRe.MatchString(current) {
		fmt.Printf("ERROR: account owning %s is suspended, unsuspend it first\n", domain)
		return 1
	}

	// the live caddy file says whether it's suspended, a copy left in suspended_dir alone doesn't
	if suspendedRe.MatchString(current) {
		currentBy := ""
		for _, l := range strings.Split(current, "\n") {
			if strings.HasPrefix(l, "# suspended_by: ") {
				currentBy = strings.TrimPrefix(l, "# suspended_by: ")
				break
			}
		}
		// admin can take over or update a suspension, users can't touch an admin one
		if by != "admin" || (currentBy == "admin" && template == "" && comment == "") {
			fmt.Println("Domain is already suspended.")
			return 0
		}
	} else {
		os.MkdirAll(suspendedDir, 0o755)
		if st, err := os.Stat(vhost); err == nil {
			os.WriteFile(suspendedDir+domain+".conf", []byte(current), st.Mode().Perm()&^0o022)
		}
	}

	var b strings.Builder
	if comment != "" {
		fmt.Fprintf(&b, "# comment: %s\n", comment)
	}
	fmt.Fprintf(&b, "# suspended_by: %s\n", by)
	if reason != "" {
		fmt.Fprintf(&b, "# reason: %s\n", reason)
	}
	if tpl, err := os.ReadFile(confTemplate); err == nil {
		s := strings.ReplaceAll(string(tpl), "<DOMAIN_NAME>", domain)
		b.WriteString(strings.ReplaceAll(s, "/suspended_domain.html", page))
	} else {
		fmt.Fprintf(os.Stderr, "sed: can't read %s: No such file or directory\n", confTemplate)
	}
	conf.WriteInPlace(vhost, b.String())

	sh.C("podman", "exec", "caddy", "sh", "-c", "caddy validate && caddy reload").Quiet().Background()

	message := fmt.Sprintf("Domain name %s has been suspended by %s.", domain, by)
	if reason != "" {
		message += " Reason: " + reason + "."
	}
	if comment != "" {
		message += " Comment: " + comment
	}
	sh.Opencli("sentinel", "--action=domains_status", "--title=Domain name "+domain+" suspended", "--message="+message).Quiet().Background()

	fmt.Println("Domain suspended successfully.")
	return 0
}
