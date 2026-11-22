// ##############################################################################
// Script Name: internal/commands/license/license.go
// Description: Manage OpenPanel Enterprise license.
// Usage: opencli license [key|verify|info|delete|<LICENSE_KEY>] [--json] [--no-restart]
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

package license

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "license",
		Desc:  "Manage OpenPanel Enterprise license.",
		Usage: "opencli license [key|verify|info|delete|<LICENSE_KEY>] [--json] [--no-restart]",
		Run:   license,
	})
}

const (
	whmcsURL    = "https://api.openpanel.com/enterprise/index.php"
	composeFile = "/root/docker-compose.yml"
	service     = "openpanel"
	green       = "\033[0;32m"
	red         = "\033[0;31m"
	reset       = "\033[0m"
)

var emailModules = []string{
	"emails",
	"webmail",
	"email_deliverability",
	"email_filters",
	"email_aliases",
	"email_import",
	"email_migrate",
	"email_export",
	"email_default",
}

type lic struct {
	json      bool
	noRestart bool
}

func usage() {
	fmt.Print(`Usage: opencli license [options]

Commands:
  key                                           View current license key.
  enterprise-XXXXXXXXXX                         Save an Enterprise license key.
  noc-XXXXXXXXXX                                Save a NOC license key.
  lifetime-XXXXXXXXXX                           Save a Lifetime license key.
  verify                                        Verify the license key.
  info                                          Display information about the license owner and expiration.
  delete                                        Delete the license key and downgrade OpenPanel to Community edition.
`)
	cli.Exit(1)
}

func awkTrim(s string) string { return strings.Trim(s, " \t") }

// readKey is read_config | grep -i 'key' | cut -d'=' -f2 on the [LICENSE] section
func readKey() string {
	flag := false
	var keys []string
	for _, l := range conf.Lines(conf.Panel) {
		if strings.Contains(l, "[LICENSE]") {
			flag = true
			continue
		}
		if strings.Contains(l, "[") {
			flag = false
		}
		if !flag {
			continue
		}
		f := strings.Split(l, "=")
		f1, f2 := awkTrim(f[0]), ""
		if len(f) > 1 {
			f2 = awkTrim(f[1])
		}
		line := f1 + "=" + f2
		if strings.Contains(strings.ToLower(line), "key") {
			keys = append(keys, strings.Split(line, "=")[1])
		}
	}
	return strings.TrimRight(strings.Join(keys, "\n"), "\n")
}

func jsonEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\t", `\t`, "\r", "", "\n", `\n`).Replace(s)
}

func (l *lic) out(msg, color string) {
	if l.json {
		fmt.Println(msg)
	} else {
		fmt.Println(color + msg + reset)
	}
}

func (l *lic) keyText() string {
	k := readKey()
	switch {
	case k == "" && l.json:
		return `{"key": null}`
	case k == "":
		return red + "No License Key" + reset
	case l.json:
		return `{"key": "` + jsonEscape(k) + `"}`
	}
	return green + k + reset
}

func client(connect, max time.Duration) *http.Client {
	d := &net.Dialer{Timeout: connect}
	return &http.Client{
		Timeout: max,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return d.DialContext(ctx, "tcp4", addr)
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// curl --silent --max-time 1 -4, whatever body arrived is kept
func get4(url string) (string, bool) {
	resp, err := client(time.Second, time.Second).Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err == nil
}

func publicIP() string {
	ip, ok := get4("https://ip.openpanel.com")
	if !ok {
		ip2, _ := get4("https://ifconfig.me/ip")
		ip += ip2
	}
	return strings.TrimRight(ip, "\n")
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// the whmcs check, curl -sS prints its error to stderr and the response is then empty
func callAPI(key string) string {
	ip := publicIP()
	data := "licensekey=" + key + "&ip=" + ip + "&check_token=" + randHex(16)
	req, _ := http.NewRequest("POST", whmcsURL, strings.NewReader(data))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client(time.Second, 2*time.Second).Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, curlErr(err))
		return ""
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, curlErr(err))
	}
	return strings.TrimRight(string(b), "\n")
}

