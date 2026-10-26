#!/bin/bash
# ======================================================================
# Shared reseller limits helpers, sourced by scripts that create or
# change accounts owned by a reseller. Only defines functions.
# ======================================================================

RESELLERS_DIR="/etc/openpanel/openadmin/resellers"
RESELLER_TEMPLATE="/etc/openpanel/openadmin/config/reseller_template.json"

# prints the reseller's limits file path, creating it from the template if it's missing
reseller_limits_file() {
    local reseller="$1"
    local file="${RESELLERS_DIR}/${reseller}.json"
    if [[ ! -f "$file" ]]; then
        mkdir -p "$RESELLERS_DIR"
        if [[ -f "$RESELLER_TEMPLATE" ]]; then
            cp "$RESELLER_TEMPLATE" "$file"
        else
            echo '{"max_accounts":0,"current_accounts":0,"allowed_plans":[],"max_disk_blocks":0,"current_disk_blocks":0}' > "$file"
        fi
    fi
    echo "$file"
}

# turns "1,2" or ["1",2] into a JSON array of numbers
reseller_plans_json() {
    local input="$1"
    if [[ "$input" == \[*\] ]]; then
        jq -c 'map(tonumber? // empty)' <<< "$input"
    else
        tr ',' '\n' <<< "$input" | jq -R 'tonumber? // empty' | jq -sc .
    fi
}

# returns 0 if plan id is in the reseller's allowed_plans, ids may be stored as strings or numbers
reseller_plan_allowed() {
    local file="$1" plan_id="$2"
    jq -e --arg id "$plan_id" '(.allowed_plans // []) | map(tostring) | index($id) != null' "$file" >/dev/null 2>&1
}

# returns 0 if the reseller is at or over max_disk_blocks, 0 means unlimited
reseller_over_disk() {
    local file="$1"
    jq -e '((.max_disk_blocks // 0) | tonumber? // 0) as $max | $max > 0 and (((.current_disk_blocks // 0) | tonumber? // 0) >= $max)' "$file" >/dev/null 2>&1
}

RESELLER_PLANS_DIR="/etc/openpanel/openadmin/reseller_plans"

# prints the reseller plan's file path, fails if the name is invalid or the plan doesn't exist
reseller_plan_file() {
    local name="$1"
    [[ "$name" =~ ^[A-Za-z0-9_-]{1,64}$ ]] || return 1
    local file="${RESELLER_PLANS_DIR}/${name}.json"
    [[ -f "$file" ]] && jq empty "$file" >/dev/null 2>&1 || return 1
    echo "$file"
}

# reseller logo URLs end up in every owned account's .env, so only plain http(s) URLs: no quotes, $, backticks, backslashes or spaces
reseller_logo_url_valid() {
    local url="$1"
    local re='^https?://[A-Za-z0-9._~:/?#@!&()*+,;=%[-]+$'
    [[ -z "$url" ]] && return 0
    (( ${#url} <= 2048 )) && [[ "$url" =~ $re ]]
}

# writes RESELLER_LOGO_URL into an account's .env as a literal line, never through sed (GNU sed's e command runs as root).
# runs as the account's own uid, so a symlink the account planted in its home can only reach files it could already write
reseller_env_set_logo() {
    local env_file="$1" logo_url="$2" owner
    owner=$(stat -c '%u:%g' "$(dirname "$env_file")" 2>/dev/null) || return 1
    [[ "$owner" =~ ^[0-9]+:[0-9]+$ && "${owner%%:*}" -ge 1000 ]] || return 1
    # shellcheck disable=SC2016 # expanded by the inner bash
    setpriv --reuid="${owner%%:*}" --regid="${owner##*:}" --clear-groups -- bash -c '
        f="$1"; v="$2"
        [[ -f "$f" ]] || exit 1
        t=$(mktemp "$f.XXXXXX") || exit 1
        if { grep -v "^RESELLER_LOGO_URL=" "$f"; printf "RESELLER_LOGO_URL=\"%s\"\n" "$v"; } > "$t" && chmod 600 "$t" && mv -f "$t" "$f"; then
            exit 0
        fi
        rm -f "$t"; exit 1
    ' _ "$env_file" "$logo_url"
}
