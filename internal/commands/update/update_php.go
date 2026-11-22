// ##############################################################################
// Script Name: internal/commands/update/update_php.go
// Description: Check if update is available, install updates.
// Usage: opencli update [--check | --force [--no-restart] | --admin | --panel | --cli | --translations | --system | --modules | --compose | --env | --php | --wp | --ols | --litespeed | --apache | --clamav | --phpmyadmin | --postgres | --skeleton | --ssh | --varnish | --cron | --caddy]
// Author: Stefan Pejcic
// Created: 10.10.2023
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

package update

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opencli/internal/conf"
	"opencli/internal/requirement"
	"opencli/internal/sh"
)

// ---------------------- PHP: SYNC FILES AND ADD NEW PHP VERSIONS ---------------------- #
var phpSvcRe = regexp.MustCompile(`^  php-fpm-([0-9][0-9.]*):[[:space:]]*$`)

// X.Y for every php-fpm-X.Y service in a compose file
func phpVersionsInCompose(file string) []string {
	var v []string
	for _, l := range awkLines(conf.Read(file)) {
		if m := phpSvcRe.FindStringSubmatch(l); m != nil {
			v = append(v, m[1])
		}
	}
	return sortUniqV(v)
}

// svcRe matches the service header, svc is a regex there like in the awk
func svcRe(svc string) *regexp.Regexp {
	re, err := regexp.Compile("^  " + svc + ":[ \t]*$")
	if err != nil {
		re = regexp.MustCompile("^  " + regexp.QuoteMeta(svc) + ":[ \t]*$")
	}
	return re
}

var (
	topKeyRe = regexp.MustCompile(`^[^ \t#]`)
	svcKeyRe = regexp.MustCompile(`^  [^ #]`)
	blankRe  = regexp.MustCompile(`^[[:space:]]*$`)
)

