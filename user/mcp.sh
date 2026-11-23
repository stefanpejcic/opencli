#!/bin/bash
################################################################################
# Script Name: user/mcp.sh
# Description: Create, list or revoke AI Assistant (MCP) tokens for an OpenPanel user and print client setup.
# Usage: opencli user-mcp <username> [--name=NAME] [--readonly|--no-deletes] [--features=a,b] [--expires=DAYS] [--client=NAME] [--json]
#        opencli user-mcp <username> --list [--json]
#        opencli user-mcp <username> --revoke=<ID>
# Author: Stefan Pejcic
# Created: 23.11.2026
# Last Modified: 23.11.2026
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

# ======================================================================
# Helpers

show_usage() {
    cat <<EOF

Usage: opencli user-mcp <USERNAME> [options]
Docs:  https://openpanel.com/docs/opencli/user/#ai-assistant-mcp-tokens

Create a token and print how to connect an MCP client with it:
  --name=NAME        Token name (default: "opencli YYYY-MM-DD").
  --readonly         The token can only look things up.
  --no-deletes       The token can create and change things, but not delete, restore or change passwords.
  --features=a,b     Limit the token to these features, e.g. dns,domains.
  --expires=DAYS     Expire the token after DAYS days (default: never).
  --client=NAME      Only print setup for one client: claude-code, claude-desktop, codex, cursor, vscode, gemini.
  --json             Print the token and MCP URL as JSON instead of setup instructions.

Manage tokens:
  --list             List the user's tokens (never the token values).
  --revoke=ID        Revoke one token by its ID from --list.

Examples:
  opencli user-mcp stefan
  opencli user-mcp stefan --no-deletes --features=dns,domains --expires=30 --client=claude-code
  opencli user-mcp stefan --list
  opencli user-mcp stefan --revoke=4

EOF
}

die() {
    echo "Error: $1" >&2
    exit 1
}

# same lookup as user-login, the MCP endpoint lives on the user panel
get_openpanel_url() {
    local port caddyfile domain_block domain ip
    port=$(opencli port 2>/dev/null)
    port=${port:-2083}
    caddyfile="/etc/openpanel/caddy/Caddyfile"

    domain_block=$(awk '/# START USERPANEL DOMAIN #/{flag=1; next} /# END USERPANEL DOMAIN #/{flag=0} flag {print}' "$caddyfile" 2>/dev/null)
    [ -z "$domain_block" ] && domain_block=$(awk '/# START HOSTNAME DOMAIN #/{flag=1; next} /# END HOSTNAME DOMAIN #/{flag=0} flag {print}' "$caddyfile" 2>/dev/null)
    domain=$(echo "$domain_block" | sed '/^\s*$/d' | grep -v '^\s*#' | head -n1 | sed 's/[[:space:]]*{//' | xargs)
    domain="${domain#http://}"
    domain="${domain#https://}"

    if [ -n "$domain" ] && [ "$domain" != "example.net" ]; then
        if { [ -f "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/${domain}/${domain}.crt" ]; } || \
           { [ -f "/etc/openpanel/caddy/ssl/custom/${domain}/${domain}.crt" ]; }; then
            openpanel_url="https://${domain}:${port}"
            return 0
        fi
    fi

    ip=$(curl --silent --max-time 2 -4 "https://ip.openpanel.com" || curl --silent --max-time 2 -4 "https://ifconfig.me/ip")
    if ! [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        ip=$(hostname -I | awk '{print $1}')
    fi
    openpanel_url="http://${ip}:${port}"
}

db() {
    # shellcheck disable=SC2154 # config_file and mysql_database come from db.sh, sourced below
    mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s -e "$1"
}

# the panel adds these on first use too, both sides use plain ADD COLUMN so it runs on MySQL and MariaDB
ensure_scope_columns() {
    local col def has
    for col in mode allowed_features; do
        has=$(db "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'mcp_tokens' AND COLUMN_NAME = '$col';") || return 1
        [ "$has" = "1" ] && continue
        def="VARCHAR(10) NULL DEFAULT NULL"
        [ "$col" = "allowed_features" ] && def="TEXT NULL DEFAULT NULL"
        db "ALTER TABLE mcp_tokens ADD COLUMN $col $def;" || return 1
    done
}

scope_columns_present() {
    [ "$(db "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'mcp_tokens' AND COLUMN_NAME IN ('mode', 'allowed_features');" 2>/dev/null)" = "2" ]
}

# features.txt next to the account wins, else the plan's feature set, same order as the panel
user_features() {
    local file set
    file="/home/${user_context}/features.txt"
    if [ ! -f "$file" ]; then
        set=$(db "SELECT plans.feature_set FROM users JOIN plans ON users.plan_id = plans.id WHERE users.id = $user_id;" 2>/dev/null)
        file="/etc/openpanel/openpanel/features/${set}.txt"
        [ -f "$file" ] || file="/etc/openpanel/openpanel/features/default.txt"
    fi
    sed 's/[[:space:]]//g; /^$/d' "$file" 2>/dev/null
}

log_activity() {
    local dir="/etc/openpanel/openpanel/core/users/${username}"
    [ -d "$dir" ] || return 0
    echo "$(date '+%Y-%m-%d %H:%M:%S')  127.0.0.1 User ${username} $1" >> "$dir/activity.log" 2>/dev/null
}

print_client() {
    local client="$1" url="$2" token="$3"
    case "$client" in
        claude-code)
            cat <<EOF
Claude Code (run in a terminal):

  claude mcp add --transport http openpanel ${url} \\
    --header "Authorization: Bearer ${token}"

EOF
            ;;
        claude-desktop)
            cat <<EOF
