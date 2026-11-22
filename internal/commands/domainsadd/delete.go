// ##############################################################################
// Script Name: internal/commands/domainsadd/delete.go
// Description: Delete a domain name.
// Usage: opencli domains-delete <DOMAIN_NAME> [--debug]
// Author: Stefan Pejcic
// Created: 07.11.2024
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
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"opencli/internal/backups"
	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/podman"
	"opencli/internal/sh"
	"opencli/internal/tenantfs"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-delete",
		Desc:  "Delete a domain name.",
		Usage: "opencli domains-delete <DOMAIN_NAME> [--debug]",
		Run:   del,
	})
}

type delJob struct {
	domain, user, domainID, userID, ctx string
	onion, debug                        bool
	webServer, keyValue                 string
}

func (j *delJob) log(s string) {
	if j.debug {
		fmt.Println(s)
	}
}

func del(args []string) int {
	if len(args) < 1 {
		fmt.Println("Usage: opencli domains-delete <DOMAIN_NAME> [--debug]")
		return 1
	}
	j := &delJob{domain: args[0]}
	j.debug = cli.Arg(args, 1) == "--debug"
	j.preflight()
	j.getUserInfo()
	j.deleteDomain()
	return 0
}

func (j *delJob) preflight() {
	j.log("Checking owner for domain " + j.domain)
	j.user = ownerOf(j.domain)
	if j.user == "" {
		cli.Fail("❌ No owner found for '%s'. Ensure the domain is assigned and MariaDB is running.", j.domain)
	}
	out, _ := db.Plain("SELECT domain_id FROM domains WHERE domain_url = '"+j.domain+"';", "-N", "-s").Out()
	j.domainID = out
	if j.domainID == "" {
		cli.Fail("❌ Domain ID not found for '%s'. Ensure it exists and MariaDB is running.", j.domain)
	}
	if addOnionRe.MatchString(j.domain) {
		j.onion = true
		j.log("Detected .onion address — Tor website will be removed from configuration.")
	}
}

func (j *delJob) getUserInfo() {
	out, _ := db.Plain("SELECT id, server FROM users WHERE username = '"+j.user+"';", "-s").Out()
	f := strings.Fields(out)
	if len(f) > 0 {
		j.userID = f[0]
	}
	if len(f) > 1 {
		j.ctx = f[1]
	}
	if j.userID == "" {
		cli.Fail("FATAL ERROR: user %s does not exist.", j.user)
	}
}

func (j *delJob) deleteDomain() {
	j.log("Removing any websites associated with domain (ID: " + j.domainID + ")")
	db.Plain("DELETE FROM sites WHERE domain_id = '" + j.domainID + "';").Run()
	j.log("Removing " + j.domain + " from the database")
	db.Plain("DELETE from domains where domain_url = '" + j.domain + "';").Run()

	result, _ := db.Plain("SELECT COUNT(*) FROM domains WHERE domain_url = '"+j.domain+"';", "-N").Out()
	if result != "0" {
		j.log("Deleting domain " + j.domain + " failed! Contact administrator to check if the mariadb database is running.")
		fmt.Println("Failed to delete domain " + j.domain)
		return
	}
	j.getWebServer()
	j.vhostDelete()
	sh.Opencli("sentinel", "--action=domains_delete", "--title=Domain deleted", "--message=Domain name: '"+j.domain+"' has been removed from OpenPanel user: '"+j.user+"'.").Quiet().Background()

	j.removeApexDNS()
	if j.onion {
		j.removeOnionFiles()
		j.log("Restarting Tor service..")
		podman.User(j.ctx, "restart", "tor").Quiet().Background()
	} else {
		j.deleteCaddyFiles()
		j.dnsStuff()
	}
	j.keyValue, _ = conf.Raw(conf.Panel, "key")
	j.deleteMailMountpoint()
	if j.keyValue != "" {
		sh.Opencli("email-ratelimit", "--delete-domain="+j.domain).Quiet().Background()
	}
	j.deleteEmails()
	j.deleteFTPAccounts()
	j.clamavList()
	fmt.Println("Domain " + j.domain + " deleted successfully")
}

