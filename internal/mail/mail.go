// ##############################################################################
// Script Name: internal/mail/mail.go
// Description: Sending emails through OpenAdmin's /send_email and the plan limit helpers around them.
// Usage: imported by command packages
// Author: Stefan Pejcic
// Created: 25.09.2026
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

package mail

import (
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/sh"
)

const usersDir = "/etc/openpanel/openpanel/core/users"

func token(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

var adminPortRe = regexp.MustCompile(`localhost:([0-9]+)`)

// AdminPort is the OpenAdmin port from the hostname block of the Caddyfile
func AdminPort() string {
	in := false
	for _, l := range conf.Lines(conf.Caddyfile) {
		if strings.Contains(l, "# START HOSTNAME DOMAIN #") {
			in = true
			continue
		}
		if strings.Contains(l, "# END HOSTNAME DOMAIN #") {
			in = false
		}
		if in {
			if m := adminPortRe.FindStringSubmatch(l); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// Proto is https when the panel domain has a letsencrypt or custom cert
func Proto(domain string) string {
	for _, dir := range []string{"/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/", "/etc/openpanel/caddy/ssl/custom/"} {
		base := dir + domain + "/" + domain
		if conf.IsFile(base+".crt") && conf.IsFile(base+".key") {
			return "https"
		}
	}
	return "http"
}

// Send emails one recipient through OpenAdmin, true only when it says the mail was sent
func Send(recipient, subject, body string, extra ...string) bool {
	if recipient == "" {
		return false
	}
	get := func(i int) string {
		if i < len(extra) {
			return extra[i]
		}
		return ""
	}
	tok := token(64)
	// the one-time token in openpanel.config proves the request is local
	lines := conf.Lines(conf.Panel)
	for i, l := range lines {
		if strings.HasPrefix(l, "mail_security_token=") {
			lines[i] = "mail_security_token=" + tok
		}
	}
	tmp := conf.Panel + ".tmp"
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	if os.WriteFile(tmp, []byte(content), 0o644) == nil {
		os.Rename(tmp, conf.Panel)
	}

	domain := sh.Opencli("domain").NoErr().Str()
	url := fmt.Sprintf("%s://%s:%s/send_email", Proto(domain), domain, AdminPort())

	var buf strings.Builder
	w := multipart.NewWriter(&buf)
	fields := [][2]string{{"transient", tok}, {"recipient", recipient}, {"subject", subject}, {"body", body},
		{"type", get(0)}, {"usage", get(1)}, {"upgrade_plan", get(2)}, {"upgrade_text", get(3)}, {"tips", get(4)}}
	for _, f := range fields {
		w.WriteField(f[0], f[1])
	}
	w.Close()
	req, err := http.NewRequest("POST", url, strings.NewReader(buf.String()))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if conf.Get(conf.AdminINI, "basic_auth") == "yes" {
		u, _ := conf.Raw(conf.AdminINI, "basic_auth_username")
		p, _ := conf.Raw(conf.AdminINI, "basic_auth_password")
		req.SetBasicAuth(u, p)
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		Proxy:           http.ProxyFromEnvironment,
	}}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return strings.Contains(string(b), "sent successfully")
}

var safeUser = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// UserAddress is the user's email from the panel database
func UserAddress(username string) string {
	if !safeUser.MatchString(username) {
		return ""
	}
	return db.Value("SELECT email FROM users WHERE username = '" + username + "' LIMIT 1")
}

// WantsNotification is true unless the user has key=0 in notifications.yaml
func WantsNotification(username, key string) bool {
	for _, l := range conf.Lines(usersDir + "/" + username + "/notifications.yaml") {
		if l == key+"=0" {
			return false
		}
	}
	return true
}

// SendUser emails a panel user when the notifications module is on and the user opted in to key
func SendUser(username, subject, body, key string, extra ...string) bool {
	v, ok := conf.Raw(conf.Panel, "enabled_modules")
	if !ok || !regexp.MustCompile(`[",]notifications[",]`).MatchString(v) {
		return false
	}
	if key != "" && !WantsNotification(username, key) {
		return false
	}
	email := UserAddress(username)
	if email == "" {
		return false
	}
	return Send(email, subject, body, append([]string{"user"}, extra...)...)
}

var sizeRe = regexp.MustCompile(`^([0-9]+([.][0-9]+)?)([KMGT]?)B?$`)

// PlanSizeMB converts "10 GB", "1G" or "500 MB" to MB, a bare number is GB, "" means unlimited
func PlanSizeMB(raw string) (string, bool) {
	raw = strings.ReplaceAll(raw, " ", "")
	if raw == "" {
		return "", true
	}
	m := sizeRe.FindStringSubmatch(strings.ToUpper(raw))
	if m == nil {
		return "", false
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	if n == 0 {
		return "", true
	}
	mult := map[string]float64{"K": 1.0 / 1024, "M": 1, "G": 1024, "": 1024, "T": 1048576}[m[3]]
	return strconv.FormatFloat(math.RoundToEven(n*mult), 'f', 0, 64), true
}

// PlanSizeLabel is a plan size for humans, "unlimited" for 0 or empty
func PlanSizeLabel(raw string) string {
	mb, ok := PlanSizeMB(raw)
	if !ok {
		return raw
	}
	if mb == "" {
		return "unlimited"
	}
	m, _ := strconv.ParseFloat(mb, 64)
	if m >= 1024 {
		v := m / 1024
		if v == math.Trunc(v) {
			return fmt.Sprintf("%d GB", int(v))
		}
		return fmt.Sprintf("%.1f GB", v)
	}
	return fmt.Sprintf("%d MB", int(m))
}

var digits = regexp.MustCompile(`^[0-9]+$`)

// LimitRaised is true when the upsell limit is higher, unlimited counts as higher, kind is size or count
func LimitRaised(cur, up, kind string) bool {
	if kind == "size" {
		var ok bool
		if cur, ok = PlanSizeMB(cur); !ok {
			return false
		}
		if up, ok = PlanSizeMB(up); !ok {
			return false
		}
	} else {
		if !digits.MatchString(cur) || !digits.MatchString(up) {
			return false
		}
		if cur == "0" {
			cur = ""
		}
		if up == "0" {
			up = ""
		}
	}
	if cur == "" {
		return false
	}
	if up == "" {
		return true
	}
	c, _ := strconv.ParseFloat(cur, 64)
	u, _ := strconv.ParseFloat(up, 64)
	return u > c
}

// Upsell returns the user's plan limits next to the upsell plan's, only on Enterprise with an upgrade URL set
func Upsell(username string) []string {
	if !safeUser.MatchString(username) {
		return nil
	}
	if !regexp.MustCompile(`(?m)^key=enterprise`).MatchString(conf.Read(conf.Panel)) {
		return nil
	}
	rows := db.Rows(`
    SELECT up.name, COALESCE(NULLIF(p.disk_limit, ''), '0'), COALESCE(NULLIF(up.disk_limit, ''), '0'),
      COALESCE(NULLIF(p.inodes_limit, ''), '0'), COALESCE(NULLIF(up.inodes_limit, ''), '0'),
      COALESCE(NULLIF(p.max_email_quota, ''), '0'), COALESCE(NULLIF(up.max_email_quota, ''), '0'),
      COALESCE(NULLIF(p.max_hourly_email, ''), '0'), COALESCE(NULLIF(up.max_hourly_email, ''), '0')
    FROM users u JOIN plans p ON p.id = u.plan_id JOIN plans up ON up.id = p.upsell_plan_id
    WHERE u.username = '` + username + `' AND COALESCE(p.upsell_url, '') != '' LIMIT 1`)
	if len(rows) == 0 {
		return nil
	}
	return rows[0]
}
