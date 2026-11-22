// ##############################################################################
// Script Name: internal/commands/domainsadd/test.go
// Description: Test a website speed through every layer (DNS, Caddy/WAF, Varnish, webserver, PHP, database), check limits and settings, and show the biggest issue with a fix.
// Usage: opencli domains-test <DOMAIN_NAME>[/SUBFOLDER] [RUNS] [--load <N|auto>]
// Author: Stefan Pejcic
// Created: 28.09.2026
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
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"crypto/rand"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/phplisten"
	"opencli/internal/podman"
	"opencli/internal/requirement"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-test",
		Desc:  "Test a website speed through every layer (DNS, Caddy/WAF, Varnish, webserver, PHP, database), check limits and settings, and show the biggest issue with a fix.",
		Usage: "opencli domains-test <DOMAIN_NAME>[/SUBFOLDER] [RUNS] [--load <N|auto>]",
		Run:   speedTest,
	})
}

type finding struct {
	impact, sev, text, fix string
}

type speed struct {
	B, R, Y, G, N string

	load, site, domain, folder, urlPath string
	runs                                int

	owner, ctx, docroot, dbPHP     string
	envFile                        string
	webServer, httpPort, httpsPort string
	proxyPort                      string
	litespeed                      bool
	hostDocroot, vhost             string
	phpContainer, phpVersion       string
	fpmSock, dbContainer           string
	running                        map[string]bool
	caddyConf                      string
	varnishDomain                  string
	viaVarnish                     bool
	wafOn                          bool
	bodyAccess, bodySource         string
	coraza                         string

	ms, code    map[string]string
	findings    []finding
	fullTime    bool
	caddyScheme string
	caddyPort   string
}

func (s *speed) envVal(key string) string {
	for _, l := range conf.Lines(s.envFile) {
		if strings.HasPrefix(l, key+"=") {
			return strings.ReplaceAll(l[len(key)+1:], `"`, "")
		}
	}
	return ""
}

// env_port is awk -F: '{print $(NF-1)}'
func (s *speed) envPort(key string) string {
	v := s.envVal(key)
	if v == "" {
		return ""
	}
	f := strings.Split(v, ":")
	if len(f) == 1 {
		return v
	}
	return f[len(f)-2]
}

func (s *speed) isRunning(c string) bool { return s.running[c] }

func (s *speed) addFinding(impact, sev, text, fix string) {
	s.findings = append(s.findings, finding{impact, sev, text, fix})
}

func (s *speed) inContainer(args ...string) *sh.Cmd {
	return podman.Ctx(s.ctx, append([]string{"exec"}, args...)...)
}

