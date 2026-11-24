// ##############################################################################
// Script Name: internal/commands/user/quota_project.go
// Description: Turn on project quotas so an account's limit counts every file in its home.
// Usage: opencli user-quota --enable-project
// Author: Stefan Pejcic
// Created: 24.11.2026
// Last Modified: 24.11.2026
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

package user

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"opencli/internal/conf"
	"opencli/internal/diskquota"
	"opencli/internal/sh"
)

// ext4 only turns on the quota and project features while unmounted, for / that's
// before it's mounted at boot, so the initramfs runs tune2fs once on the next boot
// (dracut on Ubuntu 25.10+ and EL, initramfs-tools on Debian and older Ubuntu)
const prjScript = `
# OpenPanel: turn on the ext4 quota and project features of the root filesystem before
# it's mounted, so an account's disk limit counts the files its containers write as subuids.
# Does nothing once they are on, never stops the boot. Installed by: opencli user-quota --enable-project
root=""
read -r cmdline < /proc/cmdline
for arg in $cmdline; do
    case "$arg" in root=*) root="${arg#root=}" ;; esac
done
case "$root" in
    UUID=*) dev="/dev/disk/by-uuid/${root#UUID=}" ;;
    LABEL=*) dev="/dev/disk/by-label/${root#LABEL=}" ;;
    PARTUUID=*) dev="/dev/disk/by-partuuid/${root#PARTUUID=}" ;;
    /dev/*) dev="$root" ;;
    *) exit 0 ;;
esac
[ -b "$dev" ] || exit 0
command -v tune2fs >/dev/null 2>&1 || exit 0
[ "$(blkid -s TYPE -o value "$dev" 2>/dev/null)" = "ext4" ] || exit 0
tune2fs -l "$dev" > /run/openpanel-quota.txt 2>/dev/null || exit 0
features=""
while IFS= read -r line; do
    case "$line" in "Filesystem features:"*) features=" ${line#Filesystem features:} " ;; esac
done < /run/openpanel-quota.txt
rm -f /run/openpanel-quota.txt
case "$features" in *" project "*) case "$features" in *" quota "*) exit 0 ;; esac ;; esac
echo "OpenPanel: enabling project quotas on $dev, every file is counted once, this can take a while" > /dev/kmsg 2>/dev/null
# tune2fs refuses a journal that needs recovery, -p replays it and fixes only what is safe
command -v e2fsck >/dev/null 2>&1 && e2fsck -p "$dev" >/dev/null 2>&1
if tune2fs -O project,quota -Q usrquota,grpquota,prjquota "$dev" >/dev/null 2>&1; then
    echo "OpenPanel: project quotas enabled on $dev" > /dev/kmsg 2>/dev/null
else
    echo "OpenPanel: could not enable project quotas on $dev, will try again on the next boot" > /dev/kmsg 2>/dev/null
fi
exit 0
`

type prjFile struct {
	path    string
	content string
}

