#!/bin/bash
################################################################################
# Script Name: domains/test.sh
# Description: Test a website speed through every layer (DNS, Caddy/WAF, Varnish, webserver, PHP, database), check limits and settings, and show the biggest issue with a fix.
# Usage: opencli domains-test <DOMAIN_NAME>[/SUBFOLDER] [RUNS] [--load <N|auto>]
# Author: Stefan Pejcic
# Created: 28.09.2026
# Last Modified: 30.09.2026
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

# --load can go anywhere, the rest stays positional
LOAD=""
POSITIONAL=()
while [ $# -gt 0 ]; do
    case "$1" in
        --load) LOAD="${2:-}"; shift; [ $# -gt 0 ] && shift ;;
        --load=*) LOAD="${1#*=}"; shift ;;
        *) POSITIONAL+=("$1"); shift ;;
    esac
done
set -- "${POSITIONAL[@]}"

if [ -z "${1:-}" ]; then
    echo "Usage: opencli domains-test <DOMAIN_NAME>[/SUBFOLDER] [RUNS] [--load <N|auto>]"
    echo "Example: opencli domains-test example.com"
    echo "         opencli domains-test example.com --load auto   # raise concurrency until the site breaks"
    echo "         opencli domains-test example.com --load 100    # 100 concurrent connections"
    exit 1
fi
if [ -n "$LOAD" ] && [ "$LOAD" != auto ] && ! [[ "$LOAD" =~ ^[1-9][0-9]*$ && "$LOAD" -le 5000 ]]; then
    echo "--load must be auto or a number of concurrent connections (1-5000)"
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


if [ -z "$LOAD" ]; then
    echo "${B}Site${N}         $SITE  (user $OWNER, $WEB_SERVER, ${PHP_CONTAINER:-no php container found}, $DB_CONTAINER)"
    echo "${B}Docroot${N}      $DOCROOT"
    echo "${B}Varnish${N}      container: $(is_running varnish && echo running || echo stopped), domain: ${VARNISH_DOMAIN:-?}"
    echo "${B}WAF${N}          $WAF_ON$([ "$WAF_ON" = yes ] && echo ", response body inspection: $BODY_ACCESS")"
    echo "${B}Runs${N}         1 warm-up + $RUNS measured per test, median time to first byte (local tests without connect/TLS setup)"
    echo
fi

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
# --load: stress the site locally until it breaks, skips caddy and dns so their limits don't count

