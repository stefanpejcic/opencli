// ##############################################################################
// Script Name: internal/commands/docker/hosts.go
// Description: Write the hosts file that users' new containers start with, so the panel and mail hostnames point at the server.
// Usage: opencli docker-hosts [--show | --apply [USERNAME] [--restart]]
// Author: Stefan Pejcic
// Created: 12.11.2026
// Last Modified: 25.11.2026
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
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "docker-hosts",
		Desc:  "Write the hosts file that users' new containers start with, so the panel and mail hostnames point at the server.",
		Usage: "opencli docker-hosts [--show | --apply [USERNAME] [--restart]]",
		Run:   hosts,
	})
}

// podman builds a new container's /etc/hosts from this file when the user's containers.conf has base_hosts_file set (user-add does that)
const (
	hostsFile     = "/etc/openpanel/docker/container_hosts"
	hostsCustom   = "/etc/openpanel/docker/container_hosts.custom"
	hostsCaddy    = "/etc/openpanel/caddy/Caddyfile"
	hostsMailEnv  = "/usr/local/mail/openmail/mailserver.env"
	hostsHostAddr = "169.254.1.2" // pasta --map-guest-addr, reaches the host only when the rootless netns has a copy of the host ip
	hostsNetConf  = "/etc/containers/containers.conf.d/99-openpanel-net.conf"
)

// hostsProbePorts are tried from inside a user container's netns, caddy first, then mail over tls
var hostsProbePorts = []string{"443", "465"}

var (
	hostnameRe   = regexp.MustCompile(`^([A-Za-z0-9-]+\.)+[A-Za-z]{2,}$`)
	caddyBlockRe = regexp.MustCompile(`^([A-Za-z0-9.-]*)[ \t\n\v\f\r]*\{`)
)

func hostsUsage() {
	fmt.Println("Usage: opencli docker-hosts [--show | --apply [USERNAME] [--restart]]")
	fmt.Println("")
	fmt.Println("  (no args)           Write " + hostsFile + " from the panel domain and the mail hostname")
	fmt.Println("  --show              Print the file without writing it")
	fmt.Println("  --apply [USERNAME]  Write the file, set it for all existing users (or one) and update /etc/hosts inside their running containers, no restart")
	fmt.Println("  --restart           With --apply, stop and start the php-fpm and webserver containers instead of editing them in place")
}

func hostsPanelDomain() string {
	if !conf.IsFile(hostsCaddy) {
		return ""
	}
	lines := conf.Lines(hostsCaddy)
	for _, block := range []string{"USERPANEL DOMAIN", "HOSTNAME DOMAIN"} {
		start := "# START " + block + " #"
		end := "# END " + block + " #"
		// sed -n '/start/,/end/p' then the first line that looks like "domain {"
		in := false
		first := ""
		for _, l := range lines {
			if !in {
				if !strings.Contains(l, start) {
					continue
				}
				in = true
			} else if strings.Contains(l, end) {
				in = false
			}
			if m := caddyBlockRe.FindStringSubmatch(l); m != nil {
				first = m[1]
				break
			}
		}
		if first != "" {
			return first
		}
	}
	return ""
}

func hostsMailHostname() string {
	if !conf.IsFile(hostsMailEnv) {
		return ""
	}
	v, _ := conf.Raw(hostsMailEnv, "OVERRIDE_HOSTNAME")
	return strings.NewReplacer(`"`, "", `'`, "", " ", "").Replace(v)
}

// hostsPublicIP is the source address the host uses to reach the internet, no packet is sent
func hostsPublicIP() string {
	c, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	ip := c.LocalAddr().(*net.UDPAddr).IP
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return ""
	}
	return ip.String()
}

// hostsSampleNetns gives the network namespace of a few running user containers, that's where php connects from.
// /proc/PID/ns/net works for podman 5 (pasta) and podman 4 (slirp4netns), whose netns files only exist in the user's mount namespace.
func hostsSampleNetns(max int) []string {
	dirs, _ := filepath.Glob("/home/*")
	sort.Strings(dirs)
	var out []string
	for _, d := range dirs {
		u := filepath.Base(d)
		if !conf.IsFile("/home/" + u + "/.env") {
			continue
		}
		pids := podman.User(u, "ps", "--format", "{{.Pid}}").NoErr().Str()
		for _, pid := range strings.Fields(pids) {
			if _, err := os.Stat("/proc/" + pid + "/ns/net"); pid != "0" && err == nil {
				out = append(out, "/proc/"+pid+"/ns/net")
				break
			}
		}
		if len(out) >= max {
			break
		}
	}
	return out
}

