// ##############################################################################
// Script Name: internal/commands/sentinel/sentinel.go
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
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
)

const usageLine = "opencli sentinel [--startup] [--report] [--action=<name> --title=<title> --message=<msg> [--severity=<level>] [--details=<json> [--merge-key=<key>]] [--no-send]]"

func init() {
	cli.Register(&cli.Command{
		Name:  "sentinel",
		Desc:  "Check system services, traffic and resource usage, and log/send custom notifications on request.",
		Usage: usageLine,
		Run:   sentinel,
	})
}

// config
const (
	confFile               = "/etc/openpanel/openpanel/conf/openpanel.config"
	iniFile                = "/etc/openpanel/openadmin/config/notifications.ini"
	notificationsPauseFile = "/tmp/openpanel_notifications_paused"

	// lock files
	snapshotFile              = "/var/log/openpanel/admin/sentinel_snapshots.jsonl"
	snapshotMaxLines          = 8640 // 30d
	lockFileForDNSCheck       = "/tmp/sentinel.dns"
	lockFileForOOMCheck       = "/tmp/sentinel.oom"
	lockFileForDockerPrune    = "/tmp/sentinel.docker"
	lockFileForSwapCleanup    = "/tmp/sentinel.swap"
	lockFileForRedisStuck     = "/tmp/sentinel.redis_stuck"
	lockFileForUserContainers = "/tmp/sentinel.user_containers"
	lockFileForRatelimit      = "/tmp/sentinel.ratelimit"
	lockFileForMailReport     = "/var/lib/openpanel/sentinel.mail_report"
	lockFileForRBL            = "/var/lib/openpanel/sentinel.rbl"
	// one blocklist zone per line, admins can add or drop lists here
	rblListFile = "/etc/openpanel/openadmin/config/rbl.txt"
	// one JSON per day with the result, OpenAdmin's daily summary reads these
	rblDataDir        = "/var/lib/openpanel/rbl"
	ratelimitCheckHrs = 6
	startupStagger    = 2    // seconds between user starts on boot
	mysqlIdleGrace    = 7200 // mysql of an account this old with no databases gets stopped
	runTimeout        = 900  // whole run gets killed after this, so a hung podman can't hold the run lock forever
	taskTimeout       = 600  // each parallel check gets killed after this
	runTimeoutTitle   = "Sentinel checks timed out!"
	mailQueueMax      = 500 // hardcoded until they get settings in OpenAdmin, 0 turns the mail queue check off
	holdSpam          = "yes"
	blockIP           = "yes"
	blockIPMinutes    = 60
	streakRuns        = 2 // consecutive over-threshold runs needed before load/cpu/ram alert, so short spikes don't page the admin
	ipCacheFile       = "/tmp/public.ipv4"
)

const (
	cOK   = "\033[32m[✔]\033[0m"
	cWarn = "\033[38;5;214m[!]\033[0m"
	cFail = "\033[31m[✘]\033[0m"
	cArr  = "\033[33m[→]\033[0m"
)

var (
	displayTime string
	hostname    string

	email, emailAlert, webhookURL                      string
	optReboot, optDNS, optLogin, optSSH, services      string
	optLimit, optRatelimit, optAttack, optMail, optRBL string
	maxTotalConn, maxConnPerIP                         int
	mailBouncePct, mailDeferPct                        int
	loadThreshold, cpuThreshold, ramThreshold          int
	diskThreshold, swapThreshold                       int
	caddyIsActive                                      bool
	dockerPSCache                                      string
)

// keyGet is awk -F= "/^key=/{print $2; exit}" file
func keyGet(file, key string) string {
	re, err := regexp.Compile("^" + key + "=")
	if err != nil {
		re = regexp.MustCompile("^" + regexp.QuoteMeta(key) + "=")
	}
	for _, l := range conf.Lines(file) {
		if re.MatchString(l) {
			return strings.Split(l, "=")[1]
		}
	}
	return ""
}

func confGet(k string) string { return keyGet(confFile, k) }
func iniGet(k string) string  { return keyGet(iniFile, k) }

func validateYesNo(v string) string {
	if v == "yes" || v == "no" {
		return v
	}
	return "yes"
}

