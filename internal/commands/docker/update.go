// ##############################################################################
// Script Name: internal/commands/docker/update.go
// Description: Check for image updates across root's stack and every
// Usage: opencli docker-update [IMAGE] [-y|--yes] [--dry-run] [--no-php-move]
// Author: Stefan Pejcic
// Created: 20.08.2026
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

package docker

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/requirement"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "docker-update",
		Desc:  "Check for image updates across root's stack and every",
		Usage: "opencli docker-update [IMAGE] [-y|--yes] [--dry-run] [--no-php-move]",
		Run:   update,
	})
}

const (
	updLogDir = "/var/log/openpanel/admin/docker"
	// pulls into the shared store use their own runroot, see docker-autostart
	updRunroot = "/run/containers/shared-storage"
	// first release on each php branch with the latest php security fixes (CVE-2026-109618, CVE-2026-109619), bump on the next php security release
	updPHPFixed = "8.2.36 8.3.37 8.4.28 8.5.13 8.6.0"
	// php-fpm blocks from the compose template replace the ones still on shinsenter/php
	updPHPTemplate = "/etc/openpanel/docker/compose/1.0/docker-compose.yml"
)

var (
	updHeader   = regexp.MustCompile(`^  [A-Za-z0-9._-]+:[ \t\n\v\f\r]*$`)
	updImageKey = regexp.MustCompile(`^    image:[ \t\n\v\f\r]`)
	updNameKey  = regexp.MustCompile(`^    container_name:[ \t\n\v\f\r]`)
	updAwkVal   = regexp.MustCompile(`^[^:]*:[ \t\n\v\f\r]*`)
	updVarDef   = regexp.MustCompile(`\$\{[A-Za-z0-9_]+:-([^}]*)\}`)
	updPHPVer   = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+`)
	updWebRe    = regexp.MustCompile(`nginx|openresty|apache|litespeed`)
	updAnswerRe = regexp.MustCompile(`^[Yy]$`)
)

func updUsage() int {
	fmt.Print(`Usage: opencli docker-update [IMAGE] [options]

Options:
  -y, --yes       Apply updates without prompting for confirmation.
  --dry-run       Only check and print available updates, never prompt or apply.
  --no-php-move   Keep php-fpm services on the image they use, only pull its new build.

With no options, available updates are printed and confirmation is asked
for 10 seconds - if there's no answer in that time the script exits
without changing anything.

Only running containers on an outdated image are recreated, stacks are never
taken down. With IMAGE (e.g. shinsenter/php:7.4-fpm) only that image is checked.
After a php-fpm container is recreated the user's webserver is reloaded so it
connects to the new container.

php-fpm services that still use shinsenter/php get the openpanel/php-fpm block
from the compose template (/etc/openpanel/docker/compose/1.0/docker-compose.yml)
and running ones are recreated on it. Users without PHP_FPM_LISTEN in .env get
PHP_FPM_LISTEN="port" so their vhosts keep working, switch with opencli php-listen.
This is skipped when IMAGE is given, unless it's an openpanel/php-fpm image, and
with --no-php-move.
`)
	return 1
}

type updState struct {
	logFile string
	shared  string
	failed  int

	contexts []string
	label    map[string]string

	// normalized image -> the name the compose files use
	allImages map[string]string
	allOrder  []string
	// normalized image -> "context service container" lines that use it
	imageUsers map[string][]string
	// "context/container" -> state and image id
	cState, cImage map[string]string

	phpSwapSvcs  map[string]string
	phpSwapOrder []string
	phpSwapImage map[string]string
	phpSwapLines []string

	planPull     []string
	planRecreate []string

	targetImage string
}

func (u *updState) log(msg string) {
	line := time.Now().Format("2006-01-02 15:04:05") + " : " + msg
	fmt.Println(line)
	if f, err := os.OpenFile(u.logFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666); err == nil {
		f.WriteString(line + "\n")
		f.Close()
	}
}

func updNormalize(image string) string {
	image = strings.TrimPrefix(image, "docker.io/")
	image = strings.TrimPrefix(image, "library/")
	last := image
	if i := strings.LastIndex(image, "/"); i >= 0 {
		last = image[i+1:]
	}
	if !strings.ContainsAny(last, ":@") {
		image += ":latest"
	}
	return image
}

// docker.io/library/<name> for official images, docker.io/<name> for other short names, so skopeo and containers-storage get the same name podman stores
func updQualify(image string) string {
	first, _, hasSlash := strings.Cut(image, "/")
	switch {
	case !hasSlash:
		return "docker.io/library/" + image
	case !strings.Contains(first, ".") && !strings.Contains(first, ":") && first != "localhost":
		return "docker.io/" + image
	}
	return image
}

// root's own stack uses root's storage, its copy there wins over the shared one; user stacks read the shared store
func updStoreOf(ctx string) string {
	if ctx == "root" {
		return "root"
	}
	return "shared"
}

func (u *updState) storePodman(store string, args ...string) *sh.Cmd {
	if store == "shared" {
		return sh.C("podman", append([]string{"--root", u.shared}, args...)...)
	}
	return sh.C("podman", args...)
}

func (u *updState) imageID(store, image string) string {
	return u.storePodman(store, "image", "inspect", "--format", "{{.Id}}", image).NoErr().Str()
}

// true if the store already has the digest for the image, .Digest is only one of the digests it was pulled under so all RepoDigests are checked
func (u *updState) hasDigest(store, image, digest string) bool {
	if digest == "" {
		return false
	}
	out, _ := u.storePodman(store, "image", "inspect", "--format", "{{.Digest}} {{range .RepoDigests}}{{.}} {{end}}", image).NoErr().Out()
	for _, tok := range strings.Split(out, " ") {
		for _, l := range strings.Split(tok, "\n") {
			if strings.HasSuffix(l, digest) {
				return true
			}
		}
	}
	return false
}

func updRemoteDigest(image string) string {
	return sh.C("skopeo", "inspect", "--format", "{{.Digest}}", "docker://"+image).NoErr().Str()
}

func (u *updState) pullImage(store, image string) bool {
	if store == "shared" {
		img := updQualify(image)
		ok := sh.C("skopeo", "copy", "-q", "docker://"+img, "containers-storage:[overlay@"+u.shared+"+"+updRunroot+"]"+img).Quiet().OK()
		podman.FixSharedPerms()
		return ok
	}
	return sh.C("podman", "pull", image).Quiet().OK()
}

// the PHP_VERSION php reports inside the image, empty if the image has no php (only images with PHP_VERSION in their env are run)
func (u *updState) imagePHPVersion(store, image string) string {
	id := u.imageID(store, image)
	if id == "" {
		return ""
	}
	env := u.storePodman(store, "image", "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", id).NoErr().Str()
	has := false
	for _, l := range strings.Split(env, "\n") {
		if strings.HasPrefix(l, "PHP_VERSION=") {
			has = true
		}
	}
	if !has {
		return ""
	}
	// run by root's own podman, which reads the shared store as an additional image store: a container created in the shared store itself would rewrite root's mount records too
	return sh.C("podman", "run", "--rm", "--network", "none", "--entrypoint", "php", id, "-r", "echo PHP_VERSION;").NoErr().Str()
}

// the fixed release for the version's branch if the version is older than it, empty if it's current or the branch gets no fixes anymore
func updPHPOutdated(version string) string {
	if !updPHPVer.MatchString(version) {
		return ""
	}
	parts := strings.Split(version, ".")
	branch := parts[0] + "." + parts[1]
	out := ""
	for _, fixed := range strings.Fields(updPHPFixed) {
		if fixed[:strings.LastIndex(fixed, ".")] != branch {
			continue
		}
		if version != fixed && text.VersionLess(version, fixed) {
			out = fixed
		}
	}
	return out
}

// "service image container" lines of a compose config stream, tracks 2-space service headers, off inside top-level networks/volumes/configs/secrets blocks
func updParseCompose(in string) [][3]string {
	var out [][3]string
	svc, img, cn := "", "", ""
	flush := func() {
		if svc != "" && img != "" {
			name := cn
			if name == "" {
				name = svc
			}
			out = append(out, [3]string{svc, img, name})
		}
		svc, img, cn = "", "", ""
	}
	value := func(line string) string {
		line = updAwkVal.ReplaceAllString(line, "")
		line = strings.NewReplacer(`"`, "", `'`, "").Replace(line)
		if i := strings.IndexAny(line, " \t\n\v\f\r"); i >= 0 {
			line = line[:i]
		}
		return line
	}
	s := true
	for _, l := range strings.Split(strings.TrimSuffix(in, "\n"), "\n") {
		switch {
		case autoServices.MatchString(l):
			flush()
			s = true
		case autoSkipBlock.MatchString(l):
			flush()
			s = false
		case s && updHeader.MatchString(l):
			flush()
			svc = strings.TrimSuffix(strings.Fields(l)[0], ":")
		case s && updImageKey.MatchString(l):
			img = value(l)
		case s && updNameKey.MatchString(l):
			cn = value(l)
		}
	}
	flush()
	return out
}

