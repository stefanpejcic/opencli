// ##############################################################################
// Script Name: internal/commands/userreport/report.go
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
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-report",
		Desc:  "Prints a health report for a user: what is working and what needs attention.",
		Usage: "opencli user-report <USERNAME|--all> [--section <name,...>] [--json]",
		Run:   userReport,
	})
}

const (
	rpPanelConfig = "/etc/openpanel/openpanel/conf/openpanel.config"
	rpUsersCore   = "/etc/openpanel/openpanel/core/users"
	rpQuotaFile   = "/etc/openpanel/openpanel/quota_report.json"
	rpAllJSON     = "/etc/openpanel/openpanel/user_report_status.json"
)

// report order, each name maps to a section function
var rpAllSections = []string{"info", "account", "containers", "processes", "files", "backups", "domains", "dns", "ssl", "waf", "webserver", "php", "websites", "databases", "cache", "emails", "crons", "stats", "security", "activity"}

var rpSectionFuncs = map[string]func(*sec){
	"info": secInfo, "account": secAccount, "containers": secContainers, "processes": secProcesses,
	"files": secFiles, "backups": secBackups, "domains": secDomains, "dns": secDNS, "ssl": secSSL,
	"waf": secWAF, "webserver": secWebserver, "php": secPHP, "websites": secWebsites,
	"databases": secDatabases, "cache": secCache, "emails": secEmails, "crons": secCrons,
	"stats": secStats, "security": secSecurity, "activity": secActivity,
}

func rpUsage() {
	fmt.Printf(`Usage: opencli user-report <USERNAME|--all> [--section <name,...>] [--json]

Arguments:
    USERNAME              Report for a single user.
    --all                 Report for every user on the server.
    --section <list>      Only run these sections, comma separated.
    --json                Save the report as JSON instead of printing it:
                          a user to /home/<context>/user_report_status.json,
                          --all as one line per user to %s.

Sections:
    %s

Examples:
    opencli user-report stefan
    opencli user-report stefan --section info,files
    opencli user-report --all
    opencli user-report stefan --json
`, rpAllJSON, strings.Join(rpAllSections, " "))
	cli.Exit(1)
}

// report is the run wide state, what the script kept in globals
type report struct {
	target   string
	selected []string
	run      []string
	jsonMode bool
	anyError int

	cRed, cYellow, cGreen, cCyan, cBold, cDim, cReset string

	progress                                          bool
	pRed, pYellow, pGreen, pCyan, pDim, pBold, pReset string

	pconf    map[string]string
	serverIP string

	// server wide lookups started once
	gRootPS, gFacts, gQuota, gNS, gTLS, gLatest, gEmails, gAliases, gRejects, gWebmail *future

	rootPSOnce sync.Once
	rootPS     string
	factsOnce  sync.Once
	facts      map[string]string
	quotaOnce  sync.Once
	quotaTS    string
	quotaTSV   string
	emailOnce  sync.Once
	emailList  string
	aliasList  string
	rejects    string
	nsOnce     sync.Once
	nsConf     []string
	nsBroken   []string
	tlsOnce    sync.Once
	tlsErrors  string
	phpOnce    sync.Once
	phpLevels  map[string]string
	defSvcOnce sync.Once
	defSvc     string

	dbAll                                               bool
	dbUsers, dbPlans, dbDomains, dbSites, dbPass, dbMCP string

	reportPos  string
	allJSONTmp *os.File
}

// breadArray is `IFS=<ifs> read -ra arr <<< "$s"`
func breadArray(s, ifs string) []string {
	s = strings.SplitN(s, "\n", 2)[0]
	s = breadTrim(s, ifs)
	var out []string
	for s != "" {
		var w string
		w, s = breadWord(s, ifs)
		out = append(out, w)
	}
	return out
}

