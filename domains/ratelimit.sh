#!/bin/bash
################################################################################
# Script Name: domains/ratelimit.sh
# Description: Set the per-ip and per-domain request rate limits in Caddy, server default or per domain.
# Usage: opencli domains-ratelimit [status] [--json]
#        opencli domains-ratelimit default <per_ip> <per_site>
#        opencli domains-ratelimit <DOMAIN_NAME...|--all|--user USERNAME> <per_ip> <per_site>|default|off
#        opencli domains-ratelimit stats [DOMAIN_NAME...|--all|--user USERNAME] [--hours 24] [--json]
# Author: Stefan Pejcic
# Created: 15.10.2026
# Last Modified: 15.10.2026
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

set -euo pipefail

# limits are requests per minute; 0 turns that zone off
CADDY_DIR="/etc/openpanel/caddy"
CADDYFILE="${CADDY_DIR}/Caddyfile"
RATELIMIT_CONF="${CADDY_DIR}/ratelimit.conf"
DOMAIN_DIR="${CADDY_DIR}/domains"
CADDY_IMAGE_DEFAULT="docker.io/openpanel/caddy-coraza:latest"

# every rate limit line in a domain file, so they can be swapped as a set
RL_LINE_RE='^[[:space:]]*(import[[:space:]]+ratelimit(_ip|_site)?([[:space:]].*)?|# ratelimit off)[[:space:]]*$'

usage() {
    echo "Usage:"
    echo "  opencli domains-ratelimit [status] [--json]                         show the server default and every domain"
    echo "  opencli domains-ratelimit default <per_ip> <per_site>               server default, used by domains set to 'default'"
    echo "  opencli domains-ratelimit <targets> <per_ip> <per_site>             custom limits"
    echo "  opencli domains-ratelimit <targets> default|off                     use the server default, or no limits"
    echo "  opencli domains-ratelimit stats [targets] [--hours 24] [--json]     requests, 429s and peaks from the access logs"
    echo ""
    echo "Targets: one or more domains, --all, or --user <username>, mixed freely."
    echo "Limits are requests per minute, per visitor IP and per domain; 0 turns that limit off."
    echo "Example: opencli domains-ratelimit --user john shop.com 600 20000"
    exit 1
}

is_limit() { [[ "$1" =~ ^[0-9]+$ ]]; }

# ---------------------------------------------------------------- snippets

# reads "<per_ip> <per_site>" from the '# default:' line, 0 0 when missing
read_default() {
    local line
    line=$(grep -m1 '^# default:' "$RATELIMIT_CONF" 2>/dev/null || true)
    line="${line#\# default:}"
    read -r d_ip d_site <<< "${line:-0 0}"
    is_limit "${d_ip:-}" || d_ip=0
    is_limit "${d_site:-}" || d_site=0
    echo "$d_ip $d_site"
}

# echoes the import lines for a set of limits, indented like the redirects import above them
limit_lines() {
    local indent="$1" ip="$2" site="$3" domain="$4"
    (( ip > 0 ))   && printf '%simport ratelimit_ip %s %s\n' "$indent" "$ip" "$domain"
    (( site > 0 )) && printf '%simport ratelimit_site %s %s\n' "$indent" "$site" "$domain"
    (( ip == 0 && site == 0 )) && printf '%s# ratelimit off\n' "$indent"
    return 0
}

