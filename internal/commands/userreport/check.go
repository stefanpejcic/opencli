// ##############################################################################
// Script Name: internal/commands/userreport/check.go
// Description: Performs comprehensive security checks on user files, rootless Podman instance and containers.
// Usage: opencli user-check <USERNAME>
// Author: Stefan Pejcic
// Created: 26.07.2025
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
	osuser "os/user"
	"regexp"
	"strings"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/podman"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-check",
		Desc:  "Performs comprehensive security checks on user files, rootless Podman instance and containers.",
		Usage: "opencli user-check <USERNAME>",
		Run:   userCheck,
	})
}

const (
	ckRed    = `\033[0;31m`
	ckGreen  = `\033[0;32m`
	ckYellow = `\033[1;33m`
	ckBlue   = `\033[0;34m`
	ckCyan   = `\033[0;36m`
	ckNC     = `\033[0m`
)

type checker struct {
	context              string
	pass, fail, warn, nf int
}

func (c *checker) header(title string) { echoe(ckBlue + "===== " + title + " =====" + ckNC) }

func (c *checker) subheader(title string) { echoe(ckCyan + "---- " + title + " ----" + ckNC) }

func (c *checker) result(status, message string, details ...string) {
	switch status {
	case "PASS":
		echoe("[" + ckGreen + status + ckNC + "] " + message)
		c.pass++
	case "FAIL":
		echoe("[" + ckRed + status + ckNC + "] " + message)
		c.fail++
	case "WARN":
		echoe("[" + ckYellow + status + ckNC + "] " + message)
		c.warn++
	case "INFO":
		echoe("[" + ckBlue + status + ckNC + "] " + message)
		c.nf++
	default:
		fmt.Println("[UNKNOWN] " + message)
	}
	if len(details) > 0 && details[0] != "" {
		fmt.Println("    " + details[0])
	}
}

// ctxPodman is podman_user, the socket error goes to stderr unless quiet
func ctxPodman(ctx string, quiet bool, args ...string) *sh.Cmd {
	st, err := os.Stat("/home/" + ctx)
	if err != nil {
		if !quiet {
			fmt.Fprintf(os.Stderr, "podman_user_socket: no such user '%s'\n", ctx)
		}
		return sh.C("false").Quiet()
	}
	uid := st.Sys().(*syscall.Stat_t).Uid
	return sh.C("podman", append([]string{"--remote"}, args...)...).Env(fmt.Sprintf("CONTAINER_HOST=unix:///hostfs/run/user/%d/podman/podman.sock", uid))
}

func (c *checker) prop(container, format string) string {
	return ctxPodman(c.context, true, "inspect", "--format="+format, container).NoErr().Str()
}

func userCheck(args []string) int {
	if os.Geteuid() != 0 {
		fmt.Println("This script must be run as root.")
		return 1
	}
	user := cli.Arg(args, 0)
	if user == "" {
		fmt.Println("Error: Username not provided.")
		fmt.Println("Usage: opencli user-check <username>")
		return 1
	}
	if strings.HasPrefix(user, "SUSPENDED_") {
		fmt.Println("Error: Suspended users cannot be checked.")
		return 1
	}
	c := &checker{}
	c.header("Checking user: " + user)
	c.dockerContext(user)

	c.daemonSecurity(user)
	fmt.Println("")
	c.files(user)
	fmt.Println("")
	c.allContainers()
	fmt.Println("")
	return c.summary()
}

func (c *checker) dockerContext(user string) {
	info, _ := db.Plain("SELECT id, server FROM users WHERE username = '"+user+"';", "-s").Out()
	var ids, ctxs []string
	for _, l := range strings.Split(info, "\n") {
		ids = append(ids, text.Field(l, 1))
		ctxs = append(ctxs, text.Field(l, 2))
	}
	userID := strings.TrimRight(strings.Join(ids, "\n"), "\n")
	c.context = strings.TrimRight(strings.Join(ctxs, "\n"), "\n")
	if userID == "" {
		fmt.Printf("FATAL ERROR: user %s does not exist.\n", user)
		cli.Exit(1)
	}
	if c.context == "" {
		fmt.Printf("FATAL ERROR: docker context %s does not exist.\n", c.context)
		cli.Exit(1)
	}
}

