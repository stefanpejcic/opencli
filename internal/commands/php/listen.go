// ##############################################################################
// Script Name: internal/commands/php/listen.go
// Description: View or change how a user's websites reach php-fpm: TCP port 9000, a unix socket, or Caddy talking to the socket directly without a webserver container.
// Usage: opencli php-listen <USERNAME> [port|socket|caddy] [--webserver nginx|apache|openresty]
// Author: Stefan Pejcic
// Created: 01.11.2026
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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/phplisten"
	"opencli/internal/podman"
	"opencli/internal/sh"
	"opencli/internal/tenantfs"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "php-listen",
		Desc:  "View or change how a user's websites reach php-fpm: TCP port 9000, a unix socket, or Caddy talking to the socket directly without a webserver container.",
		Usage: "opencli php-listen <USERNAME> [port|socket|caddy] [--webserver nginx|apache|openresty]",
		Run:   listen,
	})
}

func listenUsage() int {
	fmt.Print(`Usage: opencli php-listen <USERNAME> [port|socket|caddy] [--webserver nginx|apache|openresty]

  port     webserver reaches php-fpm-X.Y:9000 (default)
  socket   webserver reaches php-fpm over /run/php/php-fpm-X.Y/www.sock
  caddy    no webserver container, Caddy serves files and talks to the php-fpm socket itself

  --webserver  webserver to start again when leaving caddy mode (default: the one used before caddy, or nginx)
`)
	return 1
}

var (
	suspendedRe = regexp.MustCompile(`^SUSPENDED_[0-9]{14}_`)
	backWsRe    = regexp.MustCompile(`^(nginx|apache|openresty)$`)
	phpNameRe   = regexp.MustCompile(`^php-fpm-[0-9.]+$`)
)

// listenSetEnv replaces KEY="value" in the env file or appends it
func listenSetEnv(envFile, key, value string) {
	prefix := key + "="
	has := false
	for _, l := range conf.Lines(envFile) {
		if strings.HasPrefix(l, prefix) {
			has = true
			break
		}
	}
	if has {
		conf.SedFile(envFile, func(lines []string) []string {
			for i, l := range lines {
				if strings.HasPrefix(l, prefix) {
					lines[i] = prefix + `"` + value + `"`
				}
			}
			return lines
		})
		return
	}
	if f, err := os.OpenFile(envFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
		f.WriteString(prefix + `"` + value + `"` + "\n")
		f.Close()
	}
}

// readTabs is `IFS=$'\t' read -r a b c`, runs of tabs count as one separator and the last name gets the rest
func readTabs(line string, n int) []string {
	out := make([]string, n)
	rest := strings.TrimLeft(line, "\t")
	for i := 0; i < n; i++ {
		if i == n-1 {
			out[i] = strings.TrimRight(rest, "\t")
			break
		}
		j := strings.IndexByte(rest, '\t')
		if j < 0 {
			out[i] = rest
			break
		}
		out[i] = rest[:j]
		rest = strings.TrimLeft(rest[j:], "\t")
	}
	return out
}

func containsLine(text, want string) bool {
	for _, l := range strings.Split(text, "\n") {
		if l == want {
			return true
		}
	}
	return false
}

func caddyMountsHome() bool {
	out := sh.C("podman", "inspect", "caddy", "--format", "{{range .Mounts}}{{.Destination}} {{end}}").NoErr().Str()
	for _, d := range strings.Split(out, " ") {
		if d == "/home" {
			return true
		}
	}
	return false
}

