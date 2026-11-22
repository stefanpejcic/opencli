// ##############################################################################
// Script Name: internal/commands/useracct/delete.go
// Description: Delete user account and permanently remove all their data.
// Usage: opencli user-delete <username> [-y]
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
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/redis"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-delete",
		Desc:  "Delete user account and permanently remove all their data.",
		Usage: "opencli user-delete <username> [-y]",
		Run:   del,
	})
}

// isTTY tells if stdin is a terminal, read -p only shows its prompt then
func isTTY(f *os.File) bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

type delJob struct {
	username, userID, context string
}

func del(args []string) int {
	if len(args) < 1 || len(args) > 2 {
		fmt.Println("Usage: opencli user-delete <username> [-y]")
		return 1
	}
	username := args[0]
	skip := len(args) > 1 && args[1] == "-y"
	if username == "" {
		fmt.Println("ERROR: Username is required.")
		return 1
	}
	if i := strings.LastIndexByte(username, '_'); i >= 0 {
		username = username[i+1:]
	}

	// 1. -y
	if !skip {
		if isTTY(os.Stdin) {
			fmt.Fprintf(os.Stderr, "This will permanently delete user '%s' and all associated data. Confirm? [Y/n]: ", username)
		}
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		line = strings.ToLower(strings.TrimSuffix(line, "\n"))
		if !regexp.MustCompile(`^(yes|y| )`).MatchString(line) {
			fmt.Printf("Operation canceled for user '%s'.\n", username)
			return 0
		}
	}
	db.Require()
	j := &delJob{username: username}

	// 2. get docker context and user ID from database
	j.userInfo()

	// 3. in parallel: delete emails, delete ftp accounts, user/sites/domains from database, homedir, postfwd limits
	j.deleteEmails()
	var wg sync.WaitGroup
	for _, f := range []func(){j.deleteFTP, j.deleteFromDB, j.deleteFiles} {
		wg.Add(1)
		go func(f func()) { defer wg.Done(); f() }(f)
	}
	j.deleteSystemUser() // NOT DETACHED TO TEST FOR https://github.com/stefanpejcic/OpenPanel/issues/1023
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Delete hourly email limits for the user
		sh.Opencli("email-ratelimit", "--delete-user="+username).Quiet().Run()
	}()

	// 4. wait for all of the above functions to finish
	wg.Wait()

	// 5. notify
	fmt.Printf("User %s deleted successfully.\n", username)
	sh.Opencli("sentinel", "--action=user_delete", "--title=User account deleted", "--message=User account '"+username+"' has been deleted.").Quiet().Background()

	// 6. refresh resellers usage
	delRefreshResellers()
	sh.Opencli("user-quota").Quiet().Background()
	return 0
}

func (j *delJob) userInfo() {
	esc := db.Escape(j.username)
	// exact name or SUSPENDED_<14 digit timestamp>_<name>, exact name first
	out, _ := db.Cmd(`
        SELECT id, server FROM users
        WHERE username='`+esc+`'
           OR (username REGEXP '^SUSPENDED_[0-9]{14}_' AND SUBSTRING(username, 26) = '`+esc+`')
        ORDER BY username='`+esc+`' DESC
        LIMIT 1;
    `, "-N").Out()
	f := strings.Fields(text.FirstLine(out))
	if len(f) > 0 {
		j.userID = f[0]
	}
	if len(f) > 1 {
		j.context = strings.Join(f[1:], " ")
	}
	if j.userID == "" {
		cli.Fail("ERROR: User '%s' not found in the database.", j.username)
	}
	if j.context == "" {
		cli.Fail("ERROR: Could not determine podman context for '%s'.", j.username)
	}
}