func (c *checker) daemonSecurity(user string) {
	c.subheader("Podman Rootless Security")
	if !sh.Has("podman") {
		c.result("FAIL", "Podman is not installed or not in PATH")
		return
	}
	c.result("INFO", "Running for user: "+user)

	if !ctxPodman(c.context, true, "info").Quiet().OK() {
		c.result("FAIL", "Podman is not running or not accessible for '"+c.context+"'")
		return
	}
	c.result("PASS", "Podman is reachable for '"+c.context+"'")

	// rootless: the socket only exists under /run/user/<uid>/ for a rootless instance
	sockOK := false
	if st, err := os.Stat("/home/" + c.context); err == nil {
		uid := st.Sys().(*syscall.Stat_t).Uid
		if s, err := os.Stat(fmt.Sprintf("/hostfs/run/user/%d/podman/podman.sock", uid)); err == nil && s.Mode()&os.ModeSocket != 0 {
			sockOK = true
		}
	}
	if sockOK {
		c.result("PASS", "Rootless podman is configured")
	} else {
		c.result("FAIL", "Rootless podman socket not found for '"+c.context+"'")
	}

	// no-new-privileges, set as the per-user default in containers.conf by user/add.sh
	cc := "/home/" + c.context + "/.config/containers/containers.conf"
	ccText, _ := os.ReadFile(cc)
	if regexp.MustCompile(`no_new_privileges *= *true`).Match(ccText) {
		c.result("PASS", "Containers can not get new privileges")
	} else {
		c.result("FAIL", "Containers can escalate privileges (no_new_privileges is not set in "+cc+")")
	}

	if strings.Contains(string(ccText), "dns_servers") {
		c.result("PASS", "Custom DNS resolvers are configured")
	} else {
		c.result("FAIL", "Custom resolvers are NOT configured - this will cause dns issues")
	}
}

// fileReadable is [[ -f $1 && -r $1 ]]
func fileReadable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && syscall.Access(p, 4) == nil
}

// ckPct is used * 100 / quota from bash arithmetic
func ckQuotaRow(c *checker, used, quota int64, fail, warn, pass, odd string) {
	usage := used * 100 / quota
	switch {
	case usage >= 99:
		c.result("FAIL", fail)
	case usage >= 90:
		c.result("WARN", warn)
	case usage < 90:
		c.result("PASS", pass)
	default:
		c.result("WARN", fmt.Sprintf(odd, usage))
	}
}

