// ##############################################################################
// Script Name: internal/commands/php/domain.go
// Description: View or change the PHP version used for a single domain name.
// Usage: opencli php-domain <DOMAIN_NAME> [--update <PHP_VERSION>]
// Author: Stefan Pejcic
// Created: 07.10.2023
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

package php

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/podman"
	"opencli/internal/sh"
	"opencli/internal/tenantfs"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "php-domain",
		Desc:  "View or change the PHP version used for a single domain name.",
		Usage: "opencli php-domain <DOMAIN_NAME> [--update <PHP_VERSION>]",
		Run:   domain,
	})
	// the bash script detached this step with nohup so it outlives the command
	cli.Register(&cli.Command{
		Name:   "php-domain-bg",
		Desc:   "Internal helper for php-domain.",
		Usage:  "opencli php-domain-bg <CONTEXT> <WEBSERVER> <NEW_SERVICE> <STOP_OLD> <OWNER>",
		Hidden: true,
		Run:    domainBg,
	})
}

var (
	phpFpmRe    = regexp.MustCompile(`php-fpm-[0-9.]+`)
	versionOnly = regexp.MustCompile(`[0-9.]+`)
	webServerRe = regexp.MustCompile(`nginx|openresty|apache|openlitespeed|litespeed|caddy`)
)