// the services deployed for a context, resolving ${VAR} via podman-compose config, falling back to a raw parse if that's empty
func updContextImages(ctx, composeFile string) [][3]string {
	dir := filepath.Dir(composeFile)
	cfg, _ := podman.ComposeCtx(ctx, "-f", composeFile, "config").Dir(dir).NoErr().Raw()
	pairs := updParseCompose(cfg)
	if len(pairs) == 0 {
		raw, _ := os.ReadFile(composeFile)
		var lines []string
		for _, l := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
			lines = append(lines, updVarDef.ReplaceAllString(l, "$1"))
		}
		pairs = updParseCompose(strings.Join(lines, "\n"))
	}
	return pairs
}

func updConfirm() bool {
	if st, err := os.Stdin.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprint(os.Stderr, "Apply now? [y/N] (auto-cancel in 10s): ")
	}
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		l, err := bufio.NewReader(os.Stdin).ReadString('\n')
		ch <- res{l, err}
	}()
	var r res
	select {
	case r = <-ch:
	case <-time.After(10 * time.Second):
		r.err = os.ErrDeadlineExceeded
	}
	fmt.Println()
	if r.err != nil {
		fmt.Println("No response within 10 seconds - exiting without changing anything.")
		return false
	}
	if !updAnswerRe.MatchString(strings.Trim(r.line, " \t\r\n")) {
		fmt.Println("Aborted - nothing changed.")
		return false
	}
	return true
}