func (j *delJob) deleteFromDB() {
	// 1. Get all domain IDs and URLs
	out, _ := db.Cmd(`
	SELECT 
	    GROUP_CONCAT(domain_id) AS ids,
	    GROUP_CONCAT(domain_url) AS urls
	FROM domains
	WHERE user_id='`+j.userID+`';
	`, "-N").Out()
	f := strings.Fields(text.FirstLine(out))
	ids, urls := "", ""
	if len(f) > 0 {
		ids = f[0]
	}
	if len(f) > 1 {
		urls = strings.Join(f[1:], " ")
	}

	// 2. tokens and passkeys first and on their own, the tables may not exist yet and that must not stop the delete
	for _, t := range []string{"mcp_tokens", "user_passkeys"} {
		db.Cmd("DELETE FROM " + t + " WHERE user_id='" + j.userID + "';").NoErr().Run()
	}
	sql := ""
	if ids != "" {
		sql += "DELETE FROM sites WHERE domain_id IN (" + ids + "); "
	}
	sql += "DELETE FROM domains WHERE user_id='" + j.userID + "'; "
	sql += "DELETE FROM users WHERE id='" + j.userID + "';"
	db.Cmd(sql).Run()

	// 3. terminate redis sessions
	redis.DropUserSessions(j.userID)
	redis.DropUserCache(j.userID, j.username)
	// the account is gone, so drop every cached key under its name, otherwise a new user with the same name sees the old data
	redis.DropPattern("openpanel_cache_*:"+j.username, "openpanel_cache_*:"+j.username+":*")

	// 4. delete domain files, emails and reload Caddy
	os.RemoveAll("/var/log/caddy/stats/" + j.username) // goaccess reports
	if urls == "" {
		return
	}
	storage, _ := conf.Raw(conf.AdminINI, "email_storage_location")
	storage = text.Xargs(storage)
	var paths []string
	for _, d := range strings.Split(urls, ",") {
		if strings.HasPrefix(storage, "/") && storage != "user_dir" {
			paths = append(paths, storage+"/"+d)
		}
		paths = append(paths,
			"/etc/openpanel/caddy/domains/"+d+".conf",
			"/etc/openpanel/caddy/suspended_domains/"+d+".conf",
			"/var/log/caddy/domlogs/"+d,
			"/var/log/caddy/coraza_waf/"+d+".log",
			"/etc/openpanel/caddy/ssl/custom/"+d,
			"/etc/bind/zones/"+d+".zone",
		)
		named := "/etc/bind/named.conf.local"
		if strings.Contains(conf.Read(named), `zone "`+d+`"`) {
			start, err1 := regexp.Compile(`zone "` + d + `" IN {`)
			end := regexp.MustCompile(`};`)
			if err1 == nil {
				in := false
				var keep []string
				for _, l := range conf.Lines(named) {
					if !in && start.MatchString(l) {
						in = true
						continue
					}
					if in {
						if end.MatchString(l) {
							in = false
						}
						continue
					}
					keep = append(keep, l)
				}
				out := strings.Join(keep, "\n")
				if len(keep) > 0 {
					out += "\n"
				}
				conf.WriteFileKeep(named, out, false)
			}
		}
	}
	for _, p := range paths {
		os.RemoveAll(p)
	}
	// reload webserver and dns
	sh.C("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").Quiet().Background()
	sh.C("podman", "exec", "openpanel_dns", "rndc", "reconfig").Quiet().Background()
}

// field2 is `read -r _ x _`, the second whitespace separated word
func field2(l string) (string, string) {
	f := strings.Fields(l)
	a, b := "", ""
	if len(f) > 1 {
		a = f[1]
	}
	if len(f) > 2 {
		b = strings.Join(f[2:], " ")
	}
	return a, b
}

func sortUniq(in []string) []string {
	// printf '%s\n' of an empty array still prints one empty line
	if len(in) == 0 {
		return []string{""}
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func (j *delJob) deleteEmails() {
	core := conf.UsersDir + "/" + j.username
	var domains, emails []string
	if conf.IsFile(core + "/emails.yml") {
		for _, l := range conf.Lines(core + "/emails.yml") {
			if e, _ := field2(l); e != "" {
				emails = append(emails, e)
				d := e
				if i := strings.IndexByte(e, '@'); i >= 0 {
					d = e[i+1:]
				}
				if d != "" {
					domains = append(domains, d)
				}
			}
		}
	}

	// emails.yml is only rewritten when /emails is opened, so also catch live mailboxes on the user's domains
	dbDomains, _ := db.Cmd("SELECT domain_url FROM domains WHERE user_id='"+j.userID+"';", "-N").NoErr().Out()
	if dbDomains != "" {
		own := map[string]bool{}
		for _, d := range strings.Split(strings.ToLower(dbDomains), "\n") {
			own[d] = true
		}
		list := sh.Opencli("email-setup", "email", "list").NoErr().Str()
		for _, l := range text.Lines(list) {
			f := strings.Fields(l)
			if len(f) < 2 || f[0] != "*" {
				continue
			}
			p := strings.Split(strings.ToLower(f[1]), "@")
			if len(p) > 1 && own[p[1]] {
				if !strings.Contains(" "+strings.Join(emails, " ")+" ", " "+f[1]+" ") {
					emails = append(emails, f[1])
				}
			}
		}
		for _, d := range strings.Split(dbDomains, "\n") {
			if d != "" {
				domains = append(domains, d)
			}
		}
	}

	// not backgrounded, a detached del got lost and left the mailboxes behind
	if len(emails) > 0 {
		sh.Opencli(append([]string{"email-setup", "email", "del", "-y"}, emails...)...).Quiet().Run()
	}
	domains = sortUniq(domains)

	// aliases
	if aliasesFile := core + "/aliases.yml"; conf.IsFile(aliasesFile) {
		var aliases []string
		for _, l := range conf.Lines(aliasesFile) {
			if e, _ := field2(l); e != "" {
				aliases = append(aliases, e)
				d := e
				if i := strings.IndexByte(e, '@'); i >= 0 {
					d = e[i+1:]
				}
				if d != "" {
					domains = append(domains, d)
				}
			}
		}
		if len(aliases) > 0 {
			sh.Opencli(append([]string{"email-setup", "alias", "del"}, aliases...)...).Quiet().Background()
		}
	}
	domains = sortUniq(domains)

	// regex aliases (default email address / catch-all)
	regexFile := "/usr/local/mail/openmail/docker-data/dms/config/postfix-regex.cf"
	if conf.IsFile(regexFile) && len(domains) > 0 {
		var keep []string
		changed := false
		for _, l := range conf.Lines(regexFile) {
			f := strings.Fields(l)
			if len(f) == 0 {
				continue
			}
			pattern := f[0]
			target := strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(l, " \t"), pattern))
			matched := false
			for _, d := range domains {
				if strings.ToLower(pattern) == "/*@"+strings.ToLower(d)+"/" {
					matched = true
					break
				}
			}
			if matched {
				changed = true
			} else {
				keep = append(keep, pattern+" "+target)
			}
		}
		if changed {
			out := ""
			for _, k := range keep {
				out += k + "\n"
			}
			tmp := regexFile + ".opencli-tmp"
			if os.WriteFile(tmp, []byte(out), 0o600) == nil && os.Rename(tmp, regexFile) == nil {
				sh.Opencli("email-default_sync").Run()
			}
		}
	}
}

