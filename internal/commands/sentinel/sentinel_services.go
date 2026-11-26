// ##############################################################################
// Script Name: internal/commands/sentinel/sentinel_services.go
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
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/redis"
	"opencli/internal/sh"
)

// builds an alert body: what failed, what sentinel did about it, where to look next and the last log lines
func alertDetails(what, did, check, logs string) string {
	msg := what + " " + did + " Check with: " + check
	if logs != "" {
		msg += "\n\nLast log lines:\n" + logs
	} else {
		msg += "\n\nNo log output."
	}
	return msg
}

func isActive(svc string) bool { return sh.C("systemctl", "is-active", "--quiet", svc).Run() == nil }

func (r *run) checkServiceStatus(svc, title string) {
	if isActive(svc) {
		r.pass++
		r.printf("%s %s is active.\n", cOK, svc)
		r.resolveNotification(title, false)
		return
	}
	if svc == "admin" && exists("/root/openadmin_is_disabled") {
		r.pass++
		r.printf("%s %s disabled by Administrator.\n", cOK, svc)
		r.resolveNotification(title, false)
		return
	}
	reason := "was not active"
	log := sh.C("journalctl", "-n", "5", "-u", svc, "--no-pager").NoErr().Str()
	switch {
	case strings.Contains(log, "start-limit-hit"):
		reason = "hit the systemd start rate limit"
		r.printf("%s %s hit start rate limit — resetting and restarting.\n", cFail, svc)
		sh.C("systemctl", "reset-failed", svc).Stdout(r.out).Stderr(r.out).Run()
	case strings.Contains(log, "Deactivated successfully"):
		r.warn++
		r.printf("%s %s is inactive (disabled by Administrator), skipping restart.\n", cWarn, svc)
		return
	default:
		r.printf("%s %s is not active — restarting.\n", cFail, svc)
	}
	sh.C("systemctl", "restart", svc).Stdout(r.out).Stderr(r.out).Run()
	if isActive(svc) {
		r.atLeast1()
		r.warn++
		r.printf("%s %s restarted successfully.\n", cOK, svc)
		r.resolveNotification(title, false)
		writeInfoNotification("service", svc+" service restarted", svc+" "+reason+". Sentinel restarted it and it is running again.")
	} else {
		r.fail++
		r.status = 2
		r.printf("%s Failed to restart %s.\n", cFail, svc)
		log = sh.C("journalctl", "-n", "5", "-u", svc, "--no-pager").NoErr().Str()
		r.writeNotification("critical", "service", title, alertDetails(svc+" "+reason+".", "Sentinel ran 'systemctl restart "+svc+"', but it is still not running.", "systemctl status "+svc+" and journalctl -u "+svc+" -n 50", log), "")
	}
}

