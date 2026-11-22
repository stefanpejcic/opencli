// ##############################################################################
// Script Name: internal/commands/waf/waf_crs.go
// Description: Manage CorazaWAF
// Usage: opencli waf <status|enable|disable|domain|user|plugins|tags|ids|update|stats|count> [options]
// Author: Stefan Pejcic
// Created: 22.05.2025
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

package waf

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

const pluginsDir = "/etc/openpanel/caddy/coreruleset-plugins"

// official CRS rule exclusion plugins users can turn on per domain as app profiles in OpenPanel
var crsPlugins = []string{"wordpress", "drupal", "nextcloud", "dokuwiki", "phpbb", "xenforo", "phpmyadmin"}

// rule sets for stacks we don't run (IIS) or only rarely host (Java, Ruby)
var unusedRulesets = []string{
	"REQUEST-944-APPLICATION-ATTACK-JAVA",
	"RESPONSE-952-DATA-LEAKAGES-JAVA",
	"RESPONSE-954-DATA-LEAKAGES-IIS",
	"RESPONSE-956-DATA-LEAKAGES-RUBY",
}

func disableUnusedRulesets() {
	if !conf.IsDir(crsDir + "/rules") {
		return
	}
	for _, name := range unusedRulesets {
		f := crsDir + "/rules/" + name + ".conf"
		if conf.IsFile(f) {
			os.Rename(f, f+".disabled")
			fmt.Println("- Disabled ruleset " + name)
		}
		sh.C("git", "-C", crsDir, "update-index", "--assume-unchanged", "rules/"+name+".conf").NoErr().Run()
	}
}

// git pull can bring back a ruleset the admin disabled, drop the original again so it stays off
func keepDisabledRulesets() {
	files, _ := filepath.Glob(crsDir + "/rules/*.conf.disabled")
	for _, f := range files {
		if !conf.IsFile(f) {
			continue
		}
		orig := strings.TrimSuffix(f, ".disabled")
		if conf.IsFile(orig) {
			os.Rename(orig, f)
			fmt.Println("- Kept ruleset " + filepath.Base(orig) + " disabled")
		}
	}
}

func pluginDir(name string) string { return pluginsDir + "/" + name + "-rule-exclusions-plugin" }

func installPlugins() bool {
	os.MkdirAll(pluginsDir, 0o755)
	ok := true
	for _, name := range crsPlugins {
		dir := pluginDir(name)
		if conf.IsFile(dir + "/plugins/" + name + "-rule-exclusions-before.conf") {
			continue
		}
		os.RemoveAll(dir)
		if sh.C("git", "clone", "--quiet", "--depth", "1", "https://github.com/coreruleset/"+name+"-rule-exclusions-plugin", dir).Run() == nil {
			fmt.Println("- Installed plugin " + name + "-rule-exclusions")
		} else {
			fmt.Println("- Failed to download plugin " + name + "-rule-exclusions")
			ok = false
		}
	}
	return ok
}

func updatePlugins() {
	installPlugins()
	dirs, _ := filepath.Glob(pluginsDir + "/*-rule-exclusions-plugin")
	for _, dir := range dirs {
		if !conf.IsDir(dir + "/.git") {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(dir), "-plugin")
		if sh.C("git", "-C", dir, "pull", "--quiet").Run() == nil {
			fmt.Println("- Updated plugin " + name)
		} else {
			fmt.Println("- Failed to update plugin " + name)
		}
	}
}

func listPlugins() {
	fmt.Printf("%-14s %-10s %s\n", "PLUGIN", "STATUS", "DOMAINS")
	confs, _ := filepath.Glob(domainsDir + "/*.conf")
	for _, name := range crsPlugins {
		needle := "/" + name + "-rule-exclusions-plugin/plugins/"
		count := 0
		for _, c := range confs {
			if strings.Contains(conf.Read(c), needle) {
				count++
			}
		}
		status := "missing"
		if conf.IsFile(pluginDir(name) + "/plugins/" + name + "-rule-exclusions-before.conf") {
			status = "installed"
		}
		fmt.Printf("%-14s %-10s %d\n", name, status, count)
	}
}

var (
	paranoiaRe  = regexp.MustCompile(`blocking_paranoia_level=([0-9]+)`)
	thresholdRe = regexp.MustCompile(`inbound_anomaly_score_threshold=([0-9]+)`)
	profileRe   = regexp.MustCompile(`/([a-z0-9]+)-rule-exclusions-plugin/plugins/`)
)

// profiles is grep -oP '/\K[a-z0-9]+(?=-rule-exclusions-plugin/plugins/)', the lookahead part isn't consumed
func profiles(lines []string) []string {
	var out []string
	for _, l := range lines {
		pos := 0
		for pos <= len(l) {
			m := profileRe.FindStringSubmatchIndex(l[pos:])
			if m == nil {
				break
			}
			out = append(out, l[pos+m[2]:pos+m[3]])
			pos += m[3]
		}
	}
	return out
}