func (j *delJob) getWebServer() {
	j.log("Checking webserver configuration")
	j.webServer = addWSRe.FindString(envValue(j.ctx, "WEB_SERVER"))
}

func (j *delJob) vhostDelete() {
	j.log("Deleting /etc/" + j.webServer + "/sites-available/" + j.domain + ".conf")
	os.Remove("/home/" + j.ctx + "/docker-data/volumes/" + j.ctx + "_webserver_data/_data/" + j.domain + ".conf")
	j.log("Reloading " + j.webServer + " to apply changes")
	silence(func() { podman.ReloadWebserver(j.ctx, j.webServer) })
}

func (j *delJob) reloadBind() {
	if podman.IsRunning("openpanel_dns") {
		j.log("Reloading BIND DNS to apply changes")
		sh.C("podman", "exec", "openpanel_dns", "rndc", "reconfig").Quiet().Run()
	}
}

func (j *delJob) removeApexDNS() {
	const tldFile = "/etc/openpanel/openpanel/conf/public_suffix_list.dat"
	tld := j.domain[strings.LastIndex(j.domain, ".")+1:]
	tldLower := strings.ToLower(tld)
	update := false
	st, err := os.Stat(tldFile)
	if err != nil {
		j.log("TLD list not found, downloading from IANA...")
		update = true
	} else if time.Since(st.ModTime()) >= 7*24*time.Hour {
		update = true
	}
	if update {
		os.MkdirAll(filepath.Dir(tldFile), 0o777)
		// wget -O truncates the file before it even connects
		if f, err := os.Create(tldFile); err == nil {
			f.Close()
		}
		if !wgetFile("https://publicsuffix.org/list/public_suffix_list.dat", tldFile, 3) {
			j.log("Failed to download TLD list from IANA")
		}
	}
	found := false
	for _, l := range conf.Lines(tldFile) {
		if l == tldLower {
			found = true
			break
		}
	}
	if !found {
		cli.Fail("ERROR: Invalid domain or unrecognized TLD: .%s", tldLower)
	}
	base := j.domain[:strings.LastIndex(j.domain, ".")]
	if strings.Contains(base, ".") {
		second := base[strings.LastIndex(base, ".")+1:]
		apex := second + "." + tld
		sub := strings.TrimSuffix(j.domain, "."+apex)
		j.log("Detected subdomain: " + sub + " of apex domain: " + apex)
		zone := "/etc/bind/zones/" + apex + ".zone"
		if conf.IsFile(zone) {
			j.log("Removing DNS records for subdomain " + sub + " from " + zone)
			var keep []string
			for _, l := range strings.Split(strings.TrimSuffix(conf.Read(zone), "\n"), "\n") {
				if zoneRecordOf(l, j.domain) {
					continue
				}
				keep = append(keep, l)
			}
			writeLines(zone, keep)
			j.reloadBind()
		} else {
			j.log("Zone file for apex domain " + apex + " not found.")
		}
	} else {
		zone := "/etc/bind/zones/" + j.domain + ".zone"
		if conf.IsFile(zone) {
			j.log("Deleting DNS zone " + zone)
			os.RemoveAll(zone)
			j.reloadBind()
		} else {
			j.log("Zone file for domain " + j.domain + " not found.")
		}
	}
}

// zoneRecordOf is sed's /^D[[:space:]]/ and /^D\.[[:space:]]/ deletes
func zoneRecordOf(line, d string) bool {
	for _, p := range []string{d, d + "."} {
		if strings.HasPrefix(line, p) && len(line) > len(p) && strings.ContainsRune(" \t\n\v\f\r", rune(line[len(p)])) {
			return true
		}
	}
	return false
}

