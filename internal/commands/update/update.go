// ##############################################################################
// Script Name: internal/commands/update/update.go
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
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/completion"
	"opencli/internal/conf"
	"opencli/internal/notify"
	"opencli/internal/ojson"
	"opencli/internal/redis"
	"opencli/internal/sh"
	"opencli/internal/text"
)

const usageLine = "opencli update [--check | --force [--no-restart] | --admin | --panel | --cli | --translations | --system | --modules | --compose | --env | --php | --wp | --ols | --litespeed | --apache | --clamav | --phpmyadmin | --postgres | --skeleton | --ssh | --varnish | --cron | --caddy]"

func init() {
	cli.Register(&cli.Command{
		Name:  "update",
		Desc:  "Check if update is available, install updates.",
		Usage: usageLine,
		Run:   run,
	})
}

// ---------------------- CONSTANTS ---------------------- #
const (
	composeFile          = "/root/docker-compose.yml"
	configFile           = "/etc/openpanel/openpanel/conf/openpanel.config"
	skipVersionsFile     = "/etc/openpanel/upgrade/skip_versions"
	keepKernels          = 2
	updateTimeout        = 300
	openpanelRestartFlag = "/root/openpanel_restart_needed"
	openadminRestartFlag = "/root/openadmin_restart_needed"
	updateLock           = "/var/lock/openpanel_update.lock"
	updateMaxRuntime     = 1800 // update gets killed and its lock counts as stale after 30min
	configRepoURL        = "https://github.com/stefanpejcic/openpanel-configuration/archive/refs/heads/main.tar.gz"
)

// ---------------------- COLOR CODES ---------------------- #
const (
	red    = "\033[0;31m"
	green  = "\033[0;32m"
	yellow = "\033[1;33m"
	blue   = "\033[0;34m"
	cyan   = "\033[0;36m"
	nc     = "\033[0m" // no color/restart
)

type updater struct {
	noRestart bool
	beta      bool
	image     string
	imageDone bool
	logFile   string
	errors    []string
	out, errw io.Writer
	configTmp string
	configSrc string
	localVer  string
}

// ---------------------- LOGGING FUNCTIONS ---------------------- #
func (u *updater) info(msg string)  { fmt.Fprintf(u.out, "%s[INFO]%s %s\n", green, nc, msg) }
func (u *updater) warn(msg string)  { fmt.Fprintf(u.out, "%s[WARN]%s %s\n", yellow, nc, msg) }
func (u *updater) err(msg string)   { fmt.Fprintf(u.errw, "%s[ERROR]%s %s\n", red, nc, msg) }
func (u *updater) debug(msg string) { fmt.Fprintf(u.out, "%s[DEBUG]%s %s\n", blue, nc, msg) }

func (u *updater) header(title string) { fmt.Fprintf(u.out, "%s===== %s =====%s\n", blue, title, nc) }

func (u *updater) appendLog(s string) {
	if f, err := os.OpenFile(u.logFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
		f.WriteString(s)
		f.Close()
	}
}

// log is the step marker of a full update, in the log file and on screen
func (u *updater) log(msg string) {
	u.appendLog(fmt.Sprintf("\n---- %s ----\n", msg))
	fmt.Fprintf(u.out, "%s---- %s ----%s\n", cyan, msg, nc)
}

// say prints plain with --no-log, else as a log() step
func (u *updater) say(noLog bool, msg string) {
	if noLog {
		fmt.Fprintln(u.out, msg)
	} else {
		u.log(msg)
	}
}

// logWriter opens the log for appending, like >> "$log_file"
func (u *updater) logWriter() *os.File {
	f, err := os.OpenFile(u.logFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil
	}
	return f
}

// ---------------------- USAGE ---------------------- #
func usage() {
	fmt.Print(`Usage: opencli update [OPTION]...

Options:
    --check             Check if update is available
    --force             Force update even when autopatch/autoupdate is disabled
    --no-restart        Don't restart OpenPanel/OpenAdmin after the update, flag them as needing a restart instead
    (no argument)       Update if autopatch/autoupdate is enabled
    --admin             Update OpenAdmin UI only
    --panel             Update OpenPanel UI only
    --cli               Update OpenCLI only
    --translations      Update translation files and restart OpenPanel UI
    --system            Update system packages and kernel, purge older kernels, check if reboot required
    --modules           Update OpenAdmin modules/features list
    --compose           Update docker-compose.yml template in /etc/openpanel/docker/compose/1.0/
    --env               Update .env template in /etc/openpanel/docker/compose/1.0/
    --php               Update /etc/openpanel/php/ and add new PHP versions to the template and all users
    --wp                Update WP-CLI (/etc/openpanel/wordpress/wp-cli.phar)
    --ols               Update OpenLiteSpeed files in /etc/openpanel/openlitespeed/
    --litespeed         Update LiteSpeed Enterprise files in /etc/openpanel/litespeed/
    --apache            Update Apache files in /etc/openpanel/apache/
    --clamav            Update /etc/openpanel/clamav/ and recreate the clamav container with a new image if running
    --phpmyadmin        Update /etc/openpanel/mysql/phpmyadmin/ and recreate the phpmyadmin container with a new image if running
    --postgres          Update PostgreSQL files in /etc/openpanel/postgres/
    --skeleton          Update new user skeleton files in /etc/openpanel/skeleton/
    --ssh               Update SSH files in /etc/openpanel/ssh/
    --varnish           Update Varnish files in /etc/openpanel/varnish/
    --cron              Update /etc/openpanel/cron and /etc/cron.d/openpanel
    --caddy             Pull a new image for the running caddy container, validate the Caddyfile with it and recreate caddy
    -h, --help          Show this help message

Examples:
    opencli update                 # Update if auto-update is enabled
    opencli update --check         # Check for available updates
    opencli update --force         # Force update regardless of settings
    opencli update --force --no-restart # Update, restart services manually later
    opencli update --panel beta    # Update OpenPanel UI to the nightly-release
    opencli update --translations  # Update translation files and restart OpenPanel UI
    opencli update --system        # Update system packages and kernel
    opencli update --compose --env # Update both user templates
    opencli update --php           # Add new PHP versions to all users
    opencli update --wp --cron     # Update WP-CLI and cron jobs
    opencli update --phpmyadmin    # Update phpMyAdmin files and image
    opencli update --caddy         # Update the caddy image (e.g. openpanel/caddy-coraza)

Multiple options can be combined and run in the order given.
`)
	cli.Exit(1)
}

// ---------------------- DOCKER IMAGE ---------------------- #
// added in 1.7.42 to detect if custom image is used, read the first time it's needed
func (u *updater) imageName() string {
	if u.imageDone {
		return u.image
	}
	u.imageDone = true
	cfg := sh.C("podman-compose", "-f", composeFile, "config").NoErr().Str()
	f := false
	for _, l := range strings.Split(cfg, "\n") {
		fs := strings.Fields(l)
		if len(fs) == 0 {
			continue
		}
		if fs[0] == "openpanel:" {
			f = true
			continue
		}
		if f && fs[0] == "image:" {
			v := ""
			if len(fs) > 1 {
				v, _, _ = strings.Cut(fs[1], ":")
			}
			u.image = v
			break
		}
	}
	if u.image == "" {
		u.image = "openpanel/openpanel" // fallback
	}
	return u.image
}

