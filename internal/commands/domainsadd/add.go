// ##############################################################################
// Script Name: internal/commands/domainsadd/add.go
// Description: Add a domain name for user.
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

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/phplisten"
	"opencli/internal/sh"
	"opencli/internal/tenantfs"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-add",
		Desc:  "Add a domain name for user.",
		Usage: "opencli domains-add <DOMAIN_NAME> <USERNAME> [--docroot DOCUMENT_ROOT] [--php_version N.N] [--skip_caddy] [--skip_vhost] [--skip_containers] [--skip_dns] [--skip-sentinel] [--debug] [--hs_ed25519_public_key KEY --hs_ed25519_secret_key KEY]",
		Run:   add,
	})
}

const (
	addNamedConfLocal = "/etc/bind/named.conf.local"
	addBindConfig     = "/etc/bind/named.conf.options"
	addZoneDir        = "/etc/bind/zones/"
	addForbiddenFile  = "/etc/openpanel/openpanel/conf/domain_restriction.txt"
	addTLDFile        = "/etc/openpanel/openpanel/conf/public_suffix_list.dat"
	addMailCompose    = "/usr/local/mail/openmail/compose.yml"
)

type addJob struct {
	domain, user, ctx, ctxUID, userID string
	docroot, php, ip, dedicated, apex string
	isSub, onion                      bool
	ipv4                              bool
	webServer, phpListen              string
	useParent                         bool
	enterprise                        string
	ns1, ns2, ns3, ns4, rpemail       string
	permitSub, modules                string
	hsPub, hsSec                      string
	hsRoot, hsPubPath, hsSecPath      string

	debug                                                   bool
	skipVhost, skipCaddy, skipDNS, skipContainers, skipNote bool
}

var addErrMu sync.Mutex

func (j *addJob) log(msg string) {
	if j.debug {
		addErrMu.Lock()
		fmt.Fprintln(os.Stderr, msg)
		addErrMu.Unlock()
	}
}

func (j *addJob) die(msg string) {
	fmt.Fprintln(os.Stderr, "FATAL ERROR: "+msg)
	cli.Exit(1)
}

func (j *addJob) err(msg string) {
	fmt.Fprintln(os.Stderr, "ERROR: "+msg)
	cli.Exit(1)
}

func (j *addJob) warn(msg string) { fmt.Fprintln(os.Stderr, "WARNING: "+msg) }

