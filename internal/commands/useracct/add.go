// ##############################################################################
// Script Name: internal/commands/useracct/add.go
// Description: Create a new user with the provided plan_name.
// Usage: opencli user-add <USERNAME> <PASSWORD|generate> <EMAIL> "<PLAN_NAME>" [--send-email] [--debug] [--webserver="<nginx|apache|openresty|openlitespeed|litespeed|varnish+nginx|varnish+apache|varnish+openresty|varnish+openlitespeed>"] [--sql=<mysql|mariadb>] [--reseller=<RESELLER_USERNAME>] [--private-note="this user.."] [--no-sentinel]
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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/mail"
	"opencli/internal/passwd"
	"opencli/internal/podman"
	"opencli/internal/reseller"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-add",
		Desc:  "Create a new user with the provided plan_name.",
		Usage: `opencli user-add <USERNAME> <PASSWORD|generate> <EMAIL> "<PLAN_NAME>" [--send-email] [--debug] [--webserver="<nginx|apache|openresty|openlitespeed|litespeed|varnish+nginx|varnish+apache|varnish+openresty|varnish+openlitespeed>"] [--sql=<mysql|mariadb>] [--reseller=<RESELLER_USERNAME>] [--private-note="this user.."] [--no-sentinel]`,
		Docs:  "https://docs.openpanel.com",
		Run:   add,
	})
}

const (
	addForbiddenFile = "/etc/openpanel/openadmin/config/forbidden_usernames.txt"
	addLockFile      = "/var/lock/openpanel_user_add.lock"
	addPastaModule   = "/etc/openpanel/docker/selinux/pasta_local.pp"
	addAutostartFile = "/etc/openpanel/docker/compose/1.0/autostart.services"
)

var addEmailRe = regexp.MustCompile(`^[a-zA-Z0-9._%+\\\-]+@[a-zA-Z0-9.\\\-]+\.[a-zA-Z]{2,}$`)

type addJob struct {
	username, password, email, planName    string
	debug, skipImages, sendEmail, sentinel bool
	reseller, webserver, sqlType           string
	privateNote                            string
	enterprise, plaintextPw                string

	planID, hashed, userID string
	ports                  [7]string
	sharedStore            string
	bg                     sync.WaitGroup
	openpanelDone          chan struct{}
}

func addUsage() {
	fmt.Println("Usage: opencli user-add <username> <password|generate> <email> '<plan_name>' [--send-email] [--debug] [--reseller=<RESELLER_USER>] [--private-note=<NOTE>] [--webserver=<TYPE>] [--sql=<mysql|mariadb>] [--no-sentinel]")
	fmt.Println()
	fmt.Println("Required arguments:")
	fmt.Println("  <username>                 The username of the new user.")
	fmt.Println("  <password|generate>        The password for the new user, or 'generate' to auto-generate a password.")
	fmt.Println("  <email>                    The email address associated with the new user.")
	fmt.Println("  <plan_name>                The plan to assign to the new user.")
	fmt.Println()
	fmt.Println("Optional flags:")
	fmt.Printf("%-25s %-45s\n", "  --send-email", "Send a welcome email to the user.")
	fmt.Printf("%-25s %-45s\n", "  --debug", "Enable debug mode for additional output.")
	fmt.Printf("%-25s %-45s\n", "  --private-note=", "Write a private note for this user (visible only on OpenAdmin).")
	fmt.Printf("%-25s %-45s\n", "  --reseller=", "Assign a reseller as the owner of this user.")
	fmt.Printf("%-25s %-45s\n", "  --webserver=", "Webserver type to use (nginx|apache|openresty|openlitespeed|litespeed, optionally varnish+ prefixed).")
	fmt.Printf("%-25s %-45s\n", "  --sql=", "Database type to use (mysql|mariadb).")
	fmt.Printf("%-25s %-45s\n", "  --no-sentinel", "Skip sending a Sentinel notification for this action.")
	fmt.Println()
}

