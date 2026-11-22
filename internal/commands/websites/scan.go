// ##############################################################################
// Script Name: internal/commands/websites/scan.go
// Description: Scan user files for WP sites and add them to SiteManager interface.
// Usage: opencli websites-scan <USERNAME|-all>
// Author: Stefan Pejcic
// Created: 23.10.2024
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

package websites

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "websites-scan",
		Desc:  "Scan user files for WP sites and add them to SiteManager interface.",
		Usage: "opencli websites-scan <USERNAME|-all>",
		Run:   scan,
	})
}

const scanUsage = "Usage: opencli websites-scan <USERNAME> OR opencli websites-scan -all"

var (
	scanDataPath = regexp.MustCompile(`^.*/_data/`)
	scanNumeric  = regexp.MustCompile(`^[0-9]+$`)
	scanVersion  = regexp.MustCompile(`^[0-9]+(\.[0-9]+)+([-.A-Za-z0-9]*)?$`)
	scanVerNum   = regexp.MustCompile(`\d+\.\d+`)
	scanHostSubs = func(host string) []*regexp.Regexp {
		var res []*regexp.Regexp
		for _, v := range []string{`localhost`, `localhost:3306`, `127\.0\.0\.1`, `127\.0\.0\.1:3306`} {
			res = append(res, regexp.MustCompile(`(define\([[:space:]]*['"]DB_HOST['"],[[:space:]]*)['"]`+v+`['"]`))
		}
		return res
	}
)

type scanCtx struct {
	user       string
	context    string
	phpVersion string
	dbHost     string
	base       string
}

func scan(args []string) int {
	switch {
	case len(args) == 0:
		fmt.Println(scanUsage)
		return 1
	case args[0] == "-all":
		users := userNames()
		if users == "" || users == "No users." {
			fmt.Println("No users found in the database.")
			return 1
		}
		words := strings.Fields(users)
		for i, u := range words {
			fmt.Printf("Processing user: %s (%d/%d)\n", u, i+1, len(words))
			(&scanCtx{user: u}).run()
			fmt.Println("------------------------------")
		}
		fmt.Println("DONE.")
	case len(args) == 1:
		(&scanCtx{user: args[0]}).run()
	default:
		fmt.Println(scanUsage)
		return 1
	}
	return 0
}

func scanInside(p string) string {
	return scanDataPath.ReplaceAllString(p, "/var/www/html/")
}

// scanFind is find <base> -name <name> in readdir order, follow is find -L
func scanFind(base, name string, follow bool, skip func(string) bool) []string {
	var out []string
	type key struct{ dev, ino uint64 }
	var walk func(d string, chain map[key]bool)
	walk = func(d string, chain map[key]bool) {
		f, err := os.Open(d)
		if err != nil {
			return
		}
		names, _ := f.Readdirnames(-1)
		f.Close()
		for _, n := range names {
			p := d + "/" + n
			if n == name && (skip == nil || !skip(p)) {
				out = append(out, p)
			}
			st, err := os.Lstat(p)
			if err != nil {
				continue
			}
			if follow && st.Mode()&os.ModeSymlink != 0 {
				if s2, err := os.Stat(p); err == nil {
					st = s2
				}
			}
			if !st.IsDir() {
				continue
			}
			k := key{}
			if s, ok := st.Sys().(*syscall.Stat_t); ok {
				k = key{uint64(s.Dev), s.Ino}
			}
			if follow && chain[k] {
				continue
			}
			chain[k] = true
			walk(p, chain)
			delete(chain, k)
		}
	}
	if base == name {
		out = append(out, base)
	}
	walk(base, map[key]bool{})
	return out
}

func (s *scanCtx) wp(path, command string) string {
	out, _ := podman.Ctx(s.context, "exec", "php-fpm-"+s.phpVersion, "sh", "-c",
		"php -d memory_limit=-1 -d open_basedir=none -d disable_functions= -d display_errors=0 -d error_log=/dev/null /usr/local/bin/wp --allow-root --path="+path+" "+command).Out()
	return out
}