// ---------------------- INSTALL PACKAGE ---------------------- #
func (u *updater) installPackage(pkg string, quiet bool) bool {
	c := func(name string, args ...string) *sh.Cmd {
		cmd := sh.C(name, args...).Stdout(u.out).Stderr(u.errw)
		if quiet {
			cmd.Quiet()
		}
		return cmd
	}
	if !quiet {
		u.info("Installing " + pkg)
	}
	switch {
	case sh.Has("apt-get"):
		return c("apt-get", "update").Run() == nil && c("apt-get", "install", "-y", pkg).Run() == nil
	case sh.Has("dnf"):
		return c("dnf", "install", "-y", pkg).Run() == nil
	case sh.Has("yum"):
		return c("yum", "install", "-y", pkg).Run() == nil
	default:
		u.err("No supported package manager found (apt/dnf/yum)")
		return false
	}
}

// ---------------------- HELPERS ---------------------- #
func (u *updater) writeNotification(severity, title, message, log string) {
	details := ""
	if log != "" {
		details = ojson.Compact(ojson.New().Set("log_file", log))
	}
	notify.Add("no", "unread", severity, "update", "update", title, message, details)
}

// every step that didn't work is added here, the update only reports success when it stays empty
func (u *updater) updateError(msg string) {
	u.err(msg)
	if u.logFile != "" {
		u.appendLog("[ERROR] " + msg + "\n")
	}
	u.errors = append(u.errors, msg)
}

func writeNotificationForUpdateCheck(severity, title, message string) {
	notify.Add("yes", "unread", severity, "update", "update", title, message, "")
}

// ---------------------- GET CURRENT ---------------------- #
func getLocalVersion() string {
	out, err := sh.Opencli("version").NoErr().Out()
	if err != nil {
		if out != "" {
			out += "\n"
		}
		out += "0.0.0"
	}
	return out
}

// ---------------------- GET AVAILABLE ---------------------- #
func (u *updater) getRemoteVersion(beta bool) string {
	body := curlBody("https://hub.docker.com/v2/repositories/"+u.imageName()+"/tags", 0)
	var tags []string
	if o, err := ojson.ParseObject(strings.TrimSpace(body)); err == nil {
		if r, ok := o.Get("results"); ok {
			if arr, ok := r.([]any); ok {
				for _, it := range arr {
					if obj, ok := it.(*ojson.Object); ok {
						v, _ := obj.Get("name")
						if s, ok := v.(string); ok {
							tags = append(tags, s)
						} else {
							tags = append(tags, ojson.Compact(v))
						}
					}
				}
			}
		}
	}
	if len(tags) == 0 {
		return ""
	}
	var pick []string
	for _, t := range tags {
		if beta {
			if strings.HasSuffix(t, "-beta") {
				pick = append(pick, strings.TrimSuffix(t, "-beta"))
			}
		} else if t != "latest" && !strings.HasSuffix(t, "-beta") {
			pick = append(pick, t)
		}
	}
	if len(pick) == 0 {
		return ""
	}
	sort.SliceStable(pick, func(i, j int) bool { return versionBefore(pick[i], pick[j]) })
	return pick[len(pick)-1]
}

// versionBefore is sort -V order, ties fall back to a byte compare like sort does
func versionBefore(a, b string) bool {
	if text.VersionLess(a, b) {
		return true
	}
	if text.VersionLess(b, a) {
		return false
	}
	return a < b
}

// ---------------------- COMPARE, DUH! ---------------------- #
func compareVersions(v1, v2 string) int {
	if v1 == v2 {
		return 0
	}
	if !versionBefore(v2, v1) {
		return -1
	}
	return 1
}

