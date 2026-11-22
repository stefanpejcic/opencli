// ##############################################################################
// Script Name: internal/commands/userreport/report_db.go
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
	"strings"
	"time"

	"opencli/internal/ojson"
	"opencli/internal/sh"
)

// umysql is sql against the user's own mysql/mariadb, --defaults-file so /etc/my.cnf's tcp host isn't used
func (u *ruser) umysql(q string) string {
	return sh.C("mariadb", "--defaults-file=/home/"+u.context+"/my.cnf", "--socket=/home/"+u.context+"/sockets/mysqld/mysqld.sock", "-N", "-B", "-e", q).
		Timeout(5 * time.Second).NoErr().Str()
}

// awkNFm1 is awk -F: '{ print $(NF - 1) }'
func awkNFm1(s string) string {
	if s == "" {
		return ""
	}
	f := strings.Split(s, ":")
	if len(f) == 1 {
		return s
	}
	return f[len(f)-2]
}

// remoteState is "on, port 32770" or "off" from a .env port mapping like 127.0.0.1:32770:3306
func remoteState(m string) string {
	if strings.HasPrefix(m, "127.0.0.1:") {
		return "off"
	}
	return "on, port " + awkNFm1(m)
}

// remoteConns lists the client ips with established connections to a host port
func remoteConns(port string) string {
	if !reDigits.MatchString(port) {
		return ""
	}
	out := sh.C("ss", "-Htn", "state", "established", "( sport = :"+port+" )").NoErr().Str()
	var ips []string
	for _, l := range nonEmpty(out) {
		f := strings.Fields(l)
		ip := ""
		if len(f) >= 4 {
			ip = regexp.MustCompile(`:[0-9]+$`).ReplaceAllString(f[3], "")
		}
		ips = append(ips, ip)
	}
	if len(ips) == 0 {
		return ""
	}
	var parts []string
	for i, c := range uniqCSorted(ips) {
		sep := ""
		if i > 0 {
			sep = ", "
		}
		parts = append(parts, fmt.Sprintf("%s%s (%d)", sep, fieldN(c[1], 1), bashInt(c[0])))
	}
	return strings.Join(parts, "")
}

// part is the lines between ##NAME and the next ## marker of a batched query output
func part(name, out string) string {
	var res []string
	f := false
	for _, l := range strings.Split(out, "\n") {
		if l == "##"+name {
			f = true
			continue
		}
		if strings.HasPrefix(l, "##") {
			f = false
		}
		if f {
			res = append(res, l)
		}
	}
	return strings.Join(res, "\n")
}

// every mysql query the section needs in one connection
func (u *ruser) jobMysqlAll() string {
	r := u.r
	rdbs := restrictedList(r.panelConfig("mysql_restricted_databases", "information_schema performance_schema mysql phpmyadmin sys mariadb.sys"))
	rusers := restrictedList(r.panelConfig("mysql_restricted_usernames", "mysql.sys mysql sys mariadb.sys phpmyadmin mysql.session mysql.infoschema root debian-sys-maint healthcheck percona.telemetry"))
	return u.umysql(`SELECT '##VERSION'; SELECT VERSION();
        SELECT '##DBS';
        SELECT s.schema_name, IFNULL(SUM(t.data_length + t.index_length), 0), IFNULL(SUM(t.data_free), 0), COUNT(t.table_name)
            FROM information_schema.schemata s LEFT JOIN information_schema.tables t ON t.table_schema = s.schema_name
            WHERE s.schema_name NOT IN (` + rdbs + `) GROUP BY s.schema_name ORDER BY s.schema_name;
        SELECT '##USERS';
        SELECT REPLACE(Db, '\\_', '_'), GROUP_CONCAT(DISTINCT User SEPARATOR ', ') FROM mysql.db WHERE User NOT LIKE 'mysql.s%' AND User NOT IN (` + rusers + `) GROUP BY Db;
        SELECT '##UCOUNT';
        SELECT COUNT(DISTINCT User) FROM mysql.user WHERE User NOT LIKE 'mysql.s%' AND User NOT IN (` + rusers + `);
        SELECT '##SLOW';
        SELECT ID, USER, IFNULL(DB, '-'), TIME, LEFT(REPLACE(IFNULL(INFO, ''), '\n', ' '), 60) FROM information_schema.processlist
            WHERE COMMAND NOT IN ('Sleep', 'Daemon', 'Binlog Dump') AND INFO IS NOT NULL AND ID != CONNECTION_ID() ORDER BY TIME DESC LIMIT 5;`)
}

