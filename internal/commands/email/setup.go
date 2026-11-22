// ##############################################################################
// Script Name: internal/commands/email/setup.go
// Description: Setup email addresses, forwarders, filters..
// Usage: opencli email-setup <COMMAND> <ATTRIBUTES>
// Author: Stefan Pejcic
// Created: 18.08.2024
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

package email

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/redis"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "email-setup",
		Desc:  "Setup email addresses, forwarders, filters..",
		Usage: "opencli email-setup <COMMAND> <ATTRIBUTES>",
		Docs:  "https://docs.openpanel.com",
		Run:   setup,
	})
	cli.Register(&cli.Command{
		Name:   "email-setup-bg",
		Desc:   "Detached steps of email-setup.",
		Usage:  "opencli email-setup-bg <start-mailserver|chown|welcome> ARGS...",
		Hidden: true,
		Run:    setupBg,
	})
}

var (
	suFillNewRe = regexp.MustCompile(`^\* ([^ ]+) \((  /  \) \[%\]| 0 / ~ \) \[0%\])$`)
	suFillOldRe = regexp.MustCompile(`^\* ([^ ]+) \( ([^ ]+) / ([^ ]+) \) \[[0-9]+%\]$`)
	suDigitsRe  = regexp.MustCompile(`^[0-9]+$`)
	suSubcmdRe  = regexp.MustCompile(`^(add|del)$`)
)

// sa returns the nth arg or empty, like an unset $n
func sa(a []string, i int) string {
	if i < len(a) {
		return a[i]
	}
	return ""
}

func numfmt(args ...string) (string, bool) {
	out, err := sh.C("numfmt", args...).NoErr().Out()
	return out, err == nil
}

func suQuotaLimit(email string) string {
	for _, l := range strings.Split(conf.Read(suQuotas), "\n") {
		if strings.HasPrefix(l, email+":") {
			f := strings.Split(l, ":")
			if len(f) > 1 {
				return f[1]
			}
			return ""
		}
	}
	return ""
}

// suFill rewrites the quota column of accounts dovecot hasn't fully loaded yet
func suFill(in string) string {
	var out strings.Builder
	for _, line := range strings.SplitAfter(in, "\n") {
		if !strings.HasSuffix(line, "\n") {
			break
		}
		line = strings.TrimSuffix(line, "\n")
		if m := suFillNewRe.FindStringSubmatch(line); m != nil {
			limit, ok := numfmt("--from=iec", "--to=iec", suQuotaLimit(m[1]))
			if !ok {
				limit = ""
			}
			if limit == "" {
				limit = "~"
			}
			line = "* " + m[1] + " ( 0 / " + limit + " ) [0%]"
		} else if m := suFillOldRe.FindStringSubmatch(line); m != nil {
			// a changed quota also shows the old limit for a few seconds, the quotas file already has the new one
			email, used := m[1], m[2]
			if limit := suQuotaLimit(email); limit != "" {
				usedB, _ := numfmt("--from=iec", used)
				limitB, _ := numfmt("--from=iec", limit)
				if suDigitsRe.MatchString(usedB) && suDigitsRe.MatchString(limitB) {
					u, _ := strconv.ParseInt(usedB, 10, 64)
					l, _ := strconv.ParseInt(limitB, 10, 64)
					if l > 0 {
						h, _ := numfmt("--to=iec", limitB)
						line = fmt.Sprintf("* %s ( %s / %s ) [%d%%]", email, used, h, u*100/l)
					}
				}
			}
		}
		out.WriteString(line + "\n")
	}
	return out.String()
}

// runSetupCommand runs the setup command in the container, or straight against the config files if it's not up
func runSetupCommand(args []string, w io.Writer) int {
	if podman.CanExec(container) {
		rc := sh.Code(sh.C("podman", append([]string{"exec", container, "setup"}, args...)...).Stdout(w).Run())
		// 125 and up is podman failing to run it (container died mid exec), not setup saying no
		if rc < 125 {
			return rc
		}
	}
	suEprintf("Warning: '%s' is not running - writing directly to mailserver config files instead.\n", container)

	var rest []string
	if len(args) > 2 {
		rest = args[2:]
	}
	switch sa(args, 0) + " " + sa(args, 1) {
	case "email add":
		if rc := suEmailAdd(rest); rc != 0 {
			return rc
		}
		// account is on disk now, dms finds it by itself if it's only in its no-accounts restart loop, still no exec after that means the container is dead
		sh.Opencli("email-setup-bg", "start-mailserver").Quiet().Background()
		return 0
	case "email update":
		return suEmailUpdate(rest)
	case "email del":
		return suEmailDel(rest)
	case "email list":
		return suEmailList(w)
	case "quota set":
		return suQuotaSet(rest)
	case "quota del":
		return suQuotaDel(rest)
	}
	suEprintf("Error: '%s %s' requires the mailserver container to be running, and it currently is not.\n", sa(args, 0), sa(args, 1))
	return 1
}

// first line, second field to the end like `read -r _ owner`
func suOwner(who string) string {
	line := strings.SplitN(who, "\n", 2)[0]
	line = strings.TrimLeft(line, " \t")
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return ""
	}
	return strings.Trim(line[i:], " \t")
}

type suState struct {
	user, domain, owner, opUID string
}

func (s *suState) lookup(email string) {
	if i := strings.LastIndex(email, "@"); i >= 0 {
		s.user = email[:i]
	} else {
		s.user = email
	}
	if i := strings.Index(email, "@"); i >= 0 {
		s.domain = email[i+1:]
	} else {
		s.domain = email
	}
	who, _ := sh.Opencli("domains-whoowns", s.domain, "--context").Out()
	s.owner = suOwner(who)
	if s.owner != "" {
		var st syscall.Stat_t
		if err := syscall.Stat("/home/"+s.owner, &st); err != nil {
			fmt.Fprintf(os.Stderr, "stat: cannot statx '/home/%s': No such file or directory\n", s.owner)
		} else {
			s.opUID = strconv.Itoa(int(st.Uid))
		}
	}
}