write_ratelimit_conf() {
    local ip="$1" site="$2" tmp="${RATELIMIT_CONF}.tmp"
    {
        echo '# opencli domains-ratelimit'
        echo '# requests per minute, per visitor IP and per domain, for domains with `import ratelimit <domain>`; 0 = off'
        echo "# default: $ip $site"
        echo '#'
        echo "# zone names carry the domain because caddy-ratelimit shares a zone's state by name across all sites, so a per-domain override would change everyone's limit"
        echo "# and the limit, because raising a limit on a live zone keeps blocking until its oldest hit leaves the window (SetMaxEvents resets the cursor)"
        echo
        echo '(ratelimit_ip) {'
        echo '    rate_limit {'
        echo '        zone ip_{args[1]}_{args[0]} {'
        echo '            key {client_ip}'
        echo '            events {args[0]}'
        echo '            window 1m'
        echo '        }'
        echo '    }'
        echo '}'
        echo
        echo '(ratelimit_site) {'
        echo '    rate_limit {'
        echo '        zone site_{args[1]}_{args[0]} {'
        echo '            key {host}'
        echo '            events {args[0]}'
        echo '            window 1m'
        echo '        }'
        echo '    }'
        echo '}'
        echo
        echo '# server default, args[0] = domain'
        echo '(ratelimit) {'
        limit_lines "    " "$ip" "$site" '{args[0]}' | grep -v '# ratelimit off' || true
        echo '}'
    } > "$tmp"
    mv "$tmp" "$RATELIMIT_CONF"
}

# ---------------------------------------------------------------- domains

# "default", "off" or "<per_ip> <per_site>" for one domain file
domain_setting() {
    local file="$1" ip=0 site=0
    if grep -qE '^[[:space:]]*import[[:space:]]+ratelimit[[:space:]]' "$file"; then
        echo "default"; return
    fi
    if ! grep -qE '^[[:space:]]*import[[:space:]]+ratelimit_(ip|site)[[:space:]]' "$file"; then
        echo "off"; return
    fi
    ip=$(awk '$1 == "import" && $2 == "ratelimit_ip" {print $3; exit}' "$file")
    site=$(awk '$1 == "import" && $2 == "ratelimit_site" {print $3; exit}' "$file")
    echo "${ip:-0} ${site:-0}"
}

# replaces the domain's rate limit lines with the new set, placed right after each redirects import
set_domain() {
    local file="$1" mode="$2" ip="${3:-0}" site="${4:-0}" domain
    domain=$(basename "$file" .conf)

    if ! grep -q 'import /etc/openpanel/caddy/redirects.conf' "$file"; then
        echo "SKIPPED: $domain (no redirects import to place the limits after, suspended or custom vhost?)"
        return 0
    fi

    awk -v mode="$mode" -v ip="$ip" -v site="$site" -v domain="$domain" -v re="$RL_LINE_RE" '
        $0 ~ re { next }
        /^[[:space:]]*# rate limit \(opencli domains-ratelimit\)[[:space:]]*$/ { next }
        {
            print
            if ($0 ~ /import \/etc\/openpanel\/caddy\/redirects\.conf/) {
                match($0, /^[[:space:]]*/); indent = substr($0, 1, RLENGTH)
                print ""
                print indent "# rate limit (opencli domains-ratelimit)"
                if (mode == "default") {
                    print indent "import ratelimit " domain
                } else {
                    if (ip > 0)   print indent "import ratelimit_ip " ip " " domain
                    if (site > 0) print indent "import ratelimit_site " site " " domain
                    if (ip == 0 && site == 0) print indent "# ratelimit off"
                }
            }
        }
    ' "$file" | cat -s > "${file}.tmp"
    mv "${file}.tmp" "$file"

    case "$mode" in
        default) echo "Updated: $domain uses the server default" ;;
        *)       if (( ip == 0 && site == 0 )); then echo "Updated: $domain has no rate limits"; else echo "Updated: $domain ${ip}/min per IP, ${site}/min per domain"; fi ;;
    esac
}

# ---------------------------------------------------------------- caddy

# caddy bind-mounts the Caddyfile as a single file, so it has to keep its inode: after a mv or sed -i the container still sees the old one
write_in_place() {
    cat "$1" > "$2"
    rm -f "$1"
}

