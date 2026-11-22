// ##############################################################################
// Script Name: internal/commands/sentinel/sentinel_checks.go
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
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

var isoLayouts = []string{"2006-01-02T15:04:05-0700", "2006-01-02T15:04:05-07:00", "2006-01-02T15:04:05.999999999-07:00", "2006-01-02T15:04:05.999999999-0700", "2006-01-02T15:04:05Z07:00"}

// isoMinute is date -d "$ts" '+%Y-%m-%d %H:%M'
func isoMinute(ts string) string {
	for _, l := range isoLayouts {
		if t, err := time.Parse(l, ts); err == nil {
			return t.Local().Format("2006-01-02 15:04")
		}
	}
	if n, ok := dateEpoch(ts); ok {
		return time.Unix(n, 0).Format("2006-01-02 15:04")
	}
	return ""
}

var (
	memLimitRe = regexp.MustCompile(`memory: usage [0-9]+kB, limit ([0-9]+)kB`)
	pidRe      = regexp.MustCompile(`.*,pid=([0-9]+)`)
	ownerRe    = regexp.MustCompile(`.*task_memcg=[^,]*user-([0-9]+)\.slice`)
	ctrRe      = regexp.MustCompile(`.*task_memcg=[^,]*libpod-([0-9a-f]{12,})\.scope`)
	killedRe   = regexp.MustCompile(`Killed process ([0-9]+) \((.*)\).*anon-rss:([0-9]+)kB.*UID:([0-9]+)`)
)

