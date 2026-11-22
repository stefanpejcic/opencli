// ##############################################################################
// Script Name: internal/commands/server/logrotate.go
// Description: Configures logrotate for caddy, openpanel, syslog and limits the journald size.
// Usage: opencli server-logrotate
// Author: Stefan Pejcic
// Created: 16.01.2024
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

package server

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/requirement"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "server-logrotate",
		Desc:  "Configures logrotate for caddy, openpanel, syslog and limits the journald size.",
		Usage: "opencli server-logrotate",
		Run:   logrotate,
	})
}

// same as grep "^key=" | cut -d= -f2 | tr -d ' ', the default never kicks in since tr always succeeds
func logrotateConf(key string) string {
	var vals []string
	for _, l := range conf.Lines(conf.Panel) {
		if strings.HasPrefix(l, key+"=") {
			f := strings.Split(l, "=")
			vals = append(vals, strings.ReplaceAll(f[1], " ", ""))
		}
	}
	return strings.Join(vals, "\n")
}

// grepR is grep -rq, symlinks are followed only for the paths given, quiet drops the missing file errors like -s
func grepR(re *regexp.Regexp, quiet bool, paths ...string) bool {
	found := false
	check := func(p string) bool {
		b, err := os.ReadFile(p)
		if err != nil {
			return false
		}
		return re.Match(b)
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			if !quiet {
				fmt.Fprintf(os.Stderr, "grep: %s: No such file or directory\n", p)
			}
			continue
		}
		if !st.IsDir() {
			if check(p) {
				return true
			}
			continue
		}
		filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil || found {
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if d.Type().IsRegular() && check(path) {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		if found {
			return true
		}
	}
	return false
}

func logrotate(args []string) int {
	requirement.Command("logrotate")

	enable := logrotateConf("logrotate_enable")
	size := logrotateConf("logrotate_size_limit")
	retention := logrotateConf("logrotate_retention")
	keep := logrotateConf("logrotate_keep_days")

	if enable != "yes" {
		fmt.Println("Log rotation is not enabled.")
		return 0
	}

	// ---------- CADDY ----------
	conf.WriteInPlace("/etc/logrotate.d/caddy-logs", `/var/log/caddy/coraza_waf/*.log
/var/log/caddy/domlogs/*/access.log {
    size `+size+`
    rotate `+retention+`
    daily
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
    create 640 root adm
    maxage `+keep+`
}
`)
	// no caddy reload needed with copytruncate, and podman exec fails in logrotate.service's sandbox which marked every nightly run failed

	sh.Run("logrotate", "-f", "/etc/logrotate.d/caddy-logs")
	fmt.Println("Caddy log rotation configured.")

	// ---------- SYSLOG ----------
	if !grepR(regexp.MustCompile("/var/log/syslog"), false, "/etc/logrotate.conf", "/etc/logrotate.d/") {
		conf.WriteInPlace("/etc/logrotate.d/syslog", `/var/log/syslog {
    su root adm
    size `+size+`
    rotate `+retention+`
    weekly
    missingok
    notifempty
    compress
    delaycompress
    postrotate
        systemctl reload rsyslog >/dev/null 2>&1 || true
    endscript
    maxage `+keep+`
}
`)
		sh.Run("logrotate", "-f", "/etc/logrotate.d/syslog")
		fmt.Println("Syslog rotation configured.")
	} else if b := conf.Read("/etc/logrotate.d/rsyslog"); conf.IsFile("/etc/logrotate.d/rsyslog") && !strings.Contains(b, "maxsize") {
		// the distro rotates syslog weekly with no size limit, container output (caddy, php-fpm) can grow it by gigabytes a day
		lines := strings.Split(b, "\n")
		for i, l := range lines {
			if strings.HasPrefix(l, "{") {
				lines[i] = "{\n\tmaxsize " + size + l[1:]
				break
			}
		}
		conf.WriteFileKeep("/etc/logrotate.d/rsyslog", strings.Join(lines, "\n"), false)
		fmt.Printf("Syslog already rotated by /etc/logrotate.d/rsyslog, added maxsize %s to it.\n", size)
	} else {
		fmt.Println("Syslog is already configured in logrotate, skipping.")
	}

	// csf installs this one group-writable, logrotate then refuses it and exits with an error on every run
	if conf.IsFile("/etc/logrotate.d/lfd") {
		os.Chmod("/etc/logrotate.d/lfd", 0o644)
	}

	// ---------- JOURNALD ----------
	// container output goes to the journal as well, cap it unless the admin already set a limit
	if !grepR(regexp.MustCompile(`(?m)^SystemMaxUse=`), true, "/etc/systemd/journald.conf", "/etc/systemd/journald.conf.d/") {
		os.MkdirAll("/etc/systemd/journald.conf.d", 0o755)
		conf.WriteInPlace("/etc/systemd/journald.conf.d/openpanel.conf", "[Journal]\nSystemMaxUse=1G\n")
		sh.Run("systemctl", "restart", "systemd-journald")
		fmt.Println("Journald limited to 1G.")
	} else {
		fmt.Println("Journald already has a SystemMaxUse limit, skipping.")
	}

	// ---------- OPENPANEL ----------
	conf.WriteInPlace("/etc/logrotate.d/openpanel", `# rotate all panel logs except notifications.log, logrotate doesn't support extglob
/var/log/openpanel/admin/[!n]*.log
/var/log/openpanel/user/*.log {
    su root adm
    size `+size+`
    rotate `+retention+`
    weekly
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
    create 640 root adm
    maxage `+keep+`
}

# update logs and reports are never rotated, this stamp file only runs the daily age cleanup:
# update logs older than 3m, reports older than 7d
/var/log/openpanel/.cleanup {
    daily
    rotate 0
    missingok
    ifempty
    create 600 root root
    prerotate
        find /var/log/openpanel/updates/ -type f -name "*.log" -mtime +90 -delete 2>/dev/null || true
        find /var/log/openpanel/admin/reports/ -type f -name "*.txt" -mtime +7 -delete 2>/dev/null || true
    endscript
}

`)

	logrotateTouch("/var/log/openpanel/.cleanup")
	sh.Run("logrotate", "-f", "/etc/logrotate.d/openpanel")

	fmt.Println("OpenPanel log rotation configured.")
	return 0
}

func logrotateTouch(p string) {
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
	} else {
		fmt.Fprintf(os.Stderr, "touch: cannot touch '%s': No such file or directory\n", p)
		return
	}
	now := time.Now()
	os.Chtimes(p, now, now)
}