// awkSecond is awk -F '=' '{print $2}' over every line, joined like the lines would be
func awkSecond(lines []string) string {
	var out []string
	for _, l := range lines {
		p := strings.Split(l, "=")
		if len(p) > 1 {
			out = append(out, p[1])
		} else {
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}

func domain(args []string) int {
	dom := cli.Arg(args, 0)
	if len(args) < 1 {
		fmt.Println("Usage: opencli php-domain <domain> [--update <new_php_version>]")
		return 1
	}
	update := false
	newVer := ""
	if cli.Arg(args, 1) == "--update" {
		v := cli.Arg(args, 2)
		if v == "" {
			fmt.Println("Error: --update flag requires a new PHP version in the format number.number.")
			return 1
		}
		if !versionRe.MatchString(strings.TrimSuffix(v, "\n")) {
			fmt.Println("Invalid PHP version format. Please use the format 'number.number' (e.g., 8.1 or 5.6).")
			return 1
		}
		update = true
		newVer = v
	}

	// awk treats a separator longer than one char as a regex, so the dots in the domain match anything
	owner := ""
	who, _ := sh.Opencli("domains-whoowns", dom).Out()
	if re, err := regexp.Compile("Owner of '" + dom + "': "); err == nil {
		var parts []string
		for _, l := range strings.Split(who, "\n") {
			f := re.Split(l, -1)
			if len(f) > 1 {
				parts = append(parts, f[1])
			} else {
				parts = append(parts, "")
			}
		}
		owner = strings.TrimRight(strings.Join(parts, "\n"), "\n")
	}
	if owner == "" {
		fmt.Fprintf(os.Stderr, "Failed to determine the owner of the domain '%s'.\n", dom)
		return 1
	}

	db.Require()
	context := db.Plain("SELECT server FROM users WHERE username = '"+db.Escape(owner)+"'", "-D", db.Database, "-s", "-N").NoErr().Str()
	if context == "" {
		context = owner
	}

	envFile := "/home/" + context + "/.env"
	if !conf.Exists(envFile) {
		fmt.Fprintf(os.Stderr, "grep: %s: No such file or directory\n", envFile)
	}
	var wsLines []string
	for _, l := range conf.Lines(envFile) {
		if strings.HasPrefix(l, "WEB_SERVER=") {
			wsLines = append(wsLines, l)
		}
	}
	ws := text.NoSpace(awkSecond(wsLines))
	if len(ws) >= 2 && ws[0] == '"' && ws[len(ws)-1] == '"' {
		ws = ws[1 : len(ws)-1]
	}
	ws = webServerRe.FindString(text.FirstLine(ws))
	if ws == "openlitespeed" || ws == "litespeed" {
		fmt.Printf("ERROR: PHP version can not be changed on %s webserver. Instead you need to change the docker image tag for the user.\n", ws)
		fmt.Printf("Available tags: https://hub.docker.com/r/litespeedtech/%s/tags\n", ws)
		return 0
	}

	vhost := "/home/" + context + "/docker-data/volumes/" + context + "_webserver_data/_data/" + dom + ".conf"
	// caddy mode keeps the php socket in the caddy conf, there's no vhost
	if ws == "caddy" {
		vhost = "/etc/openpanel/caddy/domains/" + dom + ".conf"
	}
	b, err := os.ReadFile(vhost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "grep: %s: No such file or directory\n", vhost)
	}
	oldVer := ""
	if m := phpFpmRe.FindString(string(b)); m != "" {
		oldVer = versionOnly.FindString(m)
	}
	if oldVer == "" {
		fmt.Fprintf(os.Stderr, "Failed to determine the PHP version for the domain '%s' (owned by user %s).\n", dom, owner)
		return 1
	}

	if !update {
		fmt.Printf("Domain '%s' (owned by user: %s) uses PHP version: %s\n", dom, owner, oldVer)
		return 0
	}

	// 1. replace in domain vhost file
	if ws == "caddy" {
		conf.SedFile(vhost, func(lines []string) []string {
			for i, l := range lines {
				lines[i] = phpFpmRe.ReplaceAllLiteralString(l, "php-fpm-"+newVer)
			}
			return lines
		})
	} else {
		tenantfs.SedI(tenantfs.Volume(context, "webserver_data"), vhost, "s/php-fpm-[0-9.]\\+/php-fpm-"+newVer+"/g")
	}

	// 2. start new php version container if not running
	ownerUID := ""
	if st, err := os.Stat("/home/" + owner); err == nil {
		ownerUID = strconv.Itoa(int(st.Sys().(*syscall.Stat_t).Uid))
	} else {
		fmt.Fprintf(os.Stderr, "stat: cannot statx '/home/%s': No such file or directory\n", owner)
	}
	sock := "unix:///hostfs/run/user/" + ownerUID + "/podman/podman.sock"
	// up -d would recreate it if another domain already runs this version
	if !podman.CtxIsRunning(owner, "php-fpm-"+newVer) {
		sh.C("podman-compose", "up", "-d", "php-fpm-"+newVer).Dir("/home/" + owner).Env("CONTAINER_HOST=" + sock).Quiet().Background()
	}

	// 3. previous version gets stopped if not used by other domains AND not set as default
	var defLines []string
	for _, l := range conf.Lines(envFile) {
		if strings.Contains(l, "DEFAULT_PHP_VERSION") {
			defLines = append(defLines, l)
		}
	}
	defVer := text.NoSpace(awkSecond(defLines))
	stopOld := ""
	if defVer != "" && oldVer != defVer {
		if ws == "caddy" {
			if !grepTree("/etc/openpanel/caddy/domains/", func(s string) bool {
				return strings.Contains(s, "/home/"+context+"/sockets/php/php-fpm-"+oldVer+"/")
			}) {
				stopOld = "php-fpm-" + oldVer
			}
		} else {
			// grep without -F, so the dots in the version match any char
			re := regexp.MustCompile("php-fpm-" + oldVer)
			if !grepTree("/home/"+context+"/docker-data/volumes/"+context+"_webserver_data/_data/", re.MatchString) {
				stopOld = "php-fpm-" + oldVer
			}
		}
	}

	// 4. reload webserver once the new php-fpm listens, and only then stop the old one so the site never points at a stopped container
	sh.Opencli("php-domain-bg", context, ws, "php-fpm-"+newVer, stopOld, owner).Quiet().Background()

	// 5. save in database
	db.Plain("UPDATE domains SET php_version='" + db.Escape(newVer) + "' WHERE domain_url='" + db.Escape(dom) + "';").Run()

	// 6. jobs of this domain that ran in the old php-fpm move to the new one, same as the PHP version switch in the user panel
	docroot := db.Plain("SELECT docroot FROM domains WHERE domain_url='"+db.Escape(dom)+"'", "-s", "-N").NoErr().Str()
	cronsFile := "/home/" + context + "/crons.ini"
	if conf.IsFile(cronsFile) {
		moved := moveCrons(cronsFile, dom, strings.TrimSuffix(docroot, "/"), "php-fpm-"+oldVer, "php-fpm-"+newVer)
		if moved > 0 {
			if podman.CtxIsRunning(owner, "cron") {
				podman.Ctx(owner, "restart", "cron").Quiet().Run()
			}
			fmt.Printf("Moved %d cron job(s) of %s from php-fpm-%s to php-fpm-%s\n", moved, dom, oldVer, newVer)
		}
	}

	fmt.Println("Updated PHP version in the configuration file to " + newVer)
	return 0
}

func domainBg(args []string) int {
	ctx, ws, svc, stopOld, owner := cli.Arg(args, 0), cli.Arg(args, 1), cli.Arg(args, 2), cli.Arg(args, 3), cli.Arg(args, 4)
	if !podman.ReloadWebserver(ctx, ws, svc) {
		return 1
	}
	if stopOld != "" {
		if os.Chdir("/home/"+owner) == nil {
			podman.ComposeCtx(owner, "down", stopOld).Quiet().Run()
		}
	}
	return 0
}

// grepTree is grep -rq over *.conf files, symlinks are skipped
func grepTree(dir string, match func(string) bool) bool {
	found := false
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found || !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".conf") {
			return nil
		}
		if b, e := os.ReadFile(p); e == nil && match(string(b)) {
			found = true
		}
		return nil
	})
	return found
}