// ---------------------- USERS CAN SET VERSION TO BE SKIPPED ---------------------- #
func isVersionSkipped(version string) bool {
	b, err := os.ReadFile(skipVersionsFile)
	if err != nil {
		return false
	}
	re, err := regexp.Compile(version)
	if err != nil {
		re = regexp.MustCompile(regexp.QuoteMeta(version))
	}
	for _, l := range strings.Split(string(b), "\n") {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

// ---------------------- READ AUTOPATCH AND AUTOUPDATE VALUES ---------------------- #
func getConfigValue(key, def string) string {
	if !isFile(configFile) {
		return def
	}
	re, err := regexp.Compile("^" + key + "=")
	if err != nil {
		return ""
	}
	for _, l := range conf.Lines(configFile) {
		if re.MatchString(l) {
			f := strings.Split(l, "=")
			return strings.ReplaceAll(f[1], `"`, "")
		}
	}
	return ""
}

// ---------------------- CHECK IF UPDATE IS AVAILABLE ---------------------- #
func (u *updater) updateCheck() {
	local := strings.TrimSuffix(getLocalVersion(), "-beta")
	remote := u.getRemoteVersion(false)

	if remote == "" {
		writeNotificationForUpdateCheck("warning", "Update check failed", "Failed connecting to Docker Hub")
		fmt.Fprintln(os.Stderr, `{"error": "Error fetching remote version"}`)
		cli.Exit(1)
	}
	if strings.HasSuffix(remote, "-beta") {
		cli.Exit(0)
	}
	status := ""
	switch compareVersions(local, remote) {
	case 0:
		status = "Up to date"
	case 1:
		status = "Local version is greater"
	case -1:
		if isVersionSkipped(remote) {
			status = "Skipped version"
		} else {
			writeNotificationForUpdateCheck("info", "New OpenPanel update is available", "Installed version: "+local+" | Available version: "+remote)
			status = "Update available"
		}
	}
	fmt.Printf(`{"status": "%s", "installed_version": "%s", "latest_version": "%s"}`+"\n", status, local, remote)
}

// latestFromCheck is opencli update --check | jq -r '.latest_version'
func latestFromCheck() string {
	out := sh.Opencli("update", "--check").NoErr().Str()
	o, err := ojson.ParseObject(strings.TrimSpace(out))
	if err != nil {
		return ""
	}
	v, ok := o.Get("latest_version")
	if !ok || v == nil {
		return "null"
	}
	return ojson.Text(v)
}

// ---------------------- CHECKS IF CUSTOM FILE EXISTS AND RUNS IT ---------------------- #

// https://github.com/stefanpejcic/OpenPanel/issues/984
func (u *updater) runVersionSpecificScriptsInRange(from, to string) {
	split := func(v string) (int, int, int) {
		p := strings.SplitN(v, ".", 3)
		for len(p) < 3 {
			p = append(p, "")
		}
		return text.Atoi(p[0]), text.Atoi(p[1]), text.Atoi(p[2])
	}
	fromMaj, fromMin, fromPat := split(from)
	toMaj, toMin, toPat := split(to)

	u.info(fmt.Sprintf("Running version-specific scripts from %s to %s", from, to))

	for maj := fromMaj; maj <= toMaj; maj++ {
		minStart, minEnd := 0, 999
		if maj == fromMaj {
			minStart = fromMin
		}
		if maj == toMaj {
			minEnd = toMin
		}
		for min := minStart; min <= minEnd; min++ {
			patStart, patEnd := 0, 999
			if maj == fromMaj && min == fromMin {
				patStart = fromPat + 1
			}
			if maj == toMaj && min == toMin {
				patEnd = toPat
			}
			for pat := patStart; pat <= patEnd; pat++ {
				u.runVersionSpecificScript(fmt.Sprintf("%d.%d.%d", maj, min, pat))
			}
		}
	}
}

func (u *updater) runVersionSpecificScript(version string) {
	url := "https://raw.githubusercontent.com/stefanpejcic/OpenPanel/refs/heads/main/UPDATES/" + version + "/UPDATE.sh"
	u.info("Checking for version-specific update script")
	if !wgetSpider(url) {
		u.info("[✔] No version-specific script")
		return
	}
	u.info("Downloading and executing version-specific script: " + url)
	// run from a file with stdin closed: piped into bash, a command in the script that reads stdin (apt-get) ate the rest of it and bash went on mid-line
	tmp, err := os.CreateTemp("", "tmp.")
	if err != nil {
		return
	}
	script := tmp.Name()
	tmp.Close()
	defer os.Remove(script)
	if wgetDownload(bgCtx(), url, script, 15*time.Second, 2) != 0 {
		u.updateError(fmt.Sprintf("Failed to download the %s update script, run it with: wget -O - %s | bash", version, url))
		return
	}
	lf := u.logWriter()
	c := sh.C("bash", script).Timeout(updateTimeout * time.Second)
	if lf != nil {
		c.Stdout(lf).Stderr(lf)
		defer lf.Close()
	} else {
		c.Quiet()
	}
	switch code := c.Code(); code {
	case 0:
		u.info("[✔] Version-specific script executed successfully")
	case 124:
		u.updateError(fmt.Sprintf("The %s update script timed out after %d seconds and its remaining steps did not run, run it again: wget -O - %s | bash", version, updateTimeout, url))
	default:
		u.updateError(fmt.Sprintf("The %s update script failed with exit code %d, run it again: wget -O - %s | bash", version, code, url))
	}
}

// ---------------------- MAIN UPDATE FUNCTION - THIS IS WHERE MAGIC HAPPENS ---------------------- #
func (u *updater) runUpdateImmediately(version string) int {
	logDir := "/var/log/openpanel/updates"

	// ---------------------- 1. PREPARATION
	os.MkdirAll(logDir, 0o755)
	u.logFile = logDir + "/" + version + ".log"
	if isFile(u.logFile) {
		u.logFile = fmt.Sprintf("%s/%s_%s.log", logDir, version, time.Now().Format("20060102_150405"))
	}
	touch(u.logFile)

	// ---------------------- 2. START PROCESS, WRITE NOTIFICATION
	u.header("Starting update to version " + version)
	u.info("Update log: " + u.logFile)
	u.writeNotification("info", "OpenPanel update started", "Started update to version "+version+".", u.logFile)

	// ---------------------- 3. DOWNLOAD THE OPENCLI BINARY FROM GITHUB, CHECKED AGAINST SHA256SUMS (must be before the openpanel image update)
	u.updateOpencli(false)

	// ---------------------- 4. DOWNLOAD NEW IMAGE FROM DOCKER HUB
	u.log("Updating OpenPanel container image")
	img := u.imageName() + ":" + version
	// the pipe into tee decided the status, so only a log that can't be opened counts as a failed pull
	lf := u.logWriter()
	if lf == nil {
		sh.C("podman", "image", "pull", img).Timeout(60 * time.Second).ErrToOut().Run()
		u.err("Failed to pull image or command timed out: podman image pull " + img)
		notify.DeleteTitle("OpenPanel update started")
		u.writeNotification("critical", "OpenPanel update failed!", "OpenPanel failed to update to version "+version+".", u.logFile)
		u.log("Update failed!")
		return 1
	}
	w := io.MultiWriter(os.Stdout, lf)
	sh.C("podman", "image", "pull", img).Timeout(60 * time.Second).Stdout(w).Stderr(w).Run()
	lf.Close()

	u.log("[✔] image " + img + " downloaded successfully")

	u.log("Updating version in /root/.env")
	if isFile(conf.RootEnv) {
		lines := strings.Split(conf.Read(conf.RootEnv), "\n")
		want := `VERSION="` + version + `"`
		for i, l := range lines {
			if strings.HasPrefix(l, "VERSION=") {
				lines[i] = want
			}
		}
		conf.WriteFileKeep(conf.RootEnv, strings.Join(lines, "\n"), false)
		found := false
		for _, l := range conf.Lines(conf.RootEnv) {
			if l == want {
				found = true
			}
		}
		if !found {
			u.updateError(`Failed to set VERSION="` + version + `" in /root/.env`)
		}
	}

	if u.noRestart {
		u.log("[!] Skipping OpenPanel restart, restart the 'openpanel' container to apply the new version")
		os.WriteFile(openpanelRestartFlag, []byte("Restart needed for OpenPanel service.\n"), 0o644)
	} else {
		u.log("Restarting OpenPanel service")
		if isFile("/root/docker-compose.yml") || isFile("/root/compose.yml") {
			if sh.C("podman-compose", "down", "openpanel").Dir("/root").Run() == nil {
				if lf := u.logWriter(); lf != nil {
					w := io.MultiWriter(os.Stdout, lf)
					sh.C("podman-compose", "up", "-d", "openpanel").Dir("/root").Stdout(w).Stderr(w).Run()
					lf.Close()
				}
			}
		}
		u.verifyOpenpanelRunning(version)
	}
	redis.DropKey("openpanel_cache_app.get_openpanel_version")

	u.log("Cleaning up previous images")
	u.purgePreviousImages(img)

	// ---------------------- 5. UPDATE INSTALLED LOCALES
	if !u.updateLocales(false) {
		u.updateError("Some locales failed to update, run: opencli update --translations")
	}

	// ---------------------- 6. UPDATE MODULES/FEATURES
	if !u.updateModules(false) {
		u.updateError("Failed to update /etc/openpanel/openadmin/config/features.json, run: opencli update --modules")
	}

	// ---------------------- 7. DOWNLOAD OPENADMIN FILES FROM GITHUB
	u.updateOpenadmin(false)

	// ---------------------- 8. RUN VERSION-SPECIFIC FILES IF EXIST
	u.runVersionSpecificScriptsInRange(u.localVer, version)

	// ---------------------- 9. REGENERATE LOGROTATE CONFIGS
	// logrotate.d files are only written by server-logrotate, so its fixes never reach existing servers otherwise
	u.log("Regenerating logrotate configs")
	if lf := u.logWriter(); lf != nil {
		err := sh.Opencli("server-logrotate").Stdout(lf).Stderr(lf).Run()
		lf.Close()
		if err != nil {
			u.updateError("Failed to regenerate logrotate configs, run: opencli server-logrotate")
		}
	}

	// ---------------------- 10. IF MAJOR VERSION, ALSO UPDATE SYSTEM, DOCKER AND KERNEL
	curMajor, _, _ := strings.Cut(u.localVer, ".")
	newMajor, _, _ := strings.Cut(version, ".")
	if text.Atoi(curMajor) < text.Atoi(newMajor) {
		u.updateSystem("-y")
		u.log("Updating caddy image")
		ok := true
		if lf := u.logWriter(); lf != nil {
			out, errw := u.out, u.errw
			u.out, u.errw = lf, lf
			ok = u.updateCaddy()
			u.out, u.errw = out, errw
			lf.Close()
		}
		if !ok {
			u.updateError("Failed to update the caddy image, run: opencli update --caddy")
		}
	} else {
		u.log("[✔] Minor update - skipping system updates")
	}

	// ---------------------- 11. RUN POST-UPDATE HOOK IF EXISTS
	u.log("Checking for custom post-update scripts")
	u.runCustomPostupdateScript()

	// ---------------------- 12. CHECK, CLEANUP AND REPORT
	u.verifyOpenadminRunning()

	notify.DeleteTitle("OpenPanel update started")
	if len(u.errors) > 0 {
		u.log(fmt.Sprintf("Update to version %s completed with %d error(s):", version, len(u.errors)))
		for _, e := range u.errors {
			fmt.Println("  - " + e)
			u.appendLog("  - " + e + "\n")
		}
		u.writeNotification("warning", "OpenPanel update completed with errors", fmt.Sprintf("Updated to version %s, but %d step(s) failed: %s", version, len(u.errors), strings.Join(u.errors, "; ")), u.logFile)
		return 1
	}
	notify.DeleteTitle("New OpenPanel update is available")
	u.writeNotification("info", "OpenPanel updated successfully!", "OpenPanel updated to version "+version+".", u.logFile)
	u.log("Update completed successfully!")
	return 0
}

// waits up to a minute for the openpanel container to be running on the new image
func (u *updater) verifyOpenpanelRunning(version string) {
	state := ""
	for i := 0; i < 30; i++ {
		state = sh.C("podman", "inspect", "-f", "{{.State.Status}}", "openpanel").NoErr().Str()
		if state == "running" {
			break
		}
		time.Sleep(2 * time.Second)
	}
	image := sh.C("podman", "inspect", "-f", "{{.ImageName}}", "openpanel").NoErr().Str()
	switch {
	case state != "running":
		s := state
		if s == "" {
			s = "missing"
		}
		u.updateError("OpenPanel container is not running after the update (state: " + s + "), start it with: cd /root && podman-compose up -d openpanel")
	case !strings.HasSuffix(image, ":"+version):
		u.updateError("OpenPanel container runs " + image + " instead of version " + version + ", recreate it with: cd /root && podman-compose up -d --force-recreate openpanel")
	default:
		u.log("[✔] OpenPanel is running version " + version)
	}
}

func selfUpdateUnit() string { return fmt.Sprintf("openadmin-selfupdate-%d", os.Getpid()) }

// the OpenAdmin binary is swapped in a background unit, waits up to 2 minutes for it and checks the admin service came back
func (u *updater) verifyOpenadminRunning() {
	unit := selfUpdateUnit() + ".service"
	if !isDir("/usr/local/admin") || u.noRestart {
		return
	}
	active := func(name string) bool { return sh.C("systemctl", "is-active", "--quiet", name).Run() == nil }
	for i := 0; i < 60; i++ {
		if !active(unit) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	switch {
	case active(unit):
		u.updateError("OpenAdmin update is still running after 2 minutes, check: journalctl -u " + unit)
	case sh.C("systemctl", "is-enabled", "--quiet", "admin").NoErr().Run() == nil && !active("admin"):
		u.updateError("OpenAdmin (admin service) is not running after the update, start it with: systemctl start admin")
	default:
		u.log("[✔] OpenAdmin is running")
	}
}

// ---------------------- HELPER: RUNS AFTER DOWNLOADING NEW IMAGE ---------------------- #
// keep is the image just pulled: with --no-restart (OpenAdmin's Update now) no container runs it yet, so it would look unused and be removed
func (u *updater) purgePreviousImages(keep string) {
	u.info("Cleaning up old Podman images")
	var all []string
	prefix, err := regexp.Compile("^docker.io/" + u.imageName())
	if err != nil {
		prefix = regexp.MustCompile("^" + regexp.QuoteMeta("docker.io/"+u.imageName()))
	}
	for _, l := range strings.Split(sh.C("podman", "images", "--format", "{{.Repository}} {{.ID}}").Str(), "\n") {
		if prefix.MatchString(l) {
			if id := text.Field(l, 2); id != "" {
				all = append(all, id)
			}
		}
	}
	running := strings.Fields(sh.C("podman", "ps", "--format", "{{.Image}}").Str())
	var ids []string
	for _, img := range running {
		if id := sh.C("podman", "inspect", "--format", "{{.Id}}", img).NoErr().Str(); id != "" {
			ids = append(ids, strings.Split(id, "\n")...)
		}
	}
	if id := sh.C("podman", "inspect", "--format", "{{.Id}}", keep).NoErr().Str(); id != "" {
		ids = append(ids, strings.Split(id, "\n")...)
	}
	sort.Strings(ids)
	used := strings.Join(ids, "\n")
	for _, img := range all {
		re, err := regexp.Compile(img)
		if err != nil {
			re = regexp.MustCompile(regexp.QuoteMeta(img))
		}
		if re.MatchString(used) {
			u.debug("Skipping in-use image: " + img)
		} else {
			u.info("Removing unused image: " + img)
			sh.C("podman", "rmi", img).NoErr().Run()
		}
	}
}

// ---------------------- RUNS AFTER UPDATE ---------------------- #
func (u *updater) runCustomPostupdateScript() {
	scriptPath := "/root/openpanel_run_after_update"
	if st, err := os.Stat(scriptPath); err != nil || st.Size() == 0 {
		return
	}
	u.info("[!] Running custom post-update script: " + scriptPath)
	var w io.Writer = os.Stdout
	lf := u.logWriter()
	if lf != nil {
		w = io.MultiWriter(os.Stdout, lf)
		defer lf.Close()
	}
	rc := sh.C("bash", scriptPath).StdinReader(os.Stdin).Stdout(w).Stderr(w).Code()
	if rc != 0 {
		u.updateError(fmt.Sprintf("Custom post-update script %s failed with exit code %d", scriptPath, rc))
	}
}

// ---------------------- RUNS AFTER UPDATE ---------------------- #
func (u *updater) updateModules(noLog bool) bool {
	u.say(noLog, "Updating OpenAdmin modules")
	url := "https://raw.githubusercontent.com/stefanpejcic/openpanel-configuration/refs/heads/main/openadmin/config/features.json"
	features := "/etc/openpanel/openadmin/config/features.json"
	tmp := features + ".tmp"
	code := curlDownload(url, tmp, 10*time.Second, 60*time.Second, 2)
	if code == "200" && jsonStream(tmp) {
		os.Rename(tmp, features)
		return true
	}
	os.Remove(tmp)
	if code == "" {
		code = "000"
	}
	u.warn("Failed to download features from github (HTTP " + code + "), keeping current file")
	return false
}

func (u *updater) updateComposeTemplate(file string) bool {
	dir := "/etc/openpanel/docker/compose/1.0"
	url := "https://raw.githubusercontent.com/stefanpejcic/openpanel-configuration/refs/heads/main/docker/compose/1.0/" + file
	t, err := os.CreateTemp("", "tmp.")
	if err != nil {
		return false
	}
	tmp := t.Name()
	t.Close()

	u.info("Updating " + dir + "/" + file)
	if wgetDownload(bgCtx(), url, tmp, 10*time.Second, 1) == 0 && fileSize(tmp) > 0 {
		os.MkdirAll(dir, 0o755)
		// keep a copy in case the new template breaks something
		if isFile(dir+"/"+file) && copyFile(dir+"/"+file, dir+"/"+file+".bak", false) == nil {
			u.info("Previous file saved as " + dir + "/" + file + ".bak")
		}
		moveFile(tmp, dir+"/"+file)
		os.Chmod(dir+"/"+file, 0o644)
		u.info("[✔] " + file + " updated")
		return true
	}
	os.Remove(tmp)
	u.err("Failed to download " + url)
	return false
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

// moveFile is mv -f, falls back to copy and delete across filesystems
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	os.Remove(dst)
	if err := copyFile(src, dst, true); err != nil {
		return err
	}
	return os.Remove(src)
}

func touch(p string) {
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
	}
	now := time.Now()
	os.Chtimes(p, now, now)
}

func (u *updater) updateLocales(noLog bool) bool {
	u.say(noLog, "Updating installed locales")

	babel := "/etc/openpanel/openpanel/translations"
	repo := "stefanpejcic/openpanel-translations"

	if !isDir(babel) {
		u.say(noLog, "[!] No translations directory found, skipping")
		return true
	}

	updated, failed := 0, 0

	// remote folders have region codes (sr-rs, pt-br) while local ones are just the language
	body := curlBody("https://api.github.com/repos/"+repo+"/contents", 15*time.Second)
	var remote []string
	for _, m := range regexp.MustCompile(`"name": *"[a-z]{2}-[a-z]{2}"`).FindAllString(body, -1) {
		remote = append(remote, regexp.MustCompile(`[a-z]{2}-[a-z]{2}`).FindString(m))
	}

	files, _ := filepath.Glob(babel + "/*/LC_MESSAGES/messages.po")
	sort.Strings(files)
	for _, po := range files {
		if !isFile(po) {
			continue
		}
		two := ""
		parts := strings.Split(po, "/")
		for i, p := range parts {
			if p == "translations" && i+1 < len(parts) {
				two = parts[i+1]
			}
		}
		formatted := ""
		for _, r := range remote {
			if strings.HasPrefix(r, two+"-") {
				formatted = r
				break
			}
		}
		if formatted == "" {
			formatted = two + "-" + two
		}
		url := "https://raw.githubusercontent.com/" + repo + "/main/" + formatted + "/messages.po"

		// default wget retries 20x with 15min read timeout, that hung updates with several locales #1199
		tmp := po + ".tmp"
		code := curlDownload(url, tmp, 10*time.Second, 60*time.Second, 2)
		if code == "200" && hasMsgid(tmp) {
			os.Rename(tmp, po)
			updated++
			u.info("Updated locale: " + formatted)
		} else if code == "404" {
			os.Remove(tmp)
			u.warn("No remote locale found for: " + formatted + " (skipping)")
		} else {
			os.Remove(tmp)
			u.warn("Failed to download locale: " + formatted + " (HTTP " + code + ")")
			failed++
		}
	}

	if updated > 0 {
		redis.DropKey("openpanel_cache_app.get_available_locales")
	}

	summary := fmt.Sprintf("Locales updated: %d, failed: %d", updated, failed)
	u.say(noLog, "[✔] "+summary)
	return failed == 0
}

func hasMsgid(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "msgid") {
			return true
		}
	}
	return false
}

