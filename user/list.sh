#!/bin/bash
################################################################################
# Script Name: user/list.sh
# Description: Display all users: id, username, email, plan, registered date,
#              online status, 2FA, IP, domain count, resource usage, notes.
# Usage: opencli user-list [--json] [--total]
# Docs: https://docs.openpanel.com
# Author: Stefan Pejcic
# Created: 16.10.2023
# Last Modified: 01.09.2026
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

print_usage() {
    echo "Usage: opencli user-list [--json] [--total]"
    exit 1
}

json_output=false
total_users=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --quota)
            opencli user-quotas
            exit 0
            ;;
        --json)
            json_output=true
            shift
            ;;
        --total)
            total_users=true
            shift
            ;;
        *)
            print_usage
            ;;
    esac
done


# DB
source /usr/local/opencli/db.sh
# shellcheck disable=SC1091
source /usr/local/opencli/lib/requirement.sh
# shellcheck disable=SC1091
source /usr/local/opencli/lib/redis.sh


# Count total users
if [ "$total_users" = true ]; then
    user_count=$(timeout 10 mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -se "SELECT COUNT(*) FROM users")
    if [ "$json_output" = true ]; then
        echo "$user_count"
    else
        echo "Total number of users: $user_count"
    fi

exit 0
fi


# ======================================================================
# The following mirrors how openadmin's /users page (users.go,
# users_all.js) sources this same per-user info, so the CLI and the UI
# never disagree:
#   - online:  Redis "session:*" hashes, HGET'd for their "username" field
#              (user_activity.go)
#   - 2fa:     users.twofa_enabled column (paneldb/users.go)
#   - ip:      /etc/openpanel/openpanel/core/users/<user>/ip.json if present
#              (dedicated), else the server's shared IP (users.go firstIPFor,
#              user/ip.sh)
#   - domains: COUNT(*) of the domains table per user (user_account_settings.go)
#   - usage:   last JSON line of /home/<context>/resource_usage.txt for
#              RAM/CPU (docker/collect_stats.sh writes it, users.go reads it),
#              and /etc/openpanel/openpanel/quota_report.json for disk/inodes
#              (user/quota.sh writes it, users.go readDiskUsageAll reads it)
#   - notes:   /home/<context>/notes.txt (users.go readUserNotes)
# ======================================================================

QUOTA_REPORT_PATH="/etc/openpanel/openpanel/quota_report.json"
IP_FILES_BASE="/etc/openpanel/openpanel/core/users"

# strip_suspended_prefix mirrors users.go's stripSuspendedPrefix: a
# "SUSPENDED_<id>_<username>" account name is reduced to "<username>" by
# cutting everything up to the LAST underscore; a normal username is
# returned unchanged.
strip_suspended_prefix() {
    local u="$1"
    if [[ "$u" == *"SUSPENDED_"* ]]; then
        echo "${u##*_}"
    else
        echo "$u"
    fi
}

# build_online_users scans Redis (via the openpanel_redis container, same as
# lib/redis.sh) for live openpanel login sessions and fills the
# ONLINE_USERS assoc array with every username that owns at least one.
declare -A ONLINE_USERS
build_online_users() {
    local keys key uname
    keys=$(redis_cli --scan --pattern "session:*" 2>/dev/null)
    [ -z "$keys" ] && return 0
    while IFS= read -r key; do
        [ -z "$key" ] && continue
        uname=$(redis_cli HGET "$key" username 2>/dev/null)
        [ -n "$uname" ] && ONLINE_USERS["$uname"]=1
    done <<< "$keys"
}

# humangb converts a quota_report.json KB figure to the same "N.NNGB" string
# users.go's humanGB() renders (KB / 1024000). Forced to the C locale so the
# decimal point is always "." regardless of the server's locale.
humangb() {
    LC_NUMERIC=C awk -v v="${1:-0}" 'BEGIN { printf "%.2fGB", v/1024000 }'
}

get_dedicated_ip() {
    local plain="$1" ip_file="$IP_FILES_BASE/$plain/ip.json"
    if [ -f "$ip_file" ]; then
        jq -r '.ip // empty' "$ip_file" 2>/dev/null
    fi
}

get_user_notes() {
    local context="$1" notes_file="/home/$context/notes.txt"
    [ -f "$notes_file" ] && cat "$notes_file"
}

get_resource_usage_line() {
    local context="$1" usage_file="/home/$context/resource_usage.txt"
    [ -f "$usage_file" ] || return 0
    tail -n 1 "$usage_file"
}