// every postgres query in one exec
func (u *ruser) jobPgAll() string {
	return u.pod(5*time.Second, "exec", "postgres", "psql", "-U", u.userEnv("POSTGRES_USER"), "-At", "-F", "\t",
		"-c", "SELECT '##VERSION'", "-c", "SHOW server_version",
		"-c", "SELECT '##DBS'", "-c", "SELECT datname, pg_database_size(datname), pg_get_userbyid(datdba) FROM pg_database WHERE NOT datistemplate AND datname != 'postgres' ORDER BY 1",
		"-c", "SELECT '##ACTIVE'", "-c", "SELECT pid, usename, datname, EXTRACT(EPOCH FROM now() - query_start)::int, LEFT(query, 60) FROM pg_stat_activity WHERE state = 'active' AND pid != pg_backend_pid() ORDER BY 4 DESC LIMIT 5").Str()
}

func (u *ruser) jobMongo() string {
	return u.pod(5*time.Second, "exec", "mongodb", "mongosh", "--quiet", "--norc", "-u", u.userEnv("MONGODB_ROOT_USER"), "-p", u.userEnv("MONGODB_ROOT_PASSWORD"), "--authenticationDatabase", "admin",
		"--eval", `const l = db.adminCommand({listDatabases: 1}); print(db.version()); l.databases.filter(d => !["admin","config","local"].includes(d.name)).forEach(d => print(d.name + "\t" + d.sizeOnDisk))`).Str()
}

func (u *ruser) dblog(name string) *future {
	return u.fut("dblog "+name, func() string {
		out, _ := u.pod(5*time.Second, "logs", "--since", "24h", "--tail", "3000", name).Combined()
		return out
	})
}

func lineIn(list, v string) bool {
	for _, l := range strings.Split(list, "\n") {
		if l == v {
			return true
		}
	}
	return false
}

func (s *sec) remoteRows(port string) {
	if strings.HasPrefix(port, "127.0.0.1:") {
		return
	}
	p := awkNFm1(port)
	s.row("Remote address", s.r.serverIP+":"+p)
	conns := remoteConns(p)
	s.row("Remote connections", or(conns, "none"))
}