// the whole service block, trailing blank lines stripped
func extractServiceBlock(file, svc string) string {
	re := svcRe(svc)
	var out []string
	f := false
	for _, l := range awkLines(conf.Read(file)) {
		if re.MatchString(l) {
			f = true
			out = append(out, l)
			continue
		}
		if f && (svcKeyRe.MatchString(l) || topKeyRe.MatchString(l)) {
			break
		}
		if f {
			out = append(out, l)
		}
	}
	for i := range out {
		out[i] = strings.ReplaceAll(out[i], "\r", "")
	}
	for len(out) > 0 && blankRe.MatchString(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// line after which a new php-fpm block goes: end of the last php-fpm service, or end of services, 0 appends
func phpInsertLine(file string) int {
	s, php := false, false
	last, lastphp := 0, 0
	for i, l := range awkLines(conf.Read(file)) {
		nr := i + 1
		if strings.HasPrefix(l, "services:") {
			s = true
			continue
		}
		if s && topKeyRe.MatchString(l) {
			s = false
		}
		if s && svcKeyRe.MatchString(l) {
			php = strings.HasPrefix(l, "  php-fpm-")
		}
		// awk's NF, only spaces and tabs split fields
		if s && strings.Trim(l, " \t") != "" {
			last = nr
			if php {
				lastphp = nr
			}
		}
	}
	if lastphp != 0 {
		return lastphp
	}
	return last
}

// inserts block after line n of file, optionally with a blank line in front, cat keeps owner and permissions
func insertAfterLine(file string, n int, block string, gap bool) {
	var b strings.Builder
	done := false
	dump := func() {
		if gap {
			b.WriteString("\n")
		}
		for _, l := range awkLines(block) {
			b.WriteString(l + "\n")
		}
		done = true
	}
	for i, l := range awkLines(conf.Read(file)) {
		b.WriteString(l + "\n")
		if i+1 == n {
			dump()
		}
	}
	if !done {
		dump()
	}
	conf.WriteInPlace(file, b.String())
}

// adds missing php-fpm services from src to dst
func mergePHPCompose(src, dst string) []string {
	var added []string
	have := phpVersionsInCompose(dst)
	for _, v := range phpVersionsInCompose(src) {
		if contains(have, v) {
			continue
		}
		block := extractServiceBlock(src, "php-fpm-"+v)
		if block == "" {
			continue
		}
		insertAfterLine(dst, phpInsertLine(dst), block, true)
		added = append(added, "php-fpm-"+v)
	}
	return added
}

func lastLineWithPrefix(file, prefix string) int {
	n := 0
	for i, l := range awkLines(conf.Read(file)) {
		if strings.HasPrefix(l, prefix) {
			n = i + 1
		}
	}
	return n
}

// adds missing PHP_FPM_X_Y_* vars from src to dst
func mergePHPEnv(src, dst string, versions []string) []string {
	var added []string
	for _, v := range versions {
		key := "PHP_FPM_" + strings.ReplaceAll(v, ".", "_") + "_"
		var missing, names []string
		for _, l := range awkLines(conf.Read(src)) {
			if !strings.HasPrefix(l, key) {
				continue
			}
			l = strings.ReplaceAll(l, "\r", "")
			name, _, _ := strings.Cut(l, "=")
			if lastLineWithPrefix(dst, name+"=") == 0 {
				missing = append(missing, l)
				names = append(names, name)
			}
		}
		if len(missing) == 0 {
			continue
		}
		if line := lastLineWithPrefix(dst, key); line > 0 {
			insertAfterLine(dst, line, strings.Join(missing, "\n")+"\n", false)
		} else {
			insertAfterLine(dst, lastLineWithPrefix(dst, "PHP_FPM_"), "# PHP "+v+"\n"+strings.Join(missing, "\n")+"\n", true)
		}
		added = append(added, names...)
	}
	return added
}

type volLine struct {
	nr   int
	line string
}

// volume entries of a service with their line numbers
func serviceVolumes(file, svc string) []volLine {
	re := svcRe(svc)
	var out []volLine
	f, v := false, false
	for i, l := range awkLines(conf.Read(file)) {
		if re.MatchString(l) {
			f = true
			continue
		}
		if f && (svcKeyRe.MatchString(l) || topKeyRe.MatchString(l)) {
			break
		}
		if f && strings.HasPrefix(l, "    volumes:") {
			v = true
			continue
		}
		if f && v && len(l) > 4 && strings.HasPrefix(l, "    ") && l[4] != ' ' {
			v = false
		}
		if f && v && strings.HasPrefix(l, "      - ") {
			out = append(out, volLine{i + 1, strings.ReplaceAll(l, "\r", "")})
		}
	}
	return out
}

// container side of a volume line, "- ./a:/b:ro" -> /b
func volumeTarget(l string) string {
	if i := strings.Index(l, "- "); i >= 0 {
		l = l[i+2:]
	}
	l = strings.NewReplacer(`"`, "", "'", "").Replace(l)
	f := strings.Split(l, ":")
	if len(f) < 2 {
		return l
	}
	return f[1]
}

var volSrcRe = regexp.MustCompile(`^ *- *`)

// adds volumes of svc from src that are missing in dst, only lines containing want
func mergeServiceMounts(src, dst, svc, want string) []string {
	var added []string
	hdr, err := regexp.Compile("^  " + svc + ":[[:space:]]*$")
	if err != nil {
		return nil
	}
	found := false
	for _, l := range awkLines(conf.Read(dst)) {
		if hdr.MatchString(l) {
			found = true
		}
	}
	if !found {
		return nil
	}
	for _, sv := range serviceVolumes(src, svc) {
		line := sv.line
		if !strings.Contains(line, want) {
			continue
		}
		vols := serviceVolumes(dst, svc)
		if len(vols) == 0 {
			break
		}
		target := volumeTarget(line)
		dup := false
		for _, dv := range vols {
			if volumeTarget(dv.line) == target {
				dup = true
			}
		}
		if dup {
			continue
		}
		// put it next to mounts from the same dir, else at the end of volumes
		source := volSrcRe.ReplaceAllString(line, "")
		source, _, _ = strings.Cut(source, ":")
		dir := source
		if i := strings.LastIndex(source, "/"); i >= 0 {
			dir = source[:i]
		}
		at := 0
		for _, dv := range vols {
			if strings.Contains(dv.line, "- "+dir+"/") {
				at = dv.nr
			}
		}
		if at == 0 {
			at = vols[len(vols)-1].nr
		}
		insertAfterLine(dst, at, line+"\n", false)
		added = append(added, svc+":"+source)
	}
	return added
}

// adds missing php mounts to openlitespeed and missing ionCube mounts to existing php-fpm services
func mergePHPMounts(src, dst string) []string {
	added := mergeServiceMounts(src, dst, "openlitespeed", "php")
	for _, v := range phpVersionsInCompose(dst) {
		added = append(added, mergeServiceMounts(src, dst, "php-fpm-"+v, "ioncube")...)
	}
	return added
}

func composeConfigValid(dir string) bool {
	return sh.C("podman-compose", "config").Dir(dir).Quiet().Run() == nil
}

func (u *updater) updatePHP() bool {
	requirement.Command("podman-compose")
	repoURL := "https://github.com/stefanpejcic/openpanel-configuration/archive/refs/heads/main.tar.gz"
	templateDir := "/etc/openpanel/docker/compose/1.0"
	ts := time.Now().Format("20060102_150405")
	tmpDir, _ := os.MkdirTemp("", "tmp.")

	u.header("Updating PHP")

	// 1. fresh copy of the configuration repo
	u.info("Downloading configuration files from github")
	if wgetDownload(bgCtx(), repoURL, tmpDir+"/cfg.tar.gz", 30*time.Second, 2) != 0 || !sh.C("tar", "-xzf", tmpDir+"/cfg.tar.gz", "-C", tmpDir).OK() {
		u.err("Failed to download " + repoURL)
		os.RemoveAll(tmpDir)
		return false
	}
	src := firstSubdir(tmpDir)
	if !isDir(src+"/php") || !isFile(src+"/docker/compose/1.0/docker-compose.yml") || !isFile(src+"/docker/compose/1.0/.env") {
		u.err("Downloaded archive is missing php/ or docker/compose/1.0/ files")
		os.RemoveAll(tmpDir)
		return false
	}
	remoteCompose := src + "/docker/compose/1.0/docker-compose.yml"
	remoteEnv := src + "/docker/compose/1.0/.env"
	versions := phpVersionsInCompose(remoteCompose)
	u.info("PHP versions on github: " + strings.Join(versions, " ") + " ")

	// 2. overwrite everything in /etc/openpanel/php/
	u.info("Overwriting /etc/openpanel/php/")
	os.MkdirAll("/etc/openpanel/php", 0o755)
	copyTree(src+"/php", "/etc/openpanel/php")
	u.info("[✔] /etc/openpanel/php/ updated")

	// 3. template files, then 4. every user, same steps for both
	total, changed, unchanged, failed, skipped := 0, 0, 0, 0, 0
	var summary []string

	// merge into dir, label is for output
	mergeDir := func(dir, label string) string {
		compose, env := dir+"/docker-compose.yml", dir+"/.env"
		if !isFile(compose) || !isFile(env) {
			u.warn("[" + label + "] docker-compose.yml or .env missing, skipping")
			return "skipped"
		}
		if !composeConfigValid(dir) {
			u.warn("[" + label + "] config is already invalid before changes, skipping")
			return "skipped"
		}
		cb, eb := compose+".php_"+ts+".bak", env+".php_"+ts+".bak"
		copyFile(compose, cb, true)
		copyFile(env, eb, true)

		added := mergePHPCompose(remoteCompose, compose)
		added = append(added, mergePHPEnv(remoteEnv, env, versions)...)
		mounts := mergePHPMounts(remoteCompose, compose)

		if len(added) == 0 && len(mounts) == 0 {
			os.Remove(cb)
			os.Remove(eb)
			u.info("[" + label + "] up to date")
			return "unchanged"
		}
		if !composeConfigValid(dir) {
			copyFile(cb, compose, true)
			copyFile(eb, env, true)
			u.err("[" + label + "] config invalid after changes, restored backup")
			return "failed"
		}

		// the new services mount ./php.ini/X.Y.ini so it has to exist
		if isDir(dir + "/php.ini") {
			for _, v := range versions {
				ini := dir + "/php.ini/" + v + ".ini"
				if !isFile(ini) && isFile("/etc/openpanel/php/ini/"+v+".ini") {
					copyFile("/etc/openpanel/php/ini/"+v+".ini", ini, false)
					if st, err := os.Stat(dir + "/php.ini"); err == nil {
						s := st.Sys().(*syscall.Stat_t)
						os.Chown(ini, int(s.Uid), int(s.Gid))
					}
					added = append(added, "php.ini/"+v+".ini")
				}
			}
		}

		res := "changed:"
		if len(added) > 0 {
			u.info("[" + label + "] added: " + strings.Join(added, " "))
			res += " " + strings.Join(added, " ")
		}
		if len(mounts) > 0 {
			u.info("[" + label + "] mounts added: " + strings.Join(mounts, " "))
			res += " | mounts: " + strings.Join(mounts, " ")
		}
		u.info("[" + label + "] backups: " + cb + ", " + eb)
		return res
	}

	u.info("Checking template in " + templateDir)
	summary = append(summary, "template: "+mergeDir(templateDir, "template"))

	homes, _ := filepath.Glob("/home/*")
	sort.Strings(homes)
	for _, home := range homes {
		home = strings.TrimSuffix(home, "/")
		if !isDir(home) || !isFile(home+"/docker-compose.yml") {
			continue
		}
		user := filepath.Base(home)
		total++
		u.info("Checking user " + user + " (" + strconv.Itoa(total) + ")")
		switch r := mergeDir(home, user); r {
		case "unchanged":
			unchanged++
		case "skipped":
			skipped++
			summary = append(summary, user+": skipped")
		case "failed":
			failed++
			summary = append(summary, user+": failed, restored")
		default:
			changed++
			summary = append(summary, user+": "+r)
		}
	}

	os.RemoveAll(tmpDir)

	u.header("PHP update summary")
	for _, s := range summary {
		u.printf("  %s\n", s)
	}
	u.printf("  Users checked: %d | updated: %d | up to date: %d | skipped: %d | failed: %d\n", total, changed, unchanged, skipped, failed)
	return failed == 0
}
