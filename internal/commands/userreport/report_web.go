// ##############################################################################
// Script Name: internal/commands/userreport/report_web.go
// Description: Prints a health report for a user: what is working and what needs attention.
// Usage: opencli user-report <USERNAME|--all> [--section <name,...>] [--json]
// Author: Stefan Pejcic
// Created: 02.10.2026
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

package userreport

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"opencli/internal/conf"
	"opencli/internal/ojson"
	"opencli/internal/text"
)

// sedFirst is sed s/re/repl/ without g, only the first match per line
func sedFirst(re *regexp.Regexp, s, repl string) string {
	loc := re.FindStringSubmatchIndex(s)
	if loc == nil {
		return s
	}
	var dst []byte
	dst = re.ExpandString(dst, repl, s, loc)
	return s[:loc[0]] + string(dst) + s[loc[1]:]
}

var (
	wsRootRe     = regexp.MustCompile(`^\s*(?:DocumentRoot|root|docRoot|vhRoot)\s+([^;\s]+)`)
	severeRe     = regexp.MustCompile(`(\[[a-z_]+:|\[)(crit|alert|emerg)\]`)
	errorLineRe  = regexp.MustCompile(`(\[[a-z_]+:|\[)error\]`)
	emergErrRe   = regexp.MustCompile(`(?i)emerg|error`)
	logStripRes  = []*regexp.Regexp{regexp.MustCompile(`^\[[^\]]*\] `), regexp.MustCompile(`\[pid [^\]]*\] `), regexp.MustCompile(`\[client [^\]]*\] `), regexp.MustCompile(`^[0-9/: ]+\[error\] [0-9#]+: \*?[0-9]* ?`)}
	bracketStrip = regexp.MustCompile(`^\[[^\]]*\] `)
)

func grepLines(s string, match func(string) bool) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if match(l) {
			out = append(out, l)
		}
	}
	return out
}

// topCounts prints the most common lines like `cut -c1-N | sort | uniq -c | sort -rn | head -n K`
func (s *sec) topCounts(lines []string, width, k int) {
	var cut []string
	for _, l := range lines {
		cut = append(cut, cutC(l, width))
	}
	for _, x := range head2(uniqC(cut), k) {
		s.p("  %s  %s\n", padL(x[0], 7), x[1])
	}
}

