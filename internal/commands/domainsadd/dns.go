// ##############################################################################
// Script Name: internal/commands/domainsadd/dns.go
// Description: Manage DNS zones and the DNS server via subcommands (create, delete, reload, list, check, start/stop/restart, etc).
// Usage: opencli domains-dns <reconfig|check|reload|show|list|create|delete|default|count|config|start|restart|hard-restart|stop> [DOMAIN] [-y]
// Author: Stefan Pejcic
// Created: 31.08.2024
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

package domainsadd

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-dns",
		Desc:  "Manage DNS zones and the DNS server via subcommands (create, delete, reload, list, check, start/stop/restart, etc).",
		Usage: "opencli domains-dns <reconfig|check|reload|show|list|create|delete|default|count|config|start|restart|hard-restart|stop> [DOMAIN] [-y]",
		Run:   dns,
	})
}

const (
	dnsGreen = "\033[0;32m"
	dnsReset = "\033[0m"
	dnsZones = "/etc/bind/zones/"
)

type dnsJob struct {
	serverIP string
	domain   string
}

func dnsUsage() {
	c := "opencli domains-dns"
	rows := [][2]string{
		{"reconfig", "Load new DNS zones into bind server."},
		{"check <DOMAIN>", "Check and validate dns zone for a domain."},
		{"reload <DOMAIN>", "Reload DNS zone for a single domain."},
		{"show <DOMAIN>", "Display DNS zone for a single domain."},
		{"list", "List all domains with DNS zones on the server."},
		{"create <DOMAIN>", "Create DNS zone for a domain."},
		{"delete <DOMAIN>", "Delete DNS zone for a domain."},
		{"default <DOMAIN>", "Restart default DNS zone for a domain."},
		{"count", "Display total number of DNS zones present on the server."},
		{"config", "Check main bind configuration file for syntax errros."},
		{"start", "Start the DNS server."},
		{"restart", "Soft restart of bind9 podman container."},
		{"hard-restart", "Hard restart: terminates container and start again."},
		{"stop", "Stop the DNS server."},
	}
	// the dashes line up by hand in the bash, keep the same padding
	pad := map[string]string{
		"reconfig": "               ", "check <DOMAIN>": "         ", "reload <DOMAIN>": "        ", "show <DOMAIN>": "          ",
		"list": "                   ", "create <DOMAIN>": "        ", "delete <DOMAIN>": "        ", "default <DOMAIN>": "       ",
		"count": "                  ", "config": "                 ", "start": "                  ", "restart": "                ",
		"hard-restart": "           ", "stop": "                   ",
	}
	fmt.Println("Usage:")
	for _, r := range rows {
		fmt.Println(" " + dnsGreen + c + " " + r[0] + dnsReset + pad[r[0]] + "- " + r[1])
	}
	cli.Exit(1)
}

func dnsStdinIsTerminal() bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

