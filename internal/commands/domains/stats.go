// ##############################################################################
// Script Name: internal/commands/domains/stats.go
// Description: Parse caddy access logs for users domains and generate static html
// Usage: opencli domains-stats [USERNAME] [--debug]
// Author: Stefan Pejcic
// Created: 14.12.2023
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

package domains

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-stats",
		Desc:  "Parse caddy access logs for users domains and generate static html",
		Usage: "opencli domains-stats [USERNAME] [--debug]",
		Run:   stats,
	})
}

const statsConfDir = "/etc/openpanel/goaccess"

func stats(args []string) int {
	debug, single, username := false, false, ""
	for _, a := range args {
		if a == "--debug" {
			debug = true
		} else {
			single = true
			username = a
		}
	}

	// shared lock like flock -s, it only waits for an exclusive holder
	if lock, err := os.OpenFile("/var/lock/caddy_stats.lock", os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
		defer lock.Close()
		syscall.Flock(int(lock.Fd()), syscall.LOCK_SH)
	}

	// added in 0.2.8
	if !strings.Contains(cutField2(conf.Panel, "enabled_modules"), "goaccess") {
		fmt.Println("'goaccess' module is not enabled. Skipping report generation.")
		return 1
	}

	statsConfigure()

	if single {
		statsProcess(username, debug)
		return 0
	}
	out, _ := sh.Opencli("user-list", "--local", "--json").Timeout(20 * time.Second).Out()
	var names []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "SUSPENDED") || !strings.Contains(l, "username") {
			continue
		}
		f := strings.Split(l, `"`)
		if len(f) > 3 {
			names = append(names, f[3])
		}
	}
	for _, u := range strings.Fields(strings.Join(names, "\n")) {
		statsProcess(u, debug)
	}
	return 0
}

// setup database and data for container
func statsConfigure() {
	sh.C("tar", "-xzvf", statsConfDir+"/GeoLite2-City_20231219.tar.gz", "-C", statsConfDir+"/").NoOut().Run()
	src := statsConfDir + "/GeoLite2-City_20231219/"
	dst := "/usr/local/share/GeoIP/GeoLite2-City_20231219"
	os.MkdirAll(dst, 0o755)
	entries, _ := filepath.Glob(src + "*")
	if len(entries) == 0 {
		fmt.Fprintf(os.Stderr, "cp: cannot stat '%s*': No such file or directory\n", src)
		return
	}
	for _, e := range entries {
		statsCopy(e, dst+"/"+filepath.Base(e))
	}
}

func statsCopy(src, dst string) {
	st, err := os.Stat(src)
	if err != nil {
		return
	}
	if st.IsDir() {
		os.MkdirAll(dst, 0o755)
		ents, _ := os.ReadDir(src)
		for _, e := range ents {
			statsCopy(src+"/"+e.Name(), dst+"/"+e.Name())
		}
		return
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return
	}
	os.WriteFile(dst, b, st.Mode().Perm()&^fs.FileMode(0o022))
}

func statsProcess(username string, debug bool) {
	excludedFile := "/etc/openpanel/openpanel/core/users/" + username + "/domains/excluded_ips_for_goaccess"
	excluded := ""
	if st, err := os.Stat(excludedFile); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
		excluded = strings.TrimRight(conf.Read(excludedFile), "\n")
	}

	domains, _ := sh.Opencli("domains-user", username).Out()
	if strings.Contains(domains, "No domains found for user '"+username+"'") {
		fmt.Printf("No domains found for user %s. Skipping.\n", username)
		return
	}
	for _, domain := range strings.Fields(domains) {
		logFile := "/var/log/caddy/domlogs/" + domain + "/access.log"
		outputDir := "/var/log/caddy/stats/" + username + "/"
		htmlOutput := outputDir + "/" + domain + ".html"

		os.MkdirAll(outputDir, 0o755)
		if st, err := os.Stat(logFile); err == nil && st.Size() > 0 {
			f, err := os.Create(htmlOutput)
			if err == nil {
				// nice and the io class carry through conmon into the goaccess container
				sh.LowPrio("podman", "run", "--memory=256m", "--cpus=0.5",
					"-v", "/usr/local/share/GeoIP/GeoLite2-City_20231219/GeoLite2-City.mmdb:/GeoLite2-City.mmdb",
					"-v", logFile+":/var/log/caddy/access.log",
					"-v", outputDir+":"+outputDir,
					"--rm", "-i", "-e", "LANG=EN", "allinurl/goaccess",
					"-e", excluded, "--ignore-panel=KEYPHRASES", "-a", "-o", "html",
					"--log-format=CADDY", "/var/log/caddy/access.log").StdinReader(os.Stdin).Stdout(f).Run()
				f.Close()
			}
			if b, err := os.ReadFile(htmlOutput); err == nil {
				conf.WriteFileKeep(htmlOutput, strings.ReplaceAll(string(b), "Dashboard", domain), false)
			}
			if debug {
				fmt.Printf("Processed domain %s for user %s with IP exclusions\n", domain, username)
			} else {
				fmt.Printf("Processed domain %s for user %s\n", domain, username)
			}
		} else {
			fmt.Printf("Skipped empty log file for domain %s of user %s\n", domain, username)
		}
	}
}