func secWebserver(s *sec) {
	u := s.u
	s.section("WEBSERVER")
	ws := u.userEnv("WEB_SERVER")
	if ws == "" {
		s.err("WEB_SERVER isn't set in /home/" + u.context + "/.env, the account has no web server.")
		return
	}
	f := bread(strings.SplitN(u.pod(20*time.Second, "inspect", "-f", "{{.State.Status}}\t{{.ImageName}}", ws).Str(), "\n", 2)[0], "\t", 2)
	state, image := f[0], f[1]
	if image != "" {
		s.row("Web server", ws+" ("+image+")")
	} else {
		s.row("Web server", ws)
	}
	s.row("Status", or(state, "not created"))
	if state != "running" {
		s.err("Web server " + ws + " isn't running, websites are down.")
	}

	// varnish sits in front when PROXY_HTTP_PORT is active, same check as 'opencli user-varnish'
	if regexp.MustCompile(`(?m)^PROXY_HTTP_PORT=`).MatchString(readFile("/home/" + u.context + "/.env")) {
		v := u.psField("varnish", 2)
		s.row("Varnish", "on ("+or(v, "not created")+")")
		if v != "running" {
			s.err("Varnish is on but not running, websites are down.")
		}
	} else {
		s.row("Varnish", "off")
	}

	// same config test as the panel's webserver page
	if state == "running" {
		switch ws {
		case "nginx", "openresty":
			out, _ := u.pod(20*time.Second, "exec", ws, "nginx", "-t").Combined()
			if strings.Contains(out, "successful") {
				s.row("Config test", "ok")
			} else {
				s.row("Config test", "failed")
				first := ""
				if l := grepLines(out, emergErrRe.MatchString); len(l) > 0 {
					first = cutC(l[0], 150)
				}
				s.err(ws + " config test failed: " + first)
			}
		case "apache":
			out, _ := u.pod(20*time.Second, "exec", ws, "apachectl", "configtest").Combined()
			if strings.Contains(out, "Syntax OK") {
				s.row("Config test", "ok")
			} else {
				s.row("Config test", "failed")
				first := ""
				if l := grepLines(out, func(x string) bool { return !strings.HasPrefix(x, "AH00558") }); len(l) > 0 {
					first = cutC(l[0], 150)
				}
				s.err("apache config test failed: " + first)
			}
		default:
			s.row("Config test", "not available for "+ws)
		}
	}

	s.subsection("VHOSTS")
	vdir := "/home/" + u.context + "/docker-data/volumes/" + u.context + "_webserver_data/_data"
	html := "/home/" + u.context + "/docker-data/volumes/" + u.context + "_html_data/_data"
	nOK := 0
	for _, d := range hereLines(u.domains) {
		if d == "" {
			continue
		}
		vhost := vdir + "/" + d + ".conf"
		if !conf.IsFile(vhost) {
			s.err(d + " has no vhost file (" + d + ".conf), the web server doesn't serve it.")
			continue
		}
		// the folder the web server really serves, apache DocumentRoot, nginx root, openlitespeed docRoot
		var roots []string
		for _, l := range strings.Split(readFile(vhost), "\n") {
			if m := wsRootRe.FindStringSubmatch(l); m != nil {
				roots = append(roots, m[1])
			}
		}
		sort.Strings(roots)
		bad := false
		for _, r := range uniq(roots) {
			if !strings.HasPrefix(r, "/var/www/html") {
				continue
			}
			if !conf.IsDir(html + strings.TrimPrefix(r, "/var/www/html")) {
				s.err(d + " vhost serves " + r + ", which doesn't exist, the website shows an error.")
				bad = true
			}
		}
		if !bad {
			nOK++
		}
	}
	s.row("Vhosts", fmt.Sprintf("%d of %d ok", nOK, countLines(u.domains)))

	// files nobody uses anymore
	files, _ := filepath.Glob(globQuote(vdir) + "/*")
	doms := map[string]bool{}
	for _, d := range strings.Split(u.domains, "\n") {
		doms[d] = true
	}
	var extra []string
	for _, f := range files {
		if !conf.IsFile(f) {
			continue
		}
		n := filepath.Base(f)
		if !doms[strings.TrimSuffix(n, ".conf")] {
			extra = append(extra, n)
		}
	}
	if len(extra) > 0 {
		s.rowList("Not for any domain", extra...)
	}

	s.subsection("ERROR LOG (24h)")
	if state != "running" {
		s.row("Log", "web server isn't running")
		return
	}
	logs, _ := u.pod(20*time.Second, "logs", "--since", "24h", ws).Combined()
	// apache tags lines [module:level], nginx [level]
	severe := grepLines(logs, severeRe.MatchString)
	errors := grepLines(logs, errorLineRe.MatchString)
	s.row("Errors", fmt.Sprint(countLines(strings.Join(errors, "\n"))))
	s.row("Critical", fmt.Sprint(countLines(strings.Join(severe, "\n"))))
	if len(severe) > 0 {
		s.warn(fmt.Sprintf("%s logged %d critical error(s) in 24h, last: %s", ws, countLines(strings.Join(severe, "\n")), cutC(severe[len(severe)-1], 150)))
	}
	if len(errors) > 0 {
		s.dim("Most common:")
		// strip timestamps, pids and clients so the same error groups together
		var st []string
		for _, l := range errors {
			for _, re := range logStripRes {
				l = sedFirst(re, l, "")
			}
			st = append(st, l)
		}
		s.topCounts(st, 110, 5)
	}
}

const phpAPIFile = "/etc/openpanel/openpanel/php/api_versions.json"

// phpLevels is good/secure/unsupported per version, same classification as the PHP page
func (r *report) needPHPLevels() map[string]string {
	r.phpOnce.Do(func() {
		r.phpLevels = map[string]string{}
		o, err := ojson.ParseObject(readFile(phpAPIFile))
		if err != nil {
			return
		}
		for _, k := range o.Keys() {
			v, _ := o.Get(k)
			t := func(f string) bool { return jqTruthy(jqPath(v, f)) }
			l := "unsupported"
			if t("isLatestVersion") || t("isFutureVersion") || t("isNextVersion") {
				l = "good"
			} else if t("isSecureVersion") {
				l = "secure"
			}
			if k != "" {
				r.phpLevels[k] = l
			}
		}
	})
	return r.phpLevels
}

func (r *report) phpLevel(v string) string { return or(r.needPHPLevels()[v], "unsupported") }

