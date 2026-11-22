// ##############################################################################
// Script Name: internal/commands/docker/backup.go
// Description: Backs up every account whose backups the hosting provider manages, or one with --account.
// Usage: opencli docker-backup [--parallel=N | --account=USERNAME]   (skip a user: touch /home/<context>/backup.off)
// Author: Stefan Pejcic
// Created: 22.07.2025
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

package docker

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "docker-backup",
		Desc:  "Backs up every account whose backups the hosting provider manages, or one with --account.",
		Usage: "opencli docker-backup [--parallel=N | --account=USERNAME]   (skip a user: touch /home/<context>/backup.off)",
		Run:   backup,
	})
}

const (
	backupLog      = "/var/log/openpanel/admin/docker-backup.log"
	backupLocked   = 75 // a backup that finds its account locked by another run exits with this
	backupLockDir  = "/run/lock"
	backupCoreDir  = "/etc/openpanel/openpanel/core/users"
	backupProxyMax = 15 // https://github.com/stefanpejcic/OpenPanel/discussions/1146#discussioncomment-18528896
	backupSettle   = 2
)

var (
	backupParallelRe = regexp.MustCompile(`^[1-9][0-9]*$`)
	backupJobRe      = regexp.MustCompile(`(?m)^[ \t\n\v\f\r]*\[job-`)
)

// backupRun is the per user state, parallel workers write to their own log file and stay silent
type backupRun struct {
	logFile      string
	quiet        bool
	proxyStarted bool
}

func backupNow() string { return time.Now().Format("2006-01-02 15:04:05") }

func (r *backupRun) file() string {
	if r.logFile != "" {
		return r.logFile
	}
	return backupLog
}

