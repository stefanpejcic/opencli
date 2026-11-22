// ##############################################################################
// Script Name: internal/commands/sentinel/sentinel_containers.go
// Description: Check system services, traffic and resource usage, and log/send custom notifications on request.
// Usage: opencli sentinel [--startup] [--report] [--action=<name> --title=<title> --message=<msg> [--severity=<level>] [--details=<json> [--merge-key=<key>]] [--no-send]]
// Author: Stefan Pejcic
// Created: 01.11.2023
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

package sentinel

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/mail"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

// restartResults replaces the results files the parallel starts appended to
type restartResults struct {
	mu     sync.Mutex
	counts [][2]string // label, count
	names  [][2]string // label, name
	events []string    // label\tname\toom\tresult
}

func (rr *restartResults) count(label string, n int) {
	rr.mu.Lock()
	rr.counts = append(rr.counts, [2]string{label, strconv.Itoa(n)})
	rr.mu.Unlock()
}

func (rr *restartResults) name(label, name string) {
	rr.mu.Lock()
	rr.names = append(rr.names, [2]string{label, name})
	rr.mu.Unlock()
}

// restart state the summary is built from
type restartState struct {
	rootCount, userTotal int
	userLines            []string
	rootNames            string
	events               string
	elapsed              int
}

func isRunningAt(sock, name string) bool {
	c := sh.C("podman", "inspect", name, "--format", "{{.State.Status}}")
	if sock != "" {
		c.Env("CONTAINER_HOST=unix://" + sock)
	}
	return c.NoErr().Str() == "running"
}

// podman_ensure_running against sock, recovers via podman-compose if exited/absent or force-removes a wedged one first
func ensureRunningAt(sock, container, dir, service string, timeout int) bool {
	p := func(args ...string) *sh.Cmd {
		c := sh.C("podman", args...)
		if sock != "" {
			c.Env("CONTAINER_HOST=unix://" + sock)
		}
		return c
	}
	state := p("inspect", container, "--format", "{{.State.Status}}").NoErr().Str()
	if state == "running" {
		return true
	}
	if state != "" && state != "exited" && state != "stopped" {
		p("kill", container).Quiet().Run()
		p("rm", "-f", container).Quiet().Run()
		p("rm", "-f", "--storage", container).Quiet().Run()
	}
	// --no-deps: without it podman-compose 1.0.6 can remove other services of the stack and not start them again
	c := tmo(timeout, "podman-compose", "up", "-d", "--no-deps", service).Dir(dir).Quiet()
	if sock != "" {
		c.Env("CONTAINER_HOST=unix://" + sock)
	}
	c.Run()
	return isRunningAt(sock, container)
}

// starts/recovers containers for root or a user's podman socket, podman start is tried first, falling back to restart
func (r *run) startContainersForSocket(sock, label string, rr *restartResults, skip string) {
	skip = " " + skip + " "
	if st, err := os.Stat(sock); err != nil || st.Mode()&os.ModeSocket == 0 {
		r.printf("no socket at %s for %s, skipping\n", sock, label)
		rr.count(label, 0)
		return
	}
	var dead []string
	for _, l := range strings.Split(atSock(sock, "ps", "-a", "--format", "{{.ID}} {{.State}}").Timeout(20*time.Second).NoErr().Str(), "\n") {
		f := fields(l)
		st := ""
		if len(f) > 1 {
			st = f[1]
		}
		if len(f) > 0 && st != "running" {
			dead = append(dead, f[0]+"|"+st)
		}
	}
	if len(dead) == 0 {
		r.printf("%s %s: no dead containers\n", cOK, label)
		rr.count(label, 0)
		return
	}
	count := 0
	for _, entry := range dead {
		id, state, _ := strings.Cut(entry, "|")
		info := text1(atSock(sock, "inspect", id, "--format", "{{.Name}}|{{.State.OOMKilled}}").Timeout(10 * time.Second).NoErr().Str())
		name, oom, _ := strings.Cut(info, "|")
		if name == "" {
			name = id
		}
		// containers sentinel stops on purpose, startConditionalContainersForUser handles those
		if strings.Contains(skip, " "+name+" ") {
			continue
		}
		r.printf("%s: starting %s (was: %s)\n", label, name, state)
		result := ""
		if atSock(sock, "start", id).Timeout(30*time.Second).Quiet().Run() == nil || atSock(sock, "restart", id).Timeout(30*time.Second).Quiet().Run() == nil {
			count++
			result = "restarted"
			rr.name(label, name)
		} else {
			r.printf("%s %s: FAILED to start %s\n", cFail, label, name)
			result = "failed"
		}
		// per user list of what was down, emailed to the user by notifyUsersOfFailedServices
		if label != "root" {
			if oom == "" {
				oom = "false"
			}
			rr.mu.Lock()
			rr.events = append(rr.events, label+"\t"+name+"\t"+oom+"\t"+result)
			rr.mu.Unlock()
		}
	}
	rr.count(label, count)
}

