// ##############################################################################
// Script Name: internal/commands/domainsadd/add_tasks.go
// Description: Background steps of domains-add (caddy, dns, vhost, mail, tor).
// Usage: opencli domains-add <DOMAIN_NAME> <USERNAME> [--docroot DOCUMENT_ROOT] [--php_version N.N] [--skip_caddy] [--skip_vhost] [--skip_containers] [--skip_dns] [--skip-sentinel] [--debug] [--hs_ed25519_public_key KEY --hs_ed25519_secret_key KEY]
// Author: Stefan Pejcic
// Created: 20.08.2024
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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"opencli/internal/backups"
	"opencli/internal/conf"
	"opencli/internal/phplisten"
	"opencli/internal/podman"
	"opencli/internal/sh"
	"opencli/internal/tenantfs"
	"opencli/internal/text"
)

type addTask struct {
	j     *addJob
	label string
}

// task output only shows up with --debug, prefixed with the task name
func (t addTask) log(msg string) { t.j.log("[" + t.label + "] " + msg) }

func (j *addJob) runParallel() {
	var wg sync.WaitGroup
	run := func(label string, fn func(addTask)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { recover() }()
			fn(addTask{j, label})
		}()
	}
	if j.onion {
		run("tor-setup", (*addJob).torSetupTask(j))
		run("tor-start", (*addJob).torStartTask(j))
	} else {
		run("caddy", j.caddyTask)
		run("dns", j.dnsTask)
	}
	run("vhost", j.vhostTask)
	run("FTP permissions", j.ftpPermsTask)
	run("php-fpm", j.phpFpmTask)
	if !j.onion {
		run("mail", j.mailTask)
		run("postfwd", j.postfwdTask)
	}
	wg.Wait()
}

func (j *addJob) torSetupTask() func(addTask) { return j.torSetup }
func (j *addJob) torStartTask() func(addTask) { return j.torStart }

func (j *addJob) isVarnish() bool {
	for _, l := range conf.Lines("/home/" + j.ctx + "/.env") {
		if strings.HasPrefix(l, "PROXY_HTTP_PORT=") {
			return true
		}
	}
	return false
}

func (j *addJob) vhostTask(t addTask) {
	if j.skipVhost {
		t.log("Skipping vhost creation (--skip_vhost)")
		return
	}
	// caddy mode has no webserver container or vhost, its caddy conf points at php-fpm instead
	if j.webServer == "caddy" {
		t.log("Caddy talks to php-fpm directly, no vhost needed")
		return
	}
	varnish := j.isVarnish()
	if !j.skipContainers {
		// podman-compose up recreates running containers too, which drops every site, so only start what's down
		var services []string
		svcs := []string{j.webServer}
		if varnish {
			svcs = append(svcs, "varnish")
		}
		for _, s := range svcs {
			if !podman.CtxIsRunning(j.ctx, s) {
				services = append(services, s)
			}
		}
		if len(services) > 0 {
			word := "containers"
			if len(services) == 1 {
				word = "container"
			}
			t.log("Starting " + strconv.Itoa(len(services)) + " " + word + " ( " + strings.Join(services, " ") + ")")
			sh.Opencli(append([]string{"domains-add-bg", "compose", j.ctx, "up", "-d", "--no-deps"}, services...)...).Quiet().Background()
		}
	}

	whDir := "/home/" + j.ctx + "/docker-data/volumes/" + j.ctx + "_webserver_data/"
	vhostFile := whDir + "_data/" + j.domain + ".conf"
	tpl := "/etc/openpanel/nginx/vhosts/1.1/docker_" + j.webServer + "_domain.conf"
	t.log("Creating vhost: " + vhostFile)
	b, _ := os.ReadFile(tpl)
	content := replaceAll(string(b), "<DOMAIN_NAME>", j.domain, "<USER>", j.user, "<PHP>", j.php, "<DOCUMENT_ROOT>", j.docroot)
	content = phplisten.VhostTransport(j.phpListen, content)
	tenantfs.Write(tenantfs.Volume(j.ctx, "webserver_data"), vhostFile, 0o644, []byte(content))
	if uid, ok := statUID("/home/" + j.ctx); ok && j.ctxUID != "" {
		os.Chown(whDir, uid, uid)
		os.Chown(whDir+"_data/", uid, uid)
	}
	if j.skipContainers {
		return
	}
	// litespeed only registers new vhosts in its start.sh, so it still needs a real restart
	if strings.Contains(j.webServer, "litespeed") {
		sh.Opencli("domains-add-bg", "restart", j.ctx, j.webServer).Quiet().Background()
	} else {
		args := []string{"domains-add-bg", "reload", j.ctx, j.webServer}
		if j.isModule("php") {
			args = append(args, "php-fpm-"+j.php)
		}
		sh.Opencli(args...).Quiet().Background()
	}
}