func sub1(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func lookupUID(uid string) string {
	if u, err := user.LookupId(uid); err == nil {
		return u.Username
	}
	if u, err := user.Lookup(uid); err == nil {
		return u.Username
	}
	return ""
}

func (r *run) checkOOMLogs() {
	if optLimit == "no" {
		r.warn++
		r.println("[!] OOM errors check disabled.")
		return
	}
	now := time.Now().Unix()
	sinceS := readTrim(lockFileForOOMCheck)
	since, err := strconv.ParseInt(sinceS, 10, 64)
	// first run or after reboot (watermark is in /tmp) covers the last hour, so old kills aren't reported again
	if err != nil || !regexp.MustCompile(`^[0-9]+$`).MatchString(sinceS) {
		since = now - 3600
	}
	if now-since < 3600 {
		return
	}
	// kernel ring buffer through journald, syslog files are missing on journald-only systems and use a different date format
	klog, err := tmo(30, "journalctl", "-k", "--since", fmt.Sprintf("@%d", since), "--until", fmt.Sprintf("@%d", now), "-o", "short-iso", "--no-pager").NoErr().Out()
	if err != nil {
		r.warn++
		r.printf("%s Could not read kernel log with journalctl, skipping OOM check.\n", cWarn)
		return
	}
	os.WriteFile(lockFileForOOMCheck, []byte(fmt.Sprintf("%d\n", now)), 0o644)

	sinceTime := time.Unix(since, 0).Format("2006-01-02 15:04")
	systemCount, userCount := 0, 0
	var systemLines, summary, keys []string
	type ue struct{ user, line string }
	var userLines []ue
	owner, ctr, ctrName, memLimit, seen, panelUser := map[string]string{}, map[string]string{}, map[string]string{}, map[string]int{}, map[string]bool{}, map[string]bool{}

	// panel users by linux name, older accounts can have a UID below 1002
	for _, c := range strings.Split(db.Cmd("SELECT server FROM users", "-N", "-B").Timeout(10*time.Second).NoErr().Str(), "\n") {
		if c = strings.TrimSpace(c); c != "" {
			panelUser[c] = true
		}
	}

	klines := lines(klog)
	// rootless podman kills report a subuid as UID, the real owner is in the cgroup path of the oom-kill line
	mem := ""
	for _, line := range klines {
		if !strings.Contains(line, "memory: usage") && !strings.Contains(line, "oom-kill:") {
			continue
		}
		line = strings.Trim(line, " \t")
		// memcg kills print the cgroup limit right before their oom-kill line
		if m := memLimitRe.FindStringSubmatch(line); m != nil {
			mem = m[1]
			continue
		}
		pid := sub1(pidRe, line)
		if pid != "" && mem != "" {
			if v, _ := strconv.ParseInt(mem, 10, 64); v < 4294967296 {
				memLimit[pid] = int(v / 1024)
			}
		}
		mem = ""
		if o := sub1(ownerRe, line); pid != "" && o != "" {
			owner[pid] = o
		}
		if id := sub1(ctrRe, line); pid != "" && id != "" {
			ctr[pid] = id
		}
	}

	killRe := regexp.MustCompile(`(Memory cgroup out of memory|Out of memory): Killed process`)
	for _, line := range klines {
		if !killRe.MatchString(line) {
			continue
		}
		line = strings.Trim(line, " \t")
		m := killedRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		pid, name, uid := m[1], m[2], m[4]
		rss := atoi(m[3]) / 1024
		own := owner[pid]
		if own == "" {
			own = uid
		}
		ts, _, _ := strings.Cut(line, " ")
		when := isoMinute(ts)
		why := "server out of memory"
		if strings.Contains(line, "Memory cgroup out of memory") {
			why = "container memory limit"
			if l, ok := memLimit[pid]; ok {
				if l >= 1024 && l%1024 == 0 {
					why += fmt.Sprintf(" of %d GB", l/1024)
				} else {
					why += fmt.Sprintf(" of %d MB", l)
				}
			}
		}

		// container name from the owner's podman, short id if the container is gone by now
		c := ""
		id := ctr[pid]
		if id != "" {
			if ctrName[id] == "" {
				sock := "/run/user/" + owner[pid] + "/podman/podman.sock"
				if owner[pid] == "" {
					ctrName[id] = tmo(10, "podman", "inspect", "--format", "{{.Name}}", id).NoErr().Str()
				} else if st, err := os.Stat(sock); err == nil && st.Mode()&os.ModeSocket != 0 {
					ctrName[id] = atSock(sock, "inspect", "--format", "{{.Name}}", id).Timeout(10 * time.Second).NoErr().Str()
				}
				if ctrName[id] == "" {
					ctrName[id] = id[:12]
				}
			}
			c = ", container " + ctrName[id]
		}

		u := ""
		ctx := lookupUID(own)
		if len(panelUser) > 0 {
			if ctx != "" && panelUser[ctx] {
				u = ctx
			}
		} else if atoi(own) >= 1002 {
			u = ctx
		}
		key := ""
		if u != "" {
			userCount++
			userLines = append(userLines, ue{u, line})
			summary = append(summary, fmt.Sprintf("- user %s: %s (pid %s%s, %d MB) at %s, %s", u, name, pid, c, rss, when, why))
			key = u
		} else {
			systemCount++
			systemLines = append(systemLines, line)
			// host accounts like the admin's own rootless podman, named instead of showing the bare UID
			who := "UID " + uid
			if owner[pid] != "" {
				if hu := lookupUID(owner[pid]); hu != "" {
					who = "owner " + hu
				}
			}
			summary = append(summary, fmt.Sprintf("- system: %s (pid %s%s, %s, %d MB) at %s, %s", name, pid, c, who, rss, when, why))
			key = name
			if id != "" {
				key = ctrName[id]
			}
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}

	if systemCount == 0 && userCount == 0 {
		r.pass++
		r.printf("%s No OOM errors detected.\n", cOK)
		return
	}
	r.fail++
	r.status = 2

	// no date or counts in the title, so the same kill loop bumps one unread alert instead of emailing every hour
	k := keys
	if len(k) > 3 {
		k = k[:3]
	}
	titleWho := strings.Join(k, ", ")
	if len(keys) > 3 {
		titleWho += fmt.Sprintf(" and %d more", len(keys)-3)
	}
	title := "OOM Alert - " + titleWho
	message := ""
	if systemCount > 0 {
		message += fmt.Sprintf("%d system service(s) killed by OOM since %s.\n", systemCount, sinceTime)
		r.printf("%s %d system service(s) killed by OOM since %s\n", cFail, systemCount, sinceTime)
	}
	if userCount > 0 {
		message += fmt.Sprintf("%d user process(es) killed by OOM since %s.\n", userCount, sinceTime)
		r.printf("%s %d user process(es) killed by OOM since %s\n", cFail, userCount, sinceTime)
	}
	// cap the list so a kill loop doesn't flood the email
	top := summary
	if len(top) > 10 {
		top = top[:10]
	}
	message += strings.Join(top, "\n")
	if len(summary) > 10 {
		message += fmt.Sprintf("\n- ...and %d more", len(summary)-10)
	}
	for _, s := range top {
		r.printf("    %s\n", s)
	}
	message += "\nCheck with: journalctl -k --since '" + sinceTime + "' | grep 'Killed process'"

	sys := []any{}
	for _, l := range systemLines {
		if l != "" {
			sys = append(sys, l)
		}
	}
	sort.SliceStable(userLines, func(i, j int) bool { return userLines[i].user < userLines[j].user })
	users := []any{}
	for i := 0; i < len(userLines); {
		j := i
		var entries []any
		for j < len(userLines) && userLines[j].user == userLines[i].user {
			entries = append(entries, userLines[j].line)
			j++
		}
		users = append(users, ojson.New().Set("username", userLines[i].user).Set("entries", entries))
		i = j
	}
	details := ojson.Compact(ojson.New().Set("kind", "oom").Set("system", sys).Set("users", users))
	r.writeNotification("warning", "resources", title, strings.TrimSuffix(message, "\n"), details)
}

// warns once per due date when the Enterprise license ends in 3 days or less, the api is asked at most once a day
func (r *run) checkLicenseExpiry() {
	checked, notified := "/root/sentinel_license_checked", "/root/sentinel_license_notified"
	if !regexp.MustCompile(`(?m)^key=.+`).MatchString(conf.Read(confFile)) {
		return
	}
	if age, ok := ageSecs(checked); ok && age < 1440*60 {
		return
	}
	touch(checked)

	due := ""
	if o, err := ojson.ParseObject(strings.TrimSpace(sh.Opencli("license", "info", "--json").NoErr().Str())); err == nil {
		if v, ok := o.Get("Next Due Date"); ok && v != nil && v != false {
			due = ojson.Text(v)
		}
	}
	if due == "" || due == "0000-00-00" {
		return
	}
	dueTS, ok := dateEpoch(due)
	if !ok {
		return
	}
	y, mo, d := time.Now().Date()
	today := time.Date(y, mo, d, 0, 0, 0, 0, time.Local).Unix()
	days := (dueTS - today) / 86400
	if days > 3 {
		r.pass++
		r.printf("%s License is paid until %s.\n", cOK, due)
		return
	}
	r.warn++
	r.printf("%s License expires on %s.\n", cWarn, due)
	if readTrim(notified) == due {
		return
	}
	os.WriteFile(notified, []byte(due+"\n"), 0o644)
	when := fmt.Sprintf("in %d day(s)", days)
	if days <= 0 {
		when = "today"
	}
	r.writeNotification("warning", "system", "License expires "+when, "The OpenPanel Enterprise license on "+hostname+" expires on "+due+". Renew it at https://my.openpanel.com/clientarea.php?action=services to keep Enterprise features working.", "")
}

var dottedRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$`)

func (r *run) checkNewLogins() {
	if optLogin == "no" {
		r.warn++
		r.printf("%s Login check disabled.\n", cWarn)
		return
	}
	loginLog := "/var/log/openpanel/admin/login.log"
	watermark := "/tmp/sentinel.login_watermark"
	if !isFile(loginLog) {
		os.WriteFile(loginLog, nil, 0o644)
	}
	last := 0
	if isFile(watermark) {
		last = atoi(readTrim(watermark))
	}
	data := conf.Read(loginLog)
	current := strings.Count(data, "\n")
	os.WriteFile(watermark, []byte(strconv.Itoa(current)+"\n"), 0o644)

	if current <= last {
		r.pass++
		r.printf("%s No new logins to OpenAdmin.\n", cOK)
		return
	}
	all := strings.Split(data, "\n")
	if strings.HasSuffix(data, "\n") {
		all = all[:len(all)-1]
	}
	if last < 0 {
		last = 0
	}
	newLines := strings.TrimRight(strings.Join(all[min(last, len(all)):], "\n"), "\n")
	if newLines == "" {
		r.pass++
		r.printf("%s No new logins to OpenAdmin.\n", cOK)
		return
	}
	seen := map[string]bool{}
	if last > 0 {
		for _, l := range all[:min(last, len(all))] {
			f := fields(l)
			a, b := l, l
			if len(f) >= 2 {
				a, b = f[len(f)-2], f[len(f)-1]
			} else if len(f) == 1 {
				b = f[0]
			}
			w := fields(a + " " + b)
			u, ip := "", ""
			if len(w) > 0 {
				u = w[0]
			}
			if len(w) > 1 {
				ip = strings.Join(w[1:], " ")
			}
			seen[u+" "+ip] = true
		}
	}
	foundNew := false
	for _, line := range strings.Split(newLines, "\n") {
		f := fields(line)
		username, ip := "", ""
		if len(f) > 2 {
			username = f[2]
		}
		if len(f) > 3 {
			ip = f[3]
		}
		if !dottedRe.MatchString(ip) || ip == "127.0.0.1" {
			continue
		}
		pair := username + " " + ip
		if !seen[pair] {
			if isIPWhitelisted(ip) {
				r.printf("%s %s from new but whitelisted IP: %s\n", cOK, username, ip)
			} else {
				r.fail++
				r.status = 2
				foundNew = true
				r.printf("%s %s logged in from new IP: %s\n", cFail, username, ip)
				r.writeNotification("warning", "security", "Admin "+username+" accessed from new IP: "+ip, "Admin account "+username+" was accessed from new IP: "+ip+". If this wasn't you, change the password for "+username+" right away.", "")
			}
			// remember it so repeat logins for this IP in the same run aren't flagged again
			seen[pair] = true
		} else {
			r.printf("%s %s from known IP: %s\n", cOK, username, ip)
		}
	}
	if !foundNew {
		r.pass++
	}
}

func (r *run) checkSSHLogins() {
	if optSSH == "no" {
		return
	}
	if isUnreadMessagePresent("Suspicious SSH login detected") {
		r.warn++
		r.printf("%s Unread SSH notification exists. Skipping.\n", cWarn)
		return
	}
	var ips []string
	for _, l := range strings.Split(sh.C("who").Str(), "\n") {
		if !strings.Contains(l, "pts") {
			continue
		}
		f5 := strings.NewReplacer("(", "", ")", "", ":", "").Replace(field(l, 5))
		ips = append(ips, f5)
	}
	sshIPs := strings.TrimRight(strings.Join(ips, "\n"), "\n")
	if sshIPs == "" {
		r.pass++
		r.printf("%s No active SSH sessions.\n", cOK)
		return
	}
	var loginIPs []string
	for _, l := range lines(conf.Read("/var/log/openpanel/admin/login.log")) {
		f := fields(l)
		v := l
		if len(f) > 0 {
			v = f[len(f)-1]
		}
		loginIPs = append(loginIPs, v)
	}
	login := strings.TrimRight(strings.Join(loginIPs, "\n"), "\n")
	if login == "" {
		r.warn++
		r.printf("%s SSH user detected; postponing check until OpenAdmin is ready.\n", cWarn)
		return
	}
	var suspicious, safe []string
	for _, ip := range fields(sshIPs) {
		if !ipv4Re.MatchString(ip) {
			continue
		}
		if !isIPWhitelisted(ip) && !strings.Contains(login, ip) {
			suspicious = append(suspicious, ip)
		} else {
			safe = append(safe, ip)
		}
	}
	if len(suspicious) > 0 {
		r.fail++
		r.status = 2
		r.printf("%s Suspicious SSH IPs: %s\n", cFail, strings.Join(suspicious, " "))
		r.writeNotification("critical", "security", "Suspicious SSH login detected", "Active SSH session(s) from IP(s) that never logged into OpenAdmin and are not whitelisted: "+strings.Join(suspicious, " ")+". Check with: who. If these are yours, add them to the SSH whitelist in OpenAdmin > Settings > Notifications.", "")
	} else {
		r.pass++
		r.printf("%s %d SSH session(s) from known IPs: %s\n", cOK, len(safe), strings.Join(safe, " "))
	}
}

func diskDetails(pct int) string {
	parts := sh.C("sort", "-r", "-k", "5", "-i").Stdin(sh.C("df", "-h").Str() + "\n").Str()
	return ojson.Compact(ojson.New().Set("kind", "disk").Set("percent", pct).Set("partitions", parts))
}

// the shared store has no containers of its own, so "unused" means no containers.json anywhere points at the image
func pruneSharedImages() (removed, failed []string, errMsg string) {
	store := podman.SharedImageStore()
	if !isDir(store + "/overlay-images") {
		return
	}
	used := map[string]bool{}
	files := []string{"/var/lib/containers/storage/overlay-containers/containers.json"}
	g, _ := filepath.Glob("/home/*/docker-data/overlay-containers/containers.json")
	sort.Strings(g)
	files = append(files, g...)
	for _, f := range files {
		if !isFile(f) {
			continue
		}
		// if one can't be read we don't know what's in use, so don't delete anything
		var arr []map[string]any
		if err := json.Unmarshal([]byte(conf.Read(f)), &arr); err != nil {
			return nil, nil, "could not read " + f
		}
		for _, c := range arr {
			if s, ok := c["image"].(string); ok && s != "" {
				for _, id := range fields(s) {
					used[id] = true
				}
			}
		}
	}
	out := tmo(30, "podman", "--root", store, "images", "--no-trunc", "--format", "{{.ID}} {{.Repository}}:{{.Tag}}").NoErr().Str()
	ls := lines(out)
	key := func(l string) string { k, _, _ := strings.Cut(l, " "); return k }
	sort.SliceStable(ls, func(i, j int) bool { return key(ls[i]) < key(ls[j]) })
	for i, l := range ls {
		if i > 0 && key(l) == key(ls[i-1]) {
			continue
		}
		f := fields(l)
		if len(f) == 0 {
			continue
		}
		id := strings.TrimPrefix(f[0], "sha256:")
		names := strings.Join(f[1:], " ")
		if used[id] {
			continue
		}
		// just pulled by docker-update or autostart and its containers aren't recreated yet
		if age, ok := ageSecs(store + "/overlay-images/" + id); ok && age < 1440*60 {
			continue
		}
		if names == "<none>:<none>" {
			names = id[:min(12, len(id))]
		}
		// skopeo with the shared store's own runroot: podman rmi there used root's runroot, which unmounts every running root container's layer so its next restart fails
		if tmo(60, "skopeo", "delete", "containers-storage:[overlay@"+store+"+/run/containers/shared-storage]"+id).Quiet().Run() == nil {
			sh.C("chmod", "-R", "o+rX", store).Quiet().Run()
			removed = append(removed, names)
		} else {
			failed = append(failed, names)
		}
	}
	return
}

func trashEntries(trash string) bool {
	ents, _ := os.ReadDir(trash)
	for _, e := range ents {
		if e.Name() != ".trash_restore" {
			return true
		}
	}
	return false
}

// same as Empty Trash in the File Manager, keeps .trash_restore but empties it
func emptyUsersTrash() (emptied, failed []string) {
	homes, _ := filepath.Glob("/home/*")
	sort.Strings(homes)
	for _, home := range homes {
		trash := home + "/.local/share/Trash"
		if !isDir(trash) {
			continue
		}
		// skip if any part of the path is a symlink, the user controls it and we run rm as root
		if rp, err := filepath.EvalSymlinks(trash); err != nil || rp != trash {
			continue
		}
		if !trashEntries(trash) {
			continue
		}
		u := filepath.Base(home)
		kb, _, _ := strings.Cut(tmo(30, "du", "-sk", trash).NoErr().Str(), "\t")
		ents, _ := os.ReadDir(trash)
		for _, e := range ents {
			if e.Name() != ".trash_restore" {
				os.RemoveAll(filepath.Join(trash, e.Name()))
			}
		}
		if !trashEntries(trash) {
			if isFile(trash + "/.trash_restore") {
				os.Truncate(trash+"/.trash_restore", 0)
			}
			if kb == "" {
				kb = "0"
			}
			emptied = append(emptied, u+" ("+humanKB(int64(atoi(kb)))+")")
		} else {
			failed = append(failed, u)
		}
	}
	return
}

func diskUsedKB() int64 {
	ls := lines(sh.C("df", "/").Str())
	if len(ls) < 2 {
		return 0
	}
	n, _ := strconv.ParseInt(field(ls[1], 3), 10, 64)
	return n
}

func diskPct() int {
	ls := lines(sh.C("df", "--output=pcent", "/").Str())
	if len(ls) < 2 {
		return 0
	}
	return atoi(strings.ReplaceAll(field(ls[1], 1), "%", ""))
}

func humanKB(kb int64) string {
	return sh.C("numfmt", "--to=iec", strconv.FormatInt(kb*1024, 10)).Env("LC_ALL=C").NoErr().Str() + "B"
}

func cleanupJournal() {
	if !sh.Has("journalctl") {
		return
	}
	tmo(60, "journalctl", "--vacuum-size=500M").Quiet().Run()
}

func cleanupPackageCache() {
	switch {
	case sh.Has("apt-get"):
		tmo(60, "apt-get", "clean").Quiet().Run()
	case sh.Has("dnf"):
		tmo(60, "dnf", "clean", "all").Quiet().Run()
	case sh.Has("yum"):
		tmo(60, "yum", "clean", "all").Quiet().Run()
	}
}

func cleanupOldLogs() {
	tmo(60, "find", "/var/log/openpanel/admin/crashlog", "/var/log/openpanel/admin/reports", "-type", "f", "-mtime", "+7", "-delete").Quiet().Run()
	tmo(60, "find", "/var/log/openpanel/updates", "-type", "f", "-mtime", "+30", "-delete").Quiet().Run()
	// rotated archives only, wtmp/btmp are kept for login history
	tmo(120, "find", "/var/log", "-xdev", "-type", "f", "(", "-name", "*.gz", "-o", "-name", "*.[0-9]", "-o", "-name", "*.old", ")", "!", "-name", "wtmp*", "!", "-name", "btmp*", "-mtime", "+7", "-delete").Quiet().Run()
	tmo(60, "find", "/tmp", "-maxdepth", "1", "(", "-name", "trash_purge_*", "-o", "-name", "sentinel.container_restart_results.*", ")", "-mmin", "+1440", "-exec", "rm", "-rf", "--", "{}", "+").Quiet().Run()
}

// keeps the last 100MB in place so caddy and others keep writing to the same file
func cleanupLargeLogs() []string {
	var big []string
	// %b is blocks really used, so sparse files like lastlog don't count
	out := tmo(60, "find", "/var/log", "-xdev", "-path", "/var/log/journal", "-prune", "-o", "-type", "f", "!", "-name", "lastlog", "!", "-name", "wtmp*", "!", "-name", "btmp*", "-printf", "%b %p\n").NoErr().Str()
	for _, l := range lines(out) {
		n, p, _ := strings.Cut(l, " ")
		if v, _ := strconv.ParseFloat(n, 64); v <= 2097152 {
			continue
		}
		if shrinkLog(p) {
			big = append(big, p)
		}
	}
	return big
}

func shrinkLog(p string) bool {
	const keep = 100 * 1024 * 1024
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	st, _ := f.Stat()
	off := st.Size() - keep
	if off < 0 {
		off = 0
	}
	tmp, err := os.CreateTemp("/tmp", "sentinel.log_tail.")
	if err != nil {
		f.Close()
		return false
	}
	defer os.Remove(tmp.Name())
	f.Seek(off, 0)
	_, err = tmp.ReadFrom(f)
	f.Close()
	if err != nil {
		tmp.Close()
		return false
	}
	tmp.Seek(0, 0)
	w, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		tmp.Close()
		return false
	}
	_, err = w.ReadFrom(tmp)
	w.Close()
	tmp.Close()
	return err == nil
}

type cleanup struct {
	steps                     []string
	bigLogs                   []string
	imgRemoved, imgFailed     []string
	imgErr                    string
	trashEmptied, trashFailed []string
}

func (c *cleanup) podman() {
	tmo(30, "podman", "system", "prune", "-f", "--filter", "until=24h").Quiet().Run()
	homes, _ := filepath.Glob("/home/*")
	sort.Strings(homes)
	for _, ctx := range homes {
		if !isDir(ctx) {
			continue
		}
		uid, err := podman.UID(filepath.Base(ctx))
		if err != nil {
			continue
		}
		tmo(15, "podman", "--remote", "system", "prune", "-f", "--filter", "until=24h").Env(fmt.Sprintf("CONTAINER_HOST=unix:///hostfs/run/user/%d/podman/podman.sock", uid)).Quiet().Run()
	}
	c.imgRemoved, c.imgFailed, c.imgErr = pruneSharedImages()
}

// runs one cleanup step and notes how much it freed, false once the disk is under the threshold
func (c *cleanup) step(label string, fn func()) bool {
	before := diskUsedKB()
	fn()
	freed := before - diskUsedKB()
	if freed < 0 {
		freed = 0
	}
	c.steps = append(c.steps, label+": "+humanKB(freed))
	return diskPct() > diskThreshold
}

func (r *run) checkDiskUsage() {
	title := "Running out of Disk Space!"
	pct := diskPct()
	if pct <= diskThreshold {
		r.pass++
		r.printf("%s Disk %d%% < threshold %d%%\n", cOK, pct, diskThreshold)
		r.resolveNotification(title, false)
		return
	}
	if isUnreadMessagePresent(title) {
		r.warn++
		r.printf("%s Unread DU notification. Skipping.\n", cWarn)
		return
	}
	// Try cleanup if not done in last 24h
	if age, ok := ageSecs(lockFileForDockerPrune); ok && age < 86400 {
		r.writeNotification("warning", "resources", title, fmt.Sprintf("Disk usage is %d%% (threshold %d%%).", pct, diskThreshold), diskDetails(pct))
		return
	}
	touch(lockFileForDockerPrune)

	kbBefore := diskUsedKB()
	c := &cleanup{}
	// safest first, users' Trash last since it's the only step that loses user data
	last := c.step("systemd journal", cleanupJournal) &&
		c.step("package cache", cleanupPackageCache) &&
		c.step("old and rotated logs", cleanupOldLogs) &&
		c.step("logs over 1GB", func() { c.bigLogs = cleanupLargeLogs() }) &&
		c.step("unused podman data and images", c.podman) &&
		c.step("users' Trash", func() { c.trashEmptied, c.trashFailed = emptyUsersTrash() })

	msg := " Cleaned up " + strings.Join(c.steps, ", ") + "."
	if !last {
		msg += " Stopped once the disk was under the threshold."
	}
	if len(c.bigLogs) > 0 {
		msg += " Shrank to the last 100MB: " + strings.Join(c.bigLogs, " ") + "."
	}
	if c.imgErr != "" {
		msg += " Skipped removing unused images from the shared store: " + c.imgErr + "."
	} else if len(c.imgRemoved) > 0 {
		msg += fmt.Sprintf(" Removed %d unused image(s) from the shared store: %s.", len(c.imgRemoved), strings.Join(c.imgRemoved, " "))
	}
	if len(c.imgFailed) > 0 {
		msg += " Failed to remove: " + strings.Join(c.imgFailed, " ") + "."
	}
	if len(c.trashEmptied) > 0 {
		msg += fmt.Sprintf(" Emptied the Trash of %d user(s): %s.", len(c.trashEmptied), strings.Join(c.trashEmptied, ", "))
	}
	if len(c.trashFailed) > 0 {
		msg += " Failed to empty the Trash of: " + strings.Join(c.trashFailed, " ") + "."
	}
	freedKB := kbBefore - diskUsedKB()
	if freedKB < 0 {
		freedKB = 0
	}
	freed := humanKB(freedKB)
	after := diskPct()
	if after <= diskThreshold {
		r.writeNotification("warning", "resources", title, fmt.Sprintf("Disk was %d%%, Sentinel freed %s and it is now at %d%% (threshold %d%%).%s", pct, freed, after, diskThreshold, msg), diskDetails(after))
		r.warn++
		r.printf("%s Disk was %d%%, sentinel freed %s, disk is now at %d%%.%s\n", cWarn, pct, freed, after, msg)
	} else {
		r.fail++
		r.status = 2
		r.printf("%s Disk was %d%% > threshold %d%%, cleanup freed %s and it is still at %d%%.%s\n", cFail, pct, diskThreshold, freed, after, msg)
		r.writeNotification("warning", "resources", title, fmt.Sprintf("Disk usage is %d%% (threshold %d%%). Sentinel freed %s but it is still at %d%%.%s Check with: du -xh / --max-depth=2 | sort -rh | head", pct, diskThreshold, freed, after, msg), diskDetails(after))
	}
}