// hostsReachable tries addr on the probe ports from inside the netns, true on the first open port
func hostsReachable(netns, addr string) bool {
	for _, port := range hostsProbePorts {
		if sh.C("nsenter", "--net="+netns, "timeout", "3", "bash", "-c", "</dev/tcp/"+addr+"/"+port).Quiet().OK() {
			return true
		}
	}
	return false
}

// hostsPinnedNetns is true when pasta_options give the rootless netns its own address (-a), then --map-guest-addr points at that private address and not at the host
func hostsPinnedNetns() bool {
	if !conf.IsFile(hostsNetConf) {
		return false
	}
	for _, l := range conf.Lines(hostsNetConf) {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "pasta_options") && regexp.MustCompile(`"(-a|--address)"`).MatchString(l) {
			return true
		}
	}
	return false
}

// hostsAddr picks the address the hostnames point at: 169.254.1.2 when it works from the users' netns, else the public ip.
// With no running netns to test from it goes by the pasta config.
func hostsAddr() string {
	pub := hostsPublicIP()
	candidates := []string{hostsHostAddr}
	if pub != "" {
		candidates = append(candidates, pub)
	}
	for _, ns := range hostsSampleNetns(3) {
		for _, a := range candidates {
			if hostsReachable(ns, a) {
				return a
			}
		}
	}
	if pub != "" && hostsPinnedNetns() {
		return pub
	}
	return hostsHostAddr
}

// hostsNames are the panel and mail hostnames that point at the server
func hostsNames() []string {
	var names []string
	for _, n := range []string{hostsPanelDomain(), hostsMailHostname()} {
		if !hostnameRe.MatchString(n) {
			continue
		}
		dup := false
		for _, x := range names {
			if x == n {
				dup = true
			}
		}
		if !dup {
			names = append(names, n)
		}
	}
	return names
}

func hostsBuild(addr string) string {
	names := hostsNames()
	var b strings.Builder
	b.WriteString("# written by opencli docker-hosts, put your own lines in " + hostsCustom + "\n")
	b.WriteString("127.0.0.1\tlocalhost\n")
	b.WriteString("::1\tlocalhost ip6-localhost ip6-loopback\n")
	if len(names) > 0 {
		b.WriteString(addr + "\t" + strings.Join(names, " ") + "\n")
	}
	if conf.IsFile(hostsCustom) {
		b.WriteString("\n")
		b.WriteString(conf.Read(hostsCustom))
	}
	return b.String()
}

// hostsSetUserBase sets base_hosts_file in the user's containers.conf, false when the user has no podman config
func hostsSetUserBase(user string) bool {
	cf := "/home/" + user + "/.config/containers/containers.conf"
	if !conf.IsFile(cf) {
		return false
	}
	already := regexp.MustCompile(`^base_hosts_file *= *"` + hostsFile + `"`)
	for _, l := range conf.Lines(cf) {
		if already.MatchString(l) {
			return true
		}
	}
	keyRe := regexp.MustCompile(`^base_hosts_file *=`)
	conf.SedFile(cf, func(lines []string) []string {
		var out []string
		for _, l := range lines {
			if !keyRe.MatchString(l) {
				out = append(out, l)
			}
		}
		return out
	})
	hasSection := false
	for _, l := range conf.Lines(cf) {
		if strings.HasPrefix(l, "[containers]") {
			hasSection = true
			break
		}
	}
	if hasSection {
		conf.SedFile(cf, func(lines []string) []string {
			var out []string
			for _, l := range lines {
				out = append(out, l)
				if strings.HasPrefix(l, "[containers]") {
					out = append(out, `base_hosts_file = "`+hostsFile+`"`)
				}
			}
			return out
		})
	} else if f, err := os.OpenFile(cf, os.O_WRONLY|os.O_APPEND, 0); err == nil {
		fmt.Fprintf(f, "\n[containers]\nbase_hosts_file = \"%s\"\n", hostsFile)
		f.Close()
	}
	return true
}

// podman builds /etc/hosts on every start, so a stop and start is enough, php first and the webserver last
func hostsRestart(user string) {
	running := podman.User(user, "ps", "--format", "{{.Names}}").NoErr().Str()
	v, _ := conf.Raw("/home/"+user+"/.env", "WEB_SERVER")
	ws := strings.NewReplacer(`"`, "", `'`, "", " ", "").Replace(v)
	var names []string
	for _, l := range strings.Split(running, "\n") {
		if strings.HasPrefix(l, "php-fpm-") {
			names = append(names, strings.Fields(l)...)
		}
	}
	for _, l := range strings.Split(running, "\n") {
		if l == ws {
			names = append(names, strings.Fields(l)...)
		}
	}
	for _, c := range names {
		if podman.User(user, "stop", c).Quiet().OK() && podman.User(user, "start", c).Quiet().OK() {
			fmt.Println("  restarted " + c)
		} else {
			fmt.Printf("  failed to restart %s, check it with: opencli docker-logs %s\n", c, user)
		}
	}
	podman.SyncVarnish(user)
}