func (j *delJob) removeOnionFiles() {
	host := "/home/" + j.ctx + "/tor"
	var dirs []string
	filepath.WalkDir(host, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || !strings.HasSuffix(p, "/hostname") {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasSuffix(l, ".onion") {
				dirs = append(dirs, filepath.Dir(p))
				break
			}
		}
		return nil
	})
	torrc := host + "/torrc"
	for _, dir := range dirs {
		name := filepath.Base(dir)
		var re *regexp.Regexp
		if name == "hidden_service" {
			re = regexp.MustCompile(`^HiddenServiceDir.*/hidden_service$`)
		} else if regexp.MustCompile(`^[0-9]+$`).MatchString(name) {
			re = regexp.MustCompile(`^HiddenServiceDir.*/hidden_service/` + name + `$`)
		} else {
			continue
		}
		b, err := os.ReadFile(torrc)
		if err != nil {
			continue
		}
		s := string(b)
		trail := strings.HasSuffix(s, "\n")
		lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
		var out []string
		for i := 0; i < len(lines); i++ {
			// sed N drops the next line too, on the last line it prints and stops
			if re.MatchString(lines[i]) && i+1 < len(lines) {
				i++
				continue
			}
			out = append(out, lines[i])
		}
		res := strings.Join(out, "\n")
		if trail && len(out) > 0 {
			res += "\n"
		}
		conf.WriteFileKeep(torrc, res, false)
	}
	// each one goes by its bare name from a checked cwd, so a swapped path can't send rm elsewhere
	for _, dir := range dirs {
		if real, err := tenantfs.Resolve(host, filepath.Dir(dir)); err == nil {
			os.RemoveAll(real + "/" + filepath.Base(dir))
		}
	}
}

func (j *delJob) deleteCaddyFiles() {
	j.log("Removing domain from the proxy")
	os.Remove("/etc/openpanel/caddy/domains/" + j.domain + ".conf")
	os.Remove("/etc/openpanel/caddy/suspended_domains/" + j.domain + ".conf")
	if podman.IsRunning("caddy") {
		j.log("Caddy is running, reloading configuration")
		sh.C("podman", "exec", "caddy", "sh", "-c", "caddy validate && caddy reload ").Quiet().Background()
	}
	os.RemoveAll("/var/log/caddy/domlogs/" + j.domain)
	os.Remove("/var/log/caddy/stats/" + j.user + "/" + j.domain + ".html")
	ents, _ := os.ReadDir("/var/log/caddy/coraza_waf")
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), j.domain+".log") {
			os.Remove("/var/log/caddy/coraza_waf/" + e.Name())
		}
	}
	os.RemoveAll("/etc/openpanel/caddy/ssl/custom/" + j.domain)
}

func (j *delJob) dnsStuff() {
	line := ""
	for _, l := range conf.Lines(conf.Panel) {
		if strings.HasPrefix(l, "enabled_modules=") {
			line += l + "\n"
		}
	}
	if !strings.Contains(line, "dns") {
		return
	}
	// zone file
	zone := "/etc/bind/zones/" + j.domain + ".zone"
	if backups.MountRemove(j.ctx, "/backup/dns/"+j.domain+".zone") {
		backups.ServiceReload(j.user, j.ctx)
	}
	if conf.IsFile(zone) {
		j.log("Removing DNS zone file: " + zone)
		os.Remove(zone)
		if podman.IsRunning("openpanel_dns") {
			j.log("DNS service is running, reloading the zones")
			sh.C("podman", "exec", "openpanel_dns", "rndc", "reconfig").Quiet().Run()
		}
	} else {
		j.log("DNS zone file does not exist: " + zone)
	}
	// named.conf.local
	re := regexp.MustCompile(`zone "` + sedRegexLiteral(j.domain) + `"`)
	if re.MatchString(conf.Read(addNamedConfLocal)) {
		j.log("Removing zone information from the server.")
		var keep []string
		for _, l := range strings.Split(strings.TrimSuffix(conf.Read(addNamedConfLocal), "\n"), "\n") {
			if !re.MatchString(l) {
				keep = append(keep, l)
			}
		}
		writeLines(addNamedConfLocal, keep)
	}
	j.slaves()
}

