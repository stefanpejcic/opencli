// ##############################################################################
// Script Name: internal/podman/podman.go
// Description: Shared helpers for talking to root's and users' rootless Podman instances.
// Usage: imported by command packages
// Author: Stefan Pejcic
// Created: 10.07.2026
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

package podman

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opencli/internal/conf"
	"opencli/internal/sh"
)

// UID returns the owner uid of /home/<user>
func UID(user string) (int, error) {
	st, err := os.Stat("/home/" + user)
	if err != nil {
		return 0, err
	}
	return int(st.Sys().(*syscall.Stat_t).Uid), nil
}

// Socket is the CONTAINER_HOST url for a user's rootless podman socket
func Socket(user string) (string, error) {
	uid, err := UID(user)
	if err != nil {
		fmt.Fprintf(os.Stderr, "podman_user_socket: no such user '%s'\n", user)
		return "", err
	}
	return fmt.Sprintf("unix:///hostfs/run/user/%d/podman/podman.sock", uid), nil
}

func failing() *sh.Cmd { return sh.C("false").Quiet() }

// User runs podman against a user's rootless instance
func User(user string, args ...string) *sh.Cmd {
	sock, err := Socket(user)
	if err != nil {
		return failing()
	}
	return sh.C("podman", append([]string{"--remote"}, args...)...).Env("CONTAINER_HOST=" + sock)
}

// ComposeUser runs podman-compose against a user's instance, CONTAINER_HOST alone puts it in remote mode
func ComposeUser(user string, args ...string) *sh.Cmd {
	sock, err := Socket(user)
	if err != nil {
		return failing()
	}
	return sh.C("podman-compose", args...).Env("CONTAINER_HOST=" + sock)
}

// ComposeUserRetry retries since two runs at once both create a missing network and the loser exits 125
func ComposeUserRetry(user string, quiet bool, args ...string) bool {
	for try := 1; try <= 3; try++ {
		c := ComposeUser(user, args...)
		if quiet {
			c.Quiet()
		}
		if c.OK() {
			return true
		}
		if try < 3 {
			time.Sleep(time.Duration(try*2) * time.Second)
		}
	}
	return false
}

func isRoot(ctx string) bool { return ctx == "" || ctx == "default" || ctx == "root" }

// Ctx runs podman for a context from the users table server column, root's own podman for ""/default/root
func Ctx(ctx string, args ...string) *sh.Cmd {
	if isRoot(ctx) {
		return sh.C("podman", args...)
	}
	return User(ctx, args...)
}

// ComposeCtx is Ctx for podman-compose
func ComposeCtx(ctx string, args ...string) *sh.Cmd {
	if isRoot(ctx) {
		return sh.C("podman-compose", args...)
	}
	return ComposeUser(ctx, args...)
}

// State returns .State.Status of a root container, "" when it doesn't exist
func State(container string) string {
	return sh.C("podman", "inspect", container, "--format", "{{.State.Status}}").NoErr().Str()
}

// IsRunning checks the state, a wedged container is missing from podman ps too
func IsRunning(container string) bool { return State(container) == "running" }

// CanExec actually tries an exec, a dead container can keep reporting running
func CanExec(container string) bool {
	return IsRunning(container) && sh.C("podman", "exec", container, "true").Timeout(10*time.Second).Quiet().OK()
}

// ForceRemove removes a container stuck in a state start and stop refuse, dependents go first
func ForceRemove(container string, dependents ...string) bool {
	for _, c := range append(dependents, container) {
		sh.Quiet("podman", "kill", c)
		sh.Quiet("podman", "rm", "-f", c)
		sh.Quiet("podman", "rm", "-f", "--storage", c)
	}
	return !sh.OK("podman", "container", "exists", container)
}

// EnsureRunning brings a root container up through compose, force removing it first if it's wedged
func EnsureRunning(container, composeDir, service string, timeout int) bool {
	if timeout <= 0 {
		timeout = 30
	}
	state := State(container)
	if state == "running" {
		return true
	}
	if state != "" && state != "exited" && state != "stopped" {
		sh.Quiet("podman", "kill", container)
		sh.Quiet("podman", "rm", "-f", container)
		sh.Quiet("podman", "rm", "-f", "--storage", container)
	}
	// --no-deps: without it podman-compose 1.0.6 can remove other services of the stack and not start them again
	sh.C("podman-compose", "up", "-d", "--no-deps", service).Dir(composeDir).Timeout(time.Duration(timeout) * time.Second).Quiet().Run()
	return IsRunning(container)
}

// CtxIsRunning checks .State.Running against a context's podman
func CtxIsRunning(ctx, container string) bool {
	return Ctx(ctx, "inspect", "-f", "{{.State.Running}}", container).NoErr().Str() == "true"
}