// text1 is the first line, like read does
func text1(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// seconds since <user>'s account was created, from /home birth time or the panel's registered_date where the filesystem doesn't keep one
func accountAge(u string) string {
	born := sh.C("stat", "-c", "%W", "/home/"+u).NoErr().Str()
	if n, err := strconv.ParseInt(born, 10, 64); err == nil && regexp.MustCompile(`^[0-9]+$`).MatchString(born) && n > 0 {
		return strconv.FormatInt(time.Now().Unix()-n, 10)
	}
	return db.Cmd("SELECT UNIX_TIMESTAMP() - UNIX_TIMESTAMP(registered_date) FROM users WHERE server = '"+u+"' LIMIT 1", "-N", "-B").Timeout(10 * time.Second).NoErr().Str()
}

func firstMatchValue(file, prefix string) string {
	for _, l := range conf.Lines(file) {
		if strings.HasPrefix(l, prefix) {
			return strings.TrimPrefix(l, prefix)
		}
	}
	return ""
}

// reads the datadir on disk instead of asking mysql, so it costs the user nothing and works while it's stopped
func userHasMysqlDatabases(u string) bool {
	datadir := "/home/" + u + "/docker-data/volumes/" + u + "_mysql_data/_data"
	if !isDir(datadir) {
		return false
	}
	restricted := strings.NewReplacer(`"`, "", "'", "").Replace(firstMatchValue(confFile, "mysql_restricted_databases="))
	if restricted == "" {
		restricted = "information_schema performance_schema mysql phpmyadmin sys mariadb.sys"
	}
	restricted = " " + restricted + " "
	dirs, _ := filepath.Glob(datadir + "/*")
	sort.Strings(dirs)
	for _, d := range dirs {
		if !isDir(d) {
			continue
		}
		name := filepath.Base(d)
		if strings.HasPrefix(name, "#") || strings.Contains(restricted, " "+name+" ") {
			continue
		}
		return true
	}
	return false
}

// works out which on-demand containers <user> needs right now
func planConditionalContainers(u string) (needed, unneeded []string) {
	home := "/home/" + u
	cron, backup := false, false
	// disabled jobs are fully commented out in crons.ini, so only count active [job-*] blocks
	if regexp.MustCompile(`(?m)^[[:space:]]*\[job-`).MatchString(conf.Read(home + "/crons.ini")) {
		cron = true
	}
	// backup if backup.env has any active remote backup config
	if isFile(home+"/backup.env") && regexp.MustCompile(`(?m)^(WEBDAV_URL|AWS_S3_BUCKET_NAME|SSH_HOST_NAME|AZURE_STORAGE_ACCOUNT_NAME|DROPBOX_REMOTE_PATH)=`).MatchString(conf.Read(home+"/backup.env")) {
		backup = true
	}
	// admin managed backups run centrally via opencli docker-backup, the user's own backup daemon would only double them
	if exists("/etc/openpanel/openpanel/core/users/" + u + "/admin.backups") {
		backup = false
	}
	// https://github.com/stefanpejcic/OpenPanel/issues/1132
	// docker-proxy goes first when starting and last when stopping, cron and backup talk to podman through it
	if cron || backup {
		needed = append(needed, "docker-proxy")
	}
	if cron {
		needed = append(needed, "cron")
	} else {
		unneeded = append(unneeded, "cron")
	}
	if backup {
		needed = append(needed, "backup")
	} else {
		unneeded = append(unneeded, "backup")
	}
	if !cron && !backup {
		unneeded = append(unneeded, "docker-proxy")
	}

	mtype := strings.NewReplacer(`"`, "", "'", "", " ", "").Replace(firstMatchValue(home+"/.env", "MYSQL_TYPE="))
	if mtype != "mysql" && mtype != "mariadb" {
		return
	}
	// both share the mysql_data volume, so the type the account isn't on must never run
	if mtype == "mariadb" {
		unneeded = append(unneeded, "mysql")
	} else {
		unneeded = append(unneeded, "mariadb")
	}
	// no databases means nothing to serve, new accounts get 2h to create their first one before it's stopped
	idle := false
	if age := accountAge(u); age != "" && atoi(age) > mysqlIdleGrace {
		idle = true
	}
	if userHasMysqlDatabases(u) {
		needed = append(needed, mtype)
	} else if idle {
		unneeded = append(unneeded, mtype)
	}
	return
}

// pgrepF is pgrep -f >/dev/null
func pgrepF(pattern string) bool { return sh.C("pgrep", "-f", pattern).Quiet().Run() == nil }

// starts containers a feature needs but that podman never saw "die" and stops the ones nothing uses anymore
func (r *run) startConditionalContainersForUser(u, sock string, rr *restartResults, needed, unneeded []string) {
	home := "/home/" + u
	// central opencli docker-backup in progress, it starts docker-proxy before its one-off backup container exists
	centralBackup := pgrepF(`/usr/local/opencli/docker/[b]ackup\.sh|opencli[^ ]* docker-backu[p]`)

	for _, svc := range unneeded {
		if !isRunningAt(sock, svc) {
			continue
		}
		if centralBackup && (svc == "docker-proxy" || svc == "backup") {
			continue
		}
		// docker-backup starts docker-proxy itself and runs backup as a one-off container, don't pull it out from under that
		if svc == "docker-proxy" && atSock(sock, "ps", "--filter", "name=backup", "--format", "{{.Names}}").Timeout(10*time.Second).NoErr().Str() != "" {
			continue
		}
		r.printf("%s: stopping %s (not needed)\n", u, svc)
		if atSock(sock, "stop", "-t", "30", svc).Timeout(60*time.Second).Quiet().Run() != nil {
			r.printf("%s %s: FAILED to stop %s\n", cFail, u, svc)
		}
	}
	if len(needed) == 0 {
		return
	}
	count := 0
	for _, svc := range needed {
		if isRunningAt(sock, svc) {
			continue
		}
		r.printf("%s: starting required container %s\n", u, svc)
		if ensureRunningAt(sock, svc, home, svc, 20) {
			count++
			rr.name(u, svc)
		} else {
			r.printf("%s %s: FAILED to start required container %s\n", cFail, u, svc)
		}
	}
	if count > 0 {
		rr.count(u, count)
	}
}

func (r *run) startContainersForUser(u string, rr *restartResults) {
	usr, err := user.Lookup(u)
	if err != nil {
		return
	}
	sock := "/run/user/" + usr.Uid + "/podman/podman.sock"
	if st, err := os.Stat(sock); err != nil || st.Mode()&os.ModeSocket == 0 {
		r.printf("%s %s: no active podman socket at %s, skipping\n", cWarn, u, sock)
		return
	}
	needed, unneeded := planConditionalContainers(u)
	r.startContainersForSocket(sock, u, rr, strings.Join(unneeded, " "))
	r.startConditionalContainersForUser(u, sock, rr, needed, unneeded)
}

// on boot blocks while IO pressure or load is high so user starts don't pile up, gives up after 60s
func waitForIOToSettle(cores int) {
	for waited := 0; waited < 60; waited += 2 {
		io := 0
		for _, l := range conf.Lines("/proc/pressure/io") {
			if strings.HasPrefix(l, "some") {
				_, v, _ := strings.Cut(field(l, 2), "=")
				f, _ := strconv.ParseFloat(v, 64)
				io = int(f)
			}
		}
		lf, _ := strconv.ParseFloat(field(conf.Read("/proc/loadavg"), 1), 64)
		if io < 40 && int(lf) < cores*2 {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// true if any account has an FTP user, one per line in users.list
func ftpAccountsExist() bool {
	files, _ := filepath.Glob("/etc/openpanel/ftp/users/*/users.list")
	sort.Strings(files)
	for _, f := range files {
		if isFile(f) && strings.TrimSpace(conf.Read(f)) != "" {
			return true
		}
	}
	return false
}

// openadmin_ftp only runs while there's an FTP account, ftp-add starts it again for the first one
func (r *run) stopFTPIfUnneeded() bool {
	if ftpAccountsExist() {
		return false
	}
	// ftp-add starts the container before it writes users.list
	if pgrepF(`/usr/local/opencli/ftp/[a]dd\.sh|opencli[^ ]* ftp-ad[d]`) {
		return false
	}
	if isRunningAt("", "openadmin_ftp") {
		r.println("root: stopping openadmin_ftp (no FTP accounts)")
		if tmo(60, "podman", "stop", "-t", "10", "openadmin_ftp").Quiet().Run() != nil {
			r.printf("%s root: FAILED to stop openadmin_ftp\n", cFail)
		}
	}
	return true
}

func homeUsers() []string {
	dirs, _ := filepath.Glob("/home/*")
	sort.Strings(dirs)
	var out []string
	for _, d := range dirs {
		if isDir(d) {
			out = append(out, filepath.Base(d))
		}
	}
	return out
}

// loops root then every non-suspended user starting/recovering dead containers
func (r *run) restartDeadUserContainers(mode string) restartState {
	start := time.Now()
	rr := &restartResults{}

	// root first
	rootSkip := ""
	if r.stopFTPIfUnneeded() {
		rootSkip = "openadmin_ftp"
	}
	r.startContainersForSocket("/run/podman/podman.sock", "root", rr, rootSkip)

	// try opencli user-list --json to get suspended users that we will exclude
	validJSON := false
	suspended := map[string]bool{}
	if out := sh.Opencli("user-list", "--local", "--json").Timeout(15 * time.Second).NoErr().Str(); out != "" && sh.Has("jq") {
		if o, err := ojson.ParseObject(strings.TrimSpace(out)); err == nil {
			if d, ok := o.Get("data"); ok && d != nil && d != false {
				validJSON = true
				if arr, ok := d.([]any); ok {
					for _, it := range arr {
						if u, ok := it.(*ojson.Object); ok && strings.HasPrefix(u.Str("username"), "SUSPENDED_") {
							suspended[u.Str("context")] = true
						}
					}
				}
			}
		}
	}

	var users []string
	for _, u := range homeUsers() {
		if validJSON && suspended[u] {
			r.printf("%s: suspended, skipping\n", u)
			continue
		}
		users = append(users, u)
	}

	// if <=3 users run sequentially, on boot use a staged start queue, else in parallel batches of cores x 2
	n := len(users)
	lw := &lockedWriter{w: r.out}
	pr := &run{out: lw}
	cores := runtime.NumCPU()
	switch {
	case n <= 3:
		for _, u := range users {
			r.startContainersForUser(u, rr)
		}
	case mode == "staged":
		maxJobs := cores / 2
		if maxJobs < 2 {
			maxJobs = 2
		}
		if maxJobs > 8 {
			maxJobs = 8
		}
		pr.printf("%d users to start, staged (max %d at a time, %ds apart, waiting on high IO/load)\n", n, maxJobs, startupStagger)
		sem := make(chan struct{}, maxJobs)
		var wg sync.WaitGroup
		for i, u := range users {
			sem <- struct{}{}
			waitForIOToSettle(cores)
			pr.printf("[%d/%d] %s: starting containers\n", i+1, n, u)
			wg.Add(1)
			go func(u string) {
				defer func() { <-sem; wg.Done() }()
				pr.startContainersForUser(u, rr)
			}(u)
			time.Sleep(startupStagger * time.Second)
		}
		wg.Wait()
	default:
		jobs := cores * 2
		pr.printf("%d users to process, running in parallel (max %d jobs, %d cores x2)\n", n, jobs, cores)
		sem := make(chan struct{}, jobs)
		var wg sync.WaitGroup
		for _, u := range users {
			sem <- struct{}{}
			wg.Add(1)
			go func(u string) {
				defer func() { <-sem; wg.Done() }()
				pr.startContainersForUser(u, rr)
			}(u)
		}
		wg.Wait()
	}

	// results
	var st restartState
	perUser := map[string]int{}
	var order []string
	for _, c := range rr.counts {
		if c[0] == "" {
			continue
		}
		if c[0] == "root" {
			st.rootCount = atoi(c[1])
		} else {
			st.userTotal += atoi(c[1])
			if _, ok := perUser[c[0]]; !ok {
				order = append(order, c[0])
			}
			perUser[c[0]] += atoi(c[1])
		}
	}
	// names of what was started, per user (a user can have some from the sweep and some from the required containers)
	names := map[string]string{}
	for _, nm := range rr.names {
		if nm[0] == "" || nm[1] == "" {
			continue
		}
		if nm[0] == "root" {
			if st.rootNames != "" {
				st.rootNames += ", "
			}
			st.rootNames += nm[1]
		} else {
			if names[nm[0]] != "" {
				names[nm[0]] += ", "
			}
			names[nm[0]] += nm[1]
		}
	}
	for _, l := range order {
		if perUser[l] > 0 {
			v := names[l]
			if v == "" {
				v = fmt.Sprintf("%d container(s)", perUser[l])
			}
			st.userLines = append(st.userLines, l+": "+v)
		}
	}
	st.events = strings.Join(rr.events, "\n")
	st.elapsed = int(time.Since(start).Seconds())
	return st
}

// "Started php-fpm-8.3 for 2 users in 5s" with one line per user naming what was started, root only when something of it was
func restartSummary(verb string, st restartState) string {
	var parts []string
	if st.rootCount > 0 {
		rn := st.rootNames
		if rn == "" {
			rn = "names unknown"
		}
		parts = append(parts, fmt.Sprintf("%d system container(s) (%s)", st.rootCount, rn))
	}
	if st.userTotal > 0 {
		parts = append(parts, fmt.Sprintf("%d container(s) of %d user(s)", st.userTotal, len(st.userLines)))
	}
	p0, p1 := "", ""
	if len(parts) > 0 {
		p0 = parts[0]
	}
	if len(parts) > 1 {
		p1 = " and " + parts[1]
	}
	msg := fmt.Sprintf("%s %s%s in %ds.", verb, p0, p1, st.elapsed)
	for _, l := range st.userLines {
		msg += "\n- " + l
	}
	return msg
}

var (
	ipv4Re    = regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}$`)
	privateRe = regexp.MustCompile(`^(10\.|127\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.|100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.)`)
)

// FTP passive mode hands clients FTP_PASV_ADDRESS from /root/.env, so after a reboot on a new IP it's
// rewritten and openadmin_ftp recreated (env is fixed at container create, a restart keeps the old one)
func (r *run) updateFTPPasvAddress() {
	envFile := "/root/.env"
	if !isFile(envFile) {
		return
	}
	os.Remove(ipCacheFile) // a cached IP may be from before the change
	ip := getPublicIP()
	if !ipv4Re.MatchString(ip) {
		r.println("[!] FTP passive address: no valid public IPv4, left as is")
		return
	}
	// getPublicIP falls back to the interface IP, which behind NAT is private and useless to FTP clients
	if privateRe.MatchString(ip) {
		r.printf("[!] FTP passive address: %s is private, left as is\n", ip)
		return
	}
	current := ""
	has := false
	ls := strings.Split(conf.Read(envFile), "\n")
	for _, l := range ls {
		if strings.HasPrefix(l, "FTP_PASV_ADDRESS=") {
			if !has {
				v := strings.TrimPrefix(strings.TrimPrefix(l, "FTP_PASV_ADDRESS="), `"`)
				current, _, _ = strings.Cut(v, `"`)
			}
			has = true
		}
	}
	if current == ip {
		return
	}
	if has {
		for i, l := range ls {
			if strings.HasPrefix(l, "FTP_PASV_ADDRESS=") {
				ls[i] = `FTP_PASV_ADDRESS="` + ip + `"`
			}
		}
		conf.WriteFileKeep(envFile, strings.Join(ls, "\n"), false)
	} else if f, err := os.OpenFile(envFile, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintf(f, "\n# public IPv4 for FTP passive mode, rewritten by sentinel on boot if the IP changes\nFTP_PASV_ADDRESS=\"%s\"\n", ip)
		f.Close()
	}
	cur := current
	if cur == "" {
		cur = "unset"
	}
	r.printf("[*] FTP passive address: %s -> %s\n", cur, ip)

	// only when the compose file passes it to the container, otherwise recreating changes nothing
	if !strings.Contains(conf.Read("/root/docker-compose.yml"), "PASV_ADDRESS=${FTP_PASV_ADDRESS") {
		return
	}
	if tmo(30, "podman", "container", "exists", "openadmin_ftp").Run() != nil {
		return
	}
	// --no-deps: without it podman-compose 1.0.6 can remove other services of the stack
	if tmo(120, "podman-compose", "up", "-d", "--no-deps", "--force-recreate", "openadmin_ftp").Dir("/root").Quiet().Run() != nil {
		r.println("[!] Could not recreate openadmin_ftp, run: cd /root && podman-compose up -d --force-recreate openadmin_ftp")
	}
}