func (j *addJob) ftpPermsTask(t addTask) {
	realPath := "/home/" + j.ctx + "/docker-data/volumes/" + j.ctx + "_html_data/_data/"
	newDir := realPath + strings.TrimPrefix(j.docroot, "/var/www/html/")
	out, _ := sh.Opencli("ftp-list", j.user).NoErr().Out()
	lines := strings.Split(out, "\n")
	found := false
	if len(lines) > 1 {
		for _, l := range lines[1:] {
			f := l
			if strings.Contains(l, "|") {
				f = strings.Split(l, "|")[1]
			}
			if strings.Trim(f, " ") == j.docroot {
				found = true
				break
			}
		}
	}
	if !found {
		return
	}
	tenantfs.ChownR(strings.TrimSuffix(realPath, "/"), newDir, j.user+":"+j.user)
	for _, d := range []string{
		"/home/" + j.ctx,
		"/home/" + j.ctx + "/docker-data",
		"/home/" + j.ctx + "/docker-data/volumes",
		"/home/" + j.ctx + "/docker-data/volumes/" + j.ctx + "_html_data",
		"/home/" + j.ctx + "/docker-data/volumes/" + j.ctx + "_html_data/_data",
	} {
		if st, err := os.Stat(d); err == nil {
			os.Chmod(d, st.Mode().Perm()|0o555|(st.Mode() & ^fs.ModePerm))
		}
	}
}

func (j *addJob) phpFpmTask(t addTask) {
	if j.skipContainers || strings.Contains(j.webServer, "litespeed") {
		return
	}
	if !j.isModule("php") {
		t.log("PHP module disabled — skipping")
		return
	}
	if podman.CtxIsRunning(j.ctx, "php-fpm-"+j.php) {
		return
	}
	t.log("Starting PHP-FPM " + j.php)
	sh.Opencli("domains-add-bg", "compose", j.ctx, "up", "-d", "php-fpm-"+j.php).Quiet().Background()
}

func (j *addJob) postfwdTask(t addTask) {
	if j.enterprise == "" {
		return
	}
	sh.Opencli("email-ratelimit", "--domain="+j.domain).Quiet().Background()
}

// ---------------------------------------------------------------- caddy

var addSecRule = regexp.MustCompile(`SecRuleEngine .*`)

func bindInBlock(confPath, header, ip string) {
	b, err := os.ReadFile(confPath)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	if strings.HasSuffix(string(b), "\n") {
		lines = lines[:len(lines)-1]
	}
	bindRe := regexp.MustCompile(`^[[:space:]]*bind `)
	has := false
	for i := 0; i+1 < len(lines); i++ {
		if strings.HasPrefix(lines[i], header) && bindRe.MatchString(lines[i+1]) {
			has = true
			break
		}
	}
	var out []string
	if has {
		for i := 0; i < len(lines); i++ {
			out = append(out, lines[i])
			if strings.HasPrefix(lines[i], header) && i+1 < len(lines) {
				i++
				if bindRe.MatchString(lines[i]) {
					out = append(out, "    bind "+ip)
				} else {
					out = append(out, lines[i])
				}
			}
		}
	} else {
		for _, l := range lines {
			out = append(out, l)
			if strings.HasPrefix(l, header) {
				out = append(out, "    bind "+ip)
			}
		}
	}
	conf.WriteFileKeep(confPath, strings.Join(out, "\n")+"\n", false)
}