const listenCheck = `awk '$4 == "0A"' /proc/net/tcp /proc/net/tcp6 2>/dev/null | grep -q . || awk '$4 == "00010000" && $8 ~ /^\/run\/php\//' /proc/net/unix 2>/dev/null | grep -q .`

// CtxIsListening also wants a listening tcp socket or a php-fpm unix socket inside the container
func CtxIsListening(ctx, container string) bool {
	return CtxIsRunning(ctx, container) && Ctx(ctx, "exec", container, "sh", "-c", listenCheck).Quiet().OK()
}

var vclHost = regexp.MustCompile(`^[[:space:]]*\.host[[:space:]]*=[[:space:]]*"([0-9.]*)"`)

const varnishReload = `
        set -e
        b=$(getent ahostsv4 "$WEB_SERVER" | awk "NR==1{print \$1}")
        [ -n "$b" ]
        sed "s|VARNISH_BACKEND_HOST|$b|g" /etc/varnish/default.vcl.template > /tmp/opencli.vcl
        cat /tmp/opencli.vcl > /etc/varnish/default.vcl
        n="opencli_$(date +%s)_$$"
        varnishadm vcl.load "$n" /etc/varnish/default.vcl
        varnishadm vcl.use "$n"
        for v in $(varnishadm vcl.list | awk "\$1 == \"available\" {print \$NF}"); do varnishadm vcl.discard "$v" || true; done
    `

// SyncVarnish reloads varnish's vcl when the webserver got a new IP, uncached pages 503 otherwise
func SyncVarnish(ctx string) {
	if !CtxIsRunning(ctx, "varnish") {
		return
	}
	wsIP := ""
	if out := Ctx(ctx, "exec", "varnish", "sh", "-c", `getent ahostsv4 "$WEB_SERVER"`).NoErr().Str(); out != "" {
		if f := strings.Fields(strings.SplitN(out, "\n", 2)[0]); len(f) > 0 {
			wsIP = f[0]
		}
	}
	vclIP := ""
	vcl := Ctx(ctx, "exec", "varnish", "cat", "/etc/varnish/default.vcl").NoErr().Str()
	for _, l := range strings.Split(vcl, "\n") {
		if m := vclHost.FindStringSubmatch(l); m != nil {
			vclIP = m[1]
			break
		}
	}
	if wsIP != "" && wsIP == vclIP {
		return
	}
	if !Ctx(ctx, "exec", "varnish", "sh", "-c", varnishReload).Quiet().OK() {
		Ctx(ctx, "restart", "varnish").Quiet().Run()
	}
}

// RestartWebserver restarts the webserver and points varnish at its new IP
func RestartWebserver(ctx, ws string) bool {
	ok := Ctx(ctx, "restart", ws).Quiet().OK()
	SyncVarnish(ctx)
	return ok
}