var (
	phpFpmVerRe = regexp.MustCompile(`^php-fpm-([0-9.]+)`)
	vhostPHPRe  = regexp.MustCompile(`php-fpm-([0-9]+\.[0-9]+):[0-9]`)
	iniSkipRe   = regexp.MustCompile(`^[ \t]*([;#]|\[|$)`)
	disableRe   = regexp.MustCompile(`\b(exec|shell_exec)\b`)
)

func iniNorm(v string) string {
	v = strings.TrimSuffix(v, "\r")
	v = strings.Trim(v, " \t")
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	switch strings.ToLower(v) {
	case "on", "1", "true", "yes":
		return "On"
	case "off", "0", "false", "no", "none":
		return "Off"
	}
	return v
}

func iniMap(file string) (map[string]string, []string) {
	m := map[string]string{}
	var keys []string
	for _, l := range awkLines(readFile(file)) {
		l = strings.TrimSuffix(l, "\r")
		if iniSkipRe.MatchString(l) {
			continue
		}
		i := strings.IndexByte(l, '=')
		if i < 0 {
			continue
		}
		k := strings.NewReplacer(" ", "", "\t", "").Replace(l[:i])
		if _, ok := m[k]; !ok {
			keys = append(keys, k)
		}
		m[k] = iniNorm(l[i+1:])
	}
	return m, keys
}

// phpIniDiff lists changed/added/removed keys between a default and a user php.ini, sorted by key
func phpIniDiff(def, user string) [][4]string {
	d, dk := iniMap(def)
	u, uk := iniMap(user)
	var out [][4]string
	for _, k := range uk {
		dv, ok := d[k]
		if !ok {
			out = append(out, [4]string{"added", k, "", u[k]})
		} else if dv != u[k] {
			out = append(out, [4]string{"changed", k, dv, u[k]})
		}
	}
	for _, k := range dk {
		if _, ok := u[k]; !ok {
			out = append(out, [4]string{"removed", k, d[k], ""})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i][1] != out[j][1] {
			return out[i][1] < out[j][1]
		}
		return strings.Join(out[i][:], "\x1f") < strings.Join(out[j][:], "\x1f")
	})
	return out
}

// sedRange is sed -n '/start/,/end/p'
func sedRange(lines []string, start, end func(string) bool) []string {
	var out []string
	in := false
	for _, l := range lines {
		if !in {
			if start(l) {
				in = true
				out = append(out, l)
			}
			continue
		}
		out = append(out, l)
		if end(l) {
			in = false
		}
	}
	return out
}

func eq(s string) func(string) bool { return func(l string) bool { return l == s } }

