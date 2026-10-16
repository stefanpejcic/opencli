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
