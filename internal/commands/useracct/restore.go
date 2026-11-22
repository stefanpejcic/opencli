// ##############################################################################
// Script Name: internal/commands/useracct/restore.go
// Description: Restores a single OpenPanel user account from a full account .tar.gz backup.
// Usage: opencli user-restore --file <ARCHIVE> [--force] [--new-username=NAME] [--reseller=NAME] [--quiet] [--temp-dir=<PATH> ]
// Author: Stefan Pejcic
// Created: 01.10.2023
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

package useracct

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/podman"
	"opencli/internal/reseller"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-restore",
		Desc:  "Restores a single OpenPanel user account from a full account .tar.gz backup.",
		Usage: "opencli user-restore --file <ARCHIVE> [--force] [--new-username=NAME] [--reseller=NAME] [--quiet] [--temp-dir=<PATH> ]",
		Docs:  "https://docs.openpanel.com",
		Run:   restore,
	})
}

const restoreMailContainer = "openadmin_mailserver"

type restoreJob struct {
	archive, work, newUsername, resellerName string
	force, quiet                             bool
	logFile                                  string
	warnings                                 []string

	domainsAdded, domainsSkipped int
	ftpCreated, ftpSkipped       int
	emailsAdded, emailsSkipped   int
	systemUserStatus, planStatus string
	userID, newIP                string

	man                       map[string]string
	username, context         string
	origUsername, origContext string
	usernameExists            string
	resellerFile              string
	srcSubUID, dstSubUID      string
	srcSubGID, dstSubGID      string
}

func (r *restoreJob) log(msg string) {
	line := "[" + time.Now().Format("2006-01-02 15:04:05") + "] " + msg + "\n"
	if !r.quiet {
		fmt.Print(line)
	}
	appendFile(r.logFile, line)
}

func (r *restoreJob) warn(msg string) {
	r.warnings = append(r.warnings, msg)
	r.log("WARNING: " + msg)
}

func (r *restoreJob) die(msg string) {
	r.log("FATAL ERROR: " + msg)
	cli.Exit(1)
}

func (r *restoreJob) slog(msg string) {
	fmt.Println(msg)
	appendFile(r.logFile, msg+"\n")
}

func restoreQ(q string) string {
	out, _ := db.Cmd(q, "-N", "-s").Out()
	return out
}

// mysqlRun pipes sql into mariadb like mysql_run without -D
func mysqlRun(sql string, flags ...string) {
	sh.C("mariadb", append([]string{"--defaults-extra-file=" + db.ConfigFile}, flags...)...).Stdin(sql).Run()
}