func (s *scanCtx) coreVersion(path string) string {
	out := strings.ReplaceAll(s.wp(path, "core version 2>/dev/null"), "\r", "")
	if i := strings.LastIndex(out, "\n"); i >= 0 {
		out = out[i+1:]
	}
	v := strings.Join(strings.Fields(out), "")
	if scanVersion.MatchString(v) {
		return v
	}
	return ""
}

func scanExists(site string) bool {
	r, _ := db.Plain("SELECT EXISTS(SELECT 1 FROM sites WHERE site_name = '"+db.Escape(site)+"');", "-s", "-s").Out()
	return r == "1"
}

func scanDomainID(domain string) string {
	r, _ := db.Plain("SELECT domain_id FROM domains WHERE domain_url = '"+db.Escape(domain)+"';", "-s", "-s").Out()
	return r
}

func scanMysql(sql string) {
	sh.C("mysql").Stdin(sql + "\n").Run()
}

func (s *scanCtx) fixPerms(inside string) {
	sh.C("timeout", "600", sh.Self(), "files-fix_permissions", s.user, inside).Quiet().Background()
}

func (s *scanCtx) socketReady() bool {
	home := "/home/" + s.user
	if !conf.IsFile(home + "/docker-compose.yml") {
		return false
	}
	header := regexp.MustCompile(`^  [A-Za-z0-9_.-]+:$`)
	mount := regexp.MustCompile(`^[[:space:]]*- \./sockets/mysqld:/var/run/mysqld[[:space:]]*$`)
	php, mounted, bad, seen := false, false, false, false
	for _, l := range conf.Lines(home + "/docker-compose.yml") {
		if header.MatchString(l) {
			if php && !mounted {
				bad = true
			}
			name := strings.Replace(strings.Fields(l)[0], ":", "", 1)
			php = strings.HasPrefix(name, "php-fpm-") || name == "openlitespeed" || name == "litespeed"
			if php {
				seen = true
			}
			mounted = false
			continue
		}
		if php && mount.MatchString(l) {
			mounted = true
		}
	}
	if php && !mounted {
		bad = true
	}
	if !seen || bad {
		return false
	}
	inis, _ := filepath.Glob(home + "/php.ini/*.ini")
	if len(inis) == 0 {
		return false
	}
	re1 := regexp.MustCompile(`^[[:space:]]*mysqli\.default_socket[[:space:]]*=[[:space:]]*"?/var/run/mysqld/mysqld\.sock"?[[:space:]]*$`)
	re2 := regexp.MustCompile(`^[[:space:]]*pdo_mysql\.default_socket[[:space:]]*=[[:space:]]*"?/var/run/mysqld/mysqld\.sock"?[[:space:]]*$`)
	for _, ini := range inis {
		b, err := os.ReadFile(ini)
		if err != nil {
			return false
		}
		a, c := false, false
		for _, l := range strings.Split(string(b), "\n") {
			a = a || re1.MatchString(l)
			c = c || re2.MatchString(l)
		}
		if !a || !c {
			return false
		}
	}
	return true
}

func (s *scanCtx) insertScanned(site, domainID, email, version, cms string) {
	fmt.Println("Adding website " + site + " to Site Manager")
	scanMysql("INSERT INTO sites (site_name, domain_id, admin_email, version, type) VALUES ('" + db.Escape(site) + "', '" + db.Escape(domainID) + "', '" + db.Escape(email) + "', '" + db.Escape(version) + "', '" + db.Escape(cms) + "');")
	inside := "/var/www/html/" + site
	fmt.Println("Fixing permissions and ownership for the directory " + inside)
	s.fixPerms(inside)
}