func listDomainPlugins(domain string) {
	file := domainFile(domain)
	if !conf.IsFile(file) {
		fmt.Println("Domain not found!")
		cli.Exit(1)
	}
	lines := conf.Lines(file)
	// level SecAction written by OpenPanel > WAF, no line means the CRS default
	level := "standard"
	for _, l := range lines {
		if !strings.Contains(l, "id:10100,") {
			continue
		}
		key := strings.Join(grepO([]string{l}, paranoiaRe), "\n") + "/" + strings.Join(grepO([]string{l}, thresholdRe), "\n")
		switch key {
		case "1/10":
			level = "compatibility"
		case "1/5":
			level = "standard"
		case "2/5":
			level = "strict"
		default:
			level = "custom"
		}
		break
	}
	fmt.Printf("Protection level for domain %s: %s\n", domain, level)
	plugins := sortU(profiles(lines))
	if len(plugins) == 0 {
		fmt.Println("No app profiles active for domain " + domain)
		return
	}
	fmt.Println("App profiles active for domain " + domain + ":")
	for _, p := range plugins {
		fmt.Println(" - " + p)
	}
}

func updateRules() {
	if os.Chdir(crsDir+"/") != nil {
		fmt.Println("Failed to enter modsec directory: /etc/openpanel/caddy/coreruleset/")
		return
	}
	disabled, _ := filepath.Glob("rules/*.conf.disabled")
	if len(disabled) == 0 {
		// no nullglob, so the loop ran once with the pattern itself
		disabled = []string{"rules/*.conf.disabled"}
	}
	for _, f := range disabled {
		orig := strings.TrimSuffix(f, ".disabled")
		fmt.Println("- Excluding disabled ruleset " + orig)
		sh.C("git", "update-index", "--assume-unchanged", orig).Run()
	}
	fmt.Println("Updating OWASP CRS..")
	if sh.C("git", "pull", "--quiet").Run() != nil {
		fmt.Println("Update failed.")
		return
	}
	keepDisabledRulesets()
	fmt.Println("Update successful.")
	fmt.Println("Updating CRS plugins..")
	updatePlugins()
	reloadCaddyNow()
}

// download is wget --timeout=15 --tries=3 --inet4-only url -O file
func download(url, file string) bool {
	d := &net.Dialer{Timeout: 15 * time.Second}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return d.DialContext(ctx, "tcp4", addr)
		},
		ResponseHeaderTimeout: 15 * time.Second,
	}}
	f, err := os.Create(file)
	if err != nil {
		return false
	}
	defer f.Close()
	for try := 1; try <= 3; try++ {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			// wget doesn't retry a 404
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return false
			}
			continue
		}
		f.Truncate(0)
		f.Seek(0, 0)
		_, err = io.Copy(f, resp.Body)
		resp.Body.Close()
		if err == nil {
			return true
		}
	}
	return false
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