func add(args []string) int {
	if len(args) < 4 || len(args) > 12 {
		addUsage()
		return 1
	}
	j := &addJob{
		username: strings.ToLower(args[0]),
		password: args[1],
		email:    args[2],
		planName: args[3],
		sentinel: true,
	}

	// the lock file goes away on every exit after this point, like the old EXIT trap
	defer os.Remove(addLockFile)

	j.readConfig()
	for _, a := range args[4:] {
		switch {
		case a == "--debug":
			j.debug = true
		case a == "--send-email":
			j.sendEmail = true
		case a == "--skip-images":
			j.skipImages = true
		case a == "--no-sentinel":
			j.sentinel = false
		case strings.HasPrefix(a, "--reseller="):
			j.reseller = a[strings.IndexByte(a, '=')+1:]
		case strings.HasPrefix(a, "--sql="):
			j.sqlType = a[strings.IndexByte(a, '=')+1:]
		case strings.HasPrefix(a, "--webserver="):
			j.webserver = a[strings.IndexByte(a, '=')+1:]
		case strings.HasPrefix(a, "--private-note="):
			j.privateNote = a[strings.IndexByte(a, '=')+1:]
		default:
			fmt.Fprintf(os.Stderr, "[!] Warning: unknown flag '%s'\n", a)
		}
	}

	db.Require()
	j.sharedStore = podman.SharedImageStore()
	os.Setenv("SHARED_STORE", j.sharedStore)

	lock, err := os.OpenFile(addLockFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err == nil {
		defer lock.Close()
	}
	if err != nil || syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		fmt.Println("[✘] Error: A user creation process is already running.")
		fmt.Println("Please wait for it to complete before starting a new one. Exiting.")
		return 1
	}
	// background steps write to the terminal too, so they finish before we exit
	defer j.bg.Wait()

	// 1. validate data and fetch plan info
	j.validateCreation()
	j.validateUsername()
	j.validatePasswordInLists()
	// checked again before useradd, the old script only did it after and left a half made system user behind
	if j.password != "generate" {
		passwd.RequireStrength(j.password)
	}
	j.loadPlan()
	j.checkResellerLimits()

	// 2. create system user
	j.createLinuxUser()
	j.ensureSubIDs()

	// 3. setup podman rootless for user
	j.setupPodman()

	// 4. do background stuff while podman setup is running
	portsDone := make(chan [7]string, 1)
	go func() { portsDone <- addFindPorts() }()

	j.bg.Add(1)
	go func() { defer j.bg.Done(); j.copySkeleton() }()

	j.openpanelDone = make(chan struct{})
	go func() {
		defer close(j.openpanelDone)
		compose := func() {
			sh.C("sh", "-c", "cd /root && podman-compose up -d openpanel").Stdin("").Quiet().Run()
		}
		if sh.C("podman", "container", "exists", "openpanel").OK() {
			if !sh.C("podman", "start", "openpanel").Quiet().OK() {
				compose()
			}
		} else {
			compose()
		}
	}()

	j.generatePasswordHash()
	j.bg.Add(1)
	go func() { defer j.bg.Done(); j.createUserVolume() }()

	j.ports = <-portsDone
	j.configureEnvironment()

	// https://github.com/stefanpejcic/opencli/pull/242
	home := "/home/" + j.username
	for _, f := range []string{"my.cnf", ".env", "backup.env"} {
		if conf.IsFile(home + "/" + f) {
			os.Chmod(home+"/"+f, 0o600)
		}
	}
	// pma.php runs as www-data (uid/gid 33) in the shared phpmyadmin container and reads MYSQL_ROOT_PASSWORD from .env for autologin,
	// with 600 it gets an empty password and loops between pma.php and index.php, so .env is 640 with group 33, still not readable by other accounts
	if conf.IsFile(home+"/.env") && os.Chown(home+"/.env", -1, 33) == nil {
		os.Chmod(home+"/.env", 0o640)
	}

	// 6. validate podman service is started for user (socket exists), compose command and context are working
	j.testPodman()

	// 8. save and notify
	j.waitForPanelDB()
	j.saveToDatabase()

	// needs to run AFTER saving user to database
	sh.Opencli("plan-apply", j.planID, j.username).Quiet().Run()

	// apply limits first, then start containers
	j.autostartServices()

	// this needs to run AFTER plan-apply to show updated du info
	sh.Opencli("user-quota").Quiet().Background()

	j.bg.Add(1)
	go func() { defer j.bg.Done(); j.sendWelcomeEmail() }()
	j.notifySentinel()
	j.updateResellerCount() // must run after saving user to db

	// backup.env holds remote backup credentials, compose reads it as root so it stays root:root 0600 after the chown
	// the chown -R also resets .env back to the user's group, so give it group 33 again or phpmyadmin autologin loops
	chownName(home+"/", j.username)
	if conf.IsFile(home+"/backup.env") && os.Lchown(home+"/backup.env", 0, 0) == nil {
		os.Chmod(home+"/backup.env", 0o600)
	}
	if conf.IsFile(home+"/.env") && os.Lchown(home+"/.env", -1, 33) == nil {
		os.Chmod(home+"/.env", 0o640)
	}
	return 0
}

func (j *addJob) log(format string, a ...any) {
	if j.debug {
		fmt.Printf(format+"\n", a...)
	}
}

func addDie(msg string) {
	fmt.Fprintln(os.Stderr, "[✘] ERROR: "+msg)
	cli.Exit(1)
}

// addEscape doubles single quotes like the script's escape()
func addEscape(s string) string { return strings.ReplaceAll(s, "'", "''") }

// addBackslashQuotes is the ${var//\'/\\\'} that ran on top of escape()
func addBackslashQuotes(s string) string { return strings.ReplaceAll(s, "'", `\'`) }

func addQuery(q string) (string, error) { return db.Cmd(q, "-sN").Out() }

// readConfig mimics `while IFS='=' read -r key value`, a last line without newline is skipped like read did
func (j *addJob) readConfig() {
	b, err := os.ReadFile(conf.Panel)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	for _, line := range lines[:len(lines)-1] {
		key, value, _ := strings.Cut(line, "=")
		if regexp.MustCompile(`^[[:space:]]*#`).MatchString(key) || key == "" {
			continue
		}
		switch key {
		case "key":
			j.enterprise = value
		case "email_plaintext_passwords":
			j.plaintextPw = value
		}
	}
}

func (j *addJob) validateCreation() {
	if j.enterprise == "" {
		if j.reseller != "" {
			addDie("Resellers require the Enterprise edition.")
		}
		j.log("Community edition: checking 3-user limit")
	} else if j.reseller == "" {
		j.log("Enterprise edition: unlimited users")
	}
	esc := addEscape(j.username)
	result, _ := addQuery("SELECT COUNT(*), SUM(username='" + esc + "' OR (username REGEXP '^SUSPENDED_[0-9]{14}_' AND SUBSTRING(username, 26) = '" + esc + "')) FROM users")
	f := strings.Fields(text.FirstLine(result))
	count, taken := 0, 0
	if len(f) > 0 {
		count = text.Atoi(f[0])
	}
	if len(f) > 1 {
		taken = text.Atoi(f[1])
	}
	if j.enterprise == "" && count > 2 {
		addDie("Community edition is limited to 3 accounts. Upgrade to Enterprise for unlimited users.")
	}
	if taken > 0 {
		addDie("Username '" + j.username + "' is already taken.")
	}
}

var addUsernameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*$`)

func (j *addJob) validateUsername() {
	j.log("Validating username '%s'", j.username)
	n := len([]rune(j.username))
	if n < 3 || n > 20 {
		addDie("Username must be 3–20 characters. See https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openpanel")
	}
	if !addUsernameRe.MatchString(j.username) {
		addDie("Username must start with a letter and contain only letters and numbers.")
	}
	if b, err := os.ReadFile(addForbiddenFile); err == nil {
		// the last line has no newline when saved from OpenAdmin, and a browser form sends \r\n
		for _, f := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
			f = strings.TrimSuffix(f, "\r")
			if strings.ToLower(j.username) == strings.ToLower(f) {
				addDie("Username '" + j.username + "' is not allowed. See https://openpanel.com/docs/articles/accounts/forbidden-usernames/#reserved-usernames")
			}
		}
	}
}

func (j *addJob) validatePasswordInLists() {
	// generated passwords are random, no point checking the literal word
	if j.password == "generate" {
		return
	}
	j.log("Checking password against weak-password dictionary")
	passwd.RequireNotCommon(j.password)
}

func (j *addJob) loadPlan() {
	j.log("Looking up plan '%s'", j.planName)
	j.planID, _ = addQuery("SELECT id FROM plans WHERE name = '" + addBackslashQuotes(addEscape(j.planName)) + "'")
	if j.planID == "" {
		addDie("Plan '" + j.planName + "' not found.")
	}
}

func (j *addJob) checkResellerLimits() {
	if j.reseller == "" {
		return
	}
	exists := sh.C("sqlite3", "/etc/openpanel/openadmin/users.db", "SELECT COUNT(*) FROM user WHERE username='"+addEscape(j.reseller)+"' AND role='reseller';").Str()
	if text.Atoi(exists) < 1 {
		addDie("User '" + j.reseller + "' is not a reseller or is not allowed to create users.")
	}
	limits := reseller.LimitsFile(j.reseller)
	current, _ := addQuery("SELECT COUNT(*) FROM users WHERE owner='" + addBackslashQuotes(addEscape(j.reseller)) + "'")
	if current == "" {
		current = "0"
	}
	jqSetNumber(limits, "current_accounts", current)
	max, _ := jqRaw(limits, "max_accounts", "unlimited")
	if max != "unlimited" && max != "0" && text.Atoi(current) >= text.Atoi(max) {
		addDie("Reseller '" + j.reseller + "' has reached the maximum account limit (" + max + ").")
	}
	if !reseller.PlanAllowed(limits, j.planID) {
		addDie("Plan ID '" + j.planID + "' is not assigned to reseller '" + j.reseller + "'.")
	}
	if reseller.OverDisk(limits) {
		addDie("Reseller '" + j.reseller + "' has reached the maximum disk usage limit.")
	}
}

// addFreeUID is the lowest uid from 1002 up with no passwd entry, never 1000
func addFreeUID() int {
	var uids []int
	for _, l := range conf.Lines("/etc/passwd") {
		f := strings.Split(l, ":")
		if len(f) > 2 {
			if n, err := strconv.Atoi(f[2]); err == nil && n >= 1002 {
				uids = append(uids, n)
			}
		}
	}
	sort.Ints(uids)
	c := 1002
	for _, u := range uids {
		if c < u {
			return c
		}
		c = u + 1
	}
	return c
}

func (j *addJob) createLinuxUser() {
	j.log("Creating local Linux user '%s'", j.username)
	uid := addFreeUID()
	if sh.C("useradd", "-m", "-d", "/home/"+j.username, "-u", strconv.Itoa(uid), j.username).Run() != nil {
		addDie("Failed to create Linux user '" + j.username + "' on master.")
	}
	j.userID = statUID("/home/" + j.username)
}

func (j *addJob) ensureSubIDs() {
	// modern useradd assigns the /etc/subuid /etc/subgid range automatically, but add it if it's missing
	if !hasLinePrefix("/etc/subuid", j.username+":") && sh.C("usermod", "--add-subuids", "100000-165535", j.username).Run() != nil {
		addDie("Failed to assign subuid range for '" + j.username + "'.")
	}
	if !hasLinePrefix("/etc/subgid", j.username+":") && sh.C("usermod", "--add-subgids", "100000-165535", j.username).Run() != nil {
		addDie("Failed to assign subgid range for '" + j.username + "'.")
	}
}

const addPastaTE = `module pasta_local 1.0;

require {
    type pasta_t;
    type unconfined_t;
    class dir open;
}