// resolve maps an inside container path to its domain by the longest docroot, ok is false when none owns it
func (s *scanCtx) resolve(filePath string) (string, string, bool) {
	target := filepath.Dir(filePath)
	rows, _ := db.Plain("SELECT domains.domain_id, domains.domain_url, domains.docroot FROM domains JOIN users ON users.id = domains.user_id WHERE users.username = '"+db.Escape(s.user)+"' AND domains.docroot IS NOT NULL AND domains.docroot != '' ORDER BY LENGTH(domains.docroot) DESC;", "-s", "-s").Out()
	for _, line := range strings.Split(rows, "\n") {
		f := readFields(line, 3, "\t")
		docroot := strings.TrimSuffix(f[2], "/")
		if f[2] == "" {
			continue
		}
		if target == docroot || strings.HasPrefix(target, docroot+"/") {
			sub := strings.TrimPrefix(strings.TrimPrefix(target, docroot), "/")
			if sub != "" {
				return f[0], f[1] + "/" + sub, true
			}
			return f[0], f[1], true
		}
	}
	return "", "", false
}

// scanOther is the shared tail of the TinyPhotoGallery, TinyFileManager and OJS scans
func (s *scanCtx) scanOther(label, inside, version, cms string) {
	fmt.Println("- Found possible " + label + " install: " + inside)
	domainID, site, ok := s.resolve(inside)
	if !ok {
		fmt.Println("  WARNING: unable to resolve domain for " + inside + " - make sure a domain/subdirectory docroot covers this path - Skipping")
		return
	}
	if scanExists(site) {
		fmt.Println("  Site " + site + " already exists in the SiteManager - Skipping")
		return
	}
	domain := site
	if i := strings.Index(domain, "/"); i >= 0 {
		domain = domain[:i]
	}
	s.insertScanned(site, domainID, "admin@"+domain, version, cms)
}