func speedTest(args []string) int {
	if sh.Has("apt-get") {
		requirement.Command("dig", "dnsutils")
	} else {
		requirement.Command("dig", "bind-utils")
	}
	db.Require()
	s := &speed{ms: map[string]string{}, code: map[string]string{}}
	// --load can go anywhere, the rest stays positional
	var pos []string
	for len(args) > 0 {
		a := args[0]
		switch {
		case a == "--load":
			s.load = ""
			if len(args) > 1 {
				s.load = args[1]
			}
			args = args[1:]
			if len(args) > 0 {
				args = args[1:]
			}
		case strings.HasPrefix(a, "--load="):
			s.load = a[len("--load="):]
			args = args[1:]
		default:
			pos = append(pos, a)
			args = args[1:]
		}
	}
	if cli.Arg(pos, 0) == "" {
		fmt.Println("Usage: opencli domains-test <DOMAIN_NAME>[/SUBFOLDER] [RUNS] [--load <N|auto>]")
		fmt.Println("Example: opencli domains-test example.com")
		fmt.Println("         opencli domains-test example.com --load auto   # raise concurrency until the site breaks")
		fmt.Println("         opencli domains-test example.com --load 100    # 100 concurrent connections")
		return 1
	}
	if s.load != "" && s.load != "auto" {
		n, err := strconv.Atoi(s.load)
		if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(s.load) || err != nil || n > 5000 {
			fmt.Println("--load must be auto or a number of concurrent connections (1-5000)")
			return 1
		}
	}
	s.site = strings.ToLower(pos[0])
	runs := "5"
	if len(pos) > 1 && pos[1] != "" {
		runs = pos[1]
	}
	if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(runs) {
		fmt.Println("runs must be a positive number")
		return 1
	}
	s.runs, _ = strconv.Atoi(runs)
	s.domain = s.site
	if i := strings.Index(s.site, "/"); i >= 0 {
		s.domain = s.site[:i]
		s.folder = s.site[i+1:]
	}
	s.urlPath = "/"
	if s.folder != "" {
		s.urlPath = "/" + s.folder + "/"
	}
	if stdoutIsTerminal() {
		s.B, s.R, s.Y, s.G, s.N = "\033[1m", "\033[31m", "\033[33m", "\033[32m", "\033[0m"
	}

	// lookups
	row, _ := db.Cmd("SELECT u.username, u.server, d.docroot, d.php_version FROM domains d JOIN users u ON u.id = d.user_id WHERE d.domain_url = '"+db.Escape(s.domain)+"' LIMIT 1", "-s", "-N").Timeout(10 * time.Second).Out()
	f := strings.Fields(row)
	for i, p := range []*string{&s.owner, &s.ctx, &s.docroot, &s.dbPHP} {
		if i < len(f) {
			*p = f[i]
		}
	}
	if s.owner == "" {
		fmt.Printf("Domain %s is not hosted on this server.\n", s.domain)
		return 1
	}
	s.envFile = "/home/" + s.ctx + "/.env"
	if !conf.IsFile(s.envFile) {
		fmt.Printf("Could not find %s for user %s.\n", s.envFile, s.owner)
		return 1
	}
	s.webServer = s.envVal("WEB_SERVER")
	s.httpPort = s.envPort("HTTP_PORT")
	s.httpsPort = s.envPort("HTTPS_PORT")
	for _, l := range conf.Lines(s.envFile) {
		if strings.HasPrefix(l, "PROXY_HTTP_PORT=") {
			s.proxyPort = s.envPort("PROXY_HTTP_PORT")
			break
		}
	}
	s.litespeed = strings.Contains(s.webServer, "litespeed")
	// caddy mode has no webserver container, the webserver steps go to the server's caddy on 443 instead
	if s.webServer == "caddy" {
		s.httpsPort = "443"
	}
	if !strings.HasPrefix(s.docroot, "/var/www/html/") {
		fmt.Printf("Could not detect the docroot for %s.\n", s.domain)
		return 1
	}
	if s.folder != "" {
		s.docroot += "/" + s.folder
	}
	s.hostDocroot = "/home/" + s.ctx + "/docker-data/volumes/" + s.ctx + "_html_data/_data/" + strings.TrimPrefix(s.docroot, "/var/www/html/")
	s.vhost = "/home/" + s.ctx + "/docker-data/volumes/" + s.ctx + "_webserver_data/_data/" + s.domain + ".conf"
	s.phpContainer = regexp.MustCompile(`php-fpm-[0-9.]*`).FindString(conf.Read(s.vhost))
	if s.phpContainer == "" && s.dbPHP != "" {
		s.phpContainer = "php-fpm-" + s.dbPHP
	}
	if s.litespeed {
		s.phpContainer = s.webServer
	}
	s.phpVersion = strings.TrimPrefix(s.phpContainer, "php-fpm-")
	if phplisten.Mode(s.ctx) != "port" {
		s.fpmSock = phplisten.SocketDir + "/" + s.phpContainer + "/www.sock"
	}
	s.dbContainer = s.envVal("MYSQL_TYPE")
	if s.dbContainer == "" {
		s.dbContainer = "mysql"
	}

	s.running = map[string]bool{}
	names, _ := podman.Ctx(s.ctx, "ps", "--format", "{{.Names}}").NoErr().Out()
	for _, n := range strings.Split(names, "\n") {
		s.running[n] = true
	}
	s.caddyConf = "/etc/openpanel/caddy/domains/" + s.domain + ".conf"
	s.varnishDomain = "off"
	cc := conf.Read(s.caddyConf)
	if regexp.MustCompile(`(?m)^#.*reverse_proxy https://127\.0\.0\.1`).MatchString(cc) {
		s.varnishDomain = "on"
	}
	s.viaVarnish = s.isRunning("varnish") && s.proxyPort != "" && s.varnishDomain == "on"

	s.coraza = "/etc/openpanel/caddy/coraza_rules.conf"
	s.bodyAccess = "off"
	s.wafOn = regexp.MustCompile(`(?m)^[[:space:]]*SecRuleEngine[[:space:]]+On`).MatchString(cc)
	if s.wafOn {
		if regexp.MustCompile(`(?m)^SecResponseBodyAccess[[:space:]]+On`).MatchString(conf.Read(s.coraza)) {
			s.bodyAccess, s.bodySource = "on", "global"
		}
		// a per-domain line after the includes wins
		if regexp.MustCompile(`(?m)^[[:space:]]*SecResponseBodyAccess[[:space:]]+Off`).MatchString(cc) {
			s.bodyAccess, s.bodySource = "off", ""
		}
		if regexp.MustCompile(`(?m)^[[:space:]]*SecResponseBodyAccess[[:space:]]+On`).MatchString(cc) {
			s.bodyAccess, s.bodySource = "on", "domain"
		}
	}

	waf := "no"
	if s.wafOn {
		waf = "yes"
	}
	if s.load == "" {
		php := s.phpContainer
		if php == "" {
			php = "no php container found"
		}
		vs := "stopped"
		if s.isRunning("varnish") {
			vs = "running"
		}
		extra := ""
		if s.wafOn {
			extra = ", response body inspection: " + s.bodyAccess
		}
		fmt.Printf("%sSite%s         %s  (user %s, %s, %s, %s)\n", s.B, s.N, s.site, s.owner, s.webServer, php, s.dbContainer)
		fmt.Printf("%sDocroot%s      %s\n", s.B, s.N, s.docroot)
		fmt.Printf("%sVarnish%s      container: %s, domain: %s\n", s.B, s.N, vs, s.varnishDomain)
		fmt.Printf("%sWAF%s          %s%s\n", s.B, s.N, waf, extra)
		fmt.Printf("%sRuns%s         1 warm-up + %d measured per test, median time to first byte (local tests without connect/TLS setup)\n", s.B, s.N, s.runs)
		fmt.Println()
	}

	if s.load != "" {
		return s.loadTest()
	}
	return s.timings()
}

// ---------------------------------------------------------------- helpers

func (s *speed) curlRuns(o curlOpts) []string {
	var out []string
	for i := 0; i <= s.runs; i++ {
		o.timeout = 15 * time.Second
		o.ua = "OpenPanel speed test"
		r := curlTimed(o, false)
		if i == 0 && r.code == "000" {
			return out
		}
		v := r.start
		if !s.fullTime {
			v = r.start - r.pre
		}
		out = append(out, fmt.Sprintf("%.6f %s", v, r.code))
	}
	return out
}

func (s *speed) runTest(key, label, note string, o curlOpts) {
	s.ms[key], s.code[key] = median(s.curlRuns(o))
	s.show(label, key, note)
}

func (s *speed) show(label, key, note string) {
	m := s.ms[key]
	if m == "-" || m == "" {
		fmt.Printf("  %s %s%s%s   %s\n", padR(label, 36), s.R, padL("no answer", 12), s.N, note)
		return
	}
	color := ""
	if s.code[key] != "200" {
		color = s.Y
	}
	fmt.Printf("  %s %s ms   %sHTTP %s%s   %s\n", padR(label, 36), padL(m, 9), color, s.code[key], s.N, note)
}

func (s *speed) msv(key string) string {
	v := s.ms[key]
	if v == "-" {
		return ""
	}
	return v
}

func (s *speed) caddyTest(key, label, note, path string) {
	s.runTest(key, label, note, curlOpts{
		url:  s.caddyScheme + "://" + s.domain + path,
		dial: map[string]string{s.domain + ":" + s.caddyPort: "127.0.0.1:" + s.caddyPort},
	})
}

func (s *speed) webOpts(path string) curlOpts {
	return curlOpts{url: "https://" + s.domain + path, dial: map[string]string{s.domain + ":443": "127.0.0.1:" + s.httpsPort}}
}

func (s *speed) throttledUsec(c string) string {
	return s.inContainer(c, "sh", "-c", `grep -E "^throttled_usec" /sys/fs/cgroup/cpu.stat 2>/dev/null | cut -d" " -f2`).NoErr().Str()
}

func loadavg1() string {
	return strings.Fields(conf.Read("/proc/loadavg") + " ")[0]
}

func hostIPs() string {
	out, _ := sh.C("hostname", "-I").Out()
	return out
}