var rebootRe = regexp.MustCompile(`Power key pressed|Power button|System is rebooting|System is powering down|The system will (reboot|power off)|Shutdown scheduled|unattended-upgrade.*[Rr]eboot|COMMAND=.*(reboot|shutdown|poweroff|halt|init [06])|Kernel panic|Out of memory: Killed|watchdog: .*(hard|soft) LOCKUP|Linux version [0-9]|Stopped target multi-user\.target`)

var isoTSRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T`)

func readMaybeGz(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	var rd io.Reader = f
	if strings.HasSuffix(p, ".gz") {
		if gz, err := gzip.NewReader(f); err == nil {
			rd = gz
		}
	}
	b, _ := io.ReadAll(rd)
	return string(b)
}

// marker lines of the boot before this one: journal of the previous boot, plus auth/kern/syslog lines older than this boot
func rebootMarkerLines(bootISO string) []string {
	var all []string
	for _, l := range strings.Split(tmo(20, "journalctl", "-b", "-1", "--no-pager", "-o", "short-iso").NoErr().Str(), "\n") {
		if rebootRe.MatchString(l) {
			all = append(all, l)
		}
	}
	for _, f := range []string{"/var/log/auth.log.2.gz", "/var/log/auth.log.1.gz", "/var/log/auth.log.1", "/var/log/auth.log",
		"/var/log/kern.log.1.gz", "/var/log/kern.log.1", "/var/log/kern.log", "/var/log/syslog.1", "/var/log/syslog"} {
		if isFile(f) {
			for _, l := range lines(readMaybeGz(f)) {
				if rebootRe.MatchString(l) {
					all = append(all, l)
				}
			}
		}
	}
	var keep []string
	for _, l := range all {
		f1 := field(l, 1)
		if isoTSRe.MatchString(f1) && prefix19(f1) < bootISO {
			keep = append(keep, l)
		}
	}
	key := func(l string) string { k, _, _ := strings.Cut(l, " "); return k }
	sort.SliceStable(keep, func(i, j int) bool { return key(keep[i]) < key(keep[j]) })
	var out []string
	for i, l := range keep {
		if i == 0 || key(l) != key(keep[i-1]) {
			out = append(out, l)
		}
	}
	return out
}

func prefix19(s string) string {
	if len(s) > 19 {
		return s[:19]
	}
	return s
}

// last log line written before this boot, to tell how long the server was down
func lastLogBeforeBoot(bootISO string) string {
	ts := ""
	for _, l := range strings.Split(tmo(10, "journalctl", "-b", "-1", "-n", "1", "--no-pager", "-o", "short-iso").NoErr().Str(), "\n") {
		if f := field(l, 1); regexp.MustCompile(`^[0-9]{4}-`).MatchString(f) {
			ts = f
		}
	}
	for _, f := range []string{"/var/log/syslog", "/var/log/syslog.1"} {
		if ts != "" || !isFile(f) {
			continue
		}
		ls := lines(conf.Read(f))
		for i := len(ls) - 1; i >= 0; i-- {
			if f1 := field(ls[i], 1); regexp.MustCompile(`^[0-9]{4}-`).MatchString(f1) && prefix19(f1) < bootISO {
				ts = f1
				break
			}
		}
	}
	return ts
}

// dateEpoch is date -d "$s" +%s
func dateEpoch(s string) (int64, bool) {
	out, err := sh.C("date", "-d", s, "+%s").NoErr().Out()
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(out, 10, 64)
	return n, err == nil
}

// one or two lines on why the server went down, from the logs of the previous boot
func detectRebootCause() string {
	up := sh.C("uptime", "-s").NoErr().Str()
	bt, err := time.ParseInLocation("2006-01-02 15:04:05", up, time.Local)
	if err != nil {
		return ""
	}
	bootISO := bt.Format("2006-01-02T15:04:05")
	// only what happened after the previous boot started
	var buf []string
	for _, l := range rebootMarkerLines(bootISO) {
		if regexp.MustCompile(`Linux version [0-9]`).MatchString(l) {
			buf = nil
			continue
		}
		buf = append(buf, l)
	}
	last := lastLogBeforeBoot(bootISO)
	hit := func(re string) string {
		rx := regexp.MustCompile(re)
		h := ""
		for _, l := range buf {
			if rx.MatchString(l) {
				h = l
			}
		}
		return h
	}
	who := ""
	whoRe := regexp.MustCompile(`sudo: +[^ ]+ : .*COMMAND=[^ ]+`)
	for _, l := range buf {
		for _, m := range whoRe.FindAllString(l, -1) {
			who = m
		}
	}
	if who != "" {
		who = regexp.MustCompile(`sudo: +([^ ]+) : .*COMMAND=(.*)`).ReplaceAllString(who, "$1 ran $2")
	}
	after := func(s string) string {
		_, a, ok := strings.Cut(s, " ")
		if !ok {
			return s
		}
		return a
	}
	withWho := func(pre string) string {
		if who != "" {
			return pre + ", " + who
		}
		return pre
	}
	var cause string
	if hit("Power key pressed|Power button") != "" {
		cause = "Cause: the power button was pressed (ACPI power key) from outside the server, usually the hosting provider's panel or the host node in the data center, nothing on this server shut it down."
	} else if hit(`unattended-upgrade.*[Rr]eboot`) != "" {
		cause = "Cause: automatic reboot by unattended-upgrades after installing updates."
	} else if h := hit("Kernel panic"); h != "" {
		cause = "Cause: kernel panic, the server crashed: " + after(h)
	} else if h := hit("Out of memory: Killed|LOCKUP"); h != "" && hit("System is (rebooting|powering down)|Stopped target") == "" {
		cause = "Cause: the server froze (out of memory or CPU lockup) and was reset, last kernel message: " + after(h)
	} else if hit(`System is rebooting|will reboot|COMMAND=.*(reboot|init 6)`) != "" {
		cause = "Cause: restarted from inside the server (reboot command or " + withWho("OpenAdmin") + ")."
	} else if hit(`System is powering down|will power off|COMMAND=.*(shutdown|poweroff|halt|init 0)`) != "" {
		cause = "Cause: powered off from inside the server (" + withWho("shutdown/poweroff command") + ")."
	} else if hit("Stopped target multi-user") != "" {
		cause = "Cause: the server was shut down cleanly, the logs don't say who started it."
	} else if last != "" {
		cause = "Cause: no clean shutdown in the logs, the server lost power or was hard reset (crash, host node failure or a forced reset from the provider's panel)."
	} else {
		cause = "Cause: unknown, the logs from before the reboot are not available."
	}
	if last != "" {
		le, ok := dateEpoch(last)
		downSince := ""
		if ok {
			downSince = time.Unix(le, 0).Format("2006-01-02 15:04:05")
		}
		down := bt.Unix() - le
		if down > 0 && down < 31536000 {
			cause += fmt.Sprintf("\nDown from %s until %s (%dm %ds).", downSince, bt.Format("2006-01-02 15:04:05"), down/60, down%60)
		}
	}
	return cause
}

var dnsPortRe = regexp.MustCompile(`^( *- *")([0-9.]+:)?(53:53/(tcp|udp)")`)

func (r *run) performStartupActions() {
	cause := detectRebootCause()
	os.MkdirAll("/tmp/redis", 0o777)
	os.Chmod("/tmp/redis", 0o777) // for redis
	os.MkdirAll("/tmp/ssh_cm", 0o700)
	os.Chmod("/tmp/ssh_cm", 0o700) // for ssh connections to slave servers
	ip := field(sh.C("hostname", "-I").Str(), 1)
	if b, err := os.ReadFile("/root/docker-compose.yml"); err == nil {
		ls := strings.Split(string(b), "\n")
		for i, l := range ls {
			if m := dnsPortRe.FindStringSubmatchIndex(l); m != nil {
				ls[i] = l[m[2]:m[3]] + ip + ":" + l[m[6]:m[7]] + l[m[1]:]
			}
		}
		conf.WriteFileKeep("/root/docker-compose.yml", strings.Join(ls, "\n"), false)
	} else {
		fmt.Fprintln(os.Stderr, "sed: can't read /root/docker-compose.yml: No such file or directory")
	}
	r.updateFTPPasvAddress()

	touch(lockFileForUserContainers) // so a cron run during the staged start doesn't sweep all users at once
	st := r.restartDeadUserContainers("staged")
	touch(lockFileForUserContainers) // starts the hourly window from reboot, so the next cron tick doesn't re-sweep immediately

	summary := ""
	if st.rootCount > 0 || st.userTotal > 0 {
		summary = restartSummary("Started", st)
	}
	if optReboot == "no" {
		r.warn++
		r.println("[!] Reboot notifications are disabled.")
		return
	}
	msg := "System was rebooted. " + sh.C("uptime").Str() + "\n"
	if cause != "" {
		msg += cause + "\n"
	}
	msg += summary
	r.writeNotification("warning", "system", "SYSTEM REBOOT!", msg, "")
}

func (r *run) checkUserContainers() {
	hr(r.out)
	r.println("Checking container status for openpanel users...")
	if age, ok := ageSecs(lockFileForUserContainers); ok && age < 3600 {
		r.warn++
		r.printf("%s User container check skipped (last run %dm ago).\n", cWarn, age/60)
		return
	}
	touch(lockFileForUserContainers)

	st := r.restartDeadUserContainers("")
	if st.rootCount == 0 && st.userTotal == 0 {
		r.pass++
		r.printf("%s No dead user containers found (checked in %ds).\n", cOK, st.elapsed)
		return
	}
	r.warn++
	msg := restartSummary("Started/restarted", st)
	r.printf("%s %s\n", cWarn, msg)
	writeInfoNotification("service", "Dead/required user containers started", msg)
	r.notifyUsersOfFailedServices(st.events)
}

// emails each user (if they have notify_service_failed on) which of their services were down and restarted, at most once a day per user
func (r *run) notifyUsersOfFailedServices(events string) {
	if events == "" {
		return
	}
	today := time.Now().Format("2006-01-02")
	evs := lines(events)
	seen := map[string]bool{}
	var contexts []string
	for _, e := range evs {
		c, _, _ := strings.Cut(e, "\t")
		if !seen[c] {
			seen[c] = true
			contexts = append(contexts, c)
		}
	}
	sort.Strings(contexts)
	safe := regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	for _, ctx := range contexts {
		if !safe.MatchString(ctx) {
			continue
		}
		username := db.Cmd("SELECT username FROM users WHERE server = '"+ctx+"' LIMIT 1", "-N", "-B").NoErr().Str()
		if username == "" || strings.HasPrefix(username, "SUSPENDED_") {
			continue
		}
		state := "/etc/openpanel/openpanel/core/users/" + username + "/.service_failed_notified"
		if readTrim(state) == today {
			continue
		}
		services := ""
		oomNote, failedNote := false, false
		for _, e := range evs {
			f := strings.Split(e, "\t")
			for len(f) < 4 {
				f = append(f, "")
			}
			if f[0] != ctx {
				continue
			}
			services += "- " + f[1] + ": "
			if f[2] == "true" {
				services += "ran out of memory, "
				oomNote = true
			}
			if f[3] == "restarted" {
				services += "restarted\n"
			} else {
				services += "could not be started\n"
				failedNote = true
			}
		}
		tips := "If a service keeps stopping, check its logs on the Containers page in OpenPanel."
		if oomNote {
			tips += " A service that ran out of memory needs a higher memory limit on the Containers page, or less memory use, for example fewer PHP workers or a smaller database cache."
		}
		if failedNote {
			tips += " Services that could not be started are still down, contact your hosting provider if they don't come back."
		}
		body := "Some services on account " + username + " had stopped, the server's automatic checks tried to start them again:\n\n" + services + "\nChecked: " + time.Now().Format("2006-01-02 15:04:05 MST")
		if mail.SendUser(username, "Services restarted on account "+username, body, "notify_service_failed", "", "", "", tips) {
			if os.WriteFile(state, []byte(today+"\n"), 0o644) == nil {
				r.printf("%s: notified about restarted services.\n", username)
			}
		}
	}
}

func (r *run) emailDailyReport() {
	if emailAlert == "no" {
		r.println("Email alerts disabled.")
		return
	}
	r.emailNotification("Daily Usage Report", "Daily Usage Report", "")
}