// ---------------------- UPDATES TRANSLATIONS ONLY, THEN RESTARTS OPENPANEL ---------------------- #
func (u *updater) updateTranslations() {
	u.updateLocales(true)
	u.info("Restarting OpenPanel service")
	sh.C("podman", "restart", "openpanel").Quiet().Run()
	u.info("[✔] Translations updated and OpenPanel restarted")
}

func (u *updater) updateOpenpanel() bool {
	if u.beta {
		base := u.getRemoteVersion(true)
		if base == "" {
			u.err("Could not find a beta image on Docker Hub for " + u.imageName())
			return false
		}
		u.info("Latest beta version found: " + base + "-beta")
		if isFile(conf.RootEnv) {
			lines := strings.Split(conf.Read(conf.RootEnv), "\n")
			for i, l := range lines {
				if strings.HasPrefix(l, "VERSION=") {
					lines[i] = `VERSION="` + base + `-beta"`
				}
			}
			conf.WriteFileKeep(conf.RootEnv, strings.Join(lines, "\n"), false)
		}
	}
	sh.C("podman-compose", "up", "-d", "openpanel", "--force-recreate", "--pull").Dir("/root").Run()
	redis.DropKey("openpanel_cache_app.get_openpanel_version")
	return true
}

func unameMachine() string {
	return sh.C("uname", "-m").NoErr().Str()
}

