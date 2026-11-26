// ##############################################################################
// Script Name: internal/commands/general/backup.go
// Description: Backs up (and restores) OpenPanel/OpenAdmin SYSTEM AND USER
// Usage: opencli backup [--restore <ARCHIVE>] [--quiet]
// Author: Stefan Pejcic
// Created: 20.08.2026
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

package general

import (
	"fmt"
	"os"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "backup",
		Desc:  "Backs up (and restores) OpenPanel/OpenAdmin SYSTEM AND USER",
		Usage: "opencli backup [--restore <ARCHIVE>] [--quiet]",
		Docs:  "https://docs.openpanel.com",
		Run:   backup,
	})
}

const (
	backupConfig = "/etc/openpanel/openadmin/config/backups.ini"
	backupLog    = "/var/log/openpanel/admin/system-backup.log"
	backupRuns   = "/var/log/openpanel/admin/system-backup-runs.jsonl"
	backupPrefix = "system-backup_"
)

// hand-picked config paths relative to /, so tar -C / restores with a plain tar -xzf -C / -- a path missing on this install is just skipped
var backupPaths = []string{
	"root/docker-compose.yml",
	"root/.env",
	"root/openpanel_run_after_update",
	"usr/local/mail/openmail/compose.yml",
	"usr/local/mail/openmail/.env",
	"usr/local/mail/openmail/mailserver.env",
	"etc/cron.d/openpanel",
	// /etc/bind has the live named config and zone files -- etc/openpanel/bind9 below is just install-time templates
	"etc/bind",
	"etc/openpanel/openadmin/config",
	// openadmin/cluster is left out on purpose, it holds this server's cluster identity and restoring it elsewhere would clone it
	"etc/openpanel/openadmin/service",
	"etc/openpanel/openpanel/conf",
	"etc/openpanel/openpanel/features",
	"etc/openpanel/openpanel/custom_code",
	"etc/openpanel/openpanel/service",
	"etc/openpanel/csf",
	"etc/openpanel/docker/compose",
	"etc/openpanel/docker/daemon",
	"etc/openpanel/docker/templates",
	"etc/openpanel/mysql",
	"etc/openpanel/nginx/vhosts",
	"etc/openpanel/nginx/templates",
	"etc/openpanel/nginx/modsecurity",
	"etc/openpanel/nginx/error_pages",
	"etc/openpanel/nginx/certs",
	"etc/openpanel/caddy/Caddyfile",
	"etc/openpanel/caddy/domains",
	"etc/openpanel/caddy/templates",
	"etc/openpanel/caddy/deny",
	"etc/openpanel/caddy/suspended_domains",
	"etc/openpanel/caddy/ssl",
	"etc/openpanel/openlitespeed",
	"etc/openpanel/litespeed",
	"etc/openpanel/apache",
	"etc/openpanel/openresty",
	"etc/openpanel/php/ini",
	"etc/openpanel/ftp",
	"etc/openpanel/ssh",
	"etc/openpanel/ofelia",
	"etc/openpanel/skeleton",
	"etc/openpanel/varnish",
	"etc/openpanel/postgres",
	"etc/openpanel/services",
	"etc/openpanel/modules",
	"etc/openpanel/bind9",
	"etc/openpanel/backups",
	"etc/openpanel/clamav",
	"etc/openpanel/email",
}

type backupRun struct{ quiet bool }

func appendFile(path, s string) {
	if f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
		f.WriteString(s)
		f.Close()
	}
}

func (b *backupRun) log(msg string) {
	line := "[" + time.Now().Format("2006-01-02 15:04:05") + "] " + msg + "\n"
	if !b.quiet {
		fmt.Print(line)
	}
	appendFile(backupLog, line)
}

func (b *backupRun) die(msg string) {
	b.log("[✘] " + msg)
	cli.Exit(1)
}

// writes one json-line summary to RUNS_FILE for OpenAdmin's Runs tab -- LOG_FILE above has the full detail
func recordRun(action, status, archive, size string, duration int64, detail string) {
	if size == "" {
		size = "0"
	}
	appendFile(backupRuns, fmt.Sprintf(`{"timestamp":"%s","action":"%s","status":"%s","archive":"%s","size_bytes":%s,"duration_seconds":%d,"detail":"%s"}`+"\n",
		time.Now().Format("2006-01-02T15:04:05-0700"), action, status, archive, size, duration, strings.ReplaceAll(detail, `"`, `\"`)))
}

// reads a key=value line from CONFIG_FILE, ignores [section] headers since backups.ini only has one section anyway
func backupIni(key string) string {
	v, _ := conf.Last(backupConfig, key)
	return text.Xargs(v)
}

func fileSize(p string) string {
	st, err := os.Stat(p)
	if err != nil {
		return "0"
	}
	return fmt.Sprint(st.Size())
}