func dnsA(host string) []string {
	out := sh.C("dig", "+short", host, "A", "@1.1.1.1").NoErr().Str()
	var r []string
	for _, l := range strings.Split(out, "\n") {
		if regexp.MustCompile(`^[0-9.]+$`).MatchString(l) {
			r = append(r, l)
		}
	}
	return r
}

const speedDBProbe = `<?php
if (($_GET['t'] ?? '') !== '__TOKEN__') { http_response_code(404); exit; }
header('Content-Type: application/json');
$cfg = @file_get_contents(__DIR__ . '/wp-config.php');
if (!$cfg) { echo json_encode(['error' => 'no wp-config.php']); exit; }
$get = function ($name) use ($cfg) { return preg_match("/define\\(\\s*['\"]" . $name . "['\"]\\s*,\\s*['\"]([^'\"]*)['\"]/", $cfg, $m) ? $m[1] : ''; };
$prefix = preg_match('/\$table_prefix\s*=\s*[\'"]([^\'"]*)[\'"]/', $cfg, $m) ? $m[1] : 'wp_';
$host = $get('DB_HOST'); $port = 3306;
if (strpos($host, ':') !== false) { [$host, $port] = explode(':', $host, 2); $port = (int) $port ?: 3306; }
mysqli_report(MYSQLI_REPORT_OFF);
$t = hrtime(true);
$db = @mysqli_connect($host, $get('DB_USER'), $get('DB_PASSWORD'), $get('DB_NAME'), $port);
$out = ['connect_ms' => round((hrtime(true) - $t) / 1e6, 1)];
if (!$db) { $out['error'] = mysqli_connect_error(); echo json_encode($out); exit; }
$t = hrtime(true); mysqli_query($db, 'SELECT 1'); $out['select1_ms'] = round((hrtime(true) - $t) / 1e6, 2);
$t = hrtime(true);
$r = mysqli_query($db, "SELECT COUNT(*), COALESCE(SUM(LENGTH(option_value)), 0) FROM {$prefix}options WHERE autoload IN ('yes', 'on', 'auto-on', 'auto')");
$out['autoload_ms'] = round((hrtime(true) - $t) / 1e6, 2);
if ($r) { [$rows, $bytes] = mysqli_fetch_row($r); $out['autoload_rows'] = (int) $rows; $out['autoload_kb'] = round($bytes / 1024); }
$r = mysqli_query($db, "SELECT option_value FROM {$prefix}options WHERE option_name = 'active_plugins'");
if ($r && ($row = mysqli_fetch_row($r))) { $p = @unserialize($row[0]); $out['active_plugins'] = is_array($p) ? count($p) : null; }
$r = mysqli_query($db, "SELECT COUNT(*) FROM {$prefix}options WHERE option_name LIKE '\\_transient\\_%'");
if ($r) { $out['transients'] = (int) mysqli_fetch_row($r)[0]; }
echo json_encode($out);
`

const speedFCGI = `
[, $root, $host, $path, $runs, $sock] = $argv + [5 => ""];
$script = $path . "index.php";
$params = ["SCRIPT_FILENAME" => "$root/index.php", "SCRIPT_NAME" => $script, "PHP_SELF" => $script, "REQUEST_URI" => $path,
    "DOCUMENT_ROOT" => $root, "REQUEST_METHOD" => "GET", "QUERY_STRING" => "", "SERVER_PROTOCOL" => "HTTP/1.1",
    "GATEWAY_INTERFACE" => "CGI/1.1", "HTTP_HOST" => $host, "SERVER_NAME" => $host, "SERVER_PORT" => "443", "HTTPS" => "on",
    "REMOTE_ADDR" => "127.0.0.1", "HTTP_USER_AGENT" => "OpenPanel speed test", "CONTENT_LENGTH" => "0"];
$rec = function ($t, $c) { $l = strlen($c); return chr(1) . chr($t) . "\x00\x01" . chr($l >> 8) . chr($l & 255) . "\x00\x00" . $c; };
$nv = function ($n, $v) { $a = strlen($n); $b = strlen($v); return ($a < 128 ? chr($a) : pack("N", $a | 0x80000000)) . ($b < 128 ? chr($b) : pack("N", $b | 0x80000000)) . $n . $v; };
$p = ""; foreach ($params as $k => $v) { $p .= $nv($k, $v); }
for ($i = 0; $i <= $runs; $i++) {
    $fp = $sock !== "" ? @fsockopen("unix://" . $sock, -1, $en, $es, 5) : @fsockopen("127.0.0.1", 9000, $en, $es, 5);
    if (!$fp) { echo "0 000\n"; continue; }
    $t0 = hrtime(true); $ttfb = null; $out = "";
    fwrite($fp, $rec(1, "\x00\x01\x00\x00\x00\x00\x00\x00") . $rec(4, $p) . $rec(4, "") . $rec(5, ""));
    while (!feof($fp)) {
        $h = fread($fp, 8); if (strlen($h) < 8) break;
        $u = unpack("Cver/Ctype/nid/nlen/Cpad/Cres", $h);
        $c = ""; while (strlen($c) < $u["len"]) { $c .= fread($fp, $u["len"] - strlen($c)); }
        if ($u["pad"]) fread($fp, $u["pad"]);
        if ($u["type"] == 6) { if ($ttfb === null) $ttfb = hrtime(true); $out .= $c; }
        if ($u["type"] == 3) break;
    }
    fclose($fp);
    $code = preg_match("/^Status: (\d+)/m", $out, $m) ? $m[1] : 200;
    printf("%.4f %d\n", (($ttfb ?? hrtime(true)) - $t0) / 1e9, $code);
}`

const speedNoCache = `
[, $root, $host, $path] = $argv;
define("WP_REDIS_DISABLED", true);
$script = $path . "index.php";
$_SERVER = array_merge($_SERVER, ["HTTP_HOST" => $host, "SERVER_NAME" => $host, "HTTPS" => "on", "SERVER_PORT" => "443",
    "REQUEST_METHOD" => "GET", "REQUEST_URI" => $path, "SCRIPT_NAME" => $script, "PHP_SELF" => $script,
    "SCRIPT_FILENAME" => "$root/index.php", "DOCUMENT_ROOT" => $root, "REMOTE_ADDR" => "127.0.0.1",
    "HTTP_USER_AGENT" => "OpenPanel speed test", "SERVER_PROTOCOL" => "HTTP/1.1"]);
chdir($root);
$t = hrtime(true);
ob_start(function ($b) { return ""; });
register_shutdown_function(function () use ($t) { while (ob_get_level()) { ob_end_flush(); } fwrite(STDERR, sprintf("%.4f %d\n", (hrtime(true) - $t) / 1e9, http_response_code() ?: 200)); });
require "$root/index.php";`