func (c *checker) files(user string) {
	c.subheader("System and User Files")

	curUID := ""
	if st, err := os.Lstat("/home/" + user); err == nil {
		curUID = fmt.Sprint(st.Sys().(*syscall.Stat_t).Uid)
	} else {
		fmt.Fprintf(os.Stderr, "stat: cannot statx '/home/%s': No such file or directory\n", user)
	}
	curGID := ""
	if u, ok := lookupPasswd(user); ok {
		curGID = u
	} else {
		fmt.Fprintf(os.Stderr, "id: '%s': no such user\n", user)
	}

	storage, _ := os.ReadFile("/home/" + c.context + "/.config/containers/storage.conf")
	if regexp.MustCompile(`graphroot *= *"/home/` + regexp.QuoteMeta(c.context) + `/docker-data"`).Match(storage) {
		c.result("PASS", "/home/"+c.context+"/docker-data is configured for podman storage.")
	} else {
		c.result("FAIL", "/home/"+c.context+"/docker-data is NOT configured for podman storage.")
	}

	checkFile := func(path string, failMsg ...string) {
		name := path[strings.LastIndex(path, "/")+1:]
		msg := name + " missing."
		if len(failMsg) > 0 {
			msg = failMsg[0]
		}
		if fileReadable(path) {
			c.result("PASS", name+" file exists.")
		} else {
			c.result("FAIL", msg)
		}
	}
	h := "/home/" + c.context
	checkFile(h + "/.env")
	checkFile(podman.ComposeFile(c.context), "compose file does not exist - no containers can be started.")
	checkFile(h+"/backup.env", "backup.env missing - Backups can not be configured via UI")
	checkFile(h+"/crons.ini", "crons.ini missing - Cron Jobs can not be created via UI")
	checkFile(h+"/custom.cnf", "custom.cnf missing - Database limits can not be configured via UI.")
	checkFile(h+"/default.vcl", "default.vcl missing - Varnish Caching can not be configured via UI.")
	checkFile(h+"/httpd.conf", "httpd.conf missing - Apache web server can not be configured via UI.")
	checkFile(h+"/openlitespeed.conf", "openlitespeed.conf missing - OpenLitespeed web server can not be configured via UI.")
	checkFile(h+"/litespeed.conf", "litespeed.conf missing - LiteSpeed web server can not be configured via UI.")
	checkFile(h+"/nginx.conf", "nginx.conf missing - Nginx web server can not be configured via UI.")
	checkFile(h+"/openresty.conf", "openresty.conf missing - OpenResty web server can not be configured via UI.")

	// disk and inodes
	qout, _ := sh.C("quota", "-u", c.context).NoErr().Out()
	if strings.Contains(qout, "none") {
		c.result("WARN", "No quota set for user.")
	} else {
		var lines []string
		for _, l := range strings.Split(qout, "\n") {
			if strings.HasPrefix(strings.TrimLeft(l, " \t\v\f\r"), "/") {
				lines = append(lines, l)
			}
		}
		if len(lines) == 0 {
			c.result("WARN", "Quota info not found for user.")
		} else {
			line := lines[0]
			used, quota := text.Field(line, 2), text.Field(line, 3)
			usedInodes, quotaInodes := text.Field(line, 5), text.Field(line, 6)
			//todo: 5 is grace if over quota!!
			if used == "" || quota == "" || text.Atoi(quota) == 0 {
				c.result("WARN", "Could not parse disk quota info properly for user.")
			} else {
				u, q := int64(text.Atoi(used)), int64(text.Atoi(quota))
				usedGB, quotaGB := bcDiv(u*100/1024/1024), bcDiv(q*100/1024/1024)
				ckQuotaRow(c, u, q,
					fmt.Sprintf("Disk usage is over quota (%s GB used of %s GB).", usedGB, quotaGB),
					fmt.Sprintf("Disk usage is over 90%% (%s GB used of %s GB).", usedGB, quotaGB),
					fmt.Sprintf("Disk usage is below quota (%s GB used of %s GB).", usedGB, quotaGB),
					"Disk usage percentage is unexpected: %d%% ("+usedGB+" GB used of "+quotaGB+" GB).")
			}
			if usedInodes == "" || quotaInodes == "" || text.Atoi(quotaInodes) == 0 {
				c.result("WARN", "Could not parse inode quota info properly for user.")
			} else {
				ckQuotaRow(c, int64(text.Atoi(usedInodes)), int64(text.Atoi(quotaInodes)),
					fmt.Sprintf("Inode usage is over quota (%s used of %s).", usedInodes, quotaInodes),
					fmt.Sprintf("Inode usage is over 90%% (%s used of %s).", usedInodes, quotaInodes),
					fmt.Sprintf("Inode usage is below quota (%s used of %s).", usedInodes, quotaInodes),
					"Inode usage percentage is unexpected: %d ("+usedInodes+" used of "+quotaInodes+").")
			}
		}
	}

	c.result("INFO", fmt.Sprintf("Checking files ownership for user: %s (UID: %s, GID: %s)", user, curUID, curGID))

	homeDir := "/home/" + c.context + "/docker-data/"
	bad := false
	if _, err := os.Lstat(homeDir); err != nil {
		fmt.Fprintf(os.Stderr, "find: '%s': No such file or directory\n", homeDir)
	} else {
		want := text.Atoi(curUID)
		findWalk(homeDir, func(p string, st *syscall.Stat_t) bool {
			// skip /run/.. and /var/cache/apt/..
			if strings.Contains(p, "/diff/run/") || strings.Contains(p, "/diff/var/cache/apt") {
				return true
			}
			if int(st.Uid) != want {
				c.result("WARN", fmt.Sprintf("File '%s' is owned by UID:%d instead of UID:%s", p, st.Uid, curUID))
				bad = true
				return false
			}
			return true
		})
	}
	if !bad {
		c.result("PASS", "All docker files are owned by UID:"+curUID)
	} else {
		c.result("FAIL", "Some docker files in "+homeDir+" are not owned by UID:"+curUID+" - fix permissions started in background.")
		sh.C("timeout", "600", sh.Self(), "files-fix_permissions", c.context).Quiet().Background()
	}
}

