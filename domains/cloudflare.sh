#!/bin/bash
################################################################################
# Script Name: domains/cloudflare.sh
# Description: Enable/disable/check Cloudflare-only access to domains.
# Usage: opencli domains-cloudflare <enable|disable|status> <DOMAIN_NAME|--all> [-y]
# Author: Stefan Pejcic
# Created: 30.09.2026
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

set -euo pipefail

# ANSI color codes
RED='\033[1;31m'
GREEN='\033[1;32m'
NC='\033[0m'

# Conf
TEMPLATE_DIR="/etc/openpanel/caddy/templates"
OUTPUT="${TEMPLATE_DIR}/cloudflare.only"
DOMAIN_DIR="/etc/openpanel/caddy/domains"
DOMAIN_TEMPLATE="${TEMPLATE_DIR}/domain.conf"
TMP="${OUTPUT}.tmp"

ACTION="${1:-}"
TARGET="${2:-}"
AUTO_YES="${3:-}"

# Usage
if [[ "$ACTION" != "enable" && "$ACTION" != "disable" && "$ACTION" != "status" && -n "$ACTION" ]]; then
    echo "Usage:"
    echo "  opencli domains-cloudflare"
    echo "  opencli domains-cloudflare enable <domain|--all> [-y]"
    echo "  opencli domains-cloudflare disable <domain|--all> [-y]"
    echo "  opencli domains-cloudflare status [domain|--all]"
    exit 1
fi

if [[ "$ACTION" == "enable" || "$ACTION" == "disable" ]]; then
    if [[ -z "$TARGET" ]]; then
        echo "ERROR: Target is required."
        echo "Usage: opencli domains-cloudflare $ACTION <domain|--all> [-y]"
        exit 1
    fi

    if [[ "$TARGET" != "--all" && "$TARGET" == -* ]]; then
        echo "ERROR: Invalid target: $TARGET"
        echo "Usage: opencli domains-cloudflare $ACTION <domain|--all> [-y]"
        exit 1
    fi
fi

# Default TARGET for status action if left empty
if [[ "$ACTION" == "status" && -z "$TARGET" ]]; then
    TARGET="--all"
fi