func update(args []string) int {
	u := &updState{label: map[string]string{"root": "root"}}
	os.MkdirAll(updLogDir, 0o755)
	u.logFile = updLogDir + "/" + time.Now().Format("2006-01-02") + ".log"
	u.shared = podman.SharedImageStore()
	requirement.Command("skopeo")

	autoYes, dryRun, noPHPMove := false, false, false
	for _, a := range args {
		switch {
		case a == "-y" || a == "--yes":
			autoYes = true
		case a == "--dry-run":
			dryRun = true
		case a == "--no-php-move":
			noPHPMove = true
		case a == "-h" || a == "--help":
			return updUsage()
		case strings.HasPrefix(a, "-"):
			fmt.Println("Unknown option: " + a)
			return updUsage()
		default:
			if u.targetImage != "" {
				fmt.Println("Only one image can be given.")
				return updUsage()
			}
			u.targetImage = a
		}
	}

	phpSwapEnabled := true
	if noPHPMove || (u.targetImage != "" && !strings.HasPrefix(updNormalize(u.targetImage), "openpanel/php-fpm:")) {
		phpSwapEnabled = false
	}

	// A: enumerate every image in use, root first then non-suspended users
	u.contexts = []string{"root"}
	for _, c := range userContexts(userListCmd()) {
		if c == "" {
			continue
		}
		u.contexts = append(u.contexts, c)
		u.label[c] = "user: " + c
	}

	u.allImages = map[string]string{}
	u.imageUsers = map[string][]string{}
	u.cState, u.cImage = map[string]string{}, map[string]string{}
	u.phpSwapSvcs, u.phpSwapImage = map[string]string{}, map[string]string{}

	for _, ctx := range u.contexts {
		composeFile := podman.ComposeFile(ctx)
		if st, err := os.Stat(composeFile); err != nil || !st.Mode().IsRegular() {
			continue
		}
		pairs := updContextImages(ctx, composeFile)
		if len(pairs) == 0 {
			continue
		}
		if phpSwapEnabled && ctx != "root" {
			if st, err := os.Stat(updPHPTemplate); err == nil && st.Mode().IsRegular() {
				todo, warnings, _, _, _, _, _ := phpSwapPlan(updPHPTemplate, composeFile)
				for _, w := range warnings {
					f := strings.Fields(w)
					rest := ""
					if len(f) > 3 {
						rest = strings.Join(f[3:], " ")
					}
					u.log(fmt.Sprintf("WARNING: %s: %s stays on %s, %s", u.label[ctx], f[0], f[1], rest))
				}
				for _, t := range todo {
					if _, ok := u.phpSwapSvcs[ctx]; !ok {
						u.phpSwapOrder = append(u.phpSwapOrder, ctx)
					}
					u.phpSwapSvcs[ctx] += t.name + " "
					u.phpSwapImage[ctx+"/"+t.name] = t.new
					u.phpSwapLines = append(u.phpSwapLines, fmt.Sprintf("  %s: %s %s -> %s", u.label[ctx], t.name, t.old, t.new))
				}
			}
		}
		ps := podman.Ctx(ctx, "ps", "-a", "--no-trunc", "--format", "{{.Names}} {{.State}} {{.ImageID}}").NoErr().Str()
		for _, l := range strings.Split(ps, "\n") {
			f := strings.Fields(l)
			if len(f) == 0 {
				continue
			}
			for len(f) < 3 {
				f = append(f, "")
			}
			u.cState[ctx+"/"+f[0]] = f[1]
			u.cImage[ctx+"/"+f[0]] = strings.Join(f[2:], " ")
		}
		for _, p := range pairs {
			svc, img, cname := p[0], p[1], p[2]
			// skip images with a literal ${, a compose variable that never got resolved
			if img == "" || strings.Contains(img, "${") {
				continue
			}
			if sw := u.phpSwapImage[ctx+"/"+svc]; sw != "" {
				img = sw
			}
			key := updNormalize(img)
			if _, ok := u.allImages[key]; !ok {
				u.allOrder = append(u.allOrder, key)
			}
			u.allImages[key] = img
			u.imageUsers[key] = append(u.imageUsers[key], ctx+" "+svc+" "+cname)
		}
	}

	var targets []string
	if u.targetImage != "" {
		want := updNormalize(u.targetImage)
		if _, ok := u.allImages[want]; !ok {
			fmt.Printf("No stack uses %s.\n", u.targetImage)
			return 0
		}
		targets = []string{want}
	} else if len(u.allImages) == 0 {
		fmt.Println("No compose-managed images found.")
		return 0
	} else {
		// TARGETS is filled while walking ALL_IMAGES, so bash's order is the walk of a walk
		targets = bashAssocOrder(bashAssocOrder(u.allOrder))
	}

	// B: plan, per image and store
	if len(u.phpSwapLines) > 0 {
		fmt.Println("php-fpm moving from shinsenter/php to openpanel/php-fpm:")
		for _, l := range u.phpSwapLines {
			fmt.Println(l)
		}
		fmt.Println()
	}
	if u.targetImage == "" {
		fmt.Printf("Checking %d unique image(s)...\n", len(targets))
		fmt.Println()
	}
	for _, key := range targets {
		u.planImage(key, u.allImages[key])
	}

	if len(u.planPull) == 0 && len(u.planRecreate) == 0 && len(u.phpSwapLines) == 0 {
		fmt.Println("Nothing to do, every running container already uses the current image.")
		return 0
	}
	fmt.Printf("%d pull(s), %d container(s) to recreate, %d php-fpm service(s) to move to openpanel/php-fpm.\n", len(u.planPull), len(u.planRecreate), len(u.phpSwapLines))

	if dryRun {
		fmt.Println("Dry run - not pulling or recreating anything.")
		return 0
	}

	// C: apply, if approved
	if !autoYes && !updConfirm() {
		return 0
	}
	return u.apply()
}