func (u *updater) updateOpenadmin(noLog bool) bool {
	u.say(noLog, "Updating OpenAdmin")
	if !isDir("/usr/local/admin") {
		return true
	}
	if isFile("/usr/local/admin/templates/emails/reports.html") {
		copyFile("/usr/local/admin/templates/emails/reports.html", "/tmp/report.html.backup", false)
	}

	var bin string
	switch m := unameMachine(); m {
	case "x86_64", "amd64":
		bin = "openadmin-amd64"
	case "aarch64", "arm64":
		bin = "openadmin-arm64"
	default:
		bin = m
	}

	remote := latestFromCheck()
	base := "https://github.com/stefanpejcic/openadmin/releases/download/" + remote + "/"
	url := base + bin
	targetLog := u.logFile
	if targetLog == "" {
		targetLog = "/dev/null"
	}

	if curlHeadCode(url) != "200" {
		u.updateError("No OpenAdmin release found at " + url + ", kept the previous OpenAdmin")
		return false
	}
	// download and check it before the service is stopped, a bad download keeps the running OpenAdmin
	newBin := "/usr/local/admin/" + bin + ".new"
	if code := u.fetchRelease(base, bin, newBin); code != 0 {
		os.Remove(newBin)
		if code == 124 {
			u.updateError(fmt.Sprintf("Downloading %s timed out after %d seconds, kept the previous OpenAdmin binary", url, updateTimeout))
		} else {
			u.updateError("Downloaded " + url + " doesn't match SHA256SUMS or doesn't run, kept the previous OpenAdmin binary")
		}
		return false
	}
	if u.noRestart {
		// mv over the running binary is safe, the old one keeps running until restarted
		if os.Rename(newBin, "/usr/local/admin/"+bin) == nil {
			if isFile("/tmp/report.html.backup") {
				copyFile("/tmp/report.html.backup", "/usr/local/admin/templates/emails/reports.html", false)
			}
			os.WriteFile(openadminRestartFlag, []byte("Restart needed\n"), 0o644)
			u.say(noLog, "[!] OpenAdmin updated, restart the 'admin' service to apply it")
		} else {
			os.Remove(newBin)
			u.updateError("Failed to replace /usr/local/admin/" + bin + ", kept the previous OpenAdmin binary")
		}
		return true
	}
	// systemd expands ${VAR} in the command line before bash sees it, so the script below uses $VAR only, never ${VAR}
	sh.C("systemd-run", "--collect", "--unit="+selfUpdateUnit(), "--no-block", "bash", "-c", `
			    admin_binary="`+bin+`"
			    target_log="`+targetLog+`"
			    was_active=false
			
			    # downloaded and checked against SHA256SUMS before this runs
			    [ -x "/usr/local/admin/$admin_binary.new" ] || exit 1
			
			    if systemctl is-active --quiet admin; then
			        was_active=true
			        if ! timeout 30 systemctl stop admin; then
			            systemctl kill -s SIGKILL admin
			            systemctl reset-failed admin
			        fi
			    fi
			
			    mv -f "/usr/local/admin/$admin_binary.new" "/usr/local/admin/$admin_binary"
			
			    [[ -f "/tmp/report.html.backup" ]] && cp /tmp/report.html.backup /usr/local/admin/templates/emails/reports.html
			
			    if [ "$was_active" = true ]; then
			        systemctl start admin
			        sleep 2
			        if ! systemctl is-active --quiet admin; then
			            {
			                echo "[$(date "+%Y-%m-%d %H:%M:%S")] admin service failed to start after update"
			                systemctl status admin --no-pager
			            } >> "$target_log" 2>&1
			        fi
			    fi
			`).Run()

	fmt.Fprintln(u.out, "[✔] OpenAdmin update triggered (applying in background)")
	return true
}