// ---------------------------------------------------------------- timings

func (s *speed) timings() int {
	B, R, Y, G, N := s.B, s.R, s.Y, s.G, s.N
	token := make([]byte, 12)
	rand.Read(token)
	tok := fmt.Sprintf("%x", token)
	txtFile, phpFile, dbFile := "op-speed-"+tok+".txt", "op-speed-"+tok+".php", "op-speed-db-"+tok+".php"
	defer func() {
		for _, f := range []string{txtFile, phpFile, dbFile} {
			os.Remove(s.hostDocroot + "/" + f)
		}
	}()

	if conf.IsDir(s.hostDocroot) {
		var uid, gid int
		if st, err := os.Stat(s.hostDocroot); err == nil {
			if sys, ok := st.Sys().(*syscall.Stat_t); ok {
				uid, gid = int(sys.Uid), int(sys.Gid)
			}
		}
		os.WriteFile(s.hostDocroot+"/"+txtFile, []byte("OpenPanel speed test\n"), 0o644)
		os.WriteFile(s.hostDocroot+"/"+phpFile, []byte("<?php echo \"ok\";\n"), 0o644)
		// reads the db settings from wp-config.php without loading wordpress, answers only with the right token
		os.WriteFile(s.hostDocroot+"/"+dbFile, []byte(strings.Replace(speedDBProbe, "__TOKEN__", tok, 1)), 0o644)
		for _, f := range []string{txtFile, phpFile, dbFile} {
			os.Chown(s.hostDocroot+"/"+f, uid, gid)
		}
	} else {
		fmt.Printf("%sDocroot %s not found on the host, skipping the .txt/.php/database tests.%s\n", Y, s.hostDocroot, N)
	}

	// container counters before the tests, to see who hit a CPU limit while we load the site
	services := []string{s.webServer}
	if !s.litespeed && s.phpContainer != "" {
		services = append(services, s.phpContainer)
	}
	services = append(services, s.dbContainer)
	if s.isRunning("varnish") {
		services = append(services, "varnish")
	}
	if s.isRunning("redis") {
		services = append(services, "redis")
	}
	throttleBefore := map[string]string{}
	for _, sv := range services {
		if s.isRunning(sv) {
			throttleBefore[sv] = s.throttledUsec(sv)
		}
	}

	// 1. timings
	fmt.Printf("%sHome page (%s) through each layer%s\n", B, s.urlPath, N)

	// the ip this account's sites answer on: dedicated ip if the user has one, otherwise the server's public ip
	expected := ipJSON(conf.UsersDir + "/" + s.owner + "/ip.json")
	if expected == "" {
		if v, ok := curlGet("https://ip.openpanel.com", 3*time.Second, "tcp4"); ok {
			expected = v
		} else {
			expected, _ = curlGet("https://ifconfig.me/ip", 3*time.Second, "tcp4")
		}
	}
	if !regexp.MustCompile(`^[0-9.]+$`).MatchString(expected) {
		expected = ""
		priv := regexp.MustCompile(`^(10|127|172\.(1[6-9]|2[0-9]|3[01])|192\.168)\.`)
		for _, ip := range strings.Fields(hostIPs()) {
			if !priv.MatchString(ip) {
				expected = ip
				break
			}
		}
	}
	serverIPs := " " + hostIPs() + " " + expected + " "
	ipTxt := expected
	if ipTxt == "" {
		ipTxt = "the IP of this server"
	}
	publicIPs := dnsA(s.domain)
	wwwIPs := dnsA("www." + s.domain)
	publicIP := ""
	if len(publicIPs) > 0 {
		publicIP = publicIPs[0]
	}
	pointsHere := func(ips []string) bool {
		for _, ip := range ips {
			if strings.Contains(serverIPs, " "+ip+" ") {
				return true
			}
		}
		return false
	}
	behindCDN := "no"
	if publicIP != "" {
		r := curlTimed(curlOpts{url: "https://" + s.domain + "/", dial: map[string]string{s.domain + ":443": publicIP + ":443"}, timeout: 10 * time.Second}, false)
		if r.hasReply {
			for k, v := range r.respHdr {
				if strings.HasPrefix(strings.ToLower(k), "cf-ray") || (strings.ToLower(k) == "server" && len(v) > 0 && strings.HasPrefix(strings.ToLower(v[0]), "cloudflare")) {
					behindCDN = "cloudflare"
				}
			}
		}
		s.fullTime = true
		s.runTest("public", "Public DNS -> "+publicIP+" (https)", "what visitors get, incl. TLS handshake",
			curlOpts{url: "https://" + s.domain + s.urlPath, dial: map[string]string{s.domain + ":443": publicIP + ":443"}})
		s.fullTime = false
	} else {
		fmt.Printf("  %s %s%s%s   %s\n", padR("Public DNS", 36), R, padL("no A record", 12), N, s.domain+" does not resolve, visitors can't reach it")
	}

	// caddy over https, or plain http when there's no certificate yet
	s.caddyScheme, s.caddyPort = "https", "443"
	if curlTimed(curlOpts{url: "https://" + s.domain + "/", dial: map[string]string{s.domain + ":443": "127.0.0.1:443"}, timeout: 10 * time.Second}, false).code == "000" {
		s.caddyScheme, s.caddyPort = "http", "80"
	}
	s.caddyTest("caddy", "Caddy + WAF (127.0.0.1, "+s.caddyScheme+")", "skips DNS and network", s.urlPath)

	if s.viaVarnish {
		s.runTest("varnish", "Varnish (127.0.0.1:"+s.httpPort+")", "page cache, skips caddy/WAF", curlOpts{
			url:     "http://" + s.domain + s.urlPath,
			dial:    map[string]string{s.domain + ":80": "127.0.0.1:" + s.httpPort},
			headers: [][2]string{{"X-Forwarded-Proto", "https"}},
		})
	} else {
		fmt.Printf("  %s %s   %s\n", padR("Varnish", 36), padL("off", 12), "page cache is not used for "+s.domain)
	}

	s.runTest("web", s.webServer+" (127.0.0.1:"+s.httpsPort+")", "skips caddy/WAF and varnish", s.webOpts(s.urlPath))

	phpBin := "php"
	if !conf.IsFile(s.hostDocroot + "/index.php") {
		fmt.Printf("  %s %s   %s\n", padR("PHP", 36), padL("skipped", 12), "no index.php in the docroot, the home page isn't served by PHP")
	} else if s.phpContainer != "" && s.isRunning(s.phpContainer) {
		if s.inContainer(s.phpContainer, "test", "-x", "/usr/local/bin/php").Quiet().OK() {
			phpBin = "/usr/local/bin/php"
		}
		phpFlags := []string{"-d", "disable_functions=", "-d", "open_basedir=none", "-d", "display_errors=0", "-d", "error_log=/dev/null", "-d", "memory_limit=512M"}

		if !s.litespeed {
			a := append([]string{s.phpContainer, phpBin}, phpFlags...)
			a = append(a, "-r", speedFCGI, s.docroot, s.domain, s.urlPath, strconv.Itoa(s.runs), s.fpmSock)
			out, _ := s.inContainer(a...).NoErr().Out()
			s.ms["fpm"], s.code["fpm"] = median(strings.Split(out, "\n"))
			where := ":9000"
			if s.fpmSock != "" {
				where = " socket"
			}
			s.show("php-fpm ("+s.phpContainer+where+")", "fpm", "FastCGI directly, skips the webserver")
		} else {
			fmt.Printf("  %s %s   %s\n", padR("php-fpm", 36), padL("n/a", 12), s.webServer+" runs PHP inside the webserver")
		}

		var lines []string
		for i := 0; i <= s.runs; i++ {
			a := append([]string{s.phpContainer, phpBin}, phpFlags...)
			a = append(a, "-d", "opcache.enable=0", "-d", "opcache.enable_cli=0", "-r", speedNoCache, s.docroot, s.domain, s.urlPath)
			// 2>&1 >/dev/null | tail -n 1, only stderr is read
			var errOut strings.Builder
			c := s.inContainer(a...).NoOut()
			c.Stderr(&errOut)
			c.Run()
			if t := strings.TrimRight(errOut.String(), "\n"); t != "" {
				ls := strings.Split(t, "\n")
				lines = append(lines, ls[len(ls)-1])
			}
		}
		s.ms["nocache"], s.code["nocache"] = median(lines)
		s.show("PHP, no cache at all", "nocache", "separate process, OPcache off, Redis off")
	} else {
		pc := s.phpContainer
		if pc == "" {
			pc = "?"
		}
		fmt.Printf("  %s %s   %s\n", padR("PHP", 36), padL("skipped", 12), "php container "+pc+" is not running")
	}

	if conf.IsFile(s.hostDocroot + "/" + txtFile) {
		fmt.Println()
		fmt.Printf("%sTest files (removed afterwards)%s\n", B, N)
		s.caddyTest("txt_caddy", ".txt through Caddy + WAF", "no PHP at all", s.urlPath+txtFile)
		s.runTest("txt_web", ".txt from "+s.webServer, "no PHP, no caddy", s.webOpts(s.urlPath+txtFile))
		s.caddyTest("php_caddy", ".php (no database) through Caddy", "PHP without connecting to the database", s.urlPath+phpFile)
		s.runTest("php_web", ".php (no database) from "+s.webServer, "PHP without database, no caddy", s.webOpts(s.urlPath+phpFile))

		o := s.webOpts(s.urlPath + dbFile + "?t=" + tok)
		o.timeout = 20 * time.Second
		dbJSON := curlTimed(o, true).body
		fmt.Println()
		fmt.Printf("%sDatabase%s\n", B, N)
		s.dbReport(dbJSON)
	}

	// 2. resources and settings
	fmt.Println()
	fmt.Printf("%sContainers%s\n", B, N)
	fmt.Printf("  %s %s %s %s %s\n", padR("container", 16), padR("cpu", 8), padR("throttled", 10), padR("memory", 22), "notes")
	for _, sv := range services {
		if !s.isRunning(sv) {
			fmt.Printf("  %s %snot running%s\n", padR(sv, 16), R, N)
			if sv == s.dbContainer || sv == s.phpContainer || sv == s.webServer {
				s.addFinding("0", "3", "Container "+sv+" is not running.", "Start it from OpenPanel > Containers.")
			}
			continue
		}
		prefix := envName(sv)
		cpuLimit, ramLimit := s.envVal(prefix+"_CPU"), s.envVal(prefix+"_RAM")
		after, before := s.throttledUsec(sv), throttleBefore[sv]
		throttledMs := int64(0)
		if isNum(after) && isNum(before) {
			a, _ := strconv.ParseInt(after, 10, 64)
			b, _ := strconv.ParseInt(before, 10, 64)
			throttledMs = (a - b) / 1000
		}
		memLine := s.inContainer(sv, "sh", "-c", `echo "$(cat /sys/fs/cgroup/memory.current 2>/dev/null) $(cat /sys/fs/cgroup/memory.max 2>/dev/null) $(grep -E "^oom_kill " /sys/fs/cgroup/memory.events 2>/dev/null | cut -d" " -f2)"`).NoErr().Str()
		mf := strings.Fields(memLine)
		var memCur, memMax, ooms string
		for i, p := range []*string{&memCur, &memMax, &ooms} {
			if i < len(mf) {
				*p = mf[i]
			}
		}
		memTxt, memPct := "?", int64(0)
		if isNum(memCur) {
			cur, _ := strconv.ParseInt(memCur, 10, 64)
			if isNum(memMax) {
				mx, _ := strconv.ParseInt(memMax, 10, 64)
				memPct = cur * 100 / mx
				memTxt = fmt.Sprintf("%d/%d MB (%d%%)", cur/1048576, mx/1048576, memPct)
			} else {
				memTxt = fmt.Sprintf("%d MB (no limit)", cur/1048576)
			}
		}
		notes := ""
		oomHit := regexp.MustCompile(`^[1-9]`).MatchString(ooms)
		if oomHit {
			notes = R + ooms + " OOM kills" + N
		}
		tcolor := ""
		if throttledMs > 0 {
			tcolor = Y
		}
		cl := cpuLimit
		if cl == "" {
			cl = "?"
		}
		rl := ramLimit
		if rl == "" {
			rl = "?"
		}
		fmt.Printf("  %s %s %s%s%s %s %s\n", padR(sv, 16), padR(cl, 8), tcolor, padR(fmt.Sprintf("%d ms", throttledMs), 10), N, padR(memTxt, 22), notes)
		if throttledMs > 50 {
			s.addFinding(strconv.FormatInt(throttledMs, 10), "2", fmt.Sprintf("%s hit its CPU limit (%s CPU) and was paused for %d ms during this test.", sv, cl, throttledMs), fmt.Sprintf("Raise %s_CPU in %s (now %s) and recreate the container, or raise the plan limits in OpenAdmin.", prefix, s.envFile, cl))
		}
		if memPct >= 90 {
			s.addFinding("0", "2", fmt.Sprintf("%s uses %d%% of its memory limit (%s).", sv, memPct, rl), fmt.Sprintf("Raise %s_RAM in %s (now %s) and recreate the container.", prefix, s.envFile, rl))
		}
		if oomHit {
			s.addFinding("0", "3", fmt.Sprintf("%s was killed %s times for running out of memory (limit %s).", sv, ooms, rl), fmt.Sprintf("Raise %s_RAM in %s and recreate the container.", prefix, s.envFile))
		}
	}

	if s.phpContainer != "" && !s.litespeed && s.isRunning(s.phpContainer) {
		out, _ := podman.Ctx(s.ctx, "logs", "--since", "24h", s.phpContainer).Combined()
		if maxch := grepCount(out, "max_children"); maxch > 0 {
			fmt.Printf("  %s%s reached pm.max_children %d times in the last 24h, requests had to wait for a free PHP worker%s\n", Y, s.phpContainer, maxch, N)
			s.addFinding("0", "2", fmt.Sprintf("PHP-FPM ran out of workers (pm.max_children reached %d times in 24h), visitors wait in a queue.", maxch), "Raise pm.max_children for "+s.phpContainer+" (and its RAM limit), or enable the page cache so fewer requests reach PHP.")
		}
	}

	fmt.Println()
	fmt.Printf("%sServer%s\n", B, N)
	cores := runtime.NumCPU()
	load := loadavg1()
	memAvail, memTotal := meminfo("MemAvailable"), meminfo("MemTotal")
	disk := diskUsedPct("/")
	fmt.Printf("  %s %s\n", padR("load (1 min) / CPU cores", 36), fmt.Sprintf("%s / %d", load, cores))
	fmt.Printf("  %s %s\n", padR("memory available", 36), fmt.Sprintf("%d of %d MB", memAvail, memTotal))
	fmt.Printf("  %s %s\n", padR("disk used (/)", 36), fmt.Sprintf("%d%%", disk))
	if gt(load, strconv.Itoa(cores)) {
		s.addFinding("0", "2", fmt.Sprintf("Server load (%s) is higher than the number of CPU cores (%d), everything on the server is slowed down.", load, cores), "Find what uses the CPU with top, or in OpenAdmin > Server > Processes.")
	}
	if memAvail < memTotal/10 {
		s.addFinding("0", "2", fmt.Sprintf("Only %d MB of memory is free on the server.", memAvail), "Find what uses it: free -m; ps aux --sort=-rss | head")
	}
	if disk >= 90 {
		s.addFinding("0", "2", fmt.Sprintf("Disk is %d%% full.", disk), "Free up space: old backups, logs in /var/log, unused images (podman image prune).")
	}

	fmt.Println()
	fmt.Printf("%sSettings%s\n", B, N)
	wpconfig := s.hostDocroot + "/wp-config.php"
	isWP := conf.IsFile(wpconfig)
	offRe := regexp.MustCompile(`(?im)^[[:space:]]*opcache\.enable[[:space:]]*=[[:space:]]*(0|off|false)`)
	opcacheSite := "on"
	if offRe.MatchString(conf.Read(s.hostDocroot + "/.user.ini")) {
		opcacheSite = "off"
	}
	opcacheGlobal := "on"
	if offRe.MatchString(conf.Read("/home/" + s.ctx + "/php.ini/" + s.phpVersion + ".ini")) {
		opcacheGlobal = "off"
	}
	redisDropin := "no"
	if strings.Contains(conf.Read(s.hostDocroot+"/wp-content/object-cache.php"), "Redis Object Cache Drop-In") {
		redisDropin = "yes"
	}
	via := "no"
	if s.viaVarnish {
		via = "yes"
	}
	fmt.Printf("  %s %s\n", padR("page cache (Varnish)", 36), via)
	fmt.Printf("  %s %s\n", padR("OPcache (PHP "+s.phpVersion+" / this site)", 36), opcacheGlobal+" / "+opcacheSite)
	if isWP {
		fmt.Printf("  %s %s\n", padR("Redis object cache", 36), redisDropin)
		for _, c := range []string{"WP_DEBUG", "SAVEQUERIES"} {
			if regexp.MustCompile(`define\(\s*['"]` + c + `['"]\s*,\s*true`).MatchString(conf.Read(wpconfig)) {
				fmt.Printf("  %s %s%s%s\n", padR(c, 36), Y, "true", N)
				s.addFinding("0", "1", c+" is enabled in wp-config.php, it slows every request.", "Turn it off in OpenPanel > WordPress > site > Debugging, or set define('"+c+"', false); in wp-config.php.")
			}
		}
	}

	// 3. what costs the time
	caddyMs, webMs, varnishMs, fpmMs := s.msv("caddy"), s.msv("web"), s.msv("varnish"), s.msv("fpm")
	txtWeb, phpWeb, publicMs := s.msv("txt_web"), s.msv("php_web"), s.msv("public")
	backendMs := webMs
	if s.viaVarnish {
		backendMs = varnishMs
	}

	if caddyMs != "" && backendMs != "" {
		wafCost := subMs(caddyMs, backendMs)
		if awkNum(wafCost) > 50 {
			switch {
			case s.bodyAccess == "on" && s.bodySource == "domain":
				s.addFinding(wafCost, "3", fmt.Sprintf("Caddy + WAF add %s ms to every page. The WAF buffers and inspects every HTML response because %s has SecResponseBodyAccess On.", wafCost, s.caddyConf), fmt.Sprintf(`Remove that line for this domain, request protection stays on:\n       sed -i '/^[[:space:]]*SecResponseBodyAccess On/d' %s && podman exec caddy caddy reload --config /etc/caddy/Caddyfile`, s.caddyConf))
			case s.bodyAccess == "on":
				s.addFinding(wafCost, "3", fmt.Sprintf("Caddy + WAF add %s ms to every page. The WAF buffers and inspects every HTML response (SecResponseBodyAccess On in %s), which is the usual cause.", wafCost, s.coraza), fmt.Sprintf(`Turn off response body inspection for all domains, request protection stays on:\n       sed -i 's/^SecResponseBodyAccess On/SecResponseBodyAccess Off/' %s && podman restart caddy\n       (a caddy reload is not enough, the WAF only rereads that file on restart)`, s.coraza))
			case s.wafOn:
				s.addFinding(wafCost, "2", fmt.Sprintf("Caddy + WAF add %s ms to every page.", wafCost), "Check which rules match in OpenPanel > WAF > Log, or compare with the WAF off for this domain: opencli waf domain "+s.domain+" disable")
			default:
				s.addFinding(wafCost, "2", fmt.Sprintf("Caddy adds %s ms to every page although the WAF is off for this domain.", wafCost), "Check the caddy container: podman logs --tail 50 caddy")
			}
		}
	}

	// network + TLS between visitors and the server
	if publicMs != "" && caddyMs != "" {
		netCost := subMs(publicMs, caddyMs)
		if awkNum(netCost) > 150 {
			s.addFinding(netCost, "1", fmt.Sprintf("Going through public DNS adds %s ms (network, TLS handshake, or a CDN/proxy in front of the server).", netCost), "Test from outside the server too, and check the CDN if there is one. The layers measured on the server are the ones OpenPanel controls.")
		}
	}

	// no page cache
	if !s.viaVarnish && webMs != "" && gt(webMs, "100") {
		fix := "Turn on Varnish in OpenPanel > Cache > Varnish and enable it for " + s.domain + "."
		if isWP {
			fix = "Turn on the page cache in OpenPanel > WordPress > site > Cache tab."
		}
		s.addFinding(subMs(webMs, "5"), "2", fmt.Sprintf("No page cache: every visit makes PHP build the page (%s ms). With Varnish most visits take a few ms.", webMs), fix)
	}

	// slow application when a page isn't cached
	if fpmMs != "" && gt(fpmMs, "300") {
		var cause, fix string
		if isWP {
			cause = "plugins, theme or database queries"
			fix = "Profile the site with the Query Monitor plugin to find slow plugins or queries."
			if redisDropin == "no" {
				fix = "Enable the Redis object cache in OpenPanel > WordPress > site > Cache tab. " + fix
			}
		} else {
			cause = "the application's code or its database queries"
			fix = "Profile the application to find slow code or queries, and use a page or object cache where it supports one."
		}
		pw := phpWeb
		if pw == "" {
			pw = "0"
		}
		pwShow := phpWeb
		if pwShow == "" {
			pwShow = "?"
		}
		s.addFinding(subMs(fpmMs, pw), "2", fmt.Sprintf("The site needs %s ms to build a page (a PHP file without database takes %s ms), so the time goes to %s.", fpmMs, pwShow, cause), fix)
	}

	// plain PHP slow = php-fpm itself
	if phpWeb != "" && txtWeb != "" && awkNum(subMs(phpWeb, txtWeb)) > 30 {
		d := subMs(phpWeb, txtWeb)
		s.addFinding(d, "2", fmt.Sprintf("Even a PHP file without database takes %s ms (a static file %s ms), PHP-FPM itself is slow or busy.", phpWeb, txtWeb), "Check the "+s.phpContainer+" CPU/memory limits above and pm.max_children.")
	}

	// static file slow = webserver
	if txtWeb != "" && gt(txtWeb, "30") {
		s.addFinding(txtWeb, "2", fmt.Sprintf("%s needs %s ms for a tiny static file (normally 1-5 ms).", s.webServer, txtWeb), "Check the "+s.webServer+" container limits above.")
	}

	if opcacheGlobal == "off" {
		s.addFinding("0", "2", "OPcache is disabled in the PHP "+s.phpVersion+" settings, PHP recompiles every file on every request.", "Set opcache.enable=1 in OpenPanel > PHP > PHP.INI Editor, then restart "+s.phpContainer+".")
	}
	if opcacheSite == "off" && opcacheGlobal == "on" {
		fix := "Remove the opcache.enable=0 line from " + s.hostDocroot + "/.user.ini."
		if isWP {
			fix = "Turn it on in OpenPanel > WordPress > site > Cache tab."
		}
		s.addFinding("0", "2", "OPcache is turned off for this site (.user.ini).", fix)
	}

	// dns, severity 4 so it always comes first: nothing else matters if visitors don't reach the server
	// lead sentence depends on whether the site answers locally
	dnsLead := "DNS is the issue"
	if caddyMs != "" && s.code["caddy"] == "200" {
		dnsLead = fmt.Sprintf("The site itself loads fine on this server (%s ms through Caddy), but DNS is the issue", caddyMs)
	}
	pointFix := fmt.Sprintf("Point the domain to %s: at your DNS provider set an A record for %s (and www.%s) to %s. If the domain uses this server's nameservers, check its zone in OpenPanel > Domains > DNS Zone Editor. Changes can take up to a few hours to spread.", ipTxt, s.domain, s.domain, ipTxt)
	if publicIP == "" {
		s.addFinding("0", "4", fmt.Sprintf("%s: %s has no A record, so visitors can't reach it.", dnsLead, s.domain), pointFix)
	} else if !pointsHere(publicIPs) {
		if behindCDN == "cloudflare" {
			s.addFinding("0", "1", fmt.Sprintf("%s is proxied through Cloudflare (%s), so visitors reach this server through it.", s.domain, publicIP), fmt.Sprintf("If the site is slow only from outside, check the Cloudflare settings. Cloudflare's origin (DNS record) for %s should be %s.", s.domain, ipTxt))
		} else {
			s.addFinding("0", "4", fmt.Sprintf("%s: %s points to %sinstead of this server (%s), so visitors reach a different server.", dnsLead, s.domain, strings.Join(publicIPs, " ")+" ", ipTxt), pointFix)
		}
	} else if publicMs == "" {
		s.addFinding("0", "4", fmt.Sprintf("%s points to this server (%s), but the site doesn't answer over the public IP.", s.domain, publicIP), "Check that ports 80/443 are open in the firewall (OpenAdmin > Security > Firewall) and that "+s.domain+" has an SSL certificate (OpenPanel > Domains > SSL Certificates).")
	}
	if publicIP != "" && behindCDN == "no" {
		if len(wwwIPs) == 0 {
			s.addFinding("0", "1", fmt.Sprintf("www.%s has no DNS record, visitors typing www get an error.", s.domain), fmt.Sprintf("Add an A record for www.%s pointing to %s (or a CNAME to %s).", s.domain, ipTxt, s.domain))
		} else if !pointsHere(wwwIPs) {
			s.addFinding("0", "2", fmt.Sprintf("www.%s points to %sinstead of this server (%s).", s.domain, strings.Join(wwwIPs, " ")+" ", ipTxt), fmt.Sprintf("Change the A record for www.%s to %s (or a CNAME to %s).", s.domain, ipTxt, s.domain))
		}
	}

	// 4. verdict
	fmt.Println()
	fmt.Printf("%s======================================================================%s\n", B, N)
	fmt.Printf("%sDiagnosis for %s%s\n", B, s.site, N)
	fmt.Printf("%s======================================================================%s\n", B, N)
	if len(s.findings) == 0 {
		shown := publicMs
		if shown == "" {
			shown = caddyMs
		}
		if shown == "" {
			shown = "?"
		}
		fmt.Printf("%sNo problems found.%s Visitors get the page in %s ms.\n", G, N, shown)
		return 0
	}

	// biggest measured cost first, then severity
	type srt struct {
		flag, padded, line string
		sev                float64
		f                  finding
	}
	var sorted []srt
	for _, f := range s.findings {
		flag := "0"
		if awkNum(f.sev) >= 4 {
			flag = "1"
		}
		padded := fmt.Sprintf("%010d", int64(awkNum(f.impact)))
		line := flag + "|" + padded + "|" + f.impact + "|" + f.sev + "|" + f.text + "|" + f.fix
		sorted = append(sorted, srt{flag, padded, line, awkNum(f.sev), f})
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.flag != b.flag {
			return a.flag > b.flag
		}
		if a.padded != b.padded {
			return a.padded > b.padded
		}
		if a.sev != b.sev {
			return a.sev > b.sev
		}
		return a.line < b.line
	})
	for i, e := range sorted {
		f := e.f
		if i == 0 {
			fmt.Println()
			fmt.Printf("%s%sBiggest issue:%s %s\n", R, B, N, f.text)
			if f.impact != "0" {
				fmt.Printf("  %sImpact:%s about %s ms per request\n", B, N, f.impact)
			}
			fmt.Printf("  %sFix:%s %s\n", B, N, printfB(f.fix))
			if len(sorted) > 1 {
				fmt.Println()
				fmt.Printf("%sAlso:%s\n", B, N)
			}
		} else {
			c := ""
			switch f.sev {
			case "3", "4":
				c = R
			case "2":
				c = Y
			}
			fmt.Printf("  %s%d.%s %s\n", c, i, N, f.text)
			if f.impact != "0" {
				fmt.Printf("     about %s ms\n", f.impact)
			}
			fmt.Printf("     Fix: %s\n", printfB(f.fix))
		}
	}
	fmt.Println()
	return 0
}

