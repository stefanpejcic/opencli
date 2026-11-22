// ##############################################################################
// Script Name: internal/commands/imunify/imunify_install.go
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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
)

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func install() {
	if out, _ := sh.C("uname", "-m").Out(); !strings.Contains(out, "x86_64") && !strings.Contains(out, "amd64") {
		fmt.Println("ABORTING: ImunifyAV does NOT support your CPU architecture, only supports: AMD/x86")
		cli.Exit(1)
	}
	fmt.Println("Creating directories...")
	os.MkdirAll("/etc/sysconfig/imunify360/", 0o755)

	pam := "/etc/pam.d/imunify360-deny"
	if !conf.IsFile(pam) {
		fmt.Println("Creating pam_deny.so file...")
		writeFile(pam, "auth required pam_deny.so\naccount required pam_deny.so\n", 0o644)
	} else {
		fmt.Println(pam + " already exists, skipping...")
	}
	integ := "/etc/sysconfig/imunify360/integration.conf"
	if !conf.IsFile(integ) {
		fmt.Println("Creating integration.conf file...")
		writeFile(integ, `[paths]
ui_path = /etc/sysconfig/imunify360/imav
ui_path_owner = _imunify:_imunify 

[pam]
service_name = imunify360-deny

[integration_scripts]
panel_info = /etc/sysconfig/imunify360/get-panel-info.sh
users = /usr/local/opencli/opencli user-list --json
domains = /usr/local/opencli/opencli domains-all --json

[malware]
basedir = /home
pattern_to_watch = ^/home/[^/]+/docker-data/volumes/[^/]+_html_data/_data(/.*)?$
`, 0o644)
	} else {
		fmt.Println(integ + " already exists, skipping...")
	}
	updateVersion()

	deploy := "imav-deploy.sh"
	if !conf.IsFile(deploy) {
		fmt.Println("Downloading deploy script...")
		sh.C("wget", "--timeout=5", "--tries=3", "--inet4-only", "https://repo.imunify360.cloudlinux.com/defence360/imav-deploy.sh", "-O", deploy).Run()
	} else {
		fmt.Println(deploy + " already downloaded, skipping...")
	}
	if !strings.Contains(conf.Read(deploy), "# Deployed by imav-deploy") {
		fmt.Println("Running deploy script...")
		if sh.C("bash", deploy).Run() != nil {
			fmt.Println()
			fmt.Fprintln(os.Stderr, "[ERROR] Installing ImunifyAV failed - please check the above output.")
			fmt.Println()
			cli.Exit(1)
		}
	} else {
		fmt.Println("Deploy script already executed or invalid, skipping...")
	}

	// uninstall removes the ui files but keeps the packages, so the deploy script won't put them back
	if ents, _ := os.ReadDir("/etc/sysconfig/imunify360/imav"); len(ents) == 0 {
		fmt.Println("Reinstalling the ImunifyAV UI...")
		os.MkdirAll("/etc/sysconfig/imunify360/imav", 0o755)
		if sh.Has("dnf") {
			sh.C("dnf", "reinstall", "-y", "imunify-ui-generic").Run()
		} else if sh.Has("apt-get") {
			sh.C("apt-get", "install", "-y", "--reinstall", "imunify-ui-generic").Run()
		}
	}
	// same for the config layers, without 90-local.config imunify ignores every setting below
	layers := "/etc/sysconfig/imunify360/imunify360.config.d"
	if _, err := os.Lstat(layers + "/90-local.config"); err != nil {
		fmt.Println("Restoring the ImunifyAV config layers...")
		sh.C("install", "-d", "-m", "700", "-o", "_imunify", "-g", "root", layers).Run()
		os.Remove(layers + "/90-local.config")
		os.Symlink("../imunify360.config", layers+"/90-local.config")
		sh.C("chown", "-h", "_imunify:root", layers+"/90-local.config").Run()
	}

	configureLimits()
	fmt.Println("Allowing users to initiate a scan..")
	sh.C("imunify360-agent", "config", "update", `{"PERMISSIONS": {"allow_malware_scan": true}}`).Run()
	av("config", "update", `{"PERMISSIONS": {"allow_malware_scan": true}}`)

	fmt.Println("Installing PHP if not present...")
	if !sh.Has("php") {
		switch {
		case sh.Has("apt-get"):
			sh.C("apt-get", "update").Run()
			sh.C("apt-get", "install", "-y", "php-cli").Run()
		case sh.Has("yum"):
			sh.C("yum", "update").Run()
			sh.C("yum", "install", "-y", "php-cli").Run()
		default:
			fmt.Println("Unsupported package manager. Only apt-get and yum are supported.")
			cli.Exit(1)
		}
	} else {
		fmt.Println("PHP already installed.")
	}

	// add it to openadmin > services > status
	addService()

	moduleOn := enablePanelModule()
	recreated := mountSocket()
	setupPanelGUI()
	// openpanel reads enabled_modules on start, a recreate for the mount already covered it
	if moduleOn && !recreated {
		for _, n := range strings.Split(sh.C("podman", "ps", "--format", "{{.Names}}").Str(), "\n") {
			if n == "openpanel" {
				sh.C("podman", "restart", "-t", "10", "openpanel").Quiet().Run()
				break
			}
		}
	}
	fmt.Println("Install completed!")
}

func addService() {
	newService := `    {
        "name": "ImunifyAV",
        "type": "system",
        "real_name": "imunify-antivirus"
    }`
	lines := conf.Lines(servicesJS)
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "]") {
		if !conf.Exists(servicesJS) {
			fmt.Fprintf(os.Stderr, "tail: cannot open '%s' for reading: No such file or directory\n", servicesJS)
		}
		fmt.Println("Invalid JSON format in " + servicesJS)
		return
	}
	lines = lines[:len(lines)-1]
	// add comma to the last existing object if needed
	if n := len(lines); n > 0 && !strings.Contains(lines[n-1], "},") {
		lines[n-1] = strings.Replace(lines[n-1], "}", "},", 1)
	}
	lines = append(lines, newService, "]")
	writeFile(servicesJS+".tmp", strings.Join(lines, "\n")+"\n", 0o644)
	os.Rename(servicesJS+".tmp", servicesJS)
	fmt.Println("New service added successfully.")
}
