#!/bin/bash
################################################################################
# Script Name: domains/test.sh
# Description: Test a website speed through every layer (DNS, Caddy/WAF, Varnish, webserver, PHP, database), check limits and settings, and show the biggest issue with a fix.
# Usage: opencli domains-test <DOMAIN_NAME>[/SUBFOLDER] [RUNS]
# Author: Stefan Pejcic
# Created: 28.09.2026
# Last Modified: 28.09.2026
# Company: OpenPanel, LLC.
# Copyright (c) openpanel.com
# 
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
# 
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
# 
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
# THE SOFTWARE.
################################################################################

# shellcheck disable=SC1091
. /usr/local/opencli/db.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/podman.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/requirement.sh
require_command jq
require_command curl
if command -v apt-get &> /dev/null; then require_command dig dnsutils; else require_command dig bind-utils; fi

set -u

if [ -z "${1:-}" ]; then
    echo "Usage: opencli domains-test <DOMAIN_NAME>[/SUBFOLDER] [RUNS]"
    echo "Example: opencli domains-test example.com"
    exit 1
fi
SITE="${1,,}"
RUNS="${2:-5}"
[[ "$RUNS" =~ ^[1-9][0-9]*$ ]] || { echo "runs must be a positive number"; exit 1; }
DOMAIN="${SITE%%/*}"
FOLDER=""
[[ "$SITE" == */* ]] && FOLDER="${SITE#*/}"
URLPATH="/${FOLDER:+$FOLDER/}"


B=$'\e[1m'; R=$'\e[31m'; Y=$'\e[33m'; G=$'\e[32m'; N=$'\e[0m'
[ -t 1 ] || { B=""; R=""; Y=""; G=""; N=""; }

# ======================================================================
# lookups

DB_ROW=$(timeout 10 mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -sN -e \
    "SELECT u.username, u.server, d.docroot, d.php_version FROM domains d JOIN users u ON u.id = d.user_id WHERE d.domain_url = '$(mysql_escape "$DOMAIN")' LIMIT 1")
read -r OWNER CONTEXT DOCROOT DB_PHP_VERSION <<<"$DB_ROW"
if [ -z "${OWNER:-}" ]; then
    echo "Domain $DOMAIN is not hosted on this server."
    exit 1
fi
ENV_FILE="/home/$CONTEXT/.env"
[ -f "$ENV_FILE" ] || { echo "Could not find $ENV_FILE for user $OWNER."; exit 1; }
env_val() { grep -E "^$1=" "$ENV_FILE" | head -1 | cut -d= -f2- | tr -d '"'; }
env_port() { env_val "$1" | awk -F: '{print $(NF-1)}'; }

WEB_SERVER=$(env_val WEB_SERVER)
HTTP_PORT=$(env_port HTTP_PORT)
HTTPS_PORT=$(env_port HTTPS_PORT)
PROXY_PORT=$(grep -qE '^PROXY_HTTP_PORT=' "$ENV_FILE" && env_port PROXY_HTTP_PORT)
LITESPEED=no; [[ "$WEB_SERVER" == *litespeed* ]] && LITESPEED=yes

[[ "${DOCROOT:-}" != /var/www/html/* ]] && { echo "Could not detect the docroot for $DOMAIN."; exit 1; }
[ -n "$FOLDER" ] && DOCROOT="$DOCROOT/$FOLDER"
HOST_DOCROOT="/home/$CONTEXT/docker-data/volumes/${CONTEXT}_html_data/_data/${DOCROOT#/var/www/html/}"
VHOST="/home/$CONTEXT/docker-data/volumes/${CONTEXT}_webserver_data/_data/$DOMAIN.conf"
PHP_CONTAINER=$(grep -o 'php-fpm-[0-9.]*' "$VHOST" 2>/dev/null | head -1)
[ -z "$PHP_CONTAINER" ] && [ -n "${DB_PHP_VERSION:-}" ] && PHP_CONTAINER="php-fpm-$DB_PHP_VERSION"
[ "$LITESPEED" = yes ] && PHP_CONTAINER="$WEB_SERVER"
PHP_VERSION="${PHP_CONTAINER#php-fpm-}"
DB_CONTAINER=$(env_val MYSQL_TYPE); DB_CONTAINER="${DB_CONTAINER:-mysql}"

RUNNING=$(podman_ctx "$CONTEXT" ps --format '{{.Names}}' 2>/dev/null)
is_running() { grep -qx "$1" <<<"$RUNNING"; }
CADDY_CONF="/etc/openpanel/caddy/domains/$DOMAIN.conf"
# same check as domains-varnish: the direct https proxy is commented out when traffic goes through varnish
VARNISH_DOMAIN=off; grep -q "^#.*reverse_proxy https://127.0.0.1" "$CADDY_CONF" 2>/dev/null && VARNISH_DOMAIN=on
VIA_VARNISH=no; is_running varnish && [ -n "$PROXY_PORT" ] && [ "$VARNISH_DOMAIN" = on ] && VIA_VARNISH=yes

CORAZA_RULES="/etc/openpanel/caddy/coraza_rules.conf"
WAF_ON=no; grep -qE '^[[:space:]]*SecRuleEngine[[:space:]]+On' "$CADDY_CONF" 2>/dev/null && WAF_ON=yes
BODY_ACCESS=off; BODY_SOURCE=""
if [ "$WAF_ON" = yes ]; then
    grep -qE '^SecResponseBodyAccess[[:space:]]+On' "$CORAZA_RULES" 2>/dev/null && { BODY_ACCESS=on; BODY_SOURCE=global; }
    # a per-domain line after the includes wins
    grep -qE '^[[:space:]]*SecResponseBodyAccess[[:space:]]+Off' "$CADDY_CONF" && { BODY_ACCESS=off; BODY_SOURCE=""; }
    grep -qE '^[[:space:]]*SecResponseBodyAccess[[:space:]]+On' "$CADDY_CONF" && { BODY_ACCESS=on; BODY_SOURCE=domain; }
fi

echo "${B}Site${N}         $SITE  (user $OWNER, $WEB_SERVER, ${PHP_CONTAINER:-no php container found}, $DB_CONTAINER)"
echo "${B}Docroot${N}      $DOCROOT"
echo "${B}Varnish${N}      container: $(is_running varnish && echo running || echo stopped), domain: ${VARNISH_DOMAIN:-?}"
echo "${B}WAF${N}          $WAF_ON$([ "$WAF_ON" = yes ] && echo ", response body inspection: $BODY_ACCESS")"
echo "${B}Runs${N}         1 warm-up + $RUNS measured per test, median time to first byte (local tests without connect/TLS setup)"
echo

# ======================================================================
# helpers

declare -A MS CODE
FINDINGS=()   # "impact_ms|severity|text|fix"
add_finding() { FINDINGS+=("$1|$2|$3|$4"); }

# stdin: "seconds code" lines, first one is the warm-up; prints "median_ms code"
median() {
    tail -n +2 | sort -n | awk '{ t[++n] = $1 * 1000; c = $2 } END { if (!n) { print "- 000"; exit } m = (n % 2) ? t[(n + 1) / 2] : (t[n / 2] + t[n / 2 + 1]) / 2; printf "%.1f %s\n", m, c }'
}

# stops after the warm-up if nothing answers, so a dead layer doesn't hang every run
# local tests leave out connect + TLS setup so https and http layers compare fairly, FULL_TIME=1 keeps it
curl_runs() {
    local line i
    for i in $(seq 0 "$RUNS"); do
        line=$(curl -sk -o /dev/null --max-time 15 -A "OpenPanel speed test" -w '%{time_starttransfer} %{time_pretransfer} %{http_code}' "$@")
        [ "$i" -eq 0 ] && [ "${line##* }" = "000" ] && return
        awk -v full="${FULL_TIME:-0}" '{ printf "%.6f %s\n", full ? $1 : $1 - $2, $3 }' <<<"$line"
    done
}

# key label note -- curl args...
run_test() {
    local key="$1" label="$2" note="$3"; shift 4
    local res; res=$(curl_runs "$@" | median)
    MS[$key]="${res% *}"; CODE[$key]="${res#* }"
    show "$label" "$key" "$note"
}

show() {
    local label="$1" key="$2" note="$3"
    if [ "${MS[$key]:-}" = "-" ] || [ -z "${MS[$key]:-}" ]; then
        printf "  %-36s ${R}%12s${N}   %s\n" "$label" "no answer" "$note"
    else
        local color=""; [ "${CODE[$key]}" != 200 ] && color="$Y"
        printf "  %-36s %9s ms   ${color}HTTP %s${N}   %s\n" "$label" "${MS[$key]}" "${CODE[$key]}" "$note"
    fi
}

ms() { local v="${MS[$1]:-}"; { [ -z "$v" ] || [ "$v" = "-" ]; } && echo "" || echo "$v"; }
gt() { awk -v a="$1" -v b="$2" 'BEGIN { exit !(a > b) }'; }
sub_ms() { awk -v a="$1" -v b="$2" 'BEGIN { printf "%.0f", a - b }'; }

in_container() { podman_ctx "$CONTEXT" exec "$@"; }

# ======================================================================
# temporary test files

TOKEN=$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')
TXT_FILE="op-speed-$TOKEN.txt"
PHP_FILE="op-speed-$TOKEN.php"
DB_FILE="op-speed-db-$TOKEN.php"
cleanup() { rm -f "$HOST_DOCROOT/$TXT_FILE" "$HOST_DOCROOT/$PHP_FILE" "$HOST_DOCROOT/$DB_FILE"; }
trap cleanup EXIT

if [ -d "$HOST_DOCROOT" ]; then
    OWNER_UID=$(stat -c '%u:%g' "$HOST_DOCROOT")
    printf 'OpenPanel speed test\n' > "$HOST_DOCROOT/$TXT_FILE"
    printf '<?php echo "ok";\n' > "$HOST_DOCROOT/$PHP_FILE"
    # reads the db settings from wp-config.php without loading wordpress, answers only with the right token
    cat > "$HOST_DOCROOT/$DB_FILE" <<'PHP'
<?php
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
PHP
    sed -i "s/__TOKEN__/$TOKEN/" "$HOST_DOCROOT/$DB_FILE"
    chown "$OWNER_UID" "$HOST_DOCROOT/$TXT_FILE" "$HOST_DOCROOT/$PHP_FILE" "$HOST_DOCROOT/$DB_FILE" 2>/dev/null
else
    echo "${Y}Docroot $HOST_DOCROOT not found on the host, skipping the .txt/.php/database tests.${N}"
fi

# ======================================================================
# container counters before the tests, to see who hit a CPU limit while we load the site

SERVICES=("$WEB_SERVER")
[ "$LITESPEED" = no ] && [ -n "$PHP_CONTAINER" ] && SERVICES+=("$PHP_CONTAINER")
SERVICES+=("$DB_CONTAINER")
is_running varnish && SERVICES+=(varnish)
is_running redis && SERVICES+=(redis)
declare -A THROTTLE_BEFORE
throttled_usec() { in_container "$1" sh -c 'grep -E "^throttled_usec" /sys/fs/cgroup/cpu.stat 2>/dev/null | cut -d" " -f2' 2>/dev/null; }
for s in "${SERVICES[@]}"; do is_running "$s" && THROTTLE_BEFORE[$s]=$(throttled_usec "$s"); done

# ======================================================================
# 1. timings

echo "${B}Home page ($URLPATH) through each layer${N}"

# the ip this account's sites answer on: dedicated ip if the user has one, otherwise the server's public ip
EXPECTED_IP=$(jq -r '.ip // empty' "/etc/openpanel/openpanel/core/users/$OWNER/ip.json" 2>/dev/null)
[ -z "$EXPECTED_IP" ] && EXPECTED_IP=$(curl --silent --max-time 3 -4 https://ip.openpanel.com 2>/dev/null || curl --silent --max-time 3 -4 https://ifconfig.me/ip 2>/dev/null)
[[ "$EXPECTED_IP" =~ ^[0-9.]+$ ]] || EXPECTED_IP=$(hostname -I | tr ' ' '\n' | grep -vE '^(10|127|172\.(1[6-9]|2[0-9]|3[01])|192\.168)\.' | head -1)
SERVER_IPS=" $(hostname -I) $EXPECTED_IP "
IP_TXT="${EXPECTED_IP:-the IP of this server}"
dns_a() { dig +short "$1" A @1.1.1.1 2>/dev/null | grep -E '^[0-9.]+$'; }
PUBLIC_IPS=$(dns_a "$DOMAIN"); WWW_IPS=$(dns_a "www.$DOMAIN")
PUBLIC_IP=$(head -1 <<<"$PUBLIC_IPS")
points_here() { local ip; for ip in $1; do [[ "$SERVER_IPS" == *" $ip "* ]] && return 0; done; return 1; }
BEHIND_CDN=no
if [ -n "$PUBLIC_IP" ]; then
    curl -sk -o /dev/null -D - --max-time 10 --resolve "$DOMAIN:443:$PUBLIC_IP" "https://$DOMAIN/" 2>/dev/null | grep -qiE '^(cf-ray|server: cloudflare)' && BEHIND_CDN=cloudflare
    FULL_TIME=1 run_test public "Public DNS -> $PUBLIC_IP (https)" "what visitors get, incl. TLS handshake" -- --resolve "$DOMAIN:443:$PUBLIC_IP" "https://$DOMAIN$URLPATH"
else
    printf "  %-36s ${R}%12s${N}   %s\n" "Public DNS" "no A record" "$DOMAIN does not resolve, visitors can't reach it"
fi

# caddy over https, or plain http when there's no certificate yet
CADDY_SCHEME=https; CADDY_PORT=443
[ "$(curl -sk -o /dev/null --max-time 10 -w '%{http_code}' --resolve "$DOMAIN:443:127.0.0.1" "https://$DOMAIN/")" = 000 ] && { CADDY_SCHEME=http; CADDY_PORT=80; }
caddy_test() { run_test "$1" "$2" "$3" -- --resolve "$DOMAIN:$CADDY_PORT:127.0.0.1" "$CADDY_SCHEME://$DOMAIN$4"; }
caddy_test caddy "Caddy + WAF (127.0.0.1, $CADDY_SCHEME)" "skips DNS and network" "$URLPATH"

if [ "$VIA_VARNISH" = yes ]; then
    run_test varnish "Varnish (127.0.0.1:$HTTP_PORT)" "page cache, skips caddy/WAF" -- --connect-to "$DOMAIN:80:127.0.0.1:$HTTP_PORT" -H "X-Forwarded-Proto: https" "http://$DOMAIN$URLPATH"
else
    printf "  %-36s %12s   %s\n" "Varnish" "off" "page cache is not used for $DOMAIN"
fi

run_test web "$WEB_SERVER (127.0.0.1:$HTTPS_PORT)" "skips caddy/WAF and varnish" -- --connect-to "$DOMAIN:443:127.0.0.1:$HTTPS_PORT" "https://$DOMAIN$URLPATH"

PHPBIN=php
if [ ! -f "$HOST_DOCROOT/index.php" ]; then
    printf "  %-36s %12s   %s\n" "PHP" "skipped" "no index.php in the docroot, the home page isn't served by PHP"
elif [ -n "$PHP_CONTAINER" ] && is_running "$PHP_CONTAINER"; then
    in_container "$PHP_CONTAINER" test -x /usr/local/bin/php 2>/dev/null && PHPBIN=/usr/local/bin/php
    PHP_FLAGS=(-d disable_functions= -d open_basedir=none -d display_errors=0 -d error_log=/dev/null -d memory_limit=512M)

    if [ "$LITESPEED" = no ]; then
        FCGI_PHP='
[, $root, $host, $path, $runs] = $argv;
$script = $path . "index.php";
$params = ["SCRIPT_FILENAME" => "$root/index.php", "SCRIPT_NAME" => $script, "PHP_SELF" => $script, "REQUEST_URI" => $path,
    "DOCUMENT_ROOT" => $root, "REQUEST_METHOD" => "GET", "QUERY_STRING" => "", "SERVER_PROTOCOL" => "HTTP/1.1",
    "GATEWAY_INTERFACE" => "CGI/1.1", "HTTP_HOST" => $host, "SERVER_NAME" => $host, "SERVER_PORT" => "443", "HTTPS" => "on",
    "REMOTE_ADDR" => "127.0.0.1", "HTTP_USER_AGENT" => "OpenPanel speed test", "CONTENT_LENGTH" => "0"];
$rec = function ($t, $c) { $l = strlen($c); return chr(1) . chr($t) . "\x00\x01" . chr($l >> 8) . chr($l & 255) . "\x00\x00" . $c; };
$nv = function ($n, $v) { $a = strlen($n); $b = strlen($v); return ($a < 128 ? chr($a) : pack("N", $a | 0x80000000)) . ($b < 128 ? chr($b) : pack("N", $b | 0x80000000)) . $n . $v; };
$p = ""; foreach ($params as $k => $v) { $p .= $nv($k, $v); }
for ($i = 0; $i <= $runs; $i++) {
    $fp = @fsockopen("127.0.0.1", 9000, $en, $es, 5);
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
}'
        res=$(in_container "$PHP_CONTAINER" "$PHPBIN" "${PHP_FLAGS[@]}" -r "$FCGI_PHP" "$DOCROOT" "$DOMAIN" "$URLPATH" "$RUNS" 2>/dev/null | median)
        MS[fpm]="${res% *}"; CODE[fpm]="${res#* }"
        show "php-fpm ($PHP_CONTAINER:9000)" fpm "FastCGI directly, skips the webserver"
    else
        printf "  %-36s %12s   %s\n" "php-fpm" "n/a" "$WEB_SERVER runs PHP inside the webserver"
    fi

    NOCACHE_PHP='
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
require "$root/index.php";'
    res=$(for _ in $(seq 0 "$RUNS"); do
        in_container "$PHP_CONTAINER" "$PHPBIN" "${PHP_FLAGS[@]}" -d opcache.enable=0 -d opcache.enable_cli=0 \
            -r "$NOCACHE_PHP" "$DOCROOT" "$DOMAIN" "$URLPATH" 2>&1 >/dev/null | tail -n 1
    done | median)
    MS[nocache]="${res% *}"; CODE[nocache]="${res#* }"
    show "PHP, no cache at all" nocache "separate process, OPcache off, Redis off"
else
    printf "  %-36s %12s   %s\n" "PHP" "skipped" "php container ${PHP_CONTAINER:-?} is not running"
fi

if [ -f "$HOST_DOCROOT/$TXT_FILE" ]; then
    echo
    echo "${B}Test files (removed afterwards)${N}"
    caddy_test txt_caddy ".txt through Caddy + WAF" "no PHP at all" "$URLPATH$TXT_FILE"
    run_test txt_web ".txt from $WEB_SERVER" "no PHP, no caddy" -- --connect-to "$DOMAIN:443:127.0.0.1:$HTTPS_PORT" "https://$DOMAIN$URLPATH$TXT_FILE"
    caddy_test php_caddy ".php (no database) through Caddy" "PHP without connecting to the database" "$URLPATH$PHP_FILE"
    run_test php_web ".php (no database) from $WEB_SERVER" "PHP without database, no caddy" -- --connect-to "$DOMAIN:443:127.0.0.1:$HTTPS_PORT" "https://$DOMAIN$URLPATH$PHP_FILE"

    DB_JSON=$(curl -sk --max-time 20 --connect-to "$DOMAIN:443:127.0.0.1:$HTTPS_PORT" "https://$DOMAIN$URLPATH$DB_FILE?t=$TOKEN")
    echo
    echo "${B}Database${N}"
    if jq -e . >/dev/null 2>&1 <<<"$DB_JSON"; then
        DB_ERR=$(jq -r '.error // empty' <<<"$DB_JSON")
        if [ "$DB_ERR" = "no wp-config.php" ]; then
            echo "  skipped, no wp-config.php (not a WordPress site)"
        elif [ -n "$DB_ERR" ]; then
            echo "  ${R}$DB_ERR${N}"
            add_finding 0 3 "Database check failed: $DB_ERR" "Check that the $DB_CONTAINER container is running and the DB_* settings in wp-config.php are correct."
        else
            DB_CONNECT=$(jq -r '.connect_ms' <<<"$DB_JSON"); DB_SELECT=$(jq -r '.select1_ms' <<<"$DB_JSON")
            AUTOLOAD_KB=$(jq -r '.autoload_kb // 0' <<<"$DB_JSON"); AUTOLOAD_ROWS=$(jq -r '.autoload_rows // 0' <<<"$DB_JSON")
            AUTOLOAD_MS=$(jq -r '.autoload_ms // 0' <<<"$DB_JSON"); PLUGINS=$(jq -r '.active_plugins // "?"' <<<"$DB_JSON")
            TRANSIENTS=$(jq -r '.transients // 0' <<<"$DB_JSON")
            printf "  %-36s %9s ms\n" "connect to $DB_CONTAINER" "$DB_CONNECT"
            printf "  %-36s %9s ms\n" "SELECT 1" "$DB_SELECT"
            printf "  %-36s %9s ms   %s KB in %s rows\n" "autoloaded options" "$AUTOLOAD_MS" "$AUTOLOAD_KB" "$AUTOLOAD_ROWS"
            printf "  %-36s %12s\n" "active plugins" "$PLUGINS"
            printf "  %-36s %12s\n" "transients in wp_options" "$TRANSIENTS"
            gt "$DB_CONNECT" 50 && add_finding "$DB_CONNECT" 2 "Connecting to the database takes ${DB_CONNECT} ms (normally under 5 ms)." "Check the $DB_CONTAINER container's CPU/memory limits and the server load below."
            gt "$DB_SELECT" 5 && add_finding "$DB_SELECT" 2 "The database answers a trivial query in ${DB_SELECT} ms (normally under 1 ms), it is overloaded or throttled." "Check the $DB_CONTAINER limits below, and what is running with SHOW FULL PROCESSLIST in phpMyAdmin."
            gt "$AUTOLOAD_KB" 800 && add_finding "$AUTOLOAD_MS" 2 "WordPress loads ${AUTOLOAD_KB} KB of autoloaded options on every request (keep it under 800 KB)." "Find the biggest ones: SELECT option_name, LENGTH(option_value) FROM wp_options WHERE autoload IN ('yes','on','auto-on','auto') ORDER BY 2 DESC LIMIT 20; then remove leftovers of old plugins."
            gt "$TRANSIENTS" 5000 && add_finding 0 1 "$TRANSIENTS transients are stored in wp_options." "Clean them up with: wp transient delete --expired (or enable the Redis object cache, which keeps transients out of the database)."
            [ "$PLUGINS" != "?" ] && gt "$PLUGINS" 40 && add_finding 0 1 "$PLUGINS plugins are active, every one of them runs on every uncached request." "Deactivate plugins that aren't needed."
        fi
    else
        echo "  ${Y}could not run the database probe (no wp-config.php or PHP error)${N}"
    fi
fi

# ======================================================================
# 2. resources and settings

echo
echo "${B}Containers${N}"
printf "  %-16s %-8s %-10s %-22s %s\n" "container" "cpu" "throttled" "memory" "notes"
for s in "${SERVICES[@]}"; do
    if ! is_running "$s"; then
        printf "  %-16s ${R}not running${N}\n" "$s"
        if [ "$s" = "$DB_CONTAINER" ] || [ "$s" = "$PHP_CONTAINER" ] || [ "$s" = "$WEB_SERVER" ]; then
            add_finding 0 3 "Container $s is not running." "Start it from OpenPanel > Containers."
        fi
        continue
    fi
    prefix=$(echo "$s" | tr 'a-z.-' 'A-Z__')
    cpu_limit=$(env_val "${prefix}_CPU"); ram_limit=$(env_val "${prefix}_RAM")
    after=$(throttled_usec "$s"); before="${THROTTLE_BEFORE[$s]:-}"
    throttled_ms=0
    [[ "$after" =~ ^[0-9]+$ ]] && [[ "$before" =~ ^[0-9]+$ ]] && throttled_ms=$(( (after - before) / 1000 ))
    mem_line=$(in_container "$s" sh -c 'echo "$(cat /sys/fs/cgroup/memory.current 2>/dev/null) $(cat /sys/fs/cgroup/memory.max 2>/dev/null) $(grep -E "^oom_kill " /sys/fs/cgroup/memory.events 2>/dev/null | cut -d" " -f2)"' 2>/dev/null)
    read -r mem_cur mem_max ooms <<<"$mem_line"
    mem_txt="?"; mem_pct=0
    if [[ "${mem_cur:-}" =~ ^[0-9]+$ ]]; then
        if [[ "${mem_max:-}" =~ ^[0-9]+$ ]]; then
            mem_pct=$(( mem_cur * 100 / mem_max ))
            mem_txt="$(( mem_cur / 1048576 ))/$(( mem_max / 1048576 )) MB ($mem_pct%)"
        else
            mem_txt="$(( mem_cur / 1048576 )) MB (no limit)"
        fi
    fi
    notes=""
    [[ "${ooms:-0}" =~ ^[1-9] ]] && notes="${R}${ooms} OOM kills${N}"
    tcolor=""; [ "$throttled_ms" -gt 0 ] && tcolor="$Y"
    printf "  %-16s %-8s ${tcolor}%-10s${N} %-22s %s\n" "$s" "${cpu_limit:-?}" "${throttled_ms} ms" "$mem_txt" "$notes"

    if [ "$throttled_ms" -gt 50 ]; then
        add_finding "$throttled_ms" 2 "$s hit its CPU limit (${cpu_limit:-?} CPU) and was paused for ${throttled_ms} ms during this test." "Raise ${prefix}_CPU in $ENV_FILE (now ${cpu_limit:-?}) and recreate the container, or raise the plan limits in OpenAdmin."
    fi
    if [ "$mem_pct" -ge 90 ]; then
        add_finding 0 2 "$s uses ${mem_pct}% of its memory limit (${ram_limit:-?})." "Raise ${prefix}_RAM in $ENV_FILE (now ${ram_limit:-?}) and recreate the container."
    fi
    if [[ "${ooms:-0}" =~ ^[1-9] ]]; then
        add_finding 0 3 "$s was killed $ooms times for running out of memory (limit ${ram_limit:-?})." "Raise ${prefix}_RAM in $ENV_FILE and recreate the container."
    fi
done

if [ -n "$PHP_CONTAINER" ] && [ "$LITESPEED" = no ] && is_running "$PHP_CONTAINER"; then
    MAXCH=$(podman_ctx "$CONTEXT" logs --since 24h "$PHP_CONTAINER" 2>&1 | grep -c "max_children")
    if [ "$MAXCH" -gt 0 ]; then
        echo "  ${Y}$PHP_CONTAINER reached pm.max_children $MAXCH times in the last 24h, requests had to wait for a free PHP worker${N}"
        add_finding 0 2 "PHP-FPM ran out of workers (pm.max_children reached $MAXCH times in 24h), visitors wait in a queue." "Raise pm.max_children for $PHP_CONTAINER (and its RAM limit), or enable the page cache so fewer requests reach PHP."
    fi
fi

echo
echo "${B}Server${N}"
CORES=$(nproc); LOAD=$(cut -d' ' -f1 /proc/loadavg)
MEM_AVAIL=$(awk '/MemAvailable/ {print int($2/1024)}' /proc/meminfo); MEM_TOTAL=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
DISK=$(df -P / | awk 'NR==2 {print $5}' | tr -d '%')
printf "  %-36s %s\n" "load (1 min) / CPU cores" "$LOAD / $CORES" "memory available" "$MEM_AVAIL of $MEM_TOTAL MB" "disk used (/)" "$DISK%"
gt "$LOAD" "$CORES" && add_finding 0 2 "Server load ($LOAD) is higher than the number of CPU cores ($CORES), everything on the server is slowed down." "Find what uses the CPU with top, or in OpenAdmin > Server > Processes."
[ "$MEM_AVAIL" -lt $(( MEM_TOTAL / 10 )) ] && add_finding 0 2 "Only $MEM_AVAIL MB of memory is free on the server." "Find what uses it: free -m; ps aux --sort=-rss | head"
[ "$DISK" -ge 90 ] && add_finding 0 2 "Disk is ${DISK}% full." "Free up space: old backups, logs in /var/log, unused images (podman image prune)."

echo
echo "${B}Settings${N}"
WPCONFIG="$HOST_DOCROOT/wp-config.php"
is_wp=no; [ -f "$WPCONFIG" ] && is_wp=yes
OPCACHE_SITE=on
grep -qiE '^[[:space:]]*opcache\.enable[[:space:]]*=[[:space:]]*(0|off|false)' "$HOST_DOCROOT/.user.ini" 2>/dev/null && OPCACHE_SITE=off
OPCACHE_GLOBAL=on
grep -qiE '^[[:space:]]*opcache\.enable[[:space:]]*=[[:space:]]*(0|off|false)' "/home/$CONTEXT/php.ini/$PHP_VERSION.ini" 2>/dev/null && OPCACHE_GLOBAL=off
REDIS_DROPIN=no
grep -q "Redis Object Cache Drop-In" "$HOST_DOCROOT/wp-content/object-cache.php" 2>/dev/null && REDIS_DROPIN=yes
printf "  %-36s %s\n" "page cache (Varnish)" "$VIA_VARNISH" "OPcache (PHP $PHP_VERSION / this site)" "$OPCACHE_GLOBAL / $OPCACHE_SITE"
[ "$is_wp" = yes ] && printf "  %-36s %s\n" "Redis object cache" "$REDIS_DROPIN"
if [ "$is_wp" = yes ]; then
    for c in WP_DEBUG SAVEQUERIES; do
        if grep -qE "define\(\s*['\"]$c['\"]\s*,\s*true" "$WPCONFIG"; then
            printf "  %-36s ${Y}%s${N}\n" "$c" "true"
            add_finding 0 1 "$c is enabled in wp-config.php, it slows every request." "Turn it off in OpenPanel > WordPress > site > Debugging, or set define('$c', false); in wp-config.php."
        fi
    done
fi

# ======================================================================
# 3. what costs the time

CADDY_MS=$(ms caddy); WEB_MS=$(ms web); VARNISH_MS=$(ms varnish); FPM_MS=$(ms fpm)
TXT_WEB=$(ms txt_web); PHP_WEB=$(ms php_web); PUBLIC_MS=$(ms public)
BACKEND_MS="$WEB_MS"; [ "$VIA_VARNISH" = yes ] && BACKEND_MS="$VARNISH_MS"

# caddy + WAF on top of whatever it proxies to
if [ -n "$CADDY_MS" ] && [ -n "$BACKEND_MS" ]; then
    WAF_COST=$(sub_ms "$CADDY_MS" "$BACKEND_MS")
    if [ "$WAF_COST" -gt 50 ]; then
        if [ "$BODY_ACCESS" = on ] && [ "$BODY_SOURCE" = domain ]; then
            add_finding "$WAF_COST" 3 "Caddy + WAF add ${WAF_COST} ms to every page. The WAF buffers and inspects every HTML response because $CADDY_CONF has SecResponseBodyAccess On." "Remove that line for this domain, request protection stays on:\\n       sed -i '/^[[:space:]]*SecResponseBodyAccess On/d' $CADDY_CONF && podman exec caddy caddy reload --config /etc/caddy/Caddyfile"
        elif [ "$BODY_ACCESS" = on ]; then
            add_finding "$WAF_COST" 3 "Caddy + WAF add ${WAF_COST} ms to every page. The WAF buffers and inspects every HTML response (SecResponseBodyAccess On in $CORAZA_RULES), which is the usual cause." "Turn off response body inspection for all domains, request protection stays on:\\n       sed -i 's/^SecResponseBodyAccess On/SecResponseBodyAccess Off/' $CORAZA_RULES && podman restart caddy\\n       (a caddy reload is not enough, the WAF only rereads that file on restart)"
        elif [ "$WAF_ON" = yes ]; then
            add_finding "$WAF_COST" 2 "Caddy + WAF add ${WAF_COST} ms to every page." "Check which rules match in OpenPanel > WAF > Log, or compare with the WAF off for this domain: opencli waf domain $DOMAIN disable"
        else
            add_finding "$WAF_COST" 2 "Caddy adds ${WAF_COST} ms to every page although the WAF is off for this domain." "Check the caddy container: podman logs --tail 50 caddy"
        fi
    fi
fi

# network + TLS between visitors and the server
if [ -n "$PUBLIC_MS" ] && [ -n "$CADDY_MS" ]; then
    NET_COST=$(sub_ms "$PUBLIC_MS" "$CADDY_MS")
    [ "$NET_COST" -gt 150 ] && add_finding "$NET_COST" 1 "Going through public DNS adds ${NET_COST} ms (network, TLS handshake, or a CDN/proxy in front of the server)." "Test from outside the server too, and check the CDN if there is one. The layers measured on the server are the ones OpenPanel controls."
fi

# no page cache
if [ "$VIA_VARNISH" = no ] && [ -n "$WEB_MS" ] && gt "$WEB_MS" 100; then
    fix="Turn on Varnish in OpenPanel > Cache > Varnish and enable it for $DOMAIN."
    [ "$is_wp" = yes ] && fix="Turn on the page cache in OpenPanel > WordPress > site > Cache tab."
    add_finding "$(sub_ms "$WEB_MS" 5)" 2 "No page cache: every visit makes PHP build the page (${WEB_MS} ms). With Varnish most visits take a few ms." "$fix"
fi

# slow application when a page isn't cached
if [ -n "$FPM_MS" ] && gt "$FPM_MS" 300; then
    if [ "$is_wp" = yes ]; then
        cause="plugins, theme or database queries"
        fix="Profile the site with the Query Monitor plugin to find slow plugins or queries."
        [ "$REDIS_DROPIN" = no ] && fix="Enable the Redis object cache in OpenPanel > WordPress > site > Cache tab. $fix"
    else
        cause="the application's code or its database queries"
        fix="Profile the application to find slow code or queries, and use a page or object cache where it supports one."
    fi
    add_finding "$(sub_ms "$FPM_MS" "${PHP_WEB:-0}")" 2 "The site needs ${FPM_MS} ms to build a page (a PHP file without database takes ${PHP_WEB:-?} ms), so the time goes to $cause." "$fix"
fi

# plain PHP slow = php-fpm itself
if [ -n "$PHP_WEB" ] && [ -n "$TXT_WEB" ] && gt "$(sub_ms "$PHP_WEB" "$TXT_WEB")" 30; then
    add_finding "$(sub_ms "$PHP_WEB" "$TXT_WEB")" 2 "Even a PHP file without database takes ${PHP_WEB} ms (a static file ${TXT_WEB} ms), PHP-FPM itself is slow or busy." "Check the $PHP_CONTAINER CPU/memory limits above and pm.max_children."
fi

# static file slow = webserver
if [ -n "$TXT_WEB" ] && gt "$TXT_WEB" 30; then
    add_finding "$TXT_WEB" 2 "$WEB_SERVER needs ${TXT_WEB} ms for a tiny static file (normally 1-5 ms)." "Check the $WEB_SERVER container limits above."
fi

[ "$OPCACHE_GLOBAL" = off ] && add_finding 0 2 "OPcache is disabled in the PHP $PHP_VERSION settings, PHP recompiles every file on every request." "Set opcache.enable=1 in OpenPanel > PHP > PHP.INI Editor, then restart $PHP_CONTAINER."
[ "$OPCACHE_SITE" = off ] && [ "$OPCACHE_GLOBAL" = on ] && add_finding 0 2 "OPcache is turned off for this site (.user.ini)." "$([ "$is_wp" = yes ] && echo "Turn it on in OpenPanel > WordPress > site > Cache tab." || echo "Remove the opcache.enable=0 line from $HOST_DOCROOT/.user.ini.")"

# dns, severity 4 so it always comes first: nothing else matters if visitors don't reach the server
# lead sentence depends on whether the site answers locally
DNS_LEAD="DNS is the issue"
[ -n "$CADDY_MS" ] && [ "${CODE[caddy]}" = 200 ] && DNS_LEAD="The site itself loads fine on this server (${CADDY_MS} ms through Caddy), but DNS is the issue"
POINT_FIX="Point the domain to $IP_TXT: at your DNS provider set an A record for $DOMAIN (and www.$DOMAIN) to $IP_TXT. If the domain uses this server's nameservers, check its zone in OpenPanel > Domains > DNS Zone Editor. Changes can take up to a few hours to spread."
if [ -z "$PUBLIC_IP" ]; then
    add_finding 0 4 "$DNS_LEAD: $DOMAIN has no A record, so visitors can't reach it." "$POINT_FIX"
elif ! points_here "$PUBLIC_IPS"; then
    if [ "$BEHIND_CDN" = cloudflare ]; then
        add_finding 0 1 "$DOMAIN is proxied through Cloudflare ($PUBLIC_IP), so visitors reach this server through it." "If the site is slow only from outside, check the Cloudflare settings. Cloudflare's origin (DNS record) for $DOMAIN should be $IP_TXT."
    else
        add_finding 0 4 "$DNS_LEAD: $DOMAIN points to $(tr '\n' ' ' <<<"$PUBLIC_IPS")instead of this server ($IP_TXT), so visitors reach a different server." "$POINT_FIX"
    fi
elif [ -z "$PUBLIC_MS" ]; then
    add_finding 0 4 "$DOMAIN points to this server ($PUBLIC_IP), but the site doesn't answer over the public IP." "Check that ports 80/443 are open in the firewall (OpenAdmin > Security > Firewall) and that $DOMAIN has an SSL certificate (OpenPanel > Domains > SSL Certificates)."
fi
if [ -n "$PUBLIC_IP" ] && [ "$BEHIND_CDN" = no ]; then
    if [ -z "$WWW_IPS" ]; then
        add_finding 0 1 "www.$DOMAIN has no DNS record, visitors typing www get an error." "Add an A record for www.$DOMAIN pointing to $IP_TXT (or a CNAME to $DOMAIN)."
    elif ! points_here "$WWW_IPS"; then
        add_finding 0 2 "www.$DOMAIN points to $(tr '\n' ' ' <<<"$WWW_IPS")instead of this server ($IP_TXT)." "Change the A record for www.$DOMAIN to $IP_TXT (or a CNAME to $DOMAIN)."
    fi
fi

# ======================================================================
# 4. verdict

echo
echo "${B}======================================================================${N}"
echo "${B}Diagnosis for $SITE${N}"
echo "${B}======================================================================${N}"
if [ ${#FINDINGS[@]} -eq 0 ]; then
    echo "${G}No problems found.${N} Visitors get the page in ${PUBLIC_MS:-${CADDY_MS:-?}} ms."
    exit 0
fi

# biggest measured cost first, then severity
mapfile -t SORTED < <(printf '%s\n' "${FINDINGS[@]}" | awk -F'|' '{ printf "%d|%010d|%s\n", ($2 >= 4), $1, $0 }' | sort -t'|' -k1,1r -k2,2r -k4,4nr | cut -d'|' -f3-)
i=0
for f in "${SORTED[@]}"; do
    impact="${f%%|*}"; rest="${f#*|}"; sev="${rest%%|*}"; rest="${rest#*|}"; text="${rest%%|*}"; fix="${rest#*|}"
    i=$((i + 1))
    if [ $i -eq 1 ]; then
        echo
        echo "${R}${B}Biggest issue:${N} $text"
        [ "$impact" != 0 ] && echo "  ${B}Impact:${N} about ${impact} ms per request"
        printf "  ${B}Fix:${N} %b\n" "$fix"
        [ ${#SORTED[@]} -gt 1 ] && { echo; echo "${B}Also:${N}"; }
    else
        case "$sev" in 3|4) c="$R" ;; 2) c="$Y" ;; *) c="" ;; esac
        printf "  ${c}%d.${N} %s\n" "$((i - 1))" "$text"
        [ "$impact" != 0 ] && printf "     about %s ms\n" "$impact"
        printf "     Fix: %b\n" "$fix"
    fi
done
echo