func (u *updater) updateOpencli(noLog bool) {
	u.say(noLog, "Updating OpenCLI")

	const dir = "/usr/local/opencli"
	base := "https://github.com/stefanpejcic/opencli/releases/latest/download/"
	asset := "opencli-" + runtime.GOARCH
	if wgetSpider(base + asset) {
		u.info("Downloading terminal commands from github: " + base + asset)
		os.MkdirAll(dir, 0o755)
		// download next to the binary so the final swap is an atomic same-filesystem rename
		tmp := dir + "/.opencli.new"
		code := u.fetchOpencli(base, asset, tmp)
		if code == 0 {
			if os.Rename(tmp, dir+"/opencli") == nil {
				u.info("[✔] Terminal commands updated successfully")
				cleanOpencliDir(dir)
			} else {
				u.updateError("Failed to replace " + dir + "/opencli, kept the previous version, run: opencli update --cli")
			}
		} else if code == 124 {
			u.updateError(fmt.Sprintf("Updating terminal commands timed out after %d seconds, kept the previous version, run: opencli update --cli", updateTimeout))
		} else {
			u.updateError(fmt.Sprintf("Updating terminal commands failed with exit code %d, kept the previous version, run: opencli update --cli", code))
		}
		os.Remove(tmp)
	} else {
		u.updateError("Failed to reach github, terminal commands not updated, run: opencli update --cli")
	}
	if _, err := os.Lstat("/usr/local/bin/opencli"); os.IsNotExist(err) {
		os.Symlink(dir+"/opencli", "/usr/local/bin/opencli")
	}
	// --help regenerates aliases.txt that tab-completion reads
	sh.C(dir+"/opencli", "--help").Quiet().Run()

	// (re)install bash tab-completion for opencli
	os.MkdirAll(filepath.Dir(completion.Path), 0o755)
	if os.WriteFile(completion.Path, completion.Script, 0o644) == nil {
		if !sh.OK("rpm", "-q", "bash-completion") && !sh.OK("dpkg", "-s", "bash-completion") {
			u.installPackage("bash-completion", true)
		}
		if isDir("/etc/bash_completion.d") {
			os.Remove("/etc/bash_completion.d/opencli")
			os.Symlink(completion.Path, "/etc/bash_completion.d/opencli")
		}
	}

	if noLog {
		sh.C("podman", "restart", "openpanel").Quiet().Run()
	}
}

// cleanOpencliDir drops the old bash scripts, the binary only needs itself, aliases.txt and lib/completion.bash
func cleanOpencliDir(dir string) {
	keep := map[string]bool{"opencli": true, "aliases.txt": true, "lib": true}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !keep[e.Name()] {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
	libs, _ := os.ReadDir(dir + "/lib")
	for _, e := range libs {
		if e.Name() != filepath.Base(completion.Path) {
			os.RemoveAll(filepath.Join(dir, "lib", e.Name()))
		}
	}
}

// fetchOpencli downloads the release binary within UPDATE_TIMEOUT, checks it against SHA256SUMS and that it runs, returns 124 on timeout
func (u *updater) fetchOpencli(base, asset, dest string) int {
	return u.fetchRelease(base, asset, dest)
}

// fetchRelease downloads base+asset to dest, checks it against the release SHA256SUMS and that `dest --version` runs, returns 124 on timeout
func (u *updater) fetchRelease(base, asset, dest string) int {
	ctx, cancel := ctxTimeout(updateTimeout * time.Second)
	defer cancel()
	sums := dest + ".sums"
	defer os.Remove(sums)
	for _, d := range [][2]string{{base + asset, dest}, {base + "SHA256SUMS", sums}} {
		if code := wgetDownload(ctx, d[0], d[1], 30*time.Second, 3); code != 0 {
			if ctx.Err() != nil {
				return 124
			}
			return code
		}
	}
	want := ""
	for _, l := range strings.Split(conf.Read(sums), "\n") {
		if f := strings.Fields(l); len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			want = f[0]
		}
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		return 1
	}
	if sum := sha256.Sum256(b); want == "" || hex.EncodeToString(sum[:]) != want {
		u.warn("Checksum of the downloaded " + asset + " doesn't match SHA256SUMS")
		return 1
	}
	os.Chmod(dest, 0o755)
	// a broken or wrong-arch binary must never replace the working one
	return sh.C(dest, "--version").Quiet().Timeout(10 * time.Second).Code()
}