// ReloadWebserver reloads without dropping connections, waiting up to 60s for extra containers to listen
func ReloadWebserver(ctx, ws string, wait ...string) bool {
	tries := 30
	if (ws == "caddy" || ws == "") && conf.Env("/home/"+ctx+"/.env", "WEB_SERVER") == "caddy" {
		// caddy mode has no webserver container, the server's caddy talks to php-fpm itself
		for _, c := range wait {
			for tries > 0 && !CtxIsListening(ctx, c) {
				tries--
				time.Sleep(2 * time.Second)
			}
		}
		return sh.C("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").Quiet().OK()
	}

	if len(wait) > 0 {
		for {
			ready := CtxIsRunning(ctx, ws)
			for _, c := range wait {
				if ready && !CtxIsListening(ctx, c) {
					ready = false
				}
			}
			tries--
			if ready || tries <= 0 {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}

	if !CtxIsRunning(ctx, ws) {
		return RestartWebserver(ctx, ws)
	}

	var check []string
	var signal string
	switch {
	case ws == "nginx" || ws == "openresty":
		check, signal = []string{"nginx", "-t"}, "HUP"
	case ws == "apache":
		check, signal = []string{"apachectl", "configtest"}, "USR1"
	case strings.HasSuffix(ws, "litespeed"):
		if Ctx(ctx, "exec", ws, "/usr/local/lsws/bin/lswsctrl", "restart").Quiet().OK() {
			return true
		}
		return RestartWebserver(ctx, ws)
	default:
		return RestartWebserver(ctx, ws)
	}

	// a reload with a broken config is ignored silently, retried since aardvark-dns briefly fails lookups
	var out string
	var err error
	for tries = 5; ; {
		out, err = Ctx(ctx, append([]string{"exec", ws}, check...)...).Combined()
		if err == nil {
			break
		}
		tries--
		if tries <= 0 {
			break
		}
		time.Sleep(time.Second)
	}
	if tries <= 0 {
		fmt.Fprintf(os.Stderr, "%s config test failed, still running the previous config:\n%s\n", ws, out)
		return false
	}
	if Ctx(ctx, "kill", "--signal", signal, ws).Quiet().OK() {
		return true
	}
	return RestartWebserver(ctx, ws)
}

// CaddyReload applies caddy changes without dropping connections, an invalid config keeps the running one
func CaddyReload() bool {
	if !IsRunning("caddy") {
		sh.C("podman-compose", "up", "-d", "caddy").Dir("/root").Quiet().Run()
		return true
	}
	out, err := sh.C("podman", "exec", "caddy", "caddy", "validate", "--config", "/etc/caddy/Caddyfile").Combined()
	if err != nil {
		first := ""
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(strings.ToLower(l), "error") {
				first = l
				break
			}
		}
		fmt.Fprintf(os.Stderr, "Caddy config is invalid, kept the running one: %s\n", first)
		return false
	}
	if !sh.C("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").Quiet().OK() {
		sh.C("podman", "restart", "caddy").Quiet().Run()
	}
	return true
}

// SharedImageStore is the additionalimagestores root from storage.conf, beta installs used another path
func SharedImageStore() string {
	in := false
	quoted := regexp.MustCompile(`"[^"]+"`)
	for _, l := range conf.Lines("/etc/containers/storage.conf") {
		if regexp.MustCompile(`^[[:space:]]*additionalimagestores`).MatchString(l) {
			in = true
		}
		if in {
			if m := quoted.FindString(l); m != "" {
				return strings.Trim(m, `"`)
			}
			if strings.Contains(l, "]") {
				break
			}
		}
	}
	return "/var/lib/containers/shared-storage"
}

// FixSharedPerms makes the shared store readable again, users' podman fails to start otherwise
func FixSharedPerms() {
	store := os.Getenv("SHARED_STORE")
	if store == "" {
		store = SharedImageStore()
	}
	sh.Quiet("chmod", "-R", "o+rX", store)
	sh.Quiet("find", store, "-name", "*.lock", "-exec", "chmod", "o+rw", "{}", "+")
}

// ComposeFile is the docker-compose.yml path for a context
func ComposeFile(ctx string) string {
	if isRoot(ctx) {
		return "/root/docker-compose.yml"
	}
	return "/home/" + ctx + "/docker-compose.yml"
}

// TasksMax derives the user slice pids.max from plan RAM, /home/<user>/TasksMax overrides it
func TasksMax(ramGB int, user string) int {
	tasks := ramGB * 1000
	if tasks < 1000 {
		tasks = 1000
	}
	if user != "" {
		if b, err := os.ReadFile("/home/" + user + "/TasksMax"); err == nil {
			digits := strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' {
					return r
				}
				return -1
			}, string(b))
			if n, err := strconv.Atoi(digits); err == nil {
				tasks = n
			}
		}
	}
	return tasks
}

// UnlimitedSliceCeiling returns cpu percent, MemoryHigh MB and MemoryMax MB for unlimited plans
func UnlimitedSliceCeiling() (int, int, int) {
	cores := runtime.NumCPU()
	if out := sh.Str("nproc", "--all"); out != "" {
		if n, err := strconv.Atoi(out); err == nil {
			cores = n
		}
	}
	ramMB := 0
	for _, l := range conf.Lines("/proc/meminfo") {
		if strings.HasPrefix(l, "MemTotal:") {
			if f := strings.Fields(l); len(f) > 1 {
				kb, _ := strconv.Atoi(f[1])
				ramMB = kb / 1024
			}
		}
	}
	cpu := (cores - 2) * 100
	if cpu < cores*50 {
		cpu = cores * 50
	}
	return cpu, ramMB * 80 / 100, ramMB * 90 / 100
}

// IOWeight is cores x 100 clamped to 50..1000, unlimited gets 100
func IOWeight(cpu string) int {
	c := strings.TrimRight(cpu, "Gg")
	f, _ := strconv.ParseFloat(leadingNumber(c), 64)
	w := 100
	if f != 0 {
		w = int(f * 100)
	}
	if w < 50 {
		w = 50
	}
	if w > 1000 {
		w = 1000
	}
	return w
}

// leadingNumber mimics awk's string to number, the longest numeric prefix
func leadingNumber(s string) string {
	s = strings.TrimSpace(s)
	m := regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?`).FindString(s)
	if m == "" {
		return "0"
	}
	return m
}

// SedInPlace edits a file that's bind mounted into a container while keeping its inode
func SedInPlace(file string, sedArgs ...string) bool {
	out, err := sh.C("sed", append(sedArgs, file)...).Raw()
	if err != nil {
		return false
	}
	return conf.WriteInPlace(file, out) == nil
}