Claude Desktop (claude_desktop_config.json, needs Node.js for mcp-remote):

  "openpanel": {
    "command": "npx",
    "args": ["-y", "mcp-remote", "${url}", "--header", "Authorization:\${OPENPANEL_AUTH}"],
    "env": { "OPENPANEL_AUTH": "Bearer ${token}" }
  }

EOF
            ;;
        codex)
            cat <<EOF
Codex (~/.codex/config.toml):

  [mcp_servers.openpanel]
  url = "${url}"
  http_headers = { Authorization = "Bearer ${token}" }

EOF
            ;;
        cursor)
            cat <<EOF
Cursor (~/.cursor/mcp.json):

  {
    "mcpServers": {
      "openpanel": {
        "url": "${url}",
        "headers": { "Authorization": "Bearer ${token}" }
      }
    }
  }

EOF
            ;;
        vscode)
            cat <<EOF
VS Code / GitHub Copilot (.vscode/mcp.json):

  {
    "servers": {
      "openpanel": {
        "type": "http",
        "url": "${url}",
        "headers": { "Authorization": "Bearer ${token}" }
      }
    }
  }

EOF
            ;;
        gemini)
            cat <<EOF
Gemini CLI (run in a terminal):

  gemini mcp add --transport http --header "Authorization: Bearer ${token}" openpanel ${url}

EOF
            ;;
    esac
}

CLIENTS="claude-code claude-desktop codex cursor vscode gemini"


# ======================================================================
# Validations

username="$1"
if [ -z "$username" ] || [[ "$username" == -* ]]; then
    show_usage
    exit 1
fi
shift

token_name=""
mode="full"
features=""
expires=0
client=""
json=false
action="create"
revoke_id=""

for arg in "$@"; do
    case "$arg" in
        --name=*)     token_name="${arg#*=}" ;;
        --readonly)   mode="readonly" ;;
        --no-deletes) mode="modify" ;;
        --features=*) features="${arg#*=}" ;;
        --expires=*)  expires="${arg#*=}" ;;
        --client=*)   client="${arg#*=}" ;;
        --json)       json=true ;;
        --list)       action="list" ;;
        --revoke=*)   action="revoke"; revoke_id="${arg#*=}" ;;
        -h|--help)    show_usage; exit 0 ;;
        *)            show_usage; die "unknown option '$arg'" ;;
    esac