// runs the update in a child that doesn't inherit the lock fd, otherwise conmon of containers it starts keeps the lock forever
func (u *updater) runUpdateLocked(from, to string) int {
	if st, err := os.Stat(updateLock); err == nil {
		age := int(time.Since(st.ModTime()).Seconds())
		if age > updateMaxRuntime {
			u.warn(fmt.Sprintf("Removing stale update lock from %d minutes ago", age/60))
			os.Remove(updateLock)
		}
	}

	rc := sh.C("flock", "-n", "-o", "-E", "75", updateLock, "timeout", "--foreground", "-k", "60", strconv.Itoa(updateMaxRuntime), sh.Self(), "update").
		Env("OPENPANEL_UPDATE_FROM="+from, "OPENPANEL_UPDATE_TO="+to, "OPENPANEL_UPDATE_NO_RESTART="+strconv.FormatBool(u.noRestart), "OPENCLI_RAW=1").
		StdinReader(os.Stdin).Code()

	switch rc {
	case 75:
		fmt.Println("[✘] Error: Update process is already running.")
		if pids := text.Xargs(sh.Str("fuser", updateLock)); pids != "" {
			fmt.Printf("Lock %s is held by PID: %s\n", updateLock, pids)
		}
		fmt.Println("Please wait for it to complete before retrying.")
	case 124, 137:
		u.err(fmt.Sprintf("Update did not finish within %d minutes and was stopped", updateMaxRuntime/60))
		notify.DeleteTitle("OpenPanel update started")
		u.writeNotification("critical", "OpenPanel update failed!", fmt.Sprintf("Update to version %s did not finish within %d minutes and was stopped.", to, updateMaxRuntime/60), "/var/log/openpanel/updates/")
	}
	return rc
}

// Main update check and execution
func (u *updater) checkUpdate(force bool) {
	if force {
		u.info("[!] Forcing update, ignoring autopatch and autoupdate settings")
	}

	var autopatch, autoupdate string
	if force {
		autopatch, autoupdate = "on", "on"
	} else {
		autopatch = getConfigValue("autopatch", "off")
		autoupdate = getConfigValue("autoupdate", "off")
	}

	if autopatch != "on" && autoupdate != "on" && !force {
		u.info("[!] Autopatch and Autoupdate are both disabled. No updates will be installed automatically.")
		return
	}

	local := strings.TrimSuffix(getLocalVersion(), "-beta")
	remote := latestFromCheck()

	if remote == "" || remote == "null" {
		u.info("No update available or unable to check for updates")
		return
	}

	if isVersionSkipped(remote) {
		u.info("[!] Version " + remote + " is skipped due to skip_versions configuration")
		return
	}

	if compareVersions(local, remote) == -1 || force {
		if autoupdate == "off" && !force {
			u.info("Update available, but only autopatch is enabled. Installing patch")
		} else {
			u.info("Update available and will be automatically installed")
		}
		u.runUpdateLocked(local, remote)
	} else {
		u.info("[✔] No update needed")
	}
}

// ---------------------- CONFIGURATION FILES FROM GITHUB ---------------------- #
// downloads openpanel-configuration once per run and sets configSrc to the extracted repo
func (u *updater) fetchConfigurationRepo() bool {
	if u.configSrc != "" && isDir(u.configSrc) {
		return true
	}
	u.configTmp, _ = os.MkdirTemp("", "tmp.")
	u.info("Downloading configuration files from github")
	if wgetDownload(bgCtx(), configRepoURL, u.configTmp+"/cfg.tar.gz", 30*time.Second, 2) != 0 || !sh.C("tar", "-xzf", u.configTmp+"/cfg.tar.gz", "-C", u.configTmp).OK() {
		u.err("Failed to download " + configRepoURL)
		os.RemoveAll(u.configTmp)
		u.configTmp = ""
		return false
	}
	u.configSrc = firstSubdir(u.configTmp)
	return true
}

// overwrites /etc/openpanel/$1 (file or dir) with the copy from github, local-only files are kept
func (u *updater) updateConfigPath(rel string) bool {
	if !u.fetchConfigurationRepo() {
		return false
	}
	src := u.configSrc + "/" + rel
	dst := "/etc/openpanel/" + rel
	switch {
	case isDir(src):
		os.MkdirAll(dst, 0o755)
		copyTree(src, dst)
	case isFile(src):
		os.MkdirAll(filepath.Dir(dst), 0o755)
		copyFile(src, dst, false)
	default:
		u.err(rel + " not found in the configuration repository")
		return false
	}
	u.info("[✔] " + dst + " updated")
	return true
}

func runningNames() []string {
	return strings.Split(sh.C("podman", "ps", "--format", "{{.Names}}").Str(), "\n")
}

func contains(list []string, s string) bool {
	for _, l := range list {
		if l == s {
			return true
		}
	}
	return false
}

// recreates a service from /root/docker-compose.yml with a freshly pulled image, only if it's already running
func (u *updater) refreshRunningContainer(service string) bool {
	if !contains(runningNames(), service) {
		u.info(service + " container is not running, skipping image update")
		return true
	}
	image := sh.C("podman", "inspect", "--format", "{{.ImageName}}", service).NoErr().Str()
	u.info("Removing " + service + " container")
	sh.C("podman-compose", "down", service).Dir("/root").Quiet().Run()
	if image != "" {
		u.info("Deleting image " + image)
		sh.C("podman", "rmi", "-f", image).Quiet().Run()
	}
	u.info("Downloading new " + service + " image")
	sh.C("podman-compose", "pull", service).Dir("/root").Quiet().Run()
	u.info("Starting " + service + " container")
	if sh.C("podman-compose", "up", "-d", service).Dir("/root").Quiet().Run() == nil {
		u.info("[✔] " + service + " container restarted with the new image")
		return true
	}
	u.err("Failed to start " + service + " - check: cd /root && podman-compose up -d " + service)
	return false
}

func (u *updater) updateWPCli() bool {
	if !u.updateConfigPath("wordpress/wp-cli.phar") {
		return false
	}
	return os.Chmod("/etc/openpanel/wordpress/wp-cli.phar", 0o755) == nil
}

func (u *updater) updateClamav() bool {
	if !u.updateConfigPath("clamav") {
		return false
	}
	return u.refreshRunningContainer("clamav")
}

func (u *updater) updatePhpmyadmin() bool {
	if !u.updateConfigPath("mysql/phpmyadmin") {
		return false
	}
	return u.refreshRunningContainer("phpmyadmin")
}

