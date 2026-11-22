// ##############################################################################
// Script Name: internal/commands/sentinel/sentinel_notify.go
// Description: Check system services, traffic and resource usage, and log/send custom notifications on request.
// Usage: opencli sentinel [--startup] [--report] [--action=<name> --title=<title> --message=<msg> [--severity=<level>] [--details=<json> [--merge-key=<key>]] [--no-send]]
// Author: Stefan Pejcic
// Created: 01.11.2023
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

package sentinel

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"opencli/internal/conf"
	"opencli/internal/mail"
	"opencli/internal/notify"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

// alerts of a full run are queued here and sent as one email/webhook at the end, see flushNotificationQueue
var (
	queueOn bool
	queueMu sync.Mutex
	queue   []*ojson.Object
)

func isUnreadMessagePresent(title string) bool { return notify.IsUnread(title) }

// marks unread alerts with this title as read and resolved once the issue is gone, prefix matches titles with dynamic parts (ips, domains)
func (r *run) resolveNotification(title string, prefix bool) {
	first, ok := notify.Resolve(title, prefix)
	if !ok {
		return
	}
	title = strings.TrimSuffix(title, " ")
	r.printf("%s Issue resolved, marked notification as read: %s\n", cOK, title)

	// the admin got the alert by email/webhook, so tell them it's over too
	if titleSnoozed(title) {
		return
	}
	since := ""
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", first, time.Local); err == nil {
		since = fmt.Sprintf(" It was first reported at %s, %s ago.", first, formatDuration(int(time.Now().Unix()-t.Unix())))
	}
	r.sendNotification("resolved", "Resolved: "+title, "Sentinel no longer detects this issue on "+hostname+"."+since)
}

func formatDuration(s int) string {
	switch {
	case s >= 86400:
		return fmt.Sprintf("%dd %dh", s/86400, s%86400/3600)
	case s >= 3600:
		return fmt.Sprintf("%dh %dm", s/3600, s%3600/60)
	}
	return fmt.Sprintf("%dm", s/60)
}

// overThresholdStreak bumps the streak for a check and is true once it has been over threshold streakRuns runs in a row
func (r *run) overThresholdStreak(name string) bool {
	f := "/tmp/sentinel.streak." + name
	n, err := strconv.Atoi(readTrim(f))
	if err != nil || n < 0 || !regexp.MustCompile(`^[0-9]+$`).MatchString(readTrim(f)) {
		n = 0
	}
	r.streak = n + 1
	os.WriteFile(f, []byte(strconv.Itoa(r.streak)+"\n"), 0o644)
	return r.streak >= streakRuns
}

func clearStreak(name string) { os.Remove("/tmp/sentinel.streak." + name) }

var ipCacheMu sync.Mutex

func ipv4Client(timeout time.Duration, insecure bool) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
			}}}
}

// curl4 is curl --silent --max-time N -4 url, ok is false when curl would exit non-zero
func curl4(url string, timeout time.Duration) (string, bool) {
	resp, err := ipv4Client(timeout, false).Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return strings.TrimRight(string(b), "\n"), err == nil
}

func getPublicIP() string {
	ipCacheMu.Lock()
	defer ipCacheMu.Unlock()
	if age, ok := ageSecs(ipCacheFile); ok && age < 21600 { // 6h
		return readTrim(ipCacheFile)
	}
	ip, ok := curl4("https://ip.openpanel.com", 3*time.Second)
	if !ok {
		ip, _ = curl4("https://ifconfig.me/ip", 3*time.Second)
	}
	if ip == "" {
		for _, l := range strings.Split(sh.C("ip", "-4", "addr", "show", "scope", "global").Str(), "\n") {
			if strings.Contains(l, "inet ") {
				ip, _, _ = strings.Cut(field(l, 2), "/")
				break
			}
		}
	}
	if ip != "" {
		os.WriteFile(ipCacheFile, []byte(ip+"\n"), 0o644)
	}
	return ip
}