get_quota_for_user() {
    local plain="$1"
    [ -f "$QUOTA_REPORT_PATH" ] || return 0
    jq -c --arg u "$plain" '.users[]? | select(.username == $u)' "$QUOTA_REPORT_PATH" 2>/dev/null
}

require_command jq

# print_users queries users+plans (plus a separate per-user domain count,
# since joining "domains" directly would multiply the user row per domain),
# then augments each row with the live/on-disk info documented above.
print_users() {
    local rows
    rows=$(timeout 10 mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -e "
        SELECT
            users.username,
            users.email,
            IF(users.owner IS NULL OR users.owner = '', 'root', users.owner) AS owner,
            users.server,
            users.registered_date,
            users.twofa_enabled,
            plans.name,
            plans.cpu,
            plans.ram,
            (SELECT COUNT(*) FROM domains d WHERE d.user_id = users.id) AS domains_count
        FROM users
        INNER JOIN plans ON users.plan_id = plans.id;
    " 2>/dev/null | tail -n +2)

    if [ -z "$rows" ]; then
        if [ "$json_output" = true ]; then
            echo '{"data": [], "metadata": {"result": "ok"}}'
        else
            echo "No users."
        fi
        return 0
    fi

    build_online_users

    local server_ip
    server_ip=$(hostname -I 2>/dev/null | awk '{print $1}')

    local -a json_objects=()
    local table_header="USERNAME\tEMAIL\tSTATUS\tONLINE\t2FA\tIP ADDRESS\tPLAN\tDOMAINS\tRAM (used/allocated)\tCPU (used/allocated)\tDISK (used/allocated)\tINODES (used/allocated)\tNOTES"
    local -a table_rows=()

    while IFS=$'\t' read -r username email owner context registered_date twofa plan_name cpu ram domains_count; do
        [ -z "$username" ] && continue

        local plain suspended online twofa_label ip_dedicated ip_label ip_kind uid
        plain=$(strip_suspended_prefix "$username")
        suspended=false
        [[ "$username" == *"SUSPENDED_"* ]] && suspended=true

        uid=$(stat -c '%u' "/home/$context" 2>/dev/null || echo "null")

        online=false
        [ -n "${ONLINE_USERS[$plain]:-}" ] && online=true

        twofa_label="Off"
        [ "$twofa" = "1" ] && twofa_label="On"

        ip_dedicated=$(get_dedicated_ip "$plain")
        if [ -n "$ip_dedicated" ]; then
            ip_kind="dedicated"
            ip_label="$ip_dedicated (dedicated)"
        else
            ip_kind="shared"
            ip_label="$server_ip (shared)"
        fi

        local notes
        notes=$(get_user_notes "$context")

        # Allocated RAM/CPU come from the user's plan (same columns the
        # list page's "Allocated Memory"/"Allocated CPU" columns render,
        # users_list.html) -- "0"/"0g" is that plan's convention for
        # unlimited.
        local ram_alloc cpu_alloc
        if [ "$ram" = "0g" ]; then
            ram_alloc="Unlimited"
        else
            ram_alloc="${ram%g} GB"
        fi
        if [ "$cpu" = "0" ]; then
            cpu_alloc="Unlimited"
        else
            cpu_alloc="$cpu Core"
        fi

        local ram_used="N/A" cpu_used="N/A"
        local mem_used_bytes=0 mem_total_bytes=0 mem_pct=0 cpu_pct=0
        if [ "$suspended" = false ]; then
            local usage_line
            usage_line=$(get_resource_usage_line "$context")
            if [ -n "$usage_line" ] && echo "$usage_line" | jq -e . >/dev/null 2>&1; then
                ram_used=$(echo "$usage_line" | jq -r '.memory.used.human // "N/A"')
                mem_pct=$(echo "$usage_line" | jq -r '.memory.usage_pct // 0')
                cpu_used=$(echo "$usage_line" | jq -r '.cpu.usage.human // "N/A"')
                cpu_pct=$(echo "$usage_line" | jq -r '.cpu.usage.pct // 0')
                mem_used_bytes=$(echo "$usage_line" | jq -r '.memory.used.bytes // 0')
                mem_total_bytes=$(echo "$usage_line" | jq -r '.memory.total.bytes // 0')
            fi
        fi

        local disk_used="0GB" disk_hard="unlimited" inodes_used=0 inodes_hard="unlimited"
        local disk_used_kb=0 disk_hard_kb=0 inodes_used_n=0 inodes_hard_n=0
        local quota_json
        quota_json=$(get_quota_for_user "$plain")
        if [ -n "$quota_json" ]; then
            disk_used_kb=$(echo "$quota_json" | jq -r '.disk_used // 0')
            disk_hard_kb=$(echo "$quota_json" | jq -r '.disk_hard // 0')
            inodes_used_n=$(echo "$quota_json" | jq -r '.inodes_used // 0')
            inodes_hard_n=$(echo "$quota_json" | jq -r '.inodes_hard // 0')
            disk_used=$(humangb "$disk_used_kb")
            [ "$disk_hard_kb" != "0" ] && disk_hard=$(humangb "$disk_hard_kb")
            inodes_used=$inodes_used_n
            [ "$inodes_hard_n" != "0" ] && inodes_hard=$inodes_hard_n
        fi

        if [ "$json_output" = true ]; then
            json_objects+=("$(jq -n \
                --arg id "$uid" \
                --arg username "$username" \
                --arg email "$email" \
                --arg owner "$owner" \
                --arg context "$context" \
                --arg registered_date "$registered_date" \
                --arg locale_code "EN_us" \
                --argjson suspended "$suspended" \
                --argjson online "$online" \
                --argjson twofa_enabled "$([ "$twofa" = "1" ] && echo true || echo false)" \
                --arg ip_kind "$ip_kind" \
                --arg ip_address "${ip_dedicated:-$server_ip}" \
                --arg plan "$plan_name" \
                --argjson domains "${domains_count:-0}" \
                --arg ram_used "$ram_used" \
                --arg ram_allocated "$ram_alloc" \
                --argjson ram_usage_pct "${mem_pct:-0}" \
                --argjson ram_used_bytes "${mem_used_bytes:-0}" \
                --argjson ram_total_bytes "${mem_total_bytes:-0}" \
                --arg cpu_used "$cpu_used" \
                --arg cpu_allocated "$cpu_alloc" \
                --argjson cpu_usage_pct "${cpu_pct:-0}" \
                --arg disk_used "$disk_used" \
                --arg disk_allocated "$disk_hard" \
                --argjson disk_used_kb "${disk_used_kb:-0}" \
                --argjson disk_hard_kb "${disk_hard_kb:-0}" \
                --argjson inodes_used "${inodes_used:-0}" \
                --arg inodes_allocated "$inodes_hard" \
                --arg notes "$notes" \
                '{
                    id: (if $id == "null" then null else ($id | tonumber) end),
                    username: $username,
                    context: $context,
                    owner: $owner,
                    package: { name: $plan, owner: $owner },
                    email: $email,
                    locale_code: $locale_code,
                    registered_date: $registered_date,
                    suspended: $suspended,
                    online: $online,
                    twofa_enabled: $twofa_enabled,
                    ip: { kind: $ip_kind, address: $ip_address },
                    domains: $domains,
                    usage: {
                        ram: { used: $ram_used, allocated: $ram_allocated, usage_pct: $ram_usage_pct, used_bytes: $ram_used_bytes, total_bytes: $ram_total_bytes },
                        cpu: { used: $cpu_used, allocated: $cpu_allocated, usage_pct: $cpu_usage_pct },
                        disk: { used: $disk_used, allocated: $disk_allocated, used_kb: $disk_used_kb, hard_kb: $disk_hard_kb },
                        inodes: { used: $inodes_used, allocated: $inodes_allocated }
                    },
                    notes: $notes
                }'
            )")
        else
            local status_label="Active"
            [ "$suspended" = true ] && status_label="Suspended"
            local online_label="No"
            [ "$online" = true ] && online_label="Yes"
            local notes_display="${notes//$'\n'/ }"
            table_rows+=("$plain\t$email\t$status_label\t$online_label\t$twofa_label\t$ip_label\t$plan_name\t${domains_count:-0}\t$ram_used/$ram_alloc\t$cpu_used/$cpu_alloc\t$disk_used/$disk_hard\t$inodes_used/$inodes_hard\t$notes_display")
        fi
    done <<< "$rows"

    if [ "$json_output" = true ]; then
        printf '%s\n' "${json_objects[@]}" | jq -s '{data: ., metadata: {result: "ok"}}'
    else
        {
            printf '%b\n' "$table_header"
            printf '%b\n' "${table_rows[@]}"
        } | column -t -s $'\t'
    fi
}

print_users