func isBlank(s string) bool { return strings.Trim(s, " \t") == "" }

// awkHas finds needle in s with an allowed char before and after, hostonly wants "host:" or "://" in front
func awkHas(s, needle, before, after string, hostonly bool) bool {
	if needle == "" {
		return false
	}
	p := 0
	for {
		i := strings.Index(s[p:], needle)
		if i < 0 {
			return false
		}
		p += i + 1
		b, a := "", ""
		if p > 1 {
			b = s[p-2 : p-1]
		}
		if end := p - 1 + len(needle); end < len(s) {
			a = s[end : end+1]
		}
		if (b == "" || strings.Contains(before, b)) && (a == "" || strings.Contains(after, a)) {
			if !hostonly {
				return true
			}
			pre := strings.TrimRight(strings.ToLower(s[:p-1]), " \t")
			if strings.HasSuffix(pre, "host:") || strings.HasSuffix(pre, "://") {
				return true
			}
		}
	}
}

var (
	contLineRe = regexp.MustCompile(`^#?[ \t]*container[ \t]*=`)
	hashRe     = regexp.MustCompile(`^#[ \t]*`)
	contKeyRe  = regexp.MustCompile(`^container[ \t]*=`)
	cmdKeyRe   = regexp.MustCompile(`^command[ \t]*=`)
)

// moveCrons rewrites the container line of this domain's jobs, returns how many it touched
func moveCrons(file, dom, root, old, new string) int {
	data, err := os.ReadFile(file)
	if err != nil {
		return 0
	}
	s := strings.TrimSuffix(string(data), "\n")
	var lines []string
	if s != "" || strings.HasSuffix(string(data), "\n") && len(data) > 0 {
		lines = strings.Split(s, "\n")
	}
	cont := map[int]string{}
	cmd := map[int]string{}
	blk := 0
	for _, l := range lines {
		if isBlank(l) {
			blk++
			continue
		}
		line := hashRe.ReplaceAllString(l, "")
		if loc := contKeyRe.FindStringIndex(line); loc != nil {
			cont[blk] = strings.Trim(line[loc[1]:], " \t")
		}
		if loc := cmdKeyRe.FindStringIndex(line); loc != nil {
			cmd[blk] = line[loc[1]:]
		}
	}
	move := map[int]bool{}
	for k, c := range cont {
		if c == old && (awkHas(cmd[k], dom, ":/ \t", "/: \t'\"", true) || awkHas(cmd[k], root, " \t'\"=:", "/ \t'\"", false)) {
			move[k] = true
		}
	}
	tail := regexp.MustCompile(regexp.QuoteMeta(old) + `[ \t]*$`)
	// the awk used the old version as a regex, so its dots match any char
	if re, err := regexp.Compile(old + `[ \t]*$`); err == nil {
		tail = re
	}
	moved := 0
	b := 0
	var out strings.Builder
	for _, l := range lines {
		if isBlank(l) {
			b++
		} else if move[b] && contLineRe.MatchString(l) {
			if loc := tail.FindStringIndex(l); loc != nil {
				l = l[:loc[0]] + new + l[loc[1]:]
			}
			moved++
		}
		out.WriteString(l + "\n")
	}
	if moved > 0 {
		conf.WriteInPlace(file, out.String())
	}
	return moved
}