func userReport(args []string) int {
	db.Require()
	r := &report{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--all":
			r.target = "--all"
		case a == "--json":
			r.jsonMode = true
		case a == "--section" || a == "--sections":
			if cli.Arg(args, i+1) == "" {
				rpUsage()
			}
			r.selected = breadArray(args[i+1], ",")
			i++
		case strings.HasPrefix(a, "--section=") || strings.HasPrefix(a, "--sections="):
			r.selected = breadArray(a[strings.IndexByte(a, '=')+1:], ",")
		case a == "-h" || a == "--help":
			rpUsage()
		case strings.HasPrefix(a, "-"):
			fmt.Println("Unknown option: " + a)
			rpUsage()
		default:
			if r.target != "" {
				rpUsage()
			}
			r.target = a
		}
	}
	if r.target == "" {
		rpUsage()
	}
	all := " " + strings.Join(rpAllSections, " ") + " "
	for _, s := range r.selected {
		if !strings.Contains(all, " "+s+" ") {
			fmt.Println("Unknown section: " + s)
			rpUsage()
		}
	}

	// json keeps each section's text, so no colors in it
	if isTTY(os.Stdout) && !r.jsonMode {
		r.cRed, r.cYellow, r.cGreen, r.cCyan, r.cBold, r.cDim, r.cReset = "\033[31m", "\033[33m", "\033[32m", "\033[36m", "\033[1m", "\033[2m", "\033[0m"
	}
	// live progress on stderr, only when it's a terminal so cron, pipes and files get the plain report
	if isTTY(os.Stderr) {
		r.progress = true
		r.pRed, r.pYellow, r.pGreen, r.pCyan, r.pDim, r.pBold, r.pReset = "\033[31m", "\033[33m", "\033[32m", "\033[36m", "\033[2m", "\033[1m", "\033[0m"
	}

	r.loadPanelConfig()
	r.getServerIP()

	r.run = r.selected
	if len(r.run) == 0 {
		r.run = rpAllSections
	}

	// server wide lookups, started once in the background, sections wait for the ones they read
	r.gRootPS = goFuture(jobRootPS)
	r.gFacts = newFuture(r.jobServerFacts)
	r.gQuota = newFuture(jobQuota)
	r.gNS = newFuture(r.jobNS)
	r.gTLS = newFuture(jobTLS)
	r.gLatest = newFuture(jobLatest)
	r.gEmails = newFuture(jobEmailList)
	r.gAliases = newFuture(jobAliasList)
	r.gRejects = newFuture(jobMailRejects)
	r.gWebmail = newFuture(jobWebmail)
	if r.wants("info") {
		r.gFacts.start()
	}
	if r.wants("info") || r.wants("files") {
		r.gQuota.start()
	}
	if r.wants("dns") {
		r.gNS.start()
	}
	if r.wants("ssl") {
		r.gTLS.start()
	}
	if r.wants("websites") {
		r.gLatest.start()
	}
	if r.wants("emails") {
		r.gEmails.start()
		r.gAliases.start()
		r.gRejects.start()
		r.gWebmail.start()
	}

	if r.target == "--all" {
		// everyone's rows in one query per table, sections pick their user's rows from these
		r.dbPrefetchAll()
		var users []string
		for _, l := range strings.Split(r.dbUsers, "\n") {
			users = append(users, awkPart(l, "\t", 2))
		}
		if strings.Join(users, "\n") == "" {
			fmt.Println("No users.")
			return 0
		}
		total := countLines(strings.Join(users, "\n"))
		if r.jsonMode {
			f, err := os.Create(rpAllJSON + ".tmp")
			if err == nil {
				r.allJSONTmp = f
			}
		}
		for pos, u := range users {
			if strings.HasPrefix(u, "SUSPENDED_") {
				u = u[strings.LastIndexByte(u, '_')+1:]
			}
			r.reportPos = fmt.Sprintf("%d of %d users", pos+1, total)
			r.reportUser(u)
		}
		if r.jsonMode {
			if r.allJSONTmp != nil {
				r.allJSONTmp.Close()
			}
			if os.Rename(rpAllJSON+".tmp", rpAllJSON) == nil {
				os.Chmod(rpAllJSON, 0o644)
			}
			fmt.Printf("Reports for %d user(s) saved to %s\n", countLines(readFile(rpAllJSON)), rpAllJSON)
		}
	} else {
		r.reportUser(r.target)
	}
	return r.anyError
}

func (r *report) wants(s string) bool {
	return strings.Contains(" "+strings.Join(r.run, " ")+" ", " "+s+" ")
}