# Helpers
patch_config() {
    local file="$1"

    sed -i '/^[[:space:]]*import[[:space:]]\+cloudflare-only[[:space:]]*$/d' "$file"

    awk '
        /^[[:space:]]*https?:\/\/.*\{[[:space:]]*$/ {
            print
            print "  import cloudflare-only"
            next
        }
        { print }
    ' "$file" > "${file}.tmp"

    mv "${file}.tmp" "$file"

    echo "Enabled: $file"
}

remove_config() {
    local file="$1"
    sed -i '/^[[:space:]]*import[[:space:]]\+cloudflare-only[[:space:]]*$/d' "$file"
    echo "Disabled: $file"
}

check_domain_status() {
    local file="$1"
    if grep -qEs '^[[:space:]]*import[[:space:]]+cloudflare-only([[:space:]]|$)' "$file"; then
        return 0 # Enabled
    else
        return 1 # Disabled
    fi
}

reload_caddy() {
    local mode="${1:-reload}"
    echo "Reloading Caddy to apply the setting..."
    local reload_output
    local cmd

    if [[ "$mode" == "restart" ]]; then
        cmd="podman restart caddy"
    else
        cmd="podman exec caddy caddy reload --config /etc/caddy/Caddyfile"
    fi

    if reload_output=$(eval "$cmd" 2>&1); then
        echo "SUCCESS: Caddy reloaded successfully."
    else
        echo "WARNING: Failed to reload Caddy."
        if [[ -n "$reload_output" ]]; then
            echo "$reload_output"
        fi
        return 1
    fi
}

update_cloudflare_template() {
    echo "Updating list of Cloudflare IP ranges..."

    CF_IPV4_RAW=$(curl -fsSL https://www.cloudflare.com/ips-v4)
    CF_IPV6_RAW=$(curl -fsSL https://www.cloudflare.com/ips-v6)

    if [[ -z "$CF_IPV4_RAW" || -z "$CF_IPV6_RAW" ]]; then
        echo "ERROR: Failed to retrieve Cloudflare IP ranges."
        exit 1
    fi

    mapfile -t CF_IPV4 < <(echo "$CF_IPV4_RAW" | sed '/^[[:space:]]*$/d')
    mapfile -t CF_IPV6 < <(echo "$CF_IPV6_RAW" | sed '/^[[:space:]]*$/d')

    echo "Retrieved ${#CF_IPV4[@]} IPv4 Cloudflare ranges:"
    for ip in "${CF_IPV4[@]}"; do
        echo "  $ip"
    done

    echo "Retrieved ${#CF_IPV6[@]} IPv6 Cloudflare ranges:"
    for ip in "${CF_IPV6[@]}"; do
        echo "  $ip"
    done

    IPS=$(printf '%s\n%s\n' "$CF_IPV4_RAW" "$CF_IPV6_RAW" | sed '/^[[:space:]]*$/d')

    {
        echo '(cloudflare-only) {'
        echo '    @not_cloudflare {'
        echo '        not remote_ip \'

        mapfile -t IP_LIST <<< "$IPS"

        last=$(( ${#IP_LIST[@]} - 1 ))

        for i in "${!IP_LIST[@]}"; do
            if (( i == last )); then
                printf '            %s\n' "${IP_LIST[$i]}"
            else
                printf '            %s \\\n' "${IP_LIST[$i]}"
            fi
        done

        echo '    }'
        echo
        echo '    respond @not_cloudflare "Access allowed only through Cloudflare" 403'
        echo '}'
    } > "$TMP"

    mv "$TMP" "$OUTPUT"

    echo "Updated: $OUTPUT"
}

# STATUS ACTION
if [[ "$ACTION" == "status" ]]; then
    if [[ "$TARGET" == "--all" ]]; then
        shopt -s nullglob
        DOMAIN_FILES=("${DOMAIN_DIR}"/*.conf)

        if (( ${#DOMAIN_FILES[@]} == 0 )); then
            echo "No existing domain configs found."
            exit 0
        fi

        enabled_count=0
        disabled_count=0

        echo "Cloudflare-only status for all domains:"
        echo "----------------------------------------"
        for FILE in "${DOMAIN_FILES[@]}"; do
            domain_name=$(basename "$FILE" .conf)
            if check_domain_status "$FILE"; then
                echo -e "  $domain_name: ${GREEN}ENABLED${NC}"
                ((enabled_count++))
            else
                echo -e "  $domain_name: ${RED}DISABLED${NC}"
                ((disabled_count++))
            fi
        done
        echo "----------------------------------------"
        echo "Summary: $enabled_count enabled, $disabled_count disabled out of ${#DOMAIN_FILES[@]} domain(s)."
        exit 0
    else
        DOMAIN_FILE="$DOMAIN_DIR/$TARGET.conf"
        if [[ ! -f "$DOMAIN_FILE" ]]; then
            echo "ERROR: Domain config not found: $DOMAIN_FILE"
            exit 1
        fi

        if check_domain_status "$DOMAIN_FILE"; then
            echo -e "Cloudflare-only mode for $TARGET is ${GREEN}ENABLED${NC}."
        else
            echo -e "Cloudflare-only mode for $TARGET is ${RED}DISABLED${NC}."
        fi
        exit 0
    fi
fi

# ENABLE / DISABLE
if [[ "$ACTION" == "enable" || "$ACTION" == "disable" ]]; then

    # --all
    if [[ "$TARGET" == "--all" ]]; then

        if [[ "$AUTO_YES" != "-y" ]]; then
            if [[ "$ACTION" == "enable" ]]; then
                echo -e "WARNING: This will ${GREEN}ENABLE${NC} Cloudflare-only mode globally (for all current & new domains)."
                echo "Ensure all domains are proxied via Cloudflare, or they will return a 403 error."
            else
                echo -e "WARNING: This will ${RED}DISABLE${NC} Cloudflare-only mode globally (for all current & new domains)."
                echo "Direct server traffic will be permitted for all domains."
            fi

            read -t 15 -p "Proceed? (y/N) [15s]: " CONFIRM || true
            echo ""

            if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
                echo "Operation cancelled or timed out."
                exit 1
            fi
        fi

        shopt -s nullglob
        DOMAIN_FILES=("${DOMAIN_DIR}"/*.conf)

        if (( ${#DOMAIN_FILES[@]} > 0 )); then
            for FILE in "${DOMAIN_FILES[@]}"; do
                if [[ "$ACTION" == "enable" ]]; then
                    patch_config "$FILE"
                else
                    remove_config "$FILE"
                fi
            done
            echo "Total domains ${ACTION}d: ${#DOMAIN_FILES[@]}"
        else
            echo "No existing domain configs found."
        fi

        # only --all modifies the template.
        if [[ ! -f "$DOMAIN_TEMPLATE" ]]; then
            echo "ERROR: Domain template not found: $DOMAIN_TEMPLATE"
            exit 1
        fi

        if [[ "$ACTION" == "enable" ]]; then
            patch_config "$DOMAIN_TEMPLATE"
            update_cloudflare_template
        fi

        reload_caddy restart
        exit 0

    # single domain
    else

        DOMAIN_FILE="$DOMAIN_DIR/$TARGET.conf"

        if [[ ! -f "$DOMAIN_FILE" ]]; then
            echo "ERROR: Domain config not found: $DOMAIN_FILE"
            exit 1
        fi

        if [[ "$ACTION" == "enable" ]]; then
            if [[ ! -f "$OUTPUT" ]]; then
                echo "Cloudflare-only template missing. Generating it before enabling..."
                update_cloudflare_template
            fi
            patch_config "$DOMAIN_FILE"
            reload_caddy reload
            exit 0
        else
            remove_config "$DOMAIN_FILE"
            if ! grep -RqsE '^[[:space:]]*import[[:space:]]+cloudflare-only([[:space:]]|$)' "$DOMAIN_DIR"/*.conf 2>/dev/null; then
                rm -f "$OUTPUT"
                echo "Removed: $OUTPUT"
            fi
            reload_caddy reload
            exit 0
        fi
    fi
fi

if [[ "$ACTION" == "" ]]; then
    USED_COUNT=$(grep -lEs '^[[:space:]]*import[[:space:]]+cloudflare-only([[:space:]]|$)' "$DOMAIN_DIR"/*.conf 2>/dev/null | wc -l || true)

    if (( USED_COUNT == 0 )); then
        echo "Cloudflare-only template is not in use. Skipping."
        exit 0
    else
        echo "Cloudflare-only setting is used by $USED_COUNT domains."
        update_cloudflare_template
        #reload_caddy reload
    fi
fi
