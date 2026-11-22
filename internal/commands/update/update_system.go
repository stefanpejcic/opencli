// ##############################################################################
// Script Name: internal/commands/update/update_system.go
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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"opencli/internal/conf"
	"opencli/internal/sh"
)

func bgCtx() context.Context { return context.Background() }

func ctxTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func deadline(ctx context.Context) time.Time { t, _ := ctx.Deadline(); return t }

// ---------------------- HELPERS USED IN MAJOR UPDATES ONLY ---------------------- #
// the bash version ran this in $(), so an unsupported distro only printed the error and went on with ""
func (u *updater) detectSystem() string {
	switch {
	case sh.Has("apt"):
		return "debian"
	case sh.Has("dnf"):
		return "dnf"
	case sh.Has("yum"):
		return "yum"
	}
	fmt.Fprintf(os.Stderr, "%s[ERROR]%s [✘] Unsupported Linux distribution\n", red, nc)
	return ""
}

// without these a conffile or debconf prompt hangs an update that runs from openadmin or cron
func (u *updater) apt(args ...string) *sh.Cmd {
	a := append([]string{"-y", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}, args...)
	return u.toLog(sh.C("apt-get", a...).Env("DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l"))
}

// toLog points stdout and stderr at the log file like >> "$log_file" 2>&1
func (u *updater) toLog(c *sh.Cmd) *sh.Cmd {
	if f := u.logWriter(); f != nil {
		return c.Stdout(f).Stderr(f)
	}
	return c.Quiet()
}

// ---------------------- RUNS ONLY ON MAJOR ---------------------- #
func (u *updater) installRequiredTools() {
	distro := u.detectSystem()
	u.info("Installing required system tools")
	switch distro {
	case "debian":
		// newer ubuntu releases dropped deborphan
		if !sh.OK("dpkg", "-s", "deborphan") && regexp.MustCompile(`Candidate: [^(]`).MatchString(sh.Str("apt-cache", "policy", "deborphan")) {
			u.apt("install", "deborphan").Run()
		}
	case "dnf", "yum":
		if !sh.Has("needs-restarting") || (distro == "yum" && !sh.Has("package-cleanup")) {
			u.installPackage("yum-utils", true)
		}
	}
}

func sortUniqV(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range list {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return versionBefore(out[i], out[j]) })
	return out
}

// installedDpkg is dpkg-query -W -f='${Package} ${Status}\n' <patterns> | awk '$NF == "installed" {print $1}'
func installedDpkg(patterns ...string) []string {
	out := sh.C("dpkg-query", append([]string{"-W", "-f=${Package} ${Status}\n"}, patterns...)...).NoErr().Str()
	var pk []string
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) > 0 && f[len(f)-1] == "installed" {
			pk = append(pk, f[0])
		}
	}
	return pk
}

// versions of the installed versioned kernel images, oldest first, meta packages like linux-generic are left out
func installedKernelVersionsDeb() []string {
	re := regexp.MustCompile(`^linux-image-(unsigned-)?`)
	var v []string
	for _, p := range installedDpkg("linux-image-[0-9]*", "linux-image-unsigned-[0-9]*") {
		v = append(v, re.ReplaceAllString(p, ""))
	}
	return sortUniqV(v)
}

func installedKernelVersionsRpm() []string {
	var v []string
	for _, l := range strings.Split(sh.C("rpm", "-q", "--qf", "%{VERSION}-%{RELEASE}.%{ARCH}\n", "kernel-core", "kernel").NoErr().Str(), "\n") {
		if l != "" && !strings.Contains(l, "not installed") {
			v = append(v, l)
		}
	}
	return sortUniqV(v)
}

func unameR() string { return sh.C("uname", "-r").NoErr().Str() }

// ---------------------- RUNS ONLY ON MAJOR ---------------------- #
func (u *updater) removeOldKernels() {
	distro := u.detectSystem()
	current := unameR()
	switch distro {
	case "debian":
		u.info(fmt.Sprintf("Removing old kernels (Debian/Ubuntu), keeping the running one (%s) and the %d newest others", current, keepKernels))
		var kernels, remove []string
		for _, k := range installedKernelVersionsDeb() {
			if k != current {
				kernels = append(kernels, k)
			}
		}
		if len(kernels) <= keepKernels {
			u.info("[✔] No old kernels to remove")
			return
		}
		for _, ver := range kernels[:len(kernels)-keepKernels] {
			// image, modules and headers of that version, otherwise modules and headers stay behind forever
			remove = append(remove, installedDpkg("linux-*-"+ver)...)
		}
		if len(remove) == 0 {
			return
		}
		u.info("Purging: " + strings.Join(remove, " "))
		if u.apt(append([]string{"purge"}, remove...)...).Run() != nil {
			u.warn("Failed to purge old kernels, see " + u.logFile)
		}
		// ubuntu splits headers into a flavor package and a shared one, the shared one is only autoremovable now
		u.apt("autoremove", "--purge").Run()
	case "dnf":
		u.info(fmt.Sprintf("Removing old kernels (RHEL), keeping the running one (%s) and the %d newest others", current, keepKernels))
		// never removes the running kernel, exits 1 when there's nothing to remove
		if u.toLog(sh.C("dnf", "remove", "-y", "--oldinstallonly", "--setopt", fmt.Sprintf("installonly_limit=%d", keepKernels+1))).Run() != nil {
			u.info("[✔] No old kernels to remove")
		}
	case "yum":
		u.info("Removing old kernels (CentOS 7)")
		if sh.Has("package-cleanup") {
			u.toLog(sh.C("package-cleanup", "--oldkernels", fmt.Sprintf("--count=%d", keepKernels+1), "-y")).Run()
		}
	}
}