// top 9 processes for an alert with who runs them and the full command, a process inside a rootless container runs as a subuid so that maps back to its account
func topProcesses(key string) string {
	col, sortKey := "pcpu", "-%cpu"
	if key == "mem" {
		col, sortKey = "pmem", "-%mem"
	}
	name := map[string]string{}
	for _, l := range conf.Lines("/etc/passwd") {
		p := strings.Split(l, ":")
		if len(p) > 2 {
			name[p[2]] = p[0]
		}
	}
	type rng struct {
		owner    string
		from, to float64
	}
	var subs []rng
	for _, l := range conf.Lines("/etc/subuid") {
		p := strings.Split(l, ":")
		for len(p) < 3 {
			p = append(p, "")
		}
		f, _ := strconv.ParseFloat(p[1], 64)
		c, _ := strconv.ParseFloat(p[2], 64)
		subs = append(subs, rng{p[0], f, f + c})
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%7s %-20s %6s %s\n", "PID", "USER", "%"+strings.ToUpper(key), "COMMAND")
	out := lines(sh.C("ps", "-eww", "--sort="+sortKey, "-o", "pid=,uid=,"+col+"=,args=").Str())
	if len(out) > 9 {
		out = out[:9]
	}
	strip := regexp.MustCompile(`^ *[^ ]+ +[^ ]+ +[^ ]+ +`)
	for _, l := range out {
		f := fields(l)
		for len(f) < 3 {
			f = append(f, "")
		}
		who := name[f[1]]
		if who == "" {
			u, _ := strconv.ParseFloat(f[1], 64)
			for _, s := range subs {
				if u >= s.from && u < s.to {
					who = s.owner
				}
			}
		}
		if who == "" {
			who = f[1]
		}
		cmd := strip.ReplaceAllString(l, "")
		if len(cmd) > 200 {
			cmd = cmd[:200]
		}
		fmt.Fprintf(&b, "%7s %-20s %6s %s\n", f[0], who, f[2], cmd)
	}
	return strings.TrimRight(b.String(), "\n")
}

// cached podman ps of the run
func dockerPSRefresh() {
	dockerPSCache = tmo(20, "podman", "ps", "--format", "{{.Names}}").NoErr().Str()
}

func dockerLog(name string) string {
	s, _ := tmo(10, "podman", "logs", "--tail", "10", name).Combined()
	return s
}

// removes a storage-only leftover that still holds the container name, so podman-compose up can't create it
// rm --storage refuses when the unmounted merged/ dir isn't empty, so clear empty dirs there and retry, never files, never a mounted dir
func (r *run) rmStorageLeftover(name string) bool {
	id := ""
	for _, l := range strings.Split(tmo(20, "podman", "ps", "-a", "--external", "--filter", "name=^"+name+"$", "--format", "{{.ID}} {{.Status}}").NoErr().Str(), "\n") {
		if field(l, 2) == "Storage" {
			id = field(l, 1)
			break
		}
	}
	if id == "" {
		return true
	}
	r.printf("  - Removing storage-only leftover of %s (%s)\n", name, id)
	out, err := tmo(30, "podman", "rm", "--storage", "--force", id).Combined()
	if err == nil {
		return true
	}
	if m := regexp.MustCompile(`mount point "(/var/lib/containers/storage/overlay/[0-9a-f]{64}/merged)": directory not empty`).FindStringSubmatch(out); m != nil {
		merged := m[1]
		if isDir(merged) && !strings.Contains(conf.Read("/proc/mounts"), " "+merged+" ") {
			sh.C("find", merged, "-mindepth", "1", "-depth", "-type", "d", "-empty", "-delete").Quiet().Run()
			if tmo(30, "podman", "rm", "--storage", "--force", id).Quiet().Run() == nil {
				return true
			}
		}
	}
	ls := lines(out)
	last := ""
	if len(ls) > 0 {
		last = ls[len(ls)-1]
	}
	r.printf("  - Could not remove storage-only %s (%s): %s\n", name, id, last)
	return false
}

func httpCode(url string, connect, total time.Duration, insecure bool) string {
	c := ipv4Client(total, insecure)
	c.Transport.(*http.Transport).DialContext = (&net.Dialer{Timeout: connect}).DialContext
	c.Transport.(*http.Transport).TLSHandshakeTimeout = connect
	resp, err := c.Get(url)
	if err != nil {
		return "000"
	}
	resp.Body.Close()
	return strconv.Itoa(resp.StatusCode)
}

func openpanelHTTPOK() bool {
	code := httpCode("https://localhost:2083/", 3*time.Second, 10*time.Second, true)
	if code == "000" {
		code = httpCode("http://localhost:2083/", 3*time.Second, 10*time.Second, false)
	}
	return code == "200" || code == "301" || code == "302" || code == "401"
}

func caddyHTTPOK() bool {
	code := httpCode("http://localhost/check", 2*time.Second, 5*time.Second, false)
	return code == "200" || code == "404"
}

// a busy server can miss one probe, and recreating the container then logs every user out, so it only counts as unresponsive when it keeps failing for about a minute
func httpOKRetry(check func() bool) bool {
	for i := 1; i <= 6; i++ {
		if check() {
			return true
		}
		if i < 6 {
			time.Sleep(10 * time.Second)
		}
	}
	return false
}

func rmContainer(name string) {
	tmo(30, "podman", "rm", "-f", name).Quiet().Run()
	tmo(30, "podman", "rm", "-f", "--storage", name).Quiet().Run()
}

func composeUp(svc string, secs int) error {
	return tmo(secs, "podman-compose", "up", "-d", svc).Dir("/root").Quiet().Run()
}

// recreating openpanel needs clamav and phpmyadmin removed, only the ones that were running come back
func openpanelRecreate() {
	var running []string
	for _, svc := range []string{"clamav", "phpmyadmin"} {
		if sh.C("podman", "inspect", "--format", "{{.State.Running}}", svc).NoErr().Str() == "true" {
			running = append(running, svc)
		}
		tmo(30, "podman", "rm", "-f", svc).Quiet().Run()
	}
	composeUp("openpanel", 120)
	for _, svc := range running {
		composeUp(svc, 180)
	}
}

// reason says why sentinel (re)started the container, used in both the info entry and the alert
func (r *run) dockerCheckAfterRestart(svc, title, reason string) bool {
	if reason == "" {
		reason = "was not running"
	}
	dockerPSRefresh()
	if grepW(dockerPSCache, svc) {
		r.warn--
		r.printf("%s %s restarted successfully.\n", cOK, svc)
		r.resolveNotification(title, false)
		writeInfoNotification("service", svc+" container restarted", "Container "+svc+" "+reason+". Sentinel started it and it is running again.")
		return true
	}
	r.warn--
	r.fail++
	r.status = 2
	r.printf("%s %s failed to restart.\n", cFail, svc)
	r.writeNotification("critical", "service", title, alertDetails("Container "+svc+" "+reason+".", "Sentinel tried to start it, but it is still not running.", "podman ps -a --filter name="+svc+" and podman logs "+svc, dockerLog(svc)), "")
	return false
}

func globAny(p string) bool { m, _ := filepath.Glob(p); return len(m) > 0 }

func (r *run) dockerContainersStatus(svc, title string) {
	if grepW(dockerPSCache, svc) {
		switch svc {
		case "caddy":
			caddyIsActive = true
			if httpOKRetry(caddyHTTPOK) {
				r.pass++
				r.printf("%s caddy is active and responding.\n", cOK)
				r.resolveNotification(title, false)
				return
			}
			r.warn++
			r.printf("%s caddy running but unresponsive — restarting.\n", cWarn)
			tmo(30, "podman", "restart", "caddy").Quiet().Run()
			rmContainer("caddy")
			composeUp("caddy", 120)
			time.Sleep(2 * time.Second)
			dockerPSRefresh()
			if httpOKRetry(caddyHTTPOK) {
				r.pass++
				r.warn--
				r.printf("%s caddy recovered.\n", cOK)
				r.resolveNotification(title, false)
				writeInfoNotification("service", "Caddy restarted and websites are up!", "Caddy was running but not responding on http://localhost/check. Sentinel recreated the container and it responds again.")
			} else {
				r.warn--
				r.fail++
				r.status = 2
				r.printf("%s caddy still unresponsive after restart.\n", cFail)
				r.writeNotification("critical", "service", title, alertDetails("Caddy is running but not responding on http://localhost/check, so websites may be down.", "Sentinel recreated the container, but it still does not respond.", "podman logs caddy", dockerLog("caddy")), "")
			}
		case "openpanel":
			if httpOKRetry(openpanelHTTPOK) {
				r.pass++
				r.printf("%s openpanel is active and responding.\n", cOK)
				r.resolveNotification(title, false)
				return
			}
			r.warn++
			r.printf("%s openpanel running but unresponsive — restarting.\n", cWarn)
			rmContainer("openpanel")
			r.rmStorageLeftover("openpanel")
			r.rmStorageLeftover("openpanel_redis")
			openpanelRecreate()
			time.Sleep(2 * time.Second)
			dockerPSRefresh()
			time.Sleep(2 * time.Second)
			if httpOKRetry(openpanelHTTPOK) {
				r.pass++
				r.warn--
				r.printf("%s openpanel recovered.\n", cOK)
				r.resolveNotification(title, false)
				writeInfoNotification("service", "OpenPanel restarted and responding!", "OpenPanel was running but not responding on port 2083. Sentinel recreated the container and it responds again.")
			} else {
				r.warn--
				r.fail++
				r.status = 2
				r.printf("%s openpanel still unresponsive after restart!\n", cFail)
				r.writeNotification("critical", "service", title, alertDetails("OpenPanel is running but not responding on port 2083, so users can't log in.", "Sentinel recreated the container, but it still does not respond.", "podman logs openpanel", dockerLog("openpanel")), "")
			}
		default:
			r.pass++
			r.printf("%s %s container is active.\n", cOK, svc)
			r.resolveNotification(title, false)
		}
		return
	}

	r.warn++
	modules := ""
	for _, l := range conf.Lines(confFile) {
		if strings.HasPrefix(l, "enabled_modules=") {
			modules += l + "\n"
		}
	}
	switch svc {
	case "openpanel":
		var users []string
		for _, l := range strings.Split(sh.Opencli("user-list", "--local", "--json").Timeout(15*time.Second).NoErr().Str(), "\n") {
			if strings.Contains(l, "username") {
				f := strings.Split(l, `"`)
				v := ""
				if len(f) > 3 {
					v = f[3]
				}
				if !strings.Contains(v, "SUSPENDED") {
					users = append(users, v)
				}
			}
		}
		u := strings.Join(users, "\n")
		if u == "" || u == "No users." {
			r.warn--
			r.printf("  - No users found; %s not needed.\n", svc)
			r.resolveNotification(title, false)
		} else {
			rmContainer("openpanel")
			r.rmStorageLeftover("openpanel")
			r.rmStorageLeftover("openpanel_redis")
			openpanelRecreate()
			r.dockerCheckAfterRestart(svc, title, "")
		}
	case "openpanel_dns":
		if strings.Contains(modules, "dns") {
			if globAny("/etc/bind/zones/*.zone") {
				rmContainer("bind9")
				composeUp("bind9", 120)
				r.dockerCheckAfterRestart(svc, title, "")
			} else {
				r.warn--
				r.println("  - No DNS zones; bind9 not needed.")
				r.resolveNotification(title, false)
			}
		} else {
			r.warn--
			r.println("  - DNS module not enabled; bind9 not starting.")
			r.resolveNotification(title, false)
		}
	case "phpmyadmin":
		if strings.Contains(modules, "phpmyadmin") {
			if globAny("/home/*/sockets/mysqld/mysqld.sock") {
				rmContainer("phpmyadmin")
				composeUp("phpmyadmin", 120)
				r.dockerCheckAfterRestart(svc, title, "")
			} else {
				r.warn--
				r.println("  - No mysql/mariadb services yet; phpmyadmin not needed.")
				r.resolveNotification(title, false)
			}
		} else {
			r.warn--
			r.println("  - phpmyadmin module not enabled; phpmyadmin not starting.")
			r.resolveNotification(title, false)
		}
	case "caddy":
		if exists("/etc/openpanel/caddy/domains") {
			rmContainer("caddy")
			composeUp("caddy", 120)
			r.dockerCheckAfterRestart(svc, title, "")
		} else {
			r.warn--
			r.println("  - No domains; caddy not needed.")
			r.resolveNotification(title, false)
		}
	default:
		tmo(30, "podman", "restart", svc).Quiet().Run()
		r.dockerCheckAfterRestart(svc, title, "")
	}
}

func mariadbPong() bool {
	return strings.Contains(db.Plain("SELECT 'PONG' AS PING;", "-N").Timeout(10*time.Second).NoErr().Str(), "PONG")
}

func (r *run) removeDependentOpenpanel() {
	if tmo(30, "podman", "inspect", "openpanel").Quiet().Run() != nil {
		return
	}
	r.println("  - Removing openpanel first (has a dependency on this container)")
	tmo(30, "podman", "kill", "openpanel").Quiet().Run()
	tmo(30, "podman", "container", "cleanup", "openpanel").Quiet().Run()
	rmContainer("openpanel")
	r.rmStorageLeftover("openpanel")
}

func (r *run) recreateMysql() bool {
	r.removeDependentOpenpanel()
	rmContainer("openpanel_mysql")
	composeUp("openpanel_mysql", 120)
	for i := 0; i < 6; i++ {
		time.Sleep(5 * time.Second)
		if mariadbPong() {
			return true
		}
	}
	return false
}

func (r *run) mysqlDockerContainersStatus() {
	title := "MariaDB service not active!"
	if strings.Contains(dockerPSCache, "openpanel_mysql") {
		if mariadbPong() {
			r.pass++
			r.printf("%s MariaDB container active and responding.\n", cOK)
			r.resolveNotification(title, false)
			r.resolveNotification("MariaDB service restarted!", false)
			return
		}
		r.warn++
		// a new container runs initialize.sql with networking off on its first start, recreating it then leaves the panel schema half-built
		started := tmo(10, "podman", "inspect", "openpanel_mysql", "--format", "{{.State.StartedAt.Unix}}").NoErr().Str()
		if n, err := strconv.ParseInt(started, 10, 64); err == nil && regexp.MustCompile(`^[0-9]+$`).MatchString(started) && time.Now().Unix()-n < 300 {
			r.printf("%s MariaDB started %ds ago and isn't answering yet — will re-check next run.\n", cWarn, time.Now().Unix()-n)
			return
		}
		r.printf("%s MariaDB running but not responding — restarting.\n", cFail)
		if r.recreateMysql() {
			r.warn--
			r.pass++
			r.println("    MariaDB is back online.")
			r.resolveNotification(title, false)
			writeInfoNotification("service", "MariaDB restarted successfully!", "MariaDB was running but not answering queries. Sentinel recreated the container and it responds again.")
		} else {
			r.warn--
			r.fail++
			r.status = 2
			r.println("    Error: MariaDB still not responding!")
			r.writeNotification("critical", "service", title, alertDetails("MariaDB is running but not answering queries, so websites and OpenPanel can't reach their databases.", "Sentinel recreated the container, but it still does not respond after 30s.", "podman logs openpanel_mysql", dockerLog("openpanel_mysql")), "")
		}
		return
	}
	r.fail++
	r.status = 2
	r.printf("%s MariaDB container not running — restarting.\n", cFail)
	if r.recreateMysql() {
		r.fail--
		r.atLeast1()
		r.println("    MariaDB is back online.")
		r.resolveNotification(title, false)
		writeInfoNotification("service", "MariaDB restarted successfully!", "The MariaDB container was not running. Sentinel started it and it responds again.")
	} else {
		r.println("    Error: MariaDB still not responding!")
		r.writeNotification("critical", "service", title, alertDetails("The MariaDB container was not running, so websites and OpenPanel can't reach their databases.", "Sentinel started it, but it still does not respond after 30s.", "podman ps -a --filter name=openpanel_mysql and podman logs openpanel_mysql", dockerLog("openpanel_mysql")), "")
	}
}

// redisPong pings over the unix socket first, since 2.0.30 the panel Redis runs with --port 0 and a plain redis-cli PING to 127.0.0.1:6379 always fails
func redisPong(c string) bool {
	if strings.Contains(tmo(30, "podman", "exec", c, "redis-cli", "-s", redis.Socket, "PING").NoErr().Str(), "PONG") {
		return true
	}
	return strings.Contains(tmo(30, "podman", "exec", c, "redis-cli", "PING").NoErr().Str(), "PONG")
}

func (r *run) redisDockerContainerStatus() {
	title := "Redis service not active!"
	const c = "openpanel_redis"
	if tmo(30, "podman", "inspect", c).Quiet().Run() != nil {
		r.warn++
		r.printf("%s Redis container not found — starting.\n", cFail)
		r.rmStorageLeftover(c)
		composeUp(c, 120)
		time.Sleep(2 * time.Second)
		r.dockerCheckAfterRestart(c, title, "did not exist")
		return
	}
	state := tmo(30, "podman", "inspect", c, "--format", "{{.State.Status}}").NoErr().Str()
	switch state {
	case "running":
		if redisPong(c) {
			os.Remove(lockFileForRedisStuck)
			r.pass++
			r.printf("%s Redis container active and responding.\n", cOK)
			r.resolveNotification(title, false)
			r.resolveNotification("Redis service restarted!", false)
			r.resolveNotification("Redis container stuck", false)
		} else {
			r.warn++
			r.printf("%s Redis running but not responding — restarting.\n", cFail)
			r.removeDependentOpenpanel()
			rmContainer(c)
			r.rmStorageLeftover(c)
			composeUp(c, 120)
			r.dockerCheckAfterRestart(c, title, "was running but not responding to PING")
		}
	case "exited", "stopped":
		os.Remove(lockFileForRedisStuck)
		r.warn++
		r.printf("%s Redis container is %s — restarting.\n", cWarn, state)
		r.rmStorageLeftover(c)
		composeUp(c, 120)
		r.dockerCheckAfterRestart(c, title, "had stopped (state: "+state+")")
	default:
		// anything else is treated as wedged, only force-recreate once the same stuck state shows up on two consecutive runs, to avoid nuking a container that's simply mid-startup
		now := time.Now().Unix()
		stuckSince := ""
		if isFile(lockFileForRedisStuck) {
			l := text1(conf.Read(lockFileForRedisStuck))
			ps, pt, _ := strings.Cut(l, ":")
			if ps == state {
				stuckSince = pt
			}
		}
		if stuckSince == "" {
			os.WriteFile(lockFileForRedisStuck, []byte(fmt.Sprintf("%s:%d\n", state, now)), 0o644)
			r.warn++
			r.printf("%s Redis container in transitional state '%s' — will re-check next run.\n", cWarn, state)
			return
		}
		stuckAge := now - int64(atoi(stuckSince))
		if stuckAge < 180 {
			r.warn++
			r.printf("%s Redis container still in '%s' (%ds) — waiting.\n", cWarn, state, stuckAge)
			return
		}
		r.warn++
		r.printf("%s Redis container stuck in '%s' for %ds — forcing removal and recreation.\n", cFail, state, stuckAge)
		os.Remove(lockFileForRedisStuck)
		r.removeDependentOpenpanel()
		tmo(30, "podman", "kill", c).Quiet().Run()
		rmContainer(c)
		r.rmStorageLeftover(c)
		composeUp(c, 120)
		time.Sleep(2 * time.Second)
		r.dockerCheckAfterRestart(c, title, fmt.Sprintf("was stuck in state '%s' for %ds", state, stuckAge))
	}
}

func (r *run) checkServices() {
	// "docker" kept as an accepted alias for "podman" so existing services= ini entries from before the podman migration keep working
	for _, svc := range []string{"caddy", "csf", "admin", "docker", "podman", "mysql", "panel", "phpmyadmin", "named"} {
		if !strings.Contains(","+services+",", ","+svc+",") {
			continue
		}
		switch svc {
		case "caddy":
			r.dockerContainersStatus("caddy", "Caddy not active — websites down!")
		case "phpmyadmin":
			r.dockerContainersStatus("phpmyadmin", "phpmyadmin not active — users can not access databases!")
		case "csf":
			r.checkServiceStatus("csf", "CSF Firewall not active — server unprotected!")
		case "admin":
			r.checkServiceStatus("admin", "OpenAdmin service not accessible!")
		case "mysql":
			r.mysqlDockerContainersStatus()
		case "docker", "podman":
			r.checkServiceStatus("podman.socket", "Podman not active — user websites down!")
		case "panel":
			r.redisDockerContainerStatus()
			r.dockerContainersStatus("openpanel", "OpenPanel container not running!")
		case "named":
			r.dockerContainersStatus("openpanel_dns", "BIND9 not active — DNS broken!")
		}
	}
}