func secPHP(s *sec) {
	u, r := s.u, s.r
	s.section("PHP")
	r.needPHPLevels()
	ws := u.userEnv("WEB_SERVER")
	def := u.userEnv("DEFAULT_PHP_VERSION")
	s.row("Default version", or(def, "not set"))
	if !conf.IsFile(phpAPIFile) {
		s.dim("No PHP versions API cache (" + phpAPIFile + "), support levels can't be checked.")
	}
	lite := strings.Contains(strings.ToLower(ws), "litespeed")
	if lite {
		s.row("Web server", ws+", PHP runs inside it (lsphp)")
	}

	vers := func(list string) []string {
		var out []string
		for _, l := range strings.Split(list, "\n") {
			if m := phpFpmVerRe.FindStringSubmatch(l); m != nil {
				out = append(out, m[1])
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return text.VersionLess(out[i], out[j]) })
		return out
	}
	available := vers(u.needServices())
	u.needPS()
	running := vers(u.running)
	s.rowList("Available", available...)
	if len(running) == 0 {
		s.rowList("Running", "none")
	} else {
		s.rowList("Running", running...)
	}
	has := func(list []string, v string) bool {
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	}
	if def != "" && len(available) > 0 && !has(available, def) {
		s.err("Default PHP " + def + " isn't in docker-compose.yml, new domains can't use it.")
	}

	// outdated versions still installed, update suggestions from the API
	latest := ""
	if o, err := ojson.ParseObject(readFile(phpAPIFile)); err == nil {
		for _, k := range o.Keys() {
			v, _ := o.Get(k)
			if jqTruthy(jqPath(v, "isLatestVersion")) {
				latest = k
				break
			}
		}
	}
	var old []string
	for _, v := range available {
		if r.phpLevel(v) == "unsupported" {
			old = append(old, v)
		}
	}
	if len(old) > 0 {
		m := "Unsupported PHP versions installed: " + strings.Join(old, " ")
		if latest != "" {
			m += ", latest is " + latest
		}
		s.info(m + ".")
	}

	s.subsection("DOMAINS")
	vdir := "/home/" + u.context + "/docker-data/volumes/" + u.context + "_webserver_data/_data"
	unsupported := 0
	var missing []string
	s.p("  %s%s %s %s%s\n", r.cDim, padR("Domain", 36), padR("PHP", 9), "Support", r.cReset)
	for _, d := range hereLines(u.domains) {
		if d == "" {
			continue
		}
		// the version the vhost really sends requests to, same as the PHP page
		v := ""
		if m := vhostPHPRe.FindStringSubmatch(readFile(vdir + "/" + d + ".conf")); m != nil {
			v = m[1]
		}
		if v == "" {
			s.p("  %s %s %s\n", padR(d, 36), padR("-", 9), "-")
			continue
		}
		lvl := r.phpLevel(v)
		if lvl == "unsupported" {
			unsupported++
		}
		if !has(available, v) {
			missing = append(missing, d+" ("+v+")")
		}
		s.p("  %s %s %s\n", padR(d, 36), padR(v, 9), lvl)
	}
	// same thresholds as the PHP page toast
	if unsupported >= 5 {
		s.err(fmt.Sprintf("%d domains are running an outdated/unsupported PHP version.", unsupported))
	} else if unsupported > 0 {
		s.warn(fmt.Sprintf("%d domain(s) are running an outdated/unsupported PHP version.", unsupported))
	}
	if len(missing) > 0 {
		s.err("PHP version not in docker-compose.yml for: " + strings.Join(missing, " ") + ", those sites can't run PHP.")
	}

	s.subsection("PHP.INI CHANGES")
	any := false
	for _, v := range available {
		f := "/home/" + u.context + "/php.ini/" + v + ".ini"
		defIni := "/etc/openpanel/php/ini/" + v + ".ini"
		if !conf.IsFile(f) || !conf.IsFile(defIni) {
			continue
		}
		diff := phpIniDiff(defIni, f)
		if len(diff) == 0 {
			continue
		}
		any = true
		s.line(fmt.Sprintf("  %s%s%s %s(%d vs %s)%s", r.cBold, v, r.cReset, r.cDim, len(diff), defIni, r.cReset))
		q := func(x string) string { return or(x, `""`) }
		badExec := false
		for _, x := range diff {
			switch x[0] {
			case "changed":
				s.p("    %s %s -> %s\n", padR(x[1], 30), q(x[2]), q(x[3]))
			case "added":
				s.p("    %s %s (added)\n", padR(x[1], 30), q(x[3]))
			case "removed":
				s.p("    %s removed, default %s\n", padR(x[1], 30), q(x[2]))
			}
			if x[1] == "disable_functions" && disableRe.MatchString(x[3]) {
				badExec = true
			}
		}
		// exec/shell_exec off breaks the image's php entrypoint, it can't write the fpm pool
		if badExec {
			s.warn("PHP " + v + " php.ini disables exec/shell_exec, the php-fpm-" + v + " container can't start with that.")
		}
	}
	if !any {
		s.ok("All php.ini files match the defaults.")
	}

	if lite {
		return
	}

	// every running version is checked in parallel, capped at 5s since a busy container can take much longer to start php
	execOut := make([]string, len(running))
	logOut := make([]string, len(running))
	var wg sync.WaitGroup
	for i, v := range running {
		wg.Add(2)
		go func(i int, v string) {
			defer wg.Done()
			execOut[i], _ = u.pod(5*time.Second, "exec", "php-fpm-"+v, "sh", "-c",
				"php -r 'echo PHP_VERSION;'; echo; echo ---INI; php -c /etc/php/"+v+"/fpm/php.ini -d display_errors=0 -r 'exit(0);' 2>&1 >/dev/null; echo ---MODS; php -m; echo ---END").Raw()
		}(i, v)
		go func(i int, v string) {
			defer wg.Done()
			logOut[i], _ = u.pod(5*time.Second, "logs", "--since", "6h", "--tail", "3000", "php-fpm-"+v).Combined()
		}(i, v)
	}
	wg.Wait()

	for i, v := range running {
		s.subsection("PHP " + v)
		out := trimNL(execOut[i])
		lines := strings.Split(out, "\n")
		if !has(lines, "---END") {
			s.row("Version", "unknown, php didn't answer in 5s (container busy or restarting)")
		} else {
			s.row("Version", lines[0])
			// same check as the php.ini editor
			iniErr := ""
			for _, l := range sedRange(lines, eq("---INI"), eq("---MODS")) {
				if !strings.HasPrefix(l, "---") {
					iniErr = l
					break
				}
			}
			if iniErr != "" {
				s.row("php.ini", "error")
				s.err("Syntax error in the PHP " + v + " ini file: " + runeCut(iniErr, 150))
			} else {
				s.row("php.ini", "ok")
			}
			var mods []string
			for _, l := range sedRange(sedRange(lines, eq("---MODS"), eq("---END")), func(x string) bool { return strings.HasPrefix(x, "[PHP Modules]") }, eq("")) {
				if !strings.HasPrefix(l, "[") && l != "" {
					mods = append(mods, l)
				}
			}
			if strings.Contains(strings.ToLower(out), "ioncube") {
				s.row("ionCube Loader", "enabled")
			} else {
				s.row("ionCube Loader", "off")
			}
			s.rowList(fmt.Sprintf("Extensions (%d)", len(mods)), mods...)
		}

		// fpm warnings worth knowing about, max_children means requests queue up
		logs := trimNL(logOut[i])
		maxch := len(grepLines(logs, func(l string) bool { return strings.Contains(l, "pm.max_children") }))
		fatal := grepLines(logs, regexp.MustCompile(`PHP Fatal error|ERROR:|ALERT:`).MatchString)
		if maxch > 0 {
			s.warn(fmt.Sprintf("PHP %s hit pm.max_children %d time(s) in 6h, visitors had to wait, raise it in PHP options.", v, maxch))
		}
		// fpm's real reason is the ALERT line before the generic "failed to post process"
		if strings.Contains(strings.Join(fatal, "\n"), "FPM initialization failed") {
			why := ""
			for _, l := range fatal {
				if strings.Contains(l, "ALERT:") {
					why = l
				}
			}
			if why == "" {
				for _, l := range fatal {
					if !strings.Contains(l, "initialization failed") {
						why = l
					}
				}
			}
			s.err("PHP " + v + " can't start: " + cutC(sedFirst(bracketStrip, why, ""), 120))
		}
		s.row("Errors (6h)", fmt.Sprint(countLines(strings.Join(fatal, "\n"))))
		if len(fatal) > 0 {
			var st []string
			for _, l := range fatal {
				st = append(st, sedFirst(bracketStrip, l, ""))
			}
			s.topCounts(st, 110, 3)
		}
	}
}