func (j *delJob) slaves() {
	lines := conf.Lines(addBindConfig)
	transfer, ok1 := slaveList(lines, slaveAllowRe)
	notify, ok2 := slaveList(lines, slaveNotifyRe)
	if !ok1 || !ok2 {
		return
	}
	joined := " " + strings.Join(splitSemi(notify), " ") + " "
	for _, ip := range splitSemi(transfer) {
		if ip == "" || !strings.Contains(joined, " "+ip+" ") {
			continue
		}
		fmt.Println("Notifying Slave DNS server (" + ip + ") to remove zone for domain " + j.domain)
		d := j.domain
		s1 := "\t    if grep -q \"" + d + ".zone\" /etc/bind/named.conf.local; then\n" +
			"\t        sed -i \"/zone \\\"" + d + "\\\" {/,/};/d\" /etc/bind/named.conf.local\n" +
			"\t\t\trm -f \"/etc/bind/zones/" + d + ".zone\"\n" +
			"\t        echo \"Zone " + d + " deleted from slave server.\"\n\t    fi\n"
		s2 := "\t    podman exec openpanel_dns rndc reconfig >/dev/null 2>&1\n"
		for _, s := range []string{s1, s2} {
			sh.C("ssh", "-q", "-o", "LogLevel=ERROR", "-o", "ConnectTimeout=5", "-T", "root@"+ip).Stdin(s).Timeout(5 * time.Second).Quiet().Run()
		}
	}
}

func (j *delJob) storeIn() string {
	var vals []string
	for _, l := range conf.Lines(conf.AdminINI) {
		if strings.HasPrefix(l, "email_storage_location=") {
			vals = append(vals, l[len("email_storage_location="):])
		}
	}
	return text.Xargs(strings.Join(vals, "\n"))
}

func (j *delJob) deleteMailMountpoint() {
	if j.keyValue == "" || !conf.IsFile(addMailCompose) {
		return
	}
	storeIn := j.storeIn()
	// only this domain's link, the other domains keep theirs
	os.Remove("/home/" + j.ctx + "/docker-data/volumes/" + j.ctx + "_mail_data/_data/" + j.domain)
	j.deleteDKIM()
	backups.MountRemove(j.ctx, "/backup/emails/"+j.domain)
	backups.ServiceReload(j.user, j.ctx)
	if strings.HasPrefix(storeIn, "/") {
		return
	}
	remove := "/home/" + j.user + "/mail/" + j.domain + "/:/var/mail/" + j.domain + "/"
	j.log("Removing volume: " + remove + " from mailserver")
	// the pattern is built the way the sed was, with the extra "  - " in it, so it never matches a real volume line
	vre := regexp.MustCompile(`^\s*-   - ` + sedRegexLiteral(remove))
	b, _ := os.ReadFile(addMailCompose)
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	nonSpace := regexp.MustCompile(`^[^[:space:]]`)
	var out []string
	inMail, inVol := false, false
	for _, l := range lines {
		if inMail && nonSpace.MatchString(l) {
			inMail, inVol = false, false
		} else if inVol && nonSpace.MatchString(l) {
			inVol = false
		}
		if !inMail && strings.HasPrefix(l, "  mailserver:") {
			inMail = true
			out = append(out, l)
			continue
		}
		if inMail && !inVol && strings.HasPrefix(l, "    volumes:") {
			inVol = true
			out = append(out, l)
			continue
		}
		if inVol && vre.MatchString(l) {
			continue
		}
		out = append(out, l)
	}
	writeLines(addMailCompose, out)
	j.log("Reloading mailserver to apply changes")
	sh.C("podman-compose", "up", "-d", "--force-recreate", "mailserver").Dir("/usr/local/mail/openmail/").Quiet().Background()
}

func (j *delJob) deleteDKIM() {
	dir := "/usr/local/mail/openmail/docker-data/dms/config/opendkim"
	if !conf.IsDir(dir + "/keys/" + j.domain) {
		return
	}
	j.log("Removing DKIM keys for the domain")
	os.RemoveAll(dir + "/keys/" + j.domain)
	for _, table := range []string{"KeyTable", "SigningTable"} {
		p := dir + "/" + table
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var buf bytes.Buffer
		for _, l := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
			first := ""
			if f := strings.Fields(l); len(f) > 0 {
				first = f[0]
			}
			drop := first == "*@"+j.domain
			if i := strings.Index(first, "._domainkey."); i >= 0 && first[i+12:] == j.domain {
				drop = true
			}
			if !drop {
				buf.WriteString(l + "\n")
			}
		}
		conf.WriteInPlace(p, buf.String())
	}
}