// readTimeout is read -r -t N -p prompt, ok is false on timeout or EOF
func dnsReadTimeout(prompt string, d time.Duration) (string, bool) {
	if dnsStdinIsTerminal() {
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

func dns(args []string) int {
	if len(args) < 1 {
		dnsUsage()
	}
	d := &dnsJob{}
	for len(args) > 0 {
		switch args[0] {
		case "reconfig":
			fmt.Println("Loading new DNS zones..")
			sh.C("podman", "exec", "openpanel_dns", "rndc", "reconfig").Run()
			return 0
		case "check":
			if cli.Arg(args, 1) == "" {
				fmt.Println("Error: 'check' command requires a zone argument.")
				dnsUsage()
			}
			fmt.Println("Checking DNS zone for domain: " + args[1])
			sh.C("podman", "exec", "openpanel_dns", "named-checkzone", args[1], dnsZones+args[1]+".zone").Run()
			return 0
		case "list":
			fmt.Println("Displaying all DNS zones on the server:")
			for _, z := range dnsFindZones() {
				fmt.Println(z)
			}
			return 0
		case "show":
			if cli.Arg(args, 1) == "" {
				fmt.Println("Error: 'show' command requires a zone argument.")
				dnsUsage()
			}
			fmt.Println("DNS zone for domain: " + args[1] + " - file: " + dnsZones + args[1] + ".zone")
			if b, err := os.ReadFile(dnsZones + args[1] + ".zone"); err == nil {
				os.Stdout.Write(b)
			} else {
				fmt.Fprintf(os.Stderr, "cat: %s: No such file or directory\n", dnsZones+args[1]+".zone")
			}
			return 0
		case "create":
			d.fetchIP()
			d.create(cli.Arg(args, 1))
			d.addSubdomains(cli.Arg(args, 1))
		case "reload":
			dom := cli.Arg(args, 1)
			if dom != "" {
				fmt.Println("Reloading DNS zone for domain: " + dom)
				sh.C("podman", "exec", "openpanel_dns", "rndc", "reload", dom).Run()
			} else {
				fmt.Println("Reloading all DNS zones..")
				sh.C("podman", "exec", "openpanel_dns", "rndc", "reload").Run()
			}
			return 0
		case "delete":
			if cli.Arg(args, 1) == "" {
				fmt.Println("Error: 'delete' command requires a domain name.")
				dnsUsage()
			}
			d.deleteZone(args[1], cli.Arg(args, 2))
		case "default":
			d.fetchIP()
			if cli.Arg(args, 1) == "" {
				fmt.Println("Error: 'default' command requires a domain name.")
				dnsUsage()
			}
			d.restoreDefault(args[1], cli.Arg(args, 2))
		case "count":
			fmt.Printf("Total number of DNS zones on the server: %s%d%s\n", dnsGreen, len(dnsFindZones()), dnsReset)
			return 0
		case "restart":
			fmt.Println("Restarting DNS service..")
			sh.C("podman", "restart", "openpanel_dns").Run()
			return 0
		case "config":
			fmt.Println("Checking /etc/bind/named.conf configuration:")
			sh.C("podman", "exec", "openpanel_dns", "named-checkconf", "/etc/bind/named.conf").Run()
			return 0
		case "start":
			fmt.Println("Starting DNS service..")
			sh.C("podman-compose", "up", "-d", "bind9").Dir("/root").Run()
			return 0
		case "stop":
			fmt.Println("Stopping DNS service..")
			dnsStop()
			return 0
		case "hard-restart":
			// stop and start each exit in the bash, so both steps are inline here
			fmt.Println("Stopping DNS service..")
			dnsStop()
			fmt.Println("Starting DNS service..")
			sh.C("podman-compose", "up", "-d", "bind9").Dir("/root").Run()
			return 0
		default:
			if d.domain == "" {
				d.domain = args[0]
			} else {
				fmt.Println("Unknown option: " + args[0])
				dnsUsage()
			}
			args = args[1:]
		}
	}
	return 0
}

func dnsStop() {
	if sh.C("podman", "stop", "openpanel_dns").Run() == nil {
		sh.C("podman", "rm", "openpanel_dns").Run()
	}
}

// dnsFindZones is `find /etc/bind/zones/ -type f -name "*.zone"`, directory order like find
func dnsFindZones() []string {
	var out []string
	var walk func(dir string)
	walk = func(dir string) {
		f, err := os.Open(dir)
		if err != nil {
			return
		}
		names, _ := f.Readdirnames(-1)
		f.Close()
		for _, n := range names {
			p := strings.TrimSuffix(dir, "/") + "/" + n
			st, err := os.Lstat(p)
			if err != nil {
				continue
			}
			if st.IsDir() {
				walk(p)
			} else if st.Mode().IsRegular() && strings.HasSuffix(n, ".zone") {
				out = append(out, p)
			}
		}
	}
	walk(dnsZones)
	return out
}

func (d *dnsJob) fetchIP() {
	if v, ok := curlGet("https://ip.openpanel.com", 2*time.Second, "tcp4"); ok {
		d.serverIP = v
		return
	}
	d.serverIP, _ = curlGet("https://ifconfig.me/ip", 2*time.Second, "tcp4")
}

func (d *dnsJob) rndc(args ...string) {
	sh.C("podman", append([]string{"exec", "openpanel_dns", "rndc"}, args...)...).Quiet().Run()
}

func (d *dnsJob) moveToBackup(domain string) {
	os.MkdirAll(dnsZones+"backups/", 0o777)
	src := dnsZones + domain + ".zone"
	if err := os.Rename(src, dnsZones+"backups/"+domain+".zone."+time.Now().Format("20060102150405")); err != nil {
		fmt.Fprintf(os.Stderr, "mv: cannot stat '%s': No such file or directory\n", src)
	}
}

func (d *dnsJob) deleteZone(domain, yes string) {
	del := func() {
		d.moveToBackup(domain)
		// drop the zone from named.conf.local and reload bind, otherwise it keeps serving a zone whose file is gone and the next reconfig errors on the missing file
		ncl := addNamedConfLocal
		if conf.IsFile(ncl) && strings.Contains(conf.Read(ncl), `zone "`+domain+`"`) {
			var keep []string
			for _, l := range strings.Split(strings.TrimSuffix(conf.Read(ncl), "\n"), "\n") {
				if !strings.Contains(l, `zone "`+domain+`"`) {
					keep = append(keep, l)
				}
			}
			if len(keep) > 0 {
				s := strings.Join(keep, "\n") + "\n"
				if os.WriteFile(ncl+".tmp", []byte(s), 0o666&^0o022) == nil {
					os.Rename(ncl+".tmp", ncl)
				}
			}
		}
		d.rndc("reconfig")
	}
	if yes == "-y" {
		fmt.Println("Deleting DNS zone for domain: " + domain)
		del()
		cli.Exit(0)
	}
	confirm, ok := dnsReadTimeout("Are you sure you want to delete the existing DNS zone for domain: "+domain+" ? (y/n): ", 10*time.Second)
	if !ok {
		fmt.Println("Timed out.")
		cli.Exit(1)
	}
	if confirm == "y" || confirm == "Y" {
		fmt.Println("Deleting DNS zone for domain: " + domain)
		del()
		cli.Exit(0)
	}
	fmt.Println("Canceled.")
	cli.Exit(1)
}

func (d *dnsJob) restoreDefault(domain, yes string) {
	edit := func() {
		d.moveToBackup(domain)
		d.create(domain)
		d.addSubdomains(domain)
		d.addExistingDKIM(domain)
	}
	if yes == "-y" {
		fmt.Println("Deleting DNS zone for domain: " + domain + "  and restoring default zone..")
		edit()
		cli.Exit(0)
	}
	confirm, ok := dnsReadTimeout("Are you sure you want to delete the existing DNS zone for domain: "+domain+" and restore default records? (y/n): ", 10*time.Second)
	if !ok {
		fmt.Println("Timed out.")
		cli.Exit(1)
	}
	if confirm == "y" || confirm == "Y" {
		fmt.Println("Restoring DNS zone for domain: " + domain + " to default...")
		edit()
		cli.Exit(0)
	}
	fmt.Println("Canceled.")
	cli.Exit(1)
}

// puts the existing DKIM key back after a reset, the key itself stays so mail already signed with it keeps verifying
func (d *dnsJob) addExistingDKIM(domain string) {
	zone := dnsZones + domain + ".zone"
	dkim := "/usr/local/mail/openmail/docker-data/dms/config/opendkim/keys/" + domain + "/mail.txt"
	if !conf.IsFile(dkim) || !conf.IsFile(zone) {
		return
	}
	if strings.Contains(conf.Read(zone), "mail._domainkey") {
		return
	}
	appendFile(zone, dkimBlock(dkim))
	d.rndc("reload", domain)
	fmt.Println("Restored DKIM record mail._domainkey for domain: " + domain)
}

func (d *dnsJob) addSubdomains(apex string) {
	zone := dnsZones + apex + ".zone"
	if !conf.IsFile(zone) {
		fmt.Println("Zone file not found: " + zone)
		return
	}
	out, _ := db.Plain("SELECT domain_url FROM domains WHERE domain_url LIKE '%."+apex+"';", "-N", "-s").Out()
	subs := strings.Fields(out)
	if len(subs) == 0 {
		fmt.Println("No subdomains found for " + apex)
		return
	}
	fmt.Println("Adding subdomains to DNS zone: " + apex)
	for _, sub := range subs {
		appendFile(zone, sub+"\t14400\tIN\tA\t"+d.serverIP+"\n")
		appendFile(zone, sub+"\t14400\tIN\tTXT\t\"v=spf1 ip4:"+d.serverIP+" +a +mx ~all\"\n")
	}
	sh.C("podman", "exec", "openpanel_dns", "rndc", "reload", apex).Run()
}

// get_config_value of the bash, every matching line joined with whitespace removed
func dnsConfigValue(key string) string {
	re := regexp.MustCompile(`^\s*` + key + `=`)
	var sb strings.Builder
	for _, l := range conf.Lines(conf.Panel) {
		if loc := re.FindStringIndex(l); loc != nil {
			sb.WriteString(strings.Join(strings.Fields(l[loc[1]:]), ""))
		}
	}
	return sb.String()
}

func (d *dnsJob) create(domain string) {
	if conf.IsFile(dnsZones+domain+".zone") || conf.Exists(dnsZones+domain+".zone") {
		fmt.Println("Error: DNS zone already exists for domain: " + domain)
		cli.Exit(1)
	}
	if domain != "" {
		fmt.Println("Creating DNS zone for domain: " + domain)
	}
	tpl := conf.Read("/etc/openpanel/bind9/zone_template.txt")
	tpl = strings.TrimRight(tpl, "\n")

	ns1, ns2, ns3 := dnsConfigValue("ns1"), dnsConfigValue("ns2"), dnsConfigValue("ns3")
	rp := dnsConfigValue("email")
	if i := strings.Index(rp, ","); i >= 0 {
		rp = rp[:i]
	}
	if ns1 == "" {
		ns1 = "ns1.openpanel.org"
	}
	if ns2 == "" {
		ns2 = "ns2.openpanel.org"
	}
	if rp == "" {
		rp = "root." + domain
	}
	rp = strings.ReplaceAll(rp, "@", ".")
	ts := time.Now().Format("20060102")

	owner := ownerOf(domain)
	var ip string
	if owner != "" {
		jf := conf.UsersDir + "/" + owner + "/ip.json"
		if conf.Exists(jf) {
			ip = "null"
			if o, err := ojson.ParseObject(conf.Read(jf)); err == nil {
				if v, ok := o.Get("ip"); ok {
					ip = ojson.Text(v)
				}
			} else {
				ip = ""
			}
		} else {
			ip = d.serverIP
		}
	} else {
		ip = d.serverIP
		if ip == "" {
			ip = ipGlobal(false)
		}
	}
	pairs := []string{"{domain}", domain, "{ns1}", ns1, "{ns2}", ns2}
	if ns3 != "" {
		pairs = append(pairs, "{ns3}", ns3, "{ns4}", dnsConfigValue("ns4"))
	}
	pairs = append(pairs, "{rpemail}", rp, "{server_ip}", ip, "YYYYMMDD", ts)
	content := strings.TrimRight(replaceAll(tpl, pairs...), "\n")
	os.MkdirAll(dnsZones, 0o777)
	os.WriteFile(dnsZones+domain+".zone", []byte(content+"\n"), 0o666&^0o022)

	// delete drops the zone from named.conf.local, without it back bind never loaded the new file
	if !strings.Contains(conf.Read(addNamedConfLocal), `zone "`+domain+`"`) {
		appendFile(addNamedConfLocal, `zone "`+domain+`" IN { type master; file "`+dnsZones+domain+`.zone"; };`+"\n")
	}
	d.rndc("reconfig")
	d.rndc("reload", domain)
	sh.C("podman-compose", "up", "-d", "bind9").Dir("/root").Quiet().Run()
}
