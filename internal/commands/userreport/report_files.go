// ##############################################################################
// Script Name: internal/commands/userreport/report_files.go
// Description: Prints a health report for a user: what is working and what needs attention.
// Usage: opencli user-report <USERNAME|--all> [--section <name,...>] [--json]
// Author: Stefan Pejcic
// Created: 02.10.2026
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

package userreport

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opencli/internal/conf"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

// duRows prints "KB path" lines as a readable size and the path
func (s *sec) duRows(lines []string) {
	for _, l := range lines {
		f := bread(l, " \t\n", 2)
		s.p("  %s %s\n", padR(humanKB(f[0]), 12), f[1])
	}
}

// findPerm walks like find with -path <prune> -prune, start dir included, stops at the deadline like timeout did
func findPerm(root, prune string, deadline time.Time, match func(os.FileMode, uint32) bool) []string {
	var out []string
	var walk func(p string, isRoot bool)
	walk = func(p string, isRoot bool) {
		if time.Now().After(deadline) {
			return
		}
		if p == prune {
			return
		}
		var st syscall.Stat_t
		if syscall.Lstat(p, &st) != nil {
			return
		}
		if match(0, st.Mode) {
			out = append(out, p)
		}
		if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
			return
		}
		f, err := os.Open(p)
		if err != nil {
			return
		}
		names, _ := f.Readdirnames(-1)
		f.Close()
		for _, n := range names {
			walk(strings.TrimSuffix(p, "/")+"/"+n, false)
		}
	}
	walk(root, true)
	return out
}

// cronD is grep -h <word> /etc/cron.d/* | grep -v <comment> | awk '{ print $1..$5 }' | head -n1
func cronD(word string, comment *regexp.Regexp) string {
	files, _ := filepath.Glob("/etc/cron.d/*")
	for _, f := range files {
		for _, l := range awkLines(readFile(f)) {
			if !strings.Contains(l, word) || comment.MatchString(l) {
				continue
			}
			fl := strings.Fields(l)
			for len(fl) < 5 {
				fl = append(fl, "")
			}
			return strings.Join(fl[:5], " ")
		}
	}
	return ""
}

var (
	cronCommentWS = regexp.MustCompile(`^\s*#`)
	cronComment   = regexp.MustCompile(`^#`)
)

// mtimeFmt is date -r file '+%Y-%m-%d %H:%M'
func mtimeFmt(p string) string {
	st, err := os.Stat(p)
	if err != nil {
		return ""
	}
	return st.ModTime().Format("2006-01-02 15:04")
}

// localEpoch is date -d "<time>" +%s for plain local timestamps
func localEpoch(t string) (int64, bool) {
	for _, l := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if x, err := time.ParseInLocation(l, t, time.Local); err == nil {
			return x.Unix(), true
		}
	}
	out, err := sh.C("date", "-d", t, "+%s").NoErr().Out()
	if err != nil {
		return 0, false
	}
	return bashInt(out), true
}

// uniqC is sort | uniq -c | sort -rn, as "count text" pairs in that order
func uniqC(items []string) [][2]string {
	a := append([]string{}, items...)
	sort.Strings(a)
	var lines []string
	for i := 0; i < len(a); {
		j := i
		for j < len(a) && a[j] == a[i] {
			j++
		}
		lines = append(lines, fmt.Sprintf("%7d %s", j-i, a[i]))
		i = j
	}
	var out [][2]string
	for _, l := range sortRN(lines, func(x string) string { return x }) {
		f := bread(l, " \t\n", 2)
		out = append(out, [2]string{f[0], f[1]})
	}
	return out
}

func head(a []string, n int) []string {
	if len(a) > n {
		return a[:n]
	}
	return a
}

