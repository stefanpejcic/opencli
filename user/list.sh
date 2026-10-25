#!/bin/bash
################################################################################
# Script Name: user/list.sh
# Description: Display all users: id, username, email, plan, registered date,
#              online status, 2FA, IP, domain count, resource usage, notes.
# Usage: opencli user-list [--json] [--total] [--quota]
# Docs: https://docs.openpanel.com
# Author: Stefan Pejcic
# Created: 16.10.2023
# Last Modified: 08.10.2026
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
    echo "Usage: opencli user-list [--json] [--total] [--quota]"
    exit 1
}

json_output=false
total_users=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --quota)
            opencli user-quota
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

# mirrors users.go's stripSuspendedPrefix -- cuts a "SUSPENDED_<id>_<username>" name down to "<username>" at the last underscore, leaves a normal username unchanged
strip_suspended_prefix() {
    local u="$1"
    if [[ "$u" == *"SUSPENDED_"* ]]; then
        echo "${u##*_}"
    else
        echo "$u"
    fi
}

# scans redis (via openpanel_redis, same as lib/redis.sh) for live login sessions and fills ONLINE_USERS with every username that owns at least one
declare -A ONLINE_USERS
# one redis call collects every session's username, a podman exec per session took ~130ms each
readonly ONLINE_USERS_LUA='local c, out = "0", {}
repeat
    local r = redis.call("SCAN", c, "MATCH", ARGV[1], "COUNT", 1000)
    c = r[1]
    for _, k in ipairs(r[2]) do
        if redis.call("TYPE", k)["ok"] == "hash" then
            local u = redis.call("HGET", k, "username")
            if u then out[#out + 1] = u end
        end
    end
until c == "0"
return out'
build_online_users() {
    local uname
    while IFS= read -r uname; do
        [ -n "$uname" ] && ONLINE_USERS["$uname"]=1
    done < <(redis_cli EVAL "$ONLINE_USERS_LUA" 0 "session:*" 2>/dev/null)
}

# converts a quota_report.json KB figure to the same "N.NNGB" string users.go's humanGB() renders (KB / 1024000), in bash so a long user list doesn't start awk twice per user
humangb() {
    local v="${1:-0}" h
    v="${v%%.*}"
    [[ "$v" =~ ^[0-9]+$ ]] || v=0
    # an exact .xx5 depends on how the double rounds, awk keeps those identical to before
    if (( v * 200 % 2048000 == 1024000 )); then
        LC_ALL=C awk -v v="$v" 'BEGIN { printf "%.2fGB", v/1024000 }'
        return
    fi
    h=$(( (v * 200 + 1024000) / 2048000 ))
    printf '%d.%02dGB' $(( h / 100 )) $(( h % 100 ))
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

# the whole quota report in one jq run, as "disk_used disk_hard inodes_used inodes_hard" per username, instead of five jq runs per user
declare -A QUOTA_ROWS
load_quota_report() {
    [ -f "$QUOTA_REPORT_PATH" ] || return 0
    local u rest
    while IFS=$'\t' read -r u rest; do
        [ -n "$u" ] && [ -z "${QUOTA_ROWS[$u]:-}" ] && QUOTA_ROWS[$u]="$rest"
    done < <(jq -r '.users[]? | [.username, (.disk_used // 0), (.disk_hard // 0), (.inodes_used // 0), (.inodes_hard // 0)] | @tsv' "$QUOTA_REPORT_PATH" 2>/dev/null)
}

require_command jq

# colors only when printing to a terminal, so piped output stays plain
use_color=false
[ -t 1 ] && [ -z "${NO_COLOR:-}" ] && use_color=true
RED=$'\033[31m'
ORANGE=$'\033[38;5;208m'
GREEN=$'\033[32m'
NC=$'\033[0m'

# red at 90%+ of the limit, orange at 80%+
color_by_pct() {
    local text="$1" pct="${2%%.*}"
    [[ "$pct" =~ ^[0-9]+$ ]] || pct=0
    if [ "$use_color" = true ]; then
        if [ "$pct" -ge 90 ]; then
            text="${RED}${text}${NC}"
        elif [ "$pct" -ge 80 ]; then
            text="${ORANGE}${text}${NC}"
        fi
    fi
    printf '%s' "$text"
}

# like column -t but ignores color codes when measuring width, and keeps empty cells
print_table() {
    shopt -s extglob
    local -a widths=() cells=()
    local row s cell plain i last
    split_row() {
        s="$1"; cells=()
        while [[ "$s" == *$'\t'* ]]; do
            cells+=("${s%%$'\t'*}")
            s="${s#*$'\t'}"
        done
        cells+=("$s")
    }
    for row in "$@"; do
        split_row "$row"
        for i in "${!cells[@]}"; do
            plain="${cells[$i]//$'\033'\[*([0-9;])m/}"
            (( ${#plain} > ${widths[$i]:-0} )) && widths[$i]=${#plain}
        done
    done
    for row in "$@"; do
        split_row "$row"
        last=$(( ${#cells[@]} - 1 ))
        for i in "${!cells[@]}"; do
            cell="${cells[$i]}"
            if [ "$i" -eq "$last" ]; then
                printf '%s\n' "$cell"
            else
                plain="${cell//$'\033'\[*([0-9;])m/}"
                printf '%s%*s' "$cell" $(( widths[$i] - ${#plain} + 2 )) ""
            fi
        done
    done
}

# queries users+plans (plus a separate per-user domain count, since joining "domains" directly would multiply the user row per domain), then augments each row with the live/on-disk info documented above
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
    load_quota_report

    local server_ip
    server_ip=$(hostname -I 2>/dev/null | awk '{print $1}')

    local -a json_objects=()
    local table_header=$'USERNAME\tEMAIL\tSTATUS\tONLINE\t2FA\tIP ADDRESS\tPLAN\tDOMAINS\tRAM (used/allocated)\tCPU (used/allocated)\tDISK (used/allocated)\tINODES (used/allocated)\tNOTES'
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

        # allocated RAM/CPU come from the user's plan (same columns users_list.html renders) -- "0"/"0g" is that plan's convention for unlimited
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
            if [ -n "$usage_line" ]; then
                local usage_tsv
                # one jq run for all six fields, nothing comes out when the line isn't valid json
                usage_tsv=$(jq -r 'select(.) | [(.memory.used.human // "N/A"), (.memory.usage_pct // 0), (.cpu.usage.human // "N/A"), (.cpu.usage.pct // 0), (.memory.used.bytes // 0), (.memory.total.bytes // 0)] | @tsv' <<< "$usage_line" 2>/dev/null)
                [ -n "$usage_tsv" ] && IFS=$'\t' read -r ram_used mem_pct cpu_used cpu_pct mem_used_bytes mem_total_bytes <<< "$usage_tsv"
            fi
        fi

        local disk_used="0GB" disk_hard="unlimited" inodes_used=0 inodes_hard="unlimited"
        local disk_used_kb=0 disk_hard_kb=0 inodes_used_n=0 inodes_hard_n=0
        if [ -n "${QUOTA_ROWS[$plain]:-}" ]; then
            IFS=$'\t' read -r disk_used_kb disk_hard_kb inodes_used_n inodes_hard_n <<< "${QUOTA_ROWS[$plain]}"
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
            if [ "$suspended" = true ]; then
                status_label="Suspended"
                [ "$use_color" = true ] && status_label="${RED}Suspended${NC}"
            fi
            local online_label="No"
            if [ "$online" = true ]; then
                online_label="Yes"
                [ "$use_color" = true ] && online_label="${GREEN}●${NC} Yes"
            fi
            local notes_display="${notes//$'\n'/ }"

            # unlimited allocations stay at 0% so they never get colored
            local ram_pct=0 cpu_alloc_pct=0 disk_pct=0 inodes_pct=0
            [ "$ram_alloc" != "Unlimited" ] && ram_pct="$mem_pct"
            [ "$cpu_alloc" != "Unlimited" ] && cpu_alloc_pct="$cpu_pct"
            [ "${disk_hard_kb%%.*}" -gt 0 ] 2>/dev/null && disk_pct=$(( ${disk_used_kb%%.*} * 100 / ${disk_hard_kb%%.*} ))
            [ "${inodes_hard_n%%.*}" -gt 0 ] 2>/dev/null && inodes_pct=$(( ${inodes_used_n%%.*} * 100 / ${inodes_hard_n%%.*} ))

            local ram_cell cpu_cell disk_cell inodes_cell
            ram_cell=$(color_by_pct "$ram_used/$ram_alloc" "$ram_pct")
            cpu_cell=$(color_by_pct "$cpu_used/$cpu_alloc" "$cpu_alloc_pct")
            disk_cell=$(color_by_pct "$disk_used/$disk_hard" "$disk_pct")
            inodes_cell=$(color_by_pct "$inodes_used/$inodes_hard" "$inodes_pct")

            table_rows+=("$plain"$'\t'"$email"$'\t'"$status_label"$'\t'"$online_label"$'\t'"$twofa_label"$'\t'"$ip_label"$'\t'"$plan_name"$'\t'"${domains_count:-0}"$'\t'"$ram_cell"$'\t'"$cpu_cell"$'\t'"$disk_cell"$'\t'"$inodes_cell"$'\t'"$notes_display")
        fi
    done <<< "$rows"

    if [ "$json_output" = true ]; then
        printf '%s\n' "${json_objects[@]}" | jq -s '{data: ., metadata: {result: "ok"}}'
    else
        print_table "$table_header" "${table_rows[@]}"
    fi
}

print_users
