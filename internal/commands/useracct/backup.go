// ##############################################################################
// Script Name: internal/commands/useracct/backup.go
// Description: Creates a full account .tar.gz backup of a single OpenPanel user account.
// Usage: opencli user-backup --account <USER> [--output <DIR>] [--quiet] [--no-throttle]
// Author: Stefan Pejcic
// Created: 01.10.2023
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

package useracct

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

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
		Name:  "user-backup",
		Desc:  "Creates a full account .tar.gz backup of a single OpenPanel user account.",
		Usage: "opencli user-backup --account <USER> [--output <DIR>] [--quiet] [--no-throttle]",
		Docs:  "https://docs.openpanel.com",
		Run:   backup,
	})
}

const backupFormatVersion = "2"

type backupJob struct {
	username, customOutput string
	quiet, throttle        bool
	logFile                string
	warnings               []string
}

func (b *backupJob) log(msg string) {
	line := "[" + time.Now().Format("2006-01-02 15:04:05") + "] " + msg + "\n"
	if !b.quiet {
		fmt.Print(line)
	}
	appendFile(b.logFile, line)
}

func (b *backupJob) warn(msg string) {
	b.warnings = append(b.warnings, msg)
	b.log("[!] " + msg)
}

func (b *backupJob) die(msg string, code int) {
	b.log("[✘] " + msg)
	cli.Exit(code)
}

// slog always prints, --quiet or not
func (b *backupJob) slog(msg string) {
	fmt.Println(msg)
	appendFile(b.logFile, msg+"\n")
}

// lowprio prefixes the heavy steps with nice and ionice unless --no-throttle
func (b *backupJob) lowprio(name string, args ...string) []string {
	if !b.throttle {
		return append([]string{name}, args...)
	}
	p := []string{"nice", "-n19"}
	if sh.Has("ionice") {
		p = append(p, "ionice", "-c2", "-n7")
	}
	return append(append(p, name), args...)
}

func backupQ(q string) string {
	out, _ := db.Cmd(q, "-N", "-s").Out()
	return out
}