var (
	addDomainRe  = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+(xn--[a-z0-9-]+|[a-zA-Z]{2,})$`)
	addOnionRe   = regexp.MustCompile(`^[a-zA-Z0-9]{16}\.onion$`)
	addDocrootRe = regexp.MustCompile(`^/var/www/html/[A-Za-z0-9._/-]*$`)
	addPHPRe     = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
	addWSRe      = regexp.MustCompile(`nginx|openresty|apache|openlitespeed|litespeed|caddy`)
)

func add(args []string) int {
	j := &addJob{}
	j.parseArgs(args)
	j.domain = idnEncode(j.domain)
	j.validateDomainFormat()
	j.detectApexOrOnion()
	j.readConfigFile()
	j.checkDomainNotTaken()
	j.checkForbidden()
	j.checkSystemDomains()
	j.checkApexOwnership()
	j.getContext()
	j.checkCommunityLimit()
	j.checkDomainsLimit()
	j.getPHPVersion()
	j.validateDocroot()
	j.verifyOnionFiles()

	j.getServerIP()
	j.getWebServer()

	j.insertDomain()
	j.clearOldDefaultAddress()

	j.makeDocrootDirs()

	j.runParallel()

	if !j.skipNote {
		sh.Opencli("sentinel", "--action=domains_add", "--title=Domain added", "--message=Domain name: '"+j.domain+"' has been added to OpenPanel user: '"+j.user+"'.").Quiet().Background()
	}
	fmt.Println("Domain " + j.domain + " added successfully")
	return 0
}

func (j *addJob) parseArgs(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: opencli domains-add <DOMAIN_NAME> <USERNAME> [--docroot /var/www/html/...] [--php_version N.N] [--skip_caddy] [--skip_vhost] [--skip_dns] [--skip_containers] [--skip-sentinel] [--hs_ed25519_public_key KEY --hs_ed25519_secret_key KEY] [--debug]")
		cli.Exit(1)
	}
	j.domain, j.user = args[0], args[1]
	rest := args[2:]
	for len(rest) > 0 {
		a := rest[0]
		switch a {
		case "--debug":
			j.debug = true
		case "--skip_caddy":
			j.skipCaddy = true
		case "--skip_vhost":
			j.skipVhost = true
		case "--skip_dns":
			j.skipDNS = true
		case "--skip-sentinel":
			j.skipNote = true
		case "--skip_containers":
			j.skipContainers = true
		case "--docroot", "--php_version", "--hs_ed25519_public_key", "--hs_ed25519_secret_key":
			v := ""
			if len(rest) > 1 {
				v = rest[1]
			}
			if v == "" {
				j.die("Missing value for " + a)
			}
			switch a {
			case "--docroot":
				j.docroot = v
			case "--php_version":
				j.php = v
				if !addPHPRe.MatchString(v) {
					j.die("Invalid PHP version format '" + v + "'. Expected: N.N (e.g. 8.2)")
				}
			case "--hs_ed25519_public_key":
				j.hsPub = v
			default:
				j.hsSec = v
			}
			rest = rest[1:]
		}
		rest = rest[1:]
	}
}

func (j *addJob) validateDomainFormat() {
	if !addDomainRe.MatchString(j.domain) {
		j.die("Invalid domain name: " + j.domain)
	}
}

func (j *addJob) isModule(m string) bool {
	return strings.Contains(","+j.modules+",", ","+m+",")
}

func (j *addJob) refreshTLD() {
	os.MkdirAll(filepath.Dir(addTLDFile), 0o777)
	if wgetFile("https://publicsuffix.org/list/public_suffix_list.dat", addTLDFile+".tmp", 3) {
		os.Rename(addTLDFile+".tmp", addTLDFile)
	} else {
		j.log("Failed to refresh TLD list; using existing copy")
		os.Remove(addTLDFile + ".tmp")
	}
}

func (j *addJob) detectApexOrOnion() {
	if addOnionRe.MatchString(j.domain) {
		j.onion = true
		j.log(".onion address — Tor will be configured")
		return
	}
	lower := strings.ToLower(j.domain)
	match := lower
	if strings.Contains(lower, "xn--") {
		match = idnDecode(lower)
		j.log("IDN domain: matching PSL as '" + match + "'")
	}
	st, err := os.Stat(addTLDFile)
	if err != nil || time.Since(st.ModTime()) >= 7*24*time.Hour {
		j.refreshTLD()
	}
	matched, maxLen := "", 0
	for _, l := range conf.Lines(addTLDFile) {
		if strings.HasPrefix(l, "//") || l == "" {
			continue
		}
		if strings.HasSuffix("."+match, "."+l) {
			if n := len([]rune(l)); n > maxLen {
				matched, maxLen = l, n
			}
		}
	}
	if match == matched {
		j.err("'" + lower + "' is a public suffix and cannot be used as a domain.")
	}
	if matched == "" {
		j.err("Invalid domain or unrecognized TLD for '" + j.domain + "'")
	}
	j.log("Detected public suffix: ." + matched)
	registrable := strings.TrimSuffix(match, "."+matched)
	sld := registrable
	if i := strings.LastIndex(registrable, "."); i >= 0 {
		sld = registrable[i+1:]
	}
	j.apex = sld + "." + matched
	if match != lower {
		j.apex = idnEncode(j.apex)
	}
	if lower != j.apex {
		j.isSub = true
		j.log("Domain '" + lower + "' is a subdomain of '" + j.apex + "'")
	}
}

func (j *addJob) readConfigFile() {
	b, _ := os.ReadFile(conf.Panel)
	s := string(b)
	// read skips a last line without newline
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[:i+1]
	} else {
		s = ""
	}
	for _, l := range strings.Split(s, "\n") {
		key, value, _ := strings.Cut(l, "=")
		if strings.HasPrefix(strings.TrimLeft(key, " \t"), "#") || key == "" {
			continue
		}
		switch key {
		case "key":
			j.enterprise = value
		case "ns1":
			j.ns1 = value
		case "ns2":
			j.ns2 = value
		case "ns3":
			j.ns3 = value
		case "ns4":
			j.ns4 = value
		case "rpemail":
			j.rpemail = value
		case "permit_subdomain_sharing":
			j.permitSub = value
		case "enabled_modules":
			j.modules = value
		}
	}
}

func (j *addJob) checkDomainNotTaken() {
	j.log("Checking if domain already exists in database")
	out, _ := sh.Opencli("domains-whoowns", j.domain).Out()
	if !strings.Contains(out, "not found in the database.") {
		j.err("Domain " + j.domain + " already exists.")
	}
}

func (j *addJob) checkForbidden() {
	b, err := os.ReadFile(addForbiddenFile)
	if err != nil {
		return
	}
	j.log("Checking domain against forbidden list")
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r", ""), "\n") {
		if strings.EqualFold(l, j.domain) {
			j.err(j.domain + " is a forbidden domain.")
		}
	}
}

func (j *addJob) checkSystemDomains() {
	j.log("Checking domain against system domains")
	re, err := regexp.Compile(`(?m)^\s*` + j.domain + `\s*\{`)
	if err == nil && re.MatchString(conf.Read(conf.Caddyfile)) {
		j.err(j.domain + " is already configured in Caddy.")
	}
}

func (j *addJob) checkApexOwnership() {
	if !j.isSub {
		return
	}
	existing := ownerOf(j.apex)
	if existing == "" {
		j.log("Apex domain " + j.apex + " not found on this server")
		return
	}
	if existing == j.user {
		j.log("User " + j.user + " already owns apex " + j.apex + " — adding subdomain")
		j.useParent = true
	} else if j.permitSub == "yes" {
		j.warn("Another user owns " + j.apex + " — adding as separate addon domain")
	} else {
		j.err("Another user owns " + j.apex + " — cannot add subdomain " + j.domain + " when 'permit_subdomain_sharing' is disabled.")
	}
}

func (j *addJob) getContext() {
	j.log("Fetching user context from database")
	row, err := db.Plain("SELECT id, server FROM users WHERE username='"+db.Escape(j.user)+"' LIMIT 1;", "-s", "-N").NoErr().Out()
	if err != nil {
		j.die("Database query failed")
	}
	f := strings.Fields(row)
	if len(f) > 0 {
		j.userID = f[0]
	}
	if len(f) > 1 {
		j.ctx = strings.Join(f[1:], " ")
		// read keeps the original spacing of the rest, trailing blanks trimmed
		if i := strings.IndexAny(row, " \t"); i >= 0 {
			j.ctx = strings.TrimRight(strings.TrimLeft(row[i:], " \t"), " \t")
		}
	}
	if j.userID == "" || j.ctx == "" {
		j.die("Missing user ID or context for user " + j.user)
	}
}

func (j *addJob) checkCommunityLimit() {
	if j.enterprise != "" {
		return
	}
	total, _ := db.Plain("SELECT COUNT(*) FROM domains;", "-s", "-N").NoErr().Out()
	j.log("Community edition: " + total + " of 50 domains used")
	if text.Atoi(total) >= 50 {
		j.err("Community edition is limited to 50 domains. Upgrade to Enterprise for unlimited domains.")
	}
}

func (j *addJob) checkDomainsLimit() {
	if j.useParent {
		j.log("Adding subdomain of existing domain — does not count toward limit")
		return
	}
	out, _ := db.Plain("\n        SELECT p.domains_limit\n        FROM users u\n        JOIN plans p ON u.plan_id = p.id\n        WHERE u.username='"+db.Escape(j.user)+"'\n        LIMIT 1;", "-s", "-N").NoErr().Out()
	limit := text.Atoi(out)
	if out == "" || limit == 0 {
		return
	}
	cnt, _ := db.Plain("\n        SELECT COUNT(*)\n        FROM domains d\n        WHERE d.user_id = '"+j.userID+"'\n        AND NOT EXISTS (\n            SELECT 1 FROM domains d2\n            WHERE d2.user_id = '"+j.userID+"'\n            AND d.domain_url LIKE CONCAT('%.', d2.domain_url)\n        );", "-s", "-N").NoErr().Out()
	j.log("Domains limit: " + out + ", current apex domains: " + cnt)
	if text.Atoi(cnt) >= limit {
		j.err("Domain limit reached. Plan allows " + out + " domain(s), user currently has " + cnt + ".")
	}
}

var addVerRe = regexp.MustCompile(`\d+\.\d+`)

func (j *addJob) getPHPVersion() {
	if j.php != "" {
		return
	}
	out, _ := sh.Opencli("php-default", j.user).Out()
	j.php = strings.Join(addVerRe.FindAllString(out, -1), "\n")
}

func (j *addJob) validateDocroot() {
	if j.docroot != "" {
		if !addDocrootRe.MatchString(j.docroot) || strings.Contains(j.docroot, "..") {
			j.die("Invalid docroot. It must start with /var/www/html/ and only use letters, numbers, dots, dashes, underscores and slashes.")
		}
	} else {
		j.docroot = "/var/www/html/" + j.domain
		j.log("No docroot specified, using " + j.docroot)
	}
}

func (j *addJob) verifyOnionFiles() {
	if !j.onion {
		return
	}
	if j.hsPub == "" || j.hsSec == "" {
		j.die("Both --hs_ed25519_public_key and --hs_ed25519_secret_key are required for .onion domains.")
	}
	for _, k := range []struct{ name, v string }{{"hs_ed25519_public_key", j.hsPub}, {"hs_ed25519_secret_key", j.hsSec}} {
		if !strings.HasPrefix(k.v, "/var/www/html/") {
			j.die("--" + k.name + " must be inside /var/www/html/")
		}
		if strings.Contains("/"+k.v+"/", "/../") {
			j.die("--" + k.name + " can't contain ..")
		}
	}
	j.hsRoot = tenantfs.Volume(j.ctx, "html_data")
	j.hsPubPath = j.hsRoot + "/" + strings.TrimPrefix(j.hsPub, "/var/www/html/")
	j.hsSecPath = j.hsRoot + "/" + strings.TrimPrefix(j.hsSec, "/var/www/html/")
	_, e1 := tenantfs.Read(j.hsRoot, j.hsPubPath)
	if e1 == nil {
		_, e1 = tenantfs.Read(j.hsRoot, j.hsSecPath)
	}
	if e1 != nil {
		j.die("hs_ed25519_public_key or hs_ed25519_secret_key files not found")
	}
}

func (j *addJob) getServerIP() {
	const ipServer = "https://ip.openpanel.com"
	get := func(url string) string {
		if v, ok := curlGet(url, 2*time.Second, "tcp4"); ok {
			return v
		}
		v, _ := wgetGet(url, 2*time.Second)
		return v
	}
	j.ip = get(ipServer)
	if j.ip == "" {
		j.log("ip.openpanel.com unreachable, trying ifconfig.me")
		j.ip = get("https://ifconfig.me")
	}
	if j.ip == "" {
		j.log("Fetching IPv4 from local interface")
		j.ip = ipGlobal(false)
	}
	if j.ip != "" {
		j.ipv4 = true
	} else {
		j.ipv4 = false
		j.log("No IPv4 found, trying IPv6")
		j.ip, _ = curlGet(ipServer, 2*time.Second, "tcp6")
		if j.ip == "" {
			j.ip = ipGlobal(true)
		}
	}
	if j.ip == "" {
		j.die("Unable to determine server IP address")
	}
	jf := conf.UsersDir + "/" + j.user + "/ip.json"
	if conf.IsFile(jf) {
		j.dedicated = ipJSON(jf)
		if j.dedicated != "" {
			j.log("User has dedicated IP: " + j.dedicated)
		}
	}
}

func (j *addJob) getWebServer() {
	j.log("Detecting web server")
	j.webServer = addWSRe.FindString(envValue(j.ctx, "WEB_SERVER"))
	j.phpListen = phplisten.Mode(j.ctx)
	if j.webServer == "nginx" {
		j.ensureNginxConf()
	}
}

const addNginxConf = `user  nginx;
worker_processes  auto;
pid        /var/run/nginx.pid;

events {
    worker_connections  1024;
}

http {
    include       /etc/nginx/mime.types;
    default_type  application/octet-stream;
    sendfile        on;
    keepalive_timeout  65;
    include /etc/nginx/conf.d/*.conf;
}
`

func (j *addJob) ensureNginxConf() {
	p := "/home/" + j.ctx + "/nginx.conf"
	if conf.Exists(p) {
		return
	}
	j.log("Creating default nginx.conf")
	os.WriteFile(p, []byte(addNginxConf), 0o666&^0o022)
}

func (j *addJob) insertDomain() {
	j.log("Inserting domain " + j.domain + " into database")
	sd, sp, sdom := db.Escape(j.docroot), db.Escape(j.php), db.Escape(j.domain)
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(j.userID) {
		j.die("Invalid user_id: " + j.userID)
	}
	if db.Plain("INSERT INTO domains (user_id, docroot, php_version, domain_url) VALUES ('"+j.userID+"', '"+sd+"', '"+sp+"', '"+sdom+"');").NoErr().Run() != nil {
		j.die("Database insert failed for " + j.domain)
	}
	cnt, _ := db.Plain("SELECT COUNT(*) FROM domains WHERE user_id='"+j.userID+"' AND docroot='"+sd+"' AND domain_url='"+sdom+"';", "-s", "-N").NoErr().Out()
	if cnt != "1" {
		j.die("Insert verification failed for " + j.domain)
	}
}

func (j *addJob) clearOldDefaultAddress() {
	file := "/usr/local/mail/openmail/docker-data/dms/config/postfix-regex.cf"
	re := regexp.MustCompile(`(?m)^/\*@` + strings.ReplaceAll(regexp.QuoteMeta(j.domain), `\.`, `[.]`) + `/[[:space:]]`)
	b, err := os.ReadFile(file)
	if err != nil || !re.Match(b) {
		return
	}
	lines := strings.Split(string(b), "\n")
	var keep []string
	for _, l := range lines {
		if !re.MatchString(l) {
			keep = append(keep, l)
		}
	}
	if conf.WriteFileKeep(file, strings.Join(keep, "\n"), false) == nil {
		sh.Opencli("email-default_sync").Run()
	}
}

func (j *addJob) makeDocrootDirs() {
	j.log("Creating docroot directories")
	uid, ok := statUID("/home/" + j.ctx)
	if !ok {
		j.warn("Could not detect UID for " + j.ctx + " — skipping chown")
		return
	}
	j.ctxUID = strconv.Itoa(uid)
	stripped := strings.TrimPrefix(j.docroot, "/var/www/html/")
	htmlRoot := tenantfs.Volume(j.ctx, "html_data")
	siteDir := htmlRoot + "/" + stripped
	dirs := []string{
		"/home/" + j.ctx + "/docker-data/volumes/" + j.ctx + "_webserver_data/_data/",
		"/home/" + j.ctx + "/docker-data/volumes/" + j.ctx + "_html_data",
		htmlRoot,
	}
	for _, d := range dirs {
		os.MkdirAll(d, 0o777)
	}
	// the docroot is picked by the user and may already exist, so it's created from a checked cwd inside the volume
	if tenantfs.Mkdir(htmlRoot, siteDir) != nil {
		j.warn("Docroot " + j.docroot + " is not a real folder inside /var/www/html/ — skipping")
		return
	}
	// https://github.com/stefanpejcic/OpenPanel/discussions/1084
	if strings.Contains(j.webServer, "litespeed") {
		if b, err := os.ReadFile("/etc/openpanel/nginx/default_page.html"); err == nil {
			tenantfs.Write(htmlRoot, siteDir+"/index.html", 0o644, b)
		}
	}
	// only the volume dirs themselves, a recursive walk of the whole volume can hit huge or network-mounted folders
	for _, d := range dirs {
		os.Chown(d, uid, uid)
		if st, err := os.Stat(d); err == nil {
			os.Chmod(d, st.Mode().Perm()|0o020|(st.Mode() & ^fs.ModePerm))
		}
	}
	owner := j.ctxUID + ":" + j.ctxUID
	// parents of a nested docroot are created by root too
	path := htmlRoot
	for _, part := range strings.Split(stripped, "/") {
		if part == "" {
			continue
		}
		path = path + "/" + part
		if path == siteDir {
			break
		}
		tenantfs.Chown(htmlRoot, path, owner)
	}
	tenantfs.ChownR(htmlRoot, siteDir, owner)
	tenantfs.ChmodR(htmlRoot, siteDir, "g+w")
}