func secFiles(s *sec) {
	u := s.u
	r := s.r
	s.section("FILES")
	html := "/home/" + u.context + "/docker-data/volumes/" + u.context + "_html_data/_data"

	s.subsection("DISK")
	ts, _ := r.needQuota()
	q := r.quotaRow(u.context)
	var qf []string
	if q != nil {
		qf = bread(idxS(q, 1)+" "+idxS(q, 2)+" "+idxS(q, 3)+" "+idxS(q, 4), " \t\n", 4)
	} else {
		qf = make([]string, 4)
	}
	if qf[0] == "" {
		s.warn("No quota data for " + u.context + ", run 'opencli user-quota --update " + u.name + "'.")
	} else {
		s.row("Quota checked", or(ts, "unknown"))
		s.dim("Quota counts files owned by the account, files written by apps as other users aren't included.")
		for _, kind := range []string{"disk", "inodes"} {
			used, hard, label := qf[0], qf[1], "Disk"
			if kind == "inodes" {
				used, hard, label = qf[2], qf[3], "Inodes"
			}
			if reDigits.MatchString(hard) && bashInt(hard) > 0 {
				pct := bashInt(used) * 100 / bashInt(hard)
				if kind == "disk" {
					s.row(label, fmt.Sprintf("%s of %s (%d%%)", humanKB(used), humanKB(hard), pct))
				} else {
					s.row(label, fmt.Sprintf("%s of %s (%d%%)", used, hard, pct))
				}
				// same thresholds as the dashboard
				if pct >= 100 {
					s.err(fmt.Sprintf("%s usage is at %d%% of the limit, new files can't be written.", label, pct))
				} else if pct >= 90 {
					s.warn(fmt.Sprintf("%s usage is at %d%% of the limit.", label, pct))
				}
			} else if kind == "disk" {
				s.row(label, humanKB(used)+" (unlimited)")
			} else {
				s.row(label, used+" (unlimited)")
			}
		}
	}

	// where the space goes, from the du the DOMAINS section shares
	vols := "/home/" + u.context + "/docker-data/volumes"
	if conf.IsDir(vols) {
		du := u.needDU()
		children := func(base string) []string {
			var out []string
			for _, l := range hereLines(du) {
				p := tabField(l, 2)
				if strings.HasPrefix(p, base+"/") && !strings.Contains(p[len(base)+1:], "/") {
					out = append(out, l)
				}
			}
			return head(sortRN(out, func(x string) string { return x }), 5)
		}
		s.dim("Biggest volumes:")
		var vl []string
		for _, l := range children(vols) {
			l = strings.Replace(l, vols+"/"+u.context+"_", "", 1)
			l = strings.Replace(l, vols+"/", "", 1)
			vl = append(vl, l)
		}
		s.duRows(vl)
		if conf.IsDir(html) {
			s.dim("Biggest folders in /var/www/html:")
			re := regexp.MustCompile(`^([0-9]+)\s+` + regexp.QuoteMeta(html) + `/`)
			var hl []string
			for _, l := range children(html) {
				hl = append(hl, re.ReplaceAllString(l, "$1 "))
			}
			s.duRows(hl)
		}
	}

	s.subsection("PERMISSIONS")
	if !conf.IsDir(html) {
		s.err("Website files volume is missing (" + html + ").")
	} else {
		// 0000 is what the file manager flags, world-writable is a security risk (sticky dirs like tmp are fine)
		prune := html + "/.quarantine"
		noperm := findPerm(html, prune, time.Now().Add(60*time.Second), func(_ os.FileMode, m uint32) bool { return m&07777 == 0 })
		writable := findPerm(html, prune, time.Now().Add(60*time.Second), func(_ os.FileMode, m uint32) bool {
			t := m & syscall.S_IFMT
			return m&02 != 0 && t != syscall.S_IFLNK && !(t == syscall.S_IFDIR && m&01000 != 0)
		})
		s.row("No permissions (0000)", strconv.Itoa(len(noperm)))
		if len(noperm) > 0 {
			s.err(fmt.Sprintf("%d file(s) have no permissions (0000) and can't be read.", len(noperm)))
			for _, p := range head(noperm, 5) {
				s.line("  /var/www/html" + strings.TrimPrefix(p, html))
			}
			if len(noperm) > 5 {
				s.line("  ...")
			}
		}
		s.row("World-writable", strconv.Itoa(len(writable)))
		if len(writable) > 0 {
			s.warn(fmt.Sprintf("%d file(s) or folder(s) are world-writable (o+w), Fix Permissions resets them.", len(writable)))
			var tops []string
			for _, p := range writable {
				p = strings.TrimPrefix(p, html+"/")
				tops = append(tops, strings.SplitN(p, "/", 2)[0])
			}
			for _, x := range head2(uniqC(tops), 5) {
				s.p("  %s  %s\n", padL(x[0], 7), "/var/www/html/"+x[1])
			}
		}
		if len(noperm)+len(writable) == 0 {
			s.ok("No permission problems found.")
		}
	}

	s.subsection("TRASH")
	trash := "/home/" + u.context + "/.local/share/Trash"
	trashKB, trashN := "0", 0
	if conf.IsDir(trash) {
		trashKB = strings.SplitN(sh.C("du", "-sk", trash).NoErr().Str(), "\t", 2)[0]
		if f, err := os.Open(trash); err == nil {
			names, _ := f.Readdirnames(-1)
			f.Close()
			for _, n := range names {
				if n != ".trash_restore" {
					trashN++
				}
			}
		}
	}
	s.row("Trash", fmt.Sprintf("%d item(s), %s", trashN, humanKB(trashKB)))

	// files-purge_trash deletes items older than autopurge_trash days, but only when its cron runs
	days := r.panelConfig("autopurge_trash")
	purgeCron := cronD("files-purge_trash", cronCommentWS)
	if reDigits.MatchString(days) && purgeCron != "" {
		s.row("Auto-purge", "after "+days+" days, "+cronLabel(purgeCron))
		if conf.IsFile(trash+"/.trash_restore") && trashN > 0 {
			var dates []string
			for _, m := range regexp.MustCompile(`deletion_date=[0-9T:-]*`).FindAllString(readFile(trash+"/.trash_restore"), -1) {
				dates = append(dates, awkPart(m, "=", 2))
			}
			sort.Strings(dates)
			if len(dates) > 0 && dates[0] != "" {
				oldest := dates[0]
				due := ""
				if e, ok := localEpoch(strings.Replace(oldest, "T", " ", 1)); ok {
					due = time.Unix(e+bashInt(days)*86400, 0).Format("2006-01-02 15:04")
				}
				s.row("Oldest item", "trashed "+oldest+", purged on the first run after "+due)
			}
		}
	} else if reDigits.MatchString(days) {
		s.row("Auto-purge", "off")
		s.warn("autopurge_trash is " + days + " days but no cron runs 'opencli files-purge_trash', so the trash is never emptied.")
	} else {
		s.row("Auto-purge", "off (autopurge_trash not set)")
	}
	if bashInt(trashKB) >= 1048576 {
		s.info("Emptying the trash would free " + humanKB(trashKB) + ".")
	}

	s.subsection("FTP")
	list := "/etc/openpanel/ftp/users/" + u.context + "/users.list"
	if !r.modulesHave("ftp") {
		s.row("FTP", "module disabled on the server")
	} else {
		ftpState := r.rootState("openadmin_ftp")
		s.row("FTP server", or(ftpState, "not installed"))
		accounts := grepCount(list, func(l string) bool { return l != "" })
		s.row("Accounts", or(accounts, "0"))
		if bashInt(accounts) > 0 {
			if ftpState != "running" {
				s.warn("FTP server isn't running, the " + accounts + " FTP account(s) can't connect.")
			}
			for _, l := range readLines(readFile(list)) {
				f := bread(l, "|", 4)
				fu, fpath := f[0], f[2]
				if fu == "" {
					continue
				}
				hostPath := fpath
				if strings.HasPrefix(fpath, "/var/www/html") {
					hostPath = html + strings.TrimPrefix(fpath, "/var/www/html")
				}
				s.p("    %s %s\n", padR(fu, 36), fpath)
				if !conf.IsDir(hostPath) {
					s.warn("FTP account " + fu + " points to " + fpath + ", which doesn't exist.")
				}
			}
		}
		var conns []string
		for _, l := range strings.Split(sh.Opencli("ftp-connections", u.name).Timeout(10*time.Second).NoErr().Str(), "\n") {
			if strings.Contains(l, "vsftpd:") {
				conns = append(conns, l)
			}
		}
		s.row("Connected now", strconv.Itoa(len(conns)))
		for _, l := range conns {
			s.line(cutC("  "+strings.TrimLeft(l, " "), 100))
		}
	}

	s.subsection("MALWARE SCANNER")
	scans := rpUsersCore + "/" + u.name + "/malware_scans.jsonl"
	clam := r.rootState("clamav")
	s.row("ClamAV", or(clam, "not installed"))
	sched := cronD("files-malware_scan", cronComment)
	if sched != "" {
		s.row("Scheduled scan", cronLabel(sched))
	} else {
		s.row("Scheduled scan", "none")
	}
	if r.modulesHave("malware_scan") && clam != "running" {
		s.warn("ClamAV isn't running, malware scans fail until it's started.")
	}

	if st, err := os.Stat(scans); err == nil && st.Size() > 0 {
		content := readFile(scans)
		lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
		last := lines[len(lines)-1]
		lastOK := ""
		for _, l := range lines {
			if strings.Contains(l, `"status":"completed"`) {
				lastOK = l
			}
		}
		if lastOK != "" {
			if o, err := ojson.ParseObject(lastOK); err == nil {
				lastOK = ojson.Text(jqAlt(o, "finished_at", "started_at"))
			} else {
				lastOK = ""
			}
		}
		failed := 0
		for _, l := range head(reverse(lines), 20) {
			if strings.Contains(l, `"status":"failed"`) {
				failed++
			}
		}
		lastTxt := ""
		lo, lerr := ojson.ParseObject(last)
		if lerr == nil {
			g := func(k string) string { v, _ := lo.Get(k); return ojson.Text(v) }
			lastTxt = fmt.Sprintf("%s (%s, %s) %s, %s scanned, %s infected", g("started_at"), g("source"), g("status"), g("path"), g("scanned"), g("infected"))
		}
		s.row("Last scan", lastTxt)
		s.row("Last completed scan", or(lastOK, "never"))
		if lerr == nil && lo.Str("status") == "failed" {
			s.warn("Last malware scan failed: " + ojson.Text(jqAlt(lo, "message", "")))
		}
		if failed > 1 {
			s.info(fmt.Sprintf("%d of the last 20 malware scans failed.", failed))
		}
	} else {
		s.row("Last scan", "never")
	}

	qdir := html + "/.quarantine"
	qn := 0
	if conf.IsDir(qdir) {
		filepath.WalkDir(qdir, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() && d.Name() != ".metadata.jsonl" {
				qn++
			}
			return nil
		})
	}
	s.row("Quarantine", fmt.Sprintf("%d file(s)", qn))
	if qn > 0 {
		s.warn(fmt.Sprintf("%d file(s) in malware quarantine, review them on the Malware Scanner > Quarantine page.", qn))
	}
}