done

[[ "$username" =~ ^[A-Za-z0-9_.-]+$ ]] || die "invalid username '$username'"
[[ "$expires" =~ ^[0-9]+$ ]] || die "--expires takes a number of days"
if [ -n "$client" ] && ! [[ " $CLIENTS " == *" $client "* ]]; then
    die "--client must be one of: $CLIENTS"
fi
if [ -z "$token_name" ]; then
    token_name="opencli $(date +%Y-%m-%d)"
fi
[ "${#token_name}" -le 100 ] || die "--name is longer than 100 characters"

# normalised and checked here, the panel ignores unknown names but a typo would make a token that can do nothing
if [ -n "$features" ]; then
    features=$(echo "$features" | tr ',' '\n' | sed 's/[[:space:]]//g; /^$/d' | sort -u | paste -sd, -)
    [[ "$features" =~ ^[a-z0-9_]+(,[a-z0-9_]+)*$ ]] || die "--features takes feature names like dns,domains"
fi

license_key=$(grep "^key=" "/etc/openpanel/openpanel/conf/openpanel.config" 2>/dev/null | cut -d'=' -f2-)
if [ -z "$license_key" ]; then
    echo "Error: OpenPanel Community edition does not support API access, which MCP tokens need. Please consider purchasing the Enterprise version."
    # shellcheck disable=SC1091
    source "/usr/local/opencli/lib/enterprise.sh"
    echo "$ENTERPRISE_LINK"
    exit 1
fi

# shellcheck disable=SC1091
source /usr/local/opencli/db.sh

user_row=$(db "SELECT id, server FROM users WHERE username='$(mysql_escape "$username")' LIMIT 1;") || die "could not look up user '$username' in the database"
user_id=$(echo "$user_row" | awk '{print $1}')
user_context=$(echo "$user_row" | awk '{print $2}')
[[ "$user_id" =~ ^[0-9]+$ ]] || die "user '$username' does not exist"
user_context=${user_context:-$username}


# ======================================================================
# Main

case "$action" in
    list)
        if scope_columns_present; then
            rows=$(db "SELECT id, name, token_prefix, IFNULL(mode, IF(read_only, 'readonly', 'full')), IFNULL(allowed_features, ''), created_at, IFNULL(expires_at, ''), IFNULL(last_used_at, '') FROM mcp_tokens WHERE user_id = $user_id ORDER BY id;" | tr '\t' '|')
        else
            rows=$(db "SELECT id, name, token_prefix, IF(read_only, 'readonly', 'full'), '', created_at, IFNULL(expires_at, ''), IFNULL(last_used_at, '') FROM mcp_tokens WHERE user_id = $user_id ORDER BY id;" | tr '\t' '|')
        fi
        if [ "$json" = true ]; then
            echo "$rows" | awk -F'|' 'BEGIN { printf "[" } NF { gsub(/\\/, "\\\\", $2); gsub(/"/, "\\\"", $2); printf "%s{\"id\":%s,\"name\":\"%s\",\"prefix\":\"%s\",\"mode\":\"%s\",\"features\":\"%s\",\"created_at\":\"%s\",\"expires_at\":\"%s\",\"last_used_at\":\"%s\"}", (n++ ? "," : ""), $1, $2, $3, $4, $5, $6, $7, $8 } END { print "]" }'
            exit 0
        fi
        if [ -z "$rows" ]; then
            echo "User $username has no MCP tokens."
            exit 0
        fi
        { echo "ID|NAME|TOKEN|ACCESS|FEATURES|CREATED|EXPIRES|LAST USED"; echo "$rows" | sed 's/|readonly|/|read-only|/; s/|modify|/|no deletes|/'; } | column -t -s'|'
        exit 0
        ;;

    revoke)
        [[ "$revoke_id" =~ ^[0-9]+$ ]] || die "--revoke takes a token ID from --list"
        name=$(db "SELECT name FROM mcp_tokens WHERE id = $revoke_id AND user_id = $user_id;")
        [ -n "$name" ] || die "user $username has no token with ID $revoke_id"
        db "DELETE FROM mcp_tokens WHERE id = $revoke_id AND user_id = $user_id;" || die "could not revoke the token"
        log_activity "revoked MCP token #${revoke_id} via opencli"
        echo "Revoked MCP token #${revoke_id} (${name}) for user ${username}. Clients using it stop working right away."
        exit 0
        ;;