var (
	numRe   = regexp.MustCompile(`^([1-9][0-9]?|100)$`)
	countRe = regexp.MustCompile(`^[1-9][0-9]{0,6}$`)
)

func validateNumber(v string, d int) int {
	if numRe.MatchString(v) {
		return atoi(v)
	}
	return d
}

// connection limits go past 100 so they can't use validateNumber
func validateCount(v string, d int) int {
	if countRe.MatchString(v) {
		return atoi(v)
	}
	return d
}

func atoi(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }

func yesOnly(v string) string {
	if v == "yes" {
		return "yes"
	}
	return "no"
}

func loadConfig() {
	displayTime = time.Now().Format("2006-01-02 15:04:05")
	hostname, _ = os.Hostname()
	email = confGet("email")
	emailAlert = "no"
	if email != "" {
		emailAlert = "yes"
	}
	webhookURL = iniGet("webhook_url")
	optReboot = validateYesNo(iniGet("reboot"))
	optDNS = validateYesNo(iniGet("dns"))
	optLogin = validateYesNo(iniGet("login"))
	optSSH = validateYesNo(iniGet("ssh"))
	services = iniGet("services")
	if services == "" {
		services = "admin,podman,mysql,csf,panel"
	}
	optLimit = validateYesNo(iniGet("limit"))
	// new and off by default, so a missing key means no
	optRatelimit = yesOnly(iniGet("ratelimit"))
	optAttack = validateYesNo(iniGet("attack"))
	maxTotalConn = validateCount(iniGet("max_total_conn"), 5000)
	maxConnPerIP = validateCount(iniGet("max_conn_per_ip"), 500)
	// yesterday's mail report, only alerts when delivery looks wrong, bounce/defer are % of the mail that went out
	optMail = yesOnly(iniGet("mail"))
	mailBouncePct = validateNumber(iniGet("mail_bounce"), 20)
	mailDeferPct = validateNumber(iniGet("mail_defer"), 20)
	// daily blocklist check of the server IP, only runs when this server sends its own mail
	optRBL = validateYesNo(iniGet("rbl"))
	loadThreshold = validateNumber(iniGet("load"), 20)
	cpuThreshold = validateNumber(iniGet("cpu"), 90)
	ramThreshold = validateNumber(iniGet("ram"), 85)
	diskThreshold = validateNumber(iniGet("du"), 85)
	swapThreshold = validateNumber(iniGet("swap"), 40)
}

// run holds the counters and output of the main run or of one parallel check
type run struct {
	out                      io.Writer
	status, pass, warn, fail int
	streak                   int
}

func (r *run) printf(f string, a ...any) { fmt.Fprintf(r.out, f, a...) }
func (r *run) println(a ...any)          { fmt.Fprintln(r.out, a...) }
func (r *run) atLeast1() {
	if r.status < 1 {
		r.status = 1
	}
}

// syncBuf collects a check's output, goroutines of the user container sweep write to it at once
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// lockedWriter serializes writes from parallel goroutines to one writer
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func hr(w io.Writer) {
	n := 80
	if c := os.Getenv("COLUMNS"); c != "" {
		if v, err := strconv.Atoi(c); err == nil {
			n = v
		}
	}
	if n < 0 {
		n = 0
	}
	fmt.Fprintln(w, strings.Repeat("-", n))
}

func (r *run) summary() {
	hr(r.out)
	switch r.status {
	case 0:
		r.println("\033[32mAll Tests Passed!\033[0m")
	case 1:
		r.println("\033[93mSome non-critical tests failed.\033[0m")
	default:
		r.println("\033[41mOne or more tests failed.\033[0m")
	}
	hr(r.out)
	r.printf("\033[1m%d PASS  %d WARN  %d FAIL\033[0m\n", r.pass, r.warn, r.fail)
	hr(r.out)
}