// lookupPasswd returns the primary gid of a user, like id -g
func lookupPasswd(user string) (string, bool) {
	u, err := osuser.Lookup(user)
	if err != nil {
		return "", false
	}
	return u.Gid, true
}

// findWalk visits paths under dir in readdir order like find -mindepth 1, stops when fn returns false
func findWalk(dir string, fn func(string, *syscall.Stat_t) bool) bool {
	f, err := os.Open(dir)
	if err != nil {
		return true
	}
	names, _ := f.Readdirnames(-1)
	f.Close()
	base := strings.TrimSuffix(dir, "/")
	for _, n := range names {
		p := base + "/" + n
		var st syscall.Stat_t
		if syscall.Lstat(p, &st) != nil {
			continue
		}
		if !fn(p, &st) {
			return false
		}
		if st.Mode&syscall.S_IFMT == syscall.S_IFDIR {
			if !findWalk(p, fn) {
				return false
			}
		}
	}
	return true
}

func (c *checker) containerUser(ctr, name string) {
	u := c.prop(ctr, "{{.Config.User}}")
	if u == "root" || u == "" {
		c.result("PASS", name+": Container is running as root")
	} else {
		c.result("FAIL", name+": Container is running as non-root user ("+u+")")
	}
}

func (c *checker) recover(ctr, name string) bool {
	c.result("INFO", name+": Attempting recovery — trying podman restart first")

	if ctxPodman(c.context, true, "restart", ctr).NoErr().OK() {
		time.Sleep(3 * time.Second)
		if c.prop(ctr, "{{.State.Status}}") == "running" {
			c.result("PASS", name+": Recovery successful via podman restart")
			podman.SyncVarnish(c.context)
			return true
		}
	}

	c.result("WARN", name+": podman restart failed or container still not running — trying compose down/up")

	compose := podman.ComposeFile(c.context)
	if !isRegular(compose) {
		c.result("FAIL", name+": Cannot recover — compose file missing: "+compose)
		return false
	}

	svc := c.prop(ctr, `{{index .Config.Labels "com.docker.compose.service"}}`)
	if svc == "" {
		c.result("FAIL", name+": Cannot recover via compose — service label missing")
		return false
	}

	if podman.ComposeUser(c.context, "-f", compose, "down", svc).NoErr().OK() &&
		podman.ComposeUser(c.context, "-f", compose, "up", "-d", svc).NoErr().OK() {
		time.Sleep(5 * time.Second)
		// re-inspect by name since the container id may have changed after recreate
		newCtr := text.FirstLine(ctxPodman(c.context, true, "ps", "-q", "--filter", "name=^"+svc+"$").NoErr().Str())
		if newCtr != "" && c.prop(newCtr, "{{.State.Status}}") == "running" {
			c.result("PASS", name+": Recovery successful via compose down/up")
			podman.SyncVarnish(c.context)
			return true
		}
	}

	c.result("FAIL", name+": All recovery attempts failed — manual intervention required")
	return false
}