const latestCache = "/tmp/openpanel-report-latest-versions.json"

func ghGet(url string) (string, bool) {
	c := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "OpenPanel")
	resp, err := c.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode >= 400 {
		return "", false
	}
	return string(b), true
}

// maxVersion is sort -V | tail -n1
func maxVersion(v []string) string {
	if len(v) == 0 {
		return ""
	}
	sort.SliceStable(v, func(i, j int) bool { return text.VersionLess(v[i], v[j]) })
	return v[len(v)-1]
}

func allSubmatch(re *regexp.Regexp, s string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// latest release per CMS from the same sources as the Sites page update check, cached 6h
func jobLatest() string {
	if st, err := os.Stat(latestCache); err == nil && st.Size() > 0 && time.Since(st.ModTime()) < 6*time.Hour {
		return trimNL(readFile(latestCache))
	}
	res := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	set := func(k, v string) {
		mu.Lock()
		res[k] = text.FirstLine(v)
		mu.Unlock()
	}
	jsonGet := func(url string) any {
		b, ok := ghGet(url)
		if !ok {
			return nil
		}
		v, _ := ojson.Parse(b)
		return v
	}
	strOf := func(v any) string {
		if v == nil || v == false {
			return ""
		}
		return ojson.Text(v)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		v := jsonGet("https://api.wordpress.org/core/version-check/1.7/")
		offers, _ := jqPath(v, "offers").([]any)
		if len(offers) > 0 {
			set("wordpress", strOf(jqPath(offers[0], "current")))
		}
	}()
	for _, p := range [][2]string{{"joomla", "joomla/joomla-cms"}, {"opencart", "opencart/opencart"}, {"prestashop", "PrestaShop/PrestaShop"}, {"matomo", "matomo-org/matomo"}} {
		wg.Add(1)
		go func(k, repo string) {
			defer wg.Done()
			set(k, strings.TrimPrefix(strOf(jqPath(jsonGet("https://api.github.com/repos/"+repo+"/releases/latest"), "tag_name")), "v"))
		}(p[0], p[1])
	}
	semverRe := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	for _, p := range [][2]string{{"drupal", "drupal/drupal"}, {"moodle", "moodle/moodle"}} {
		wg.Add(1)
		go func(k, repo string) {
			defer wg.Done()
			arr, _ := jsonGet("https://api.github.com/repos/" + repo + "/tags?per_page=100").([]any)
			var vs []string
			for _, t := range arr {
				n := strings.TrimPrefix(strOf(jqPath(t, "name")), "v")
				if semverRe.MatchString(n) {
					vs = append(vs, n)
				}
			}
			set(k, maxVersion(vs))
		}(p[0], p[1])
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		b, _ := ghGet("https://download.nextcloud.com/server/releases/")
		set("nextcloud", maxVersion(allSubmatch(regexp.MustCompile(`nextcloud-(\d+\.\d+\.\d+)\.zip"`), b)))
	}()
	go func() {
		defer wg.Done()
		b, _ := ghGet("https://releases.wikimedia.org/mediawiki/")
		branch := maxVersion(allSubmatch(regexp.MustCompile(`href="(\d+\.\d+)/"`), b))
		if branch != "" {
			p, _ := ghGet("https://releases.wikimedia.org/mediawiki/" + branch + "/")
			set("mediawiki", maxVersion(allSubmatch(regexp.MustCompile(`mediawiki-(\d+\.\d+\.\d+)\.tar\.gz"`), p)))
		}
	}()
	wg.Wait()
	var keys []string
	for k, v := range res {
		if v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	o := ojson.New()
	for _, k := range keys {
		o.Set(k, res[k])
	}
	if o.Len() == 0 {
		return "{}"
	}
	j := ojson.Pretty(o)
	os.WriteFile(latestCache, []byte(j+"\n"), 0o644)
	return j
}

// probe is one curl -w '%{http_code} %{time_total}' through the local caddy
func probe(site string, https bool) string {
	host := strings.SplitN(site, "/", 2)[0]
	port, scheme := "80", "http"
	if https {
		port, scheme = "443", "https"
	}
	d := &net.Dialer{}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, n, _ string) (net.Conn, error) {
			return d.DialContext(ctx, n, "127.0.0.1:"+port)
		},
		DisableCompression: true,
	}
	if https {
		tr.TLSClientConfig = insecureTLS()
		tr.TLSClientConfig.ServerName = host
	}
	c := &http.Client{Timeout: 5 * time.Second, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequest("GET", scheme+"://"+site+"/", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (OpenPanel report)")
	req.Header.Set("Accept", "text/html,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en")
	t := time.Now()
	resp, err := c.Do(req)
	code := "000"
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		code = fmt.Sprintf("%03d", resp.StatusCode)
	}
	return fmt.Sprintf("%s %.6f", code, time.Since(t).Seconds())
}

// wafBlockReason names the rules when a 403 right after our request is in the domain's waf log
func (s *sec) wafBlockReason(site string) {
	d := strings.SplitN(site, "/", 2)[0]
	lines := strings.Split(strings.TrimSuffix(readFile(wafLogDir+"/"+d+".log"), "\n"), "\n")
	lines = lines[max(0, len(lines)-5):]
	since := time.Now().Unix() - 60
	var last any
	for _, v := range jsonStream(strings.Join(lines, "\n")) {
		if jqTruthy(jqPath(v, "transaction", "is_interrupted")) && wafRecent(v, since) {
			last = v
		}
	}
	if last == nil {
		s.warn(site + " returns HTTP 403 on its homepage.")
		return
	}
	var ids []string
	msgs, _ := jqPath(last, "messages").([]any)
	idRe := regexp.MustCompile(`\[id "(\d+)`)
	skip := regexp.MustCompile(`^(949|959|980)`)
	for _, m := range msgs {
		em := jqPath(m, "error_message")
		if em == nil || em == false {
			continue
		}
		for _, id := range allSubmatch(idRe, ojson.Text(em)) {
			if !skip.MatchString(id) {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	s.err(site + " is blocked by the WAF for normal visitors (HTTP 403, rules " + or(strings.Join(uniq(ids), ","), "unknown") + ").")
}

func secWebsites(s *sec) {
	u, r := s.u, s.r
	s.section("WEBSITES")
	var rows []string
	for _, l := range strings.Split(u.siteRows, "\n") {
		f := strings.Split(l, "\t")
		x := strings.Join(f[:min(4, len(f))], "\t")
		if x != "" {
			rows = append(rows, x)
		}
	}
	if len(rows) == 0 {
		s.row("Websites", "none")
		return
	}
	latest := r.gLatest.get()
	latestObj, _ := ojson.ParseObject(latest)

	// every site fetched through the local caddy at once, "code seconds" per site
	results := make([]string, len(rows))
	var wg sync.WaitGroup
	for i, l := range rows {
		site := bread(l, "\t", 2)[0]
		wg.Add(1)
		go func(i int, site string) {
			defer wg.Done()
			// both at once, 5s each, http only counts when https gave nothing
			var hs, h string
			var w2 sync.WaitGroup
			w2.Add(2)
			go func() { defer w2.Done(); hs = probe(site, true) }()
			go func() { defer w2.Done(); h = probe(site, false) }()
			w2.Wait()
			res := hs
			if res == "" || strings.HasPrefix(res, "000") {
				res = h
				if res != "" && !strings.HasPrefix(res, "000") {
					res += " http"
				}
			}
			results[i] = res
		}(i, site)
	}
	wg.Wait()

	u.needPS()
	running := strings.Split(strings.ToLower(u.running), "\n")
	s.p("  %s%s %s %s %s %s%s\n", r.cDim, padR("Site", 40), padR("Type", 16), padR("Version", 14), padR("Latest", 12), "HTTP", r.cReset)
	var outdated, down []string
	for i, l := range rows {
		f := bread(l, "\t", 4)
		site, typ, ver, ctr := f[0], f[1], f[2], f[3]
		if ver == "NULL" {
			ver = ""
		}
		lat := ""
		if latestObj != nil {
			if v, _ := latestObj.Get(strings.ToLower(typ)); v != nil && v != false {
				lat = ojson.Text(v)
			}
		}
		rf := bread(results[i], " \t\n", 3)
		code, secs, proto := rf[0], rf[1], rf[2]
		http := ""
		if code == "" || code == "000" {
			http = "timeout"
		} else {
			sf := awkNum(secs)
			if sf < 10 {
				http = fmt.Sprintf("%s %.1fs", code, sf)
			} else {
				http = fmt.Sprintf("%s %.0fs", code, sf)
			}
			if proto != "" {
				http += " (http only)"
			}
		}
		s.p("  %s %s %s %s %s\n", padR(site, 40), padR(typ, 16), padR(runeCut(ver, 14), 14), padR(or(lat, "-"), 12), http)

		// sites with their own container, like node or python apps
		if ctr != "" {
			found := false
			for _, x := range running {
				if x == strings.ToLower(ctr) {
					found = true
				}
			}
			if !found {
				s.err(site + " app container " + strings.ToLower(ctr) + " isn't running.")
			}
		}
		if lat != "" && ver != "" && regexp.MustCompile(`^v?[0-9]`).MatchString(ver) {
			v := strings.TrimPrefix(ver, "v")
			if maxVersion([]string{v, lat}) != v {
				outdated = append(outdated, site+" ("+ver+" -> "+lat+")")
			}
		}
		switch {
		case code == "" || code == "000":
			down = append(down, site)
			s.err(site + " didn't answer in 10s.")
		case strings.HasPrefix(code, "5"):
			down = append(down, site)
			s.err(site + " returns HTTP " + code + ", the website is broken.")
		case code == "403":
			s.wafBlockReason(site)
		case strings.HasPrefix(code, "4"):
			s.warn(site + " returns HTTP " + code + " on its homepage.")
		}
		if proto == "http" {
			s.warn(site + " only answers over http, https hangs (see SSL).")
		}
	}

	s.line("")
	s.row("Websites", fmt.Sprintf("%d, %d answering", len(rows), len(rows)-len(down)))
	if len(outdated) > 0 {
		s.row("Updates available", fmt.Sprint(len(outdated)))
		for _, o := range outdated {
			s.line("    " + o)
		}
		s.warn(fmt.Sprintf("%d website(s) have updates available, outdated apps are the most common way sites get hacked.", len(outdated)))
	}
	if latest == "{}" {
		s.dim("Couldn't fetch latest versions, update check skipped.")
	}
}
