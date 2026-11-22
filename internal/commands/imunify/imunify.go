// ##############################################################################
// Script Name: internal/commands/imunify/imunify.go
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
	"fmt"
	"os"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/ojson"
	"opencli/internal/requirement"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "imunify",
		Desc:  "Install and manage ImunifyAV service.",
		Usage: "opencli imunify [status|start|stop|install|update|uninstall|hook]",
		Docs:  "https://docs.openpanel.com",
		Run:   imunify,
	})
}

const (
	iavHook     = "/etc/sysconfig/imunify360/iav_hook.sh"
	iavSpool    = "/var/spool/openpanel-imunify"
	iavUnit     = "openpanel-imunify-notify"
	panelGUI    = "/etc/systemd/system/imunify-gui-panel.service"
	panelGUIDir = "/etc/openpanel/imunify"
	servicesJS  = "/etc/openpanel/openadmin/config/services.json"
)

func writeFile(p, content string, mode os.FileMode) {
	os.WriteFile(p, []byte(content), mode)
}

// fixIntegration points integration.conf made by the bash opencli at the Go binary, install skips the file when it exists
func fixIntegration() {
	p := "/etc/sysconfig/imunify360/integration.conf"
	old := conf.Read(p)
	repl := strings.NewReplacer(
		"/usr/local/opencli/user/list.sh --json", "/usr/local/opencli/opencli user-list --json",
		"/usr/local/opencli/domains/all.sh --json", "/usr/local/opencli/opencli domains-all --json",
	)
	if s := repl.Replace(old); s != old {
		conf.WriteFileKeep(p, s, true)
	}
}

func updateVersion() {
	fixIntegration()
	p := "/etc/sysconfig/imunify360/get-panel-info.sh"
	writeFile(p, `#!/usr/bin/env bash
opencli_version="$(opencli version 2>/dev/null || echo unknown)"
cat <<JSON
{
  "data": {
    "name": "OpenPanel",
    "version": "$opencli_version"
  },
  "metadata": {
    "result": "ok"
  }
}
JSON
`, 0o644)
	os.Chmod(p, 0o755)
}

// imunify runs the hook as _imunify which can't use opencli, so it spools the event and a root path unit picks it up
func configureHook() {
	os.MkdirAll(iavSpool+"/tmp", 0o755)
	os.MkdirAll(iavSpool+"/new", 0o755)
	sh.C("chown", "-R", "_imunify:root", iavSpool).Run()
	sh.C("chmod", "-R", "0700", iavSpool).Run()

	writeFile(iavHook, "#!/bin/bash\ntmp=$(mktemp \""+iavSpool+"/tmp/XXXXXX\") || exit 1\ncat > \"$tmp\" && mv \"$tmp\" \""+iavSpool+"/new/${tmp##*/}.json\"\n", 0o644)
	os.Chmod(iavHook, 0o755)
	writeFile("/etc/systemd/system/"+iavUnit+".path", `[Unit]
Description=Pass ImunifyAV scan events to OpenPanel Sentinel

[Path]
DirectoryNotEmpty=`+iavSpool+`/new

[Install]
WantedBy=multi-user.target
`, 0o644)
	writeFile("/etc/systemd/system/"+iavUnit+".service", `[Unit]
Description=Pass ImunifyAV scan events to OpenPanel Sentinel

[Service]
Type=oneshot
ExecStart=/usr/local/bin/opencli imunify notify
`, 0o644)
	sh.C("systemctl", "daemon-reload").Run()
	sh.C("systemctl", "enable", "--now", iavUnit+".path").Run()

	// finished already carries the malicious files, so malware found would only duplicate it
	for _, r := range []string{"USER_SCAN_MALWARE_FOUND", "CUSTOM_SCAN_MALWARE_FOUND"} {
		sh.C("imunify-antivirus", "notifications-config", "update", `{"rules": {"`+r+`": {"SCRIPT": {"enabled": false}}}}`).Run()
	}
	for _, r := range []string{"USER_SCAN_STARTED", "CUSTOM_SCAN_STARTED", "USER_SCAN_FINISHED", "CUSTOM_SCAN_FINISHED"} {
		sh.C("imunify-antivirus", "notifications-config", "update", `{"rules": {"`+r+`": {"SCRIPT": {"scripts": ["`+iavHook+`"], "enabled": true}}}}`).Run()
	}
}