func (j *delJob) deleteFTP() {
	dir := "/etc/openpanel/ftp/users/" + j.context
	if conf.IsDir(dir) {
		if conf.IsFile(dir + "/users.list") {
			for _, l := range conf.Lines(dir + "/users.list") {
				u, _, _ := strings.Cut(l, "|")
				u = strings.TrimLeft(u, " \t")
				if u != "" {
					sh.C("podman", "exec", "openadmin_ftp", "deluser", u).Run()
				}
			}
		}
		os.RemoveAll(dir)
	}
	if sh.C("podman", "exec", "openadmin_ftp", "getent", "group", j.context).Quiet().OK() {
		sh.C("podman", "exec", "openadmin_ftp", "delgroup", j.context).Quiet().Run()
	}
}

func (j *delJob) deleteFiles() {
	if conf.IsDir("/home/" + j.context) {
		os.RemoveAll("/home/" + j.context)
	}
	os.RemoveAll(filepath.Join(conf.UsersDir, j.context))
	// differs from the context after user-rename
	if j.username != "" {
		os.RemoveAll(filepath.Join(conf.UsersDir, j.username))
	}
}

func (j *delJob) deleteSystemUser() {
	ctx := j.context
	uid := sysUID(ctx)

	// stop the user's systemd and podman first, with linger on they'd keep /run/user/<uid> alive
	sh.C("loginctl", "disable-linger", ctx).NoErr().Run()
	if text.IsInt(uid) {
		sh.C("systemctl", "stop", "user@"+uid+".service").NoErr().Run()
	}

	// kill by both real and effective/saved uid, then loop until gone
	sh.C("pkill", "-9", "-u", ctx).NoErr().Run()
	for i := 0; i < 10; i++ {
		if !sh.C("pgrep", "-u", ctx).Quiet().OK() {
			break
		}
		sh.C("pkill", "-9", "-u", ctx).NoErr().Run()
		time.Sleep(250 * time.Millisecond)
	}

	// last resort: still processes? force with loginctl / kill by name lookup
	if sh.C("pgrep", "-u", ctx).Quiet().OK() {
		sh.C("loginctl", "terminate-user", ctx).NoErr().Run()
		time.Sleep(time.Second)
		sh.C("pkill", "-9", "-u", ctx).NoErr().Run()
	}

	if sh.Has("userdel") {
		sh.C("userdel", "-f", "-r", ctx).Run() // -f forces even if logged in / process running
	} else if sh.Has("deluser") {
		sh.C("deluser", "--remove-home", ctx).Run()
	}

	if text.IsInt(uid) && text.Atoi(uid) >= 1000 {
		// a new account that gets this uid would inherit the old podman network state from here and its containers wouldn't start
		os.RemoveAll("/run/user/" + uid)
		// drop the Delegate= slice override add.sh created, so an account that reuses this uid doesn't inherit it
		os.RemoveAll("/etc/systemd/system/user-" + uid + ".slice.d")
	}
	// if the stop timed out or the pkill above hit the user manager, user@<uid> stays failed and the server shows degraded
	if text.IsInt(uid) {
		sh.C("systemctl", "reset-failed", "user@"+uid+".service", "user-runtime-dir@"+uid+".service").NoErr().Run()
	}
}

func delRefreshResellers() {
	// todo: optimize, this checks all resellers instead of just the account owner
	files, _ := filepath.Glob("/etc/openpanel/openadmin/resellers/*.json")
	for _, f := range files {
		if !conf.IsFile(f) {
			continue
		}
		r := strings.TrimSuffix(filepath.Base(f), ".json")
		n, err := db.Cmd("SELECT COUNT(*) FROM users WHERE owner='"+db.Escape(r)+"';", "-N").Out()
		if err == nil {
			jqSetNumber(f, "current_accounts", n)
		}
	}
}