# the limits key on {client_ip}, so behind cloudflare it has to be the visitor and not the edge, otherwise one CF ip limits everyone
ensure_trusted_proxies() {
    grep -q '# START TRUSTED PROXIES #' "$CADDYFILE" && return 0
    grep -qE '^[[:space:]]*(trusted_proxies|client_ip_headers)[[:space:]]' "$CADDYFILE" && return 0

    local ranges
    ranges=$( { curl -fsSL --max-time 10 https://www.cloudflare.com/ips-v4; echo; curl -fsSL --max-time 10 https://www.cloudflare.com/ips-v6; } 2>/dev/null | sed '/^[[:space:]]*$/d' | tr '\n' ' ' || true)
    [[ -n "$ranges" ]] || ranges="173.245.48.0/20 103.21.244.0/22 103.22.200.0/22 103.31.4.0/22 141.101.64.0/18 108.162.192.0/18 190.93.240.0/20 188.114.96.0/20 197.234.240.0/22 198.41.128.0/17 162.158.0.0/15 104.16.0.0/13 104.24.0.0/14 172.64.0.0/13 131.0.72.0/22 2400:cb00::/32 2606:4700::/32 2803:f800::/32 2405:b500::/32 2405:8100::/32 2a06:98c0::/29 2c0f:f248::/32"

    if grep -qE '^[[:space:]]*servers([[:space:]]|\{)' "$CADDYFILE"; then
        echo "WARNING: $CADDYFILE already has a servers block, add 'trusted_proxies static <cloudflare ranges>' and 'client_ip_headers CF-Connecting-IP' to it manually."
        return 0
    fi

    echo "Adding Cloudflare as trusted proxies to $CADDYFILE..."
    # the global options block is the first { at the start of a line
    awk -v ranges="${ranges% }" '
        !done && /^\{[[:space:]]*$/ {
            print
            print ""
            print "    # START TRUSTED PROXIES #"
            print "    # {client_ip} is the real visitor behind cloudflare, so per-ip rate limits don'"'"'t lump everyone on one edge ip together"
            print "    servers {"
            print "        trusted_proxies static " ranges
            print "        client_ip_headers CF-Connecting-IP"
            print "    }"
            print "    # END TRUSTED PROXIES #"
            done = 1
            next
        }
        { print }
    ' "$CADDYFILE" > "${CADDYFILE}.tmp"
    write_in_place "${CADDYFILE}.tmp" "$CADDYFILE"
}

ensure_caddyfile_import() {
    grep -q "import ${RATELIMIT_CONF}" "$CADDYFILE" && return 0
    echo "Adding the rate limit snippets import to $CADDYFILE..."
    sed "s|^import /etc/openpanel/caddy/domains/\*|# import rate limit snippets (opencli domains-ratelimit)\nimport ${RATELIMIT_CONF}\n\n&|" "$CADDYFILE" > "${CADDYFILE}.tmp"
    write_in_place "${CADDYFILE}.tmp" "$CADDYFILE"
}

# caddy images before the rate limit module fail to load any config that uses it
ensure_caddy_module() {
    podman exec caddy caddy list-modules 2>/dev/null | grep -q '^http.handlers.rate_limit$' && return 0
    echo "The running Caddy has no rate limit module, pulling the new image and recreating the caddy container..."
    podman pull "$CADDY_IMAGE_DEFAULT" >/dev/null
    (cd /root && podman-compose up -d --no-deps --force-recreate caddy) >/dev/null 2>&1 || true
    local i
    for i in $(seq 1 30); do
        podman exec caddy caddy list-modules 2>/dev/null | grep -q '^http.handlers.rate_limit$' && return 0
        sleep 1
    done
    echo "ERROR: Caddy still has no rate limit module, check 'podman logs caddy'."
    return 1
}

# snapshot of everything this script may touch, put back if caddy rejects the result
BACKUP_DIR=""
backup_files() {
    BACKUP_DIR=$(mktemp -d)
    cp -a "$CADDYFILE" "$BACKUP_DIR/Caddyfile"
    [[ -f "$RATELIMIT_CONF" ]] && cp -a "$RATELIMIT_CONF" "$BACKUP_DIR/ratelimit.conf"
    mkdir -p "$BACKUP_DIR/domains"
    cp -a "$DOMAIN_DIR"/*.conf "$BACKUP_DIR/domains/" 2>/dev/null || true
}

restore_files() {
    cp -a "$BACKUP_DIR/Caddyfile" "$CADDYFILE"
    [[ -f "$BACKUP_DIR/ratelimit.conf" ]] && cp -a "$BACKUP_DIR/ratelimit.conf" "$RATELIMIT_CONF"
    cp -a "$BACKUP_DIR/domains/"*.conf "$DOMAIN_DIR/" 2>/dev/null || true
}

apply_caddy() {
    local out
    if ! out=$(podman exec caddy caddy validate --config /etc/caddy/Caddyfile 2>&1); then
        echo "ERROR: Caddy rejected the new config, nothing was changed:"
        echo "$out" | grep -iE 'error|Error' | tail -5
        restore_files
        rm -rf "$BACKUP_DIR"
        exit 1
    fi
    if out=$(podman exec caddy caddy reload --config /etc/caddy/Caddyfile 2>&1); then
        echo "SUCCESS: Caddy reloaded."
    else
        echo "WARNING: Failed to reload Caddy."
        echo "$out" | tail -5
    fi
    rm -rf "$BACKUP_DIR"
}

# every change needs the snippets file, the import and the module in place before caddy sees it
prepare() {
    backup_files
    # the 2.0.16 update only writes an empty default, the per-domain snippets get added here
    if ! grep -q '^(ratelimit_ip)' "$RATELIMIT_CONF" 2>/dev/null; then
        local d_ip d_site
        read -r d_ip d_site < <(read_default)
        write_ratelimit_conf "$d_ip" "$d_site"
    fi
    ensure_caddyfile_import
    ensure_trusted_proxies
    ensure_caddy_module || { restore_files; rm -rf "$BACKUP_DIR"; exit 1; }
}

# ---------------------------------------------------------------- main

# "<mode> <per_ip> <per_site>" with the limits that actually apply, default resolved to the server default
effective_setting() {
    local setting="$1" d_ip="$2" d_site="$3"
    case "$setting" in
        default) echo "default $d_ip $d_site" ;;
        off)     echo "off 0 0" ;;
        *)       echo "custom $setting" ;;
    esac
}

status() {
    local json="$1" d_ip d_site file setting mode ip site first=1
    read -r d_ip d_site < <(read_default)
    shopt -s nullglob
    if $json; then
        printf '{"default":{"per_ip":%s,"per_site":%s},"domains":[' "$d_ip" "$d_site"
        for file in "$DOMAIN_DIR"/*.conf; do
            read -r mode ip site < <(effective_setting "$(domain_setting "$file")" "$d_ip" "$d_site")
            (( first )) || printf ','
            first=0
            printf '{"domain":"%s","mode":"%s","per_ip":%s,"per_site":%s}' "$(basename "$file" .conf)" "$mode" "$ip" "$site"
        done
        printf ']}\n'
        return
    fi
    if (( d_ip == 0 && d_site == 0 )); then
        echo "Server default: off"
    else
        echo "Server default: ${d_ip}/min per IP, ${d_site}/min per domain (0 = off)"
    fi
    echo "----------------------------------------"
    for file in "$DOMAIN_DIR"/*.conf; do
        setting=$(domain_setting "$file")
        case "$setting" in
            default|off) echo "  $(basename "$file" .conf): $setting" ;;
            *)           read -r ip site <<< "$setting"; echo "  $(basename "$file" .conf): ${ip}/min per IP, ${site}/min per domain" ;;
        esac
    done
}

# one JSON object for the domain's last <hours> of access logs: requests, 429s, the busiest minute for the whole domain and for a single IP, and the IPs that got the most 429s
domain_stats_json() {
    local domain="$1" hours="$2" dir="/var/log/caddy/domlogs/$1" since f files=()
    since=$(( $(date +%s) - hours * 3600 ))
    shopt -s nullglob
    # rolled files carry the time of their last entry, so anything rolled before the window is skipped; oldest first keeps the minutes in order
    while read -r f; do
        [[ -n "$f" ]] && files+=("$f")
    done < <(for f in "$dir"/access*.log "$dir"/access*.log.gz; do
        [[ "$f" == "$dir/access.log" || $(stat -c %Y "$f") -ge $since ]] && echo "$(stat -c %Y "$f") $f"
    done | sort -n | cut -d' ' -f2-)

    for f in "${files[@]}"; do
        if [[ "$f" == *.gz ]]; then zcat -- "$f" 2>/dev/null; else cat -- "$f"; fi
    done | awk -v since="$since" -v domain="$domain" -v hours="$hours" '
        {
            if (!match($0, /"ts":[0-9.]+/)) next
            ts = substr($0, RSTART + 5, RLENGTH - 5) + 0
            if (ts < since) next
            if (!match($0, /"size":[0-9]+,"status":[0-9]+/)) next
            st = substr($0, RSTART, RLENGTH); sub(/.*"status":/, "", st)
            ip = ""
            if (match($0, /"client_ip":"[^"]*"/)) ip = substr($0, RSTART + 13, RLENGTH - 14)
            m = int(ts / 60)
            # counters per minute only live for the current minute, so a busy day does not keep millions of ip/minute keys
            if (m != lastm) { cnt = 0; delete pip; lastm = m }
            req++; cnt++
            if (cnt > peak) peak = cnt
            if (++pip[ip] > peakip) peakip = pip[ip]
            if (st == 429) { blocked++; bip[ip]++ }
        }
        END {
            printf "{\"domain\":\"%s\",\"hours\":%d,\"requests\":%d,\"blocked\":%d,\"peak_minute\":%d,\"peak_ip_minute\":%d,\"top_blocked\":[", domain, hours, req, blocked, peak, peakip
            for (n = 0; n < 5; n++) {
                best = ""; bc = 0
                for (k in bip) if (bip[k] > bc) { bc = bip[k]; best = k }
                if (bc == 0) break
                printf "%s{\"ip\":\"%s\",\"count\":%d}", (n ? "," : ""), best, bc
                delete bip[best]
            }
            printf "]}"
        }'
}

stats() {
    local json="$1" hours="$2" first=1 domain line
    shift 2
    if $json; then
        printf '['
        for domain in "$@"; do
            (( first )) || printf ','
            first=0
            domain_stats_json "$domain" "$hours"
        done
        printf ']\n'
        return
    fi
    printf '%-40s %10s %8s %12s %14s\n' "Domain (last ${hours}h)" "Requests" "429s" "Peak/min" "Peak/min/IP"
    for domain in "$@"; do
        line=$(domain_stats_json "$domain" "$hours")
        printf '%-40s %10s %8s %12s %14s\n' "$domain" \
            "$(grep -o '"requests":[0-9]*' <<< "$line" | cut -d: -f2)" \
            "$(grep -o '"blocked":[0-9]*' <<< "$line" | cut -d: -f2)" \
            "$(grep -o '"peak_minute":[0-9]*' <<< "$line" | cut -d: -f2)" \
            "$(grep -o '"peak_ip_minute":[0-9]*' <<< "$line" | cut -d: -f2)"
    done
}

# fills TARGET_DOMAINS from domain names, --all and --user <username>, only domains with a caddy config
TARGET_DOMAINS=()
resolve_targets() {
    local args=("$@") i t u
    shopt -s nullglob
    for (( i = 0; i < ${#args[@]}; i++ )); do
        t="${args[$i]}"
        case "$t" in
            --all)
                for f in "$DOMAIN_DIR"/*.conf; do TARGET_DOMAINS+=("$(basename "$f" .conf)"); done ;;
            --user)
                u="${args[$((i + 1))]:-}"
                (( i++ )) || true
                [[ "$u" =~ ^[A-Za-z0-9_.-]+$ ]] || { echo "ERROR: --user needs a username."; exit 1; }
                local found=0
                while read -r d; do
                    [[ "$d" =~ ^[A-Za-z0-9.-]+$ && -f "$DOMAIN_DIR/$d.conf" ]] && { TARGET_DOMAINS+=("$d"); found=1; }
                done < <(opencli domains-user "$u" 2>/dev/null)
                (( found )) || { echo "ERROR: No domains found for user $u."; exit 1; } ;;
            -*)
                usage ;;
            *)
                # a domain name never has a slash, this also keeps the path inside DOMAIN_DIR
                [[ "$t" =~ ^[A-Za-z0-9.-]+$ ]] || { echo "ERROR: Invalid domain: $t"; exit 1; }
                if [[ -f "$DOMAIN_DIR/$t.conf" ]]; then
                    TARGET_DOMAINS+=("$t")
                else
                    echo "ERROR: Domain config not found: $DOMAIN_DIR/$t.conf"
                fi ;;
        esac
    done
    # same domain twice, e.g. a name plus --user
    mapfile -t TARGET_DOMAINS < <(printf '%s\n' "${TARGET_DOMAINS[@]}" | awk 'NF && !seen[$0]++')
}

# --json and --hours can go anywhere
JSON=false
HOURS=24
ARGS=()
while (( $# )); do
    case "$1" in
        --json)  JSON=true ;;
        --hours) HOURS="${2:-}"; shift; [[ "$HOURS" =~ ^[0-9]+$ ]] && (( HOURS > 0 && HOURS <= 720 )) || { echo "ERROR: --hours must be 1-720."; exit 1; } ;;
        *)       ARGS+=("$1") ;;
    esac
    shift
done
set -- "${ARGS[@]+"${ARGS[@]}"}"

ACTION="${1:-status}"

if [[ "$ACTION" == "status" ]]; then
    status "$JSON"
    exit 0
fi

if [[ "$ACTION" == "stats" ]]; then
    shift
    (( $# )) || set -- --all
    resolve_targets "$@"
    stats "$JSON" "$HOURS" "${TARGET_DOMAINS[@]+"${TARGET_DOMAINS[@]}"}"
    exit 0
fi

if [[ "$ACTION" == "default" ]]; then
    [[ $# -eq 3 ]] && is_limit "$2" && is_limit "$3" || usage
    prepare
    write_ratelimit_conf "$2" "$3"
    echo "Server default set to ${2}/min per IP, ${3}/min per domain (0 = off)."
    apply_caddy
    exit 0
fi

# <target...> then default, off or <per_ip> <per_site>
(( $# >= 2 )) || usage
ARGV=("$@")
LAST="${ARGV[-1]}"
case "$LAST" in
    default|off)
        MODE="$LAST"; IP=0; SITE=0
        TARGETS=("${ARGV[@]:0:${#ARGV[@]}-1}") ;;
    *)
        (( $# >= 3 )) && is_limit "${ARGV[-2]}" && is_limit "$LAST" || usage
        MODE="custom"; IP="${ARGV[-2]}"; SITE="$LAST"
        TARGETS=("${ARGV[@]:0:${#ARGV[@]}-2}") ;;
esac

resolve_targets "${TARGETS[@]}"
(( ${#TARGET_DOMAINS[@]} )) || { echo "ERROR: No domains to change."; exit 1; }

prepare
for domain in "${TARGET_DOMAINS[@]}"; do
    set_domain "$DOMAIN_DIR/$domain.conf" "$MODE" "$IP" "$SITE"
done
apply_caddy