func head2(a [][2]string, n int) [][2]string {
	if len(a) > n {
		return a[:n]
	}
	return a
}

func reverse(a []string) []string {
	out := make([]string, len(a))
	for i, x := range a {
		out[len(a)-1-i] = x
	}
	return out
}

// jqAlt is .a // .b // "default", the last key is used as a literal when it isn't a key
func jqAlt(o *ojson.Object, keys ...string) any {
	for i, k := range keys {
		if v, _ := o.Get(k); v != nil && v != false {
			return v
		}
		if i == len(keys)-1 && k == "" {
			return "no reason given"
		}
	}
	if keys[len(keys)-1] == "" {
		return "no reason given"
	}
	return nil
}

var envQuoteRe = regexp.MustCompile(`["'][[:space:]]*$`)

// envValue is KEY from a shell-style env file, quotes stripped, commented lines ignored
func envValue(file, key string) string {
	re := regexp.MustCompile(`^[[:space:]]*` + key + `=`)
	for _, l := range awkLines(readFile(file)) {
		if re.MatchString(l) {
			v := ""
			if i := strings.IndexByte(l, '='); i >= 0 {
				v = l[i+1:]
			}
			if v != "" && (v[0] == '"' || v[0] == '\'') {
				v = v[1:]
			}
			return envQuoteRe.ReplaceAllString(v, "")
		}
	}
	return ""
}