func readFile(p string) string { b, _ := os.ReadFile(p); return string(b) }

// sectionRun is one section's output, what the script left in <tmp>/<i>.*
type sectionRun struct {
	name   string
	out    string
	hasOut bool
	rows   string
	issues string
	ms     string
	done   bool
}

func (r *report) reportUser(name string) {
	u := r.loadUser(name)
	if u == nil {
		fmt.Printf("User %s not found.\n", name)
		r.anyError = 1
		return
	}
	r.userDataReset(u)
	u.loadEnv()
	if r.progress {
		pos := r.reportPos
		if pos == "" {
			pos = "1 user"
		}
		fmt.Fprintf(os.Stderr, "%sChecking %s%s %s(%s)%s\n", r.pBold, u.name, r.pReset, r.pDim, pos, r.pReset)
	}
	tUser := time.Now()

	sections := r.selected
	if len(sections) == 0 {
		sections = rpAllSections
	}

	// this user's slow lookups start now, next to the server-wide ones started once at the top
	u.fPS.start()
	if r.wants("files") || r.wants("domains") {
		u.fDU.start()
	}

	// every section runs in the background and waits only for the data it reads, joined in report order
	runs := make([]*sectionRun, len(sections))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, s := range sections {
		runs[i] = &sectionRun{name: s}
		fn, ok := rpSectionFuncs[s]
		if !ok {
			if len(r.selected) > 0 {
				runs[i].out = fmt.Sprintf("\n%s== %s ==%s\n", r.cBold, strings.ToUpper(s), r.cReset) + "  " + r.cDim + "Not available yet." + r.cReset + "\n"
				runs[i].hasOut = true
			}
			runs[i].done = true
			continue
		}
		wg.Add(1)
		go func(sr *sectionRun) {
			defer wg.Done()
			t := time.Now()
			x := &sec{r: r, u: u}
			func() {
				// a broken section just ends early, like a failing subshell did
				defer func() { recover() }()
				fn(x)
			}()
			mu.Lock()
			sr.out, sr.hasOut = x.buf.String(), true
			sr.rows = x.rows.String()
			sr.issues = x.issues.String()
			sr.ms = fmt.Sprint(time.Since(t).Milliseconds())
			sr.done = true
			mu.Unlock()
		}(runs[i])
	}
	if r.progress {
		r.progressWait(runs, &mu)
	}
	wg.Wait()
	if r.progress {
		fmt.Fprintf(os.Stderr, "  %sDone in %.1fs, report below.%s\n\n", r.pDim, float64(time.Since(tUser).Milliseconds())/1000, r.pReset)
	}
	var issues []string
	for _, sr := range runs {
		issues = append(issues, nonEmptyLines(sr.issues)...)
	}

	if r.jsonMode {
		ms := time.Since(tUser).Milliseconds()
		obj := r.reportJSON(u, runs, sections, ms, issues)
		if r.target == "--all" {
			if r.allJSONTmp != nil {
				r.allJSONTmp.WriteString(ojson.Compact(obj) + "\n")
			}
		} else {
			// written next to the final file and moved, so a reader never sees half a report
			out := "/home/" + u.context + "/user_report_status.json"
			if os.WriteFile(out+".tmp", []byte(ojson.Pretty(obj)+"\n"), 0o644) == nil && os.Rename(out+".tmp", out) == nil {
				os.Chmod(out, 0o644)
				fmt.Printf("Report for %s saved to %s\n", u.name, out)
			} else {
				os.Remove(out + ".tmp")
				fmt.Printf("Couldn't write %s\n", out)
				r.anyError = 1
			}
		}
		for _, e := range issues {
			if strings.HasPrefix(e, "error\t") {
				r.anyError = 1
			}
		}
		return
	}

	fmt.Printf("%s################################################################################%s\n", r.cBold, r.cReset)
	fmt.Printf("%s Report for %s, generated %s%s\n", r.cBold, u.name, time.Now().Format("2006-01-02 15:04:05 MST"), r.cReset)
	fmt.Printf("%s################################################################################%s\n", r.cBold, r.cReset)
	r.printSummary(issues)
	for _, sr := range runs {
		if sr.hasOut {
			fmt.Print(sr.out)
		}
	}
	fmt.Println()
}