var (
	prjDracutDir   = "/usr/lib/dracut/modules.d/99openpanel-quota"
	prjDracutFiles = []prjFile{
		{prjDracutDir + "/module-setup.sh", `#!/bin/bash
# OpenPanel: turns on ext4 project quotas on the root filesystem, see openpanel-quota.sh
check() { return 0; }
depends() { return 0; }
install() {
    inst_multiple tune2fs e2fsck blkid
    inst_script "$moddir/openpanel-quota.sh" /usr/sbin/openpanel-quota
    if dracut_module_included "systemd"; then
        # after systemd-fsck-root so the two never touch the disk at once, before / is mounted
        inst_simple "$moddir/openpanel-quota.service" "$systemdsystemunitdir/openpanel-quota.service"
        $SYSTEMCTL -q --root "$initdir" add-wants initrd-root-fs.target openpanel-quota.service
    else
        inst_hook pre-mount 90 "$moddir/openpanel-quota-hook.sh"
    fi
}
`},
		{prjDracutDir + "/openpanel-quota.sh", "#!/bin/sh" + prjScript},
		{prjDracutDir + "/openpanel-quota-hook.sh", "#!/bin/sh\n/usr/sbin/openpanel-quota\n"},
		{prjDracutDir + "/openpanel-quota.service", `[Unit]
Description=OpenPanel: enable project quotas on the root filesystem
DefaultDependencies=no
ConditionPathExists=/etc/initrd-release
After=initrd-root-device.target systemd-fsck-root.service
Before=sysroot.mount

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/openpanel-quota
StandardOutput=journal+console
TimeoutSec=0
`},
		{"/etc/dracut.conf.d/90-openpanel-quota.conf", "# OpenPanel: ext4 project quotas, see " + prjDracutDir + "\nadd_dracutmodules+=\" openpanel-quota \"\n"},
	}
	// on the host: project quotas are only counted until quotaon -P turns enforcement on, every boot,
	// and quotaon-root.service (quotaon -ug /) fails with "File exists" once the ext4 quota feature
	// has user and group quotas on by itself
	prjHostFiles = []prjFile{
		{"/etc/systemd/system/openpanel-quota-enforce.service", `[Unit]
Description=OpenPanel: enforce project quotas on /
After=local-fs.target quotaon-root.service
ConditionPathExists=!/etc/initrd-release

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=-/usr/sbin/quotaon -P /

[Install]
WantedBy=multi-user.target
`},
		{"/etc/systemd/system/quotaon-root.service.d/openpanel.conf", `# OpenPanel: with the ext4 quota feature user and group quotas are on at mount, quotaon then fails with "File exists"
[Service]
ExecStart=
ExecStart=-/usr/sbin/quotaon -ug /
`},
	}
	prjInitramfsFiles = []prjFile{
		{"/etc/initramfs-tools/hooks/openpanel-quota", `#!/bin/sh
# OpenPanel: tune2fs, e2fsck and blkid in the initramfs, for scripts/local-premount/openpanel-quota
PREREQ=""
prereqs() { echo "$PREREQ"; }
case "$1" in prereqs) prereqs; exit 0 ;; esac
. /usr/share/initramfs-tools/hook-functions
for bin in tune2fs e2fsck blkid; do
    p=$(command -v "$bin") && copy_exec "$p" /sbin
done
exit 0
`},
		{"/etc/initramfs-tools/scripts/local-premount/openpanel-quota", `#!/bin/sh
PREREQ=""
prereqs() { echo "$PREREQ"; }
case "$1" in prereqs) prereqs; exit 0 ;; esac
` + prjScript},
	}
)

func prjWrite(files []prjFile) error {
	for _, f := range files {
		os.MkdirAll(filepath.Dir(f.path), 0o755)
		mode := os.FileMode(0o755)
		if !strings.HasPrefix(f.content, "#!") {
			mode = 0o644
		}
		if err := os.WriteFile(f.path, []byte(f.content), mode); err != nil {
			return err
		}
	}
	return nil
}