allow pasta_t unconfined_t:dir open;
`

// https://community.openpanel.org/d/302-user-add-pasta-failed-with-exit-code-1-netns-dir-open-permission-denied
func (j *addJob) fixPastaSelinux() {
	if !sh.Has("selinuxenabled") || !sh.C("selinuxenabled").Quiet().OK() || !sh.Has("pasta") {
		return
	}
	for _, m := range strings.Split(sh.Str("semodule", "-l"), "\n") {
		if m == "pasta_local" {
			return
		}
	}
	j.log("Applying SELinux policy fix for rootless podman (pasta netns access)")
	if conf.IsFile(addPastaModule) {
		if !sh.C("semodule", "-i", addPastaModule).NoErr().OK() {
			fmt.Println("[!] Warning: Failed to load pasta SELinux module.")
		}
		return
	}
	if !sh.Has("checkmodule") || !sh.Has("semodule_package") {
		fmt.Println("[!] Warning: policycoreutils-devel/selinux-policy-devel missing; cannot build pasta SELinux module.")
		return
	}
	tmp, err := os.MkdirTemp("", "tmp.")
	if err != nil {
		return
	}
	defer os.RemoveAll(tmp)
	ok := os.WriteFile(tmp+"/pasta_local.te", []byte(addPastaTE), 0o666) == nil &&
		sh.C("checkmodule", "-M", "-m", "-o", "pasta_local.mod", "pasta_local.te").Dir(tmp).Quiet().OK() &&
		sh.C("semodule_package", "-o", "pasta_local.pp", "-m", "pasta_local.mod").Dir(tmp).Quiet().OK() &&
		os.MkdirAll("/etc/openpanel/docker/selinux", 0o777) == nil &&
		cpFile(tmp+"/pasta_local.pp", addPastaModule) == nil &&
		sh.C("semodule", "-i", "pasta_local.pp").Dir(tmp).Quiet().OK()
	if !ok {
		fmt.Println("[!] Warning: Failed to build/load pasta SELinux module.")
	}
}

func (j *addJob) userctl(args ...string) *sh.Cmd {
	return sh.C("systemctl", append([]string{"--user", "-M", j.username + "@"}, args...)...).Quiet()
}

func (j *addJob) setupPodman() {
	j.log("Configuring rootless Podman for '%s'", j.username)
	home := "/home/" + j.username
	cfg := home + "/.config/containers"

	// graphroot stays under docker-data/ so the rest of opencli (backups, quota, restore) keeps the same layout as rootless docker; additionalimagestores points at the shared read-only store from PODMAN_INSTALL.sh so containers reuse pulled images instead of duplicating them
	os.MkdirAll(home+"/docker-data", 0o777)
	os.MkdirAll(cfg, 0o777)
	os.WriteFile(cfg+"/storage.conf", []byte(`[storage]
driver = "overlay"
graphroot = "`+home+`/docker-data"

[storage.options]
additionalimagestores = [
  "`+j.sharedStore+`"
]

[storage.options.overlay]
mount_program = "/usr/bin/fuse-overlayfs"
`), 0o666)

	// security hardening that used to live in rootless docker's daemon.json (no-new-privileges + custom DNS) -- containers.conf is the podman equivalent, applied as the default for every container this user creates
	containersConf := "[containers]\nno_new_privileges = true\ndns_servers = [\"1.1.1.1\", \"1.0.0.1\"]\n"

	// containers start with the panel and mail hostnames pointing at the server, so apps reach its mail over tls without extra_hosts
	hosts := "/etc/openpanel/docker/container_hosts"
	if !conf.IsFile(hosts) {
		sh.Opencli("docker-hosts").Quiet().Run()
	}
	// podman won't create containers when base_hosts_file is missing, so it's only set when the file is there
	if conf.IsFile(hosts) {
		containersConf = strings.Replace(containersConf, "[containers]\n", "[containers]\nbase_hosts_file = \""+hosts+"\"\n", 1)
	}
	os.WriteFile(cfg+"/containers.conf", []byte(containersConf), 0o666)

	chmodR(home, 0o755)
	chownName(home, j.username)

	j.fixPastaSelinux()

	// write the slice override before user@ starts, otherwise plan-apply restarts user@ later and kills the fresh podman socket and containers
	slice := "/etc/systemd/system/user-" + j.userID + ".slice.d"
	if !conf.IsFile(slice + "/override.conf") {
		os.MkdirAll(slice+"/", 0o777)
		os.WriteFile(slice+"/override.conf", []byte("[Slice]\nDelegate=yes\n"), 0o666)
		sh.C("systemctl", "daemon-reload").Quiet().Run()
	}

	sh.C("loginctl", "enable-linger", j.username).Quiet().Run()
	for w := 0; w < 30; w++ {
		if sh.C("systemctl", "is-active", "user@"+j.userID+".service").Quiet().OK() {
			break
		}
		time.Sleep(time.Second)
	}

	// a pull into the shared store may still be running (installer prefetch, docker-update), it leaves the store unreadable for users
	podman.FixSharedPerms()

	// machinectl shell -c silently did nothing here, so the socket never got enabled and died on the next user@ restart or reboot
	j.userctl("daemon-reload").Run()
	j.userctl("reset-failed", "podman.socket").Run()
	j.userctl("enable", "--now", "podman.socket").Run()

	// confirm it actually took, retry once if not
	if !j.userctl("is-active", "podman.socket").OK() {
		time.Sleep(2 * time.Second)
		j.userctl("enable", "--now", "podman.socket").Run()
	}
}

func (j *addJob) testPodman() {
	sock := "unix:///hostfs/run/user/" + j.userID + "/podman/podman.sock"
	sockPath := "/hostfs/run/user/" + j.userID + "/podman/podman.sock"
	const maxTime = 60
	ready := false

	j.log("Testing connection to podman service..")
	output := "no socket at " + sockPath
	for elapsed := 0; elapsed < maxTime; elapsed++ {
		if isSocket(sockPath) {
			out, err := sh.C("podman", "--remote", "info").Env("CONTAINER_HOST=" + sock).Timeout(3 * time.Second).Combined()
			output = out
			if err == nil {
				ready = true
				j.log("Podman socket live (%s)", sockPath)
				break
			}
		}
		// a podman that couldn't read the shared store exits 125 and its socket gives up (trigger-limit-hit), so all this sees is
		// "connection refused": every 5s fix the store again (a pull may still be running and rewrite it) and start the socket again
		if elapsed%5 == 4 {
			j.log("Podman not answering yet, fixing shared store permissions and starting the socket again..")
			podman.FixSharedPerms()
			j.userctl("reset-failed", "podman.socket", "podman.service").Run()
			j.userctl("start", "podman.socket").Run()
		}
		time.Sleep(time.Second)
	}
	if !ready {
		j.hardCleanup()
		addDie(fmt.Sprintf("Podman not responding after %ds! error is: %s", maxTime, output))
	}

	// podman-compose can reach this user's rootless podman instance?
	out, err := sh.C("podman-compose", "ps").Dir("/home/" + j.username).Env("CONTAINER_HOST=" + sock).Timeout(5 * time.Second).Combined()
	if err != nil {
		j.hardCleanup()
		addDie("podman-compose is not working for '" + j.username + "': " + out)
	}
	j.log("podman-compose is working against '%s's rootless podman.", j.username)
}

func (j *addJob) hardCleanup() {
	if j.username == "" {
		fmt.Println("ERROR: USERNAME is empty")
		return
	}
	uid := sysUID(j.username)
	if uid == "" || !text.IsInt(uid) {
		return
	}
	// stop the manager properly so linger doesn't respawn it
	sh.C("loginctl", "disable-linger", j.username).Quiet().Run()
	sh.C("systemctl", "stop", "user@"+uid+".service").Quiet().Run()
	sh.C("loginctl", "terminate-user", j.username).Quiet().Run()
	sh.C("pkill", "-9", "-U", uid).Quiet().Run()
	time.Sleep(time.Second)

	if sh.Has("deluser") {
		sh.C("deluser", "--remove-home", j.username).Run()
	} else {
		sh.C("userdel", "-r", j.username).Run()
	}
	os.RemoveAll(conf.UsersDir + "/" + j.username)
	os.RemoveAll("/run/user/" + uid) // uid captured BEFORE delete, never empty
	os.RemoveAll("/etc/systemd/system/user-" + uid + ".slice.d")
	sh.C("systemctl", "daemon-reload").Quiet().Run()
}

var addPortLine = regexp.MustCompile(`^[A-Z_]+_PORT=`)
var addDigits = regexp.MustCompile(`[0-9]+`)

func addFindPorts() [7]string {
	last, _ := db.Cmd("SELECT server FROM users ORDER BY id DESC LIMIT 1", "-sN").NoErr().Out()
	min := 32768
	if last != "" {
		env := "/home/" + last + "/.env"
		if conf.IsFile(env) {
			best, found := -1, false
			for _, l := range grepLines(env, addPortLine) {
				for _, d := range addDigits.FindAllString(l, -1) {
					if n, err := strconv.Atoi(d); err == nil && (!found || n > best) {
						best, found = n, true
					}
				}
			}
			if found {
				min = best
			}
		}
	}
	var p [7]string
	for i := 1; i <= 7; i++ {
		p[i-1] = strconv.Itoa(min + i)
	}
	return p
}

var (
	addSvcLine     = regexp.MustCompile(`^  [A-Za-z0-9_.-]+:[[:space:]]*$`)
	addVolLine     = regexp.MustCompile(`^[[:space:]]+-[[:space:]]*["']?webserver_data:`)
	addVolSrcLine  = regexp.MustCompile(`source:[[:space:]]*["']?webserver_data["']?[[:space:]]*$`)
	addPortsLine   = regexp.MustCompile(`^    ports:`)
	addEmptyPorts  = regexp.MustCompile(`ports:[[:space:]]*\[[[:space:]]*\]`)
	addTopLevel    = regexp.MustCompile(`^[^[:space:]#]`)
	addPerconaFrom = regexp.MustCompile(`^  mysql:`)
	addTopService  = regexp.MustCompile(`^  [^ #]`)
	addPerconaImg  = regexp.MustCompile(`image:.*percona`)
)