// echo text | tee -a logfile
func (r *backupRun) raw(text string) {
	if !r.quiet {
		fmt.Println(text)
	}
	if f, err := os.OpenFile(r.file(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666); err == nil {
		f.WriteString(text + "\n")
		f.Close()
	}
}

func (r *backupRun) log(msg string) { r.raw(backupNow() + " : " + msg) }

func backupContext(username string) string {
	ctx := db.Plain("SELECT server FROM users WHERE username = '"+db.Escape(username)+"'", "-D", db.Database, "-s", "-N").Str()
	if ctx == "" {
		return username
	}
	return ctx
}

// active, suspended or missing, a suspended account is renamed to SUSPENDED_<time>_<username>
func backupAccountState(name string) string {
	if strings.HasPrefix(name, "SUSPENDED_") {
		return "suspended"
	}
	esc := db.Escape(name)
	return db.Plain(`SELECT CASE
        WHEN EXISTS (SELECT 1 FROM users WHERE username = '`+esc+`') THEN 'active'
        WHEN EXISTS (SELECT 1 FROM users WHERE username LIKE 'SUSPENDED\_%' AND SUBSTRING_INDEX(username, '_', -1) = '`+esc+`') THEN 'suspended'
        ELSE 'missing' END`, "-D", db.Database, "-s", "-N").Str()
}

func backupProviderManaged(ctx string) bool {
	_, err := os.Lstat(backupCoreDir + "/" + ctx + "/admin.backups")
	return err == nil
}

// sentinel only logs/sends it if docker_backup is enabled in notifications.ini
func backupNotifyFailure(message string) {
	details := ojson.Compact(ojson.New().Set("log_file", backupLog))
	sh.Opencli("sentinel", "--action=docker_backup", "--severity=critical", "--title=Backup failed",
		"--message="+message+" Detailed report was saved in: "+backupLog, "--details="+details).Quiet().Background()
}

func (r *backupRun) proxyReady(ctx string) bool {
	return podman.CtxIsRunning(ctx, "docker-proxy") && podman.Ctx(ctx, "exec", "docker-proxy", "sh", "-c", "nc -z 127.0.0.1 2375").Quiet().OK()
}

func (r *backupRun) waitProxy(ctx string) bool {
	for waited := 0; waited < backupProxyMax; waited++ {
		if r.proxyReady(ctx) {
			r.log(fmt.Sprintf("docker-proxy is ready for context: %s (after %ds)", ctx, waited))
			time.Sleep(backupSettle * time.Second)
			return true
		}
		time.Sleep(time.Second)
	}
	r.log(fmt.Sprintf("ERROR: docker-proxy not ready for context: %s within %ds", ctx, backupProxyMax))
	out, _ := podman.Ctx(ctx, "logs", "--tail", "20", "docker-proxy").Combined()
	r.raw(out)
	return false
}

func (r *backupRun) ensureProxy(ctx string) bool {
	if !podman.CtxIsRunning(ctx, "docker-proxy") {
		r.log("docker-proxy not running for context: " + ctx + " — starting it")
		r.proxyStarted = true
		out, err := podman.ComposeCtx(ctx, "up", "-d", "docker-proxy").Dir("/home/" + ctx).Combined()
		r.raw(out)
		if err != nil {
			r.log(fmt.Sprintf("ERROR: failed to start docker-proxy for context: %s | exit code: %d", ctx, sh.Code(err)))
			return false
		}
	}
	return r.waitProxy(ctx)
}

// db containers started before backup_meta was in the compose file need one recreate, otherwise the manifest misses their dumps and the panel has to list the whole archive
func (r *backupRun) refreshDB(ctx string) {
	if !strings.Contains(backupRead(podman.ComposeFile(ctx)), "backup_meta:/tmp/backup_meta") {
		return
	}
	for _, svc := range []string{"mysql", "mariadb", "postgres"} {
		if !podman.CtxIsRunning(ctx, svc) {
			continue
		}
		mounts := podman.Ctx(ctx, "inspect", "-f", "{{range .Mounts}}{{.Destination}} {{end}}", svc).NoErr().Str()
		if strings.Contains(mounts, "/tmp/backup_meta") {
			continue
		}
		r.log(fmt.Sprintf("Recreating %s for context: %s to add the backup_meta volume", svc, ctx))
		out, err := podman.ComposeCtx(ctx, "up", "-d", "--no-deps", "--force-recreate", svc).Dir("/home/" + ctx).Combined()
		if err != nil {
			r.log(fmt.Sprintf("WARNING: failed to recreate %s for context: %s", svc, ctx))
			r.raw(out)
		}
	}
}

func backupRead(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// rebuild the user's /backups/list index so the new backup shows up without a manual reindex
func (r *backupRun) reindex(username, ctx string) {
	if !sh.C("podman", "container", "exists", "openpanel").Quiet().OK() || sh.C("podman", "inspect", "-f", "{{.State.Running}}", "openpanel").NoErr().Str() != "true" {
		r.log("WARNING: openpanel container is not running, skipped backups reindex for user: " + username)
		return
	}
	out, err := sh.C("podman", "exec", "openpanel", "/openpanel", "backups-reindex", ctx).Combined()
	if err == nil {
		r.log("Backups reindexed for user: " + username)
	} else {
		r.log("WARNING: backups reindex failed for user: " + username + " | " + out)
	}
}

// docker-proxy we started only for this backup goes back down unless the user's cron jobs need it
func (r *backupRun) stopProxy(ctx string) {
	if !r.proxyStarted {
		return
	}
	if backupJobRe.MatchString(backupRead("/home/" + ctx + "/crons.ini")) {
		return
	}
	podman.Ctx(ctx, "stop", "docker-proxy").Quiet().Run()
}

func (r *backupRun) runForUser(username string) int {
	r.proxyStarted = false
	ctx := backupContext(username)
	if st, err := os.Stat("/home/" + ctx + "/"); err != nil || !st.IsDir() {
		r.log("ERROR: Cannot cd into /home/" + ctx + "/")
		return 1
	}
	if !r.ensureProxy(ctx) {
		r.log(fmt.Sprintf("Backup FAILED for user: %s (context: %s) — docker-proxy unavailable", username, ctx))
		return 1
	}
	r.refreshDB(ctx)
	// a leftover backup container has the same archive-pre labels, files-list.sh would run in both and race on backup_meta
	if podman.Ctx(ctx, "container", "exists", "backup").Quiet().OK() {
		r.log("Removing leftover backup container for context: " + ctx)
		podman.Ctx(ctx, "rm", "-f", "backup").Quiet().Run()
	}
	start := time.Now().Unix()
	// --no-deps since we start docker-proxy ourselves, otherwise podman-compose run can tear down the whole stack (mariadb included)
	out, err := podman.ComposeCtx(ctx, "run", "--rm", "--no-deps", "--entrypoint", "backup", "backup").Dir("/home/" + ctx).Combined()
	exit := sh.Code(err)
	duration := time.Now().Unix() - start

	r.log("--- backup container output for user: " + username + " ---")
	r.raw(out)
	r.log("--- end of backup container output for user: " + username + " ---")

	// the run container is --rm, compose down backup would also take docker-proxy down and break the user's cron jobs until sentinel restarts it
	podman.Ctx(ctx, "rm", "-f", "backup").Quiet().Run()
	r.stopProxy(ctx)

	if exit != 0 {
		r.log(fmt.Sprintf("ERROR: Backup FAILED for user: %s (context: %s) | exit code: %d | Time taken: %ds", username, ctx, exit, duration))
		return 1
	}
	r.log(fmt.Sprintf("Backup completed for user: %s (context: %s) | Time taken: %ds", username, ctx, duration))
	r.reindex(username, ctx)
	return 0
}

// runs fn while holding the account's lock, so a scheduled run and --account never back up the same account at once
func backupWithLock(ctx string, fn func() int) int {
	f, err := os.OpenFile(backupLockDir+"/openpanel-docker-backup-"+ctx+".lock", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return 1
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return backupLocked
	}
	return fn()
}

func (r *backupRun) processUser(username string) int {
	r.log("Processing user: " + username)
	status := backupWithLock(backupContext(username), func() int { return r.runForUser(username) })
	if status == backupLocked {
		return backupLocked
	}
	if status != 0 {
		r.log("Failed processing user: " + username)
		return 1
	}
	return 0
}

// --account: back up one account now, with the same checks the scheduled run applies
func (r *backupRun) processOne(username string) int {
	r.log("=== Backup of user " + username + " started at " + backupNow() + " ===")
	switch backupAccountState(username) {
	case "active":
	case "suspended":
		r.log("ERROR: user " + username + " is suspended, suspended accounts are not backed up")
		return 1
	default:
		r.log("ERROR: user " + username + " does not exist")
		return 1
	}
	ctx := backupContext(username)
	if _, err := os.Lstat("/home/" + ctx + "/backup.off"); err == nil {
		r.log("ERROR: user " + username + " is excluded from backups (/home/" + ctx + "/backup.off exists)")
		return 1
	}
	if !backupProviderManaged(ctx) {
		r.log("ERROR: user " + username + " manages its own backups (" + backupCoreDir + "/" + ctx + "/admin.backups doesn't exist)")
		return 1
	}

	status := r.processUser(username)
	if status == backupLocked {
		r.log("ERROR: a backup of user " + username + " is already running")
		return backupLocked
	}
	if status != 0 {
		backupNotifyFailure("Backup failed for user " + username + ".")
		return 1
	}
	r.log("=== Backup of user " + username + " finished at " + backupNow() + " ===")
	return 0
}

// the log line goes to the file only, bash captured it in the command substitution
func (r *backupRun) activeUsers() ([]string, bool) {
	out, _ := sh.Opencli("user-list", "--local", "--json").NoErr().Out()
	var users []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "SUSPENDED") || !strings.Contains(l, "username") {
			continue
		}
		f := strings.Split(l, `"`)
		if len(f) > 3 {
			users = append(users, f[3])
		} else {
			users = append(users, "")
		}
	}
	joined := strings.TrimRight(strings.Join(users, "\n"), "\n")
	if joined == "" || joined == "No users." {
		quiet := *r
		quiet.quiet = true
		quiet.log("No active users found in the database")
		return nil, false
	}
	return strings.Fields(joined), true
}