func secDatabases(s *sec) {
	u, r := s.u, s.r
	s.section("DATABASES")
	u.needPS()
	running := u.running

	// mysql, postgres and mongo are asked at the same time, each capped at 5s
	mtype := u.userEnv("MYSQL_TYPE")
	mname := or(mtype, "mysql")
	myOn := lineIn(running, mname) && isSocket("/home/"+u.context+"/sockets/mysqld/mysqld.sock")
	pgOn := lineIn(running, "postgres")
	mgOn := lineIn(running, "mongodb")
	fMy := u.fut("mysql_all", u.jobMysqlAll)
	fPg := u.fut("pg_all", u.jobPgAll)
	fMg := u.fut("mongo", u.jobMongo)
	if myOn {
		fMy.start()
		u.dblog(mname).start()
	}
	if pgOn {
		fPg.start()
		u.dblog("postgres").start()
	}
	if mgOn {
		fMg.start()
		u.dblog("mongodb").start()
	}

	s.subsection(strings.ToUpper(mname))
	mport := u.userEnv("MYSQL_PORT")
	if !myOn {
		s.row("Status", "not running (starts when the Databases page is opened)")
	} else {
		my := fMy.get()
		if my == "" {
			s.warn(mname + " didn't answer in 5s.")
		}
		s.row("Version", part("VERSION", my))
		s.row("Host for websites", "localhost (socket) or "+mname+":3306")
		s.row("Remote access", remoteState(mport))
		s.remoteRows(mport)

		// same filters as the Databases page, applied in jobMysqlAll
		dbs, users := part("DBS", my), part("USERS", my)
		s.row("Users", part("UCOUNT", my))
		if dbs == "" {
			s.row("Databases", "none")
		} else {
			s.p("  %s%s %s %s %s %s%s\n", r.cDim, padR("Database", 30), padR("Size", 10), padR("Tables", 8), padR("Reclaimable", 12), "Users", r.cReset)
			var nouser, frag []string
			for _, l := range hereLines(dbs) {
				f := bread(l, "\t", 4)
				db, size, free, tables := f[0], f[1], f[2], f[3]
				var who []string
				for _, ul := range strings.Split(users, "\n") {
					uf := strings.Split(ul, "\t")
					if uf[0] == db {
						who = append(who, idxS(uf, 1))
					}
				}
				w := strings.Join(who, "\n")
				fr := "-"
				if bashInt(free) > 0 {
					fr = humanBytes(free)
				}
				s.p("  %s %s %s %s %s\n", padR(db, 30), padR(humanBytes(size), 10), padR(tables, 8), padR(fr, 12), or(w, "-"))
				if w == "" {
					nouser = append(nouser, db)
				}
				// worth an OPTIMIZE when over 50MB and a fifth of the size is free space
				if bashInt(free) > 52428800 && bashInt(free)*5 > bashInt(size) {
					frag = append(frag, db+" ("+humanBytes(free)+")")
				}
			}
			// same wording as the Databases page toast
			switch len(nouser) {
			case 0:
			case 1:
				s.warn("Database " + nouser[0] + " has no users assigned.")
			case 2:
				s.warn("Databases " + nouser[0] + ", " + nouser[1] + " have no users assigned.")
			default:
				s.warn(fmt.Sprintf("%d databases have no users assigned.", len(nouser)))
			}
			if len(frag) > 0 {
				s.info("Optimizing these would free space: " + strings.Join(frag, " ") + ".")
			}
		}
		slow := part("SLOW", my)
		s.row("Running queries", fmt.Sprint(countLines(slow)))
		if slow != "" {
			for _, l := range hereLines(slow) {
				f := bread(l, "\t", 5)
				s.p("    %ss  %s@%s  %s\n", f[3], f[1], f[2], f[4])
				if bashInt(f[3]) >= 60 {
					s.warn("Query " + f[0] + " on " + f[2] + " has been running for " + f[3] + "s.")
				}
			}
		}
		s.logErrors(mname)
	}

	s.subsection("POSTGRESQL")
	if !pgOn {
		s.row("Status", "not running")
	} else {
		pg := fPg.get()
		if pg == "" {
			s.warn("PostgreSQL didn't answer in 5s.")
		}
		pport := u.userEnv("POSTGRES_PORT")
		s.row("Version", part("VERSION", pg))
		s.row("Host for websites", "postgres:5432")
		s.row("Remote access", remoteState(pport))
		s.remoteRows(pport)
		pdbs := part("DBS", pg)
		if pdbs == "" {
			s.row("Databases", "none")
		} else {
			s.p("  %s%s %s %s%s\n", r.cDim, padR("Database", 30), padR("Size", 10), "Owner", r.cReset)
			for _, l := range hereLines(pdbs) {
				f := bread(l, "\t", 3)
				s.p("  %s %s %s\n", padR(f[0], 30), padR(humanBytes(f[1]), 10), f[2])
			}
		}
		active := part("ACTIVE", pg)
		s.row("Running queries", fmt.Sprint(countLines(active)))
		if active != "" {
			for _, l := range hereLines(active) {
				f := bread(l, "\t", 5)
				s.p("    %ss  %s@%s  %s\n", f[3], f[1], f[2], f[4])
				if bashInt(f[3]) >= 60 {
					s.warn("PostgreSQL query " + f[0] + " on " + f[2] + " has been running for " + f[3] + "s.")
				}
			}
		}
		s.logErrors("postgres")
	}

	s.subsection("MONGODB")
	if !mgOn {
		s.row("Status", "not running")
	} else {
		out := fMg.get()
		if out == "" {
			s.row("Status", "running, but didn't answer")
			s.warn("MongoDB didn't answer in 5s with the root login from .env.")
		} else {
			lines := strings.Split(out, "\n")
			s.row("Version", lines[0])
			s.row("Host for websites", "mongodb:27017")
			mdbs := strings.Join(lines[1:], "\n")
			if mdbs == "" {
				s.row("Databases", "none")
			} else {
				s.p("  %s%s %s%s\n", r.cDim, padR("Database", 30), "Size", r.cReset)
				for _, l := range hereLines(mdbs) {
					f := bread(l, "\t", 2)
					s.p("  %s %s\n", padR(f[0], 30), humanBytes(f[1]))
				}
			}
		}
		s.logErrors("mongodb")
	}
}