// untilFlag is true while the file holds a future unix time, an expired or broken one is deleted
func untilFlag(path string) bool {
	if !isFile(path) {
		return false
	}
	until := readTrim(path)
	n, err := strconv.ParseInt(until, 10, 64)
	if until == "" || err != nil || !regexp.MustCompile(`^[0-9]+$`).MatchString(until) || n <= time.Now().Unix() {
		os.Remove(path)
		return false
	}
	return true
}

// checks the pause flag file admins set from OpenAdmin > Settings > Notifications
func notificationsPaused() bool { return untilFlag(notificationsPauseFile) }

// per-alert snooze flag from OpenAdmin, the title is hashed the same way OpenAdmin does so both sides agree on the filename
func titleSnoozed(title string) bool {
	h := md5.Sum([]byte(title))
	return untilFlag("/tmp/openpanel_notification_snooze_" + hex.EncodeToString(h[:]))
}

func webhookNotification(title, message string) {
	if notificationsPaused() || webhookURL == "" {
		return
	}
	// prefix the title with the server so it's clear which one the alert came from
	ip := getPublicIP()
	if ip != "" {
		title = "[" + hostname + " (" + ip + ")] " + title
	} else {
		title = "[" + hostname + "] " + title
	}
	// text is for Slack, content for Discord which rejects more than 2000 chars
	content := []rune("**" + title + "**\n" + message)
	if len(content) > 2000 {
		content = append(content[:1997], []rune("...")...)
	}
	payload := ojson.Compact(ojson.New().Set("text", "*"+title+"*\n"+message).Set("username", "OpenAdmin-"+hostname).Set("content", string(content)))
	// webhook_url can hold several URLs separated by spaces
	for _, url := range strings.Fields(webhookURL) {
		c := &http.Client{Timeout: 5 * time.Second}
		if resp, err := c.Post(url, "application/json", strings.NewReader(payload)); err == nil {
			resp.Body.Close()
		}
	}
}

func token64() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 64)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