func quotaEnableProject() int {
	if diskquota.ProjectOn() {
		if !prjHostUnits() {
			return 1
		}
		if err := diskquota.Enforce(); err != nil {
			quotaLogErr(err.Error())
			return 1
		}
		if len(prjAccounts()) == 0 {
			// the installer runs this again after the reboot, before any account exists
			quotaLog("Project quotas are on for " + diskquota.Mount + ", no accounts yet.")
			return 0
		}
		quotaLog("Project quotas are already on for " + diskquota.Mount + ", applying them to all users...")
		return sh.Opencli("user-quota", "--update", "--all").Code()
	}
	src, _ := sh.C("findmnt", "-no", "SOURCE", diskquota.Mount).NoErr().Out()
	fstype, _ := sh.C("findmnt", "-no", "FSTYPE", diskquota.Mount).NoErr().Out()
	src, fstype = strings.TrimSpace(src), strings.TrimSpace(fstype)
	switch fstype {
	case "ext4":
	case "xfs":
		return prjEnableXFS()
	default:
		quotaLogErr(fmt.Sprintf("Project quotas are not supported on %s (%s), only ext4 and xfs.", diskquota.Mount, fstype))
		return 1
	}
	info, _ := sh.C("tune2fs", "-l", src).NoErr().Out()
	for _, l := range strings.Split(info, "\n") {
		f := strings.Fields(l)
		if strings.HasPrefix(l, "Filesystem features:") {
			feat := " " + strings.Join(f[2:], " ") + " "
			if strings.Contains(feat, " project ") && strings.Contains(feat, " quota ") {
				quotaLogErr(src + " has the project and quota features, but project quota is off. Check 'quotaon -pP /' and the mount options.")
				return 1
			}
		}
		if len(f) == 3 && f[0] == "Inode" && f[1] == "size:" && f[2] == "128" {
			quotaLogErr(src + " has 128 byte inodes, project ids need 256.")
			return 1
		}
	}
	manual := "From a rescue system run: tune2fs -O project,quota -Q usrquota,grpquota,prjquota " + src
	var files []prjFile
	var regen []string
	switch {
	case sh.Has("dracut") && conf.IsDir("/usr/lib/dracut/modules.d"):
		files = prjDracutFiles
		regen = []string{"dracut", "-f", "--regenerate-all"}
	case conf.IsDir("/etc/initramfs-tools") && sh.Has("update-initramfs"):
		files = prjInitramfsFiles
		regen = []string{"update-initramfs", "-u", "-k", "all"}
	default:
		quotaLogErr("The quota and project features can only be turned on while " + src + " is unmounted, and neither dracut nor initramfs-tools is here to do it on boot. " + manual)
		return 1
	}
	if err := prjWrite(files); err != nil {
		quotaLogErr("Writing the initramfs files: " + err.Error())
		return 1
	}
	if !prjHostUnits() {
		return 1
	}
	quotaLog("Rebuilding the initramfs (" + strings.Join(regen, " ") + ")...")
	if out, err := sh.C(regen[0], regen[1:]...).Combined(); err != nil {
		quotaLogErr(regen[0] + " failed: " + strings.TrimSpace(out))
		return 1
	}
	quotaLog("Project quotas will be turned on for " + src + " on the next reboot (the first boot takes longer, every file is counted once).")
	quotaLog("Until then limits stay user quotas. After the reboot accounts are tagged and limited on their own, or run: opencli user-quota --update --all")
	return 0
}

// XFS only turns quotas on when / is mounted, and the initramfs mounts it with the kernel's rootflags=,
// a remount can't add them (they are silently dropped) and the later remount to rw keeps them.
// uquota and gquota too: the usrquota,grpquota in fstab never took effect on an XFS /
const prjXFSFlags = "uquota,gquota,pquota"

const prjGrubDefault = "/etc/default/grub"