func restore(args []string) int {
	start := time.Now()
	r := &restoreJob{systemUserStatus: "created", planStatus: "imported", man: map[string]string{}}
	for i := 0; i < len(args); {
		a := args[i]
		switch {
		case a == "--file":
			r.archive = cli.Arg(args, i+1)
			i += 2
		case a == "--force":
			r.force = true
			i++
		case a == "--quiet":
			r.quiet = true
			i++
		case strings.HasPrefix(a, "--temp-dir="):
			r.work = a[strings.IndexByte(a, '=')+1:]
			i++
		case strings.HasPrefix(a, "--new-username="):
			r.newUsername = a[strings.IndexByte(a, '=')+1:]
			i++
		case a == "--new-username":
			r.newUsername = cli.Arg(args, i+1)
			i += 2
		case strings.HasPrefix(a, "--reseller="):
			r.resellerName = a[strings.IndexByte(a, '=')+1:]
			i++
		default:
			cli.Fail("[ERROR] Unknown option: %s", a)
		}
	}
	if r.archive == "" {
		cli.Fail("Usage: opencli user-restore --file <ARCHIVE> [--force] [--new-username=NAME] [--reseller=NAME] [--temp-dir=/home/] [--quiet]")
	}
	if !conf.IsFile(r.archive) {
		cli.Fail("[ERROR] Archive not found: %s", r.archive)
	}
	if abs, err := filepath.Abs(r.archive); err == nil {
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			r.archive = real
		}
	}
	if r.work == "" {
		w, err := os.MkdirTemp("/tmp", "oprestore.")
		if err != nil {
			cli.Fail("[ERROR] Failed to create temporary directory")
		}
		r.work = w
	} else {
		if os.MkdirAll(r.work, 0o777) != nil {
			cli.Fail("[ERROR] Cannot create WORK dir: %s", r.work)
		}
		if dirHasEntries(r.work) {
			cli.Fail("[ERROR] WORK dir not empty: %s - remove the --temp-dir= flag to use /tmp/ instead OR create a new subdirectory (e.g. --temp-dir=/home/restore_process)", r.work)
		}
	}
	db.Require()
	editionKey, _ := conf.Raw(conf.Panel, "key")
	defer os.RemoveAll(r.work)

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	os.MkdirAll("/var/log/openpanel/admin/imports", 0o777)
	r.logFile = "/var/log/openpanel/admin/imports/openpanel-import_" + timestamp + ".log"

	fmt.Println("Import started, log file: " + r.logFile)
	r.log("Log file: " + r.logFile)
	r.log("PID: " + strconv.Itoa(os.Getpid()))
	r.log("Archive: " + r.archive)
	// OpenAdmin shows this log only to this reseller
	if r.resellerName != "" {
		r.log("Reseller: " + r.resellerName)
	}

	r.checkDiskExtract()

	r.log("Extracting archive ...")
	lf, _ := os.OpenFile(r.logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
	if sh.C("tar", "-C", r.work, "--acls", "--xattrs", "-xzf", r.archive).Stderr(lf).Run() != nil {
		lf.Close()
		r.die("Failed to extract archive.")
	}
	lf.Close()

	if !conf.IsFile(r.work + "/manifest.env") {
		r.die("manifest.env missing from archive.")
	}
	r.parseManifest()
	m := r.man
	r.username, r.context = m["USERNAME"], m["CONTEXT"]
	if r.username == "" || r.context == "" {
		r.die("Invalid manifest: USERNAME/CONTEXT empty.")
	}
	// these go into SQL, paths and useradd, same rule as user-add
	if !addUsernameRe.MatchString(r.username) {
		r.die("Invalid manifest: bad USERNAME.")
	}
	if !addUsernameRe.MatchString(r.context) {
		r.die("Invalid manifest: bad CONTEXT.")
	}
	if m["SOURCE_UID"] != "" && !text.IsInt(m["SOURCE_UID"]) {
		r.die("Invalid manifest: bad SOURCE_UID.")
	}
	if m["SOURCE_GID"] != "" && !text.IsInt(m["SOURCE_GID"]) {
		r.die("Invalid manifest: bad SOURCE_GID.")
	}
	if !regexp.MustCompile(`^[0-9.]*$`).MatchString(m["BACKUP_FORMAT_VERSION"]) {
		r.die("Invalid manifest: bad BACKUP_FORMAT_VERSION.")
	}

	r.origUsername, r.origContext = r.username, r.context
	if r.newUsername != "" {
		r.username, r.context = r.newUsername, r.newUsername
		r.log("Username remapped: " + r.origUsername + " → " + r.username + "  |  Context: " + r.origContext + " → " + r.context)
	}

	r.newIP = ""
	if ip, ok := get4("https://ip.openpanel.com", time.Second); ok {
		r.newIP = ip
	} else if ip, ok := get4("https://ifconfig.me", time.Second); ok {
		r.newIP = ip
	}
	if r.newIP == "" {
		r.newIP = ipAddrGlobal()
	}
	r.log("Restoring '" + r.username + "' (context: " + r.context + ")  format v" + m["BACKUP_FORMAT_VERSION"])

	// Pre-flight
	r.usernameExists = restoreQ("SELECT COUNT(*) FROM users WHERE username='" + r.username + "';")
	if text.Atoi(r.usernameExists) > 0 {
		if !r.force {
			r.die("Username '" + r.username + "' already exists. Re-run with --force to overwrite.")
		}
		r.warn("Username '" + r.username + "' exists — overwriting because --force was given.")
	}
	if editionKey == "" {
		count := restoreQ("SELECT COUNT(*) FROM users;")
		if text.Atoi(count) >= 3 && text.Atoi(r.usernameExists) == 0 {
			r.die("Community edition limit: 3 accounts. Upgrade to Enterprise for more.")
		}
		// --force drops this account's domains first, and domains owned by someone else get skipped later
		if conf.IsFile(r.work + "/db/domains.list") {
			other := restoreQ("SELECT d.domain_url FROM domains d JOIN users u ON d.user_id = u.id WHERE u.username <> '" + db.Escape(r.username) + "';")
			set := map[string]bool{}
			otherCount := 0
			for _, l := range strings.Split(other, "\n") {
				set[l] = true
				if l != "" {
					otherCount++
				}
			}
			newCount := 0
			for _, l := range conf.Lines(r.work + "/db/domains.list") {
				d := text.Field(l, 1)
				if d != "" && !set[d] {
					newCount++
				}
			}
			if otherCount+newCount > 50 {
				r.die(fmt.Sprintf("Community edition limit: 50 domains. This restore would bring the server to %d. Upgrade to Enterprise for more.", otherCount+newCount))
			}
		}
	}

	// a reseller restore has to fit the reseller's limits before anything is created
	if r.resellerName != "" {
		ok := sh.C("sqlite3", "/etc/openpanel/openadmin/users.db", "SELECT COUNT(*) FROM user WHERE username='"+addEscape(r.resellerName)+"' AND role='reseller';").NoErr().Str()
		if text.Atoi(ok) < 1 {
			r.die("User '" + r.resellerName + "' is not a reseller.")
		}
		if r.force {
			r.die("Resellers can't overwrite existing accounts with --force.")
		}
		r.resellerFile = reseller.LimitsFile(r.resellerName)
		accounts := restoreQ("SELECT COUNT(*) FROM users WHERE owner='" + db.Escape(r.resellerName) + "';")
		max, _ := jqRaw(r.resellerFile, "max_accounts", "0")
		if text.IsInt(max) && text.Atoi(max) > 0 && text.Atoi(accounts) >= text.Atoi(max) {
			r.die("Reseller '" + r.resellerName + "' has reached the maximum account limit (" + max + ").")
		}
		if reseller.OverDisk(r.resellerFile) {
			r.die("Reseller '" + r.resellerName + "' has reached the maximum disk usage limit.")
		}
		plan := awkSecondQuoted(r.work + "/db/plan.sql")
		planID := restoreQ("SELECT id FROM plans WHERE name='" + db.Escape(plan) + "' LIMIT 1;")
		if planID == "" {
			r.die("Plan '" + plan + "' from the backup doesn't exist on this server, resellers can only restore onto plans that are assigned to them.")
		}
		if !reseller.PlanAllowed(r.resellerFile, planID) {
			r.die("Plan '" + plan + "' is not assigned to reseller '" + r.resellerName + "'.")
		}
		r.log("Reseller '" + r.resellerName + "' limits ok, plan '" + plan + "' is allowed.")
	}

	r.restoreSystemUser()
	r.restoreHome()
	r.restoreDatabase()
	r.restoreStaticConfig()
	r.restoreDomains()
	r.restoreFTP()
	r.restoreEmail()
	r.restoreDocker()

	// 9) Reload services + quotas
	if d := r.work + "/caddy/stats/" + r.origUsername; conf.IsDir(d) {
		os.MkdirAll("/var/log/caddy/stats/", 0o777)
		cpA(d, "/var/log/caddy/stats/")
	}
	r.log("Reloading services ...")
	sh.C("podman-compose", "up", "-d", "openpanel", "bind9", "caddy").Dir("/root").Timeout(30 * time.Second).Quiet().Run()
	sh.C("podman", "exec", "caddy", "caddy", "reload").Quiet().Run()
	r.log("Recalculating quotas ...")
	sh.Opencli("user-quota", "--update", r.username).Quiet().Run()

	// Summary
	elapsed := int64(time.Since(start) / time.Second)
	h, mi, s := elapsed/3600, elapsed%3600/60, elapsed%60
	homeSize := duField("-sh", "/home/"+r.context)
	containers := "none"
	if f := r.work + "/docker/containers.txt"; conf.IsFile(f) {
		var parts []string
		for _, l := range text.Lines(conf.Read(f)) {
			p := strings.Split(l, ":")
			if len(p) > 1 {
				parts = append(parts, p[1])
			} else {
				parts = append(parts, p[0])
			}
		}
		containers = text.Xargs(strings.Join(parts, "\n"))
		if containers == "" {
			containers = "none"
		}
	}
	r.slog("")
	r.slog("══════════════════════════════════════════════════════════")
	r.slog("  RESTORE COMPLETE — " + r.username)
	r.slog("══════════════════════════════════════════════════════════")
	r.slog(fmt.Sprintf("  %-24s %s", "Archive:", filepath.Base(r.archive)))
	r.slog(fmt.Sprintf("  %-24s %dh %dm %ds", "Time taken:", h, mi, s))
	if r.newUsername != "" {
		r.slog(fmt.Sprintf("  %-24s %s  →  %s", "Username:", r.origUsername, r.username))
		r.slog(fmt.Sprintf("  %-24s %s  →  %s", "Context:", r.origContext, r.context))
	}
	r.slog("")
	r.slog("  Restored:")
	r.slog(fmt.Sprintf("    %-22s %s", "System user:", r.context+"  ["+r.systemUserStatus+"]"))
	r.slog(fmt.Sprintf("    %-22s %s", "Home directory:", "/home/"+r.context+"/  ("+homeSize+")"))
	r.slog(fmt.Sprintf("    %-22s %s", "Plan:", `"`+m["PLAN_NAME"]+`"  [`+r.planStatus+"]"))
	r.slog(fmt.Sprintf("    %-22s %s", "DB user ID:", r.userID))
	r.slog(fmt.Sprintf("    %-22s %d added, %d skipped", "Domains:", r.domainsAdded, r.domainsSkipped))
	r.slog(fmt.Sprintf("    %-22s %d created, %d skipped", "FTP accounts:", r.ftpCreated, r.ftpSkipped))
	r.slog(fmt.Sprintf("    %-22s %d added, %d skipped", "Email accounts:", r.emailsAdded, r.emailsSkipped))
	r.slog(fmt.Sprintf("    %-22s %s", "Containers:", containers))
	r.slog("")
	r.slog(fmt.Sprintf("  Warnings: %d", len(r.warnings)))
	if len(r.warnings) > 0 {
		for _, w := range r.warnings {
			r.slog("    [!] " + w)
		}
	} else {
		r.slog("    none")
	}
	r.slog("══════════════════════════════════════════════════════════")
	r.slog("")
	r.log(fmt.Sprintf("Elapsed time: %dh %dm %ds", h, mi, s))
	r.log("SUCCESS: Import for user " + r.username + " completed successfully.")

	sh.Opencli("sentinel", "--action=user_create", "--title=User account '"+r.username+"' restored from OpenPanel backup", "--message=User account '"+r.username+"' has been successfully restored from backup file '"+filepath.Base(r.archive)+"'").Quiet().Background()
	return 0
}

