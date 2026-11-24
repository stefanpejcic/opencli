package user

import (
	"strings"
	"testing"
)

func TestPrjMergeRootflags(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, "uquota,gquota,pquota"},
		{[]string{"", ""}, "uquota,gquota,pquota"},
		{[]string{"usrquota,grpquota"}, "uquota,gquota,pquota"},
		{[]string{"noquota"}, "uquota,gquota,pquota"},
		{[]string{"inode64,usrquota", "inode64,logbsize=256k"}, "inode64,logbsize=256k,uquota,gquota,pquota"},
		{[]string{"uquota,gquota,pquota"}, "uquota,gquota,pquota"},
	}
	for _, c := range cases {
		if got := prjMergeRootflags(c.in...); got != c.want {
			t.Errorf("prjMergeRootflags(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPrjRootflags(t *testing.T) {
	cases := map[string]string{
		"BOOT_IMAGE=/vmlinuz root=UUID=x ro quiet":                           "",
		"BOOT_IMAGE=/vmlinuz root=/dev/sda1 ro rootflags=uquota,pquota rhgb": "uquota,pquota",
		`"crashkernel=auto rootflags=inode64"`:                               "inode64",
	}
	for in, want := range cases {
		if got := prjRootflags(in); got != want {
			t.Errorf("prjRootflags(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrjSetGrubCmdline(t *testing.T) {
	f := "uquota,gquota,pquota"
	cases := []struct{ in, want string }{
		{`GRUB_TIMEOUT=5
GRUB_CMDLINE_LINUX="crashkernel=1G-4G:192M resume=/dev/mapper/rl-swap rd.lvm.lv=rl/root rhgb quiet"`,
			`GRUB_TIMEOUT=5
GRUB_CMDLINE_LINUX="crashkernel=1G-4G:192M resume=/dev/mapper/rl-swap rd.lvm.lv=rl/root rhgb quiet rootflags=uquota,gquota,pquota"`},
		{`GRUB_CMDLINE_LINUX="quiet rootflags=inode64"`, `GRUB_CMDLINE_LINUX="quiet rootflags=uquota,gquota,pquota"`},
		{`GRUB_CMDLINE_LINUX=""`, `GRUB_CMDLINE_LINUX="rootflags=uquota,gquota,pquota"`},
		{`GRUB_CMDLINE_LINUX='net.ifnames=0'`, `GRUB_CMDLINE_LINUX='net.ifnames=0 rootflags=uquota,gquota,pquota'`},
		{`GRUB_TIMEOUT=5`, "GRUB_TIMEOUT=5\nGRUB_CMDLINE_LINUX=\"rootflags=uquota,gquota,pquota\""},
	}
	for _, c := range cases {
		got := strings.Join(prjSetGrubCmdline(strings.Split(c.in, "\n"), f), "\n")
		if got != c.want {
			t.Errorf("prjSetGrubCmdline(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
	}
}