var (
	dbErrRe   = regexp.MustCompile(`(?i)\[ERROR\]|FATAL|PANIC|"s":"E"`)
	dbFatalRe = regexp.MustCompile(`(?i)FATAL|PANIC|crash|corrupt`)
	dbStrip   = []*regexp.Regexp{regexp.MustCompile(`^[0-9-]+[ T][0-9:.+Z-]+ +`), regexp.MustCompile(`^[0-9]+ `), regexp.MustCompile(`^[A-Z]{3,4} \[[0-9]+\] `)}
)

// logErrors lists error lines of a database container in the last 24h, most common first
func (s *sec) logErrors(name string) {
	logs := trimNL(s.u.dblog(name).get())
	errs := grepLines(logs, dbErrRe.MatchString)
	s.row("Log errors (24h)", fmt.Sprint(len(errs)))
	if len(errs) == 0 {
		return
	}
	var st []string
	for _, l := range errs {
		for _, re := range dbStrip {
			l = sedFirst(re, l, "")
		}
		st = append(st, l)
	}
	s.topCounts(st, 100, 3)
	if dbFatalRe.MatchString(strings.Join(errs, "\n")) {
		s.warn(name + " logged fatal errors in the last 24h, check its log.")
	}
}

var errWords = []string{"error", "fatal", "failed", "denied", "refused", "exception", "panic", "critical"}

func isWordByte(c byte) bool {
	return c == '-' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// errWordLine is grep -iP '(?<![-_\w])(error|fatal|...)(?![-_\w])'
func errWordLine(l string) bool {
	low := strings.ToLower(l)
	for _, w := range errWords {
		for i := 0; ; {
			j := strings.Index(low[i:], w)
			if j < 0 {
				break
			}
			j += i
			if (j == 0 || !isWordByte(low[j-1])) && (j+len(w) == len(low) || !isWordByte(low[j+len(w)])) {
				return true
			}
			i = j + 1
		}
	}
	return false
}

func (u *ruser) svclog(svc string) *future {
	return u.fut("svclog "+svc, func() string {
		// not inside names like bf-error-rate or error_log
		out, _ := u.pod(5*time.Second, "logs", "--since", "24h", "--tail", "3000", svc).Combined()
		return strings.Join(grepLines(out, errWordLine), "\n")
	})
}

// cacheinfo is what a cache or search service reports about itself, 5s at most
func (u *ruser) cacheinfo(svc string) *future {
	return u.fut("cacheinfo "+svc, func() string {
		var out string
		switch svc {
		case "redis":
			out, _ = u.pod(5*time.Second, "exec", "redis", "redis-cli", "info").Raw()
		case "valkey":
			var err error
			out, err = u.pod(5*time.Second, "exec", "valkey", "valkey-cli", "info").Raw()
			if err != nil {
				o2, _ := u.pod(5*time.Second, "exec", "valkey", "redis-cli", "info").Raw()
				out += o2
			}
		case "memcached":
			out, _ = u.pod(5*time.Second, "exec", "memcached", "sh", "-c", "echo stats | nc -w1 127.0.0.1 11211").Raw()
		default:
			out, _ = u.pod(5*time.Second, "exec", svc, "curl", "-s", "--max-time", "4", "localhost:9200/_cluster/health").Raw()
		}
		return trimNL(strings.ReplaceAll(out, "\r", ""))
	})
}

func grepOP(re *regexp.Regexp, s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		for _, m := range re.FindAllStringSubmatch(l, -1) {
			out = append(out, m[1])
		}
	}
	return strings.Join(out, "\n")
}

