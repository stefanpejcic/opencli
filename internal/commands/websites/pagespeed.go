// ##############################################################################
// Script Name: internal/commands/websites/pagespeed.go
// Description: Check Google PageSpeed data for website(s)
// Usage: opencli websites-pagespeed <DOMAIN | -all>
// Author: Stefan Pejcic
// Created: 27.06.2024
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

package websites

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "websites-pagespeed",
		Desc:  "Check Google PageSpeed data for website(s)",
		Usage: "opencli websites-pagespeed <DOMAIN | -all>",
		Run:   pagespeed,
	})
}

const (
	psDir        = "/etc/openpanel/openpanel/websites"
	psGlobalKey  = "/etc/openpanel/openpanel/service/pagespeed.api"
	psUsageLine  = "Usage: opencli websites-pagespeed <website> OR opencli websites-pagespeed -all"
	psAPIPrefix  = "https://www.googleapis.com/pagespeedonline/v5/runPagespeed?url=http://"
	psHistoryDir = "/etc/openpanel/openpanel/websites/history"
)

func pagespeed(args []string) int {
	os.MkdirAll(psDir, 0o777&^umask())
	switch {
	case len(args) == 0:
		fmt.Println(psUsageLine)
		return 1
	case args[0] == "-all" || args[0] == "--all":
		out, _ := sh.Opencli("websites-all").Out()
		if out == "" || out == "No sites found in the database." {
			fmt.Println("No sites found in the database or opencli command error.")
			return 1
		}
		rc := 0
		for _, w := range strings.Fields(out) {
			rc = psReport(w)
		}
		return rc
	case len(args) == 1:
		return psReport(args[0])
	}
	fmt.Println(psUsageLine)
	return 1
}

func psAPIKey(website string) string {
	domain := website
	if i := strings.Index(domain, "/"); i >= 0 {
		domain = domain[:i]
	}
	if i := strings.Index(domain, "://"); i >= 0 {
		domain = domain[i+3:]
	}
	who, _ := sh.Opencli("domains-whoowns", domain).Out()
	// awk -F takes the separator as a regex
	owner := ""
	sep, err := regexp.Compile("Owner of '" + domain + "': ")
	var parts []string
	for _, l := range strings.Split(who, "\n") {
		f := []string{l}
		if err == nil {
			f = sep.Split(l, -1)
		}
		if len(f) >= 2 {
			parts = append(parts, f[1])
		} else {
			parts = append(parts, "")
		}
	}
	owner = strings.TrimRight(strings.Join(parts, "\n"), "\n")
	if owner == "" {
		return ""
	}
	db.Require()
	ctx, _ := db.Plain("SELECT server FROM users WHERE username = '"+db.Escape(owner)+"'", "-D", db.Database, "-sN").Timeout(10 * time.Second).Out()
	if ctx == "" {
		return ""
	}
	if _, err := os.ReadFile(psGlobalKey); err != nil {
		fmt.Fprintf(os.Stderr, "cat: %s: No such file or directory\n", psGlobalKey)
	}
	userKey := "/home/" + ctx + "/docker-data/volumes/" + ctx + "_html_data/_data/pagespeed_api_key.txt"
	for _, p := range []string{userKey, psGlobalKey} {
		if st, err := os.Stat(p); err == nil && st.Size() > 0 {
			b, _ := os.ReadFile(p)
			return strings.TrimRight(string(b), "\n")
		}
	}
	return ""
}

// psEncode is jq's @uri
func psEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// psGet is `.a.b.c` on parsed json, nil when something on the way isn't an object
func psGet(v any, keys ...string) any {
	for _, k := range keys {
		o, ok := v.(*ojson.Object)
		if !ok {
			return nil
		}
		v, _ = o.Get(k)
	}
	return v
}

func psSpeed(website, strategy string) string {
	api := psAPIPrefix + psEncode(website) + "&strategy=" + strategy
	if key := psAPIKey(website); key != "" {
		api += "&key=" + key
	}
	client := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	}}
	body := ""
	if resp, err := client.Get(api); err == nil {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		body = strings.TrimRight(string(b), "\n")
	}
	if strings.TrimSpace(body) == "" {
		return `{"performance_score": , "first_contentful_paint": "", "speed_index": "", "interactive": ""}`
	}
	v, err := ojson.Parse(body)
	if err != nil {
		return `{"performance_score": , "first_contentful_paint": "", "speed_index": "", "interactive": ""}`
	}
	if msg := psGet(v, "error", "message"); msg != nil && msg != false {
		if t := ojson.Text(msg); t != "" {
			return "API Error: " + t
		}
	}
	score := "null"
	if s := psGet(v, "lighthouseResult", "categories", "performance", "score"); s != nil && s != false {
		score = ojson.Compact(s)
	}
	audit := func(name string) string {
		a := psGet(v, "lighthouseResult", "audits", name, "displayValue")
		if a == nil || a == false {
			return ""
		}
		return ojson.Text(a)
	}
	return fmt.Sprintf(`{"performance_score": %s, "first_contentful_paint": "%s", "speed_index": "%s", "interactive": "%s"}`,
		score, audit("first-contentful-paint"), audit("speed-index"), audit("interactive"))
}

func psReport(website string) int {
	desktop := psSpeed(website, "desktop")
	if strings.Contains(desktop, "API Error") {
		fmt.Println(desktop)
		fmt.Println("Aborting due to API error.")
		return 1
	}
	mobile := psSpeed(website, "mobile")
	ts := now()
	filename := psDir + "/" + reportName(website) + ".json"

	content := fmt.Sprintf("{\n  \"timestamp\": \"%s\",\n  \"website\": \"%s\",\n  \"desktop_speed\": %s,\n  \"mobile_speed\": %s\n}\n", ts, website, desktop, mobile)
	os.WriteFile(filename, []byte(content), 0o666&^umask())

	// the report is overwritten every morning, OpenAdmin's daily summary reads the copy of the day it covers
	hist := psHistoryDir + "/" + ts[:10]
	if os.MkdirAll(hist, 0o777&^umask()) == nil {
		st, _ := os.Stat(filename)
		os.WriteFile(hist+"/"+baseName(filename), []byte(content), st.Mode().Perm())
	}
	if ents, err := os.ReadDir(psHistoryDir); err == nil {
		for _, e := range ents {
			if info, err := e.Info(); err == nil && e.IsDir() && int(time.Since(info.ModTime())/(24*time.Hour)) > 35 {
				os.RemoveAll(psHistoryDir + "/" + e.Name())
			}
		}
	}

	fmt.Println("Google PageSpeed data saved to " + filename)
	return 0
}

func baseName(p string) string { return p[strings.LastIndex(p, "/")+1:] }