type updEntry struct{ ctx, svc, cname, store string }

// plans one image: which stores get it pulled and which running containers get recreated, only containers that were already running are touched
func (u *updState) planImage(want, image string) {
	var running []updEntry
	var stopped [][2]string
	var storeOrder []string
	stores := map[string]bool{}
	for _, l := range u.imageUsers[want] {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		ctx, svc := f[0], f[1]
		cname := ""
		if len(f) > 2 {
			cname = strings.Join(f[2:], " ")
		}
		store := updStoreOf(ctx)
		if !stores[store] {
			stores[store] = true
			storeOrder = append(storeOrder, store)
		}
		if u.cState[ctx+"/"+cname] == "running" {
			running = append(running, updEntry{ctx, svc, cname, store})
		} else {
			stopped = append(stopped, [2]string{ctx, svc})
		}
	}
	storeOrder = bashAssocOrder(storeOrder)

	ids := map[string]string{}
	id := func(store string) string {
		if v, ok := ids[store]; ok {
			return v
		}
		ids[store] = u.imageID(store, image)
		return ids[store]
	}

	remote := updRemoteDigest(image)
	pullStore := map[string]bool{}
	for _, store := range storeOrder {
		switch {
		case remote == "":
			continue
		case id(store) == "":
			// never pulled and nothing runs it, it's pulled whenever a service using it first starts
			for _, e := range running {
				if e.store == store {
					pullStore[store] = true
					break
				}
			}
		case !u.hasDigest(store, image, remote):
			pullStore[store] = true
		}
	}

	var todo []updEntry
	var currentOK [][2]string
	for _, e := range running {
		if pullStore[e.store] || u.cImage[e.ctx+"/"+e.cname] != id(e.store) {
			todo = append(todo, e)
		} else {
			currentOK = append(currentOK, [2]string{e.ctx, e.svc})
		}
	}

	// nothing to pull or recreate, only worth a line in single image mode
	if len(pullStore) == 0 && len(todo) == 0 && u.targetImage == "" {
		if remote == "" {
			fmt.Printf("  ?  %s - could not reach registry, skipping\n", image)
		}
		return
	}

	fmt.Println(image)
	reg := remote
	if reg == "" {
		reg = "could not reach registry"
	}
	fmt.Println("  registry: " + reg)
	for _, store := range storeOrder {
		var note string
		switch {
		case pullStore[store]:
			note = "newer image available, will be pulled"
			if id(store) == "" {
				note = "not pulled yet, will be pulled"
			}
		case id(store) == "":
			note = "not pulled, nothing runs it"
		default:
			note = "up to date"
		}
		fmt.Printf("  %s store: %s\n", store, note)
		if pullStore[store] {
			u.planPull = append(u.planPull, store+" "+image)
		}
	}
	for _, e := range todo {
		u.planRecreate = append(u.planRecreate, strings.Join([]string{e.ctx, e.svc, e.cname, e.store, image}, " "))
		if strings.HasPrefix(e.svc, "php-fpm-") && e.ctx != "root" {
			fmt.Printf("  recreate: %s: %s, then reload its webserver\n", u.label[e.ctx], e.svc)
		} else {
			fmt.Printf("  recreate: %s: %s\n", u.label[e.ctx], e.svc)
		}
	}
	for _, e := range currentOK {
		fmt.Printf("  already current: %s: %s\n", u.label[e[0]], e[1])
	}
	for _, e := range stopped {
		fmt.Printf("  not running, left alone: %s: %s\n", u.label[e[0]], e[1])
	}
	fmt.Println()
}