func curlErr(err error) string {
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return "curl: (28) Operation timed out after 2000 milliseconds with 0 bytes received"
	}
	return "curl: (7) Failed to connect to api.openpanel.com port 443: " + err.Error()
}

// grep -oP "(?<=<field>).*?(?=</field>)", empty matches are skipped like grep -o does
func field(resp, name string) string {
	re := regexp.MustCompile(`<` + regexp.QuoteMeta(name) + `>(.*?)</` + regexp.QuoteMeta(name) + `>`)
	var out []string
	for _, l := range strings.Split(resp, "\n") {
		for _, m := range re.FindAllStringSubmatch(l, -1) {
			if m[1] != "" {
				out = append(out, m[1])
			}
		}
	}
	return strings.Join(out, "\n")
}

func (l *lic) manageVolumes(enable bool) {
	vols := []string{
		"/root/.ssh/:/root/.ssh/:ro",
		"/usr/local/mail/openmail/:/usr/local/mail/openmail/",
		"/etc/openpanel/openpanel/conf/knowledge_base_articles.json:/etc/openpanel/openpanel/conf/knowledge_base_articles.json:ro",
	}
	exists := func(v string) bool { return strings.Contains(conf.Read(composeFile), "- "+v) }
	if !enable {
		for _, v := range vols {
			if !exists(v) {
				continue
			}
			re := regexp.MustCompile(`^\s*-\s*` + v + `\s*$`)
			conf.SedFile(composeFile, func(lines []string) []string {
				var out []string
				for _, ln := range lines {
					if !re.MatchString(ln) {
						out = append(out, ln)
					}
				}
				return out
			})
		}
		return
	}
	start := 0
	flag := false
	for i, ln := range conf.Lines(composeFile) {
		if strings.Contains(ln, service+":") {
			flag = true
		}
		if flag && strings.Contains(ln, "volumes:") {
			start = i + 1
			break
		}
	}
	for _, v := range vols {
		if exists(v) {
			continue
		}
		add := "      - " + v
		conf.SedFile(composeFile, func(lines []string) []string {
			var out []string
			for i, ln := range lines {
				out = append(out, ln)
				// with no volumes: line the address was empty and sed appended after every line
				if start == 0 || i+1 == start {
					out = append(out, add)
				}
			}
			return out
		})
		if start > 0 {
			start++
		}
	}
}

func (l *lic) restartServices() {
	if l.noRestart {
		fmt.Println("Please restart OpenAdmin service to enable new features.")
		return
	}
	sh.C("service", "admin", "restart").NoOut().Run()
	if sh.C("podman", "ps", "-q", "-f", "name=openpanel").Str() != "" {
		sh.Bash("cd /root && podman-compose down openpanel && podman-compose up -d openpanel").Quiet().Background()
	}
	fmt.Println("OpenPanel and OpenAdmin are restarted to apply Enterprise features.")
}

func toggleEmailModules(enable bool) {
	var vals []string
	for _, ln := range conf.Lines(conf.Panel) {
		if strings.HasPrefix(ln, "enabled_modules=") {
			vals = append(vals, strings.ReplaceAll(strings.Split(ln, "=")[1], `"`, ""))
		}
	}
	mods := strings.TrimRight(strings.Join(vals, "\n"), "\n")
	if !enable {
		for _, m := range emailModules {
			word := regexp.MustCompile(`\b` + m + `\b`)
			var lines []string
			for _, ln := range strings.Split(mods, "\n") {
				ln = word.ReplaceAllString(ln, "")
				ln = strings.ReplaceAll(ln, ",,", ",")
				ln = strings.TrimPrefix(ln, ",")
				ln = strings.TrimSuffix(ln, ",")
				lines = append(lines, ln)
			}
			mods = strings.Join(lines, "\n")
		}
	} else {
		for _, m := range emailModules {
			if !regexp.MustCompile(`\b` + m + `\b`).MatchString(mods) {
				if mods == "" {
					mods = m
				} else {
					mods += "," + m
				}
			}
		}
	}
	repl := `enabled_modules="` + mods + `"`
	conf.SedFile(conf.Panel, func(lines []string) []string {
		for i, ln := range lines {
			if strings.HasPrefix(ln, "enabled_modules=") {
				lines[i] = text.SedRepl(repl, []string{ln})
			}
		}
		return lines
	})
}