func sentinel(args []string) int {
	if !isFile(iniFile) {
		fmt.Printf("Error: OpenAdmin notifications settings file not found: %s\n", iniFile)
		return 1
	}
	if !isFile(confFile) {
		fmt.Printf("Error: OpenPanel main configuration file not found: %s\n", confFile)
		return 1
	}
	loadConfig()
	r := &run{out: os.Stdout}

	var action, message, title, severity, details, mergeKey, noSend string
	for _, arg := range args {
		val := func() string { _, v, _ := strings.Cut(arg, "="); return v }
		switch {
		case arg == "--startup":
			r.performStartupActions()
			return 0
		case arg == "--report":
			r.emailDailyReport()
			return 0
		case strings.HasPrefix(arg, "--action="):
			action = val()
		case strings.HasPrefix(arg, "--message="):
			message = val()
		case strings.HasPrefix(arg, "--title="):
			title = val()
		case strings.HasPrefix(arg, "--severity="):
			severity = val()
		case strings.HasPrefix(arg, "--details="):
			details = val()
		case strings.HasPrefix(arg, "--merge-key="):
			mergeKey = val()
		case arg == "--no-send":
			noSend = "1"
		}
	}

	if action != "" {
		if severity != "info" && severity != "warning" && severity != "critical" {
			severity = "info"
		}
		r.writeActionNotification(action, title, message, severity, details, mergeKey, noSend)
		return 0
	}

	if os.Getenv("FLOCKED") != "1" {
		// -o so containers started during the run don't inherit the lock and keep holding it
		rc := sh.C("env", append([]string{"FLOCKED=1", "flock", "-n", "-o", "-E", "75", "/root/sentinel_run.lock", "timeout", "-k", "30", strconv.Itoa(runTimeout), sh.Self(), "sentinel"}, args...)...).
			Env("OPENCLI_RAW=1").StdinReader(os.Stdin).Code()
		switch rc {
		case 75:
			fmt.Println("Error: Another instance is already running.")
			return 1
		case 124, 137:
			fmt.Printf("%s Sentinel did not finish in %ds and was stopped.\n", cFail, runTimeout)
			r.writeNotification("critical", "system", runTimeoutTitle, fmt.Sprintf("Sentinel did not finish its checks in %d minutes and was stopped, usually because a podman command hung. Until this is fixed, services are not monitored or restarted. Check with: timeout 10 podman ps and systemctl status podman.socket", runTimeout/60), "")
		default:
			r.resolveNotification(runTimeoutTitle, false)
		}
		return rc
	}
	hr(r.out)
	r.println("  Sentinel - OpenPanel server health monitor")
	hr(r.out)

	// an update restarts containers on purpose, so don't alert on it or recreate them mid-update
	// locks older than 30min are stale, update.sh deletes those too
	if updateInProgress() {
		r.printf("%s OpenPanel update in progress, skipping checks until it finishes.\n", cWarn)
		hr(r.out)
		return 0
	}

	queueOn = true

	r.println("Checking services:")
	dockerPSRefresh()
	r.checkServices()
	r.checkOOMLogs()

	hr(r.out)
	r.println("Checking traffic:")
	r.checkHTTPSTraffic()

	hr(r.out)
	r.println("Checking logins, resources, and DNS...")

	tasks := []struct {
		name string
		fn   func(*run)
	}{
		{"check_new_logins", (*run).checkNewLogins},
		{"check_ssh_logins", (*run).checkSSHLogins},
		{"check_disk_usage", (*run).checkDiskUsage},
		{"check_system_load", (*run).checkSystemLoad},
		{"check_ram_usage", (*run).checkRAMUsage},
		{"check_cpu_usage", (*run).checkCPUUsage},
		{"check_swap_usage", (*run).checkSwapUsage},
		{"check_if_panel_domain_and_ns_resolve_to_server", (*run).checkDNS},
		{"check_user_containers", (*run).checkUserContainers},
		{"check_ratelimit_hits", (*run).checkRatelimitHits},
		{"check_mail_queue", (*run).checkMailQueue},
		{"check_mail_report", (*run).checkMailReport},
		{"check_rbl", (*run).checkRBL},
		{"check_license_expiry", (*run).checkLicenseExpiry},
	}
	type result struct {
		buf  *syncBuf
		r    *run
		done chan bool
	}
	results := make([]result, len(tasks))
	for i, t := range tasks {
		buf := &syncBuf{}
		tr := &run{out: buf}
		done := make(chan bool, 1)
		results[i] = result{buf, tr, done}
		go func(fn func(*run)) {
			ok := false
			// an exit inside a check only ends that check, like exit in the bash subshell
			cli.Invoke(&cli.Command{Run: func([]string) int { fn(tr); ok = true; return 0 }}, nil)
			done <- ok
		}(t.fn)
	}

	deadline := time.After(taskTimeout * time.Second)
	var timedOut []string
	expired := false
	for i, t := range tasks {
		res := results[i]
		ok, finished := false, false
		if !expired {
			select {
			case ok = <-res.done:
				finished = true
			case <-deadline:
				expired = true
			}
		} else {
			select {
			case ok = <-res.done:
				finished = true
			default:
			}
		}
		out := res.buf.String()
		s, p, w, f := 0, 0, 0, 0
		if finished && ok {
			s, p, w, f = res.r.status, res.r.pass, res.r.warn, res.r.fail
		}
		if !finished {
			timedOut = append(timedOut, t.name)
			s, p, w, f = 2, 0, 0, 1
			out += fmt.Sprintf("%s %s did not finish in %ds and was stopped.\n", cFail, t.name, taskTimeout)
		}
		fmt.Fprint(r.out, out)
		if s > r.status {
			r.status = s
		}
		r.pass += p
		r.warn += w
		r.fail += f
	}

	if len(timedOut) > 0 {
		r.writeNotification("warning", "system", "Sentinel checks did not finish", fmt.Sprintf("These checks did not finish in %d minutes and were stopped: %s. Usually a podman command hung. Check with: timeout 10 podman ps", taskTimeout/60, strings.Join(timedOut, " ")), "")
	} else {
		r.resolveNotification("Sentinel checks did not finish", false)
	}

	r.flushNotificationQueue()
	r.writeSnapshot()
	r.summary()
	return 0
}