// nonEmptyLines is what `while IFS= read -r line` collects from a file
func nonEmptyLines(s string) []string { return readLines(s) }

func (r *report) printSummary(issues []string) {
	errors, warnings, infos := 0, 0, 0
	for _, l := range issues {
		switch strings.SplitN(l, "\t", 2)[0] {
		case "error":
			errors++
		case "warning":
			warnings++
		case "info":
			infos++
		}
	}
	fmt.Printf("\n%s== %s ==%s\n", r.cBold, "SUMMARY", r.cReset)
	fmt.Printf("  %s%d error(s)%s, %s%d warning(s)%s, %s%d info%s\n", r.cRed, errors, r.cReset, r.cYellow, warnings, r.cReset, r.cCyan, infos, r.cReset)
	if len(issues) == 0 {
		fmt.Printf("  %s[OK]%s    %s\n", r.cGreen, r.cReset, "No problems found.")
		return
	}
	fmt.Println()
	// errors first, then warnings, then info
	for _, want := range []string{"error", "warning", "info"} {
		for _, l := range issues {
			f := bread(l, "\t", 3)
			if f[0] != want {
				continue
			}
			switch f[0] {
			case "error":
				fmt.Printf("  %s%s%s %s: %s\n", r.cRed, padR("[ERROR]", 7), r.cReset, f[1], f[2])
			case "warning":
				fmt.Printf("  %s%s%s %s: %s\n", r.cYellow, padR("[WARN]", 7), r.cReset, f[1], f[2])
			case "info":
				fmt.Printf("  %s%s%s %s: %s\n", r.cCyan, padR("[INFO]", 7), r.cReset, f[1], f[2])
			}
		}
	}
	if errors > 0 {
		r.anyError = 1
	}
}

// tsvRows is jq's `split("\n") | map(select(length > 0) | split("\t"))`
func tsvRows(s string) [][]string {
	var out [][]string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, strings.Split(l, "\t"))
		}
	}
	return out
}

func idx(a []string, i int) any {
	if i < len(a) {
		return a[i]
	}
	return nil
}

// reportJSON is one user's report as an object, built from what every section left behind
func (r *report) reportJSON(u *ruser, runs []*sectionRun, secs []string, ms int64, _ []string) *ojson.Object {
	parts := []any{}
	var all []*ojson.Object
	for i, sr := range runs {
		if !sr.hasOut {
			continue
		}
		title := strings.ToUpper(secs[i])
		if m := titleRe.FindStringSubmatch(sr.out); m != nil {
			title = m[1]
		}
		var dur any
		if n, ok := jqToNumber(sr.ms); ok {
			dur = n
		}
		// rows with an empty label continue the value above them
		rows := []any{}
		for _, rr := range tsvRows(sr.rows) {
			val := ""
			if v, ok := idx(rr, 2).(string); ok {
				val = v
			}
			if idx(rr, 1) == "" && len(rows) > 0 {
				last := rows[len(rows)-1].(*ojson.Object)
				last.Set("value", last.Str("value")+"\n"+val)
				continue
			}
			rows = append(rows, ojson.New().Set("group", idx(rr, 0)).Set("label", idx(rr, 1)).Set("value", val))
		}
		issues := []any{}
		for _, ir := range tsvRows(sr.issues) {
			msg := ""
			if len(ir) > 2 {
				msg = strings.Join(ir[2:], "\t")
			}
			o := ojson.New().Set("severity", idx(ir, 0)).Set("section", idx(ir, 1)).Set("message", msg)
			issues = append(issues, o)
			all = append(all, o)
		}
		parts = append(parts, ojson.New().Set("key", secs[i]).Set("title", title).Set("duration_ms", dur).
			Set("rows", rows).Set("issues", issues).Set("text", sr.out))
	}
	count := func(sev string) []any {
		out := []any{}
		for _, o := range all {
			if o.Str("severity") == sev {
				out = append(out, o)
			}
		}
		return out
	}
	errs, warns, infos := count("error"), count("warning"), count("info")
	sorted := append(append(append([]any{}, errs...), warns...), infos...)
	return ojson.New().
		Set("username", u.name).Set("context", u.context).Set("owner", u.owner).Set("plan", u.plan).
		Set("suspended", u.suspended != "").
		Set("generated_at", time.Now().UTC().Format("2006-01-02T15:04:05Z")).Set("duration_ms", ms).
		Set("summary", ojson.New().Set("errors", len(errs)).Set("warnings", len(warns)).Set("info", len(infos))).
		Set("issues", sorted).
		Set("sections", parts)
}