// checkDiskExtract wants room for the fully uncompressed archive
func (r *restoreJob) checkDiskExtract() {
	r.log("Checking disk space for extraction ...")
	var size int64
	if st, err := os.Stat(r.archive); err == nil {
		size = st.Size()
	}
	compressed := size / 1024
	estimated := compressed * 3
	lines := strings.Split(sh.C("gzip", "-l", r.archive).NoErr().Str(), "\n")
	if len(lines) > 1 {
		g := text.Field(lines[1], 2)
		if n, err := strconv.ParseInt(g, 10, 64); err == nil && text.IsInt(g) && n > 0 {
			estimated = n / 1024
			// gzip stores the uncompressed size mod 2^32, so if it looks smaller than the compressed archive the field wrapped and can't be trusted
			if estimated < compressed {
				estimated = compressed * 3
			}
		}
	}
	free, ok := freeKB(r.work)
	if !ok {
		r.warn("Cannot read free space at " + r.work + " — disk space check skipped.")
		return
	}
	r.log(fmt.Sprintf("  Estimated extracted size (~%d MB)", estimated/1024))
	r.log(fmt.Sprintf("  Free at %s  (~%d MB)", r.work, free/1024))
	if free < estimated {
		r.die(fmt.Sprintf("Not enough disk space to extract archive. Estimated ~%d MB needed, ~%d MB free at %s. Aborting.", estimated/1024, free/1024, r.work))
	}
}

// parseManifest reads the archive's manifest literally, it can come from a reseller so it's never sourced
func (r *restoreJob) parseManifest() {
	kv := regexp.MustCompile(`^([A-Z_]+)=(.*)$`)
	q := regexp.MustCompile(`^"(.*)"$`)
	for _, l := range strings.Split(strings.TrimSuffix(conf.Read(r.work+"/manifest.env"), "\n"), "\n") {
		mm := kv.FindStringSubmatch(l)
		if mm == nil {
			continue
		}
		v := mm[2]
		if qm := q.FindStringSubmatch(v); qm != nil {
			v = qm[1]
		}
		switch mm[1] {
		case "BACKUP_FORMAT_VERSION", "USERNAME", "CONTEXT", "PLAN_NAME", "PLAN_FEATURE_SET", "SOURCE_UID", "SOURCE_GID", "SOURCE_IP", "CREATED_AT":
			r.man[mm[1]] = v
		}
	}
}

// 1) System user
func (r *restoreJob) restoreSystemUser() {
	pw := r.work + "/system/passwd.user"
	gr := r.work + "/system/group.user"
	sf := r.work + "/system/shadow.user"
	if !nonEmptyFile(pw) {
		r.warn("No system user data in backup — creating '" + r.context + "' fresh (backup was generated via OpenPanel UI).")
		if sysUID(r.context) != "" {
			r.systemUserStatus = "already existed"
		} else {
			sh.C("useradd", "-m", "-d", "/home/"+r.context, "-s", "/bin/bash", r.context).Run()
			sh.C("usermod", "-L", r.context).Run()
			r.systemUserStatus = "created fresh (UID " + statUID("/home/"+r.context) + ")"
		}
		return
	}

	r.log("Restoring system user (" + r.context + ") ...")
	if conf.IsFile(gr) {
		for _, l := range readLines(gr) {
			f := strings.SplitN(l, ":", 4)
			for len(f) < 3 {
				f = append(f, "")
			}
			group, gid := f[0], f[2]
			if group == "" {
				continue
			}
			// only the account's own group, and never with a system gid (0 would be root's group)
			if group != r.origContext || !text.IsInt(gid) || text.Atoi(gid) < 1000 {
				continue
			}
			if !sh.C("getent", "group", r.context).Quiet().OK() {
				sh.C("groupadd", "-g", gid, r.context).NoErr().Run()
			}
		}
	}

	used := map[string]bool{}
	for _, l := range conf.Lines("/etc/passwd") {
		if f := strings.Split(l, ":"); len(f) > 2 {
			used[f[2]] = true
		}
	}
	freeUID := func(u string) string {
		n := text.Atoi(u)
		for used[strconv.Itoa(n)] {
			n++
		}
		return strconv.Itoa(n)
	}

	shells := regexp.MustCompile(`^(/bin/bash|/bin/sh|/usr/bin/bash|/usr/sbin/nologin|/sbin/nologin|/bin/false)$`)
	for _, l := range readLines(pw) {
		f := strings.SplitN(l, ":", 7)
		for len(f) < 7 {
			f = append(f, "")
		}
		user, uid, gid, comment, shell := f[0], f[2], f[3], f[4], f[6]
		if user != r.origContext {
			continue
		}
		home := "/home/" + r.context
		if sysUID(r.context) != "" {
			r.log("System user " + r.context + " already exists — skipping creation.")
			r.systemUserStatus = "already existed"
			continue
		}
		// uid/gid/shell/comment come from the archive: no system ids, only a normal login shell, plain comment
		if !text.IsInt(uid) || text.Atoi(uid) < 1000 {
			uid = "1000"
		}
		if !text.IsInt(gid) || text.Atoi(gid) < 1000 {
			gid = ""
		}
		if !shells.MatchString(shell) {
			shell = "/bin/bash"
		}
		comment = regexp.MustCompile(`[^a-zA-Z0-9 ._-]`).ReplaceAllString(comment, "")
		free := freeUID(uid)
		if gid == "" || !sh.C("useradd", "-u", free, "-g", gid, "-c", comment, "-d", home, "-s", shell, r.context).NoErr().OK() {
			sh.C("useradd", "-u", free, "-c", comment, "-d", home, "-s", shell, r.context).Run()
		}
		if free != uid {
			r.warn("UID remapped for " + r.context + ": " + uid + " → " + free)
		}
		r.systemUserStatus = "created (UID " + free + ")"
	}

	if conf.IsFile(sf) {
		for _, l := range readLines(sf) {
			f := strings.SplitN(l, ":", 3)
			if len(f) > 1 && f[0] == r.origContext && f[1] != "" {
				sh.C("usermod", "-p", f[1], r.context).NoErr().Run()
			}
		}
	}
	r.srcSubUID, r.dstSubUID = r.restoreSubID("subuid")
	r.srcSubGID, r.dstSubGID = r.restoreSubID("subgid")
}