func (r *run) emailNotification(title, message, items string) {
	if items == "" {
		items = "[]"
	}
	if notificationsPaused() {
		return
	}
	tok := token64()
	var b strings.Builder
	for _, l := range conf.Lines(confFile) {
		if strings.HasPrefix(l, "mail_security_token=") {
			l = "mail_security_token=" + tok
		}
		b.WriteString(l + "\n")
	}
	if os.WriteFile(confFile+".tmp", []byte(b.String()), 0o644) == nil {
		os.Rename(confFile+".tmp", confFile)
	}

	domain := sh.Opencli("domain").Stderr(r.out).Str()
	url := fmt.Sprintf("%s://%s:%s/send_email", mail.Proto(domain), domain, mail.AdminPort())

	var body strings.Builder
	w := multipart.NewWriter(&body)
	for _, f := range [][2]string{{"transient", tok}, {"recipient", email}, {"subject", title}, {"body", message}, {"notifications", items}} {
		w.WriteField(f[0], f[1])
	}
	w.Close()
	req, err := http.NewRequest("POST", url, strings.NewReader(body.String()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	const adminINI = "/etc/openpanel/openadmin/config/admin.ini"
	for _, l := range conf.Lines(adminINI) {
		if l == "basic_auth=yes" {
			req.SetBasicAuth(keyGet(adminINI, "basic_auth_username"), keyGet(adminINI, "basic_auth_password"))
			break
		}
	}
	resp, err := ipv4Client(5*time.Second, true).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return
	}
	rb, _ := io.ReadAll(resp.Body)
	s := string(rb)
	switch {
	case strings.Contains(s, `"error"`):
		r.printf("Error sending email: %s\n", strings.TrimRight(s, "\n"))
	case strings.Contains(s, `"sent successfully"`):
		r.println("Email sent.")
	}
}

// alerts that need the admin: logged as unread and sent by email/webhook, a repeat of an unread one only bumps its count
func (r *run) writeNotification(severity, category, title, message, details string) {
	// snoozed alerts skip logging and email/webhook entirely, unlike the global pause, which still logs
	if titleSnoozed(title) {
		r.printf("[!] This alert is snoozed: %s\n", title)
		return
	}
	if !notify.Add("yes", "unread", severity, category, "sentinel", title, message, details) {
		return
	}
	r.sendNotification(severity, title, message)
}

// user actions logged by other opencli commands via --action, only if the admin enabled that action
func (r *run) writeActionNotification(action, title, message, severity, details, mergeKey, noSend string) {
	if titleSnoozed(title) {
		r.printf("[!] This alert is snoozed: %s\n", title)
		return
	}
	if os.Getenv("RUN_ACTION_LOCKED") != "" {
		return
	}
	os.Setenv("RUN_ACTION_LOCKED", "1")

	if validateYesNo(iniGet(action)) == "no" {
		r.printf("[!] Notifications are disabled for action: %s\n", action)
		return
	}
	d, err := ojson.ParseObject(strings.TrimSpace(details))
	if err != nil {
		d = ojson.New()
	}
	d.Set("action", action)
	details = ojson.Compact(d)
	// same merge key value as an earlier entry means it's an update to that one, e.g. a scan that finished
	if mergeKey == "" || !notify.Merge(mergeKey, severity, title, message, details) {
		notify.Add("no", "unread", severity, "action", action, title, message, details)
	}
	if noSend == "" {
		r.sendNotification(severity, title, message)
	}
}

// for things sentinel already fixed on its own: kept on the Notifications page as history, no email/webhook, repeats within 24h bump one entry
func writeInfoNotification(category, title, message string) {
	if titleSnoozed(title) {
		return
	}
	notify.Add("info", "read", "info", category, "sentinel", title, message, "")
}

// queued during a full run, sent right away for --startup and --action
func (r *run) sendNotification(severity, title, message string) {
	item := ojson.New().Set("severity", severity).Set("title", title).Set("message", message)
	if queueOn {
		queueMu.Lock()
		queue = append(queue, item)
		queueMu.Unlock()
		return
	}
	r.deliverNotification(title, message, "["+ojson.Compact(item)+"]")
}

func (r *run) deliverNotification(subject, body, items string) {
	if emailAlert == "yes" {
		r.emailNotification(subject, body, items)
	}
	if webhookURL != "" {
		webhookNotification(subject, body)
	}
}

// sends everything this run queued as one email and one webhook
func (r *run) flushNotificationQueue() {
	queueMu.Lock()
	items := queue
	queue = nil
	queueOn = false
	queueMu.Unlock()
	if len(items) == 0 {
		return
	}
	var subject, body string
	if len(items) == 1 {
		subject, body = items[0].Str("title"), items[0].Str("message")
	} else {
		subject = fmt.Sprintf("%d notifications from Sentinel on %s", len(items), hostname)
		var parts []string
		for i, it := range items {
			parts = append(parts, fmt.Sprintf("%d. [%s] %s\n%s", i+1, strings.ToUpper(it.Str("severity")), it.Str("title"), it.Str("message")))
		}
		body = strings.Join(parts, "\n\n")
	}
	arr := make([]any, len(items))
	for i, it := range items {
		arr[i] = it
	}
	r.deliverNotification(subject, body, ojson.Compact(arr))
}

// ipToInt is ((i1<<24)|(i2<<16)|(i3<<8)|i4) of bash arithmetic
func ipToInt(ip string) int64 {
	p := strings.SplitN(ip, ".", 4)
	for len(p) < 4 {
		p = append(p, "")
	}
	return int64(atoi(p[0]))<<24 | int64(atoi(p[1]))<<16 | int64(atoi(p[2]))<<8 | int64(atoi(p[3]))
}

func ipInCIDR(ip, cidr string) bool {
	network, m, _ := strings.Cut(cidr, "/")
	var maskbin int64
	if s := 32 - atoi(m); s >= 0 && s < 63 {
		maskbin = (int64(0xFFFFFFFF) << s) & 0xFFFFFFFF
	}
	return ipToInt(ip)&maskbin == ipToInt(network)&maskbin
}

func isIPWhitelisted(ip string) bool {
	const wl = "/etc/openpanel/openadmin/ssh_whitelist.conf"
	if !isFile(wl) {
		return false
	}
	for _, e := range bashLines(conf.Read(wl)) {
		if e == "" {
			continue
		}
		if strings.Contains(e, "/") {
			if ipInCIDR(ip, e) {
				return true
			}
		} else if e == ip {
			return true
		}
	}
	return false
}