func (j *addJob) caddyTask(t addTask) {
	if j.skipCaddy {
		t.log("Skipping Caddy config (--skip_caddy)")
		return
	}
	logsDir := "/var/log/caddy/domlogs/" + j.domain
	wafDir := "/var/log/caddy/coraza_waf"
	if os.MkdirAll(logsDir, 0o777) == nil {
		touch(logsDir + "/access.log")
	}
	if os.MkdirAll(wafDir, 0o777) == nil {
		touch(wafDir + "/" + j.domain + ".log")
	}
	envFile := "/home/" + j.ctx + "/.env"
	if !conf.IsFile(envFile) {
		t.log("WARNING: .env not found for " + j.ctx)
		return
	}
	httpPort, _ := conf.Last(envFile, "HTTP_PORT")
	httpsPort, _ := conf.Last(envFile, "HTTPS_PORT")
	nonSSL := cutField2(conf.Unquote(httpPort), ":")
	ssl := cutField2(conf.Unquote(httpsPort), ":")

	os.MkdirAll("/etc/openpanel/caddy/domains/", 0o777)
	domainsFile := "/etc/openpanel/caddy/domains/" + j.domain + ".conf"
	tpl, _ := os.ReadFile("/etc/openpanel/caddy/templates/domain.conf")
	host := "127.0.0.1:"
	// a template saved through a browser has CRLF, which breaks the $-anchored edits other scripts make to this file
	lines := strings.Split(string(tpl), "\n")
	for i, l := range lines {
		l = strings.TrimSuffix(l, "\r")
		l = replaceAll(l, "<DOMAIN_NAME>", j.domain, "127.0.0.1:", "", "<SSL_PORT>", host+ssl, "<NON_SSL_PORT>", host+nonSSL)
		lines[i] = l
	}
	os.WriteFile(domainsFile, []byte(strings.Join(lines, "\n")), 0o666&^0o022)

	// binding the main ip would take every connection to it away from the other domains
	if j.dedicated != "" && j.dedicated != j.ip && j.dedicated != firstHostIP() {
		bindInBlock(domainsFile, "http://"+j.domain+", http://*."+j.domain+" {", j.dedicated)
		bindInBlock(domainsFile, "https://"+j.domain+", https://*."+j.domain+" {", j.dedicated)
	}

	waf := "Off"
	// https://github.com/stefanpejcic/OpenPanel/discussions/1107
	if conf.Exists("/home/" + j.ctx + "/waf.disabled") {
		waf = "Off"
	} else if strings.Contains(strings.ToLower(conf.Read(conf.Panel)), "waf") {
		waf = "On"
	}
	t.log("WAF SecRuleEngine: " + waf)
	if b, err := os.ReadFile(domainsFile); err == nil {
		ls := strings.Split(string(b), "\n")
		for i, l := range ls {
			ls[i] = addSecRule.ReplaceAllLiteralString(l, "SecRuleEngine "+waf)
		}
		conf.WriteFileKeep(domainsFile, strings.Join(ls, "\n"), false)
	}

	// caddy mode, php_fastcgi on the user's socket instead of the webserver ports
	if j.webServer == "caddy" {
		t.log("Pointing Caddy at php-fpm-" + j.php + " over the socket")
		phplisten.CaddyConfDirect(domainsFile, j.ctx, j.docroot, j.php)
	}

	// varnish routing is skipped on purpose, bash only set VARNISH in another subshell so it was always false here

	if podman.IsRunning("caddy") {
		t.log("Reloading Caddy")
		if sh.C("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").Quiet().OK() {
			t.log("Caddy reloaded successfully")
		} else {
			t.log("Caddy reload failed — check config")
		}
	} else {
		t.log("Caddy not running (or stuck), recovering in background")
		sh.Opencli("domains-add-bg", "ensure", "caddy", "/root", "caddy").Quiet().Background()
	}
}