func updateInProgress() bool {
	const lock = "/var/lock/openpanel_update.lock"
	st, err := os.Stat(lock)
	if err != nil || time.Since(st.ModTime()) >= 30*time.Minute {
		return false
	}
	f, err := os.Open(lock)
	if err != nil {
		return true
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return true
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

func isFile(p string) bool { st, err := os.Stat(p); return err == nil && st.Mode().IsRegular() }
func isDir(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }
func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func touch(p string) {
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
	}
	now := time.Now()
	os.Chtimes(p, now, now)
}

// ageSecs is now minus the file's mtime, ok is false when it doesn't exist
func ageSecs(p string) (int, bool) {
	st, err := os.Stat(p)
	if err != nil {
		return 0, false
	}
	return int(time.Now().Unix() - st.ModTime().Unix()), true
}

// readFirst is $(cat file 2>/dev/null)
func readTrim(p string) string { return strings.TrimRight(conf.Read(p), "\n") }

// bashLines is what a while read loop sees, a last line without newline is skipped
func bashLines(s string) []string {
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return strings.Split(s[:i], "\n")
	}
	return nil
}

// lines is the text split on newlines without the trailing one
func lines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func fields(s string) []string { return strings.Fields(s) }

func field(s string, n int) string {
	f := strings.Fields(s)
	if n < 1 || n > len(f) {
		return ""
	}
	return f[n-1]
}

func tmo(secs int, name string, args ...string) *sh.Cmd {
	return sh.C(name, args...).Timeout(time.Duration(secs) * time.Second)
}

// atSock runs podman against a socket path, like CONTAINER_HOST=unix://$sock podman
func atSock(sock string, args ...string) *sh.Cmd {
	return sh.C("podman", args...).Env("CONTAINER_HOST=unix://" + sock)
}

// grepW is grep -w for a fixed word on any line
func grepW(text, word string) bool {
	if word == "" {
		return false
	}
	isW := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	for _, l := range strings.Split(text, "\n") {
		for i := 0; ; {
			j := strings.Index(l[i:], word)
			if j < 0 {
				break
			}
			s, e := i+j, i+j+len(word)
			if (s == 0 || !isW(l[s-1])) && (e == len(l) || !isW(l[e])) {
				return true
			}
			i = s + 1
		}
	}
	return false
}