// addComposeWebservers lists services that mount webserver_data and publish ports, backup mounts it too but has no ports
func addComposeWebservers(file string) []string {
	var out []string
	inS, vol, ports := false, false, false
	name := ""
	flush := func() {
		if name != "" && vol && ports {
			out = append(out, name)
		}
	}
	for _, l := range conf.Lines(file) {
		if strings.HasPrefix(l, "services:") {
			inS = true
			continue
		}
		if inS && addTopLevel.MatchString(l) {
			flush()
			inS, name = false, ""
		}
		if !inS {
			continue
		}
		if addSvcLine.MatchString(l) {
			flush()
			name = strings.TrimSuffix(strings.Fields(l)[0], ":")
			vol, ports = false, false
			continue
		}
		if name == "" {
			continue
		}
		if addVolLine.MatchString(l) || addVolSrcLine.MatchString(l) {
			vol = true
		}
		if addPortsLine.MatchString(l) && !addEmptyPorts.MatchString(l) {
			ports = true
		}
	}
	if inS {
		flush()
	}
	return out
}

// addSedRange is sed '/start/,/end/ s|old|new|' with a literal old, first match per line
func addSedRange(file string, start, end *regexp.Regexp, old, repl string) {
	in := false
	sedFile(file, func(l string) string {
		if !in {
			if !start.MatchString(l) {
				return l
			}
			in = true
		} else if end.MatchString(l) {
			in = false
		}
		return strings.Replace(l, old, repl, 1)
	})
}