var (
	usedMemRe = regexp.MustCompile(`^used_memory_human:(.*)`)
	maxMemRe  = regexp.MustCompile(`^maxmemory_human:(.*)`)
	evictedRe = regexp.MustCompile(`^evicted_keys:(.*)`)
	dbKeysRe  = regexp.MustCompile(`^db\d+:keys=(\d+)`)
	mcBytes   = regexp.MustCompile(`STAT bytes (\d+)`)
	mcLimit   = regexp.MustCompile(`STAT limit_maxbytes (\d+)`)
	mcItems   = regexp.MustCompile(`STAT curr_items (\d+)`)
)

func secCache(s *sec) {
	u, r := s.u, s.r
	s.section("CACHE & SEARCH")
	defined := u.needServices()
	u.needPS()
	var running []string
	for _, l := range strings.Split(u.ps, "\n") {
		f := strings.Split(l, "\t")
		running = append(running, strings.Join(f[:min(3, len(f))], "\t"))
	}
	svcs := []string{"redis", "valkey", "memcached", "elasticsearch", "opensearch"}
	isRunning := func(svc string) bool {
		for _, l := range running {
			if strings.HasPrefix(l, svc+"\trunning\t") {
				return true
			}
		}
		return false
	}
	// every running service asked at once
	for _, svc := range svcs {
		if lineIn(defined, svc) && isRunning(svc) {
			u.cacheinfo(svc).start()
			u.svclog(svc).start()
		}
	}

	s.p("  %s%s %s %s %s%s\n", r.cDim, padR("Service", 15), padR("Status", 30), padR("Host for websites", 22), "Details", r.cReset)
	for _, svc := range svcs {
		port := "9200"
		switch svc {
		case "redis", "valkey":
			port = "6379"
		case "memcached":
			port = "11211"
		}
		if !lineIn(defined, svc) {
			continue
		}
		var state, status string
		for _, l := range running {
			if strings.Split(l, "\t")[0] == svc {
				f := bread(l, "\t", 3)
				state, status = f[1], f[2]
				break
			}
		}
		details := ""
		if state != "running" {
			s.p("  %s %s %s %s\n", padR(svc, 15), padR(or(state, "not started"), 30), padR(svc+":"+port, 22), "-")
			if state != "" && state != "created" {
				s.warn(svc + " is " + state + ", apps using it get connection errors.")
			}
			continue
		}
		switch svc {
		case "redis", "valkey":
			inf := u.cacheinfo(svc).get()
			used, max, evicted := grepOP(usedMemRe, inf), grepOP(maxMemRe, inf), grepOP(evictedRe, inf)
			keys := ""
			if k := grepOP(dbKeysRe, inf); k != "" {
				var sum int64
				for _, x := range strings.Split(k, "\n") {
					sum += bashInt(x)
				}
				keys = fmt.Sprint(sum)
			}
			details = or(used, "?") + " used, " + or(keys, "0") + " keys"
			if max != "" && max != "0B" {
				details += ", max " + max
			}
			if bashInt(evicted) > 0 {
				details += ", " + evicted + " evicted"
			}
		case "memcached":
			st := u.cacheinfo("memcached").get()
			details = humanBytes(grepOP(mcBytes, st)) + " of " + humanBytes(grepOP(mcLimit, st)) + ", " + grepOP(mcItems, st) + " items"
		default:
			health := u.cacheinfo(svc).get()
			cs := ""
			if o, err := ojson.ParseObject(health); err == nil {
				if v, _ := o.Get("status"); v != nil && v != false {
					cs = ojson.Text(v)
				}
			}
			details = "cluster " + or(cs, "unknown")
			if cs == "red" {
				s.err(svc + " cluster is red, some indexes are unavailable.")
			}
			if cs == "" {
				s.warn(svc + " didn't answer on port 9200 in 5s.")
			}
		}
		s.p("  %s %s %s %s\n", padR(svc, 15), padR(runeCut(status, 30), 30), padR(svc+":"+port, 22), details)
		if strings.Contains(status, "unhealthy") {
			s.warn(svc + " is unhealthy.")
		}
		// same check as the Services page log button
		if errs := u.svclog(svc).get(); errs != "" {
			l := strings.Split(errs, "\n")
			s.warn(fmt.Sprintf("Service %s log contains %d error line(s), last: %s", svc, countLines(errs), cutC(l[len(l)-1], 120)))
		}
	}
	dir := "/home/" + u.context + "/sockets"
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		socks, _ := filepath.Glob(globQuote(dir) + "/*/*.sock")
		var keep []string
		for _, p := range socks {
			rel := strings.TrimPrefix(p, dir+"/")
			if regexp.MustCompile(`^(redis|valkey|memcached)/`).MatchString(rel) {
				keep = append(keep, rel)
			}
		}
		if len(keep) > 0 {
			s.row("Sockets", strings.Join(keep, " ")+" (in /home/"+u.context+"/sockets)")
		}
	}
}