// hostsLiveContent drops every line that names one of the hostnames (like cloud-init's 127.0.1.1 one) and adds the addr line
func hostsLiveContent(cur, addr string, names []string) string {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(cur, "\n"), "\n") {
		f := strings.Fields(l)
		drop := false
		if len(f) > 1 && !strings.HasPrefix(f[0], "#") {
			for _, n := range f[1:] {
				if want[n] {
					drop = true
				}
			}
		}
		if !drop {
			out = append(out, l)
		}
	}
	out = append(out, addr+"\t"+strings.Join(names, " "))
	return strings.Join(out, "\n") + "\n"
}

// hostsLive rewrites /etc/hosts inside the user's running containers, podman bind mounts it read-write so no restart is needed.
// It's written with cat > since sed -i would replace the bind mounted file. The next start builds it from base_hosts_file anyway.
func hostsLive(user, addr string, names []string) {
	if len(names) == 0 {
		return
	}
	running := podman.User(user, "ps", "--format", "{{.Names}}").NoErr().Str()
	v, _ := conf.Raw("/home/"+user+"/.env", "WEB_SERVER")
	ws := strings.NewReplacer(`"`, "", `'`, "", " ", "").Replace(v)
	for _, c := range strings.Fields(running) {
		// php sends the mail, the rest only matters for apps that connect out, so only those report failures
		important := strings.HasPrefix(c, "php-fpm-") || c == ws || c == "cron"
		cur, err := podman.User(user, "exec", c, "cat", "/etc/hosts").NoErr().Raw()
		if err != nil {
			if important {
				fmt.Printf("  failed to read /etc/hosts in %s\n", c)
			}
			continue
		}
		next := hostsLiveContent(cur, addr, names)
		if next == cur {
			fmt.Println("  " + c + " already up to date")
			continue
		}
		if podman.User(user, "exec", "-i", "-u", "root", c, "sh", "-c", "cat > /etc/hosts").Stdin(next).Quiet().OK() {
			fmt.Println("  updated " + c)
		} else if important {
			fmt.Printf("  failed to update /etc/hosts in %s, run: opencli docker-hosts --apply %s --restart\n", c, user)
		}
	}
}

func hostsApply(only, addr string, restart bool) {
	var users []string
	if only != "" {
		if !conf.IsFile("/home/" + only + "/.env") {
			fmt.Printf("User %s not found.\n", only)
			cli.Exit(1)
		}
		users = []string{only}
	} else {
		dirs, _ := filepath.Glob("/home/*")
		sort.Strings(dirs)
		for _, d := range dirs {
			u := filepath.Base(d)
			if conf.IsFile("/home/"+u+"/.env") && conf.IsFile("/home/"+u+"/.config/containers/containers.conf") {
				users = append(users, u)
			}
		}
	}
	for _, u := range users {
		fmt.Println(u + ":")
		if !hostsSetUserBase(u) {
			fmt.Println("  no containers.conf, skipped")
			continue
		}
		// the podman service may still have the old config loaded, it only restarts if it's running
		sh.C("systemctl", "--user", "-M", u+"@", "try-restart", "podman.service").Quiet().Run()
		if restart {
			hostsRestart(u)
		} else {
			hostsLive(u, addr, hostsNames())
		}
	}
}

func hosts(args []string) int {
	apply := false
	applyUser := ""
	restart := false
	var rest []string
	for _, a := range args {
		if a == "--restart" {
			restart = true
		} else {
			rest = append(rest, a)
		}
	}
	args = rest
	switch cli.Arg(args, 0) {
	case "--show":
		fmt.Print(hostsBuild(hostsAddr()))
		return 0
	case "--apply":
		apply = true
		applyUser = cli.Arg(args, 1)
	case "-h", "--help":
		hostsUsage()
		return 0
	case "":
	default:
		hostsUsage()
		return 1
	}

	os.MkdirAll(filepath.Dir(hostsFile), 0o755)
	tmp, err := os.CreateTemp(filepath.Dir(hostsFile), filepath.Base(hostsFile)+".*")
	if err != nil {
		return 1
	}
	// probed once, it can take a few seconds when 169.254.1.2 doesn't answer
	addr := hostsAddr()
	tmp.WriteString(hostsBuild(addr))
	tmp.Close()
	// users' podman reads it, so it has to stay world readable
	os.Chmod(tmp.Name(), 0o644)
	os.Rename(tmp.Name(), hostsFile)
	fmt.Println("Updated " + hostsFile + ", new containers will use it.")

	if apply {
		hostsApply(applyUser, addr, restart)
	}
	return 0
}