func addRandAlnum(src []byte, n int) string {
	var b strings.Builder
	for _, c := range src {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			b.WriteByte(c)
			if b.Len() == n {
				break
			}
		}
	}
	return b.String()
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func (j *addJob) configureEnvironment() {
	j.log("Configuring environment for '%s'", j.username)
	P := j.ports
	port1 := P[0] + ":80"
	port2 := P[1] + ":3306"
	port3 := P[2] + ":5432"
	port4 := P[3] + ":80"
	port5 := "127.0.0.1:" + P[4] + ":80"
	port6 := "127.0.0.1:" + P[5] + ":443"
	port7 := "127.0.0.1:" + P[6] + ":80"

	rootPw := addRandAlnum([]byte(base64.StdEncoding.EncodeToString(randBytes(16))), 24)

	home := "/home/" + j.username
	if !cp("/etc/openpanel/docker/compose/1.0/docker-compose.yml", home+"/docker-compose.yml") {
		addDie("docker-compose.yml template missing from /etc/openpanel/")
	}

	// podman-compose can't resolve a ${VAR} nested inside another ${VAR:-default} (docker-compose can), so flatten the webserver port mapping to HTTP_PORT here, swapped to PROXY_HTTP_PORT below when the webserver sits behind Varnish
	sedFile(home+"/docker-compose.yml", func(l string) string {
		return strings.ReplaceAll(l, "${PROXY_HTTP_PORT:-${HTTP_PORT}}", "${HTTP_PORT}")
	})

	sedFile("/etc/openpanel/docker/compose/1.0/.env", func(l string) string { return strings.TrimSuffix(l, "\r") })
	if !cp("/etc/openpanel/docker/compose/1.0/.env", home+"/.env") {
		addDie(".env template missing from /etc/openpanel/")
	}

	q := func(key string) *regexp.Regexp { return regexp.MustCompile(key + `="[^"\n]*"`) }
	subs := []struct {
		re   *regexp.Regexp
		repl string
	}{
		{q("USERNAME"), `USERNAME="` + j.username + `"`},
		{q("USER_ID"), `USER_ID="` + j.userID + `"`},
		{q("CONTEXT"), `CONTEXT="` + j.username + `"`},
		{q("^HTTP_PORT"), `HTTP_PORT="` + port5 + `"`},
		{q("HTTPS_PORT"), `HTTPS_PORT="` + port6 + `"`},
		{q("PGADMIN_PORT"), `PGADMIN_PORT="` + port1 + `"`},
		{q("POSTGRES_PORT"), `POSTGRES_PORT="127.0.0.1:` + port3 + `"`},
		{q("PMA_PORT"), `PMA_PORT="` + port4 + `"`},
		{regexp.MustCompile(`\{PMA_PORT\}`), P[3]},
		{regexp.MustCompile(`rootpassword`), rootPw},
		{q("MYSQL_PORT"), `MYSQL_PORT="127.0.0.1:` + port2 + `"`},
		{q("PROXY_HTTP_PORT"), `PROXY_HTTP_PORT="` + port7 + `"`},
	}
	sedFile(home+"/.env", func(l string) string {
		for _, s := range subs {
			l = subRe(s.re, s.repl, true)(l)
		}
		return l
	})

	if addEmailRe.MatchString(j.email) {
		sedFile(home+"/.env", subRe(regexp.MustCompile(`PGADMIN_MAIL=[^"\n]*`), "PGADMIN_MAIL="+j.email, true))
	}

	// private note shown on OpenAdmin
	if j.privateNote != "" {
		os.WriteFile(home+"/notes.txt", []byte(j.privateNote+"\n"), 0o666)
	}

	// Webserver
	if j.webserver != "" {
		available := " " + strings.Join(addComposeWebservers(home+"/docker-compose.yml"), " ")
		if available != " " {
			available += " "
		}
		if available == " " {
			available = " nginx apache openresty openlitespeed "
		}
		m := regexp.MustCompile(`^varnish\+([a-zA-Z0-9_.-]+)$`).FindStringSubmatch(j.webserver)
		switch {
		case m != nil && strings.Contains(available, " "+m[1]+" "):
			ws := m[1]
			j.log("Enabling Varnish + %s", ws)
			wsRe := subRe(regexp.MustCompile(`WEB_SERVER="[^"\n]*"`), `WEB_SERVER="`+ws+`"`, true)
			unc := regexp.MustCompile(`^#(PROXY_HTTP_PORT=.*)`)
			sedFile(home+"/.env", func(l string) string { return subRe(unc, `\1`, false)(wsRe(l)) })
			addSedRange(home+"/docker-compose.yml", regexp.MustCompile(`container_name: `+ws+`$`), regexp.MustCompile(`HTTPS_PORT`), "${HTTP_PORT}", "${PROXY_HTTP_PORT}")
		case strings.Contains(available, " "+j.webserver+" "):
			j.log("Setting webserver: %s", j.webserver)
			sedFile(home+"/.env", subRe(regexp.MustCompile(`WEB_SERVER="[^"\n]*"`), `WEB_SERVER="`+j.webserver+`"`, true))
		default:
			fmt.Printf("[!] Warning: Webserver '%s' is not in the docker-compose.yml template (available:%s). Using template default.\n", j.webserver, strings.TrimSuffix(available, " "))
		}
	}

	// SQL
	if j.sqlType != "" {
		if j.sqlType == "mysql" || j.sqlType == "mariadb" {
			j.log("Setting SQL type: %s", j.sqlType)
			sedFile(home+"/.env", subRe(regexp.MustCompile(`MYSQL_TYPE="[^"\n]*"`), `MYSQL_TYPE="`+j.sqlType+`"`, true))
		} else {
			fmt.Printf("[!] Warning: Invalid SQL type '%s'. Using template default.\n", j.sqlType)
		}
	}

	// if owned by a reseller with their own logo URL set, write it into the new account's .env so its container can pick it up directly, since it only ever sees its own home directory
	if j.reseller != "" {
		logo, _ := jqRaw("/etc/openpanel/openadmin/resellers/"+j.reseller+".json", "logo_url", "")
		if logo != "" && reseller.LogoURLValid(logo) {
			if !reseller.EnvSetLogo(home+"/.env", logo) {
				j.log("[!] Warning: could not set the reseller logo URL in .env")
			}
		}
	}

	j.log("Creating user files..")
	if !conf.IsFile(home + "/.env") {
		j.hardCleanup()
		addDie("Failed to create .env file.")
	}

	// Socket dirs and config files
	for _, d := range []string{"mysqld", "postgres", "redis", "valkey", "memcached", "mongodb", "php"} {
		os.MkdirAll(home+"/sockets/"+d, 0o777)
	}
	cp("/etc/openpanel/mysql/user.cnf", home+"/custom.cnf")
	cp("/etc/openpanel/postgres/postgresql.conf", home+"/postgre_custom.conf")
	cp("/etc/openpanel/nginx/user-nginx.conf", home+"/nginx.conf")
	cp("/etc/openpanel/openresty/nginx.conf", home+"/openresty.conf")
	cp("/etc/openpanel/openlitespeed/httpd_config.conf", home+"/openlitespeed.conf")
	cpFile("/etc/openpanel/litespeed/httpd.conf", home+"/litespeed.conf")
	cp("/etc/openpanel/apache/httpd.conf", home+"/httpd.conf")
	cp("/etc/openpanel/varnish/default.vcl", home+"/default.vcl")
	cpFile("/etc/openpanel/ofelia/users.ini", home+"/crons.ini")
	cpFile("/etc/openpanel/backups/backup.env", home+"/backup.env")
	if conf.Exists("/etc/openpanel/php/ini") {
		cpR("/etc/openpanel/php/ini", home+"/php.ini")
	} else {
		fmt.Fprintln(os.Stderr, "cp: cannot stat '/etc/openpanel/php/ini': No such file or directory")
	}

	chownName(home+"/sockets", j.username)
	os.Chmod(home+"/sockets/", 0o777)

	mycnf := "[client]\nuser=root\npassword=" + rootPw + "\n"
	// percona's image looks for the socket in /var/lib/mysql, the mysql tools need the shared one, same as OpenPanel sets when switching to percona
	f, p := false, false
	for _, l := range conf.Lines(home + "/docker-compose.yml") {
		if addPerconaFrom.MatchString(l) {
			f = true
			continue
		}
		if addTopService.MatchString(l) {
			f = false
		}
		if f && addPerconaImg.MatchString(l) {
			p = true
		}
	}
	if p {
		mycnf += "socket=/var/run/mysqld/mysqld.sock\n"
	}
	if os.WriteFile(home+"/my.cnf", []byte(mycnf), 0o666) != nil || !conf.IsFile(home+"/my.cnf") {
		fmt.Println("[!] Warning: Failed to create my.cnf.")
	}
}