func meminfo(key string) int {
	for _, l := range conf.Lines("/proc/meminfo") {
		if strings.HasPrefix(l, key) {
			f := strings.Fields(l)
			if len(f) > 1 {
				v, _ := strconv.Atoi(f[1])
				return v / 1024
			}
		}
	}
	return 0
}

// diskUsedPct is the Use% column of df -P
func diskUsedPct(path string) int {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0
	}
	used := st.Blocks - st.Bfree
	avail := st.Bavail
	if used+avail == 0 {
		return 0
	}
	return int((used*100 + used + avail - 1) / (used + avail))
}

func (s *speed) dbReport(body string) {
	B, R, Y, N := s.B, s.R, s.Y, s.N
	o, err := ojson.ParseObject(body)
	if err != nil {
		fmt.Printf("  %scould not run the database probe (no wp-config.php or PHP error)%s\n", Y, N)
		return
	}
	get := func(k, def string) string {
		v, ok := o.Get(k)
		if !ok || v == nil {
			return def
		}
		if b, isB := v.(bool); isB && !b {
			return def
		}
		return ojson.Text(v)
	}
	_ = B
	dbErr := get("error", "")
	if dbErr == "no wp-config.php" {
		fmt.Println("  skipped, no wp-config.php (not a WordPress site)")
		return
	}
	if dbErr != "" {
		fmt.Printf("  %s%s%s\n", R, dbErr, N)
		s.addFinding("0", "3", "Database check failed: "+dbErr, "Check that the "+s.dbContainer+" container is running and the DB_* settings in wp-config.php are correct.")
		return
	}
	dbConnect, dbSelect := get("connect_ms", "null"), get("select1_ms", "null")
	autoKB, autoRows := get("autoload_kb", "0"), get("autoload_rows", "0")
	autoMS, plugins, transients := get("autoload_ms", "0"), get("active_plugins", "?"), get("transients", "0")
	fmt.Printf("  %s %s ms\n", padR("connect to "+s.dbContainer, 36), padL(dbConnect, 9))
	fmt.Printf("  %s %s ms\n", padR("SELECT 1", 36), padL(dbSelect, 9))
	fmt.Printf("  %s %s ms   %s KB in %s rows\n", padR("autoloaded options", 36), padL(autoMS, 9), autoKB, autoRows)
	fmt.Printf("  %s %s\n", padR("active plugins", 36), padL(plugins, 12))
	fmt.Printf("  %s %s\n", padR("transients in wp_options", 36), padL(transients, 12))
	if gt(dbConnect, "50") {
		s.addFinding(dbConnect, "2", fmt.Sprintf("Connecting to the database takes %s ms (normally under 5 ms).", dbConnect), "Check the "+s.dbContainer+" container's CPU/memory limits and the server load below.")
	}
	if gt(dbSelect, "5") {
		s.addFinding(dbSelect, "2", fmt.Sprintf("The database answers a trivial query in %s ms (normally under 1 ms), it is overloaded or throttled.", dbSelect), "Check the "+s.dbContainer+" limits below, and what is running with SHOW FULL PROCESSLIST in phpMyAdmin.")
	}
	if gt(autoKB, "800") {
		s.addFinding(autoMS, "2", fmt.Sprintf("WordPress loads %s KB of autoloaded options on every request (keep it under 800 KB).", autoKB), "Find the biggest ones: SELECT option_name, LENGTH(option_value) FROM wp_options WHERE autoload IN ('yes','on','auto-on','auto') ORDER BY 2 DESC LIMIT 20; then remove leftovers of old plugins.")
	}
	if gt(transients, "5000") {
		s.addFinding("0", "1", transients+" transients are stored in wp_options.", "Clean them up with: wp transient delete --expired (or enable the Redis object cache, which keeps transients out of the database).")
	}
	if plugins != "?" && gt(plugins, "40") {
		s.addFinding("0", "1", plugins+" plugins are active, every one of them runs on every uncached request.", "Deactivate plugins that aren't needed.")
	}
}