func (j *delJob) deleteEmails() {
	dom := strings.ToLower(j.domain)
	var emails []string
	j.log("Removing email accounts for this domain")
	list, _ := sh.Opencli("email-setup", "email", "list").NoErr().Out()
	for _, l := range strings.Split(list, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "*" {
			_, d, _ := strings.Cut(strings.ToLower(f[1]), "@")
			if d == dom {
				emails = append(emails, f[1])
			}
		}
	}
	if len(emails) > 0 {
		sh.Opencli(append([]string{"email-setup", "email", "del", "-y"}, emails...)...).Quiet().Run()
	}

	aliasesFile := conf.UsersDir + "/" + j.user + "/aliases.yml"
	if conf.IsFile(aliasesFile) {
		var aliases []string
		j.log("Removing email aliases for this domain")
		for _, l := range readLoopLines(aliasesFile) {
			f := strings.Fields(l)
			if len(f) < 2 {
				continue
			}
			if strings.HasSuffix(f[1], "@"+j.domain) {
				aliases = append(aliases, f[1])
			}
		}
		if len(aliases) > 0 {
			sh.Opencli(append([]string{"email-setup", "alias", "del"}, aliases...)...).Quiet().Background()
		}
	}

	regexFile := "/usr/local/mail/openmail/docker-data/dms/config/postfix-regex.cf"
	if conf.IsFile(regexFile) {
		j.log("Removing default email address for this domain")
		var buf bytes.Buffer
		changed := false
		for _, l := range readLoopLines(regexFile) {
			f := strings.Fields(l)
			if len(f) == 0 {
				continue
			}
			target := ""
			if len(f) > 1 {
				// read keeps the original spacing of the rest of the line
				rest := strings.TrimLeft(l, " \t")
				rest = strings.TrimLeft(rest[len(f[0]):], " \t")
				target = strings.TrimRight(rest, " \t")
			}
			if strings.ToLower(f[0]) == "/*@"+strings.ToLower(j.domain)+"/" {
				j.log("Removing: " + f[0] + " -> " + target)
				changed = true
				continue
			}
			buf.WriteString(f[0] + " " + target + "\n")
		}
		if changed {
			// mktemp files are 0600 and mv keeps that
			if tmp, err := os.CreateTemp("", "tmp."); err == nil {
				tmp.WriteString(buf.String())
				tmp.Close()
				if os.Rename(tmp.Name(), regexFile) != nil {
					sh.C("mv", tmp.Name(), regexFile).Quiet().Run()
				}
				sh.Opencli("email-default_sync").Run()
			}
		}
	}
}

func (j *delJob) deleteFTPAccounts() {
	file := "/etc/openpanel/ftp/users/" + j.ctx + "/users.list"
	if conf.IsFile(file) {
		j.log("Removing FTP accounts for domain: " + j.domain)
		for _, l := range readLoopLines(file) {
			acc, _, _ := strings.Cut(l, "|")
			if !strings.HasSuffix(acc, "@"+j.domain) {
				continue
			}
			re := regexp.MustCompile(`^` + sedRegexLiteral(acc))
			var keep []string
			for _, cur := range strings.Split(strings.TrimSuffix(conf.Read(file), "\n"), "\n") {
				if !re.MatchString(cur) {
					keep = append(keep, cur)
				}
			}
			writeLines(file, keep)
			sh.C("podman", "exec", "openadmin_ftp", "sh", "-c", "deluser "+acc).Quiet().Background()
		}
	}
	if sh.C("podman", "exec", "openadmin_ftp", "getent", "group", j.ctx).Quiet().OK() {
		sh.C("podman", "exec", "openadmin_ftp", "delgroup", j.ctx).Quiet().Run()
	}
}

func (j *delJob) clamavList() {
	list := "/etc/openpanel/clamav/domains.list"
	if !conf.IsFile(list) {
		return
	}
	path := "/home/" + j.user + "/" + j.domain
	j.log("ClamAV Upload Scanner is enabled - Removing " + path + " for monitoring")
	re := regexp.MustCompile(sedRegexLiteral(path))
	var keep []string
	for _, l := range strings.Split(strings.TrimSuffix(conf.Read(list), "\n"), "\n") {
		if !re.MatchString(l) {
			keep = append(keep, l)
		}
	}
	writeLines(list, keep)
}