esac

if [ "$mode" != "full" ] || [ -n "$features" ]; then
    ensure_scope_columns || die "could not add the mode and allowed_features columns to mcp_tokens, only plain full-access or read-only tokens can be made"
fi

# same shape as the panel's own tokens: op_mcp_ + 32 random bytes in unpadded base64url, stored as a sha256
raw_token="op_mcp_$(openssl rand 32 | base64 | tr '+/' '-_' | tr -d '=\n')"
token_prefix="${raw_token:0:13}"
token_hash=$(printf '%s' "$raw_token" | sha256sum | cut -d' ' -f1)
read_only=0
[ "$mode" = "readonly" ] && read_only=1
expires_sql="NULL"
[ "$expires" -gt 0 ] && expires_sql="DATE_ADD(NOW(), INTERVAL $expires DAY)"
features_sql="NULL"
[ -n "$features" ] && features_sql="'$features'"

if scope_columns_present; then
    insert="INSERT INTO mcp_tokens (user_id, name, token_prefix, token_hash, read_only, expires_at, mode, allowed_features) VALUES ($user_id, '$(mysql_escape "$token_name")', '$token_prefix', '$token_hash', $read_only, $expires_sql, '$mode', $features_sql);"
else
    insert="INSERT INTO mcp_tokens (user_id, name, token_prefix, token_hash, read_only, expires_at) VALUES ($user_id, '$(mysql_escape "$token_name")', '$token_prefix', '$token_hash', $read_only, $expires_sql);"
fi
db "$insert" || die "could not save the token"

note=""
case "$mode" in
    readonly) note=", read-only" ;;
    modify)   note=", no deletes" ;;
esac
[ -n "$features" ] && note="${note}, features: ${features//,/ }"
[ "$expires" -gt 0 ] && note="${note}, expires in ${expires}d"
log_activity "created a new MCP token (${token_name}${note}) via opencli"

get_openpanel_url
mcp_url="${openpanel_url}/mcp"

if [ "$json" = true ]; then
    printf '{"token":"%s","url":"%s","name":"%s","mode":"%s","features":"%s","expires_in_days":%s}\n' \
        "$raw_token" "$mcp_url" "$(printf '%s' "$token_name" | sed 's/\\/\\\\/g; s/"/\\"/g')" "$mode" "$features" "$expires"
    exit 0
fi

echo "Created MCP token \"${token_name}\" for user ${username}${note}."
echo "It is shown only once. Revoke it with: opencli user-mcp ${username} --revoke=<ID from --list>"
echo
echo "MCP URL: ${mcp_url}"
echo "Token:   ${raw_token}"
echo

for c in ${client:-$CLIENTS}; do
    print_client "$c" "$mcp_url" "$raw_token"
done

# warn instead of refusing, the admin may be about to change the plan
plan_features=$(user_features)
for needed in api mcp; do
    if ! echo "$plan_features" | grep -qx "$needed"; then
        echo "Warning: the '$needed' feature isn't enabled for ${username}, so this token won't work until it is (edit the user's feature set)."
    fi
done
enabled_modules=$(grep "^enabled_modules=" /etc/openpanel/openpanel/conf/openpanel.config 2>/dev/null | cut -d'=' -f2-)
for needed in api mcp; do
    if [ -n "$enabled_modules" ] && ! echo ",$enabled_modules," | grep -q ",$needed,"; then
        echo "Warning: the '$needed' module is disabled server-wide, enable it in OpenAdmin > Settings > Modules."
    fi
done