func (b *backupRun) backup() {
	if !conf.IsFile(backupConfig) {
		b.die("Config file not found: " + backupConfig + ". Set a destination on the System Backups page first.")
	}
	destination := backupIni("destination")
	retention := backupIni("retention_days")
	if retention == "" {
		retention = "-1"
	}
	if destination == "" {
		b.die("No backup destination configured in " + backupConfig + ".")
	}
	if sh.C("mkdir", "-p", destination).Run() != nil {
		b.die("Could not create destination directory: " + destination)
	}

	start := time.Now().Unix()
	b.log("=== System backup started ===")
	var existing []string
	for _, p := range backupPaths {
		if conf.Exists("/" + p) {
			existing = append(existing, p)
		}
	}
	b.log(fmt.Sprintf("Including %d/%d configured paths that exist on this server.", len(existing), len(backupPaths)))

	archiveName := backupPrefix + time.Now().Format("2006-01-02_15-04-05") + ".tar.gz"
	archivePath := strings.TrimSuffix(destination, "/") + "/" + archiveName

	logf, _ := os.OpenFile(backupLog, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	// --exclude stops tar from choking on its own archive when destination lives inside a backed-up path like /etc/openpanel/backups/
	tarArgs := append([]string{"-czf", archivePath, "-C", "/", "--exclude=" + strings.TrimPrefix(destination, "/") + "/*"}, existing...)
	err := sh.LowPrio("tar", tarArgs...).Stderr(logf).Run()
	if logf != nil {
		logf.Close()
	}
	if err != nil {
		recordRun("backup", "failed", archiveName, "0", time.Now().Unix()-start, "tar command failed, see "+backupLog)
		b.die("Backup failed -- see " + backupLog + " for details.")
	}

	size := fileSize(archivePath)
	duration := time.Now().Unix() - start
	human := text.Field(strings.Split(sh.C("du", "-h", archivePath).Str(), "\t")[0], 1)
	b.log(fmt.Sprintf("Backup archive created: %s (%s, %ds)", archivePath, human, duration))

	// prune backups older than retention_days, if it's set above 0
	pruned := 0
	if text.IsInt(retention) && portGE(retention, 1) {
		for _, old := range backupOld(destination, text.Atoi(retention)) {
			if os.Remove(old) == nil {
				b.log("Pruned old backup: " + old)
				pruned++
			}
		}
	}
	b.log(fmt.Sprintf("=== System backup finished (%ds, pruned %d old backup(s)) ===", duration, pruned))
	recordRun("backup", "success", archiveName, size, duration, fmt.Sprintf("%d paths, pruned %d", len(existing), pruned))
}

// find dest -maxdepth 1 -name "system-backup_*.tar.gz" -mtime +N, in directory order like find
func backupOld(dest string, days int) []string {
	d, err := os.Open(dest)
	if err != nil {
		return nil
	}
	names, _ := d.Readdirnames(-1)
	d.Close()
	now := time.Now()
	prefix := dest
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	var out []string
	for _, n := range names {
		if !strings.HasPrefix(n, backupPrefix) || !strings.HasSuffix(n, ".tar.gz") || len(n) < len(backupPrefix)+len(".tar.gz") {
			continue
		}
		st, err := os.Lstat(prefix + n)
		if err != nil {
			continue
		}
		if int64(now.Sub(st.ModTime())/(24*time.Hour)) > int64(days) {
			out = append(out, prefix+n)
		}
	}
	return out
}

// basename -- name, without the "." Go gives for an empty string
func shBasename(s string) string {
	t := strings.TrimRight(s, "/")
	if t == "" {
		if s == "" {
			return ""
		}
		return "/"
	}
	if i := strings.LastIndexByte(t, '/'); i >= 0 {
		return t[i+1:]
	}
	return t
}

func (b *backupRun) restore(filename string) {
	if !conf.IsFile(backupConfig) {
		b.die("Config file not found: " + backupConfig + ".")
	}
	destination := backupIni("destination")
	if destination == "" {
		b.die("No backup destination configured in " + backupConfig + ".")
	}
	// basename only, so a caller-supplied filename can't escape the destination dir
	filename = shBasename(filename)
	archivePath := strings.TrimSuffix(destination, "/") + "/" + filename
	if !conf.IsFile(archivePath) {
		b.die("Backup archive not found: " + archivePath)
	}
	if !strings.HasPrefix(filename, backupPrefix) || !strings.HasSuffix(filename, ".tar.gz") || len(filename) < len(backupPrefix)+len(".tar.gz") {
		b.die("Refusing to restore a file that isn't a system-backup archive: " + filename)
	}

	start := time.Now().Unix()
	b.log("=== Restoring from " + filename + " ===")
	logf, _ := os.OpenFile(backupLog, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	err := sh.C("tar", "-xzf", archivePath, "-C", "/").Stderr(logf).Run()
	if logf != nil {
		logf.Close()
	}
	if err != nil {
		recordRun("restore", "failed", filename, "0", time.Now().Unix()-start, "tar extract failed, see "+backupLog)
		b.die("Restore failed -- see " + backupLog + " for details.")
	}
	duration := time.Now().Unix() - start
	b.log(fmt.Sprintf("=== Restore finished (%ds) -- affected services may need a restart to pick up restored config ===", duration))
	recordRun("restore", "success", filename, fileSize(archivePath), duration, "restored")
}

func backup(args []string) int {
	b := &backupRun{}
	restoreFile := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--restore":
			// the script looped forever on a trailing --restore since shift 2 failed
			if restoreFile = cli.Arg(args, i+1); restoreFile == "" {
				fmt.Fprintln(os.Stderr, "[ERROR] --restore requires an archive path")
				return 1
			}
			i++
		case "--quiet":
			b.quiet = true
		default:
			fmt.Fprintln(os.Stderr, "[ERROR] Unknown option: "+args[i])
			return 1
		}
	}
	os.MkdirAll("/var/log/openpanel/admin", 0o755)
	if restoreFile != "" {
		b.restore(restoreFile)
	} else {
		b.backup()
	}
	return 0
}