var tsRe = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]+`)

func secBackups(s *sec) {
	u := s.u
	s.section("BACKUPS")
	env := "/home/" + u.context + "/backup.env"
	if conf.IsFile(rpUsersCore + "/" + u.context + "/admin.backups") {
		s.row("Managed by", "administrator")
	} else {
		s.row("Managed by", "user")
	}
	if !conf.IsFile(env) {
		s.warn("No backup.env, account backups were never set up.")
		userBackupArchives(s)
		return
	}
	ev := func(k string) string { return envValue(env, k) }

	// same credential keys the Backups page uses to call a destination configured
	dest, detail := "", ""
	switch {
	case ev("AWS_ACCESS_KEY_ID") != "":
		dest, detail = "S3", ev("AWS_ENDPOINT")+"/"+ev("AWS_S3_BUCKET_NAME")+"/"+ev("AWS_S3_PATH")
	case ev("SSH_HOST_NAME") != "":
		dest, detail = "SSH", ev("SSH_USER")+"@"+ev("SSH_HOST_NAME")+":"+ev("SSH_REMOTE_PATH")
	case ev("WEBDAV_USERNAME") != "":
		dest, detail = "WebDAV", ev("WEBDAV_URL")+ev("WEBDAV_PATH")
	case ev("AZURE_STORAGE_ACCOUNT_NAME") != "":
		dest, detail = "Azure", ev("AZURE_STORAGE_ACCOUNT_NAME")+"/"+ev("AZURE_STORAGE_CONTAINER_NAME")
	case ev("DROPBOX_APP_KEY") != "":
		dest, detail = "Dropbox", ev("DROPBOX_REMOTE_PATH")
	}
	d := or(dest, "none")
	if detail != "" {
		d += " (" + detail + ")"
	}
	s.row("Destination", d)

	cron, retention := ev("BACKUP_CRON_EXPRESSION"), ev("BACKUP_RETENTION_DAYS")
	switch {
	case strings.HasPrefix(cron, "@"):
		s.row("Schedule", cron[1:])
	case cron == "":
		s.row("Schedule", "default (daily at 00:00)")
	default:
		s.row("Schedule", cronLabel(cron))
	}
	if retention == "" || retention == "-1" {
		s.row("Retention", "keep forever")
	} else {
		s.row("Retention", retention+" days")
	}
	if ev("GPG_PASSPHRASE")+ev("GPG_PUBLIC_KEY_RING")+ev("AGE_PASSPHRASE")+ev("AGE_PUBLIC_KEYS") != "" {
		s.row("Encryption", "on")
	} else {
		s.row("Encryption", "off")
	}
	if ev("NOTIFICATION_URLS") != "" {
		s.row("Failure alerts", "on ("+ev("NOTIFICATION_LEVEL")+")")
	} else {
		s.row("Failure alerts", "off")
	}

	// the backup container runs the schedule, without it nothing happens
	state := u.psField("backup", 2)
	s.row("Backup service", or(state, "not created"))
	switch {
	case dest == "":
		s.warn("No backup destination is configured, this account isn't backed up.")
	case state != "running":
		s.err("Backups go to " + dest + " but the backup service isn't running, scheduled backups don't happen.")
	default:
		if u.pod(20*time.Second, "exec", "backup", "test", "-f", "/var/run/lock/dockervolumebackup.lock").Quiet().OK() {
			s.row("In progress", "yes")
		}
		// offen/docker-volume-backup logs one line per run
		logs, _ := u.pod(20*time.Second, "logs", "--since", "720h", "backup").Combined()
		ll := strings.Split(logs, "\n")
		ll = ll[max(0, len(ll)-500):]
		lastOK, lastErr := "", ""
		for _, l := range ll {
			if strings.Contains(strings.ToLower(l), "finished running backup tasks") {
				lastOK = l
			}
			if strings.Contains(l, "level=ERROR") {
				lastErr = l
			}
		}
		lastOK = tsRe.FindString(lastOK)
		s.row("Last successful run", or(lastOK, "none in the last 30 days"))
		if lastErr != "" {
			errTime := tsRe.FindString(lastErr)
			if lastOK == "" || errTime > lastOK {
				msg := ""
				if m := regexp.MustCompile(`error="?([^"]+)`).FindStringSubmatch(lastErr); m != nil {
					msg = cutC(m[1], 150)
				}
				s.err("Last backup run failed: " + msg)
			}
		}
		if lastOK == "" {
			s.warn("No backup finished in the last 30 days.")
		}
	}

	// index the Backups page builds of what's at the destination
	index := "/home/" + u.context + "/available_backups.json"
	if dest != "" && conf.IsFile(index) {
		v, perr := ojson.Parse(readFile(index))
		idxErr := ""
		if o, ok := v.(*ojson.Object); ok && perr == nil {
			if e, _ := o.Get("error"); e != nil && e != false {
				idxErr = ojson.Text(e)
			}
		}
		if idxErr != "" {
			s.row("Backups at destination", "unknown ("+idxErr+")")
		} else {
			count := ""
			var names []string
			if perr == nil {
				count = jqLength(v)
				if arr, ok := v.([]any); ok {
					for _, it := range arr {
						for _, m := range regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}`).FindAllString(ojson.Text(jqPath(it, "backup_file")), -1) {
							names = append(names, m)
						}
					}
				}
			}
			sort.Strings(names)
			newest := ""
			if len(names) > 0 {
				newest = names[len(names)-1]
			}
			s.row("Backups at destination", or(count, "0")+", newest "+or(newest, "unknown")+" (as of "+mtimeFmt(index)+")")
		}
	}

	userBackupArchives(s)
}