// restoreSubID reuses the source subuid/subgid range when nobody else has it, volume files are owned by ids from it
func (r *restoreJob) restoreSubID(f string) (string, string) {
	line := text.FirstLine(conf.Read(r.work + "/system/" + f + ".user"))
	if line == "" {
		return "", ""
	}
	p := strings.SplitN(line, ":", 3)
	for len(p) < 3 {
		p = append(p, "")
	}
	start, count := p[1], p[2]
	src := start + ":" + count
	etc := "/etc/" + f
	if !hasLine(etc, r.context+":"+start+":"+count) {
		s, c := text.Atoi(start), text.Atoi(count)
		end := s + c - 1
		overlap := false
		for _, l := range conf.Lines(etc) {
			q := strings.Split(l, ":")
			for len(q) < 3 {
				q = append(q, "")
			}
			if q[0] != r.context && text.Atoi(q[1]) <= end && text.Atoi(q[1])+text.Atoi(q[2])-1 >= s {
				overlap = true
			}
		}
		if !overlap && r.systemUserStatus != "already existed" {
			var keep []string
			for _, l := range conf.Lines(etc) {
				if !strings.HasPrefix(l, r.context+":") {
					keep = append(keep, l)
				}
			}
			conf.WriteFileKeep(etc, linesText(keep), false)
			appendFile(etc, r.context+":"+start+":"+count+"\n")
		} else {
			r.warn(f + " range " + start + ":" + count + " is not free here, file owners in the home directory will be remapped.")
		}
	}
	dst := ""
	for _, l := range conf.Lines(etc) {
		q := strings.Split(l, ":")
		if q[0] == r.context {
			for len(q) < 3 {
				q = append(q, "")
			}
			dst = q[1] + ":" + q[2]
			break
		}
	}
	return src, dst
}

func hasLine(path, line string) bool {
	for _, l := range conf.Lines(path) {
		if l == line {
			return true
		}
	}
	return false
}

// remapIDs maps owners from the source uid/gid and subuid/subgid ranges to the ones on this server
func (r *restoreJob) remapIDs(path string) {
	if !conf.Exists(path) {
		return
	}
	newUID, newGID := sysUID(r.context), sysGID(r.context)
	if newUID == "" {
		return
	}
	srcUID := r.man["SOURCE_UID"]
	srcGID := r.man["SOURCE_GID"]
	if srcGID == "" {
		srcGID = srcUID
	}
	if srcUID == newUID && srcGID == newGID && r.srcSubUID == r.dstSubUID && r.srcSubGID == r.dstSubGID {
		return
	}
	r.log("Remapping file owners in " + path + " ...")
	pair := func(s string) (int, int) {
		if s == "" {
			s = "0:0"
		}
		a, b, _ := strings.Cut(s, ":")
		return text.Atoi(a), text.Atoi(b)
	}
	su, sc := pair(r.srcSubUID)
	du, _ := pair(r.dstSubUID)
	sg, gc := pair(r.srcSubGID)
	dg, _ := pair(r.dstSubGID)
	SU, SG, NU, NG := text.Atoi(srcUID), text.Atoi(srcGID), text.Atoi(newUID), text.Atoi(newGID)
	mu := func(u int) int {
		if u == SU {
			return NU
		}
		if sc != 0 && du != 0 && u >= su && u < su+sc {
			return u - su + du
		}
		return u
	}
	mg := func(g int) int {
		if g == SG {
			return NG
		}
		if gc != 0 && dg != 0 && g >= sg && g < sg+gc {
			return g - sg + dg
		}
		return g
	}
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		st, err := os.Lstat(p)
		if err != nil {
			return nil
		}
		s := st.Sys().(*syscall.Stat_t)
		u, g := mu(int(s.Uid)), mg(int(s.Gid))
		if u != int(s.Uid) || g != int(s.Gid) {
			if st.Mode()&fs.ModeSymlink != 0 {
				os.Lchown(p, u, g)
			} else {
				os.Chown(p, u, g)
				if st.Mode()&(fs.ModeSetuid|fs.ModeSetgid) != 0 {
					os.Chmod(p, st.Mode()&(fs.ModePerm|fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky))
				}
			}
		}
		return nil
	})
}