func (j *addJob) copySkeleton() {
	j.log("Creating configuration files for the newly created user")
	cpR("/etc/openpanel/skeleton/", conf.UsersDir+"/"+j.username+"/")
}

func (j *addJob) saveToDatabase() {
	j.log("Saving user '%s' to database", j.username)
	hash := addEscape(j.hashed)
	var q string
	if j.reseller != "" {
		q = "INSERT INTO users (username, password, owner, email, plan_id, server) VALUES ('" + j.username + "', '" + hash + "', '" + addEscape(j.reseller) + "', '" + addEscape(j.email) + "', '" + j.planID + "', '" + j.username + "');"
	} else {
		q = "INSERT INTO users (username, password, email, plan_id, server) VALUES ('" + j.username + "', '" + hash + "', '" + addEscape(j.email) + "', '" + j.planID + "', '" + j.username + "');"
	}
	if _, err := addQuery(q); err != nil {
		j.hardCleanup()
		addDie("Database insert failed.")
	}
	fmt.Printf("[✔] Successfully added user %s with password: %s\n", j.username, j.password)
}

// podman-compose up -d openpanel recreates mysql and redis when any root container has an older config hash (saving OpenAdmin > Modules is enough), so let it finish and wait for mysql before writing
func (j *addJob) waitForPanelDB() {
	waited := 0
wait:
	for waited < 300 {
		select {
		case <-j.openpanelDone:
			break wait
		default:
		}
		time.Sleep(time.Second)
		waited++
	}
	for !db.Cmd("SELECT 1", "-sN").Quiet().OK() && waited < 360 {
		time.Sleep(time.Second)
		waited++
	}
	j.log("Waited %ds for the openpanel container and the panel database.", waited)
}

func (j *addJob) sendWelcomeEmail() {
	if !j.sendEmail {
		return
	}
	if !addEmailRe.MatchString(j.email) {
		fmt.Printf("[!] Warning: %s is not a valid address; skipping email.\n", j.email)
		return
	}
	domain := sh.Opencli("domain").Str()
	port := sh.Opencli("port").Str()
	protocol := "http"
	if conf.IsFile("/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/"+domain+"/"+domain+".key") || conf.IsFile("/etc/openpanel/caddy/ssl/custom/"+domain+"/"+domain+".key") {
		protocol = "https"
	}
	adminPort := mail.AdminPort()
	loginURL := protocol + "://" + domain + ":" + port + "/login"

	token := ""
	for len(token) < 64 {
		token += addRandAlnum(randBytes(256), 64-len(token))
	}
	tokRe := regexp.MustCompile(`^mail_security_token=.*`)
	sedFile(conf.Panel, func(l string) string { return tokRe.ReplaceAllLiteralString(l, "mail_security_token="+token) })

	pw := j.password
	if j.plaintextPw == "no" {
		pw = "********"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("transient", token)
	w.WriteField("recipient", j.email)
	w.WriteField("subject", "New OpenPanel account information")
	w.WriteField("body", "OpenPanel URL: "+loginURL+" | username: "+j.username+" | password: "+pw)
	w.Close()
	ok := false
	if req, err := http.NewRequest("POST", protocol+"://"+domain+":"+adminPort+"/send_email", &buf); err == nil {
		req.Header.Set("Content-Type", w.FormDataContentType())
		dialer := &net.Dialer{}
		client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			Proxy:           http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, "tcp4", addr)
			},
		}}
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode < 400 {
				ok = true
			} else {
				fmt.Fprintf(os.Stderr, "curl: (22) The requested URL returned error: %d\n", resp.StatusCode)
			}
		}
	}
	if !ok {
		fmt.Println("[!] Warning: Failed to send welcome email.")
	}
}