// userBackupArchives covers full account archives made with 'opencli user-backup', plus the server's config backup
func userBackupArchives(s *sec) {
	u := s.u
	// 'opencli backup' saves panel config for all accounts (domains, dns, settings), not website files or databases
	lastRun := ""
	for _, l := range awkLines(readFile("/var/log/openpanel/admin/system-backup-runs.jsonl")) {
		if strings.Contains(l, `"action":"backup"`) {
			lastRun = l
		}
	}
	if lastRun != "" {
		o, err := ojson.ParseObject(lastRun)
		txt := ""
		if err == nil {
			t, _ := o.Get("timestamp")
			st, _ := o.Get("status")
			txt = ojson.Text(t) + " " + ojson.Text(st)
		}
		s.row("Server config backup", txt)
		if err == nil && o.Str("status") == "failed" {
			d, _ := o.Get("detail")
			s.warn("Last server config backup failed: " + ojson.Text(d))
		}
	} else {
		s.row("Server config backup", "never ran")
	}

	dir := "/home/" + u.context + "/docker-data/volumes/" + u.context + "_html_data/_data/_backups"
	files, _ := filepath.Glob(dir + "/" + globQuote(u.name) + "_*.tar.gz")
	if len(files) > 0 {
		type fi struct {
			p string
			t time.Time
		}
		var l []fi
		for _, f := range files {
			if st, err := os.Stat(f); err == nil {
				l = append(l, fi{f, st.ModTime()})
			}
		}
		sort.SliceStable(l, func(i, j int) bool {
			if !l[i].t.Equal(l[j].t) {
				return l[i].t.After(l[j].t)
			}
			return l[i].p < l[j].p
		})
		if len(l) > 0 {
			var paths []string
			for _, x := range l {
				paths = append(paths, x.p)
			}
			out := sh.C("du", append([]string{"-ck"}, paths...)...).NoErr().Str()
			lines := strings.Split(out, "\n")
			size := strings.SplitN(lines[len(lines)-1], "\t", 2)[0]
			s.row("Full account archives", fmt.Sprintf("%d in /var/www/html/_backups, %s, newest %s", len(l), humanKB(size), filepath.Base(l[0].p)))
			return
		}
	}
	s.row("Full account archives", "none")
}