// ---------------------------------------------------------------- dns

func (j *addJob) dnsTask(t addTask) {
	if j.skipDNS {
		t.log("Skipping DNS zone creation (--skip_dns)")
		j.generateDKIM(t)
		return
	}
	if !j.isModule("dns") {
		t.log("DNS module disabled — skipping")
		j.generateDKIM(t)
		return
	}
	j.createZoneFile(t)
	// after the zone exists, create_zone_file overwrites it so this can't run alongside
	if j.generateDKIM(t) {
		j.addDKIMToZone(t)
	}
	// own zone only, subdomains just add records to the apex one
	zone := addZoneDir + j.domain + ".zone"
	if !j.useParent && conf.IsFile(zone) {
		if backups.MountAdd(j.ctx, zone, "/backup/dns/"+j.domain+".zone") {
			backups.ServiceReload(j.user, j.ctx)
		}
	}
	j.slaveDNS(t)
	j.updateNamedConf(t)
	if podman.IsRunning("openpanel_dns") {
		t.log("Reloading BIND")
		sh.C("podman", "exec", "openpanel_dns", "rndc", "reconfig").Quiet().Run()
	} else {
		t.log("BIND not running (or stuck), recovering in background")
		sh.Opencli("domains-add-bg", "ensure", "openpanel_dns", "/root", "bind9").Quiet().Background()
	}
}

func (j *addJob) createZoneFile(t addTask) {
	os.MkdirAll(addZoneDir, 0o777)
	if j.useParent {
		t.log("Appending records to apex zone: " + addZoneDir + j.apex + ".zone")
		spf := "ip4"
		if !j.ipv4 {
			spf = "ip6"
		}
		appendFile(addZoneDir+j.apex+".zone", j.domain+"    14400     IN      A       "+j.ip+"\n"+
			j.domain+"    14400     IN      TXT     'v=spf1 "+spf+":"+j.ip+" +a +mx ~all'\n")
		return
	}
	tpl := "/etc/openpanel/bind9/zone_template.txt"
	if j.ipv4 {
		t.log("Creating zone with A records: " + addZoneDir + j.domain + ".zone")
	} else {
		tpl = "/etc/openpanel/bind9/zone_template_ipv6.txt"
		t.log("Creating zone with AAAA records: " + addZoneDir + j.domain + ".zone")
	}
	ns1, ns2, rp := j.ns1, j.ns2, j.rpemail
	if ns1 == "" {
		ns1 = "ns1.openpanel.org"
	}
	if ns2 == "" {
		ns2 = "ns2.openpanel.org"
	}
	if rp == "" {
		rp = "root." + j.domain
	}
	rp = strings.ReplaceAll(rp, "@", ".")
	ts := time.Now().Format("20060102")
	b, _ := os.ReadFile(tpl)
	s := strings.TrimRight(string(b), "\n")
	pairs := []string{"{domain}", j.domain, "{ns1}", ns1, "{ns2}", ns2}
	if j.ns3 != "" {
		pairs = append(pairs, "{ns3}", j.ns3, "{ns4}", j.ns4)
	}
	pairs = append(pairs, "{rpemail}", rp, "{server_ip}", j.ip, "YYYYMMDD", ts)
	s = replaceAll(s, pairs...)
	os.WriteFile(addZoneDir+j.domain+".zone", []byte(strings.TrimRight(s, "\n")+"\n"), 0o666&^0o022)
}

