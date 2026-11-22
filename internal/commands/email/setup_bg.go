// ##############################################################################
// Script Name: internal/commands/email/setup_bg.go
// Description: Background steps of email-setup that bash ran with nohup.
// Usage: opencli email-setup-bg (internal)
// Author: Stefan Pejcic
// Created: 21.11.2026
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
	"math/rand"
	"os"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

// the bash script detached these steps with nohup so they outlive the command
func setupBg(args []string) int {
	switch cli.Arg(args, 0) {
	case "start-mailserver":
		time.Sleep(15 * time.Second)
		if podman.CanExec(container) {
			return 0
		}
		podman.ForceRemove(container, "openadmin_roundcube")
		sh.C("timeout", "120", "podman-compose", "up", "-d", "mailserver", "roundcube").Dir(mailDir).Run()
	case "chown":
		uid, domain, user := cli.Arg(args, 1), cli.Arg(args, 2), cli.Arg(args, 3)
		script := fmt.Sprintf("chown -R \"%s:%s\" \"/var/mail/%s/%s\"", uid, uid, domain, user)
		sh.C("podman", "exec", container, "bash", "-c", script).Timeout(300 * time.Second).Quiet().Run()
	case "welcome":
		suWelcome(cli.Arg(args, 1), cli.Arg(args, 2))
	}
	return 0
}

// drops a setup guide straight into the new mailbox with doveadm, nothing goes out over smtp
func suWelcome(email, opUID string) {
	domain := email
	if i := strings.Index(email, "@"); i >= 0 {
		domain = email[i+1:]
	}
	const template = "/etc/openpanel/openpanel/custom_code/welcome.html"
	if !conf.IsFile(template) {
		return
	}
	// wait for dovecot to pick up the account with the right uid, otherwise the mailbox gets created as the wrong owner
	uid := ""
	for i := 0; i < 90; i++ {
		uid, _ = sh.C("podman", "exec", container, "doveadm", "user", "-f", "uid", email).NoErr().Out()
		if uid != "" && (opUID == "" || uid == opUID) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if uid == "" {
		return
	}

	// same host and ports as /emails/info in the panel
	imap, smtp, security := "143", "587", "No encryption"
	host := sh.Opencli("domain").NoErr().Str()
	if conf.IsFile("/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/"+host+"/"+host+".crt") || conf.IsFile("/etc/openpanel/caddy/ssl/custom/"+host+"/"+host+".crt") {
		imap, smtp, security = "993", "465", "SSL/TLS"
	}
	webmailURL := ""
	for _, l := range recLines(conf.Read("/etc/openpanel/caddy/redirects.conf")) {
		if strings.HasPrefix(l, "redir @webmail") {
			if f := awkFields(l); len(f) > 2 {
				webmailURL = f[2]
			}
			break
		}
	}
	if webmailURL == "" {
		webmailURL = "http://" + host + ":8080/"
	}

	b, _ := os.ReadFile(template)
	body := strings.TrimRight(string(b), "\n")
	body = strings.NewReplacer("{{EMAIL}}", email, "{{DOMAIN}}", domain, "{{MAIL_HOST}}", host, "{{WEBMAIL_URL}}", webmailURL, "{{IMAP_PORT}}", imap, "{{SMTP_PORT}}", smtp, "{{SECURITY}}", security).Replace(body)

	now := time.Now()
	var msg strings.Builder
	msg.WriteString("From: Mail Setup <postmaster@" + domain + ">\r\n")
	msg.WriteString("To: <" + email + ">\r\n")
	msg.WriteString("Subject: Set up " + email + " on your devices\r\n")
	msg.WriteString("Date: " + now.Format("Mon, 02 Jan 2006 15:04:05 -0700") + "\r\n")
	msg.WriteString(fmt.Sprintf("Message-ID: <welcome.%d.%d@%s>\r\n", now.Unix(), rand.Intn(32768), domain))
	msg.WriteString("MIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	msg.WriteString(body + "\r\n")
	sh.C("podman", "exec", "-i", container, "doveadm", "save", "-u", email).Stdin(msg.String()).Quiet().Run()
}