func globQuote(s string) string {
	return strings.NewReplacer(`*`, `\*`, `?`, `\?`, `[`, `\[`, `\`, `\\`).Replace(s)
}

var (
	reverseProxyRe = regexp.MustCompile(`^[[:space:]]*reverse_proxy`)
	fileServerRe   = regexp.MustCompile(`^[[:space:]]*file_server`)
)

func secDomains(s *sec) {
	u := s.u
	s.section("DOMAINS")
	rows := u.domainRows
	u.needDU()
	if rows == "" {
		s.row("Domains", "none")
		return
	}
	html := "/home/" + u.context + "/docker-data/volumes/" + u.context + "_html_data/_data"
	var all []string
	for _, l := range strings.Split(rows, "\n") {
		all = append(all, strings.SplitN(l, "\t", 2)[0])
	}
	nTotal := countLines(strings.Join(all, "\n"))
	nSub, nSusp, nRedir := 0, 0, 0

	s.p("  %s%s %s %s %s %s%s\n", s.r.cDim, padR("Domain", 36), padR("Type", 6), padR("Size", 10), padR("Status", 11), "Docroot / redirect", s.r.cReset)
	for _, l := range hereLines(rows) {
		f := bread(l, "\t", 3)
		domain, docroot := f[0], f[1]
		confFile := "/etc/openpanel/caddy/domains/" + domain + ".conf"

		// subdomain when another of the user's domains is its parent, same as the panel's Categorize
		typ := "main"
		for _, d := range all {
			if d != domain && strings.HasSuffix(domain, "."+d) {
				typ = "sub"
				nSub++
				break
			}
		}
		hostRoot := html + strings.TrimPrefix(docroot, "/var/www/html")
		size := "-"
		if conf.IsDir(hostRoot) {
			// top level docroots are in the shared du, deeper ones get their own
			kb := u.duOf(hostRoot)
			if kb == "" {
				kb = strings.SplitN(sh.C("du", "-sk", hostRoot).Timeout(30*time.Second).NoErr().Str(), "\t", 2)[0]
			}
			size = humanKB(kb)
		}

		// same rules as the domains list: the last reverse_proxy/file_server line decides, file_server means suspended
		status, comment, extra := "active", "", ""
		if !conf.IsFile(confFile) {
			status = "no config"
		} else {
			c := readFile(confFile)
			susp := false
			for _, cl := range awkLines(c) {
				if reverseProxyRe.MatchString(cl) {
					susp = false
				}
				if fileServerRe.MatchString(cl) {
					susp = true
				}
			}
			if susp {
				status = "suspended"
				first := strings.SplitN(c, "\n", 2)[0]
				if strings.HasPrefix(first, "# comment:") {
					comment = strings.TrimLeft(first[len("# comment:"):], " ")
				}
				nSusp++
			}
			for _, cl := range strings.Split(c, "\n") {
				if strings.Contains(cl, "redir ") {
					extra = fieldN(cl, 2)
					break
				}
			}
			if extra != "" {
				extra = "-> " + extra
				nRedir++
			}
		}
		if extra == "" {
			if docroot == "/var/www/html/"+domain {
				extra = "default"
			} else {
				extra = docroot
			}
		}
		s.p("  %s %s %s %s %s\n", padR(domain, 36), padR(typ, 6), padR(size, 10), padR(status, 11), extra)

		if !conf.IsFile(confFile) {
			s.err(domain + " has no Caddy config (" + confFile + "), it doesn't load.")
		}
		if !conf.IsDir(hostRoot) {
			s.warn(domain + " docroot " + docroot + " doesn't exist.")
		}
		if status == "suspended" {
			m := domain + " is suspended"
			if comment != "" {
				m += ": " + comment
			}
			s.warn(m + ".")
		}
	}
	s.line("")
	s.row("Total", fmt.Sprintf("%d (%d main, %d subdomains)", nTotal, nTotal-nSub, nSub))
	s.row("Redirects", strconv.Itoa(nRedir))
	s.row("Suspended", strconv.Itoa(nSusp))
}

// fieldN is awk '{ print $n }'
func fieldN(l string, n int) string {
	f := strings.Fields(l)
	if n-1 < len(f) {
		return f[n-1]
	}
	return ""
}