// 2) Home directory
func (r *restoreJob) restoreHome() {
	if !conf.IsDir(r.work + "/homedir") {
		r.warn("No homedir/ in archive — skipping file restore.")
		return
	}
	r.log("Checking disk space for home directory restore ...")
	used, err := strconv.ParseInt(duField("-sk", r.work+"/homedir"), 10, 64)
	os.MkdirAll("/home", 0o777)
	if err == nil && used > 0 {
		if free, ok := freeKB("/home"); ok {
			r.log(fmt.Sprintf("  Home directory size (~%d MB)", used/1024))
			r.log(fmt.Sprintf("  Free at /home  (~%d MB)", free/1024))
			if free < used {
				r.die(fmt.Sprintf("Not enough disk space at /home. Needed ~%d MB, free ~%d MB. Aborting to avoid a partial restore.", used/1024, free/1024))
			}
		} else {
			r.warn("Cannot read free space at /home — disk space check skipped.")
		}
	} else {
		r.warn("Cannot determine home directory size — disk space check skipped.")
	}

	compose := podman.ComposeFile(r.context)
	if r.systemUserStatus == "already existed" && conf.IsFile(compose) {
		r.log("Stopping existing containers for " + r.context + " ...")
		podman.ComposeUser(r.context, "-f", compose, "down").Quiet().Run()
		sh.C("systemctl", "--user", "-M", r.context+"@", "stop", "podman.socket", "podman.service").Quiet().Run()
	}

	r.log("Restoring /home/" + r.context + " ...")
	lf, _ := os.OpenFile(r.logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
	a := exec.Command("tar", "-C", r.work, "--numeric-owner", "--acls", "--xattrs", "--transform", "s,^homedir,"+r.context+",", "-cf", "-", "homedir")
	a.Stderr = os.Stderr
	b := exec.Command("tar", "-C", "/home", "--numeric-owner", "--acls", "--xattrs", "--xattrs-exclude=security.*", "--xattrs-exclude=trusted.*", "-xf", "-")
	b.Stderr = lf
	ra, rb := pipe2(a, b)
	lf.Close()
	if ra != 0 || rb != 0 {
		r.die("Failed to restore home directory.")
	}
	home := "/home/" + r.context
	// an uploaded archive could carry a setuid binary owned by root or another system user, never keep those
	if hs, err := os.Stat(home); err == nil {
		dev := hs.Sys().(*syscall.Stat_t).Dev
		filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			st, err := os.Lstat(p)
			if err != nil {
				return nil
			}
			s := st.Sys().(*syscall.Stat_t)
			if s.Dev != dev {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if st.Mode()&fs.ModeSymlink == 0 && st.Mode()&(fs.ModeSetuid|fs.ModeSetgid) != 0 && (s.Uid < 1000 || s.Gid < 1000) {
				os.Chmod(p, st.Mode()&(fs.ModePerm|fs.ModeSticky))
			}
			return nil
		})
	}

	// so containers can later start without permission issues
	for _, pat := range []string{home + "/sockets/*/*.sock", home + "/sockets/*/*.pid"} {
		ms, _ := filepath.Glob(pat)
		for _, f := range ms {
			if st, err := os.Lstat(f); err == nil && !st.IsDir() {
				os.Remove(f)
			}
		}
	}
	// MARIADB ERROR: Bad magic header in tc log
	os.Remove(home + "/docker-data/volumes/" + r.context + "_mysql_data/_data/tc.log")

	// older backups carry podman state that points at the source server's image layers, containers get recreated from the volumes instead
	if ents, err := os.ReadDir(home + "/docker-data"); err == nil {
		for _, e := range ents {
			if e.Name() != "volumes" {
				os.RemoveAll(home + "/docker-data/" + e.Name())
			}
		}
	}
	r.remapIDs(home)

	if conf.IsFile(r.work+"/mail_external/path.txt") && conf.IsFile(r.work+"/mail_external/mail.tar") {
		mp := strings.TrimRight(conf.Read(r.work+"/mail_external/path.txt"), "\n")
		r.log("Restoring external mail → " + mp + " ...")
		os.MkdirAll(mp, 0o777)
		list := r.work + "/mail_external/.extracted.list"
		lst, _ := os.Create(list)
		lf, _ := os.OpenFile(r.logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
		if sh.C("tar", "-C", mp, "--numeric-owner", "--acls", "--xattrs", "--strip-components=1", "-xvf", r.work+"/mail_external/mail.tar").Stdout(lst).Stderr(lf).Run() != nil {
			r.warn("External mail restore failed.")
		}
		lst.Close()
		lf.Close()
		seen := map[string]bool{}
		var ds []string
		for _, l := range conf.Lines(list) {
			f := strings.Split(l, "/")
			if len(f) > 1 && !seen[f[1]] {
				seen[f[1]] = true
				ds = append(ds, f[1])
			}
		}
		sort.Strings(ds)
		for _, d := range ds {
			if d != "" && conf.Exists(mp+"/"+d) {
				r.remapIDs(mp + "/" + d)
			}
		}
	}
}

// 3) Panel DB: patch SQL if new username, then import
func (r *restoreJob) restoreDatabase() {
	r.log("Restoring panel DB rows ...")
	userSQL := r.work + "/db/user.sql"
	planSQL := r.work + "/db/plan.sql"

	// Patch user.sql for --new-username: replace old username + old context values
	if r.newUsername != "" && conf.IsFile(userSQL) {
		r.log("Patching user.sql: " + r.origUsername + " → " + r.username)
		sedFile(userSQL, subRe(regexp.MustCompile("'"+r.origUsername+"'"), "'"+r.username+"'", true))
		if r.origContext != r.origUsername {
			sedFile(userSQL, subRe(regexp.MustCompile("'"+r.origContext+"'"), "'"+r.context+"'", true))
		}
	}

	// Plan
	planName := awkSecondQuoted(planSQL)
	r.man["PLAN_NAME"] = planName
	planID := restoreQ("SELECT id FROM plans WHERE name='" + planName + "' LIMIT 1;")
	if planID != "" {
		r.log("Plan '" + planName + "' exists (ID: " + planID + ") — reusing.")
		r.planStatus = "reused (ID " + planID + ")"
	} else if conf.IsFile(planSQL) {
		sqlJoinSemicolon(planSQL)
		mysqlRun("USE `" + db.Database + "`;\n" + conf.Read(planSQL))
		planID = restoreQ("SELECT id FROM plans WHERE name='" + planName + "' LIMIT 1;")
		r.log("Plan '" + planName + "' imported (ID: " + planID + ").")
		r.planStatus = "imported (ID " + planID + ")"
	}

	// User row
	if conf.IsFile(userSQL) {
		sqlJoinSemicolon(userSQL)
		pid := planID
		if pid == "" {
			pid = "NULL"
		}
		endRe := regexp.MustCompile(`,[[:space:]]*[0-9]+\);$`)
		var out []string
		for _, l := range readLinesAll(userSQL) {
			l = endRe.ReplaceAllLiteralString(l, ","+pid+");")
			out = append(out, strings.ReplaceAll(l, "'NULL'", "NULL"))
		}
		tmp := linesText(out)
		os.WriteFile(r.work+"/db/_user_local.sql", []byte(tmp), 0o666)

		if text.Atoi(r.usernameExists) > 0 && r.force {
			if old := restoreQ("SELECT id FROM users WHERE username='" + r.username + "';"); old != "" {
				restoreQ("DELETE FROM sites WHERE domain_id IN (SELECT domain_id FROM domains WHERE user_id=" + old + ");")
				restoreQ("DELETE FROM domains WHERE user_id=" + old + ";")
				db.Cmd("DELETE FROM mcp_tokens WHERE user_id="+old+";", "-N", "-s").NoErr().Run()
				db.Cmd("DELETE FROM user_passkeys WHERE user_id="+old+";", "-N", "-s").NoErr().Run()
				restoreQ("DELETE FROM users WHERE id=" + old + ";")
			}
		}
		mysqlRun("USE `" + db.Database + "`;\n" + tmp)
	}

	r.userID = restoreQ("SELECT id FROM users WHERE username='" + r.username + "';")
	if r.userID == "" {
		r.die("Failed to import user row for '" + r.username + "'.")
	}
	r.log("User row ready (ID: " + r.userID + ").")

	// the backup's owner is from the old server, a reseller restore always belongs to the reseller
	if r.resellerName != "" {
		restoreQ("UPDATE users SET owner='" + db.Escape(r.resellerName) + "' WHERE id=" + r.userID + ";")
		n := restoreQ("SELECT COUNT(*) FROM users WHERE owner='" + db.Escape(r.resellerName) + "';")
		if n == "" {
			n = "0"
		}
		jqSetNumber(r.resellerFile, "current_accounts", n)
		r.log("Owner set to reseller '" + r.resellerName + "'.")
	}

	// mcp tokens / passkeys, the tables only exist once the panel used them so create them first
	for _, t := range []string{"mcp_tokens", "user_passkeys"} {
		ddl := r.work + "/db/" + t + ".ddl"
		if !nonEmptyFile(ddl) {
			continue
		}
		mysqlRun(conf.Read(ddl), "-D", db.Database)
		rows := r.work + "/db/" + t + ".rows.sql"
		if !nonEmptyFile(rows) {
			continue
		}
		restoreQ("DROP TABLE IF EXISTS _import_" + t + "; CREATE TABLE _import_" + t + " LIKE " + t + ";")
		var fixed []string
		n := 0
		for _, l := range readLinesAll(rows) {
			if strings.HasPrefix(l, "INSERT") {
				n++
			}
			fixed = append(fixed, strings.Replace(l, "INSERT INTO `"+t+"`", "INSERT INTO `_import_"+t+"`", 1))
		}
		mysqlRun(linesText(fixed), "-D", db.Database)
		cols := restoreQ("SELECT GROUP_CONCAT(column_name ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='" + db.Database + "' AND table_name='" + t + "' AND column_name NOT IN ('id','user_id');")
		if db.Cmd("INSERT IGNORE INTO "+t+" (user_id,"+cols+") SELECT "+r.userID+","+cols+" FROM _import_"+t+"; DROP TABLE _import_"+t+";", "-N", "-s").Run() == nil {
			r.log(fmt.Sprintf("Restored %d row(s) into %s.", n, t))
		}
	}
}

// readLinesAll is like a sed pass, the last line counts even without a newline
func readLinesAll(path string) []string {
	return conf.Lines(path)
}

// 4) Feature set + core config
func (r *restoreJob) restoreStaticConfig() {
	fdir := "/etc/openpanel/openpanel/features"
	if fs, _ := filepath.Glob(r.work + "/features/*.txt"); len(fs) > 0 {
		os.MkdirAll(fdir, 0o777)
		for _, f := range fs {
			bn := filepath.Base(f)
			if conf.IsFile(fdir + "/" + bn) {
				r.log("Feature set " + bn + " present — keeping.")
			} else {
				cpA(f, fdir+"/")
				r.log("Feature set " + bn + " installed.")
			}
		}
	}
	if conf.IsDir(r.work+"/core") && dirHasEntries(r.work+"/core") {
		os.MkdirAll(conf.UsersDir+"/"+r.username, 0o777)
		cpAContents(r.work+"/core", conf.UsersDir+"/"+r.username)
		r.log("Core config restored.")
	}
}

// 5) Domains, skipped if already owned
func (r *restoreJob) restoreDomains() {
	list := r.work + "/db/domains.list"
	if !conf.IsFile(list) {
		r.log("No domains in archive.")
		return
	}
	r.log("Restoring domains ...")
	for _, d := range []string{"/etc/openpanel/caddy/domains/", "/var/log/caddy/domlogs/", "/var/log/caddy/coraza_waf/", "/etc/bind/zones/", "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/", "/etc/openpanel/caddy/ssl/custom/", "/var/log/caddy/stats/"} {
		os.MkdirAll(d, 0o777)
	}
	if f := r.work + "/caddy/blocked.ips"; conf.IsFile(f) {
		os.MkdirAll("/etc/openpanel/caddy/deny/", 0o777)
		cpA(f, "/etc/openpanel/caddy/deny/"+r.context+".ips")
		r.log("Blocked IPs restored.")
	}

	w := r.work
	srcIP := r.man["SOURCE_IP"]
	for _, l := range readLines(list) {
		f := strings.FieldsFunc(l, func(c rune) bool { return c == ' ' || c == '\t' })
		if len(f) == 0 {
			continue
		}
		domain := f[0]
		docroot, php := "", ""
		if len(f) > 1 {
			docroot = f[1]
		}
		if len(f) > 2 {
			php = strings.Join(f[2:], " ")
		}
		owner := whoOwns(sh.Opencli("domains-whoowns", domain).NoErr().Str(), domain)
		if owner != "" {
			r.warn("Domain '" + domain + "' exists (owner: " + owner + ") — skipped.")
			r.domainsSkipped++
			continue
		}
		lf, _ := os.OpenFile(r.logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
		err := sh.Opencli("domains-add", domain, r.username, "--docroot", docroot, "--php_version", php, "--skip_caddy", "--skip_vhost", "--skip_containers", "--skip_dns", "--skip-sentinel").Stdout(lf).Stderr(lf).Run()
		lf.Close()
		if err != nil {
			r.warn("opencli domains-add failed for '" + domain + "' — skipping assets.")
			r.domainsSkipped++
			continue
		}
		r.domainsAdded++

		if s := w + "/caddy/domains/" + domain + ".conf"; conf.IsFile(s) {
			cpA(s, "/etc/openpanel/caddy/domains/")
		}
		if s := w + "/caddy/domlogs/" + domain + ".log"; conf.IsFile(s) {
			os.MkdirAll("/var/log/caddy/domlogs/"+domain, 0o777)
			cpA(s, "/var/log/caddy/domlogs/"+domain+"/access.log")
		}
		if s := w + "/caddy/waf/" + domain + ".log"; conf.IsFile(s) {
			cpA(s, "/var/log/caddy/coraza_waf/")
		}
		if s := w + "/caddy/ssl/acme/" + domain; conf.IsDir(s) {
			cpA(s, "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/")
		}
		if s := w + "/caddy/ssl/custom/" + domain; conf.IsDir(s) {
			cpA(s, "/etc/openpanel/caddy/ssl/custom/")
		}
		if s := w + "/caddy/suspended/" + domain + ".conf"; conf.IsFile(s) {
			os.MkdirAll("/etc/openpanel/caddy/suspended_domains/", 0o777)
			cpA(s, "/etc/openpanel/caddy/suspended_domains/")
		}
		if s := w + "/bind/zones/" + domain + ".zone"; conf.IsFile(s) {
			cpA(s, "/etc/bind/zones/")
			zone := "/etc/bind/zones/" + domain + ".zone"
			if srcIP != "" && r.newIP != "" && srcIP != r.newIP {
				if re, err := regexp.Compile(srcIP); err == nil {
					sedFile(zone, subRe(re, r.newIP, true))
				}
			}
			named := "/etc/bind/named.conf.local"
			if !strings.Contains(conf.Read(named), `"`+domain+`"`) {
				appendFile(named, `zone "`+domain+`" IN { type master; file "/etc/bind/zones/`+domain+`.zone"; };`+"\n")
			}
		}
		r.log("Domain restored: " + domain)
	}

	sh.C("podman", "exec", "openpanel_dns", "rndc", "reconfig").Quiet().Run()
	sh.C("podman-compose", "up", "-d", "bind9").Dir("/root").Timeout(30 * time.Second).Quiet().Run()

	// Sites table
	sites := w + "/db/sites.sql"
	if !conf.IsFile(sites) {
		return
	}
	all := conf.Lines(sites)
	if len(all) > 0 {
		all = all[1:]
	}
	body := strings.ReplaceAll(strings.Join(all, "\n"), "),", ")\n")
	strip := strings.NewReplacer("(", "", ")", "", "'", "")
	for _, line := range text.Lines(body) {
		line = strings.TrimSpace(line)
		clean := strings.TrimSuffix(strip.Replace(line), ",")
		if clean == "" || clean == ";" {
			continue
		}
		f := strings.Split(clean, ",")
		col := func(i int) string {
			if i < len(f) {
				return text.Xargs(f[i])
			}
			return ""
		}
		sname, email, ver, typ, ports, path, container, domainURL := col(0), col(2), col(3), col(5), col(6), col(7), col(8), col(9)
		did, _ := db.Cmd("SELECT domain_id FROM domains WHERE domain_url='"+domainURL+"' AND user_id="+r.userID+";", "-N", "-s").NoErr().Out()
		if did == "" {
			// the old loop ran in a pipe subshell, so this never reached the summary
			r.log("WARNING: Site '" + sname + "' — could not resolve destination domain_id, skipped.")
			continue
		}
		v := func(s string) string {
			if s == "NULL" || s == "" {
				return "NULL"
			}
			return "'" + s + "'"
		}
		if !text.IsInt(ports) {
			ports = "NULL"
		}
		restoreQ("INSERT INTO sites (site_name,domain_id,admin_email,version,type,ports,path,container) VALUES ('" + sname + "'," + did + "," + v(email) + "," + v(ver) + "," + v(typ) + "," + ports + "," + v(path) + "," + v(container) + ");")
	}
}

// whoOwns is awk -F "Owner of '<domain>': " '{print $2}' on domains-whoowns output
func whoOwns(out, domain string) string {
	re, err := regexp.Compile("Owner of '" + domain + "': ")
	if err != nil {
		re = regexp.MustCompile(regexp.QuoteMeta("Owner of '" + domain + "': "))
	}
	var res []string
	for _, l := range text.Lines(out) {
		p := re.Split(l, 3)
		if len(p) > 1 {
			res = append(res, p[1])
		} else {
			res = append(res, "")
		}
	}
	return strings.TrimRight(strings.Join(res, "\n"), "\n")
}

// 6) FTP
func (r *restoreJob) restoreFTP() {
	ldir := "/etc/openpanel/ftp/users/" + r.context
	if !conf.IsDir(r.work+"/ftp") || !dirHasEntries(r.work+"/ftp") {
		r.log("No FTP in archive.")
		return
	}
	r.log("Restoring FTP accounts ...")
	os.MkdirAll(ldir, 0o777)
	cpAContents(r.work+"/ftp", ldir)
	if !conf.IsFile(ldir + "/users.list") {
		r.log("No users.list — skipping container step.")
		return
	}
	if !podman.IsRunning("openadmin_ftp") {
		podman.EnsureRunning("openadmin_ftp", "/root", "openadmin_ftp", 30)
		time.Sleep(2 * time.Second)
	}
	gid := statUID("/home/" + r.context)
	if !text.IsInt(gid) {
		r.warn("Cannot get GID for " + r.context + " — FTP container step skipped.")
		return
	}
	if !sh.C("podman", "exec", "openadmin_ftp", "sh", "-c", "getent group '"+gid+"' >/dev/null 2>&1").OK() {
		sh.C("podman", "exec", "openadmin_ftp", "addgroup", "-g", gid, r.context).NoErr().Run()
	}

	// todo: check if UID already taken and not our username, in which case assign new UID and update in $LDIR/users.list
	for _, l := range readLines(ldir + "/users.list") {
		f := strings.SplitN(l, "|", 5)
		for len(f) < 5 {
			f = append(f, "")
		}
		fu, hp, dir := f[0], f[1], f[2]
		if fu == "" {
			continue
		}
		if sh.C("podman", "exec", "openadmin_ftp", "id", fu).Quiet().OK() {
			r.log("[skip] FTP user '" + fu + "' exists.")
			r.ftpSkipped++
			continue
		}
		rp := "/home/" + r.context + "/docker-data/volumes/" + r.context + "_html_data/_data/"
		nd := rp + strings.TrimPrefix(dir, "/var/www/html/")
		os.MkdirAll(nd, 0o777)
		lchownR(nd, text.Atoi(gid), text.Atoi(gid))
		chmodR(nd, 0o775|fs.ModeSetgid)
		sh.C("podman", "exec", "openadmin_ftp", "useradd", "-d", nd, "-s", "/sbin/nologin", "-g", r.context, "-M", fu, "--badname").NoErr().Run()
		sh.C("podman", "exec", "openadmin_ftp", "sh", "-c", "usermod -p '"+hp+"' '"+fu+"'").Run()
		r.log("FTP user restored: " + fu)
		r.ftpCreated++
	}

	r.log("Setting +rx permissions for FTP paths")
	h := "/home/" + r.context
	for _, p := range []string{h, h + "/docker-data", h + "/docker-data/volumes", h + "/docker-data/volumes/" + r.context + "_html_data", h + "/docker-data/volumes/" + r.context + "_html_data/_data"} {
		addMode(p, 0o555)
	}
}

// 7) Email, existing mailboxes are skipped
func (r *restoreJob) restoreEmail() {
	ed := r.work + "/emails"
	dms := "/usr/local/mail/openmail/docker-data/dms/config"
	postfwdDst := "/usr/local/mail/openmail/postfwd/postfwd.cf"

	// DKIM keys
	if conf.IsDir(ed+"/dkim") && dirHasEntries(ed+"/dkim") {
		os.MkdirAll(dms+"/opendkim/keys", 0o777)
		cpAContents(ed+"/dkim", dms+"/opendkim/keys")
		for _, t := range []string{"KeyTable", "SigningTable", "TrustedHosts"} {
			if !nonEmptyFile(ed + "/dkim_" + t) {
				continue
			}
			dst := dms + "/opendkim/" + t
			appendFile(dst, "")
			for _, l := range readLines(ed + "/dkim_" + t) {
				if !hasLine(dst, l) {
					appendFile(dst, l+"\n")
				}
			}
		}
		r.log("DKIM keys restored.")
	}

	asrc := ed + "/postfix-accounts.cf"
	if !nonEmptyFile(asrc) {
		r.log("No postfix-accounts.cf in archive — skipping email merge.")
		return
	}
	if !podman.IsRunning(restoreMailContainer) {
		// a container wedged in a transitional state won't clear with a plain compose-up, force it out first
		if podman.State(restoreMailContainer) != "" {
			sh.Quiet("podman", "kill", restoreMailContainer)
			sh.Quiet("podman", "rm", "-f", restoreMailContainer)
			sh.Quiet("podman", "rm", "-f", "--storage", restoreMailContainer)
		}
		if conf.IsDir("/usr/local/mail/openmail") {
			sh.C("podman-compose", "up", "-d", "mailserver", "roundcube").Dir("/usr/local/mail/openmail").Timeout(30 * time.Second).Quiet().Run()
		}
		time.Sleep(2 * time.Second)
	}
	mnt := sh.C("podman", "inspect", restoreMailContainer, "--format", `{{ range .Mounts }}{{ if eq .Destination "/tmp/docker-mailserver" }}{{ .Source }}{{ end }}{{ end }}`).NoErr().Str()

	// merge a simple line-based config file, skipping lines whose key (up to first |/space) already exists
	mergeCF := func(src, dst, label string) {
		if !nonEmptyFile(src) {
			return
		}
		appendFile(dst, "")
		added, skipped := 0, 0
		for _, l := range readLines(src) {
			if l == "" {
				continue
			}
			key, _, _ := strings.Cut(l, "|")
			if strings.Contains(conf.Read(dst), key+"|") {
				skipped++
			} else {
				appendFile(dst, l+"\n")
				added++
			}
		}
		r.log(fmt.Sprintf("%s: %d added, %d skipped.", label, added, skipped))
	}

	cfg := mnt
	if cfg == "" {
		cfg = dms
	}
	adst := cfg + "/postfix-accounts.cf"
	r.log("Merging email accounts ...")
	var skippedList []string
	for _, l := range readLines(asrc) {
		addr, hash, _ := strings.Cut(l, "|")
		if addr == "" {
			continue
		}
		re, err := regexp.Compile("^" + addr + `\|`)
		exists := false
		for _, x := range conf.Lines(adst) {
			if err == nil && re.MatchString(x) || err != nil && strings.HasPrefix(x, addr+"|") {
				exists = true
				break
			}
		}
		if exists {
			r.log("[skip] Mailbox '" + addr + "' exists.")
			r.emailsSkipped++
			skippedList = append(skippedList, addr)
			continue
		}
		appendFile(adst, addr+"|"+hash+"\n")
		r.emailsAdded++
		r.log("Mailbox restored: " + addr)
	}

	mergeCF(ed+"/postfix-regex.cf", cfg+"/postfix-regex.cf", "aliases (postfix-regex.cf)")
	mergeCF(ed+"/dovecot-quotas.cf", cfg+"/dovecot-quotas.cf", "quotas (dovecot-quotas.cf)")
	mergeCF(ed+"/postfix-receive-access.cf", cfg+"/postfix-receive-access.cf", "receive-access (postfix-receive-access.cf)")
	mergeCF(ed+"/postfix-send-access.cf", cfg+"/postfix-send-access.cf", "send-access (postfix-send-access.cf)")
	mergeCF(ed+"/postfix-virtual.cf", cfg+"/postfix-virtual.cf", "catch-all (postfix-virtual.cf)")
	sh.Opencli("email-default_sync").Run()

	// postfwd.cf, append non-duplicate rule blocks (two-line id=…/action=… pairs)
	if nonEmptyFile(ed+"/postfwd.cf") && conf.IsFile(postfwdDst) {
		added := 0
		lines := strings.Split(conf.Read(ed+"/postfwd.cf"), "\n")
		// the trailing piece after the last newline isn't a line for read
		lines = lines[:len(lines)-1]
		for i := 0; i < len(lines); i += 2 {
			id := lines[i]
			action := ""
			if i+1 < len(lines) {
				action = lines[i+1]
			}
			if id == "" {
				continue
			}
			if !strings.Contains(conf.Read(postfwdDst), id) {
				appendFile(postfwdDst, id+"\n"+action+"\n")
				added++
			}
		}
		r.log(fmt.Sprintf("postfwd.cf: %d rule(s) added.", added))
	}
	if r.emailsAdded > 0 {
		if !sh.C("podman", "exec", restoreMailContainer, "postfix", "reload").Quiet().OK() {
			sh.C("podman", "restart", restoreMailContainer).Quiet().Run()
		}
	}
}

// 8) Podman
func (r *restoreJob) restoreDocker() {
	// only relevant when restoring a backup taken before the podman migration, since podman doesn't use rootlesskit
	if f := r.work + "/docker/apparmor.profile"; conf.IsFile(f) {
		cpA(f, "/etc/apparmor.d/home."+r.context+".bin.rootlesskit")
		sh.C("systemctl", "restart", "apparmor.service").Quiet().Run()
	}
	if conf.IsDir("/home/" + r.context + "/.config/containers") {
		if uid := statUID("/home/" + r.context); uid != "" {
			// context resolution is dynamic based on /home/$CONTEXT's owner uid, nothing to register anymore, just make sure the user's rootless podman.socket is enabled and running (mirrors user-add)
			sh.C("loginctl", "enable-linger", r.context).Quiet().Run()
			// enable-linger doesn't block until the user's systemd instance is actually up, and `systemctl --user` fails before user@<uid>.service is active, so wait for it first
			for w := 0; w < 30; w++ {
				if sh.C("systemctl", "is-active", "user@"+uid+".service").Quiet().OK() {
					break
				}
				time.Sleep(time.Second)
			}
			uc := func(a ...string) *sh.Cmd {
				return sh.C("systemctl", append([]string{"--user", "-M", r.context + "@"}, a...)...).Quiet()
			}
			uc("daemon-reload").Run()
			uc("reset-failed", "podman.socket").Run()
			uc("enable", "--now", "podman.socket").Run()
			if !uc("is-active", "podman.socket").OK() {
				time.Sleep(2 * time.Second)
				uc("start", "podman.socket").Run()
			}
			if !uc("is-active", "podman.socket").OK() {
				r.warn("podman.socket did not come up for '" + r.context + "' (user@" + uid + ".service may not have started in time) — containers below may fail to start.")
			}
		}
	}
	f := r.work + "/docker/containers.txt"
	if !conf.IsFile(f) || !conf.IsFile(podman.ComposeFile(r.context)) {
		return
	}
	first := text.FirstLine(conf.Read(f))
	ctx, containers, _ := strings.Cut(first, ":")
	ctx, containers = text.Xargs(ctx), text.Xargs(containers)
	if ctx == "" || containers == "no containers" || containers == "" {
		return
	}
	names := strings.Fields(containers)
	cf := podman.ComposeFile(ctx)
	r.log(fmt.Sprintf("Starting %d container(s) for %s ...", len(names), ctx))
	podman.ComposeUser(ctx, "-f", cf, "down").Quiet().Run()
	podman.ComposeUser(ctx, append([]string{"-f", cf, "up", "-d"}, names...)...).Quiet().Run()
}