func (j *addJob) generateDKIM(t addTask) bool {
	if j.enterprise == "" || !conf.IsFile(addMailCompose) {
		return false
	}
	t.log("Generating DKIM for the domain")
	sh.Opencli("email-setup", "config", "dkim", "domain", j.domain).Quiet().Run()
	return conf.IsFile("/usr/local/mail/openmail/docker-data/dms/config/opendkim/keys/" + j.domain + "/mail.txt")
}

func (j *addJob) addDKIMToZone(t addTask) {
	dkim := "/usr/local/mail/openmail/docker-data/dms/config/opendkim/keys/" + j.domain + "/mail.txt"
	zone := addZoneDir + j.domain + ".zone"
	if j.useParent {
		zone = addZoneDir + j.apex + ".zone"
	}
	if st, err := os.Stat(zone); err != nil || st.Size() == 0 {
		t.log("No local DNS zone for the domain, DKIM record not added")
		return
	}
	if strings.Contains(conf.Read(zone), "mail._domainkey") {
		t.log("DNS zone already includes a mail._domainkey record")
		return
	}
	appendFile(zone, dkimBlock(dkim))
	t.log("DKIM was successfully generated and added in the local DNS zone for domain")
}

func (j *addJob) updateNamedConf(t addTask) {
	name := j.domain
	if j.useParent {
		name = j.apex
	} else {
		t.log("Adding zone to named.conf.local")
	}
	re, err := regexp.Compile(`zone "` + name + `"`)
	if err == nil && re.MatchString(conf.Read(addNamedConfLocal)) {
		if !j.useParent {
			t.log("Zone '" + name + "' already in " + addNamedConfLocal)
		}
		return
	}
	appendFile(addNamedConfLocal, `zone "`+name+`" IN { type master; file "`+addZoneDir+name+`.zone"; };`+"\n")
}

var (
	slaveAllowRe  = regexp.MustCompile(`^.*allow-transfer\s+\{\s*([^}]*)`)
	slaveNotifyRe = regexp.MustCompile(`^.*also-notify\s+\{\s*([^}]*)`)
)

// grep -oP '^(?!\s*//).*key\s+\{\s*\K[^}]*' joined and stripped of whitespace
func slaveList(lines []string, re *regexp.Regexp) (string, bool) {
	var parts []string
	found := false
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimLeft(l, " \t\n\v\f\r"), "//") {
			continue
		}
		if m := re.FindStringSubmatch(l); m != nil {
			found = true
			parts = append(parts, m[1])
		}
	}
	return strings.Join(strings.Fields(strings.Join(parts, "")), ""), found
}

func splitSemi(s string) []string {
	f := strings.Split(s, ";")
	if len(f) > 0 && f[len(f)-1] == "" {
		f = f[:len(f)-1]
	}
	return f
}

func (j *addJob) slaveDNS(t addTask) {
	lines := conf.Lines(addBindConfig)
	transfer, ok1 := slaveList(lines, slaveAllowRe)
	notify, ok2 := slaveList(lines, slaveNotifyRe)
	if !ok1 || !ok2 {
		return
	}
	notifyIPs := splitSemi(notify)
	joined := " " + strings.Join(notifyIPs, " ") + " "
	for _, ip := range splitSemi(transfer) {
		if ip == "" {
			continue
		}
		if strings.Contains(joined, " "+ip+" ") {
			j.notifySlave(t, ip, j.ip)
		}
	}
}