func (s *suState) reloadEmailsFile() {
	if s.owner == "" {
		return
	}
	file := conf.UsersDir + "/" + s.owner + "/emails.yml"
	domains, _ := sh.Opencli("domains-user", s.owner).Out()
	// a failed list must not wipe the file
	allEmails, err := sh.Opencli("email-setup", "email", "list").NoErr().Out()
	if err != nil {
		return
	}
	var b strings.Builder
	for _, d := range strings.Split(domains, "\n") {
		for _, l := range grepAt(allEmails, d) {
			b.WriteString(l + "\n")
		}
	}
	os.WriteFile(file, []byte(b.String()), 0o644)
	// the panel memoizes the list from this file for 1h (GetEmailList), drop it so /emails shows the change now
	redis.DropKey("openpanel_cache_email_list:" + s.owner)
}

func setup(args []string) int {
	if len(args) < 1 {
		fmt.Println("Usage: opencli email-setup <command> [<args>...]")
		fmt.Println("Docs: https://openpanel.com/docs/articles/opencli/email")
		return 1
	}
	emailGate()

	// --send-welcome is ours, keep it away from the mailserver's setup
	sendWelcome := false
	var a []string
	for _, x := range args {
		if x == "--send-welcome" {
			sendWelcome = true
		} else {
			a = append(a, x)
		}
	}
	a1, a2, a3 := sa(a, 0), sa(a, 1), sa(a, 2)
	pair := a1 + " " + a2

	var rc int
	if pair == "email list" {
		// stdout goes through the quota fixer, so run it into a buffer first
		var buf strings.Builder
		rc = runSetupCommand(a, &buf)
		fmt.Print(suFill(buf.String()))
	} else {
		rc = runSetupCommand(a, os.Stdout)
	}

	// the access maps are texthash so postfix only rereads them on reload
	if pair == "email restrict" && (a3 == "add" || a3 == "del") && rc == 0 && podman.CanExec(container) {
		sh.C("podman", "exec", container, "postfix", "reload").Quiet().Run()
	}

	if pair == "email add" && rc == 0 && !suHasKey(suAccounts, strings.ToLower(a3), "|", true) {
		suEprintf("Error: '%s' was not saved to the mailserver accounts file.\n", a3)
		rc = 1
	}

	st := &suState{}
	if rc == 0 && (a1 == "email" && suAddUpdDel(a2) || a1 == "quota" && (a2 == "set" || a2 == "del")) {
		target := a3
		// 'email del -y|-n <email>': the address comes after the flag
		if pair == "email del" && regexp.MustCompile(`^-[yYnN]$`).MatchString(a3) {
			target = sa(a, 3)
		}
		// docker-mailserver stores 'Name@domain' as 'name@domain'
		target = strings.ToLower(target)
		if suEmailRe.MatchString(target) {
			st.lookup(target)
			if (a2 == "add" || a2 == "update") && st.opUID != "" && suDigitsRe.MatchString(st.opUID) {
				suSetUID(target, st.opUID)
				sh.Opencli("email-setup-bg", "chown", st.opUID, st.domain, st.user).Quiet().Background()
			}
			// reload the cached user file so the OpenPanel UI picks up email/quota add/del changes
			if a2 != "update" && sa(a, 4) != "--wait" {
				st.reloadEmailsFile()
			}
		}
	}

	// dms copies the dkim keys into /etc/opendkim only when the container starts, so new keys signed nothing until a mailserver restart
	if rc == 0 && pair == "config dkim" && podman.CanExec(container) {
		script := `
        [[ -d /tmp/docker-mailserver/opendkim/keys ]] || exit 0
        cp -a /tmp/docker-mailserver/opendkim/* /etc/opendkim/ \
            && chown -R opendkim:opendkim /etc/opendkim/ \
            && chmod -R 0700 /etc/opendkim/keys/ \
            && { ! supervisorctl status opendkim 2>/dev/null | grep -q RUNNING || supervisorctl restart opendkim; }`
		if sh.C("podman", "exec", container, "bash", "-c", script).Quiet().Run() != nil {
			suEprintf("Warning: the DKIM key was saved, but the running mailserver did not load it. Restart the mailserver to start signing with it.\n")
		}
	}

	// catch-all rules leave out every mailbox and alias, so they follow those
	if rc == 0 && (a1 == "email" || a1 == "alias") && suSubcmdRe.MatchString(a2) {
		// the account is saved by now and the panels read any stdout as a failed add
		sh.Opencli("email-default_sync").NoOut().Run()
	}

	if sendWelcome && a1 == "email" && a2 == "add" && rc == 0 && suEmailRe.MatchString(a3) {
		// panel treats any stdout as failure, so this runs detached and silent
		sh.Opencli("email-setup-bg", "welcome", a3, st.opUID).Quiet().Background()
	}
	return rc
}

func suAddUpdDel(s string) bool { return s == "add" || s == "update" || s == "del" }

// suSetUID is the sed that keeps the first two fields of the account line and sets the uid as the third
func suSetUID(email, uid string) {
	suEdit(suAccounts, func(l string) (string, bool) {
		if !strings.HasPrefix(l, email+"|") {
			return l, true
		}
		f := strings.Split(l, "|")
		if len(f) < 2 {
			f = append(f, "")
		}
		// s/^\([^|]*|[^|]*\).*/\1|UID/
		return f[0] + "|" + f[1] + "|" + uid, true
	})
}