// rootflags with the quota options added, keeps whatever else was there (from the running kernel and grub's default)
func prjMergeRootflags(current ...string) string {
	var out []string
	seen := map[string]bool{}
	add := func(o string) {
		o = strings.TrimSpace(o)
		// usrquota/uquota, grpquota/gquota, prjquota/pquota, noquota are replaced by the set below
		switch o {
		case "", "noquota", "usrquota", "uquota", "uqnoenforce", "grpquota", "gquota", "gqnoenforce", "prjquota", "pquota", "pqnoenforce", "quota", "qnoenforce":
			return
		}
		if !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	for _, c := range current {
		for _, o := range strings.Split(c, ",") {
			add(o)
		}
	}
	return strings.Join(append(out, strings.Split(prjXFSFlags, ",")...), ",")
}

// value of rootflags= in a kernel command line
func prjRootflags(cmdline string) string {
	for _, f := range strings.Fields(cmdline) {
		if v, ok := strings.CutPrefix(strings.Trim(f, `"`), "rootflags="); ok {
			return v
		}
	}
	return ""
}

// GRUB_CMDLINE_LINUX with its rootflags= swapped for rootflags=<flags>, added when the line is missing
func prjSetGrubCmdline(lines []string, flags string) []string {
	found := false
	for i, l := range lines {
		if !strings.HasPrefix(l, "GRUB_CMDLINE_LINUX=") {
			continue
		}
		found = true
		v := strings.TrimPrefix(l, "GRUB_CMDLINE_LINUX=")
		q := `"`
		if strings.HasPrefix(v, "'") {
			q = "'"
		}
		var keep []string
		for _, f := range strings.Fields(strings.Trim(v, `"'`)) {
			if !strings.HasPrefix(f, "rootflags=") {
				keep = append(keep, f)
			}
		}
		lines[i] = "GRUB_CMDLINE_LINUX=" + q + strings.Join(append(keep, "rootflags="+flags), " ") + q
	}
	if !found {
		lines = append(lines, `GRUB_CMDLINE_LINUX="rootflags=`+flags+`"`)
	}
	return lines
}

func prjEnableXFS() int {
	running := prjRootflags(conf.Read("/proc/cmdline"))
	grubLine := ""
	for _, l := range conf.Lines(prjGrubDefault) {
		if strings.HasPrefix(l, "GRUB_CMDLINE_LINUX=") {
			grubLine = l
		}
	}
	flags := prjMergeRootflags(running, prjRootflags(strings.Trim(strings.TrimPrefix(grubLine, "GRUB_CMDLINE_LINUX="), `"'`)))

	// grub's default, for kernels installed later (and the only place on Debian/Ubuntu)
	if conf.IsFile(prjGrubDefault) {
		ok := conf.SedFile(prjGrubDefault, func(lines []string) []string { return prjSetGrubCmdline(lines, flags) })
		if !ok {
			quotaLogErr("Could not update " + prjGrubDefault)
			return 1
		}
	}

	switch {
	case sh.Has("grubby"):
		// EL keeps every kernel's command line in its own boot entry
		sh.C("grubby", "--update-kernel=ALL", "--remove-args=rootflags").Quiet().Run()
		if out, err := sh.C("grubby", "--update-kernel=ALL", "--args=rootflags="+flags).Combined(); err != nil {
			quotaLogErr("grubby failed: " + strings.TrimSpace(out))
			return 1
		}
	case sh.Has("update-grub"):
		if out, err := sh.C("update-grub").Combined(); err != nil {
			quotaLogErr("update-grub failed: " + strings.TrimSpace(out))
			return 1
		}
	case sh.Has("grub2-mkconfig") && conf.IsDir("/boot/grub2"):
		if out, err := sh.C("grub2-mkconfig", "-o", "/boot/grub2/grub.cfg").Combined(); err != nil {
			quotaLogErr("grub2-mkconfig failed: " + strings.TrimSpace(out))
			return 1
		}
	default:
		quotaLogErr("No grubby, update-grub or grub2-mkconfig here. Add 'rootflags=" + flags + "' to the kernel command line and reboot.")
		return 1
	}
	if !prjHostUnits() {
		return 1
	}
	quotaLog("Added rootflags=" + flags + " to the kernel command line, project quotas (and user quotas, XFS ignores them in fstab) will be on after the next reboot.")
	quotaLog("Until then limits stay as they are. After the reboot accounts are tagged and limited on their own, or run: opencli user-quota --update --all")
	return 0
}

func prjHostUnits() bool {
	if err := prjWrite(prjHostFiles); err != nil {
		quotaLogErr("Writing the systemd units: " + err.Error())
		return false
	}
	// not named like the initrd unit: systemd keeps that one's state after switch-root, a same-named one would never run
	sh.C("systemctl", "daemon-reload").Quiet().Run()
	sh.C("systemctl", "enable", "openpanel-quota-enforce.service").Quiet().Run()
	return true
}

// accounts whose home isn't tagged with their project yet, after the features got turned on
// or a home that was restored or recreated, their limits get set again in the background
func quotaHealProjects() {
	if !diskquota.ProjectOn() {
		return
	}
	// enforcement is off again after every boot until openpanel-quota-enforce.service runs
	if err := diskquota.Enforce(); err != nil {
		quotaLogErr(err.Error())
	}
	if !conf.IsFile("/etc/my.cnf") {
		return
	}
	for _, u := range prjAccounts() {
		id, err := diskquota.UID(u)
		if err != nil || diskquota.Tagged(diskquota.Home(u), id) {
			continue
		}
		quotaHealUser(u)
	}
}

// linux users with a subuid range and a home, the OpenPanel accounts
func prjAccounts() []string {
	subuid := map[string]bool{}
	for _, l := range conf.Lines("/etc/subuid") {
		if i := strings.IndexByte(l, ':'); i > 0 {
			subuid[l[:i]] = true
		}
	}
	var out []string
	for _, l := range conf.Lines("/etc/passwd") {
		f := strings.Split(l, ":")
		if len(f) >= 3 && subuid[f[0]] && conf.IsDir(diskquota.Home(f[0])) {
			out = append(out, f[0])
		}
	}
	return out
}

// one at a time per account, a big home takes a while to tag and the report runs often
func quotaHealUser(username string) {
	lock, err := os.OpenFile("/run/openpanel-quota-"+username+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return
	}
	sh.Opencli("user-quota", "--update", username).Quiet().Run()
}