// waits until php-fpm in the recreated container listens on 9000 or its /run/php socket, a webserver reloaded before that gets connection refused
func updWaitFPM(ctx, container string) bool {
	const script = `awk '$2 ~ /:2328$/ && $4 == "0A"' /proc/net/tcp /proc/net/tcp6 2>/dev/null | grep -q . || awk '$4 == "00010000" && $8 ~ /^\/run\/php\//' /proc/net/unix 2>/dev/null | grep -q .`
	for tries := 60; tries > 0; tries-- {
		if podman.Ctx(ctx, "exec", container, "sh", "-c", script).Quiet().OK() {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

// reloads the context's webserver so it resolves the recreated php-fpm container's new address, a signal to its pid 1 instead of a restart so open connections finish
func (u *updState) reloadWebserver(ctx string) bool {
	val := ""
	for _, l := range conf.Lines("/home/" + ctx + "/.env") {
		if strings.HasPrefix(l, "WEB_SERVER=") {
			val += l[len("WEB_SERVER="):]
		}
	}
	val = strings.Map(func(r rune) rune {
		if r == '"' || r == ' ' || (r >= '\t' && r <= '\r') {
			return -1
		}
		return r
	}, val)
	ws := updWebRe.FindString(val)
	var signal string
	switch ws {
	case "nginx", "openresty":
		signal = "HUP"
	case "apache":
		signal = "USR1" // graceful
	default:
		return true // litespeed runs lsphp itself
	}
	if podman.Ctx(ctx, "inspect", "-f", "{{.State.Status}}", ws).NoErr().Str() != "running" {
		u.log(fmt.Sprintf("  %s is not running for %s, nothing to reload", ws, u.label[ctx]))
		return true
	}
	if podman.Ctx(ctx, "kill", "--signal", signal, ws).Quiet().OK() {
		u.log(fmt.Sprintf("  reloaded %s (%s) for %s", ws, signal, u.label[ctx]))
		return true
	}
	u.log(fmt.Sprintf("  ERROR: could not reload %s for %s", ws, u.label[ctx]))
	return false
}

// the compose service names of the context's containers created depending on container id, and on those in turn, since rm --depend removes the whole chain
func updDependents(ctx, id string) string {
	if id == "" {
		return ""
	}
	ids := podman.Ctx(ctx, "ps", "-aq", "--no-trunc").NoErr().Str()
	if ids == "" {
		return ""
	}
	args := append([]string{"inspect", "--format", `{{.Id}} {{index .Config.Labels "com.docker.compose.service"}} {{range .Dependencies}}{{.}} {{end}}`}, strings.Fields(ids)...)
	out, _ := podman.Ctx(ctx, args...).NoErr().Out()
	var cid, svc, deps []string
	for _, l := range strings.Split(out, "\n") {
		if l == "" {
			continue
		}
		f := strings.Fields(l)
		c, s := f[0], ""
		if len(f) > 1 {
			s = f[1]
		}
		rest := ""
		if n := len(c) + len(s) + 2; n <= len(l) {
			rest = l[n:]
		}
		cid, svc, deps = append(cid, c), append(svc, s), append(deps, " "+rest+" ")
	}
	want := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for i := range cid {
			if want[cid[i]] {
				continue
			}
			for w := range want {
				if strings.Contains(deps[i], " "+w+" ") {
					want[cid[i]] = true
					changed = true
					break
				}
			}
		}
	}
	seen := map[string]bool{}
	var names []string
	for i := range cid {
		if cid[i] != id && want[cid[i]] && svc[i] != "" && !seen[svc[i]] {
			seen[svc[i]] = true
			names = append(names, svc[i])
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, " ") + " "
}

func updShort(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func (u *updState) apply() int {
	u.log("=== Update run started ===")
	failedPull := map[string]bool{}
	phpChecked := map[string]string{}
	failedSwap := map[string]bool{}

	for _, ctx := range bashAssocOrder(u.phpSwapOrder) {
		composeFile := podman.ComposeFile(ctx)
		envFile := filepath.Dir(composeFile) + "/.env"
		label := u.label[ctx]
		updCopyArchive(composeFile, composeFile+".bak-php-image-"+time.Now().Format("20060102150405"))
		svcs := strings.Fields(u.phpSwapSvcs[ctx])
		warns, err := phpSwapApply(updPHPTemplate, composeFile, svcs)
		// the swap's own output goes to the log file only
		if f, e := os.OpenFile(u.logFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666); e == nil {
			for _, w := range warns {
				fmt.Fprintln(f, w)
			}
			if err != nil {
				fmt.Fprintln(f, err)
			}
			f.Close()
		}
		if err != nil {
			u.log("ERROR: could not move php-fpm to openpanel/php-fpm for " + label + ", its docker-compose.yml is unchanged")
			for _, s := range svcs {
				failedSwap[ctx+"/"+s] = true
			}
			u.failed++
			continue
		}
		// the template falls back to the socket, accounts made before PHP_FPM_LISTEN existed have vhosts on port 9000
		has := false
		for _, l := range conf.Lines(envFile) {
			if strings.HasPrefix(l, "PHP_FPM_LISTEN=") {
				has = true
			}
		}
		if !has {
			if f, err := os.OpenFile(envFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666); err == nil {
				f.WriteString("PHP_FPM_LISTEN=\"port\"\n")
				f.Close()
			}
			u.log("  PHP_FPM_LISTEN=\"port\" added to .env for " + label + ", its vhosts use port 9000")
		}
		dir := filepath.Dir(composeFile)
		uid := 0
		if st, err := os.Stat(dir); err == nil {
			uid = int(st.Sys().(*syscall.Stat_t).Uid)
		}
		os.MkdirAll(dir+"/sockets/php", 0o755)
		os.Chown(dir+"/sockets/php", uid, uid)
		u.log(fmt.Sprintf("moved %s to openpanel/php-fpm for %s", strings.TrimSuffix(u.phpSwapSvcs[ctx], " "), label))
	}

	for _, entry := range u.planPull {
		store, image, _ := strings.Cut(entry, " ")
		u.log(fmt.Sprintf("pull %s into the %s store", image, store))
		if !u.pullImage(store, image) {
			u.log(fmt.Sprintf("FAILED to pull %s into the %s store", image, store))
			failedPull[store+" "+image] = true
			u.failed++
		}
	}
	u.reviveSockets()

	// a pulled image with a php older than its branch's security release is reported even when nothing gets recreated on it, new accounts and php version switches would get it
	phpWarned := map[string]bool{}
	for _, entry := range u.planPull {
		store, image, _ := strings.Cut(entry, " ")
		if failedPull[entry] {
			continue
		}
		version := u.imagePHPVersion(store, image)
		fixed := updPHPOutdated(version)
		phpChecked[entry] = version + " " + fixed
		if fixed != "" {
			u.log(fmt.Sprintf("WARNING: %s in the %s store still has PHP %s, the security fixes are in %s - run this again once the image is rebuilt", image, store, version, fixed))
			phpWarned[entry] = true
			u.failed++
		}
	}

	for _, entry := range u.planRecreate {
		f := strings.Fields(entry)
		ctx, svc, cname, store, image := f[0], f[1], f[2], f[3], f[4]
		label := u.label[ctx]
		key := store + " " + image
		if failedPull[key] {
			u.log(fmt.Sprintf("skipping %s for %s, %s failed to pull", svc, label, image))
			continue
		}
		if failedSwap[ctx+"/"+svc] {
			u.log(fmt.Sprintf("skipping %s for %s, its compose block still uses shinsenter/php", svc, label))
			continue
		}
		// an image pulled before its upstream rebuilt for a php security release still has the old php, recreating on it would only look like a fix
		if _, ok := phpChecked[key]; !ok {
			version := u.imagePHPVersion(store, image)
			phpChecked[key] = version + " " + updPHPOutdated(version)
		}
		version, fixed, _ := strings.Cut(phpChecked[key], " ")
		if fixed != "" {
			u.log(fmt.Sprintf("skipping %s for %s, %s still has PHP %s (security fixes are in %s) - run this again once the image is rebuilt", svc, label, image, version, fixed))
			if !phpWarned[key] {
				u.failed++
			}
			continue
		}
		composeDir := filepath.Dir(podman.ComposeFile(ctx))
		u.log(fmt.Sprintf("recreating %s for %s", svc, label))
		// a webserver created with depends_on on this container blocks removing it, so those get removed and started again with it
		oldID := podman.Ctx(ctx, "inspect", "-f", "{{.Id}}", cname).NoErr().Str()
		dependents := updDependents(ctx, oldID)
		if dependents != "" {
			u.log(fmt.Sprintf("  also recreating %s for %s, created depending on %s", strings.TrimSuffix(dependents, " "), label, svc))
		}
		// podman-compose 1.0.6 --force-recreate can fail to remove the old container, then just starts it again on the old image and still exits 0, so it's removed here first (named volumes are kept)
		podman.Ctx(ctx, "rm", "-f", "-t", "10", "--depend", cname).Quiet().Run()
		up := append([]string{"up", "-d", "--no-deps", svc}, strings.Fields(dependents)...)
		if !podman.ComposeCtx(ctx, up...).Dir(composeDir).Quiet().OK() {
			u.log(fmt.Sprintf("  ERROR: failed to recreate %s for %s - check manually", svc, label))
			u.failed++
			continue
		}
		podman.SyncVarnish(ctx)
		expected := u.imageID(store, image)
		current := podman.Ctx(ctx, "inspect", "-f", "{{.Image}}", cname).NoErr().Str()
		if expected != "" && current != expected {
			u.log(fmt.Sprintf("  WARNING: %s for %s still runs %s, not %s - check it with: opencli docker-update %s --dry-run", svc, label, updShort(current), updShort(expected), image))
			u.failed++
		}
		if strings.HasPrefix(svc, "php-fpm-") && ctx != "root" && dependents == "" {
			if !updWaitFPM(ctx, cname) {
				u.log(fmt.Sprintf("  WARNING: %s for %s is not listening on 9000 or its socket after 2 minutes, reloading the webserver anyway", svc, label))
				u.failed++
			}
			if !u.reloadWebserver(ctx) {
				u.failed++
			}
		}
	}

	u.log("=== Update run finished ===")

	if u.failed > 0 {
		u.log(fmt.Sprintf("FAILED: %d problem(s) during the update - see %s", u.failed, u.logFile))
		sh.Opencli("sentinel", "--action=docker_update", "--title=Docker Image Update failed",
			"--message=Docker image update had "+strconv.Itoa(u.failed)+" problem(s). Detailed report was saved in: "+u.logFile).Quiet().Background()
		return 1
	}
	return 0
}

// starts the podman socket again for users whose socket gave up while the shared store was unreadable during a pull
func (u *updState) reviveSockets() {
	for _, ctx := range u.contexts {
		if ctx == "root" {
			continue
		}
		if !sh.C("systemctl", "--user", "-M", ctx+"@", "is-failed", "--quiet", "podman.socket").NoErr().OK() {
			continue
		}
		sh.C("systemctl", "--user", "-M", ctx+"@", "reset-failed", "podman.socket", "podman.service").Quiet().Run()
		if sh.C("systemctl", "--user", "-M", ctx+"@", "start", "podman.socket").Quiet().OK() {
			u.log("  started the podman socket again for " + u.label[ctx])
		} else {
			u.log(fmt.Sprintf("  ERROR: podman socket for %s is down, start it with: systemctl --user -M %s@ start podman.socket", u.label[ctx], ctx))
			u.failed++
		}
	}
}

// cp -a for one file, mode, owner and times are kept
func updCopyArchive(src, dst string) {
	st, err := os.Stat(src)
	if err != nil {
		return
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return
	}
	if os.WriteFile(dst, b, st.Mode().Perm()) != nil {
		return
	}
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		os.Chown(dst, int(s.Uid), int(s.Gid))
	}
	os.Chmod(dst, st.Mode().Perm())
	os.Chtimes(dst, st.ModTime(), st.ModTime())
}