func listen(args []string) int {
	username, target, backWs := "", "", ""
parse:
	for len(args) > 0 {
		a := args[0]
		switch {
		case a == "--webserver":
			if len(args) < 2 {
				// bash looped forever here
				backWs = ""
				break parse
			}
			backWs = args[1]
			args = args[2:]
			continue
		case a == "port" || a == "socket" || a == "caddy":
			target = a
		case a == "-h" || a == "--help":
			return listenUsage()
		case strings.HasPrefix(a, "-"):
			fmt.Println("Unknown option: " + a)
			return listenUsage()
		default:
			if username != "" {
				return listenUsage()
			}
			username = a
		}
		args = args[1:]
	}
	if username == "" {
		return listenUsage()
	}
	if backWs != "" && !backWsRe.MatchString(backWs) {
		fmt.Println("ERROR: --webserver must be nginx, apache or openresty.")
		return 1
	}

	db.Require()
	// a suspended account is renamed to SUSPENDED_<time>_<username>, it's found by its own name too
	esc := db.Escape(username)
	row := strings.TrimSpace(db.Plain("SELECT id, server FROM users WHERE username = '"+esc+"' OR (username REGEXP '^SUSPENDED_[0-9]{14}_' AND SUBSTRING(username, 26) = '"+esc+"') ORDER BY username = '"+esc+"' DESC LIMIT 1", "-D", db.Database, "-sN").NoErr().Str())
	userID, context := row, ""
	if i := strings.IndexAny(row, " \t"); i >= 0 {
		userID, context = row[:i], strings.TrimSpace(row[i:])
	}
	if userID == "" {
		fmt.Printf("ERROR: user %s does not exist.\n", username)
		return 1
	}
	if context == "" || context == "default" || context == "NULL" {
		context = suspendedRe.ReplaceAllString(username, "")
	}

	envFile := "/home/" + context + "/.env"
	composeFile := "/home/" + context + "/docker-compose.yml"
	if !conf.IsFile(envFile) || !conf.IsFile(composeFile) {
		fmt.Printf("ERROR: /home/%s/.env or docker-compose.yml is missing.\n", context)
		return 1
	}

	webServer := phplisten.EnvValue(context, "WEB_SERVER")
	current := phplisten.Mode(context)

	if target == "" {
		fmt.Printf("User '%s' reaches php-fpm over: %s (webserver: %s)\n", username, current, webServer)
		return 0
	}
	if current == target {
		fmt.Printf("User '%s' already uses: %s\n", username, target)
		return 0
	}
	if strings.Contains(webServer, "litespeed") {
		fmt.Printf("ERROR: %s runs LSPHP itself, it doesn't use php-fpm.\n", webServer)
		return 1
	}
	if target == "caddy" {
		for _, l := range conf.Lines(envFile) {
			if strings.HasPrefix(l, "PROXY_HTTP_PORT=") {
				fmt.Printf("ERROR: Varnish is enabled for %s, disable it first, caddy mode has no webserver for Varnish to sit in front of.\n", username)
				return 1
			}
		}
	}

	uid := 0
	if st, err := os.Stat("/home/" + context); err == nil {
		uid = int(st.Sys().(*syscall.Stat_t).Uid)
	} else {
		fmt.Fprintf(os.Stderr, "stat: cannot statx '/home/%s': No such file or directory\n", context)
	}
	vhostDir := "/home/" + context + "/docker-data/volumes/" + context + "_webserver_data/_data"
	wsVolume := tenantfs.Volume(context, "webserver_data")
	running := podman.Ctx(context, "ps", "--format", "{{.Names}}").NoErr().Str()
	// only containers that already run are recreated, stopped ones pick the change up when they start next time
	var runningPHP []string
	for _, l := range strings.Split(running, "\n") {
		if phpNameRe.MatchString(l) {
			runningPHP = append(runningPHP, l)
		}
	}
	defaultPHP := phplisten.EnvValue(context, "DEFAULT_PHP_VERSION")

	// domain <tab> docroot <tab> php version
	domains := db.Plain("SELECT domain_url, docroot, php_version FROM domains WHERE user_id = '"+db.Escape(userID)+"'", "-D", db.Database, "-sN").NoErr().Str()

	fmt.Printf("Switching %s from %s to %s\n", username, current, target)

	result, _ := phplisten.ComposePatch(composeFile)
	fmt.Println("- docker-compose.yml: " + result)
	sockets := "/home/" + context + "/sockets"
	os.MkdirAll(sockets+"/php", 0o755)
	os.Chown(sockets, uid, uid)
	os.Chown(sockets+"/php", uid, uid)
	os.Chmod(sockets+"/php", 0o755)

	listenValue := "socket"
	if target == "port" {
		listenValue = "port"
	}
	listenSetEnv(envFile, "PHP_FPM_LISTEN", listenValue)

	// podman-compose 1.0.6 up -d reuses an existing container with its old env and mounts, so they go first, --depend takes webserver and varnish along
	composeUp := func(svcs ...string) {
		before := podman.Ctx(context, "ps", "--format", "{{.Names}}").NoErr().Str()
		for _, c := range svcs {
			podman.Ctx(context, "rm", "-f", "-t", "10", "--depend", c).Quiet().Run()
		}
		after := podman.Ctx(context, "ps", "--format", "{{.Names}}").NoErr().Str()
		again := append([]string{}, svcs...)
		for _, c := range strings.Fields(before) {
			listed := false
			for _, a := range again {
				if a == c {
					listed = true
				}
			}
			if !containsLine(after, c) && !listed {
				again = append(again, c)
			}
		}
		cwd, _ := os.Getwd()
		os.Chdir("/home/" + context)
		podman.ComposeUserRetry(context, true, append([]string{"-f", composeFile, "up", "-d", "--no-deps"}, again...)...)
		if cwd != "" {
			os.Chdir(cwd)
		}
	}

	if target == "caddy" {
		if !caddyMountsHome() {
			fmt.Println("- Caddy doesn't see /home yet, adding a read-only mount and recreating it (every site on the server reconnects)")
			if !strings.Contains(conf.Read("/root/docker-compose.yml"), "/home:/home:ro") {
				listenCaddyMount()
			}
			sh.C("podman-compose", "up", "-d", "caddy").Dir("/root").Quiet().Run()
			if !caddyMountsHome() {
				fmt.Println("ERROR: caddy still doesn't mount /home, check /root/docker-compose.yml")
				return 1
			}
		}

		// caddy talks to php-fpm itself, so websites need one running, an account without domains doesn't
		if len(runningPHP) == 0 && domains != "" && defaultPHP != "" {
			runningPHP = []string{"php-fpm-" + defaultPHP}
		}
		shown := strings.Join(runningPHP, " ")
		if shown == "" {
			shown = "none, no domains"
		}
		fmt.Println("- Recreating php-fpm on the socket: " + shown)
		composeUp(runningPHP...)
		for _, c := range runningPHP {
			for i := 0; i < 30; i++ {
				if podman.CtxIsListening(context, c) {
					break
				}
				time.Sleep(2 * time.Second)
			}
		}

		for _, line := range strings.Split(domains, "\n") {
			f := readTabs(line, 3)
			domain, docroot, php := f[0], f[1], f[2]
			if domain == "" {
				continue
			}
			cf := "/etc/openpanel/caddy/domains/" + domain + ".conf"
			if !conf.IsFile(cf) {
				fmt.Printf("  %s: no caddy conf, skipped\n", domain)
				continue
			}
			// the vhost knows the version the domain really runs, the db column can lag behind
			v := ""
			if b, err := os.ReadFile(vhostDir + "/" + domain + ".conf"); err == nil {
				if m := phpFpmRe.FindString(string(b)); m != "" {
					v = versionOnly.FindString(m)
				}
			}
			if v == "" {
				v = php
			}
			phplisten.CaddyConfDirect(cf, context, docroot, v)
			fmt.Printf("  %s: caddy -> php-fpm-%s socket, docroot %s\n", domain, v, docroot)
		}
		if !sh.C("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").Quiet().OK() {
			fmt.Println("WARNING: caddy reload failed, check: podman exec caddy caddy validate --config /etc/caddy/Caddyfile")
		}

		if webServer != "" && webServer != "caddy" {
			listenSetEnv(envFile, "PHP_CADDY_PREV_WEB_SERVER", webServer)
			podman.ComposeCtx(context, "stop", webServer).Dir("/home/" + context).Quiet().Run()
			fmt.Printf("- Stopped %s, its vhosts are kept for switching back\n", webServer)
		}
		listenSetEnv(envFile, "WEB_SERVER", "caddy")
		fmt.Printf("Done, Caddy now talks to php-fpm directly for %s\n", username)
		return 0
	}

	// port or socket, possibly coming back from caddy mode
	if current == "caddy" {
		if backWs == "" {
			backWs = phplisten.EnvValue(context, "PHP_CADDY_PREV_WEB_SERVER")
		}
		if backWs == "" {
			backWs = "nginx"
		}
		webServer = backWs
		listenSetEnv(envFile, "WEB_SERVER", webServer)
		for _, line := range strings.Split(domains, "\n") {
			f := readTabs(line, 3)
			domain, docroot, php := f[0], f[1], f[2]
			if domain == "" {
				continue
			}
			cf := "/etc/openpanel/caddy/domains/" + domain + ".conf"
			if conf.IsFile(cf) {
				phplisten.CaddyConfProxy(cf)
			}
			vf := vhostDir + "/" + domain + ".conf"
			if !conf.IsFile(vf) {
				tpl := "/etc/openpanel/nginx/vhosts/1.1/docker_" + webServer + "_domain.conf"
				b, err := os.ReadFile(tpl)
				if err != nil {
					fmt.Fprintf(os.Stderr, "sed: can't read %s: No such file or directory\n", tpl)
				}
				s := strings.NewReplacer("<DOMAIN_NAME>", domain, "<USER>", username, "<PHP>", php, "<DOCUMENT_ROOT>", docroot).Replace(string(b))
				tenantfs.Write(wsVolume, vf, 0o644, []byte(s))
				fmt.Printf("  %s: created %s vhost\n", domain, webServer)
			}
		}
	}

	files, _ := filepath.Glob(vhostDir + "/*.conf")
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasPrefix(base, ".") || !conf.IsFile(f) {
			continue
		}
		name := strings.TrimSuffix(base, ".conf")
		// read first and never write an empty result, a failed read must not wipe the vhost
		b, err := tenantfs.Read(wsVolume, f)
		vhost := strings.TrimRight(string(b), "\n")
		if err != nil || vhost == "" {
			fmt.Printf("  %s: could not read the vhost, left unchanged\n", name)
			continue
		}
		lines := strings.Split(vhost, "\n")
		for i, l := range lines {
			lines[i] = phplisten.VhostTransport(target, l)
		}
		tenantfs.Write(wsVolume, f, 0o644, []byte(strings.Join(lines, "\n")+"\n"))
		fmt.Printf("  %s: %s -> php-fpm over %s\n", name, webServer, target)
	}

	// the webserver is recreated only when it runs, or when it comes back from caddy mode where it was stopped
	recreateWs := ""
	if webServer != "" && (containsLine(running, webServer) || current == "caddy") {
		recreateWs = webServer
	}
	if len(runningPHP) == 0 && recreateWs == "" {
		fmt.Printf("- Neither php-fpm nor %s is running, nothing recreated, they use %s when they start\n", webServer, target)
	} else {
		msg := "- Recreating "
		if len(runningPHP) > 0 {
			msg += strings.Join(runningPHP, " ") + " "
		}
		if recreateWs != "" {
			msg += recreateWs
		} else {
			ws := webServer
			if ws == "" {
				ws = "webserver"
			}
			msg += "(" + ws + " not running, left alone)"
		}
		fmt.Println(msg)
		svcs := append([]string{}, runningPHP...)
		if recreateWs != "" {
			svcs = append(svcs, recreateWs)
		}
		composeUp(svcs...)
		if recreateWs != "" {
			podman.ReloadWebserver(context, recreateWs, runningPHP...)
		}
		podman.SyncVarnish(context)
	}
	if current == "caddy" {
		sh.C("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").Quiet().Run()
	}
	fmt.Printf("Done, %s now reaches php-fpm over %s for %s\n", webServer, target, username)
	return 0
}

// listenCaddyMount adds the /home mount under caddy's log mount in /root/docker-compose.yml
func listenCaddyMount() {
	const logMount = "      - /var/log/caddy/:/var/log/caddy/"
	start := regexp.MustCompile(`^  caddy:`)
	end := regexp.MustCompile(`^  [a-z]`)
	conf.SedFile("/root/docker-compose.yml", func(lines []string) []string {
		in := false
		for i, l := range lines {
			if !in {
				if start.MatchString(l) {
					in = true
				}
				continue
			}
			if l == logMount {
				lines[i] = l + "\n      - /home:/home:ro"
			}
			if end.MatchString(l) {
				in = false
			}
		}
		return lines
	})
}