func (j *addJob) generatePasswordHash() {
	if j.password == "generate" {
		j.password = base64.StdEncoding.EncodeToString(randBytes(12))
		j.log("Generated password: %s", j.password)
	}
	passwd.RequireStrength(j.password)
	j.hashed, _ = sh.C("openssl", "passwd", "-6", "-salt", hex.EncodeToString(randBytes(8)), j.password).Out()
}

func (j *addJob) createUserVolume() {
	vol := "/home/" + j.username + "/docker-data/volumes/" + j.username + "_html_data/_data/"
	os.MkdirAll(vol, 0o777)
	addModeR(vol, 0o020)
	link := "/home/" + j.username + "/files"
	if st, err := os.Lstat(link); err == nil {
		if st.IsDir() {
			link += "/_data"
		} else {
			os.Remove(link)
		}
	}
	os.Symlink(vol, link)
	chownName("/home/"+j.username+"/docker-data/volumes", j.username)
}

func (j *addJob) notifySentinel() {
	if !j.sentinel {
		return
	}
	sh.Opencli("sentinel", "--action=user_create", "--title=User account '"+j.username+"' created", "--message=Account '"+j.username+"' created; email: "+j.email+"; plan: "+j.planName).Quiet().Background()
}

func (j *addJob) updateResellerCount() {
	if j.reseller == "" {
		return
	}
	file := "/etc/openpanel/openadmin/resellers/" + j.reseller + ".json"
	if !conf.IsFile(file) {
		return
	}
	count, _ := addQuery("SELECT COUNT(*) FROM users WHERE owner='" + addBackslashQuotes(j.reseller) + "'")
	n := count
	if n == "" {
		n = "0"
	}
	jqSetNumber(file, "current_accounts", n)
	j.log("Reseller %s account count updated to %s", j.reseller, count)
}

// autostartServices starts the plan's default services in the background once the user's podman answers
func (j *addJob) autostartServices() {
	if j.skipImages {
		j.log("Skipping image pull (--skip-images).")
		return
	}
	env := "/home/" + j.username + "/.env"
	if !conf.IsFile(env) {
		fmt.Printf("[!] Warning: %s not found; skipping image pull.\n", env)
		return
	}

	// https://community.openpanel.org/d/239-no-such-container-openlitespeed
	getEnv := func(key string) string {
		var vals []string
		for _, l := range conf.Lines(env) {
			if strings.HasPrefix(l, key+"=") {
				vals = append(vals, l[len(key)+1:])
			}
		}
		return strings.NewReplacer("\r", "", `"`, "", "'", "").Replace(strings.Join(vals, "\n"))
	}
	sqlType := getEnv("MYSQL_TYPE")
	ws := getEnv("WEB_SERVER")
	php := getEnv("DEFAULT_PHP_VERSION")
	varnish := getEnv("PROXY_HTTP_PORT")
	phpSvc := "php-fpm-" + php

	if !conf.IsFile(addAutostartFile) {
		fmt.Printf("[!] Warning: %s not found; skipping image pull.\n", addAutostartFile)
		return
	}
	var autostart []string
	blank := regexp.MustCompile(`^\s*#|^\s*$`)
	for _, l := range strings.Split(strings.ReplaceAll(conf.Read(addAutostartFile), "\r", ""), "\n") {
		if !blank.MatchString(l) {
			autostart = append(autostart, l)
		}
	}
	all := " " + strings.Join(autostart, " ") + " "
	has := func(s string) bool { return strings.Contains(all, " "+s+" ") }

	var images []string
	ols := ws == "openlitespeed" || ws == "litespeed"
	if !ols && regexp.MustCompile(`^[0-9]+\.[0-9]+$`).MatchString(php) && has(phpSvc) {
		images = append(images, phpSvc)
	}
	if (sqlType == "mysql" || sqlType == "mariadb") && has(sqlType) {
		images = append(images, sqlType)
	}
	if varnish != "" && has("varnish") {
		images = append(images, "varnish")
	}
	if regexp.MustCompile(`^(nginx|apache|openresty|openlitespeed|litespeed)$`).MatchString(ws) && has(ws) {
		images = append(images, ws)
	}
	sqlWs := " mysql mariadb nginx apache openresty openlitespeed litespeed "
	for _, svc := range autostart {
		if strings.Contains(sqlWs, " "+svc+" ") {
			continue // only webserver and sql type for user, ignore others
		}
		if ols && strings.HasPrefix(svc, "php-fpm-") {
			continue // OLS/LS manages PHP internally, no need to start php-fpm services
		}
		if strings.HasPrefix(svc, "php-fpm-") || svc == "varnish" {
			continue // only current default php version and varnish if enabled
		}
		images = append(images, svc)
	}
	if len(images) == 0 {
		return
	}
	list := strings.Join(images, " ")
	j.log("Starting services in background: %s", list)
	// detached so it outlives us, waits for the socket to answer 3 times in a row and retries each service like podman_compose_user_retry
	sock := "unix:///hostfs/run/user/" + j.userID + "/podman/podman.sock"
	u := j.username
	script := `e=0; ok=0; while [[ $e -lt 60 ]]; do if CONTAINER_HOST=` + sock + ` podman --remote info >/dev/null 2>&1; then ((ok++)); [[ $ok -ge 3 ]] && break; else ok=0; fi; sleep 3; ((e+=3)); done; cd /home/` + u + `/; for svc in ` + list + `; do for try in 1 2 3; do CONTAINER_HOST=` + sock + ` podman-compose -f /home/` + u + `/docker-compose.yml up -d $svc && break; (( try < 3 )) && sleep $(( try * 2 )); done; done`
	logf, err := os.Create("/tmp/autostart_" + u + ".log")
	if err != nil {
		return
	}
	defer logf.Close()
	sh.C("bash", "-c", script).Stdin("").Stdout(logf).Stderr(logf).Background()
}