func enable() int {
	rules := "/etc/openpanel/caddy/coraza_rules.conf"
	tmp := rules + ".tmp"
	// 1. download rules
	fmt.Println("Downloading Coraza rules..")
	// download to a temp file so a failed download doesn't leave an empty rules file behind
	if download("https://raw.githubusercontent.com/stefanpejcic/openpanel-configuration/refs/heads/main/caddy/coraza_rules.conf", tmp) && fileSize(tmp) > 0 {
		// written into the existing file, caddy sees it through a single-file bind mount and a reload has to find the new rules
		conf.WriteInPlace(rules, conf.Read(tmp))
		os.Remove(tmp)
	} else {
		os.Remove(tmp)
		if fileSize(rules) == 0 {
			fmt.Fprintln(os.Stderr, "Error: failed to download Coraza rules.")
			return 1
		}
		fmt.Println("Download failed, keeping the existing Coraza rules.")
	}

	fmt.Println("Downloading OWASP CRS..")
	// enable can run again, e.g. after a failed install, so reuse a clone that's already there
	if conf.IsDir(crsDir + "/.git") {
		fmt.Println("OWASP CRS is already downloaded.")
	} else {
		os.RemoveAll(crsDir)
		for try := 1; try <= 3; try++ {
			if sh.C("git", "clone", "https://github.com/coreruleset/coreruleset", crsDir).Run() == nil {
				break
			}
			os.RemoveAll(crsDir)
			time.Sleep(5 * time.Second)
		}
		if !conf.IsDir(crsDir + "/.git") {
			fmt.Fprintln(os.Stderr, "Error: failed to download OWASP CRS into "+crsDir)
			return 1
		}
	}

	// avoid SecDefaultAction collision with coraza_rules.conf (which sets phase:3/4/5)
	defRe := regexp.MustCompile(`^(SecDefaultAction "phase:[345])`)
	conf.SedFile(crsDir+"/crs-setup.conf.example", func(lines []string) []string {
		for i, l := range lines {
			lines[i] = defRe.ReplaceAllString(l, "#$1")
		}
		return lines
	})

	fmt.Println("Disabling rulesets not used by the stack..")
	disableUnusedRulesets()
	fmt.Println("Downloading CRS plugins for app profiles..")
	installPlugins()

	// 2. enable module
	fmt.Println("Enabling WAF module..")
	conf.SedFile(conf.Panel, func(lines []string) []string {
		for i, l := range lines {
			if strings.HasPrefix(l, "enabled_modules=") && !strings.Contains(l, "waf") {
				lines[i] = l + ",waf"
			}
		}
		return lines
	})

	// 3. set image to openpanel/caddy-coraza
	fmt.Println("Setting container image 'openpanel/caddy-coraza'..")
	imgRe := regexp.MustCompile(`^CADDY_IMAGE=".*"`)
	conf.SedFile(conf.RootEnv, func(lines []string) []string {
		for i, l := range lines {
			lines[i] = imgRe.ReplaceAllLiteralString(l, customImage)
		}
		return lines
	})

	// 4. enable WAF for all user domains, except users that turned it off
	skip := map[string]bool{}
	for _, d := range wafDisabledDomains() {
		skip[d] = true
	}
	confs, _ := filepath.Glob(domainsDir + "/*.conf")
	for _, c := range confs {
		if !conf.IsFile(c) || skip[strings.TrimSuffix(filepath.Base(c), ".conf")] {
			continue
		}
		conf.SedFile(c, func(lines []string) []string {
			for i, l := range lines {
				lines[i] = strings.ReplaceAll(l, "SecRuleEngine Off", "SecRuleEngine On")
			}
			return lines
		})
	}

	// 5. a caddy already on the coraza image (disable keeps it) only needs a reload, only a new image needs the container recreated
	if podman.IsRunning("caddy") && strings.Contains(sh.C("podman", "inspect", "-f", "{{.ImageName}}", "caddy").NoErr().Str(), "openpanel/caddy-coraza") {
		fmt.Println("Reloading Web Server with CorazaWAF..")
		if !podman.CaddyReload() {
			return 1
		}
	} else {
		fmt.Println("Restarting Web Server to use the new image with CorazaWAF..")
		if !sh.C("podman-compose", "down", "caddy").Dir("/root").OK() || !sh.C("podman-compose", "up", "-d", "caddy").Dir("/root").OK() {
			fmt.Fprintln(os.Stderr, "Error: failed to restart the caddy container.")
			return 1
		}
	}
	sentinel("waf_status", "WAF configured", "CorazaWAF has been configured on the server and WAF will be auto-enabled for new domains.")
	// 6. check status
	checkStatus()
	return 0
}

func stdinIsTerminal() bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, 0, syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

var (
	activeRe = regexp.MustCompile(`SecRuleEngine (On|DetectionOnly)`)
	wafRe1   = regexp.MustCompile(`\b,waf\b`)
	wafRe2   = regexp.MustCompile(`\bwaf,`)
	wafRe3   = regexp.MustCompile(`\bwaf\b`)
)

func disable(flag string) {
	// 1. check if used and ask for confirmation
	fmt.Println("Checking if CorazaWAF is used by any user domains..")
	confs, _ := filepath.Glob(domainsDir + "/*.conf")
	var active []string
	for _, c := range confs {
		if activeRe.MatchString(conf.Read(c)) {
			active = append(active, c)
		}
	}
	if len(active) > 0 {
		fmt.Println("WARNING: WAF is still active on some domains:")
		for _, f := range active {
			fmt.Println(" - " + strings.TrimSuffix(filepath.Base(f), ".conf"))
		}
		if flag == "-y" {
			fmt.Println("Disabling Coraza WAF...")
		} else {
			if stdinIsTerminal() {
				fmt.Fprint(os.Stderr, "Do you really want to disable WAF protection on these domains and stop using Coraza WAF on the server? [y/N]: ")
			}
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			switch strings.ToLower(strings.TrimRight(line, "\n")) {
			case "yes", "y":
				fmt.Println("Disabling Coraza WAF...")
			default:
				fmt.Println("Aborting.")
				return
			}
		}
	}

	// 2. disable module
	fmt.Println("Disabling WAF module...")
	conf.SedFile(conf.Panel, func(lines []string) []string {
		for i, l := range lines {
			l = wafRe1.ReplaceAllString(l, "")
			l = wafRe2.ReplaceAllString(l, "")
			lines[i] = wafRe3.ReplaceAllString(l, "")
		}
		return lines
	})

	// 3. disable WAF for ALL user domains
	if len(confs) == 0 {
		fmt.Fprintf(os.Stderr, "sed: can't read %s/*.conf: No such file or directory\n", domainsDir)
	}
	for _, c := range confs {
		conf.SedFile(c, func(lines []string) []string {
			for i, l := range lines {
				lines[i] = activeRe.ReplaceAllString(l, "SecRuleEngine Off")
			}
			return lines
		})
	}

	// 4. reload caddy to apply
	reloadCaddyNow()
	sentinel("waf_status", "WAF unconfigured", "CorazaWAF has been unconfigured on the server and WAF will be auto-disabled for new domains.")
	// 5. check status
	checkStatus()
}