// jqToNumber is jq's tonumber on a string
func jqToNumber(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	if text.IsInt(s) {
		return jsonNum(s), true
	}
	return nil, false
}

func termCols() int {
	var ws struct{ row, col, x, y uint16 }
	for _, f := range []*os.File{os.Stdout, os.Stderr, os.Stdin} {
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); e == 0 && ws.col > 0 {
			return int(ws.col)
		}
	}
	return 100
}

// progressDone is one line per finished section with its time, counts and the worst thing it found
func (r *report) progressDone(name, ms, issues string) {
	e, w, n := 0, 0, 0
	firstErr, firstWarn := "", ""
	for _, l := range strings.Split(issues, "\n") {
		switch {
		case strings.HasPrefix(l, "error"):
			if e == 0 {
				firstErr = l
			}
			e++
		case strings.HasPrefix(l, "warning"):
			if w == 0 {
				firstWarn = l
			}
			w++
		case strings.HasPrefix(l, "info"):
			n++
		}
	}
	first := firstErr
	if first == "" {
		first = firstWarn
	}
	first = awkPart(first, "\t", 3)
	found := ""
	plural := func(k int) string {
		if k > 1 {
			return "s"
		}
		return ""
	}
	if e > 0 {
		found += fmt.Sprintf("%s%d error%s%s ", r.pRed, e, plural(e), r.pReset)
	}
	if w > 0 {
		found += fmt.Sprintf("%s%d warning%s%s ", r.pYellow, w, plural(w), r.pReset)
	}
	if n > 0 {
		found += fmt.Sprintf("%s%d info%s ", r.pCyan, n, r.pReset)
	}
	if found == "" {
		found = r.pGreen + "ok" + r.pReset + " "
	}
	cols := termCols()
	mark := r.pGreen + "✓" + r.pReset
	if e > 0 {
		mark = r.pRed + "✗" + r.pReset
	}
	if e == 0 && w > 0 {
		mark = r.pYellow + "!" + r.pReset
	}
	msf := float64(text.Atoi(ms)) / 1000
	fmt.Fprintf(os.Stderr, "\r\033[K  %s %s %s%5.1fs%s  %s", mark, padR(name, 11), r.pDim, msf, r.pReset, found)
	fmt.Fprint(os.Stderr, "\n")
	// the most serious finding on its own line, cut to the terminal width
	if first != "" {
		max := 13
		if cols > 20 {
			max = cols - 7
		}
		fmt.Fprintf(os.Stderr, "      %s%s%s\n", r.pDim, runeCut(first, max), r.pReset)
	}
}

// progressWait prints each section as it finishes and a status line with what's still running
func (r *report) progressWait(runs []*sectionRun, mu *sync.Mutex) {
	shown := make([]bool, len(runs))
	done, k := 0, 0
	spin := `|/-\`
	t0 := time.Now()
	for done < len(runs) {
		for i, sr := range runs {
			if shown[i] {
				continue
			}
			mu.Lock()
			d, ms, iss := sr.done, sr.ms, sr.issues
			mu.Unlock()
			if !d {
				continue
			}
			shown[i] = true
			done++
			if ms != "" {
				r.progressDone(sr.name, ms, iss)
			}
		}
		if done >= len(runs) {
			break
		}
		running := ""
		for i, sr := range runs {
			if !shown[i] {
				running += sr.name + " "
			}
		}
		fmt.Fprintf(os.Stderr, "\r\033[K%s", fmt.Sprintf("  %s%c%s %d/%d done %s%ds, checking: %s%s", r.pCyan, spin[k%4], r.pReset, done, len(runs), r.pDim, int(time.Since(t0).Seconds()), strings.TrimSuffix(running, " "), r.pReset))
		k++
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Fprint(os.Stderr, "\r\033[K")
}