func backup(args []string) int {
	start := time.Now()
	b := &backupJob{throttle: true}
	for i := 0; i < len(args); {
		switch args[i] {
		case "--account":
			// shift 2 without a value looped forever, a missing value just ends parsing now
			b.username = cli.Arg(args, i+1)
			i += 2
		case "--output":
			b.customOutput = cli.Arg(args, i+1)
			i += 2
		case "--quiet":
			b.quiet = true
			i++
		case "--no-throttle":
			b.throttle = false
			i++
		default:
			cli.Fail("[ERROR] Unknown option: %s", args[i])
		}
	}
	if b.username == "" {
		cli.Fail("Usage: opencli user-backup --account <USER> [--output <DIR>] [--quiet] [--no-throttle]")
	}
	db.Require()

	// Resolve account identity
	userID := backupQ("SELECT id FROM users WHERE username = '" + db.Escape(b.username) + "';")
	if userID == "" {
		cli.Fail("[ERROR] No user found: %s", b.username)
	}
	ctx := backupQ("SELECT server FROM users WHERE username = '" + db.Escape(b.username) + "';")
	if ctx == "" {
		cli.Fail("[ERROR] Could not resolve context (server) for '%s'", b.username)
	}
	planRow := text.FirstLine(backupQ(`
    SELECT p.id, p.name, p.feature_set
    FROM plans p JOIN users u ON p.id = u.plan_id
    WHERE u.id = ` + userID + `;`))
	pf := strings.SplitN(planRow, "\t", 3)
	for len(pf) < 3 {
		pf = append(pf, "")
	}
	planID, planName, featureSet := pf[0], pf[1], pf[2]

	sUID, sGID := passwdField(ctx, 2), passwdField(ctx, 3)
	if sUID == "" || sGID == "" {
		if conf.IsDir("/home/" + ctx) {
			sUID, sGID = statUID("/home/"+ctx), statGID("/home/"+ctx)
		} else {
			cli.Failerr("%s not found in /etc/passwd and /home/%s does not exist", ctx, ctx)
		}
	}

	domainIDs := backupQ("SELECT domain_id FROM domains WHERE user_id = " + userID + ";")
	siteCount := "0"
	if domainIDs != "" {
		list := strings.Join(strings.Split(domainIDs, "\n"), ",")
		out, err := db.Cmd("SELECT COUNT(*) FROM sites WHERE domain_id IN ("+list+");", "-N", "-s").NoErr().Out()
		if err != nil {
			out = strings.TrimPrefix(out+"\n0", "\n")
		}
		siteCount = out
	}

	// External mail fires only when admin set email_storage_location to a path outside /home.  Default for <1.7.5: mail lives in /home/CONTEXT/mail/
	storeIn, _ := conf.Raw(conf.AdminINI, "email_storage_location")
	storeIn = text.Xargs(storeIn)
	mailExternal, mailExternalSize := "", "n/a"
	if strings.HasPrefix(storeIn, "/") && conf.IsDir(storeIn) {
		mailExternal = storeIn
		mailExternalSize = duField("-sh", mailExternal)
	}

	// Destination
	destDir := b.customOutput
	if destDir == "" {
		destDir = "/home/" + ctx + "/docker-data/volumes/" + ctx + "_html_data/_data/_backups"
	}
	// the default _backups folder sits in the user's own volume, so the archive is only written from a cwd checked to be inside it
	destRoot := destDir
	if b.customOutput == "" {
		destRoot = tenantfs.Volume(ctx, "html_data")
	}
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	archiveName := b.username + "_" + timestamp + ".tar.gz"
	archive := destDir + "/" + archiveName

	baseName := filepath.Base(b.username)
	logDir := "/var/log/openpanel/admin/backups"
	os.MkdirAll(logDir, 0o777)
	b.logFile = logDir + "/" + baseName + "_backup_" + timestamp + ".log"

	diskEstimate, diskFree, diskSource := int64(0), int64(0), "unknown"

	b.log(fmt.Sprintf("Backup started  log: %s  (PID: %d)", b.logFile, os.Getpid()))
	or := func(s, d string) string {
		if s == "" {
			return d
		}
		return s
	}
	b.log("Account: " + b.username + "  Context: " + ctx + "  UID:" + or(sUID, "?") + " GID:" + or(sGID, "?") + "  Plan: " + or(planName, "none"))

	// ensure_dest_dir
	if b.customOutput == "" {
		if tenantfs.Mkdir(destRoot, destDir) != nil {
			b.die("Backup folder "+destDir+" is not a real folder inside the user's volume.", 1)
		}
		// mkdir runs as root, give the folder the volume's owner or the account's own php and ftp can't write there
		if tenantfs.Chown(destRoot, destDir, statUID(destRoot)+":"+statGID(destRoot)) != nil {
			b.warn("Could not chown " + destDir + " to the account (non-fatal).")
		}
	} else if err := os.MkdirAll(destDir, 0o777); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: cannot create directory '%s': %s\n", destDir, strings.TrimPrefix(err.Error(), "mkdir "+destDir+": "))
		b.die("Cannot create backup folder "+destDir, 1)
	}

	// check_disk_space
	b.log("Checking disk space ...")
	func() {
		usedKB := int64(0)
		if sh.Has("repquota") {
			for _, l := range strings.Split(sh.Str("repquota", "-au"), "\n") {
				if text.Field(l, 1) == ctx {
					if n, err := strconv.ParseInt(text.Field(l, 3), 10, 64); err == nil && text.IsInt(text.Field(l, 3)) && n > 0 {
						usedKB, diskSource = n, "repquota"
					}
					break
				}
			}
		}
		if usedKB == 0 && conf.IsDir("/home/"+ctx) {
			n, err := strconv.ParseInt(duField("-sk", "--exclude=docker-data/volumes/"+ctx+"_html_data/_data/_backups", "/home/"+ctx), 10, 64)
			if err != nil {
				n = 0
			}
			usedKB, diskSource = n, "du"
		}
		if usedKB == 0 {
			b.warn("Cannot determine disk usage for " + ctx + " — disk space check skipped.")
			return
		}
		diskEstimate = usedKB / 1024
		free, ok := freeKB(destDir)
		if !ok {
			b.warn("Cannot read free space at " + destDir + " — disk space check skipped.")
			return
		}
		diskFree = free / 1024
		b.log(fmt.Sprintf("  Source size  (~%d MB via %s)", diskEstimate, diskSource))
		b.log(fmt.Sprintf("  Free at dest (~%d MB at %s)", diskFree, destDir))
		// abort if free < 1/3 of source -- compressed archives typically reach 30-70% of source, so even a heavily compressed one would fill the disk
		if free < usedKB/3 {
			b.die(fmt.Sprintf("Not enough disk space. Source ~%d MB, free ~%d MB at %s. Aborting to avoid a partial archive.", diskEstimate, diskFree, filepath.Dir(archive)), 1)
		}
		// Warn threshold: free < source (uncommon but possible for binary-heavy accounts)
		if free < usedKB {
			b.warn(fmt.Sprintf("Low disk space: source ~%d MB but only ~%d MB free at %s. Compressed archive is usually smaller — proceeding, but monitor progress.", diskEstimate, diskFree, filepath.Dir(archive)))
		} else {
			b.log("  Disk space OK.")
		}
	}()

	// Staging area: small metadata only; home is streamed live
	stage, err := os.MkdirTemp("/tmp", "opbackup_"+baseName+".")
	if err != nil {
		b.die("Cannot create staging directory", 1)
	}
	ok := false
	defer func() {
		if !ok {
			if d, err := tenantfs.Resolve(destRoot, destDir); err == nil {
				os.Remove(d + "/" + archiveName)
			}
		}
		os.RemoveAll(stage)
	}()
	for _, d := range []string{"db", "system", "features", "core", "ftp", "caddy/domains", "caddy/ssl/acme", "caddy/ssl/custom", "caddy/domlogs", "caddy/waf", "caddy/stats", "caddy/suspended", "bind/zones", "docker", "emails/dkim"} {
		os.MkdirAll(stage+"/"+d, 0o777)
	}

	// --- manifest ---
	b.log("Writing manifest ...")
	sourceIP := serverIP()
	os.WriteFile(stage+"/manifest.env", []byte(`BACKUP_FORMAT_VERSION="`+backupFormatVersion+`"
USERNAME="`+b.username+`"
CONTEXT="`+ctx+`"
PLAN_NAME="`+planName+`"
PLAN_FEATURE_SET="`+featureSet+`"
SOURCE_UID="`+sUID+`"
SOURCE_GID="`+sGID+`"
SOURCE_IP="`+sourceIP+`"
CREATED_AT="`+time.Now().UTC().Format("2006-01-02T15:04:05Z")+`"
`), 0o666)

	// --- panel DB ---
	b.log("Exporting panel DB rows ...")
	planTSV, _ := db.Cmd(`
  SELECT name, description, domains_limit, websites_limit, email_limit, ftp_limit,
         disk_limit, inodes_limit, db_limit, cpu, ram, bandwidth, feature_set,
         max_email_quota, max_hourly_email
  FROM plans WHERE id = `+planID+`;`, "-N", "-s").Raw()
	os.WriteFile(stage+"/db/plan.sql", []byte(awkInsert(planTSV,
		"INSERT INTO plans (name, description, domains_limit, websites_limit, email_limit, ftp_limit, disk_limit, inodes_limit, db_limit, cpu, ram, bandwidth, feature_set, max_email_quota, max_hourly_email) VALUES",
		"('%s', '%s', %s, %s, %s, %s, '%s', %s, %s, '%s', '%s', %s, '%s', '%s', %s),\n", 15)), 0o666)

	userTSV, _ := db.Cmd(`
  SELECT username, password, email, owner, user_domains, twofa_enabled, otp_secret,
         plan, registered_date, server, plan_id
  FROM users WHERE id = `+userID+`;`, "-N", "-s").Raw()
	os.WriteFile(stage+"/db/user.sql", []byte(awkInsert(userTSV,
		"INSERT INTO users (username, password, email, owner, user_domains, twofa_enabled, otp_secret, plan, registered_date, server, plan_id) VALUES",
		"('%s', '%s', '%s', '%s', '%s', %s, '%s', '%s', '%s', '%s', %s),\n", 11)), 0o666)

	if domainIDs != "" {
		list := strings.Join(strings.Split(domainIDs, "\n"), ",")
		// domain_url is included so restore can re-resolve the site's domain_id by name, since domains-add assigns a fresh auto-increment ID that rarely matches this server's
		sitesTSV, _ := db.Cmd(`
      SELECT s.site_name, s.domain_id, s.admin_email, s.version, s.created_date, s.type, s.ports, s.path, s.container, d.domain_url
      FROM sites s JOIN domains d ON s.domain_id = d.domain_id
      WHERE s.domain_id IN (`+list+`);`, "-N", "-s").Raw()
		if sitesTSV != "" {
			os.WriteFile(stage+"/db/sites.sql", []byte(awkInsert(sitesTSV,
				"-- sites export (site_name, domain_id, admin_email, version, created_date, type, ports, path, container, domain_url)",
				"('%s', %s, '%s', '%s', '%s', '%s', %s, '%s', '%s', '%s'),\n", 10)), 0o666)
		}
	}

	// per-user tables, ddl included since a fresh panel only creates them on first use
	exportUserTables(stage+"/db", userID, backupQ, func(q string) string {
		out, _ := db.Cmd(q, "-N", "-s", "-r").Raw()
		return out
	})

	// domains.list: domain <tab> docroot <tab> php_version, used by restore
	var domains []string
	allDomains := sh.Opencli("domains-user", b.username, "--docroot", "--php_version").NoErr().Str()
	if !strings.Contains(allDomains, "No domains found for user") && allDomains != "" {
		os.WriteFile(stage+"/db/domains.list", []byte(allDomains+"\n"), 0o666)
		for _, l := range strings.Split(allDomains, "\n") {
			if d := firstWord(l); d != "" {
				domains = append(domains, d)
			}
		}
	}
	domainListStr := "(none)"
	if len(domains) > 0 {
		domainListStr = strings.Join(domains, " ")
	}

	// --- system user ---
	// todo: fails in container, do we need it for restore?
	b.log("Capturing system user (" + ctx + ") ...")
	captureSystemUser(ctx, stage+"/system")

	// --- feature set ---
	if !conf.IsFile("/home/" + ctx + "/features.txt") {
		f := "/etc/openpanel/openpanel/features/" + featureSet + ".txt"
		if featureSet != "" && conf.IsFile(f) && cpA(f, stage+"/features/") == nil {
			b.log("Collected feature set for the plan")
		}
	}

	// --- per-user core config ---
	coreDir := conf.UsersDir + "/" + b.username
	if conf.IsDir(coreDir) && cpAContents(coreDir, stage+"/core") == nil {
		b.log("Collected system files for user")
	}

	// --- FTP ---
	ftpCount := "0"
	ftpDir := "/etc/openpanel/ftp/users/" + ctx
	if conf.IsDir(ftpDir) {
		if cpAContents(ftpDir, stage+"/ftp") == nil {
			b.log("Collected FTP accounts for user")
		}
		if conf.IsFile(ftpDir + "/users.list") {
			n := 0
			for _, l := range conf.Lines(ftpDir + "/users.list") {
				if l != "" {
					n++
				}
			}
			// grep -c printing 0 exits 1, so the || echo 0 adds a second 0
			ftpCount = strconv.Itoa(n)
			if n == 0 {
				ftpCount = "0\n0"
			}
		}
	}

	// --- per-domain caddy + bind assets ---
	if conf.IsFile(stage + "/db/domains.list") {
		b.log("Collecting per-domain assets...")
		lines := strings.Split(strings.TrimSuffix(conf.Read(stage+"/db/domains.list"), "\n"), "\n")
		dkimDir := "/usr/local/mail/openmail/docker-data/dms/config/opendkim"
		for i, l := range lines {
			d := firstWord(l)
			if d == "" {
				continue
			}
			prefix, sub := "├──", "│   "
			if i == len(lines)-1 {
				prefix, sub = "└──", "    "
			}
			b.log(prefix + " domain: " + d)
			if f := "/var/log/caddy/domlogs/" + d + "/access.log"; conf.IsFile(f) && cpA(f, stage+"/caddy/domlogs/"+d+".log") == nil {
				b.log(sub + "├── Domlog")
			}
			if f := "/var/log/caddy/coraza_waf/" + d + ".log"; conf.IsFile(f) && cpA(f, stage+"/caddy/waf/") == nil {
				b.log(sub + "├── WAF log")
			}
			if f := "/etc/bind/zones/" + d + ".zone"; conf.IsFile(f) && cpA(f, stage+"/bind/zones/") == nil {
				b.log(sub + "├── DNS zone")
			}
			if f := "/etc/openpanel/caddy/suspended_domains/" + d + ".conf"; conf.IsFile(f) && cpA(f, stage+"/caddy/suspended/") == nil {
				b.log(sub + "├── Suspended")
			}
			if f := "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/" + d; conf.IsDir(f) && cpA(f, stage+"/caddy/ssl/acme/") == nil {
				b.log(sub + "├── Let's Encrypt SSL")
			}
			if f := "/etc/openpanel/caddy/ssl/custom/" + d; conf.IsDir(f) && cpA(f, stage+"/caddy/ssl/custom/") == nil {
				b.log(sub + "├── Custom SSL")
			}
			if conf.IsDir(dkimDir + "/keys/" + d) {
				if cpA(dkimDir+"/keys/"+d, stage+"/emails/dkim/") == nil {
					b.log(sub + "├── DKIM")
				}
				for _, t := range []string{"KeyTable", "SigningTable", "TrustedHosts"} {
					if !conf.IsFile(dkimDir + "/" + t) {
						continue
					}
					appendFile(stage+"/emails/dkim_"+t, linesText(dkimLines(dkimDir+"/"+t, d)))
				}
			}
			if f := "/etc/openpanel/caddy/domains/" + d + ".conf"; conf.IsFile(f) && cpA(f, stage+"/caddy/domains/") == nil {
				b.log(sub + "└── Caddyfile")
			}
		}
	}

	// --- Domlogs ---
	if f := "/var/log/caddy/stats/" + b.username; conf.IsDir(f) && cpA(f, stage+"/caddy/stats/") == nil {
		b.log("Collected domlogs for domains")
	}

	// --- Blocked IPs ---
	if f := "/etc/openpanel/caddy/deny/" + ctx + ".ips"; conf.IsFile(f) && cpA(f, stage+"/caddy/blocked.ips") == nil {
		b.log("Collected IP Blocker settings for domains")
	}

	// --- docker metadata ---
	if conf.IsFile(podman.ComposeFile(ctx)) {
		names := strings.TrimSuffix(strings.ReplaceAll(podman.User(ctx, "ps", "--format", "{{.Names}}").NoErr().Str()+"\n", "\n", " "), " ")
		if names == "" {
			names = "no containers"
		}
		if os.WriteFile(stage+"/docker/containers.txt", []byte(ctx+": "+names+"\n"), 0o666) == nil {
			b.log("Collected list of currently active containers for user")
		}
	}
	// rootless docker needed a per-user AppArmor profile for rootlesskit, podman doesn't use rootlesskit so this file never exists anymore
	if f := "/etc/apparmor.d/home." + ctx + ".bin.rootlesskit"; conf.IsFile(f) && cpA(f, stage+"/docker/apparmor.profile") == nil {
		b.log("Collected AppArmor profile for user")
	}
	os.WriteFile(stage+"/docker/uid.txt", []byte(sUID+"\n"), 0o666)

	// --- Emails ---
	if mailExternal != "" {
		// the store is shared by all accounts, one dir per domain
		store := strings.TrimSuffix(mailExternal, "/")
		var dirs []string
		for _, d := range domains {
			if conf.IsDir(store + "/" + d) {
				dirs = append(dirs, filepath.Base(store)+"/"+d)
			}
		}
		if len(dirs) > 0 {
			b.log(fmt.Sprintf("Archiving mailboxes from %s for %d domain(s) ...", store, len(dirs)))
			os.MkdirAll(stage+"/mail_external", 0o777)
			lf, _ := os.OpenFile(b.logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
			targs := append([]string{"-C", filepath.Dir(store), "--numeric-owner", "--acls", "--xattrs", "-cf", stage + "/mail_external/mail.tar"}, dirs...)
			c := b.lowprio("tar", targs...)
			if sh.C(c[0], c[1:]...).NoOut().Stderr(lf).Run() != nil {
				b.warn("Failed to archive mailboxes (non-fatal).")
			}
			lf.Close()
			os.WriteFile(stage+"/mail_external/path.txt", []byte(mailExternal+"\n"), 0o666)
			var full []string
			for _, d := range dirs {
				full = append(full, filepath.Dir(store)+"/"+d)
			}
			tot := text.Lines(sh.C("du", append([]string{"-shc"}, full...)...).NoErr().Str())
			mailExternalSize = ""
			if len(tot) > 0 {
				mailExternalSize, _, _ = strings.Cut(tot[len(tot)-1], "\t")
			}
		} else {
			mailExternal = ""
		}
	}

	if nonEmptyFile(coreDir + "/emails.yml") {
		b.log("Collecting email accounts information..")
		dms := "/usr/local/mail/openmail/docker-data/dms/config"
		alt := mailDomainAlt(domains)
		dp := regexp.MustCompile("@(" + alt + ")([|: ]|$)")
		rp := regexp.MustCompile("@(" + alt + ")/")
		os.MkdirAll(stage+"/emails", 0o777)
		grepFile(dms+"/postfix-accounts.cf", stage+"/emails/postfix-accounts.cf", dp)
		grepFile(dms+"/postfix-regex.cf", stage+"/emails/postfix-regex.cf", rp)
		grepFile(dms+"/dovecot-quotas.cf", stage+"/emails/dovecot-quotas.cf", dp)
		grepFile(dms+"/postfix-receive-access.cf", stage+"/emails/postfix-receive-access.cf", dp)
		grepFile(dms+"/postfix-send-access.cf", stage+"/emails/postfix-send-access.cf", dp)
		grepFile(dms+"/postfix-virtual.cf", stage+"/emails/postfix-virtual.cf", dp)
		os.WriteFile(stage+"/emails/postfwd.cf", nil, 0o666)
		if src := "/usr/local/mail/openmail/postfwd/postfwd.cf"; conf.IsFile(src) {
			os.WriteFile(stage+"/emails/postfwd.cf", []byte(postfwdFor(src, strings.Fields(domainListStr))), 0o666)
		}
		e := stage + "/emails/"
		b.log(fmt.Sprintf("Collected %d email accounts", countLines(e+"postfix-accounts.cf")))
		b.log(fmt.Sprintf("Collected %d aliases", countLines(e+"postfix-regex.cf")))
		b.log(fmt.Sprintf("Collected dovecot quota restrictions for %d addresses", countLines(e+"dovecot-quotas.cf")))
		b.log(fmt.Sprintf("Collected suspended incoming status for %d addresses", countLines(e+"postfix-receive-access.cf")))
		b.log(fmt.Sprintf("Collected suspended outgoing status for %d addresses", countLines(e+"postfix-send-access.cf")))
		b.log(fmt.Sprintf("Collected %d default (catch-all) addresses", countLines(e+"postfix-virtual.cf")))
		b.log(fmt.Sprintf("Collected %d postfwd rules", countLines(e+"postfwd.cf")))
	}

	var items []string
	for _, it := range []string{"manifest.env", "db", "system", "features", "core", "ftp", "caddy", "bind", "docker", "emails"} {
		if conf.Exists(stage + "/" + it) {
			items = append(items, it)
		}
	}
	if conf.IsDir(stage + "/mail_external") {
		items = append(items, "mail_external")
	}
	escCtx := regexp.MustCompile(`[|\\.*^$()+?{}]`).ReplaceAllString(ctx, `\$0`)

	// Use pigz for multi-core compression if available, otherwise fall back to gzip
	compress := "gzip"
	if sh.Has("pigz") {
		b.log("Using pigz for multi-core compression")
		compress = "pigz"
	}

	tarArgs := []string{"-cf", "-", "--numeric-owner", "--acls", "--xattrs",
		"--exclude=" + ctx + "/docker-data/volumes/" + ctx + "_html_data/_data/_backups",
		"--exclude=" + ctx + "/sockets/*/*/*",
		"--exclude=" + ctx + "/docker-data/volumes/" + ctx + "_mysql_data/_data/tc.log",
		"--exclude=" + ctx + "/docker-data/[!v]*",
		"--exclude=" + ctx + "/docker-data/v[!o]*",
		"--transform=s|^" + escCtx + "/|homedir/|;s|^" + escCtx + "$|homedir|",
	}
	if conf.IsDir("/home/" + ctx) {
		b.log("Streaming /home/" + ctx + "  →  homedir/ ...")
		tarArgs = append(tarArgs, "-C", "/home", ctx)
	} else {
		b.warn("/home/" + ctx + " not found — archive will not contain home files.")
	}
	if len(items) > 0 {
		tarArgs = append(append(tarArgs, "-C", stage), items...)
	}

	backupUID := statUID("/home/" + ctx)
	backupGID := sysGID(ctx)

	rc := b.writeArchive(destRoot, destDir, archiveName, tarArgs, compress, backupUID, backupGID)
	switch rc {
	case 0:
	case 1:
		b.warn("tar reported changed files during archive (exit 1) — archive created, verify integrity.")
	case 90:
		b.die("Backup folder "+destDir+" is not a real folder inside the user's volume.", 1)
	case 91:
		b.die("Compressing the archive failed: "+archive, 1)
	case 92:
		b.die("Archive integrity check failed: "+archive, 1)
	case 93:
		b.warn("Could not chown archive to " + ctx + " (non-fatal).")
	case 94:
		b.warn("tar reported changed files during archive (exit 1) — archive created, verify integrity.")
		b.warn("Could not chown archive to " + ctx + " (non-fatal).")
	case 95:
		b.die("Could not move the finished archive into place: "+archive, 1)
	default:
		b.die(fmt.Sprintf("tar failed creating: %s (exit %d)", archive, rc), 1)
	}
	if backupUID != "" && backupGID != "" && rc != 93 && rc != 94 {
		b.log("Ownership: " + ctx + " (" + backupUID + ":" + backupGID + ")")
	}

	// Summary
	elapsed := int64(time.Since(start) / time.Second)
	archiveSize := duField("-h", archive)
	containers := "none"
	if c := conf.Read(stage + "/docker/containers.txt"); conf.IsFile(stage + "/docker/containers.txt") {
		var parts []string
		for _, l := range text.Lines(c) {
			f := strings.Split(l, ":")
			if len(f) > 1 {
				parts = append(parts, f[1])
			} else {
				parts = append(parts, f[0])
			}
		}
		containers = text.Xargs(strings.Join(parts, "\n"))
		if containers == "" {
			containers = "none"
		}
	}
	compressUsed := "gzip"
	if sh.Has("pigz") {
		compressUsed = "pigz (multi-core)"
	}
	row := func(w int, k, v string) string {
		if w == 24 {
			return fmt.Sprintf("  %-24s %s", k, v)
		}
		return fmt.Sprintf("    %-22s %s", k, v)
	}
	b.slog("")
	b.slog("══════════════════════════════════════════════════════════")
	b.slog("  BACKUP COMPLETE — " + archiveName)
	b.slog("══════════════════════════════════════════════════════════")
	b.slog(row(24, "Archive:", archive))
	b.slog(row(24, "Size:", archiveSize))
	b.slog(row(24, "Compression:", compressUsed))
	b.slog(fmt.Sprintf("  %-24s %dh %dm %ds", "Time taken:", elapsed/3600, elapsed%3600/60, elapsed%60))
	b.slog("")
	b.slog("  Contents:")
	b.slog(row(22, "Plan:", `"`+planName+`"`))
	b.slog(row(22, fmt.Sprintf("Domains (%d):", len(domains)), domainListStr))
	b.slog(row(22, "Sites:", siteCount))
	b.slog(row(22, "Feature set:", or(featureSet, "none")))
	b.slog(row(22, "FTP accounts:", ftpCount))
	b.slog(row(22, "Home dir:", fmt.Sprintf("homedir/  (source ~%d MB)", diskEstimate)))
	b.slog(row(22, "Containers:", containers))
	if mailExternal != "" {
		b.slog(row(22, "External mail:", mailExternal+"  ("+mailExternalSize+")"))
	}
	b.slog("")
	b.slog(fmt.Sprintf("  Disk check:  source ~%d MB (via %s)  |  free ~%d MB", diskEstimate, diskSource, diskFree))
	b.slog("")
	b.slog(fmt.Sprintf("  Warnings: %d", len(b.warnings)))
	if len(b.warnings) > 0 {
		for _, w := range b.warnings {
			b.slog("    [!] " + w)
		}
	} else {
		b.slog("    none")
	}
	b.slog("══════════════════════════════════════════════════════════")
	b.slog("")

	ok = true
	fmt.Println(archive)
	return 0
}

