// ##############################################################################
// Script Name: internal/commands/general/faq.go
// Description: Display answers to most frequently asked questions.
// Usage: opencli faq
// Author: Stefan Pejcic
// Created: 20.05.2024
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
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "faq",
		Desc:  "Display answers to most frequently asked questions.",
		Usage: "opencli faq",
		Run:   faq,
	})
}

const (
	faqGreen  = "\033[0;32m"
	faqBlue   = "\033[0;34m"
	faqRed    = "\033[0;31m"
	faqPurple = "\033[0;35m"
	faqNC     = "\033[0m"
)

var (
	faqSchemeRe = regexp.MustCompile(`^https?://`)
	faqBraceRe  = regexp.MustCompile(`[[:space:]]*\{`)
	faqPortRe   = regexp.MustCompile(`reverse_proxy[ \t]+\S+:([0-9]+)`)
	faqIPRe     = regexp.MustCompile(`\b([0-9]{1,3}\.){3}[0-9]{1,3}\b`)
)

func cutAt(s string, sep string) string {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i]
	}
	return s
}

func faqLinks() (userURL, adminURL string) {
	userPort := sh.Opencli("port").NoErr().Str()
	lines := conf.Lines(conf.Caddyfile)

	// 1. domain and later will check for ssl
	block := between(lines, "# START HOSTNAME DOMAIN #", "# END HOSTNAME DOMAIN #")
	domain := subFirst(faqBraceRe, firstContentLine(block), "")
	domain = text.Xargs(faqSchemeRe.ReplaceAllString(domain, ""))
	domain = cutAt(cutAt(domain, ":"), ",")

	adminPort := ""
	for _, l := range block {
		if m := faqPortRe.FindStringSubmatch(l); m != nil {
			adminPort = m[1]
			break
		}
	}

	// 2. ip and later will check for ssl
	if domain == "" || domain == "example.net" {
		ipBlock := between(lines, "# START HOSTNAME IP #", "# END HOSTNAME IP #")
		ip := text.Xargs(strings.Join(faqIPRe.FindAllString(firstContentLine(ipBlock), -1), "\n"))
		ip = cutAt(cutAt(ip, ":"), ",")
		// 3. IP and non-ssl
		if ip == "" {
			ip = publicIP()
			return "http://" + ip + ":" + userPort + "/", "http://" + ip + ":" + adminPort + "/"
		}
		domain = ip
	}

	proto := "http"
	if hasCert(domain) {
		proto = "https"
	}
	return proto + "://" + domain + ":" + userPort + "/", proto + "://" + domain + ":" + adminPort + "/"
}

func faq(args []string) int {
	userURL, adminURL := faqLinks()
	g, r, nc := faqGreen, faqRed, faqNC
	questions := []string{
		"What is the login link for admin panel?",
		"What is the login link for user panel?",
		"How to restart OpenAdmin or OpenPanel services?",
		"How to reset admin password?",
		"How to create new admin account?",
		"How to list admin accounts?",
		"How to check OpenPanel version?",
		"How to update OpenPanel?",
		"How to disable automatic updates?",
		"Where are the logs?",
		"How to enable detailed logs?",
	}
	answers := []string{
		"LINK: " + g + adminURL + nc,
		"LINK: " + g + userURL + nc,
		"OpenPanel: " + r + "podman restart openpanel" + nc + "\n- OpenAdmin: " + r + "service admin restart" + nc,
		"execute command " + g + "opencli admin password USERNAME NEW_PASSWORD" + nc,
		"execute command " + g + "opencli admin new USERNAME PASSWORD" + nc,
		"execute command " + g + "opencli admin list" + nc,
		"execute command " + g + "opencli --version" + nc,
		"execute command " + g + "opencli update --force" + nc,
		"execute command " + g + "opencli config update autoupdate off" + nc,
		"User panel errors:      " + g + "podman logs -f openpanel" + nc +
			"\n- User panel access log:  " + g + "/var/log/openpanel/user/access.log" + nc +
			"\n- Admin panel errors:     " + g + "/var/log/openpanel/admin/error.log" + nc +
			"\n- Admin panel access log: " + g + "/var/log/openpanel/admin/access.log" + nc +
			"\n- Admin panel API access: " + g + "/var/log/openpanel/admin/api.log" + nc +
			"\n- Admin panel logins:     " + g + "/var/log/openpanel/admin/login.log" + nc +
			"\n- Admin panel alerts:     " + g + "/var/log/openpanel/admin/notifications.log" + nc +
			"\n- Admin panel crons:      " + g + "/var/log/openpanel/admin/cron.log" + nc,
		g + "opencli config update dev_mode on" + nc,
	}
	fmt.Print("Frequently Asked Questions\n\n")
	for i, q := range questions {
		fmt.Printf("%s%d.%s %s\n", faqPurple, i+1, faqNC, q)
		fmt.Println("- " + answers[i])
		fmt.Println(faqBlue + "------------------------------------------------------------" + faqNC)
	}
	return 0
}