func isRegular(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func (c *checker) liveHealth(ctr, name string) {
	test := c.prop(ctr, "{{if .Config.Healthcheck}}{{index .Config.Healthcheck.Test 0}}{{end}}")
	if test == "" || test == "NONE" {
		c.result("WARN", name+": No healthcheck configured")
		return
	}
	switch st := c.prop(ctr, "{{.State.Health.Status}}"); st {
	case "healthy":
		c.result("PASS", name+": Healthcheck status is healthy")
	case "starting":
		c.result("INFO", name+": Healthcheck is still starting")
	case "unhealthy":
		c.result("FAIL", name+": Healthcheck reports unhealthy")
		last := strings.ReplaceAll(c.prop(ctr, "{{with index .State.Health.Log 0}}{{.Output}}{{end}}"), "\n", "")
		if last != "" {
			c.result("INFO", name+": Last healthcheck output: "+last)
		}
		c.recover(ctr, name)
	case "":
		c.result("INFO", name+": No health status available (healthcheck may not have run yet)")
	default:
		c.result("WARN", name+": Unknown healthcheck status: "+st)
	}
}

func (c *checker) status(ctr, name string) {
	switch st := c.prop(ctr, "{{.State.Status}}"); st {
	case "running":
		c.result("PASS", name+": Container is running")
	case "created":
		c.result("INFO", name+": Container is created but not started")
		c.recover(ctr, name)
	case "paused":
		c.result("WARN", name+": Container is paused")
	case "exited", "dead":
		c.result("FAIL", name+": Container is not running (status: "+st+")")
		c.recover(ctr, name)
	default:
		c.result("WARN", name+": Container status is unknown ("+st+")")
	}
}

func (c *checker) image(ctr, name string) {
	// php-fpm containers aren't tagged individually, skip
	if strings.HasPrefix(name, "php-fpm-") {
		return
	}
	img := c.prop(ctr, "{{.Config.Image}}")
	switch {
	case strings.Contains(img, "latest"):
		c.result("WARN", name+": Using 'latest' tag is not recommended for image: "+img)
	case strings.Contains(img, ":"):
		c.result("PASS", name+": Using specific tag for image: "+img)
	default:
		c.result("WARN", name+": Image tag not specified for image: "+img)
	}
}

func (c *checker) securityOptions(ctr, name string) {
	opt := c.prop(ctr, "{{.HostConfig.SecurityOpt}}")
	if strings.Contains(opt, "label") {
		c.result("PASS", name+": SELinux security options set")
	} else {
		c.result("WARN", name+": SELinux options not set")
	}
	if strings.Contains(opt, "no-new-privileges") {
		c.result("PASS", name+": Container cannot acquire additional privileges")
	} else {
		c.result("WARN", name+": --no-new-privileges restriction not set")
	}
}

func (c *checker) capabilities(ctr, name string) {
	capAdd := c.prop(ctr, "{{.HostConfig.CapAdd}}")
	if capAdd == "[]" || capAdd == "" {
		c.result("PASS", name+": No extra Linux capabilities added")
	} else {
		c.result("WARN", name+": Additional Linux capabilities may be added", "Capabilities: "+capAdd)
	}
}

func (c *checker) privileged(ctr, name string) {
	if c.prop(ctr, "{{.HostConfig.Privileged}}") == "true" {
		c.result("FAIL", name+": Privileged mode is enabled")
	} else {
		c.result("PASS", name+": Privileged mode is disabled")
	}
}

func (c *checker) mounts(ctr, name string) {
	mounts := c.prop(ctr, `{{range .Mounts}}{{.Source}}{{"\n"}}{{end}}`)
	var sensitive, critical []string
	for _, m := range strings.Split(mounts, "\n") {
		if strings.HasPrefix(m, "/etc/openpanel/") || strings.HasPrefix(m, "/home/") {
			sensitive = append(sensitive, m)
		}
		if (strings.HasPrefix(m, "/etc") || strings.HasPrefix(m, "/boot")) && !strings.HasPrefix(m, "/etc/openpanel/") {
			critical = append(critical, m)
		}
	}
	switch {
	case len(critical) > 0:
		c.result("FAIL", name+": Critical host directories mounted", strings.Join(critical, "\n"))
	case len(sensitive) > 0:
		c.result("WARN", name+": Bind mounts under /home/ or /etc/openpanel/")
	default:
		c.result("PASS", name+": No sensitive host directories mounted")
	}
	if name != "docker-proxy" {
		if regexp.MustCompile(`/(docker|podman)\.sock`).MatchString(mounts) {
			c.result("FAIL", name+": Container engine socket is mounted inside the container")
		} else {
			c.result("PASS", name+": Container engine socket is NOT mounted")
		}
	}
}

func (c *checker) processes(ctr, name string) {
	out := ctxPodman(c.context, true, "exec", ctr, "ps", "aux").NoErr().Str()
	if strings.Contains(out, "sshd") {
		c.result("FAIL", name+": sshd is running inside container")
	} else {
		c.result("PASS", name+": sshd is NOT running inside container")
	}
}

func (c *checker) networking(ctr, name string) {
	mode := c.prop(ctr, "{{.HostConfig.NetworkMode}}")
	switch {
	case mode == "":
		c.result("INFO", name+": No network configured")
	case strings.Contains(mode, "host"):
		c.result("FAIL", name+": Host network namespace is used")
	case regexp.MustCompile(`(_www|_db)`).MatchString(mode):
		c.result("PASS", name+": Using OpenPanel network ("+mode+")")
	default:
		c.result("WARN", name+": Using custom network ("+mode+")")
	}

	ports := ctxPodman(c.context, true, "port", ctr).NoErr().Str()
	if ports == "" {
		c.result("PASS", name+": No exposed ports")
		return
	}
	lines := strings.Split(ports, "\n")
	priv := regexp.MustCompile(`:[0-9]{1,3}$`)
	for _, l := range lines {
		if priv.MatchString(l) {
			c.result("WARN", name+": Container may be exposing privileged ports")
			break
		}
	}
	var third []string
	notLocal := false
	for _, l := range lines {
		f := text.Field(l, 3)
		third = append(third, f)
		if strings.SplitN(f, ":", 2)[0] != "127.0.0.1" {
			notLocal = true
		}
	}
	switch {
	case regexp.MustCompile(`0.0.0.0`).MatchString(ports):
		c.result("WARN", name+": Container traffic bound to all interfaces")
	case notLocal:
		c.result("WARN", name+": Some ports are not bound to 127.0.0.1")
		c.result("INFO", name+": Open container ports: "+strings.Join(third, " "))
	default:
		c.result("PASS", name+": All ports are bound to 127.0.0.1")
	}
}

// bashPositive is [[ "$v" -gt 0 ]] for the plain numbers inspect prints
func bashPositive(v string) (int64, bool) {
	var n int64
	if _, err := fmt.Sscan(strings.TrimSpace(v), &n); err != nil || !text.IsInt(strings.TrimPrefix(strings.TrimSpace(v), "-")) {
		return 0, false
	}
	return n, n > 0
}

func (c *checker) resources(ctr, name string) {
	if mem, ok := bashPositive(c.prop(ctr, "{{.HostConfig.Memory}}")); ok {
		c.result("PASS", name+": Memory usage is limited to "+bcDiv(mem*100/(1024*1024*1024))+" GB")
	} else {
		c.result("WARN", name+": No memory limit set")
	}
	if cpu, ok := bashPositive(c.prop(ctr, "{{.HostConfig.NanoCpus}}")); ok {
		c.result("PASS", name+": CPU usage is limited to "+bcDiv(cpu*100/1000000000)+" CPUs")
	} else {
		c.result("WARN", name+": No CPU limit set")
	}
	pid := c.prop(ctr, "{{.HostConfig.PidsLimit}}")
	if _, ok := bashPositive(pid); ok {
		c.result("PASS", name+": PID cgroup limit is set ("+pid+")")
	} else {
		c.result("WARN", name+": PID cgroup limit is not set")
	}
}

func (c *checker) filesystem(ctr, name string) {
	if c.prop(ctr, "{{.HostConfig.ReadonlyRootfs}}") == "true" {
		c.result("PASS", name+": Root filesystem is read-only")
	} else {
		c.result("WARN", name+": Root filesystem is NOT read-only")
	}
}

func (c *checker) devices(ctr, name string) {
	dev := c.prop(ctr, "{{json .HostConfig.Devices}}")
	if strings.Contains(dev, "/dev") {
		c.result("FAIL", name+": Host devices are exposed", "Devices: "+dev)
	} else {
		c.result("PASS", name+": Host devices are NOT exposed")
	}
}

func (c *checker) ulimits(ctr, name string) {
	ul := c.prop(ctr, "{{.HostConfig.Ulimits}}")
	if ul != "[]" && ul != "" {
		c.result("INFO", name+": Custom ulimit configured: "+ul)
	} else {
		c.result("WARN", name+": Default ulimit is used")
	}
}

func (c *checker) composeLocation(ctr, name string) {
	dir := c.prop(ctr, `{{index .Config.Labels "com.docker.compose.project.working_dir"}}`)
	file := c.prop(ctr, `{{index .Config.Labels "com.docker.compose.project.config_files"}}`)
	expected := podman.ComposeFile(c.context)
	if dir != "" && file != "" {
		full := dir + "/" + file
		if strings.HasPrefix(file, "/") {
			full = file
		}
		if full == expected {
			c.result("PASS", name+": Container running from OpenPanel")
		} else {
			c.result("FAIL", name+": Container running outside of OpenPanel: "+full)
		}
		return
	}
	if c.prop(ctr, `{{index .Config.Labels "com.docker.compose.service"}}`) != "" {
		c.result("WARN", name+": Container has compose labels but missing location info")
	} else {
		c.result("FAIL", name+": Container not started via docker-compose from expected location", "Expected: "+expected)
	}
}

func (c *checker) nameServiceMatch(ctr, name string) {
	svc := c.prop(ctr, `{{index .Config.Labels "com.docker.compose.service"}}`)
	project := c.prop(ctr, `{{index .Config.Labels "com.docker.compose.project"}}`)
	if svc == "" || project == "" {
		c.result("WARN", name+": Container not managed by docker-compose or missing service labels")
		return
	}
	if name == svc {
		c.result("PASS", name+": Container name matches compose service name: "+svc)
	} else {
		c.result("FAIL", name+": Container name does not match compose service name: "+svc)
	}
}

func (c *checker) single(ctr string) {
	name := strings.TrimPrefix(c.prop(ctr, "{{.Name}}"), "/")
	c.subheader("Container: " + name)
	c.composeLocation(ctr, name)
	c.nameServiceMatch(ctr, name)
	c.status(ctr, name)
	c.containerUser(ctr, name)
	c.image(ctr, name)
	c.liveHealth(ctr, name)
	c.securityOptions(ctr, name)
	c.capabilities(ctr, name)
	c.privileged(ctr, name)
	c.mounts(ctr, name)
	c.processes(ctr, name)
	c.networking(ctr, name)
	c.resources(ctr, name)
	c.filesystem(ctr, name)
	c.devices(ctr, name)
	c.ulimits(ctr, name)
}

func (c *checker) allContainers() {
	c.subheader("Container Security Checks")
	ids, _ := ctxPodman(c.context, false, "ps", "-q").Out()
	if ids == "" {
		c.result("INFO", "No running containers found")
		return
	}
	c.result("INFO", fmt.Sprintf("Found %d running container(s)", len(strings.Split(ids, "\n"))))
	for _, id := range strings.Split(ids, "\n") {
		if id != "" {
			c.single(id)
		}
	}
}

func (c *checker) summary() int {
	c.header("Audit Summary")
	total := c.pass + c.fail + c.warn + c.nf
	echoe(fmt.Sprintf("Total checks performed: %s%d%s", ckBlue, total, ckNC))
	echoe(fmt.Sprintf("✅ Passed: %s%d%s", ckGreen, c.pass, ckNC))
	echoe(fmt.Sprintf("❌ Failed: %s%d%s", ckRed, c.fail, ckNC))
	echoe(fmt.Sprintf("⚠️  Warnings: %s%d%s", ckYellow, c.warn, ckNC))
	echoe(fmt.Sprintf("ℹ️  Info: %s%d%s", ckBlue, c.nf, ckNC))
	switch {
	case c.fail > 0:
		echoe(`\n` + ckRed + "⚠️  Critical issues found! Please review and address failed checks." + ckNC)
		return 1
	case c.warn > 0:
		echoe(`\n` + ckYellow + "⚠️  Some improvements recommended. Please review warnings." + ckNC)
	default:
		echoe(`\n` + ckGreen + "✅ All checks passed!" + ckNC)
	}
	return 0
}
