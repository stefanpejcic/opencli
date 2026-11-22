// ##############################################################################
// Script Name: internal/commands/imunify/imunify_notify.go
// Description: Install and manage ImunifyAV service.
// Usage: opencli imunify [status|start|stop|install|update|uninstall|hook]
// Author: Stefan Pejcic
// Created: 04.08.2025
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

package imunify

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"opencli/internal/db"
	"opencli/internal/mail"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

// 75 -> 1m 15s
func humanDuration(s int64) string {
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh %dm", s/3600, s%3600/60)
}

// readValues parses every json value in a file, like jq -s does
func readValues(file string) ([]any, bool) {
	d := json.NewDecoder(strings.NewReader(readStr(file)))
	var out []any
	for d.More() {
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return nil, false
		}
		v, err := ojson.Parse(string(raw))
		if err != nil {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

func readStr(f string) string { b, _ := os.ReadFile(f); return string(b) }

func get(o *ojson.Object, k string) any { v, _ := o.Get(k); return v }

// alt is jq's `.k // def`
func alt(o *ojson.Object, k string, def any) any {
	v := get(o, k)
	if v == nil || v == false {
		return def
	}
	return v
}

func str(o *ojson.Object, k string) string {
	s, _ := get(o, k).(string)
	return s
}

func num(v any) float64 {
	f, _ := strconv.ParseFloat(ojson.Text(v), 64)
	return f
}

// jq sort order for the group_by key
func rank(v any) int {
	switch v.(type) {
	case nil:
		return 0
	case bool:
		return 1
	case json.Number:
		return 3
	case string:
		return 4
	case []any:
		return 5
	}
	return 6
}

func keyLess(a, b any) bool {
	if rank(a) != rank(b) {
		return rank(a) < rank(b)
	}
	switch x := a.(type) {
	case json.Number:
		return num(x) < num(b)
	case string:
		return x < b.(string)
	case bool:
		return !x && b.(bool)
	}
	return ojson.Compact(a) < ojson.Compact(b)
}

func endsWith(o *ojson.Object, suffix string) bool {
	return strings.HasSuffix(str(o, "event_id"), suffix)
}

func events(files []string) []*ojson.Object {
	var all []*ojson.Object
	for _, f := range files {
		vals, ok := readValues(f)
		if !ok {
			return nil
		}
		for _, v := range vals {
			o, ok := v.(*ojson.Object)
			if !ok {
				return nil
			}
			if s := get(o, "scan_id"); s != nil && s != false {
				all = append(all, o)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return keyLess(get(all[i], "scan_id"), get(all[j], "scan_id")) })
	// one event per scan, a finished one wins over started in case both landed in the same batch
	var out []*ojson.Object
	for i := 0; i < len(all); {
		j := i
		k := ojson.Compact(get(all[i], "scan_id"))
		for j < len(all) && ojson.Compact(get(all[j], "scan_id")) == k {
			j++
		}
		var pick *ojson.Object
		for _, e := range all[i:j] {
			if endsWith(e, "SCAN_FINISHED") {
				pick = e
				break
			}
		}
		if pick == nil {
			for _, e := range all[i:j] {
				if endsWith(e, "SCAN_STARTED") {
					pick = e
					break
				}
			}
		}
		if pick != nil {
			out = append(out, pick)
		}
		i = j
	}
	return out
}

func strList(v any) []string {
	arr, _ := v.([]any)
	var out []string
	for _, it := range arr {
		out = append(out, ojson.Text(it))
	}
	return out
}

var ctxRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// emails the account owner when a finished scan of their home found malware, path is /home/<context>/...
func notifyUser(ctx string, malicious int64, files []string) {
	if !ctxRe.MatchString(ctx) {
		return
	}
	username := strings.SplitN(db.Cmd("SELECT username FROM users WHERE server = '"+ctx+"' LIMIT 1", "-N", "-B").NoErr().Str(), "\n", 2)[0]
	if username == "" || strings.HasPrefix(username, "SUSPENDED_") {
		return
	}
	// the user only knows their files by the path inside the panel, not the host volume path
	vol := "/home/" + ctx + "/docker-data/volumes/" + ctx + "_html_data/_data/"
	var list []string
	for i, f := range files {
		if i >= 20 {
			break
		}
		if strings.HasPrefix(f, vol) {
			f = "/var/www/html/" + f[len(vol):]
		}
		list = append(list, "- "+f)
	}
	l := strings.Join(list, "\n")
	if malicious > 20 {
		l += fmt.Sprintf("\n- and %d more", malicious-20)
	}
	body := fmt.Sprintf("An ImunifyAV malware scan found %d infected file(s) on account %s:\n\n%s\n\nScanned: %s", malicious, username, l, time.Now().Format("2006-01-02 15:04:05 MST"))
	if mail.SendUser(username, "Malware found on account "+username, body, "notify_malware_found", "", "", "",
		"Remove or clean these files, or ask your hosting provider for help if you're not sure what they are. Malware usually gets in through outdated plugins, themes or stolen passwords, so update your websites and change your FTP and admin passwords.") {
		fmt.Printf("%s notified by email about %d malicious file(s).\n", username, malicious)
	}
}

var homeRe = regexp.MustCompile(`^/home/([^/]+)`)

// opencli imunify notify
func notify() {
	files, _ := filepath.Glob(iavSpool + "/new/*")
	if len(files) == 0 || !fileIs(files[0]) {
		return
	}
	evs := events(files)
	for _, f := range files {
		os.Remove(f)
	}
	for _, ev := range evs {
		event := str(ev, "event_id")
		path := ojson.Text(alt(ev, "path", ""))
		started := int64(math.Floor(num(alt(ev, "started", json.Number("0")))))
		completed := int64(math.Floor(num(alt(ev, "completed", json.Number("0")))))
		filesCount := ojson.Text(alt(ev, "total_resources", json.Number("0")))
		malicious := int64(num(alt(ev, "total_malicious", json.Number("0"))))
		errMsg := ojson.Text(alt(ev, "error", ""))

		user := ""
		if m := homeRe.FindStringSubmatch(path); m != nil {
			user = m[1]
		}
		suffix := ""
		if user != "" {
			suffix = ": " + user
		}
		kind := "Malware scan"
		if strings.HasPrefix(event, "CUSTOM_") {
			kind = "Custom malware scan"
		}
		severity, noSend, title, message, duration := "info", "", "", "", ""
		if strings.HasSuffix(event, "SCAN_STARTED") {
			title = "ImunifyAV scan running" + suffix
			message = kind + " (" + path + ") started at " + time.Unix(started, 0).Format("15:04:05") + ". This notification will be updated when the scan finishes."
			// only shown in the panel, email and webhook wait for the result
			noSend = "--no-send"
		} else {
			d := int64(0)
			if completed > started {
				d = completed - started
			}
			duration = humanDuration(d)
			mal := strList(alt(ev, "malicious_files", []any{}))
			switch {
			case errMsg != "":
				severity = "warning"
				title = "ImunifyAV scan failed" + suffix
				message = kind + " (" + path + ") stopped after " + duration + " with error: " + errMsg
			case malicious > 0:
				severity = "warning"
				s := ""
				if malicious > 1 {
					s = "s"
				}
				title = fmt.Sprintf("ImunifyAV found %d malicious file%s%s", malicious, s, suffix)
				first := mal
				if len(first) > 5 {
					first = first[:5]
				}
				list := strings.Join(first, ", ")
				if malicious > 5 {
					list += fmt.Sprintf(" and %d more", malicious-5)
				}
				message = kind + " (" + path + ") checked " + filesCount + " files in " + duration + " and found: " + list + ". Review them in OpenAdmin > ImunifyAV."
				if user != "" {
					notifyUser(user, malicious, mal)
				} else {
					// a scan of all of /home, each account gets an email about its own files only
					var ctxs []string
					seen := map[string]bool{}
					for _, f := range mal {
						if m := regexp.MustCompile(`^/home/([^/]+)/`).FindStringSubmatch(f); m != nil && !seen[m[1]] {
							seen[m[1]] = true
							ctxs = append(ctxs, m[1])
						}
					}
					sort.Strings(ctxs)
					for _, c := range ctxs {
						var own []string
						for _, f := range mal {
							if strings.HasPrefix(f, "/home/"+c+"/") {
								own = append(own, f)
							}
						}
						notifyUser(c, int64(len(own)), own)
					}
				}
			default:
				title = "ImunifyAV scan clean" + suffix
				message = kind + " (" + path + ") checked " + filesCount + " files in " + duration + ", no malware found."
			}
		}
		state := "finished"
		if endsWith(ev, "STARTED") {
			state = "running"
		}
		malFiles, _ := alt(ev, "malicious_files", []any{}).([]any)
		if len(malFiles) > 20 {
			malFiles = malFiles[:20]
		}
		if malFiles == nil {
			malFiles = []any{}
		}
		details := ojson.New().Set("scan_id", get(ev, "scan_id")).Set("path", get(ev, "path")).Set("user", user).
			Set("files", get(ev, "total_resources")).Set("malicious", get(ev, "total_malicious")).Set("duration", duration).
			Set("malicious_files", malFiles).Set("state", state)
		args := []string{"sentinel", "--action=imunify_scan", "--title=" + title, "--message=" + message, "--severity=" + severity, "--details=" + ojson.Compact(details), "--merge-key=scan_id"}
		if noSend != "" {
			args = append(args, noSend)
		}
		sh.Opencli(args...).Run()
	}
}

func fileIs(p string) bool { st, err := os.Stat(p); return err == nil && st.Mode().IsRegular() }