// pulls a new image for the running caddy (same tag, e.g. caddy-coraza:latest), validates the current Caddyfile with it and only then recreates caddy,
// so a failed pull or a config the new caddy rejects leaves the old one running
func (u *updater) updateCaddy() bool {
	running := strings.Split(sh.C("podman", "ps", "--format", "{{.Names}}").Stderr(u.errw).Str(), "\n")
	if !contains(running, "caddy") {
		u.info("caddy container is not running, skipping caddy image update")
		return true
	}
	image := sh.C("podman", "inspect", "--format", "{{.ImageName}}", "caddy").NoErr().Str()
	oldID := sh.C("podman", "inspect", "--format", "{{.Image}}", "caddy").NoErr().Str()
	if image == "" {
		u.err("Could not read the caddy container's image")
		return false
	}

	u.info("Downloading new caddy image " + image)
	if sh.C("podman", "pull", "-q", image).Timeout(300*time.Second).NoOut().Stderr(u.errw).Run() != nil {
		u.err("Failed to pull " + image + ", caddy keeps running the old image")
		return false
	}
	newID := sh.C("podman", "image", "inspect", "--format", "{{.Id}}", image).NoErr().Str()
	if oldID != "" && oldID == newID {
		u.info("[✔] caddy already runs the newest " + image)
		return true
	}

	// same mounts as the caddy service in /root/docker-compose.yml, so waf rules and imports resolve like in the real container
	out, err := sh.C("podman", "run", "--rm", "--network", "none",
		"-v", "/etc/openpanel/caddy/Caddyfile:/etc/caddy/Caddyfile:ro",
		"-v", "/etc/openpanel/caddy/:/etc/openpanel/caddy:ro",
		"-v", "/var/log/caddy/:/var/log/caddy/",
		image, "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile").Timeout(120 * time.Second).Combined()
	if err != nil {
		u.err("The new " + image + " does not accept /etc/openpanel/caddy/Caddyfile, caddy keeps running the old image:")
		lines := awkLines(out + "\n")
		if len(lines) > 5 {
			lines = lines[len(lines)-5:]
		}
		for _, l := range lines {
			fmt.Fprintln(u.errw, l)
		}
		return false
	}

	u.info("Recreating caddy with the new image")
	sh.C("podman-compose", "down", "caddy").Dir("/root").Quiet().Run()
	if sh.C("podman-compose", "up", "-d", "caddy").Dir("/root").Quiet().Run() != nil {
		u.err("Failed to start caddy with the new image - check: cd /root && podman-compose up -d caddy")
		return false
	}
	// caddy needs a few seconds to load the waf rules
	for i := 1; i <= 15; i++ {
		if sh.C("podman", "inspect", "--format", "{{.State.Running}}", "caddy").NoErr().Str() == "true" && sh.C("podman", "exec", "caddy", "caddy", "version").Quiet().Run() == nil {
			v := text.Field(sh.C("podman", "exec", "caddy", "caddy", "version").Stderr(u.errw).Str(), 1)
			u.info("[✔] caddy now runs " + v + " from " + image)
			// drop the old image so its CVEs don't stay on disk, ignored when something else still uses it
			if oldID != "" {
				sh.C("podman", "rmi", oldID).Quiet().Run()
			}
			return true
		}
		time.Sleep(2 * time.Second)
	}
	u.err("caddy did not come up after the image update - check: podman logs caddy")
	return false
}

func (u *updater) updateCron() bool {
	if !u.updateConfigPath("cron") {
		return false
	}
	// same as the installer: root-owned 0600, cron picks up the change on its own
	sh.C("install", "-m", "600", "-o", "root", "-g", "root", "/etc/openpanel/cron", "/etc/cron.d/openpanel").Run()
	if sh.Has("restorecon") {
		sh.C("restorecon", "/etc/cron.d/openpanel").Run()
	}
	u.info("[✔] /etc/cron.d/openpanel updated")
	return true
}

var modeFlags = map[string]bool{"--check": true, "--force": true, "--admin": true, "--panel": true, "--cli": true, "--translations": true, "--system": true, "--modules": true, "--compose": true, "--env": true, "--php": true, "--wp": true, "--ols": true, "--litespeed": true, "--apache": true, "--clamav": true, "--phpmyadmin": true, "--postgres": true, "--skeleton": true, "--ssh": true, "--varnish": true, "--cron": true, "--caddy": true}

// ---------------------- RUNS CHECK OR STARTS UPDATE ---------------------- #
func run(args []string) int {
	u := &updater{out: os.Stdout, errw: os.Stderr}

	// re-entered by runUpdateLocked while it holds the lock
	if to := os.Getenv("OPENPANEL_UPDATE_TO"); to != "" {
		touch(updateLock) // lock age counts from the start of this update
		nr := os.Getenv("OPENPANEL_UPDATE_NO_RESTART")
		if nr == "" {
			nr = "false"
		}
		u.noRestart = nr == "true"
		u.localVer = os.Getenv("OPENPANEL_UPDATE_FROM")
		// unset so nested opencli update calls (like --check) don't re-enter this branch
		os.Unsetenv("OPENPANEL_UPDATE_FROM")
		os.Unsetenv("OPENPANEL_UPDATE_TO")
		os.Unsetenv("OPENPANEL_UPDATE_NO_RESTART")
		return u.runUpdateImmediately(to)
	}

	var modes []string
	for _, arg := range args {
		switch {
		case modeFlags[arg]:
			// skip duplicates so each mode runs once
			if !strings.Contains(" "+strings.Join(modes, " ")+" ", " "+strings.TrimPrefix(arg, "--")+" ") {
				modes = append(modes, strings.TrimPrefix(arg, "--"))
			}
		case arg == "beta":
			u.beta = true
		case arg == "--no-restart":
			u.noRestart = true
		case arg == "-h" || arg == "--help":
			usage()
		default:
			u.err("[!] Unknown argument: " + arg)
			usage()
		}
	}

	if len(modes) == 0 {
		u.checkUpdate(false)
		return 0
	}

	for _, mode := range modes {
		switch mode {
		case "check":
			u.updateCheck()
		case "force":
			u.checkUpdate(true)
		case "panel":
			u.updateOpenpanel()
		case "cli":
			u.updateOpencli(true)
		case "admin":
			u.updateOpenadmin(true)
		case "translations":
			u.updateTranslations()
		case "system":
			u.updateSystem("")
		case "modules":
			u.updateModules(true)
		case "compose":
			u.updateComposeTemplate("docker-compose.yml")
		case "env":
			u.updateComposeTemplate(".env")
		case "php":
			u.updatePHP()
		case "wp":
			u.updateWPCli()
		case "ols":
			u.updateConfigPath("openlitespeed")
		case "litespeed":
			u.updateConfigPath("litespeed")
		case "apache":
			u.updateConfigPath("apache")
		case "clamav":
			u.updateClamav()
		case "phpmyadmin":
			u.updatePhpmyadmin()
		case "postgres":
			u.updateConfigPath("postgres")
		case "skeleton":
			u.updateConfigPath("skeleton")
		case "ssh":
			u.updateConfigPath("ssh")
		case "varnish":
			u.updateConfigPath("varnish")
		case "cron":
			u.updateCron()
		case "caddy":
			u.updateCaddy()
		}
	}

	if u.configTmp != "" {
		os.RemoveAll(u.configTmp)
	}
	return 0
}

func (u *updater) printf(format string, a ...any) { fmt.Fprintf(u.out, format, a...) }