type backupResult struct {
	user   string
	status int
}

func (r *backupRun) processAll(parallel string) int {
	start := time.Now().Unix()
	r.log("=== Backup started at " + backupNow() + " ===")

	r.log("Fetching list of active users...")
	users, ok := r.activeUsers()
	if !ok {
		r.log("ERROR: Could not retrieve active users from the database.")
		backupNotifyFailure("Backups did not run because the list of active users could not be retrieved from the database.")
		return 1
	}

	// users can be excluded with /home/<context>/backup.off, and users without the admin.backups marker run their own backups
	var kept, skipped, selfManaged []string
	for _, u := range users {
		ctx := backupContext(u)
		if _, err := os.Lstat("/home/" + ctx + "/backup.off"); err == nil {
			skipped = append(skipped, u)
			r.log("Skipping user: " + u + " (/home/" + ctx + "/backup.off exists)")
		} else if !backupProviderManaged(ctx) {
			selfManaged = append(selfManaged, u)
			r.log("Skipping user: " + u + " (manages its own backups)")
		} else {
			kept = append(kept, u)
		}
	}
	users = kept
	if len(skipped) > 0 {
		r.log(fmt.Sprintf("Skipped %d users with backup.off: %s", len(skipped), strings.Join(skipped, " ")))
	}
	if len(selfManaged) > 0 {
		r.log(fmt.Sprintf("Skipped %d users that manage their own backups: %s", len(selfManaged), strings.Join(selfManaged, " ")))
	}
	if len(kept) == 0 {
		r.log("No users to back up (all have backup.off or manage their own backups)")
		return 0
	}

	count := len(users)
	r.log(fmt.Sprintf("Found %d active users to process", count))

	n := 0
	if parallel == "" {
		n = runtime.NumCPU()
		if out := sh.Str("nproc"); out != "" {
			n, _ = strconv.Atoi(out)
		}
		n /= 4
		if n < 1 {
			n = 1
		}
	} else {
		n, _ = strconv.Atoi(parallel)
	}
	if n > count {
		n = count
	}

	var failed, locked []string
	if n <= 1 {
		for i, u := range users {
			r.log(fmt.Sprintf("Processing user: %s (%d/%d)", u, i+1, count))
			switch st := r.processUser(u); st {
			case 0:
			case backupLocked:
				r.log("Skipping user: " + u + " (a backup of this account is already running)")
				locked = append(locked, u)
			default:
				failed = append(failed, u)
			}
			r.log("------------------------------")
		}
	} else {
		r.log(fmt.Sprintf("Running up to %d user backups in parallel", n))
		tmpdir, _ := os.MkdirTemp("", "tmp.")
		done := make(chan backupResult)
		running := 0
		// wait for one worker, append its log in one piece, record failure
		reap := func() {
			res := <-done
			running--
			if b, err := os.ReadFile(tmpdir + "/" + res.user + ".log"); err == nil {
				if f, err := os.OpenFile(backupLog, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666); err == nil {
					f.Write(b)
					f.Close()
				}
			}
			if res.status == backupLocked {
				r.log("Skipping user: " + res.user + " (a backup of this account is already running)")
				locked = append(locked, res.user)
			} else if res.status != 0 {
				failed = append(failed, res.user)
			}
			if f, err := os.OpenFile(backupLog, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666); err == nil {
				f.WriteString(backupNow() + " : ------------------------------\n")
				f.Close()
			}
		}
		for i, u := range users {
			for running >= n {
				reap()
			}
			r.log(fmt.Sprintf("Processing user: %s (%d/%d)", u, i+1, count))
			running++
			go func(u string) {
				w := &backupRun{logFile: tmpdir + "/" + u + ".log", quiet: true}
				done <- backupResult{u, w.processUser(u)}
			}(u)
		}
		for running > 0 {
			reap()
		}
		os.RemoveAll(tmpdir)
	}

	total := time.Now().Unix() - start

	if len(locked) > 0 {
		r.log(fmt.Sprintf("Skipped %d users that were already being backed up: %s", len(locked), strings.Join(locked, " ")))
	}
	switch {
	case len(failed) == 0 && len(locked) > 0:
		r.log(fmt.Sprintf("Successfully processed %d of %d users", count-len(locked), count))
	case len(failed) == 0:
		r.log(fmt.Sprintf("Successfully processed all %d users", count))
	default:
		r.log(fmt.Sprintf("Failed to process %d users: %s", len(failed), strings.Join(failed, " ")))
		backupNotifyFailure(fmt.Sprintf("Backup failed for %d of %d users: %s.", len(failed), count, strings.Join(failed, " ")))
		return 1
	}

	r.log(fmt.Sprintf("=== Backup finished at %s | Total time: %ds ===", backupNow(), total))
	return 0
}

func backup(args []string) int {
	parallel, account := "", ""
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--parallel="):
			parallel = strings.TrimPrefix(a, "--parallel=")
		case strings.HasPrefix(a, "--account="):
			account = strings.TrimPrefix(a, "--account=")
			if account == "" {
				fmt.Println("--account needs a username")
				return 1
			}
		default:
			fmt.Println("Usage: opencli docker-backup [--parallel=N | --account=USERNAME]")
			return 1
		}
	}
	if parallel != "" && !backupParallelRe.MatchString(parallel) {
		fmt.Println("--parallel must be a positive number")
		return 1
	}
	if account != "" && parallel != "" {
		fmt.Println("--parallel and --account can't be used together")
		return 1
	}
	r := &backupRun{}
	if account != "" {
		return r.processOne(account)
	}
	return r.processAll(parallel)
}