// firstWord is `IFS=$'\t ' read -r x _`
func firstWord(l string) string {
	f := strings.FieldsFunc(l, func(r rune) bool { return r == ' ' || r == '\t' })
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// exportUserTables writes the ddl and rows of mcp_tokens and user_passkeys, the tables only exist once the panel used them
func exportUserTables(dir, userID string, q func(string) string, raw func(string) string) {
	for _, t := range []string{"mcp_tokens", "user_passkeys"} {
		found := false
		for _, l := range strings.Split(q("SHOW TABLES LIKE '"+t+"'"), "\n") {
			if l == t {
				found = true
			}
		}
		if !found {
			continue
		}
		var ddl strings.Builder
		for _, l := range text.Lines(q("SHOW CREATE TABLE " + t)) {
			_, rest, ok := strings.Cut(l, "\t")
			if !ok {
				rest = l
			}
			if strings.HasPrefix(rest, "CREATE TABLE") {
				rest = "CREATE TABLE IF NOT EXISTS" + rest[len("CREATE TABLE"):]
			}
			ddl.WriteString(strings.ReplaceAll(rest, `\n`, "\n") + "\n")
		}
		ddl.WriteString(";\n")
		os.WriteFile(dir+"/"+t+".ddl", []byte(ddl.String()), 0o666)
		cols := q("SELECT GROUP_CONCAT(CONCAT('`',column_name,'`') ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='" + db.Database + "' AND table_name='" + t + "' AND column_name<>'id';")
		qcols := q("SELECT GROUP_CONCAT(CONCAT('QUOTE(`',column_name,'`)') ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='" + db.Database + "' AND table_name='" + t + "' AND column_name<>'id';")
		os.WriteFile(dir+"/"+t+".rows.sql", []byte(raw("SELECT CONCAT('INSERT INTO `"+t+"` ("+cols+") VALUES (', CONCAT_WS(',', "+qcols+"), ');') FROM "+t+" WHERE user_id="+userID+";")), 0o666)
	}
}

// captureSystemUser saves the passwd, group, shadow and subid lines of the context
func captureSystemUser(ctx, dir string) {
	var pw []string
	for _, l := range conf.Lines("/etc/passwd") {
		if strings.Split(l, ":")[0] == ctx {
			pw = append(pw, l)
		}
	}
	os.WriteFile(dir+"/passwd.user", []byte(linesText(pw)), 0o666)

	// same as the awk: gid is taken from field 4 (the member list), so mostly only the user's own group line matches
	eq := func(a, b string) bool {
		x, e1 := strconv.ParseFloat(a, 64)
		y, e2 := strconv.ParseFloat(b, 64)
		if e1 == nil && e2 == nil && a != "" && b != "" {
			return x == y
		}
		return a == b
	}
	gid := ""
	var gr []string
	for _, l := range conf.Lines("/etc/group") {
		f := strings.Split(l, ":")
		for len(f) < 4 {
			f = append(f, "")
		}
		if f[0] == ctx {
			gid = f[3]
		}
		if eq(f[2], gid) {
			gr = append(gr, l)
		}
		if f[0] == ctx {
			gr = append(gr, l)
		}
	}
	sort.Strings(gr)
	var uniq []string
	for i, l := range gr {
		if i == 0 || l != gr[i-1] {
			uniq = append(uniq, l)
		}
	}
	os.WriteFile(dir+"/group.user", []byte(linesText(uniq)), 0o666)
	for _, f := range []string{"shadow", "subuid", "subgid"} {
		var out []string
		for _, l := range conf.Lines("/etc/" + f) {
			if strings.HasPrefix(l, ctx+":") {
				out = append(out, l)
			}
		}
		os.WriteFile(dir+"/"+f+".user", []byte(linesText(out)), 0o666)
	}
}

// writeArchive streams tar through the compressor into a new file in the checked destination, exit codes match the old subshell
func (b *backupJob) writeArchive(root, dir, name string, tarArgs []string, compress, uid, gid string) int {
	real, err := tenantfs.Resolve(root, dir)
	if err != nil {
		return 90
	}
	tmp := real + "/." + name + "." + strconv.Itoa(os.Getpid()) + ".partial"
	old := syscall.Umask(0o027)
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o666)
	syscall.Umask(old)
	if err != nil {
		return 91
	}
	lf, _ := os.OpenFile(b.logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
	defer lf.Close()

	tc := b.lowprio("tar", tarArgs...)
	cc := b.lowprio(compress)
	tarCmd := exec.Command(tc[0], tc[1:]...)
	tarCmd.Dir = real
	tarCmd.Stderr = lf
	zipCmd := exec.Command(cc[0], cc[1:]...)
	zipCmd.Dir = real
	zipCmd.Stderr = os.Stderr
	pr, pw, _ := os.Pipe()
	tarCmd.Stdout = pw
	zipCmd.Stdin = pr
	zout, _ := zipCmd.StdoutPipe()
	tarCmd.Start()
	zipCmd.Start()
	pw.Close()
	pr.Close()
	_, werr := io.Copy(out, zout)
	cerr := out.Close()
	tarRC := sh.Code(tarCmd.Wait())
	zipRC := sh.Code(zipCmd.Wait())
	if zipRC != 0 || werr != nil || cerr != nil || tarRC > 1 {
		os.Remove(tmp)
		if tarRC > 1 {
			return tarRC
		}
		return 91
	}
	gz := b.lowprio("gzip", "-t")
	f, err := os.OpenFile(tmp, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil || sh.C(gz[0], gz[1:]...).StdinReader(f).NoOut().Stderr(lf).Run() != nil {
		if f != nil {
			f.Close()
		}
		os.Remove(tmp)
		return 92
	}
	f.Close()
	chownRC := 0
	if uid != "" && gid != "" {
		if os.Lchown(tmp, text.Atoi(uid), text.Atoi(gid)) != nil {
			chownRC = 1
		}
	}
	if os.Rename(tmp, real+"/"+name) != nil {
		os.Remove(tmp)
		return 95
	}
	if tarRC == 1 {
		if chownRC == 1 {
			return 94
		}
		return 1
	}
	if chownRC == 1 {
		return 93
	}
	return 0
}
