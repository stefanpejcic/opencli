#!/bin/bash
################################################################################
# Script Name: domains/ratelimit.sh
# Description: Set the per-ip and per-domain request rate limits in Caddy, server default or per domain.
# Usage: opencli domains-ratelimit [status]
#        opencli domains-ratelimit default <per_ip> <per_site>
#        opencli domains-ratelimit <DOMAIN_NAME|--all> <per_ip> <per_site>|default|off
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
    echo "  opencli domains-ratelimit [status]                                  show the server default and every domain"
    echo "  opencli domains-ratelimit default <per_ip> <per_site>               server default, used by domains set to 'default'"
    echo "  opencli domains-ratelimit <domain|--all> <per_ip> <per_site>        custom limits for a domain"
    echo "  opencli domains-ratelimit <domain|--all> default|off                use the server default, or no limits"
    echo ""
    echo "Limits are requests per minute, per visitor IP and per domain; 0 turns that limit off."
    echo "Example: opencli domains-ratelimit default 300 6000"
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

status() {
    local d_ip d_site file
    read -r d_ip d_site < <(read_default)
    if (( d_ip == 0 && d_site == 0 )); then
        echo "Server default: off"
    else
        echo "Server default: ${d_ip}/min per IP, ${d_site}/min per domain (0 = off)"
    fi
    echo "----------------------------------------"
    shopt -s nullglob
    for file in "$DOMAIN_DIR"/*.conf; do
        local setting
        setting=$(domain_setting "$file")
        case "$setting" in
            default|off) echo "  $(basename "$file" .conf): $setting" ;;
            *)           read -r ip site <<< "$setting"; echo "  $(basename "$file" .conf): ${ip}/min per IP, ${site}/min per domain" ;;
        esac
    done
}

TARGET="${1:-status}"

if [[ "$TARGET" == "status" ]]; then
    status
    exit 0
fi

if [[ "$TARGET" == "default" ]]; then
    [[ $# -eq 3 ]] && is_limit "$2" && is_limit "$3" || usage
    prepare
    write_ratelimit_conf "$2" "$3"
    echo "Server default set to ${2}/min per IP, ${3}/min per domain (0 = off)."
    apply_caddy
    exit 0
fi

# domain or --all
MODE="${2:-}"
case "$MODE" in
    default|off) [[ $# -eq 2 ]] || usage; IP=0; SITE=0 ;;
    *)           [[ $# -eq 3 ]] && is_limit "$2" && is_limit "$3" || usage; IP="$2"; SITE="$3"; MODE="custom" ;;
esac

shopt -s nullglob
if [[ "$TARGET" == "--all" ]]; then
    FILES=("$DOMAIN_DIR"/*.conf)
    (( ${#FILES[@]} )) || { echo "No domain configs found."; exit 0; }
else
    [[ "$TARGET" == -* ]] && usage
    FILES=("$DOMAIN_DIR/$TARGET.conf")
    [[ -f "${FILES[0]}" ]] || { echo "ERROR: Domain config not found: ${FILES[0]}"; exit 1; }
fi

prepare
for file in "${FILES[@]}"; do
    set_domain "$file" "$MODE" "$IP" "$SITE"
done
apply_caddy