func removeHook() {
	sh.C("systemctl", "disable", "--now", iavUnit+".path").NoErr().Run()
	os.Remove("/etc/systemd/system/" + iavUnit + ".path")
	os.Remove("/etc/systemd/system/" + iavUnit + ".service")
	sh.C("systemctl", "daemon-reload").Run()
	os.RemoveAll(iavSpool)
}

func av(args ...string) { sh.C("imunify-antivirus", args...).Run() }

func configureLimits() {
	fmt.Println("Configuring ImunifyAV notifications to use 'OpenAdmin > Settings > Notifications'...")
	configureHook()
	// https://docs.imunifyav.com/config_file_description/
	av("config", "update", `{"MALWARE_SCANNING": {"hyperscan": true}}`)
	fmt.Println("Setting 2CPU and 1GB Memory limits for ImunifyAV service..")
	for _, c := range []string{
		`{"MALWARE_SCAN_INTENSITY": {"cpu": 2}}`,
		`{"MALWARE_SCAN_INTENSITY": {"io": 2}}`,
		`{"MALWARE_SCAN_INTENSITY": {"ram": 1024}}`,
		`{"MALWARE_SCAN_INTENSITY": {"user_scan_cpu": 2}}`,
		`{"MALWARE_SCAN_INTENSITY": {"user_scan_io": 2}}`,
		`{"MALWARE_SCAN_INTENSITY": {"user_scan_ram": 1024}}`,
		`{"RESOURCE_MANAGEMENT": {"cpu_limit": 1}}`,
		`{"RESOURCE_MANAGEMENT": {"io_limit": 1}}`,
		`{"RESOURCE_MANAGEMENT": {"ram_limit": 500}}`,
		// full scan on the 1st of every month at 3am
		`{"MALWARE_SCAN_SCHEDULE": {"interval": "month", "day_of_month": 1, "hour": 3}}`,
		`{"PERMISSIONS": {"allow_malware_scan": true}}`,
	} {
		av("config", "update", c)
	}
	fmt.Println("Adding Containers, Images and writable filesystems to ignored files list..")
	for _, p := range []string{"/home/*/docker-data/overlay", "/home/*/docker-data/containers", "/home/*/docker-data/image"} {
		sh.C("imunify360-agent", "malware", "ignore", "add", p).Run()
	}
	f, err := os.OpenFile("/etc/sysconfig/imunify360/malware-filters-admin-conf/ignored.txt", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err == nil {
		f.WriteString("^/home/(.*)/docker-data/containers/(.*)\n^/home/(.*)/docker-data/image/(.*)\n^/home/(.*)/docker-data/overlay/(.*)\n")
		f.Close()
	} else {
		fmt.Fprintln(os.Stderr, "/usr/local/opencli/imunify.sh: line 245: /etc/sysconfig/imunify360/malware-filters-admin-conf/ignored.txt: No such file or directory")
	}
	sh.C("imunify360-agent", "malware", "rebuild", "patterns").Run()
}

// the user panel's ImunifyAV page talks to the agent socket, servers installed before it don't mount it yet
func mountSocket() bool {
	compose := "/root/docker-compose.yml"
	line := "      - /run/defence360agent/:/run/defence360agent/:ro                                   # imunifyav rpc socket"
	sh.C("install", "-d", "-m", "755", "/run/defence360agent").Run()
	writeFile("/etc/tmpfiles.d/openpanel-imunify.conf", "d /run/defence360agent 0755 root root -\n", 0o644)
	if !conf.Exists(compose) {
		return false
	}
	content := conf.Read(compose)
	if strings.Contains(content, "/run/defence360agent") {
		return false
	}
	sock := "- /run/podman/podman.sock:/run/podman/podman.sock"
	n := 0
	for _, l := range conf.Lines(compose) {
		if strings.Contains(l, sock) {
			n++
		}
	}
	if n != 1 {
		fmt.Println("[!] Couldn't add the ImunifyAV socket to " + compose + ", mount /run/defence360agent/ into the openpanel service for the user panel's ImunifyAV page.")
		return false
	}
	fmt.Println("Mounting the ImunifyAV socket into the OpenPanel container..")
	conf.SedFile(compose, func(lines []string) []string {
		var out []string
		for _, l := range lines {
			out = append(out, l)
			if strings.Contains(l, sock) {
				out = append(out, line)
			}
		}
		return out
	})
	sh.C("podman-compose", "up", "-d", "openpanel").Dir("/root").Timeout(120 * 1e9).Quiet().Run()
	return true
}

var moduleRe = regexp.MustCompile(`^enabled_modules="?([^"]*,)?imunifyav(,|"|$)`)

// OpenAdmin > Settings > Modules turns the gui on and off with this module, like clamav with malware_scan
func moduleEnabled() bool {
	for _, l := range conf.Lines(conf.Panel) {
		if moduleRe.MatchString(l) {
			return true
		}
	}
	return false
}

// a fresh install turns the user panel page on, admins can still turn it off in OpenAdmin > Settings > Modules
func enablePanelModule() bool {
	if _, ok := conf.Raw(conf.Panel, "enabled_modules"); !ok || moduleEnabled() {
		return false
	}
	fmt.Println("Enabling the imunifyav module for the user panel..")
	quoted := regexp.MustCompile(`^enabled_modules=".*"`)
	q := false
	for _, l := range conf.Lines(conf.Panel) {
		if quoted.MatchString(l) {
			q = true
		}
	}
	qre := regexp.MustCompile(`^enabled_modules="(.*)"`)
	conf.SedFile(conf.Panel, func(lines []string) []string {
		for i, l := range lines {
			if q {
				if m := qre.FindStringSubmatchIndex(l); m != nil {
					l = `enabled_modules="` + l[m[2]:m[3]] + `,imunifyav"` + l[m[1]:]
				}
				if rest, ok := strings.CutPrefix(l, `enabled_modules=",imunifyav"`); ok {
					l = `enabled_modules="imunifyav"` + rest
				}
			} else if strings.HasPrefix(l, "enabled_modules=") {
				l += ",imunifyav"
				if l == "enabled_modules=,imunifyav" {
					l = "enabled_modules=imunifyav"
				}
			}
			lines[i] = l
		}
		return lines
	})
	return moduleEnabled()
}

// the user panel container can't reach 127.0.0.1 on the host, so it gets a second gui on its network's gateway
func panelGUIAddress() string {
	gw := strings.SplitN(sh.C("podman", "network", "inspect", "root_openpanel_network", "--format", "{{range .Subnets}}{{.Gateway}}{{end}}").NoErr().Str(), "\n", 2)[0]
	if gw == "" {
		return "172.20.0.1"
	}
	return gw
}

func which(name string) string {
	out, _ := sh.C("sh", "-c", "command -v "+name).Out()
	return out
}

// a real unit, not systemd-run, so the user panel's gui comes back after a reboot without anyone opening OpenAdmin
func setupPanelGUI() {
	php := which("php")
	if php == "" {
		fmt.Println("[!] PHP not found, skipping the ImunifyAV UI for the user panel.")
		return
	}
	gw := panelGUIAddress()
	// user containers can reach the gateway too, so the router only answers /imav/ with the key the panel sends
	sh.C("install", "-d", "-m", "750", "-o", "root", "-g", "_imunify", panelGUIDir).Run()
	if st, err := os.Stat(panelGUIDir + "/panel.key"); err != nil || st.Size() == 0 {
		writeFile(panelGUIDir+"/panel.key", randHex(32)+"\n", 0o640)
	}
	writeFile(panelGUIDir+"/router.php", `<?php
$key = trim((string) @file_get_contents('/etc/openpanel/imunify/panel.key'));
$got = isset($_SERVER['HTTP_X_OPENPANEL_KEY']) ? $_SERVER['HTTP_X_OPENPANEL_KEY'] : '';
$path = (string) parse_url(isset($_SERVER['REQUEST_URI']) ? $_SERVER['REQUEST_URI'] : '/', PHP_URL_PATH);
if ($key === '' || !hash_equals($key, $got) || strpos($path, '/imav/') !== 0) {
    http_response_code(403);
    exit;
}
return false;
`, 0o640)
	sh.C("chown", "root:_imunify", panelGUIDir+"/panel.key", panelGUIDir+"/router.php").Run()
	os.Chmod(panelGUIDir+"/panel.key", 0o640)
	os.Chmod(panelGUIDir+"/router.php", 0o640)
	writeFile(panelGUI, `[Unit]
Description=ImunifyAV UI for the OpenPanel user panel
After=network-online.target

[Service]
User=_imunify
ExecStart=`+php+` -S `+gw+`:9000 -t /etc/sysconfig/imunify360/ `+panelGUIDir+`/router.php
# the gateway only exists once podman brings the network up
Restart=always
RestartSec=10
StandardOutput=append:/var/log/imunify-php-server.log
StandardError=append:/var/log/imunify-php-server.log

[Install]
WantedBy=multi-user.target
`, 0o644)
	sh.C("systemctl", "daemon-reload").Run()
	if moduleEnabled() {
		// --now so an install from OpenAdmin, which never calls start, still brings it up
		sh.C("systemctl", "enable", "--now", "imunify-gui-panel").Quiet().Run()
		fmt.Println("ImunifyAV UI for the user panel listens on " + gw + ":9000.")
	} else {
		sh.C("systemctl", "disable", "--now", "imunify-gui-panel").Quiet().Run()
		fmt.Println("Enable the imunifyav module in OpenAdmin > Settings > Modules to give users the ImunifyAV page.")
	}
}

func status() {
	if sh.C("pgrep", "-u", "_imunify", "-f", "php -S 127.0.0.1:9000").NoOut().OK() {
		fmt.Println("Imunify GUI is running.")
		sh.C("pgrep", "-u", "_imunify", "-fa", "php -S 127.0.0.1:9000").Run()
	} else {
		fmt.Println("Imunify GUI is not running.")
	}
	if sh.C("systemctl", "is-active", "--quiet", "imunify-gui-panel").NoErr().OK() {
		fmt.Println("Imunify GUI for the user panel is running.")
	} else {
		fmt.Println("Imunify GUI for the user panel is not running.")
	}
}

func uninstall() {
	removeHook()
	fmt.Println("Removing files and directories...")
	os.RemoveAll("/etc/sysconfig/imunify360/")
	os.Remove("/etc/pam.d/imunify360-deny")
	os.Remove("imav-deploy.sh")
	os.Remove("/var/log/imunify-php-server.log")
	sh.C("systemctl", "disable", "imunify-gui-panel").Quiet().Run()
	os.Remove(panelGUI)
	os.RemoveAll(panelGUIDir)
	sh.C("systemctl", "daemon-reload").Run()

	fmt.Println("Checking for _imunify user...")
	if sh.C("id", "_imunify").Quiet().OK() {
		fmt.Println("User '_imunify' exists. Not removing automatically to avoid breaking dependencies.")
		fmt.Println("→ If you're sure, run: userdel _imunify")
	}
	fmt.Println("Removing ImunifyAV from OpenAdmin > Services Status...")
	v, err := ojson.Parse(conf.Read(servicesJS))
	arr, ok := v.([]any)
	if err != nil || !ok {
		if !conf.Exists(servicesJS) {
			fmt.Fprintf(os.Stderr, "jq: error: Could not open %s: No such file or directory\n", servicesJS)
		}
		fmt.Println("Failed to parse or update " + servicesJS + ". Check JSON format.")
	} else {
		out := []any{}
		for _, it := range arr {
			if o, ok := it.(*ojson.Object); ok {
				if n, has := o.Get("name"); has && n == "ImunifyAV" {
					continue
				}
			}
			out = append(out, it)
		}
		writeFile(servicesJS, ojson.Pretty(out)+"\n", 0o644)
		fmt.Println("Service 'ImunifyAV' removed successfully.")
	}
	fmt.Println("Uninstall complete.")
}

func start() {
	sh.C("pkill", "-u", "_imunify", "-f", "php -S 127.0.0.1:9000").NoErr().Run()
	sh.C("systemctl", "stop", "imunify-gui").NoErr().Run()
	sh.C("chown", "-R", "_imunify", "/etc/sysconfig/imunify360/").Run()
	if !sh.C("pgrep", "-f", "php -S 127.0.0.1:9000").NoOut().OK() {
		// its own unit, so restarting the admin service (which may have called this) doesn't take the gui down with it
		if sh.Has("systemd-run") {
			sh.C("systemd-run", "--unit=imunify-gui", "--collect", "--quiet", "-p", "User=_imunify",
				"-p", "StandardOutput=append:/var/log/imunify-php-server.log", "-p", "StandardError=append:/var/log/imunify-php-server.log",
				which("php"), "-S", "127.0.0.1:9000", "-t", "/etc/sysconfig/imunify360/").Run()
		} else {
			sh.Bash("nohup sudo -u _imunify php -S 127.0.0.1:9000 -t /etc/sysconfig/imunify360/ > /var/log/imunify-php-server.log 2>&1 &").Run()
		}
		fmt.Println("ImunifyAV GUI started.")
	} else {
		fmt.Println("ImunifyAV GUI already running.")
	}
	// servers that installed imunify before the user panel page get the mount when the module is turned on
	if moduleEnabled() {
		mountSocket()
	}
	setupPanelGUI()
	if conf.Exists(panelGUI) && moduleEnabled() {
		sh.C("systemctl", "restart", "imunify-gui-panel").Run()
	}
}

func stop() {
	sh.C("systemctl", "stop", "imunify-gui").NoErr().Run()
	sh.C("systemctl", "stop", "imunify-gui-panel").NoErr().Run()
	sh.C("pkill", "-f", "php -S 127.0.0.1:9000").Run()
	if !sh.C("pgrep", "-f", "php -S 127.0.0.1:9000").NoOut().OK() {
		fmt.Println("ImunifyAV GUI disabled.")
	} else {
		fmt.Println("Failed to disable ImunifyAV GUI.")
	}
}

func update() {
	// https://docs.imunify360.com/imunifyav/#update-instructions
	fmt.Println("Updating ImunifyAV...")
	switch {
	case sh.Has("apt-get"):
		sh.C("apt-get", "update").Run()
		sh.C("apt-get", "install", "--only-upgrade", "-y", "imunify-antivirus").Run()
	case sh.Has("yum"):
		sh.C("yum", "update", "-y", "imunify-antivirus").Run()
	default:
		fmt.Println("Unsupported package manager. Only apt-get and yum are supported.")
		cli.Exit(1)
	}
	// servers installed before the spool hook still have the old email-only one, which never reaches Sentinel
	setupHook()
	fixIntegration()
}

// opencli imunify hook: (re)installs the scan event hook, safe to run any time
func setupHook() int {
	if !sh.Has("imunify-antivirus") {
		fmt.Println("ImunifyAV is not installed.")
		return 1
	}
	fmt.Println("Configuring ImunifyAV scan notifications for OpenPanel Sentinel...")
	configureHook()
	fmt.Println("Done: scan events go to " + iavSpool + " and '" + iavUnit + "' passes them to Sentinel.")
	return 0
}

func imunify(args []string) int {
	requirement.Command("jq")
	switch cli.Arg(args, 0) {
	case "status":
		status()
	case "install":
		fmt.Println("Installing ImunifyAV...")
		install()
	case "uninstall":
		fmt.Println("Uninstalling ImunifyAV...")
		stop()
		uninstall()
	case "start":
		fmt.Println("Starting ImunifyAV...")
		updateVersion()
		start()
	case "stop":
		fmt.Println("Stopping ImunifyAV...")
		stop()
	case "update":
		update()
	case "notify":
		notify()
	case "hook":
		return setupHook()
	default:
		fmt.Println("Usage: opencli imunify {status|install|update|uninstall|start|stop|hook}")
		return 1
	}
	return 0
}