// ---------------------- RUNS ONLY ON MAJOR ---------------------- #
func (u *updater) updateSystemPackages() {
	distro := u.detectSystem()
	u.info("Updating system packages")
	switch distro {
	case "debian":
		u.apt("update").Run()
		u.apt("upgrade").Run()
		u.apt("full-upgrade").Run()

		u.info("Cleaning up packages")
		u.apt("autoremove").Run()
		u.apt("autoclean").Run()
		u.toLog(sh.C("apt-get", "clean")).Run()

		// deborphan only lists orphaned libraries by default
		if sh.Has("deborphan") {
			if l := strings.Fields(sh.C("deborphan").Str()); len(l) > 0 {
				u.apt(append([]string{"purge"}, l...)...).Run()
			}
		}
		var rc []string
		for _, l := range strings.Split(sh.C("dpkg", "-l").Str(), "\n") {
			if strings.HasPrefix(l, "rc") {
				if f := strings.Fields(l); len(f) > 1 {
					rc = append(rc, f[1])
				}
			}
		}
		if len(rc) > 0 {
			u.apt(append([]string{"purge"}, rc...)...).Run()
		}
	case "dnf", "yum":
		u.toLog(sh.C(distro, "-y", "update")).Run()
		// only autoremove, package-cleanup --leaves --all also lists podman, sshd and anything else nothing depends on
		u.info("Cleaning up packages")
		u.toLog(sh.C(distro, "-y", "autoremove")).Run()
		u.toLog(sh.C(distro, "clean", "all")).Run()
	}
}

// ---------------------- RUNS ONLY ON MAJOR ---------------------- #
func (u *updater) checkRebootRequired() {
	u.info("Checking if reboot is required")
	distro := u.detectSystem()
	running := unameR()
	newest, reason, pkgs := "", "", ""
	last := func(l []string) string {
		if len(l) == 0 {
			return ""
		}
		return l[len(l)-1]
	}
	switch distro {
	case "debian":
		newest = last(installedKernelVersionsDeb())
		if isFile("/var/run/reboot-required") {
			reason = "packages updated"
			if isFile("/var/run/reboot-required.pkgs") {
				l := conf.Lines("/var/run/reboot-required.pkgs")
				sort.Strings(l)
				var uniq []string
				for i, p := range l {
					if i == 0 || p != l[i-1] {
						uniq = append(uniq, p)
					}
				}
				pkgs = strings.Join(uniq, ", ")
			}
		}
	case "dnf", "yum":
		newest = last(installedKernelVersionsRpm())
		if sh.Has("needs-restarting") && !sh.OK("needs-restarting", "-r") {
			reason = "core libraries or kernel updated"
			u.toLog(sh.C("needs-restarting", "-r")).Run()
		}
	}

	// debian only writes /var/run/reboot-required when update-notifier or unattended-upgrades is installed
	if newest != "" && newest != running {
		reason = "new kernel " + newest + " installed, running " + running
	}
	if reason == "" {
		u.info("[✔] No reboot required")
		return
	}

	u.warn("[!] Reboot required: " + reason)
	if pkgs != "" {
		u.appendLog("Packages that need a reboot: " + pkgs + "\n")
	}
	u.appendLog("Reboot required: " + reason + "\n")

	// goes to OpenAdmin > Notifications and email/webhook, the admin can turn it off with the 'update' notification toggle
	hn, _ := os.Hostname()
	extra := ""
	if pkgs != "" {
		extra = " (packages: " + pkgs + ")"
	}
	u.toLog(sh.Opencli("sentinel", "--action=update", "--severity=warning", "--title=Reboot required",
		"--message=Server "+hn+" needs a reboot to finish the system update: "+reason+extra+". Reboot from OpenAdmin > Server reboot or with the reboot command when it suits you.").Timeout(60 * time.Second)).Run()
}

func stdinIsTerminal() bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

// readTimeout is read -t N -p prompt, ok is false on timeout or EOF
func readTimeout(prompt string, d time.Duration) (string, bool) {
	if stdinIsTerminal() {
		fmt.Fprint(os.Stderr, prompt)
	}
	type res struct {
		s  string
		ok bool
	}
	ch := make(chan res, 1)
	go func() {
		s, err := bufio.NewReader(os.Stdin).ReadString('\n')
		ch <- res{strings.TrimSuffix(s, "\n"), err == nil}
	}()
	select {
	case r := <-ch:
		return r.s, r.ok
	case <-time.After(d):
		return "", false
	}
}

func (u *updater) updateSystem(yes string) bool {
	if u.logFile == "" {
		u.logFile = "/var/log/openpanel/updates/system_" + time.Now().Format("20060102_150405") + ".log"
	}
	os.MkdirAll(filepath.Dir(u.logFile), 0o755)
	u.info("Updating system packages")

	if yes != "-y" {
		confirm, ok := readTimeout("System package update ready to proceed. A full server backup is recommended beforehand. Continue? [y/N] ", 10*time.Second)
		if !ok {
			fmt.Println()
			u.info("No response, aborting update")
			return false
		}
		if !regexp.MustCompile(`^[yY]([eE][sS])?$`).MatchString(confirm) {
			u.info("System upgrade cancelled")
			return false
		}
	}

	u.installRequiredTools()
	u.updateSystemPackages()
	u.removeOldKernels()
	u.checkRebootRequired()
	return true
}