if [ -n "$LOAD" ]; then
    if [ "$VIA_VARNISH" = yes ]; then
        LOAD_TARGET="Varnish (127.0.0.1:$HTTP_PORT)"
        LOAD_ARGS=(--connect-to "$DOMAIN:80:127.0.0.1:$HTTP_PORT" -H "X-Forwarded-Proto: https")
        LOAD_URL="http://$DOMAIN$URLPATH"
    else
        LOAD_TARGET="$WEB_SERVER (127.0.0.1:$HTTPS_PORT)"
        LOAD_ARGS=(--connect-to "$DOMAIN:443:127.0.0.1:$HTTPS_PORT")
        LOAD_URL="https://$DOMAIN$URLPATH"
    fi

    LOAD_SERVICES=()
    [ "$VIA_VARNISH" = yes ] && LOAD_SERVICES+=(varnish)
    LOAD_SERVICES+=("$WEB_SERVER")
    [ "$LITESPEED" = no ] && [ -n "$PHP_CONTAINER" ] && LOAD_SERVICES+=("$PHP_CONTAINER")
    LOAD_SERVICES+=("$DB_CONTAINER")

    echo "${B}Load test${N}    $SITE  (user $OWNER, $WEB_SERVER, ${PHP_CONTAINER:-no php}, $DB_CONTAINER)"
    echo "${B}Target${N}       $LOAD_TARGET, local so caddy, the WAF and DNS are skipped"
    [ "$VIA_VARNISH" = yes ] && echo "${B}Note${N}         the page comes from the Varnish cache, PHP is only hit on cache misses"
    echo "${B}Mode${N}         $([ "$LOAD" = auto ] && echo "auto, doubling concurrent connections until errors or response times collapse" || echo "$LOAD concurrent connections")"
    echo

    TMPD=$(mktemp -d); trap 'rm -rf "$TMPD"' EXIT
    LOAD_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    # whole test stops after a minute, whatever step it's on
    LOAD_BUDGET=60
    LOAD_DEADLINE=$(( $(date +%s) + LOAD_BUDGET ))
    TIMED_OUT=no; LOAD_TOTAL=0; LOAD_OK=0

    # throttled_usec memory.current memory.max oom_kills pids.current pids.max pids_max_hits
    cg_snap() {
        in_container "$1" sh -c 'c=/sys/fs/cgroup; echo "$(awk "/^throttled_usec/{print \$2}" $c/cpu.stat 2>/dev/null || echo 0) $(cat $c/memory.current 2>/dev/null || echo 0) $(cat $c/memory.max 2>/dev/null || echo max) $(awk "/^oom_kill /{print \$2}" $c/memory.events 2>/dev/null || echo 0) $(cat $c/pids.current 2>/dev/null || echo 0) $(cat $c/pids.max 2>/dev/null || echo max) $(awk "/^max /{print \$2}" $c/pids.events 2>/dev/null || echo 0)"' 2>/dev/null
    }
    declare -A SNAP0 SNAP1 THROTTLE_TOTAL MEM_PEAK OOM_TOTAL PIDS_HITS PIDS_PEAK
    for sv in "${LOAD_SERVICES[@]}"; do is_running "$sv" && SNAP0[$sv]=$(cg_snap "$sv"); THROTTLE_TOTAL[$sv]=0; MEM_PEAK[$sv]=0; OOM_TOTAL[$sv]=0; PIDS_HITS[$sv]=0; PIDS_PEAK[$sv]=0; done

    # c concurrent connections, n requests; curl's --parallel caps at a few hundred, so big runs are split over several curls
    load_step() {
        local c=$1 n=$2 procs p conc reqs t0 t1 i left
        left=$(( LOAD_DEADLINE - $(date +%s) )); [ "$left" -lt 1 ] && left=1
        procs=$(( (c + 199) / 200 ))
        rm -f "$TMPD"/out.*
        t0=$(date +%s%N)
        for p in $(seq 1 "$procs"); do
            conc=$(( c / procs + (p <= c % procs ? 1 : 0) ))
            reqs=$(( n * conc / c )); [ "$reqs" -lt "$conc" ] && reqs=$conc
            : > "$TMPD/urls.$p"
            for i in $(seq 1 "$reqs"); do printf 'url = "%s"\noutput = "/dev/null"\n' "$LOAD_URL" >> "$TMPD/urls.$p"; done
            timeout "$left" curl -sk --http1.1 -Z --parallel-immediate --parallel-max "$conc" --max-time 30 -A "OpenPanel load test" \
                "${LOAD_ARGS[@]}" -K "$TMPD/urls.$p" -w '%{http_code} %{time_total}\n' > "$TMPD/out.$p" 2>/dev/null &
        done
        wait
        t1=$(date +%s%N)
        # total ok err rps p50 p95 p99 codes (sorted with sort, mawk has no asort)
        cat "$TMPD"/out.* > "$TMPD/all"
        awk '$1 ~ /^[23]/ { print $2 * 1000 }' "$TMPD/all" | sort -n > "$TMPD/ok"
        local total ok err rps cs
        total=$(wc -l < "$TMPD/all"); ok=$(wc -l < "$TMPD/ok"); err=$(( total - ok ))
        rps=$(awk -v n="$ok" -v w="$(( t1 - t0 ))" 'BEGIN { printf "%.1f", n / (w / 1e9) }')
        pct() { [ "$ok" -gt 0 ] || { echo 0; return; }; local k; k=$(awk -v n="$ok" -v p="$1" 'BEGIN { k = int(n * p); if (k < 1) k = 1; print k }'); sed -n "${k}p" "$TMPD/ok" | awk '{ printf "%.0f", $1 }'; }
        cs=$(awk '$1 !~ /^[23]/ { c[$1 == "000" ? "timeout/refused" : $1]++ } END { for (k in c) printf "%s%sx%d", (n++ ? "," : ""), k, c[k] }' "$TMPD/all")
        echo "$total $ok $err $rps $(pct 0.50) $(pct 0.95) $(pct 0.99) ${cs:--}"
    }

    # after each step: how much each container got throttled, memory, oom kills and process limit hits
    collect_step() {
        local sv a b
        for sv in "${LOAD_SERVICES[@]}"; do
            is_running "$sv" || continue
            SNAP1[$sv]=$(cg_snap "$sv")
            read -r a0 m0 mx0 o0 pc0 pm0 ph0 <<<"${SNAP0[$sv]:-0 0 max 0 0 max 0}"
            read -r a1 m1 mx1 o1 pc1 pm1 ph1 <<<"${SNAP1[$sv]:-0 0 max 0 0 max 0}"
            [[ "$a1" =~ ^[0-9]+$ && "$a0" =~ ^[0-9]+$ ]] && THROTTLE_TOTAL[$sv]=$(( ${THROTTLE_TOTAL[$sv]} + (a1 - a0) / 1000 ))
            [[ "$m1" =~ ^[0-9]+$ && "$mx1" =~ ^[0-9]+$ ]] && { pct=$(( m1 * 100 / mx1 )); [ "$pct" -gt "${MEM_PEAK[$sv]}" ] && MEM_PEAK[$sv]=$pct; }
            [[ "$o1" =~ ^[0-9]+$ && "$o0" =~ ^[0-9]+$ ]] && OOM_TOTAL[$sv]=$(( ${OOM_TOTAL[$sv]} + o1 - o0 ))
            [[ "$ph1" =~ ^[0-9]+$ && "$ph0" =~ ^[0-9]+$ ]] && PIDS_HITS[$sv]=$(( ${PIDS_HITS[$sv]} + ph1 - ph0 ))
            [[ "$pc1" =~ ^[0-9]+$ ]] && [ "$pc1" -gt "${PIDS_PEAK[$sv]}" ] && PIDS_PEAK[$sv]=$pc1
            SNAP0[$sv]="${SNAP1[$sv]}"
        done
    }
    step_throttle() { local sv out=""; for sv in "${LOAD_SERVICES[@]}"; do [ -n "${STEP_T[$sv]:-}" ] && [ "${STEP_T[$sv]}" -gt 50 ] && out="$out $sv"; done; echo "${out# }"; }

    if [ "$LOAD" = auto ]; then
        LADDER=(1 2 4 8 16 32 64 128 256 512 1024 2048)
    else
        LADDER=("$LOAD")
    fi

    printf "  %-6s %-8s %-7s %-9s %-9s %-9s %-9s %s\n" "conns" "requests" "errors" "req/s" "p50 ms" "p95 ms" "p99 ms" "throttled"
    BEST_RPS=0; BEST_C=0; GOOD_C=0; GOOD_RPS=0; GOOD_P95=0; BASE_P95=""; BROKE_C=""; BROKE_WHY=""; SATURATED_C=""; PREV_RPS=0
    declare -A STEP_T
    for c in "${LADDER[@]}"; do
        # enough requests for about 5 seconds at the last rate, at least 4 per connection
        n=$(awk -v c="$c" -v r="$PREV_RPS" 'BEGIN { n = r * 5; if (n < c * 4) n = c * 4; if (n < 20) n = 20; if (n > c * 40) n = c * 40; if (n > 30000) n = 30000; printf "%d", n }')
        declare -A BEFORE_T=(); for sv in "${LOAD_SERVICES[@]}"; do BEFORE_T[$sv]=${THROTTLE_TOTAL[$sv]:-0}; done
        read -r total ok err rps p50 p95 p99 codes <<<"$(load_step "$c" "$n")"
        LOAD_TOTAL=$(( LOAD_TOTAL + total )); LOAD_OK=$(( LOAD_OK + ok ))
        # the time limit cut this step short, requests still in flight were dropped rather than counted as errors
        [ "$(date +%s)" -ge "$LOAD_DEADLINE" ] && TIMED_OUT=yes
        collect_step
        for sv in "${LOAD_SERVICES[@]}"; do STEP_T[$sv]=$(( ${THROTTLE_TOTAL[$sv]:-0} - ${BEFORE_T[$sv]:-0} )); done
        errpct=$(awk -v e="$err" -v t="$total" 'BEGIN { printf "%.1f", t ? e * 100 / t : 100 }')
        ecolor=""; gt "$errpct" 0 && ecolor="$Y"; gt "$errpct" 5 && ecolor="$R"
        printf "  %-6s %-8s ${ecolor}%-7s${N} %-9s %-9s %-9s %-9s %s\n" "$c" "$total" "${errpct}%" "$rps" "$p50" "$p95" "$p99" "$(step_throttle)"
        [ "$err" -gt 0 ] && echo "         errors: $codes"
        [ "$TIMED_OUT" = yes ] && echo "         ${Y}cut short by the ${LOAD_BUDGET}s time limit${N}"

        [ -z "$BASE_P95" ] && [ "$ok" -gt 0 ] && BASE_P95="$p95"
        if gt "$errpct" 5; then
            BROKE_C=$c; BROKE_WHY="${errpct}% of requests failed ($codes)"; break
        fi
        if [ -n "$BASE_P95" ] && gt "$p95" "$(awk -v b="$BASE_P95" 'BEGIN { v = b * 10; if (v < 3000) v = 3000; print v }')"; then
            BROKE_C=$c; BROKE_WHY="response times collapsed, p95 went from ${BASE_P95} ms to ${p95} ms"; break
        fi
        [ "$ok" -gt 0 ] && { GOOD_C=$c; GOOD_RPS=$rps; GOOD_P95=$p95; }
        gt "$rps" "$BEST_RPS" && { BEST_RPS=$rps; BEST_C=$c; }
        [ "$TIMED_OUT" = yes ] && break
        [ -z "$SATURATED_C" ] && gt "$PREV_RPS" 0 && ! gt "$rps" "$(awk -v r="$PREV_RPS" 'BEGIN { print r * 1.1 }')" && SATURATED_C=$c
        PREV_RPS=$rps
    done

    # which limit got hit
    LIMITS=()   # "text|fix"
    for sv in "${LOAD_SERVICES[@]}"; do
        is_running "$sv" || { LIMITS+=("$sv stopped running during the test.|Check it with: podman logs $sv (as user $CONTEXT), a crash under load usually means its memory limit is too low."); continue; }
        prefix=$(echo "$sv" | tr 'a-z.-' 'A-Z__')
        cpu_limit=$(env_val "${prefix}_CPU"); ram_limit=$(env_val "${prefix}_RAM"); pids_limit=$(env_val "${prefix}_PIDS")
        [ "${THROTTLE_TOTAL[$sv]}" -gt 200 ] && LIMITS+=("$sv hit its CPU limit (${cpu_limit:-?} CPU) and was paused for ${THROTTLE_TOTAL[$sv]} ms.|Raise ${prefix}_CPU in $ENV_FILE (now ${cpu_limit:-?}) and recreate the container, or raise the CPU in the hosting plan.")
        [ "${MEM_PEAK[$sv]}" -ge 90 ] && LIMITS+=("$sv used ${MEM_PEAK[$sv]}% of its memory limit (${ram_limit:-?}).|Raise ${prefix}_RAM in $ENV_FILE (now ${ram_limit:-?}) and recreate the container.")
        [ "${OOM_TOTAL[$sv]}" -gt 0 ] && LIMITS+=("$sv was killed ${OOM_TOTAL[$sv]} times for running out of memory (${ram_limit:-?}).|Raise ${prefix}_RAM in $ENV_FILE (now ${ram_limit:-?}) and recreate the container.")
        [ "${PIDS_HITS[$sv]}" -gt 0 ] && LIMITS+=("$sv reached its process limit (${pids_limit:-?}, ${PIDS_HITS[$sv]} forks refused).|Raise ${prefix}_PIDS in $ENV_FILE (now ${pids_limit:-?}) and recreate the container.")
    done
    SINCE_LOGS() { podman_ctx "$CONTEXT" logs --since "$LOAD_START" "$1" 2>&1; }
    if [ "$LITESPEED" = no ] && [ -n "$PHP_CONTAINER" ] && is_running "$PHP_CONTAINER"; then
        mc=$(SINCE_LOGS "$PHP_CONTAINER" | grep -c "max_children")
        maxch=$(in_container "$PHP_CONTAINER" sh -c 'grep -h "^pm.max_children" /usr/local/etc/php-fpm.d/*.conf 2>/dev/null | tail -1' 2>/dev/null | awk -F= '{gsub(/ /,"",$2); print $2}')
        [ "$mc" -gt 0 ] && LIMITS+=("$PHP_CONTAINER ran out of PHP workers (pm.max_children = ${maxch:-?}), requests queued.|pm.max_children is tuned from the container memory, raise $(echo "$PHP_CONTAINER" | tr 'a-z.-' 'A-Z__')_RAM in $ENV_FILE and recreate $PHP_CONTAINER, or turn on the page cache so fewer requests reach PHP.")
    fi
    case "$WEB_SERVER" in
        nginx|openresty)
            wc_hits=$(SINCE_LOGS "$WEB_SERVER" | grep -c "worker_connections are not enough")
            wc=$(grep -oE 'worker_connections[[:space:]]+[0-9]+' "/home/$CONTEXT/$WEB_SERVER.conf" 2>/dev/null | awk '{print $2}')
            [ "$wc_hits" -gt 0 ] && LIMITS+=("$WEB_SERVER ran out of connections (worker_connections ${wc:-?}).|Raise worker_connections in /home/$CONTEXT/$WEB_SERVER.conf and restart $WEB_SERVER.")
            ;;
        apache)
            mrw_hits=$(SINCE_LOGS apache | grep -c "MaxRequestWorkers")
            mrw=$(grep -oE '^[[:space:]]*MaxRequestWorkers[[:space:]]+[0-9]+' "/home/$CONTEXT/httpd.conf" 2>/dev/null | awk '{print $2}' | head -1)
            [ "$mrw_hits" -gt 0 ] && LIMITS+=("apache reached MaxRequestWorkers (${mrw:-?}), new connections had to wait.|Raise MaxRequestWorkers (and ServerLimit/ThreadsPerChild to match) in /home/$CONTEXT/httpd.conf and restart apache.")
            ;;
    esac
    if is_running "$DB_CONTAINER"; then
        read -r db_max db_used <<<"$(in_container "$DB_CONTAINER" sh -c 'c=$(command -v mariadb || command -v mysql); $c -uroot ${MYSQL_ROOT_PASSWORD:+-p"$MYSQL_ROOT_PASSWORD"} -N -e "SELECT @@max_connections, VARIABLE_VALUE FROM information_schema.GLOBAL_STATUS WHERE VARIABLE_NAME = \"MAX_USED_CONNECTIONS\"" 2>/dev/null' 2>/dev/null)"
        [[ "${db_max:-}" =~ ^[0-9]+$ && "${db_used:-}" =~ ^[0-9]+$ ]] && [ "$db_used" -ge "$db_max" ] && LIMITS+=("$DB_CONTAINER ran out of connections (max_connections $db_max).|Raise max_connections in /home/$CONTEXT/custom.cnf and restart $DB_CONTAINER.")
    fi

    # requests left in the queues keep the site busy after the test, see how long until a single visit is answered again
    RECOVERY=""
    rec_t0=$(date +%s)
    while [ $(( $(date +%s) - rec_t0 )) -lt 60 ]; do
        code=$(curl -sk --http1.1 -o /dev/null --max-time 5 -w '%{http_code}' "${LOAD_ARGS[@]}" "$LOAD_URL")
        [[ "$code" =~ ^[23] ]] && { RECOVERY=$(( $(date +%s) - rec_t0 )); break; }
        sleep 1
    done

    echo
    echo "${B}======================================================================${N}"
    echo "${B}Load test result for $SITE${N}"
    echo "${B}======================================================================${N}"
    if [ "$GOOD_C" -gt 0 ]; then
        echo "${G}Handled:${N}   $GOOD_C concurrent connections at $GOOD_RPS req/s (p95 $GOOD_P95 ms, under 5% errors)"
    else
        echo "${R}Handled:${N}   not even 1 connection without errors"
    fi
    [ "$BEST_C" -gt 0 ] && echo "${B}Peak:${N}      $BEST_RPS req/s at $BEST_C concurrent connections"
    if [ -z "$RECOVERY" ]; then
        echo "${R}Recovery:${N}  still not answering 60s after the test, requests queued during the test are still being processed"
    elif [ "$RECOVERY" -gt 2 ]; then
        echo "${Y}Recovery:${N}  the site answered normally again ${RECOVERY}s after the test"
    fi
    [ -n "$SATURATED_C" ] && echo "${B}Saturated:${N} from $SATURATED_C connections on throughput stopped growing, more connections only queue up"
    if [ -n "$BROKE_C" ]; then
        echo "${R}Broke at:${N}  $BROKE_C concurrent connections, $BROKE_WHY"
    elif [ "$TIMED_OUT" = yes ]; then
        echo "${Y}Time limit:${N} stopped after ${LOAD_BUDGET}s at $c concurrent connections without breaking, $LOAD_OK of $LOAD_TOTAL requests answered in that minute"
    elif [ "$LOAD" = auto ]; then
        echo "${G}Did not break${N} up to ${LADDER[-1]} concurrent connections"
    fi
    echo
    if [ ${#LIMITS[@]} -gt 0 ]; then
        echo "${B}Limits reached:${N}"
        i=0
        for l in "${LIMITS[@]}"; do
            i=$((i + 1))
            echo "  ${Y}$i.${N} ${l%%|*}"
            echo "     Fix: ${l#*|}"
        done
    else
        LOAD1=$(cut -d' ' -f1 /proc/loadavg)
        echo "${B}No container limit was hit.${N}"
        echo "  The bottleneck is outside the site's containers: the server CPU (load $LOAD1 on $(nproc) cores, the load generator runs on this server too) or the application itself."
        [ "$VIA_VARNISH" = no ] && [ "$LITESPEED" = no ] && echo "  Turning on the page cache (Varnish) usually raises the number of visitors a site can handle the most."
    fi
    echo
    exit 0
fi

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