func (j *addJob) notifySlave(t addTask, slave, master string) {
	if j.useParent {
		return
	}
	t.log("Notifying slave " + slave + " for zone " + j.domain)
	rndc := []string{"exec", "openpanel_dns", "rndc", "-s", slave, "-p", "953", "-k", "/etc/bind/rndc.key"}
	out := sh.C("podman", append(rndc, "status")...).NoErr().Str()
	if strings.Contains(out, "number of zones") {
		t.log("Slave reachable via rndc — adding zone " + j.domain)
		zoneDef := j.domain + " { type slave; masters { " + master + "; }; file \"/etc/bind/zones/" + j.domain + ".zone\"; allow-notify { " + master + "; }; };"
		if sh.C("podman", append(rndc, "addzone", zoneDef)...).NoErr().NoOut().OK() {
			t.log("Zone " + j.domain + " successfully added to slave " + slave)
			return
		}
		t.log("rndc addzone failed, falling back to SSH")
	} else {
		t.log("Slave not reachable via rndc, falling back to SSH")
	}
	script := fmt.Sprintf(`if ! grep -q "%[1]s.zone" /etc/bind/named.conf.local; then
    cat >> /etc/bind/named.conf.local <<ZONE
zone "%[1]s" { type slave; masters { %[2]s; }; file "/etc/bind/zones/%[1]s.zone"; allow-notify { %[2]s; }; };
ZONE
    mkdir -p /etc/bind/zones/
    touch /etc/bind/zones/%[1]s.zone
fi
if [ "$(podman inspect openpanel_dns --format '{{.State.Status}}' 2>/dev/null)" = "running" ]; then
    podman exec openpanel_dns rndc reconfig
else
    cd /root && timeout 30 podman-compose up -d bind9
fi
`, j.domain, master)
	sh.C("ssh", "-q", "-o", "BatchMode=yes", "-o", "LogLevel=ERROR", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null", "root@"+slave, "bash -s").Stdin(script).Quiet().Run()
}

// ---------------------------------------------------------------- mail

func (j *addJob) mailTask(t addTask) {
	if j.enterprise == "" || !conf.IsFile(addMailCompose) {
		return
	}
	storeIn := ""
	var vals []string
	for _, l := range conf.Lines(conf.AdminINI) {
		if strings.HasPrefix(l, "email_storage_location=") {
			vals = append(vals, l[len("email_storage_location="):])
		}
	}
	storeIn = text.Xargs(strings.Join(vals, "\n"))

	mailData := "/home/" + j.ctx + "/docker-data/volumes/" + j.ctx + "_mail_data"
	os.MkdirAll(mailData+"/_data/", 0o777)
	var volume string
	if strings.HasPrefix(storeIn, "/") {
		t.log("Using " + storeIn + " for email storage")
		os.MkdirAll(storeIn, 0o777)
		os.Symlink(storeIn+"/"+j.domain, mailData+"/_data/"+j.domain)
		volume = "      - " + storeIn + ":/var/mail/"
	} else {
		dir := "/home/" + j.ctx + "/mail/" + j.domain + "/"
		t.log("Creating " + dir + " for emails")
		os.MkdirAll(dir, 0o777)
		os.Symlink(dir, mailData+"/_data/"+j.domain)
		volume = "      - " + dir + ":/var/mail/" + j.domain + "/"
	}
	// volume dir must belong to the user, else rootless podman can't create the mail_data volume (backup container fails)
	// chown the symlinks themselves, never the mail store they point to
	if uid, ok := statUID("/home/" + j.ctx); ok {
		filepath.WalkDir(mailData, func(p string, _ fs.DirEntry, err error) error {
			if err == nil {
				os.Lchown(p, uid, uid)
			}
			return nil
		})
	}
	// the backup container can't follow the mail_data symlink, so it gets the real mail dir mounted too
	if backups.MountAdd(j.ctx, backups.MailDir(j.ctx, j.domain), "/backup/emails/"+j.domain) {
		backups.ServiceReload(j.user, j.ctx)
	} else {
		t.log("Could not add the mail backup mount for " + j.domain + " to /home/" + j.ctx + "/docker-compose.yml")
	}
	b, err := os.ReadFile(addMailCompose)
	if err != nil || strings.Contains(string(b), volume) {
		return
	}
	lines := strings.Split(string(b), "\n")
	var out []string
	in := false
	for _, l := range lines {
		if !in && strings.HasPrefix(l, "  mailserver:") {
			in = true
			out = append(out, l)
			continue
		}
		out = append(out, l)
		if in {
			if strings.HasPrefix(l, "    volumes:") {
				out = append(out, volume)
			}
			if strings.HasPrefix(l, "  roundcube:") {
				in = false
			}
		}
	}
	conf.WriteFileKeep(addMailCompose, strings.Join(out, "\n"), false)
	t.log("Reconfiguring mailserver in background")
	sh.C("podman-compose", "up", "-d", "--force-recreate", "mailserver").Dir("/usr/local/mail/openmail/").Quiet().Background()
}

// ---------------------------------------------------------------- tor

func (j *addJob) torSetup(t addTask) {
	torDir := "/home/" + j.ctx + "/tor"
	folder := "hidden_service"
	if !conf.IsDir(torDir+"/hidden_service") || !conf.IsFile(torDir+"/torrc") {
		folder = "hidden_service"
	} else {
		highest := 0
		ents, _ := os.ReadDir(torDir + "/hidden_service")
		for _, e := range ents {
			if st, err := os.Stat(torDir + "/hidden_service/" + e.Name()); err != nil || !st.IsDir() {
				continue
			}
			if text.IsInt(e.Name()) {
				if n := text.Atoi(e.Name()); n > highest {
					highest = n
				}
			}
		}
		folder = "hidden_service/" + strconv.Itoa(highest+1)
	}
	// tor_dir is mounted into the user's tor container, so dirs and files there go through the tenant helpers too
	os.MkdirAll(torDir, 0o777)
	if tenantfs.Mkdir(torDir, torDir+"/"+folder+"/authorized_clients") != nil {
		t.log("FATAL ERROR: Failed to create " + torDir + "/" + folder)
		return
	}
	mode := func(p string) fs.FileMode {
		if st, err := os.Stat(p); err == nil {
			return st.Mode().Perm()
		}
		return 0o600
	}
	for _, k := range []struct{ src, name string }{{j.hsPubPath, "hs_ed25519_public_key"}, {j.hsSecPath, "hs_ed25519_secret_key"}} {
		b, err := tenantfs.Read(j.hsRoot, k.src)
		if err == nil {
			err = tenantfs.Write(torDir, torDir+"/"+folder+"/"+k.name, mode(k.src), b)
		}
		if err != nil {
			t.log("FATAL ERROR: Failed to copy " + k.name)
			return
		}
	}
	if uid, ok := statUID("/home/" + j.ctx); ok {
		os.Chown(torDir, uid, uid)
	}
	if os.Chmod(torDir+"/torrc", 0o600) != nil {
		t.log("chmod: cannot access '" + torDir + "/torrc': No such file or directory")
	}
	proxy := j.webServer
	if j.isVarnish() {
		proxy = "varnish"
	}
	appendFile(torDir+"/torrc", fmt.Sprintf("HiddenServiceDir /var/lib/tor/%s/\nHiddenServicePort 80 %s:80\n\n", folder, proxy))
	t.log(".onion files saved in " + folder)
}

func (j *addJob) torStart(t addTask) {
	if podman.User(j.ctx, "ps", "-q", "-f", "name=tor").NoErr().Str() != "" {
		t.log("Tor running — restarting to apply new config")
		uid, _ := statUID("/home/" + j.ctx)
		sock := "unix:///hostfs/run/user/" + strconv.Itoa(uid) + "/podman/podman.sock"
		sh.C("podman", "--remote", "restart", "tor").Env("CONTAINER_HOST=" + sock).Dir("/home/" + j.ctx + "/").Quiet().Background()
	} else {
		t.log("Starting Tor")
		sh.Opencli("domains-add-bg", "compose", j.ctx, "up", "-d", "tor").Quiet().Background()
	}
}