func pagespeed(remove bool) {
	file := "/etc/openpanel/openpanel/service/pagespeed.api"
	key := "AIzaSyDow0GLE7N5gcZXa72tpqIvIaJtn1bDtsk"
	if remove {
		for _, ln := range strings.Split(conf.Read(file), "\n") {
			if ln == key {
				os.Remove(file)
				return
			}
		}
		return
	}
	if st, err := os.Stat(file); err != nil || st.Size() == 0 {
		os.WriteFile(file, []byte(key+"\n"), 0o644)
	}
}

func (l *lic) save(key string) {
	if sh.Opencli("config", "update", "key", key).NoOut().Run() != nil {
		l.out("License is valid, but failed to save the license key "+key, red)
		return
	}
	l.out("License key "+key+" added.", green)
	toggleEmailModules(true)
	sh.Opencli("config", "update", "how_to_guides", "no").NoOut().Run()
	pagespeed(false)
	l.manageVolumes(true)
	l.restartServices()
}

func (l *lic) verifyAndSave(key string) {
	if field(callAPI(key), "status") == "Active" {
		l.save(key)
		return
	}
	l.out("License is invalid", red)
	cli.Exit(1)
}

func (l *lic) verify() {
	key := readKey()
	if key == "" {
		l.out("No License Key. Please add the key first: opencli config update key XXXXXXXXXX", red)
		cli.Exit(1)
	}
	// read-only, enterprise features are applied when the key is added
	if field(callAPI(key), "status") == "Active" {
		l.out("License is valid", green)
		return
	}
	l.out("License is invalid", red)
	cli.Exit(1)
}

func (l *lic) info() {
	key := readKey()
	if key == "" {
		l.out("No License Key. Please add the key first.", red)
		cli.Exit(1)
	}
	resp := callAPI(key)
	if field(resp, "status") != "Active" {
		l.out("License is invalid", red)
		cli.Exit(1)
	}
	names := []string{"registeredname", "companyname", "email", "productname", "regdate", "nextduedate", "billingcycle", "validip"}
	labels := []string{"Owner", "Company Name", "Email", "License Type", "Registration Date", "Next Due Date", "Billing Cycle", "Valid IP"}
	if l.json {
		fmt.Println("{")
		for i, n := range names {
			sep := ","
			if i == len(names)-1 {
				sep = ""
			}
			fmt.Printf("    %s: %s%s\n", strconv.Quote(labels[i]), `"`+jsonEscape(field(resp, n))+`"`, sep)
		}
		fmt.Println("}")
		return
	}
	for i, n := range names {
		fmt.Printf("%s: %s\n", labels[i], field(resp, n))
	}
}

func (l *lic) delete() int {
	sh.Opencli("config", "update", "key", "").NoOut().Run()
	toggleEmailModules(false)
	pagespeed(true)
	l.manageVolumes(false)
	return sh.C("service", "admin", "restart").Code()
}

func license(args []string) int {
	if len(args) == 0 {
		usage()
	}
	l := &lic{}
	for _, a := range args {
		switch a {
		case "--json":
			l.json = true
		case "--no-restart":
			l.noRestart = true
		}
	}
	cmd := args[0]
	switch {
	case cmd == "key":
		fmt.Println(strings.TrimRight(l.keyText(), "\n"))
	case cmd == "info":
		l.info()
	case cmd == "verify":
		l.verify()
	case cmd == "delete":
		return l.delete()
	case strings.HasPrefix(cmd, "enterprise") || strings.HasPrefix(cmd, "noc-") || strings.HasPrefix(cmd, "lifetime-"):
		l.verifyAndSave(cmd)
	default:
		l.out("Invalid command.", red)
		usage()
	}
	return 0
}