func (s *scanCtx) run() {
	db.Require()
	ctx, _ := db.Plain("SELECT server FROM users WHERE username = '"+db.Escape(s.user)+"'", "-D", db.Database, "-sN").Out()
	if ctx == "" {
		ctx = s.user
	}
	s.context = ctx
	s.base = "/home/" + s.user

	var found, existing []string
	foundCount, existingCount := 0, 0

	var vers []string
	for _, l := range strings.Split(sh.Opencli("php-default", s.user).Str(), "\n") {
		vers = append(vers, scanVerNum.FindAllString(l, -1)...)
	}
	s.phpVersion = strings.Join(vers, "\n")

	var types []string
	env := s.base + "/.env"
	if !conf.IsFile(env) {
		fmt.Fprintf(os.Stderr, "grep: %s: No such file or directory\n", env)
	}
	for _, l := range conf.Lines(env) {
		if strings.HasPrefix(l, "MYSQL_TYPE=") {
			v := strings.TrimPrefix(l, "MYSQL_TYPE=")
			if i := strings.Index(v, "="); i >= 0 {
				v = v[:i]
			}
			types = append(types, strings.ReplaceAll(v, `"`, ""))
		}
	}
	mysqlType := strings.Join(types, "\n")
	if mysqlType != "mariadb" && mysqlType != "mysql" {
		mysqlType = "localhost"
	}
	s.dbHost = mysqlType
	if s.socketReady() {
		s.dbHost = "localhost"
	}

	for _, cfgPath := range scanFind(s.base, "wp-config.php", false, nil) {
		inside := scanInside(cfgPath)
		fmt.Println("- Parsing file: " + inside)
		subs := scanHostSubs(s.dbHost)
		conf.SedFile(cfgPath, func(lines []string) []string {
			for i, l := range lines {
				for _, re := range subs {
					l = re.ReplaceAllString(l, "${1}'"+s.dbHost+"'")
				}
				lines[i] = l
			}
			return lines
		})

		dir := filepath.Dir(inside)
		domain := s.wp(dir, "option get siteurl 2>/dev/null")
		siteName := sedHTTP(domain)

		if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
			fmt.Println("Error: unable to get sitename from database.")
			continue
		}

		var dn []string
		for _, l := range strings.Split(siteName, "\n") {
			dn = append(dn, strings.SplitN(l, "/", 2)[0])
		}
		domainName := strings.Join(dn, "\n")

		if scanExists(siteName) {
			fmt.Println("  Site " + siteName + " already exists in the SiteManager - Skipping")
			// sites added before or by the installer can carry "latest" or an old version, refresh it while we're here
			if v := s.coreVersion(dir); v != "" {
				scanMysql("UPDATE sites SET version = '" + db.Escape(v) + "' WHERE site_name = '" + db.Escape(siteName) + "' AND type = 'wordpress' AND (version IS NULL OR version <> '" + db.Escape(v) + "');")
			}
			existing = append(existing, "- "+siteName+" - domain: "+domainName+", config: "+strings.TrimSuffix(inside, "/wp-config.php"))
			existingCount++
			continue
		}

		domainID := scanDomainID(domainName)
		if !scanNumeric.MatchString(domainID) && strings.HasPrefix(domainName, "www.") {
			// https://github.com/stefanpejcic/OpenPanel/issues/1124
			baseDomain := strings.TrimPrefix(domainName, "www.")
			domainID = scanDomainID(baseDomain)
			if scanNumeric.MatchString(domainID) {
				fmt.Println("  Domain " + domainName + " not added, but " + baseDomain + " is - linking site to " + baseDomain)
			}
		}
		if !scanNumeric.MatchString(domainID) {
			fmt.Println("  WARNING: ID not detected for domain " + domainName + " - make sure that domain is added for user - Skipping")
			cli.Exit(1)
		}

		email := s.wp(dir, "option get admin_email 2>/dev/null")
		if !strings.Contains(email, "@") {
			fmt.Println("  WARNING: Invalid admin email: " + email)
		}
		version := s.coreVersion(dir)

		fmt.Println("Adding website " + siteName + " to Site Manager")
		scanMysql("INSERT INTO sites (site_name, domain_id, admin_email, version, type) VALUES ('" + db.Escape(siteName) + "', '" + db.Escape(domainID) + "', '" + db.Escape(email) + "', '" + db.Escape(version) + "', 'wordpress');")

		fmt.Println("Fixing permissions and ownership for the directory " + inside)
		s.fixPerms(inside)

		found = append(found, "- "+siteName+", domain: "+domainName+", email: "+email+", version: "+version)
		foundCount++
	}

	switch {
	case len(found) > 0:
		fmt.Println("Scan completed. Detected " + strconv.Itoa(foundCount) + " new WordPress installations:")
		for _, l := range found {
			fmt.Println(l)
		}
	case len(existing) > 0:
		fmt.Println("Scan completed. No new WordPress installations detected, but the following " + strconv.Itoa(existingCount) + " existing installations are present:")
		for _, l := range existing {
			fmt.Println(l)
		}
	default:
		fmt.Println("Scan completed. No WordPress installations detected.")
	}

	for _, idx := range scanFind(s.base, "index.php", false, nil) {
		b, err := os.ReadFile(idx)
		if err != nil || !strings.Contains(strings.ToLower(string(b)), "tinyphotogallery") {
			continue
		}
		if st, err := os.Stat(filepath.Dir(idx) + "/photos"); err != nil || !st.IsDir() {
			continue
		}
		s.scanOther("TinyPhotoGallery", scanInside(idx), "main", "tinyphotogallery")
	}
	for _, f := range scanFind(s.base, "tinyfilemanager.php", false, nil) {
		s.scanOther("TinyFileManager", scanInside(f), "latest", "tinyfilemanager")
	}
	for _, f := range scanFind(s.base, "config.inc.php", true, func(p string) bool { return strings.HasSuffix(p, "_ojsapp/config.inc.php") }) {
		s.scanOther("OJS", strings.TrimSuffix(scanInside(f), "/config.inc.php"), "latest", "ojs")
	}
}