var (
	acctRe  = regexp.MustCompile(`^\* ([^ ]+) \( ([^ ]+) / ([^ ]+) \) \[([0-9]+)%\].*`)
	aliasRe = regexp.MustCompile(`^\* ([^ ]+) (.*)`)
	sieveIf = regexp.MustCompile(`^\s*(if|elsif)\b`)
	sieveNo = regexp.MustCompile(`^\s*if true \{ stop; \}\s*$`)
	wmRe    = regexp.MustCompile(`roundcube|snappymail|sogo`)
	numRe   = regexp.MustCompile(`^[0-9.]+$`)
)

func hostnameF() string { return sh.C("hostname", "-f").NoErr().Str() }

func secEmails(s *sec) {
	u, r := s.u, s.r
	s.section("EMAILS")
	if u.domains == "" {
		s.row("Email", "no domains")
		return
	}
	// only addresses on this user's domains, the mailserver lists every account on the server
	var alts []string
	for _, d := range strings.Split(u.domains, "\n") {
		alts = append(alts, strings.ReplaceAll(d, ".", `\.`))
	}
	domRe, rerr := regexp.Compile("@(" + strings.Join(alts, "|") + ")$")
	match := func(x string) bool { return rerr == nil && domRe.MatchString(x) }

	r.needRootPS()
	r.needEmailData()
	ms := r.rootState("openadmin_mailserver")
	s.row("Mail server", or(ms, "not installed"))
	if ms != "running" {
		if r.modulesHave("emails") {
			s.err("Mail server isn't running, no email is sent or received.")
		}
		return
	}
	web := r.gWebmail.get()
	if numRe.MatchString(web) {
		web = "http://" + web + ":8080/"
	} else if web != "" && !strings.Contains(web, "Community") {
		web = "https://" + web + "/"
	}
	wstate := ""
	for _, l := range strings.Split(r.rootPS, "\n") {
		f := strings.Split(l, "\t")
		if idxS(f, 1) == "running" && wmRe.MatchString(strings.ToLower(f[0])) {
			wstate = f[0]
			break
		}
	}
	wv := or(web, "unknown")
	if wstate != "" {
		wv += " (" + wstate + " running)"
	}
	s.row("Webmail", wv)
	if wstate == "" {
		s.warn("No webmail container is running, users can't open webmail.")
	}
	host := hostnameF()
	s.row("Incoming (IMAP/POP3)", host+": 993 (IMAP SSL), 995 (POP3 SSL)")
	s.row("Outgoing (SMTP)", host+": 465 (SSL), 587 (STARTTLS)")

	// lines look like: * user@example.com ( 950M / 1G ) [95%], unlimited quota shows as ~
	var accounts [][]string
	for _, l := range strings.Split(r.emailList, "\n") {
		if m := acctRe.FindStringSubmatch(l); m != nil && match(m[1]) {
			accounts = append(accounts, m[1:5])
		}
	}

	s.subsection("ACCOUNTS")
	n := 0
	if len(accounts) == 0 {
		s.row("Accounts", "0")
	} else {
		n = len(accounts)
		s.p("  %s%s %s %s %s%s\n", r.cDim, padR("Address", 40), padR("Used", 10), padR("Quota", 10), "Filters", r.cReset)
		base := grepOP(regexp.MustCompile(`^email_storage_location=(.*)`), readFile("/etc/openpanel/openadmin/config/admin.ini"))
		base = or(base, "/var/mail")
		var full []string
		for _, a := range accounts {
			addr, used, quota, pct := a[0], a[1], a[2], a[3]
			at := strings.LastIndexByte(addr, '@')
			sieve := strings.TrimSuffix(base, "/") + "/" + addr[at+1:] + "/" + addr[:at] + "/home/.dovecot.sieve"
			// the default "if true { stop; }" placeholder isn't a filter
			filt := bashInt(grepCount(sieve, sieveIf.MatchString))
			if grepCount(sieve, sieveNo.MatchString) != "" && bashInt(grepCount(sieve, sieveNo.MatchString)) > 0 {
				filt--
			}
			if filt < 0 {
				filt = 0
			}
			q := quota
			if quota == "~" {
				q = "unlimited"
			}
			s.p("  %s %s %s %d\n", padR(addr, 40), padR(used, 10), padR(q, 10), filt)
			// same threshold as the Email Accounts page toast
			if quota != "~" && bashInt(pct) > 80 {
				full = append(full, addr+" ("+pct+"%)")
			}
		}
		switch len(full) {
		case 0:
		case 1:
			f := full[0]
			s.warn("Email " + f[:strings.LastIndex(f, " (")] + " is reaching its quota (" + f[strings.LastIndex(f, "(")+1:] + ".")
		default:
			s.warn(fmt.Sprintf("%d emails are reaching their quota: %s.", len(full), strings.Join(full, " ")))
		}
	}
	s.row("Total", fmt.Sprintf("%d of %s", n, limitLabel(u.planRow["emails"])))

	s.subsection("ALIASES & FORWARDERS")
	var aliases [][2]string
	for _, l := range strings.Split(r.aliasList, "\n") {
		if m := aliasRe.FindStringSubmatch(l); m != nil && match(m[1]) {
			aliases = append(aliases, [2]string{m[1], m[2]})
		}
	}
	if len(aliases) == 0 {
		s.row("Aliases", "0")
	} else {
		for _, a := range aliases {
			f := bread(a[0]+"\t"+a[1], "\t", 2)
			s.p("  %s -> %s\n", padR(f[0], 40), strings.ReplaceAll(f[1], ",", ", "))
		}
	}

	// rejects by the hourly limit, logged by the mailserver with the sender address
	s.row("Hourly send limit", limitLabel(u.planRow["hourly"]))
	var rej []string
	for _, l := range strings.Split(r.rejects, "\n") {
		if match(l) {
			rej = append(rej, l)
		}
	}
	if len(rej) > 0 {
		sum := 0
		var parts []string
		for i, x := range uniqC(rej) {
			sum += int(bashInt(x[0]))
			sep := ""
			if i > 0 {
				sep = ", "
			}
			parts = append(parts, fmt.Sprintf("%s%s (%d)", sep, fieldN(x[1], 1), bashInt(x[0])))
		}
		s.warn(fmt.Sprintf("%d email(s) were rejected by the hourly limit in 24h: %s.", sum, strings.Join(parts, "")))
	}
}
