#!/bin/bash
################################################################################
# Script Name: user/report.sh
# Description: Prints a health report for a user: what is working and what needs attention.
# Usage: opencli user-report <USERNAME|--all> [--section <name,...>] [--json]
# Author: Stefan Pejcic
# Created: 02.10.2026
# Last Modified: 02.10.2026
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
source /usr/local/opencli/db.sh
# shellcheck disable=SC1091
source /usr/local/opencli/lib/requirement.sh
# shellcheck disable=SC1091
source /usr/local/opencli/lib/podman.sh

require_command jq

readonly PANEL_CONFIG="/etc/openpanel/openpanel/conf/openpanel.config"
readonly USERS_CORE_DIR="/etc/openpanel/openpanel/core/users"
readonly QUOTA_REPORT_FILE="/etc/openpanel/openpanel/quota_report.json"

# report order, each name maps to a section_<name> function
readonly ALL_JSON_FILE="/etc/openpanel/openpanel/user_report_status.json"
readonly ALL_SECTIONS=(info account containers processes files backups domains dns ssl waf webserver php websites databases cache emails crons stats security activity)

usage() {
    cat << EOF
Usage: opencli user-report <USERNAME|--all> [--section <name,...>] [--json]

Arguments:
    USERNAME              Report for a single user.
    --all                 Report for every user on the server.
    --section <list>      Only run these sections, comma separated.
    --json                Save the report as JSON instead of printing it:
                          a user to /home/<context>/user_report_status.json,
                          --all as one line per user to $ALL_JSON_FILE.

Sections:
    ${ALL_SECTIONS[*]}

Examples:
    opencli user-report stefan
    opencli user-report stefan --section info,files
    opencli user-report --all
    opencli user-report stefan --json
EOF
    exit 1
}


# ======================================================================
# Arguments

target=""
selected_sections=()
JSON_MODE=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --all) target="--all"; shift ;;
        --json) JSON_MODE=1; shift ;;
        --section|--sections)
            [[ -n "${2-}" ]] || usage
            IFS=',' read -ra selected_sections <<< "$2"
            shift 2
            ;;
        --section=*|--sections=*)
            IFS=',' read -ra selected_sections <<< "${1#*=}"
            shift
            ;;
        -h|--help) usage ;;
        -*) echo "Unknown option: $1"; usage ;;
        *)
            [[ -z "$target" ]] || usage
            target="$1"; shift
            ;;
    esac
done

[[ -n "$target" ]] || usage

for s in "${selected_sections[@]}"; do
    [[ " ${ALL_SECTIONS[*]} " == *" $s "* ]] || { echo "Unknown section: $s"; usage; }
done


# ======================================================================
# Output helpers

# json keeps each section's text, so no colors in it
if [[ -t 1 ]] && (( ! JSON_MODE )); then
    C_RED=$'\e[31m' C_YELLOW=$'\e[33m' C_GREEN=$'\e[32m' C_CYAN=$'\e[36m' C_BOLD=$'\e[1m' C_DIM=$'\e[2m' C_RESET=$'\e[0m'
else
    C_RED="" C_YELLOW="" C_GREEN="" C_CYAN="" C_BOLD="" C_DIM="" C_RESET=""
fi

# one "severity<TAB>section<TAB>message" per problem, reset for every user
ISSUES=()
CUR_SECTION=""
any_error=0

section() {
    CUR_SECTION="$1"
    printf '\n%s== %s ==%s\n' "$C_BOLD" "$1" "$C_RESET"
}

# sub-heading inside a section, issues get tagged "SECTION > SUB"
subsection() {
    CUR_SECTION="${CUR_SECTION%% > *} > $1"
    printf '\n  %s-- %s --%s\n' "$C_BOLD" "$1" "$C_RESET"
}

# set per section in json mode, every row also goes there as "group<TAB>label<TAB>value"
ROW_FILE=""
json_row() {
    [[ -n "$ROW_FILE" ]] || return 0
    local group=""
    [[ "$CUR_SECTION" == *" > "* ]] && group="${CUR_SECTION#* > }"
    printf '%s\t%s\t%s\n' "$group" "$1" "${2//$'\n'/ }" >> "$ROW_FILE"
}

row() {
    printf '  %-24s %s\n' "${1:+$1:}" "$2"
    json_row "$1" "$2"
}

# long comma list wrapped under its label
row_list() {
    local label="$1"; shift
    local text; text=$(printf '%s, ' "$@"); text="${text%, }"
    [[ -n "$text" ]] || text="-"
    json_row "$label" "$text"
    fold -s -w 52 <<< "$text" | sed -e 's/ *$//' -e "1s/^/  $(printf '%-24s' "$label:") /" -e '2,$s/^/                           /'
}

ok() {
    printf '  %s[OK]%s    %s\n' "$C_GREEN" "$C_RESET" "$*"
}

# set per section since sections run in parallel, the parent reads the issues back from it
ISSUE_FILE=""
_issue() {
    local sev="$1" color="$2" tag="$3"; shift 3
    ISSUES+=("${sev}"$'\t'"${CUR_SECTION}"$'\t'"$*")
    [[ -n "$ISSUE_FILE" ]] && printf '%s\t%s\t%s\n' "$sev" "$CUR_SECTION" "$*" >> "$ISSUE_FILE"
    printf '  %s%-7s%s %s\n' "$color" "$tag" "$C_RESET" "$*"
}

err()  { _issue error "$C_RED" "[ERROR]" "$@"; }
warn() { _issue warning "$C_YELLOW" "[WARN]" "$@"; }
info() { _issue info "$C_CYAN" "[INFO]" "$@"; }

# openpanel.config read once, first value of a key wins like grep -m1 did
declare -A PCONF=()
load_panel_config() {
    local k v
    while IFS='=' read -r k v; do
        [[ "$k" =~ ^[A-Za-z0-9_]+$ ]] || continue
        v="${v%\"}"; v="${v#\"}"
        [[ -n "${PCONF[$k]+x}" ]] || PCONF[$k]="$v"
    done < "$PANEL_CONFIG" 2>/dev/null
}

# config value from openpanel.config, quotes stripped
panel_config() {
    local v="${PCONF[$1]-}"
    echo "${v:-${2-}}"
}

sql() {
    timeout 10 mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -B -e "$1" 2>/dev/null
}

# limit 0 or empty means unlimited
limit_label() {
    [[ -z "$1" || "$1" == "0" ]] && echo "unlimited" || echo "$1"
}

# KB from repquota to a readable size
human_kb() {
    LC_ALL=C awk -v k="${1:-0}" 'BEGIN { if (k >= 1048576) printf "%.2f GB\n", k / 1048576; else if (k >= 1024) printf "%.1f MB\n", k / 1024; else printf "%d KB\n", k }'
}

# ======================================================================
# Shared data
#
# Anything more than one section (or more than one user with --all) needs is fetched once here.
# Loaders run in the main shell so the result sticks, call them before any $(...) that reads it.

# background jobs: gasync/uasync <name> [args] start job_<name> into a file at the beginning,
# gfetch/ufetch <name> [args] wait for that file, or run the job right away when nothing started it
# g is shared by every user, u is per user
ASYNC_DIR=$(mktemp -d /tmp/user-report-async.XXXXXX)
# jobs still running at exit (unknown user, unused lookups) are stopped before their folder goes
trap 'kill $(jobs -p) 2>/dev/null; wait 2>/dev/null; rm -rf "$ASYNC_DIR"' EXIT
declare -A ASYNC_STARTED=()
_async() {
    local k="$*"; k="${k//[^A-Za-z0-9_.-]/_}"
    [[ -n "${ASYNC_STARTED[$k]-}" ]] && return
    ASYNC_STARTED[$k]=1
    ( "job_$2" "${@:3}" > "$ASYNC_DIR/$k.part" 2>/dev/null; mv -f "$ASYNC_DIR/$k.part" "$ASYNC_DIR/$k" 2>/dev/null ) &
}
_fetch() {
    local k="$*"; k="${k//[^A-Za-z0-9_.-]/_}"
    if [[ -n "${ASYNC_STARTED[$k]-}" ]]; then
        local n=0
        until [[ -e "$ASYNC_DIR/$k" ]] || (( n++ > 2400 )); do sleep 0.05; done
        cat "$ASYNC_DIR/$k" 2>/dev/null
    else
        "job_$2" "${@:3}" 2>/dev/null
    fi
}
gasync() { _async g "$@"; }
gfetch() { _fetch g "$@"; }
uasync() { _async "u$U_ID" "$@"; }
ufetch() { _fetch "u$U_ID" "$@"; }

SERVER_IP=""
server_ip() {
    if [[ -z "$SERVER_IP" ]]; then
        SERVER_IP=$(curl -s --max-time 2 -4 https://ip.openpanel.com 2>/dev/null)
        [[ "$SERVER_IP" =~ ^[0-9]+(\.[0-9]+){3}$ ]] || SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
    fi
    echo "$SERVER_IP"
}

# rootful containers: name, state, status, ports
ROOT_PS="" ROOT_PS_LOADED=0
job_root_ps() {
    timeout 10 podman ps -a --format '{{.Names}}\t{{.State}}\t{{.Status}}\t{{.Ports}}'
}
need_root_ps() {
    (( ROOT_PS_LOADED )) && return
    ROOT_PS=$(gfetch root_ps)
    ROOT_PS_LOADED=1
}

# state of a rootful container (running, exited, created), empty when it doesn't exist
root_state() {
    awk -F'\t' -v n="$1" '$1 == n { print $2; exit }' <<< "$ROOT_PS"
}

# panel version, urls and os, the same for every user, as key=value lines
declare -A SRV=()
job_server_facts() {
    echo "version=$(timeout 5 bash /usr/local/opencli/version.sh 2>/dev/null)"
    echo "opencli=$(grep -oP '^readonly OPENCLI_VERSION="\K[^"]+' /usr/local/opencli/opencli 2>/dev/null)"
    grep -qE '^key=enterprise' "$PANEL_CONFIG" 2>/dev/null && echo "license=Enterprise" || echo "license=Community"
    echo "panel_url=$(timeout 5 opencli domain 2>/dev/null | head -n1)"
    echo "hostname=$(hostname -f 2>/dev/null || hostname)"
    echo "os=$(. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME")"
    if systemctl is-active --quiet admin 2>/dev/null; then
        echo "admin=running"
        echo "admin_url=$(timeout 5 opencli admin 2>/dev/null | sed 's/\x1b\[[0-9;]*m//g' | grep -oP 'available on: \K\S+' | head -n1)"
    elif [[ -f /root/openadmin_is_disabled ]]; then
        echo "admin=disabled"
    else
        echo "admin=not running"
    fi
}
need_server_facts() {
    [[ -n "${SRV[loaded]-}" ]] && return
    local k v
    while IFS='=' read -r k v; do [[ -n "$k" ]] && SRV[$k]="$v"; done < <(gfetch server_facts)
    SRV[loaded]=1
}

# quota_report.json as "user<TAB>disk_used<TAB>disk_hard<TAB>inodes_used<TAB>inodes_hard"
QUOTA_TSV="" QUOTA_TS="" QUOTA_LOADED=0
# first line is the report time
job_quota() {
    [[ -f "$QUOTA_REPORT_FILE" ]] || return
    jq -r '(.timestamp // ""), (.users[]? | [.username, .disk_used, .disk_hard, .inodes_used, .inodes_hard] | @tsv)' "$QUOTA_REPORT_FILE"
}
need_quota() {
    (( QUOTA_LOADED )) && return
    local out; out=$(gfetch quota)
    QUOTA_TS=$(head -n1 <<< "$out")
    QUOTA_TSV=$(tail -n +2 <<< "$out")
    QUOTA_LOADED=1
}

# every mailbox and alias on the server, and hourly limit rejects from the mailserver log, filtered per user later
EMAIL_LIST="" ALIAS_LIST="" MAIL_REJECTS="" EMAIL_LOADED=0
job_email_list() { timeout 20 opencli email-setup email list; }
job_alias_list() { timeout 20 opencli email-setup alias list; }
job_mail_rejects() { timeout 60 podman logs --since 24h openadmin_mailserver 2>&1 | grep 'emails per hour' | grep -oP 'from=<\K[^>]+'; }
job_webmail() { timeout 10 opencli email-webmail | head -n1; }
need_email_data() {
    (( EMAIL_LOADED )) && return
    EMAIL_LIST=$(gfetch email_list)
    ALIAS_LIST=$(gfetch alias_list)
    MAIL_REJECTS=$(gfetch mail_rejects)
    EMAIL_LOADED=1
}

# nameservers from the panel settings as "ns<TAB>ip", ip empty when it doesn't resolve
job_ns() {
    local n v
    for n in ns1 ns2 ns3 ns4; do
        v=$(panel_config "$n")
        [[ -n "$v" ]] && printf '%s\t%s\n' "$v" "$(pdig "$v" A | head -n1)"
    done
}
NS_CONF=() NS_BROKEN=() NS_LOADED=0
need_nameservers() {
    (( NS_LOADED )) && return
    local n ip
    while IFS=$'\t' read -r n ip; do
        [[ -n "$n" ]] || continue
        NS_CONF+=("$n")
        [[ -n "$ip" ]] || NS_BROKEN+=("$n")
    done < <(gfetch ns)
    NS_LOADED=1
}

# panel database, everyone's rows with --all, just this user's otherwise
DB_USERS="" DB_PLANS="" DB_DOMAINS="" DB_SITES="" DB_PASSKEYS="" DB_MCP="" DB_ALL=0
db_prefetch() {
    local where="$1"
    DB_PLANS=$(sql "SELECT p.id, p.name, p.websites_limit, p.domains_limit, p.db_limit, p.email_limit, p.ftp_limit, p.disk_limit, p.inodes_limit,
                           IFNULL(p.cpu, ''), IFNULL(p.ram, ''), p.max_email_quota, p.max_hourly_email, IFNULL(up.name, ''), IFNULL(p.upsell_url, '')
                    FROM plans p LEFT JOIN plans up ON up.id = p.upsell_plan_id")
    # installs without the upsell columns
    [[ -n "$DB_PLANS" ]] || DB_PLANS=$(sql "SELECT id, name, websites_limit, domains_limit, db_limit, email_limit, ftp_limit, disk_limit, inodes_limit,
                                                   IFNULL(cpu, ''), IFNULL(ram, ''), max_email_quota, max_hourly_email, '', '' FROM plans")
    DB_DOMAINS=$(sql "SELECT user_id, domain_url, docroot, IFNULL(php_version, '') FROM domains $where ORDER BY domain_url")
    DB_SITES=$(sql "SELECT d.user_id, s.site_name, s.type, IFNULL(s.version, ''), IFNULL(s.container, ''), d.domain_url
                    FROM sites s JOIN domains d ON d.domain_id = s.domain_id ${where//user_id/d.user_id} ORDER BY s.site_name")
    DB_PASSKEYS=$(sql "SELECT user_id, COUNT(*), IFNULL(MAX(last_used_at), '') FROM user_passkeys $where GROUP BY user_id")
    DB_MCP=$(sql "SELECT user_id, name, token_prefix, IF(read_only, 'read-only', 'read-write'), IFNULL(last_used_at, 'never'), IFNULL(expires_at, '')
                  FROM mcp_tokens $where ORDER BY created_at")
}

db_prefetch_all() {
    DB_USERS=$(sql "SELECT u.id, u.username, u.email, IFNULL(NULLIF(u.owner, ''), 'root'), u.server, u.registered_date, IFNULL(u.twofa_enabled, 0), u.plan_id, IFNULL(p.name, ''), IFNULL(p.feature_set, '')
                    FROM users u LEFT JOIN plans p ON p.id = u.plan_id ORDER BY u.username")
    db_prefetch ""
    DB_ALL=1
}

# rows of a prefetched table for the current user, without the user_id column
user_rows() {
    awk -F'\t' -v u="$U_ID" '$1 == u' <<< "$1" | cut -f2-
}

# per user data, reset for every user, the need_* loaders only run when a section asks
U_DOMAIN_ROWS="" U_DOMAINS="" U_SITE_ROWS=""
declare -A PLAN=() UENV=()
U_ENV_LOADED=0 U_SERVICES="" U_SERVICES_LOADED=0 U_PS="" U_PS_OK=0 U_PS_LOADED=0 U_RUNNING="" U_DU="" U_DU_LOADED=0
user_data_reset() {
    (( DB_ALL )) || db_prefetch "WHERE user_id = '$(mysql_escape "$U_ID")'"
    U_DOMAIN_ROWS=$(user_rows "$DB_DOMAINS")
    U_DOMAINS=$(cut -f1 <<< "$U_DOMAIN_ROWS" | grep .)
    U_SITE_ROWS=$(user_rows "$DB_SITES")
    PLAN=()
    local row; row=$(awk -F'\t' -v id="$U_PLAN_ID" '$1 == id' <<< "$DB_PLANS")
    if [[ -n "$row" ]]; then
        IFS=$'\t' read -r PLAN[id] PLAN[name] PLAN[websites] PLAN[domains] PLAN[dbs] PLAN[emails] PLAN[ftp] PLAN[disk] PLAN[inodes] \
            PLAN[cpu] PLAN[ram] PLAN[mailquota] PLAN[hourly] PLAN[upsell] PLAN[upsell_url] <<< "$row"
    fi
    UENV=() U_ENV_LOADED=0 U_SERVICES="" U_SERVICES_LOADED=0 U_PS="" U_PS_OK=0 U_PS_LOADED=0 U_RUNNING="" U_DU="" U_DU_LOADED=0
}

need_env() {
    (( U_ENV_LOADED )) && return
    local k v
    while IFS='=' read -r k v; do
        [[ "$k" =~ ^[A-Za-z0-9_]+$ ]] || continue
        v="${v%"${v##*[![:space:]]}"}"
        v="${v#[\"\']}"; v="${v%[\"\']}"
        [[ -n "${UENV[$k]+x}" ]] || UENV[$k]="$v"
    done < "/home/$U_CONTEXT/.env" 2>/dev/null
    U_ENV_LOADED=1
}

need_services() {
    (( U_SERVICES_LOADED )) && return
    U_SERVICES=$(compose_services "/home/$U_CONTEXT/docker-compose.yml")
    U_SERVICES_LOADED=1
}

# the user's containers: name, state, status, id
# last line says whether podman answered
job_ps() {
    upodman ps -a --format '{{.Names}}\t{{.State}}\t{{.Status}}\t{{.ID}}' && echo "__PS_OK"
}
need_ps() {
    (( U_PS_LOADED )) && return
    U_PS=$(ufetch ps)
    if grep -qx '__PS_OK' <<< "$U_PS"; then U_PS_OK=1; U_PS=$(grep -vx '__PS_OK' <<< "$U_PS"); fi
    U_RUNNING=$(awk -F'\t' '$2 == "running" { print $1 }' <<< "$U_PS")
    U_PS_LOADED=1
}

# one du for the volumes and the folders in /var/www/html, "KB<TAB>path"
job_du() {
    local vols="/home/$U_CONTEXT/docker-data/volumes"
    [[ -d "$vols" ]] && timeout 60 du -k --max-depth=3 "$vols"
}
need_du() {
    (( U_DU_LOADED )) && return
    U_DU=$(ufetch du)
    U_DU_LOADED=1
}

# KB of a path from the shared du, empty when du didn't reach that deep
du_of() {
    awk -F'\t' -v p="${1%/}" '$2 == p { print $1; exit }' <<< "$U_DU"
}


# ======================================================================
# User lookup

# columns: id, username, email, owner, context, registered, twofa, plan_id, plan name, feature set
USER_ROW=()
load_user() {
    local u; u=$(mysql_escape "$1")
    local line
    if (( DB_ALL )); then
        line=$(awk -F'\t' -v u="$1" '$2 == u || ($2 ~ /^SUSPENDED_/ && substr($2, length($2) - length(u)) == "_" u) { print; exit }' <<< "$DB_USERS")
    else
        line=$(sql "SELECT u.id, u.username, u.email, IFNULL(NULLIF(u.owner, ''), 'root'), u.server, u.registered_date, IFNULL(u.twofa_enabled, 0), u.plan_id, IFNULL(p.name, ''), IFNULL(p.feature_set, '')
                    FROM users u LEFT JOIN plans p ON p.id = u.plan_id
                    WHERE u.username = '$u' OR u.username LIKE 'SUSPENDED\_%\_$u' LIMIT 1")
    fi
    [[ -n "$line" ]] || return 1
    IFS=$'\t' read -ra USER_ROW <<< "$line"
    U_ID="${USER_ROW[0]}" U_DBNAME="${USER_ROW[1]}" U_EMAIL="${USER_ROW[2]}" U_OWNER="${USER_ROW[3]}"
    U_CONTEXT="${USER_ROW[4]}" U_REGISTERED="${USER_ROW[5]}" U_TWOFA="${USER_ROW[6]}" U_PLAN_ID="${USER_ROW[7]}"
    U_PLAN="${USER_ROW[8]}" U_FEATURES="${USER_ROW[9]}"
    U_NAME="$U_DBNAME"
    U_SUSPENDED=""
    if [[ "$U_DBNAME" == SUSPENDED_* ]]; then
        U_NAME="${U_DBNAME##*_}"
        U_SUSPENDED=$(sed -E 's/^SUSPENDED_([0-9]{4})([0-9]{2})([0-9]{2})([0-9]{2})([0-9]{2}).*/\1-\2-\3 \4:\5/' <<< "$U_DBNAME")
    fi
}


# ======================================================================
# Sections

section_info() {
    section "INFORMATION"

    row "Username" "$U_NAME"
    if [[ -n "$U_SUSPENDED" ]]; then
        row "Status" "Suspended (since $U_SUSPENDED)"
        warn "Account is suspended, its websites and services are offline."
    else
        row "Status" "Active"
    fi
    row "Owner" "$U_OWNER"
    row "Email" "${U_EMAIL:--}"
    row "Created" "$U_REGISTERED"
    row "Context" "$U_CONTEXT"

    if ! id "$U_CONTEXT" &>/dev/null; then
        err "System user $U_CONTEXT does not exist, the account is broken."
    elif [[ ! -d "/home/$U_CONTEXT" ]]; then
        err "Home directory /home/$U_CONTEXT is missing."
    fi

    local ip ip_type="shared"
    ip=$(jq -r '.ip // empty' "$USERS_CORE_DIR/$U_NAME/ip.json" 2>/dev/null)
    if [[ -n "$ip" ]]; then
        ip_type="dedicated"
    else
        ip=$(server_ip)
    fi
    row "IP address" "${ip:--} ($ip_type)"

    info_server
    info_plan
    info_features
    info_openpanel
    info_containers
    info_resources
}

info_server() {
    subsection "SERVER"
    need_server_facts
    row "Hostname" "${SRV[hostname]}"
    row "OS" "${SRV[os]:-unknown}, kernel $(uname -r)"
    row "Time" "$(date '+%Y-%m-%d %H:%M:%S %Z (UTC%:z)'), up $(uptime -p 2>/dev/null | sed 's/^up //')"

    # flagged when the whole server is under pressure
    local cores load1 load5 load15 cpu_model
    cores=$(nproc 2>/dev/null || echo 1)
    cpu_model=$(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2- | sed 's/^ *//')
    read -r load1 load5 load15 _ < /proc/loadavg
    row "CPU" "$cores cores, $(uname -m)${cpu_model:+, $cpu_model}"
    row "Load" "$load1, $load5, $load15 (1, 5, 15 min)"
    if LC_ALL=C awk -v l="$load5" -v c="$cores" 'BEGIN { exit !(l >= c * 2) }'; then
        err "Server load ($load5) is over twice the CPU cores ($cores)."
    elif LC_ALL=C awk -v l="$load5" -v c="$cores" 'BEGIN { exit !(l >= c) }'; then
        warn "Server load ($load5) is above the CPU cores ($cores)."
    fi

    local mem_total mem_avail mem_pct
    mem_total=$(awk '/^MemTotal:/ { print $2 }' /proc/meminfo)
    mem_avail=$(awk '/^MemAvailable:/ { print $2 }' /proc/meminfo)
    if [[ -n "$mem_total" && -n "$mem_avail" ]]; then
        mem_pct=$(( (mem_total - mem_avail) * 100 / mem_total ))
        row "RAM" "$(human_kb $((mem_total - mem_avail))) used of $(human_kb "$mem_total") (${mem_pct}%)"
        if (( mem_pct >= 95 )); then
            err "Server RAM is ${mem_pct}% used."
        elif (( mem_pct >= 90 )); then
            warn "Server RAM is ${mem_pct}% used."
        fi
    fi

    local d_total d_used d_avail d_pct
    read -r d_total d_used d_avail d_pct < <(df -Pk /home 2>/dev/null | awk 'NR == 2 { sub("%", "", $5); print $2, $3, $4, $5 }')
    if [[ -n "$d_total" ]]; then
        row "Disk" "$(human_kb "$d_used") used of $(human_kb "$d_total"), $(human_kb "$d_avail") free (${d_pct}%)"
        if (( d_pct >= 95 )); then
            err "Server disk is ${d_pct}% full."
        elif (( d_pct >= 90 )); then
            warn "Server disk is ${d_pct}% full."
        fi
    fi
}

# plan limits, also used by info_resources
L_SITES="" L_DOMAINS="" L_DBS="" L_EMAILS="" L_FTP="" L_DISK="" L_INODES=""
info_plan() {
    subsection "PLAN"
    L_SITES="" L_DOMAINS="" L_DBS="" L_EMAILS="" L_FTP="" L_DISK="" L_INODES=""
    if [[ -z "${PLAN[id]-}" ]]; then
        row "Plan" "${U_PLAN:-none}"
        err "Plan (id ${U_PLAN_ID:-none}) not found, limits can't be applied."
        return
    fi
    L_SITES="${PLAN[websites]}" L_DOMAINS="${PLAN[domains]}" L_DBS="${PLAN[dbs]}" L_EMAILS="${PLAN[emails]}" L_FTP="${PLAN[ftp]}" L_DISK="${PLAN[disk]}" L_INODES="${PLAN[inodes]}"

    row "Plan" "$U_PLAN"
    row "CPU / RAM" "$(limit_label "${PLAN[cpu]}") cores / $(limit_label "${PLAN[ram]}")"
    row "Max mailbox quota" "$(limit_label "${PLAN[mailquota]}")"
    row "Max emails per hour" "$(limit_label "${PLAN[hourly]}")"
}

info_features() {
    subsection "FEATURES"
    # same lookup as LoadUserFeatures in the panel: user's features.txt, else the plan's set, else default.txt
    local features_file features_src
    if [[ -f "/home/$U_CONTEXT/features.txt" ]]; then
        features_file="/home/$U_CONTEXT/features.txt" features_src="user override"
    elif [[ -f "/etc/openpanel/openpanel/features/${U_FEATURES}.txt" ]]; then
        features_file="/etc/openpanel/openpanel/features/${U_FEATURES}.txt" features_src="plan feature set $U_FEATURES"
    else
        features_file="/etc/openpanel/openpanel/features/default.txt" features_src="default"
        warn "Feature set file for '${U_FEATURES}' is missing, the user gets default.txt."
    fi
    local modules=() features=() allowed=() off_server=() m
    IFS=',' read -ra modules <<< "$(panel_config enabled_modules)"
    mapfile -t features < <(sed 's/[[:space:]]//g' "$features_file" 2>/dev/null | grep .)
    for m in "${features[@]}"; do
        if [[ ",$(panel_config enabled_modules)," == *",$m,"* ]]; then
            allowed+=("$m")
        else
            off_server+=("$m")
        fi
    done
    row "Features from" "$features_file ($features_src)"
    if (( ${#allowed[@]} == ${#modules[@]} )); then
        row "User modules" "all ${#modules[@]} modules enabled on the server"
    else
        local missing=()
        for m in "${modules[@]}"; do
            [[ " ${allowed[*]} " == *" $m "* ]] || missing+=("$m")
        done
        row "User modules" "${#allowed[@]} of ${#modules[@]} enabled on the server"
        # whichever list is shorter
        if (( ${#allowed[@]} <= ${#missing[@]} )); then
            row_list "Allowed for user" "${allowed[@]}"
        else
            row_list "Not allowed for user" "${missing[@]}"
        fi
    fi
    (( ${#off_server[@]} )) && row_list "Off on server" "${off_server[@]}"
}

info_openpanel() {
    subsection "OPENPANEL"
    need_server_facts
    row "OpenPanel" "${SRV[version]:-unknown} (${SRV[license]})"
    row "OpenCLI" "${SRV[opencli]:-unknown}"
    [[ -n "${SRV[panel_url]}" ]] && row "Panel domain" "${SRV[panel_url]}"

    # same check as 'opencli admin'
    if [[ "${SRV[admin]}" == running ]]; then
        row "OpenAdmin" "running${SRV[admin_url]:+ on ${SRV[admin_url]}}"
    elif [[ "${SRV[admin]}" == disabled ]]; then
        row "OpenAdmin" "disabled"
    else
        row "OpenAdmin" "not running"
        warn "OpenAdmin is not running but wasn't disabled, start it with 'opencli admin on'."
    fi
}

info_containers() {
    subsection "SYSTEM CONTAINERS"
    # rootful containers that every account depends on
    local containers name status ports
    need_root_ps
    containers=$(cut -f1,3,4 <<< "$ROOT_PS")
    if [[ -z "$containers" ]]; then
        err "No system containers found, podman isn't responding."
        return
    fi
    printf '  %s%-24s %-26s %s%s\n' "$C_DIM" "Container" "Status" "Ports" "$C_RESET"
    while IFS=$'\t' read -r name status ports; do
        # just the published host ports, e.g. "25,110,143"
        ports=$(grep -oE ":[0-9]+(-[0-9]+)?->" <<< "$ports" | sed -E "s/^://; s/->$//" | sort -un | paste -sd, -)
        printf '  %-24s %-26s %s\n' "$name" "$status" "${ports:--}"
        if [[ "$status" != Up* ]]; then
            err "System container $name is not running ($status)."
        elif [[ "$status" == *unhealthy* ]]; then
            warn "System container $name is unhealthy."
        fi
    done <<< "$containers"
}

info_resources() {
    subsection "RESOURCES"
    # counted the same way as the dashboard
    local domains main_domains=0 sub_domains=0 sites dbs="" emails ftp
    domains="$U_DOMAINS"
    if [[ -n "$domains" ]]; then
        read -r main_domains sub_domains < <(awk '{ d[NR] = $1 } END { m = 0; s = 0; for (i in d) { sub_ = 0; for (j in d) if (i != j && substr(d[i], length(d[i]) - length(d[j])) == "." d[j]) { sub_ = 1; break } if (sub_) s++; else m++ } print m, s }' <<< "$domains")
    fi
    sites=$(grep -c . <<< "$U_SITE_ROWS")
    local sock="/home/$U_CONTEXT/sockets/mysqld/mysqld.sock"
    [[ -S "$sock" ]] || dbs=0
    if [[ -S "$sock" ]]; then
        local restricted
        restricted=$(panel_config mysql_restricted_databases "information_schema performance_schema mysql phpmyadmin sys mariadb.sys" | awk '{ for (i = 1; i <= NF; i++) printf "%s'\''%s'\''", (i > 1 ? "," : ""), $i }')
        # --defaults-file, not -extra-file, so /etc/my.cnf's tcp host doesn't send this to the panel db
        dbs=$(timeout 5 mariadb --defaults-file="/home/$U_CONTEXT/my.cnf" --socket="$sock" -N -B -e "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name NOT IN ($restricted)" 2>/dev/null)
    fi
    emails=$(grep -c '^\*' "$USERS_CORE_DIR/$U_NAME/emails.yml" 2>/dev/null)
    ftp=$(grep -c . "/etc/openpanel/ftp/users/$U_CONTEXT/users.list" 2>/dev/null)

    local disk_used="" inodes_used=""
    need_quota
    read -r disk_used inodes_used < <(awk -F'\t' -v u="$U_CONTEXT" '$1 == u { print $2, $4; exit }' <<< "$QUOTA_TSV")

    printf '  %s%-24s %-12s %-12s%s\n' "$C_DIM" "Resource" "Used" "Limit" "$C_RESET"
    local reached=() name used limit
    while IFS='|' read -r name used limit; do
        printf '  %-24s %-12s %-12s\n' "$name" "$used" "$(limit_label "$limit")"
        if [[ "$used" =~ ^[0-9]+$ && "$limit" =~ ^[0-9]+$ ]] && (( limit > 0 && used >= limit )); then
            reached+=("$name")
            warn "$name limit reached ($used of $limit)."
        fi
    done << EOF
Websites|${sites:-0}|$L_SITES
Domains|$main_domains|$L_DOMAINS
Databases|${dbs:-?}|$L_DBS
Email accounts|${emails:-0}|$L_EMAILS
FTP accounts|${ftp:-0}|$L_FTP
EOF
    printf '  %-24s %-12s %-12s\n' "Subdomains" "$sub_domains" "-"
    printf '  %-24s %-12s %-12s\n' "Disk" "$( [[ -n "$disk_used" ]] && human_kb "$disk_used" || echo -)" "$(limit_label "$L_DISK")"
    printf '  %-24s %-12s %-12s\n' "Inodes" "${inodes_used:--}" "$(limit_label "$L_INODES")"
    if [[ ! -S "$sock" ]]; then
        echo "  ${C_DIM}MySQL isn't running, databases counted as 0.${C_RESET}"
    elif [[ -z "$dbs" ]]; then
        echo "  ${C_DIM}MySQL didn't answer in 5 seconds, databases not counted.${C_RESET}"
    fi

    if (( ${#reached[@]} )) && grep -qE '^key=enterprise' "$PANEL_CONFIG" 2>/dev/null; then
        local upsell
        [[ -n "${PLAN[upsell_url]-}" ]] && upsell="${PLAN[upsell]-}"
        [[ -n "$upsell" ]] && info "Upgrade to the $upsell plan for higher limits."
    fi
}


# key|default|title|parent, same list and defaults as notificationDefs in the panel
readonly NOTIFICATION_DEFS="notify_account_login|1|New login|
notify_account_login_for_known_netblock|0|Also for known IP addresses|notify_account_login
notify_account_login_notification_disabled|1|Email if login alerts get turned off|notify_account_login
notify_password_change|1|Password changed|
notify_password_change_notification_disabled|1|Email if this alert gets turned off|notify_password_change
notify_contact_address_change|1|Contact email changed|
notify_contact_address_change_notification_disabled|1|Email if this alert gets turned off|notify_contact_address_change
notify_twofactorauth_change|1|Two-factor authentication changed|
notify_twofactorauth_change_notification_disabled|1|Email if this alert gets turned off|notify_twofactorauth_change
notify_passkey_change|1|Passkey added or removed|
notify_passkey_change_notification_disabled|1|Email if this alert gets turned off|notify_passkey_change
notify_api_token_change|1|API token created or revoked|
notify_api_token_change_notification_disabled|1|Email if this alert gets turned off|notify_api_token_change
notify_ssl_expiry|1|SSL certificate problem|
notify_malware_found|1|Malware found|
notify_disk_limit|1|Disk space running out|
notify_email_quota_limit|1|Mailbox almost full|
notify_email_ratelimit|1|Hourly email limit reached|
notify_service_failed|1|Service stopped|"

section_account() {
    section "ACCOUNT"

    # same chain as the panel: user's locale file, else the server default, else en
    local locale locale_src="user" default_locale
    default_locale=$(tr -d '[:space:]' 2>/dev/null < /etc/openpanel/openpanel/default_locale)
    locale=$(tr -d '[:space:]' 2>/dev/null < "/home/$U_CONTEXT/locale")
    if [[ -z "$locale" ]]; then
        locale="${default_locale:-en}" locale_src="server default"
    fi
    row "Locale" "$locale ($locale_src)"
    if [[ "$locale" != "en" && ! -f "/etc/openpanel/openpanel/translations/$locale/LC_MESSAGES/messages.po" ]]; then
        warn "Locale $locale is not installed, the panel falls back to the browser language or English."
    fi

    # extras the admin may have set for this user
    local notes_first
    notes_first=$(grep -m1 . "/home/$U_CONTEXT/notes.txt" 2>/dev/null | cut -c1-60)
    row "Admin notes" "${notes_first:-none}"
    # custom_message.html is shown on the user's dashboard, first bit of its text without the html
    local msg_file="$USERS_CORE_DIR/$U_NAME/custom_message.html" msg_text
    if [[ -s "$msg_file" ]]; then
        msg_text=$(sed -e 's/<[^>]*>/ /g' "$msg_file" | tr -s '[:space:]' ' ' | sed 's/^ //' | cut -c1-60)
        row "Custom message" "set: ${msg_text:-(no text)}"
    else
        row "Custom message" "none"
    fi

    subsection "NOTIFICATIONS"
    # send_user_email in lib/email.sh needs all of these to deliver anything
    local can_send=1
    if [[ -z "$U_EMAIL" ]]; then
        warn "No contact email is set, the user gets no notification emails."
        can_send=0
    fi
    if [[ ",$(panel_config enabled_modules)," != *",notifications,"* ]]; then
        warn "Notifications module is disabled on the server, no notification emails are sent."
        can_send=0
    fi
    if ! systemctl is-active --quiet admin 2>/dev/null; then
        warn "OpenAdmin is not running, notification emails can't be sent until it is."
        can_send=0
    fi
    local smtp; smtp=$(panel_config mail_server)
    row "Sent to" "${U_EMAIL:--}"
    row "Mail server" "${smtp:-localhost (built-in mail server)}"
    (( can_send )) && ok "Notification emails can be delivered."

    local prefs_file="$USERS_CORE_DIR/$U_NAME/notifications.yaml" key def title parent value label off=()
    [[ -f "$prefs_file" ]] || echo "  ${C_DIM}No preferences saved yet, defaults apply.${C_RESET}"
    printf '  %s%-44s %s%s\n' "$C_DIM" "Alert" "Email" "$C_RESET"
    while IFS='|' read -r key def title parent; do
        value=$(grep -m1 "^[[:space:]]*${key}[[:space:]]*=" "$prefs_file" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')
        [[ "$value" == 0 || "$value" == 1 ]] || value="$def"
        [[ "$value" == 1 ]] && label="${C_GREEN}on${C_RESET}" || label="${C_DIM}off${C_RESET}"
        if [[ -n "$parent" ]]; then
            printf '    %-42s %s\n' "$title" "$label"
        else
            printf '  %-44s %s\n' "$title" "$label"
            [[ "$value" == 0 ]] && off+=("$title")
        fi
    done <<< "$NOTIFICATION_DEFS"
    (( ${#off[@]} )) && info "Turned off by the user: $(printf '%s, ' "${off[@]}" | sed 's/, $//')."

    subsection "FAVORITES"
    local fav_file="$USERS_CORE_DIR/$U_NAME/favorites.json" count max
    max=$(panel_config favorites_items 10)
    if [[ ! -s "$fav_file" ]]; then
        row "Favorites" "none"
        return
    fi
    if ! count=$(jq 'length' "$fav_file" 2>/dev/null); then
        err "Favorites file $fav_file is not valid JSON, favorites won't load."
        return
    fi
    row "Favorites" "$count of $max"
    jq -r '.[] | "\(.title)\t\(.link)"' "$fav_file" 2>/dev/null | while IFS=$'\t' read -r title link; do
        printf '  %-24s %s\n' "$title" "$link"
    done
}


# podman against the user's rootless instance, with a timeout (timeout can't run the podman_user function)
upodman() {
    local sock
    sock=$(podman_user_socket "$U_CONTEXT") || return 1
    CONTAINER_HOST="$sock" timeout 20 podman --remote "$@"
}

readonly DEFAULT_COMPOSE="/etc/openpanel/docker/compose/1.0/docker-compose.yml"
DEFAULT_SERVICES=""

# service names under the top-level services: key, whatever the indent
compose_services() {
    awk '/^services:/ { f = 1; next } f && /^[^ #]/ { exit } f && /^ +[A-Za-z0-9_.-]+:/ { match($0, /^ +/); if (!lvl) lvl = RLENGTH; if (RLENGTH == lvl) { sub(/^ +/, ""); sub(/:.*/, ""); print } }' "$1" 2>/dev/null
}

# bytes to a readable size
human_bytes() {
    LC_ALL=C awk -v b="${1:-0}" 'BEGIN { if (b >= 1073741824) printf "%.2f GB\n", b / 1073741824; else if (b >= 1048576) printf "%.0f MB\n", b / 1048576; else if (b >= 1024) printf "%.0f KB\n", b / 1024; else printf "%d B\n", b }'
}

# "daily at 04:45" for simple schedules, the raw expression otherwise
cron_label() {
    local m h dom mon dow
    read -r m h dom mon dow <<< "$1"
    if [[ "$m" =~ ^[0-9]+$ && "$h" =~ ^[0-9]+$ && "$mon" == "*" ]]; then
        local at; at=$(printf '%02d:%02d' "$h" "$m")
        if [[ "$dom" == "*" && "$dow" == "*" ]]; then echo "daily at $at"; return; fi
        if [[ "$dom" == "*" && "$dow" =~ ^[0-7]$ ]]; then
            local days=(Sunday Monday Tuesday Wednesday Thursday Friday Saturday Sunday)
            echo "weekly on ${days[$dow]} at $at"; return
        fi
        if [[ "$dom" =~ ^[0-9]+$ && "$dow" == "*" ]]; then echo "monthly on day $dom at $at"; return; fi
    fi
    echo "cron $1"
}

# "1h 5m" style age from seconds
human_age() {
    local s="${1:-0}"
    if (( s >= 86400 )); then echo "$((s / 86400))d $((s % 86400 / 3600))h"
    elif (( s >= 3600 )); then echo "$((s / 3600))h $((s % 3600 / 60))m"
    elif (( s >= 60 )); then echo "$((s / 60))m"
    else echo "${s}s"
    fi
}

section_containers() {
    section "CONTAINERS"

    local ps_out
    need_ps
    ps_out="$U_PS"
    if (( ! U_PS_OK )); then
        err "Can't reach the user's podman, none of the account's services can be checked or managed."
        containers_usage
        return
    fi
    if [[ -z "$ps_out" ]]; then
        warn "No containers exist for this account."
        containers_usage
        return
    fi

    # live usage of running containers, keyed by name
    local -A s_cpu s_mem s_memp s_pids
    local name cpu mem memp pids
    while IFS=$'\t' read -r name cpu mem memp pids; do
        [[ -n "$name" ]] || continue
        s_cpu[$name]="${cpu%\%}" s_mem[$name]="$mem" s_memp[$name]="${memp%\%}" s_pids[$name]="$pids"
    done < <(upodman stats --no-stream --format json 2>/dev/null | jq -r '.[] | [.name, .cpu_percent, .mem_usage, .mem_percent, .pids] | @tsv' 2>/dev/null)

    # limits and history from inspect
    local -A i_cpus i_mem i_pids i_restarts i_oom i_exit i_started i_hlog i_htest
    local ncpu memlim pidlim restarts oom code started hlog htest
    while IFS=$'\t' read -r name ncpu memlim pidlim restarts oom code started hlog htest; do
        [[ -n "$name" ]] || continue
        i_cpus[$name]="$ncpu" i_mem[$name]="$memlim" i_pids[$name]="$pidlim" i_restarts[$name]="$restarts"
        i_oom[$name]="$oom" i_exit[$name]="$code" i_started[$name]="$started" i_hlog[$name]="$hlog" i_htest[$name]="$htest"
    done < <(upodman inspect $(cut -f4 <<< "$ps_out") \
        --format '{{.Name}}\t{{.HostConfig.NanoCpus}}\t{{.HostConfig.Memory}}\t{{.HostConfig.PidsLimit}}\t{{.RestartCount}}\t{{.State.OOMKilled}}\t{{.State.ExitCode}}\t{{.State.StartedAt}}\t{{len .State.Health.Log}}\t{{if .Config.Healthcheck}}{{len .Config.Healthcheck.Test}}{{else}}0{{end}}' 2>/dev/null)

    local no_checks=()

    # where each container comes from: panel template, a panel app (sites table), the user's own, or not in compose at all
    local -A c_src
    local svc site
    need_services
    [[ -n "$DEFAULT_SERVICES" ]] || DEFAULT_SERVICES=$(compose_services "$DEFAULT_COMPOSE")
    while read -r svc; do [[ -n "$svc" ]] && c_src[$svc]="custom"; done <<< "$U_SERVICES"
    while read -r svc; do [[ -n "${c_src[$svc]}" ]] && c_src[$svc]="default"; done <<< "$DEFAULT_SERVICES"
    while IFS=$'\t' read -r svc site; do
        svc="${svc,,}"
        [[ "${c_src[$svc]}" == custom ]] && c_src[$svc]="app:$site"
    done < <(awk -F'\t' '$4 != "" { print $4 "\t" $1 }' <<< "$U_SITE_ROWS")
    local custom=()
    local now total=0 running=0 state status id cores cpu_pct cpu_txt mem_txt pids_txt up_secs started_epoch
    now=$(date +%s)
    printf '  %s%-18s %-8s %-30s %-16s %-26s %-11s %s%s\n' "$C_DIM" "Container" "Source" "Status" "CPU" "RAM" "PIDs" "Restarts" "$C_RESET"
    while IFS=$'\t' read -r name state status id; do
        ((total++))
        cores=$(LC_ALL=C awk -v n="${i_cpus[$name]:-0}" 'BEGIN { printf "%g", n / 1e9 }')
        cpu_txt="-" mem_txt="-" pids_txt="-" cpu_pct=""
        if [[ "$state" == running ]]; then
            ((running++))
            if [[ -n "${s_cpu[$name]}" ]]; then
                # percent of the container's own cpu limit, same math as the containers page
                if [[ "$cores" != 0 ]]; then
                    cpu_pct=$(LC_ALL=C awk -v c="${s_cpu[$name]}" -v n="$cores" 'BEGIN { printf "%.0f", c / n }')
                    cpu_txt="${cpu_pct}% of ${cores}"
                else
                    cpu_txt="${s_cpu[$name]}%"
                fi
                mem_txt="${s_mem[$name]// /} ($(printf '%.0f' "${s_memp[$name]:-0}")%)"
                pids_txt="${s_pids[$name]}/${i_pids[$name]:-?}"
            fi
        fi
        # podman-compose leaves an empty health check on containers without one and podman shows that as "(starting)" forever
        [[ "${i_htest[$name]:-0}" == 0 ]] && status="${status% (starting)}"
        local src="${c_src[$name]:-manual}" src_txt
        case "$src" in
            default) src_txt="default" ;;
            app:*) src_txt="${C_CYAN}app${C_RESET}    " ; custom+=("$name (app for ${src#app:})") ;;
            custom) src_txt="${C_YELLOW}custom${C_RESET} " ; custom+=("$name (added to docker-compose.yml)") ;;
            *) src_txt="${C_YELLOW}manual${C_RESET} " ; custom+=("$name (not in docker-compose.yml)") ;;
        esac
        printf '  %-18s %-8s %-30s %-16s %-26s %-11s %s\n' "$name" "$src_txt" "${status:0:30}" "$cpu_txt" "$mem_txt" "$pids_txt" "${i_restarts[$name]:-0}"

        # problems
        started_epoch=$(date -d "$(sed -E 's/\.[0-9]+//; s/ [A-Z]+$//' <<< "${i_started[$name]}")" +%s 2>/dev/null || echo "$now")
        up_secs=$(( now - started_epoch ))
        if [[ "${i_oom[$name]}" == true ]]; then
            warn "$name was killed for running out of memory (limit $(human_bytes "${i_mem[$name]}"))."
        fi
        if [[ "$state" != running ]]; then
            [[ "${i_exit[$name]:-0}" != 0 ]] && warn "$name is stopped, it exited with code ${i_exit[$name]}."
            continue
        fi
        if [[ "$status" == *"(unhealthy)"* ]]; then
            warn "$name is unhealthy, its health check is failing."
        elif [[ "$status" == *"(starting)"* ]] && (( up_secs > 600 )); then
            # no health log at all means podman never ran the check, not that it failed
            if [[ "${i_hlog[$name]:-0}" == 0 ]]; then
                no_checks+=("$name")
            else
                warn "$name health check hasn't passed after $(human_age "$up_secs")."
            fi
        fi
        if [[ -n "$cpu_pct" ]] && (( cpu_pct >= 90 )); then
            warn "$name is using ${cpu_pct}% of its CPU limit (${cores} cores)."
        fi
        if [[ -n "${s_memp[$name]}" ]] && LC_ALL=C awk -v m="${s_memp[$name]}" 'BEGIN { exit !(m >= 90) }'; then
            warn "$name is using $(printf '%.0f' "${s_memp[$name]}")% of its memory limit."
        fi
        if [[ "${s_pids[$name]}" =~ ^[0-9]+$ && "${i_pids[$name]}" =~ ^[0-9]+$ ]] && (( i_pids[$name] > 0 && s_pids[$name] * 100 / i_pids[$name] >= 90 )); then
            warn "$name is using ${s_pids[$name]} of ${i_pids[$name]} processes."
        fi
        if [[ "${i_restarts[$name]}" =~ ^[0-9]+$ ]] && (( i_restarts[$name] >= 5 )); then
            warn "$name restarted ${i_restarts[$name]} times, it may be crashing."
        fi
    done <<< "$ps_out"
    echo
    row "Containers" "$running running, $((total - running)) stopped"
    # custom services in the compose file that were never created
    for svc in "${!c_src[@]}"; do
        [[ "${c_src[$svc]}" == default ]] && continue
        grep -qx "$svc" < <(cut -f1 <<< "$ps_out") && continue
        custom+=("$svc (in docker-compose.yml, not created)")
    done
    local label="Not from the template" c
    for c in "${custom[@]}"; do
        row "$label" "$c"
        label=""
    done
    (( ${#no_checks[@]} )) && info "Health checks never ran for $(printf '%s, ' "${no_checks[@]}" | sed 's/, $//'), so their health is unknown."

    local images
    images=$(upodman images --format json 2>/dev/null | jq -r '"\(length) \(map(.Size) | add // 0)"' 2>/dev/null)
    [[ -n "$images" ]] && row "Images" "${images%% *}, $(human_bytes "${images##* }")"

    containers_usage
}

# account-wide usage from resource_usage.txt, written hourly by 'opencli docker-collect_stats'
containers_usage() {
    subsection "ACCOUNT USAGE"
    local file="/home/$U_CONTEXT/resource_usage.txt" last
    last=$(tail -n 1 "$file" 2>/dev/null)
    if ! jq -e . >/dev/null 2>&1 <<< "$last"; then
        warn "No usage data collected yet ($file)."
        return
    fi

    local ts mem_used mem_total mem_pct cpu_used cpu_total cpu_pct tasks tasks_limit tasks_pct age
    IFS=$'\t' read -r ts mem_used mem_total mem_pct cpu_used cpu_total cpu_pct tasks tasks_limit tasks_pct < <(jq -r '[.timestamp, .memory.used.human, .memory.total.human, .memory.usage_pct, .cpu.usage.human, .cpu.total.human,
        (if (.cpu.total.pct // 0) > 0 then (.cpu.usage.pct * 100 / .cpu.total.pct | floor) else 0 end), .tasks.current, .tasks.limit, .tasks.usage_pct] | @tsv' <<< "$last")
    age=$(( $(date +%s) - $(date -d "$ts" +%s 2>/dev/null || date +%s) ))
    row "Last sample" "$ts ($(human_age "$age") ago)"
    (( age > 7200 )) && warn "Usage stats haven't been collected for $(human_age "$age"), check the 'opencli docker-collect_stats' cron."
    row "RAM" "$mem_used of $mem_total (${mem_pct}%)"
    row "CPU" "$cpu_used of $cpu_total (${cpu_pct}%)"
    row "Processes" "$tasks of $tasks_limit (${tasks_pct}%)"

    # same thresholds as the dashboard
    if (( mem_pct >= 100 )); then err "Account RAM is at ${mem_pct}% of the plan limit."
    elif (( mem_pct >= 90 )); then warn "Account RAM is at ${mem_pct}% of the plan limit."
    fi
    if (( cpu_pct >= 100 )); then err "Account CPU is at ${cpu_pct}% of the plan limit."
    elif (( cpu_pct >= 90 )); then warn "Account CPU is at ${cpu_pct}% of the plan limit."
    fi
    (( tasks_pct >= 90 )) && warn "Account is using ${tasks_pct}% of its process limit."

    # last 24 samples (hourly), averages and peaks
    local hist
    hist=$(tail -n 24 "$file" | jq -sr '[.[] | select(type == "object")] as $s | if ($s | length) == 0 then empty else
        [($s | length),
         ([$s[].memory.usage_pct] | add / length | floor), ([$s[].memory.usage_pct] | max),
         ([$s[] | if (.cpu.total.pct // 0) > 0 then .cpu.usage.pct * 100 / .cpu.total.pct else 0 end] | (add / length | floor), (max | floor)),
         ([$s[] | select(.memory.usage_pct >= 90 or ((.cpu.total.pct // 0) > 0 and .cpu.usage.pct * 100 / .cpu.total.pct >= 90))] | length)] | @tsv end' 2>/dev/null)
    if [[ -n "$hist" ]]; then
        local n mem_avg mem_max cpu_avg cpu_max hot
        IFS=$'\t' read -r n mem_avg mem_max cpu_avg cpu_max hot <<< "$hist"
        row "Last $n samples" "RAM avg ${mem_avg}%, peak ${mem_max}% / CPU avg ${cpu_avg}%, peak ${cpu_max}%"
        (( hot > 0 )) && info "Account was at 90% or more of its CPU or RAM limit in $hot of the last $n hourly samples."
    fi
}


section_processes() {
    section "PROCESSES"

    local running
    need_ps
    running="$U_RUNNING"
    if (( ! U_PS_OK )); then
        err "Can't reach the user's podman, processes can't be listed."
        return
    fi
    if [[ -z "$running" ]]; then
        row "Processes" "none, no containers are running"
        return
    fi

    # host pid -> container and in-container user, from podman top
    local -A p_ctr p_user
    local c hpid user
    while read -r c; do
        while read -r hpid user _; do
            [[ "$hpid" =~ ^[0-9]+$ ]] || continue
            p_ctr[$hpid]="$c" p_user[$hpid]="$user"
        done < <(upodman top "$c" hpid user 2>/dev/null)
    done <<< "$running"
    if (( ${#p_ctr[@]} == 0 )); then
        warn "podman top returned no processes for the running containers."
        return
    fi

    # real RAM and state come from the host ps, podman top has no rss
    local rows
    rows=$(ps -o pid=,stat=,rss=,pcpu=,etimes=,args= -p "$(IFS=,; echo "${!p_ctr[*]}")" 2>/dev/null \
        | awk '{ printf "%s\t%s\t%s\t%s\t%s\t", $1, $2, $3, $4, $5; $1 = $2 = $3 = $4 = $5 = ""; sub(/^ +/, ""); print }')

    local -A per_ctr per_rss
    local pid stat rss pcpu etimes args total=0 zombies=() total_rss=0
    while IFS=$'\t' read -r pid stat rss pcpu etimes args; do
        [[ -n "$pid" ]] || continue
        c="${p_ctr[$pid]}"
        ((total++))
        per_ctr[$c]=$(( ${per_ctr[$c]:-0} + 1 ))
        per_rss[$c]=$(( ${per_rss[$c]:-0} + rss ))
        total_rss=$(( total_rss + rss ))
        [[ "$stat" == Z* ]] && zombies+=("$c")
    done <<< "$rows"

    row "Processes" "$total in $(wc -l <<< "$running") containers, $(human_kb "$total_rss") RAM"
    printf '  %s%-24s %-10s %s%s\n' "$C_DIM" "Container" "Processes" "RAM" "$C_RESET"
    for c in $(for k in "${!per_ctr[@]}"; do echo "${per_rss[$k]} $k"; done | sort -rn | awk '{ print $2 }'); do
        printf '  %-24s %-10s %s\n' "$c" "${per_ctr[$c]}" "$(human_kb "${per_rss[$c]}")"
    done

    # ps %cpu is the average over the process lifetime, good enough to spot hogs
    local header
    header=$(printf '  %s%-16s %-9s %-10s %-7s %-10s %-9s %s%s' "$C_DIM" "Container" "Host PID" "User" "CPU%" "RAM" "Running" "Command" "$C_RESET")
    subsection "TOP BY CPU"
    echo "$header"
    sort -t$'\t' -k4,4 -rn <<< "$rows" | head -n 5 | proc_rows p_ctr p_user

    subsection "TOP BY RAM"
    echo "$header"
    sort -t$'\t' -k3,3 -rn <<< "$rows" | head -n 5 | proc_rows p_ctr p_user

    # problems
    if (( ${#zombies[@]} )); then
        warn "${#zombies[@]} zombie process(es) in $(printf '%s\n' "${zombies[@]}" | sort -u | paste -sd, - | sed 's/,/, /g'), their parent isn't reaping them."
    fi
    local hog
    while IFS=$'\t' read -r pid stat rss pcpu etimes args; do
        [[ -n "$pid" ]] || continue
        if LC_ALL=C awk -v c="$pcpu" 'BEGIN { exit !(c >= 90) }' && (( etimes >= 600 )); then
            hog="${args:0:60}"
            warn "Process $pid in ${p_ctr[$pid]} ($hog) has used ${pcpu}% CPU for $(human_age "$etimes")."
        fi
    done <<< "$rows"
}

# prints process rows from stdin, the two args are the names of the pid->container and pid->user maps
proc_rows() {
    local -n _ctr="$1" _usr="$2"
    local pid stat rss pcpu etimes args
    while IFS=$'\t' read -r pid stat rss pcpu etimes args; do
        printf '  %-16s %-9s %-10s %-7s %-10s %-9s %s\n' "${_ctr[$pid]:0:16}" "$pid" "${_usr[$pid]:0:10}" "$pcpu" "$(human_kb "$rss")" "$(human_age "$etimes")" "${args:0:50}"
    done
}


# GB/MB/KB from du -sk style KB with a dimmed path
du_rows() {
    local kb path
    while read -r kb path; do
        printf '  %-12s %s\n' "$(human_kb "$kb")" "$path"
    done
}

section_files() {
    section "FILES"
    local html="/home/$U_CONTEXT/docker-data/volumes/${U_CONTEXT}_html_data/_data"

    subsection "DISK"
    local disk_used="" disk_hard="" inodes_used="" inodes_hard="" ts=""
    need_quota
    ts="$QUOTA_TS"
    read -r disk_used disk_hard inodes_used inodes_hard < <(awk -F'\t' -v u="$U_CONTEXT" '$1 == u { print $2, $3, $4, $5; exit }' <<< "$QUOTA_TSV")
    if [[ -z "$disk_used" ]]; then
        warn "No quota data for $U_CONTEXT, run 'opencli user-quota --update $U_NAME'."
    else
        local pct
        row "Quota checked" "${ts:-unknown}"
        echo "  ${C_DIM}Quota counts files owned by the account, files written by apps as other users aren't included.${C_RESET}"
        for kind in disk inodes; do
            local used hard label
            if [[ $kind == disk ]]; then used=$disk_used hard=$disk_hard label="Disk"; else used=$inodes_used hard=$inodes_hard label="Inodes"; fi
            if [[ "$hard" =~ ^[0-9]+$ ]] && (( hard > 0 )); then
                pct=$(( used * 100 / hard ))
                if [[ $kind == disk ]]; then
                    row "$label" "$(human_kb "$used") of $(human_kb "$hard") (${pct}%)"
                else
                    row "$label" "$used of $hard (${pct}%)"
                fi
                # same thresholds as the dashboard
                if (( pct >= 100 )); then err "$label usage is at ${pct}% of the limit, new files can't be written."
                elif (( pct >= 90 )); then warn "$label usage is at ${pct}% of the limit."
                fi
            else
                [[ $kind == disk ]] && row "$label" "$(human_kb "$used") (unlimited)" || row "$label" "$used (unlimited)"
            fi
        done
    fi

    # where the space goes, from the du the DOMAINS section shares
    local vols="/home/$U_CONTEXT/docker-data/volumes"
    if [[ -d "$vols" ]]; then
        need_du
        echo "  ${C_DIM}Biggest volumes:${C_RESET}"
        awk -F'\t' -v v="$vols" 'index($2, v "/") == 1 && substr($2, length(v) + 2) !~ /\//' <<< "$U_DU" | sort -rn | head -n 5 \
            | sed -E "s#$vols/${U_CONTEXT}_##; s#$vols/##" | du_rows
        if [[ -d "$html" ]]; then
            echo "  ${C_DIM}Biggest folders in /var/www/html:${C_RESET}"
            awk -F'\t' -v h="$html" 'index($2, h "/") == 1 && substr($2, length(h) + 2) !~ /\//' <<< "$U_DU" | sort -rn | head -n 5 \
                | sed -E "s#^([0-9]+)\s+$html/#\1 #" | du_rows
        fi
    fi

    subsection "PERMISSIONS"
    if [[ ! -d "$html" ]]; then
        err "Website files volume is missing ($html)."
    else
        # 0000 is what the file manager flags, world-writable is a security risk (sticky dirs like tmp are fine)
        local noperm writable n_noperm n_writable
        noperm=$(timeout 60 find "$html" -path "$html/.quarantine" -prune -o -perm 0000 -print 2>/dev/null)
        writable=$(timeout 60 find "$html" -path "$html/.quarantine" -prune -o -perm -o+w ! -type l ! \( -type d -perm -1000 \) -print 2>/dev/null)
        n_noperm=$(grep -c . <<< "$noperm"); n_writable=$(grep -c . <<< "$writable")
        row "No permissions (0000)" "$n_noperm"
        if (( n_noperm )); then
            err "$n_noperm file(s) have no permissions (0000) and can't be read."
            sed "s#^$html#  /var/www/html#" <<< "$noperm" | head -n 5
            (( n_noperm > 5 )) && echo "  ..."
        fi
        row "World-writable" "$n_writable"
        if (( n_writable )); then
            warn "$n_writable file(s) or folder(s) are world-writable (o+w), Fix Permissions resets them."
            sed "s#^$html/##" <<< "$writable" | cut -d/ -f1 | sort | uniq -c | sort -rn | head -n 5 \
                | while read -r n dir; do printf '  %7s  %s\n' "$n" "/var/www/html/$dir"; done
        fi
        (( n_noperm + n_writable == 0 )) && ok "No permission problems found."
    fi

    subsection "TRASH"
    local trash="/home/$U_CONTEXT/.local/share/Trash" trash_kb=0 trash_n=0
    if [[ -d "$trash" ]]; then
        trash_kb=$(du -sk "$trash" 2>/dev/null | cut -f1)
        trash_n=$(find "$trash" -mindepth 1 -maxdepth 1 ! -name .trash_restore 2>/dev/null | wc -l)
    fi
    row "Trash" "$trash_n item(s), $(human_kb "$trash_kb")"

    # files-purge_trash deletes items older than autopurge_trash days, but only when its cron runs
    local purge_days purge_cron
    purge_days=$(panel_config autopurge_trash)
    purge_cron=$(grep -h "files-purge_trash" /etc/cron.d/* 2>/dev/null | grep -v '^\s*#' | awk '{ print $1, $2, $3, $4, $5 }' | head -n1)
    if [[ "$purge_days" =~ ^[0-9]+$ && -n "$purge_cron" ]]; then
        row "Auto-purge" "after $purge_days days, $(cron_label "$purge_cron")"
        if [[ -f "$trash/.trash_restore" ]] && (( trash_n )); then
            local oldest
            oldest=$(grep -o 'deletion_date=[0-9T:-]*' "$trash/.trash_restore" | cut -d= -f2 | sort | head -n1)
            if [[ -n "$oldest" ]]; then
                local due
                due=$(date -d "@$(( $(date -d "${oldest/T/ }" +%s) + purge_days * 86400 ))" '+%Y-%m-%d %H:%M' 2>/dev/null)
                row "Oldest item" "trashed $oldest, purged on the first run after $due"
            fi
        fi
    elif [[ "$purge_days" =~ ^[0-9]+$ ]]; then
        row "Auto-purge" "off"
        warn "autopurge_trash is $purge_days days but no cron runs 'opencli files-purge_trash', so the trash is never emptied."
    else
        row "Auto-purge" "off (autopurge_trash not set)"
    fi
    (( trash_kb >= 1048576 )) && info "Emptying the trash would free $(human_kb "$trash_kb")."

    subsection "FTP"
    local list="/etc/openpanel/ftp/users/$U_CONTEXT/users.list"
    if [[ ",$(panel_config enabled_modules)," != *",ftp,"* ]]; then
        row "FTP" "module disabled on the server"
    else
        local ftp_state
        need_root_ps
        ftp_state=$(root_state openadmin_ftp)
        row "FTP server" "${ftp_state:-not installed}"
        local accounts; accounts=$(grep -c . "$list" 2>/dev/null)
        row "Accounts" "${accounts:-0}"
        if (( accounts )); then
            [[ "$ftp_state" == running ]] || warn "FTP server isn't running, the ${accounts} FTP account(s) can't connect."
            local fu fpath host_path
            while IFS='|' read -r fu _ fpath _; do
                [[ -n "$fu" ]] || continue
                host_path="$fpath"
                [[ "$fpath" == /var/www/html* ]] && host_path="$html${fpath#/var/www/html}"
                printf '    %-36s %s\n' "$fu" "$fpath"
                [[ -d "$host_path" ]] || warn "FTP account $fu points to $fpath, which doesn't exist."
            done < "$list"
        fi
        local conns
        conns=$(timeout 10 opencli ftp-connections "$U_NAME" 2>/dev/null | grep "vsftpd:")
        row "Connected now" "$(grep -c . <<< "$conns")"
        [[ -n "$conns" ]] && sed 's/^ */  /' <<< "$conns" | cut -c1-100
    fi

    subsection "MALWARE SCANNER"
    local clam scans_file="$USERS_CORE_DIR/$U_NAME/malware_scans.jsonl"
    need_root_ps
    clam=$(root_state clamav)
    row "ClamAV" "${clam:-not installed}"
    local sched
    sched=$(grep -h "files-malware_scan" /etc/cron.d/* 2>/dev/null | grep -v '^#' | awk '{ print $1, $2, $3, $4, $5 }' | head -n1)
    row "Scheduled scan" "$( [[ -n "$sched" ]] && cron_label "$sched" || echo none)"
    if [[ ",$(panel_config enabled_modules)," == *",malware_scan,"* && "$clam" != running ]]; then
        warn "ClamAV isn't running, malware scans fail until it's started."
    fi

    if [[ -s "$scans_file" ]]; then
        local last last_ok failed
        last=$(tail -n 1 "$scans_file")
        last_ok=$(grep '"status":"completed"' "$scans_file" | tail -n 1 | jq -r '.finished_at // .started_at' 2>/dev/null)
        failed=$(tail -n 20 "$scans_file" | grep -c '"status":"failed"')
        row "Last scan" "$(jq -r '"\(.started_at) (\(.source), \(.status)) \(.path), \(.scanned) scanned, \(.infected) infected"' <<< "$last" 2>/dev/null)"
        row "Last completed scan" "${last_ok:-never}"
        [[ "$(jq -r .status <<< "$last" 2>/dev/null)" == failed ]] && warn "Last malware scan failed: $(jq -r '.message // "no reason given"' <<< "$last")"
        (( failed > 1 )) && info "$failed of the last 20 malware scans failed."
    else
        row "Last scan" "never"
    fi

    local qdir="$html/.quarantine" qn=0
    if [[ -d "$qdir" ]]; then
        qn=$(find "$qdir" -type f ! -name .metadata.jsonl 2>/dev/null | wc -l)
    fi
    row "Quarantine" "$qn file(s)"
    (( qn > 0 )) && warn "$qn file(s) in malware quarantine, review them on the Malware Scanner > Quarantine page."
}


# value of KEY from a shell-style env file, quotes stripped, commented lines ignored
env_value() {
    grep -m1 -E "^[[:space:]]*$2=" "$1" 2>/dev/null | cut -d= -f2- | sed -E "s/^[\"']//; s/[\"'][[:space:]]*$//"
}

section_backups() {
    section "BACKUPS"
    local env="/home/$U_CONTEXT/backup.env"

    if [[ -f "$USERS_CORE_DIR/$U_CONTEXT/admin.backups" ]]; then
        row "Managed by" "administrator"
    else
        row "Managed by" "user"
    fi
    if [[ ! -f "$env" ]]; then
        warn "No backup.env, account backups were never set up."
        user_backup_archives
        return
    fi

    # same credential keys the Backups page uses to call a destination configured
    local dest="" detail=""
    if [[ -n "$(env_value "$env" AWS_ACCESS_KEY_ID)" ]]; then
        dest="S3" detail="$(env_value "$env" AWS_ENDPOINT)/$(env_value "$env" AWS_S3_BUCKET_NAME)/$(env_value "$env" AWS_S3_PATH)"
    elif [[ -n "$(env_value "$env" SSH_HOST_NAME)" ]]; then
        dest="SSH" detail="$(env_value "$env" SSH_USER)@$(env_value "$env" SSH_HOST_NAME):$(env_value "$env" SSH_REMOTE_PATH)"
    elif [[ -n "$(env_value "$env" WEBDAV_USERNAME)" ]]; then
        dest="WebDAV" detail="$(env_value "$env" WEBDAV_URL)$(env_value "$env" WEBDAV_PATH)"
    elif [[ -n "$(env_value "$env" AZURE_STORAGE_ACCOUNT_NAME)" ]]; then
        dest="Azure" detail="$(env_value "$env" AZURE_STORAGE_ACCOUNT_NAME)/$(env_value "$env" AZURE_STORAGE_CONTAINER_NAME)"
    elif [[ -n "$(env_value "$env" DROPBOX_APP_KEY)" ]]; then
        dest="Dropbox" detail="$(env_value "$env" DROPBOX_REMOTE_PATH)"
    fi
    row "Destination" "${dest:-none}${detail:+ ($detail)}"

    local cron retention
    cron=$(env_value "$env" BACKUP_CRON_EXPRESSION)
    retention=$(env_value "$env" BACKUP_RETENTION_DAYS)
    case "$cron" in
        @*) row "Schedule" "${cron#@}" ;;
        "") row "Schedule" "default (daily at 00:00)" ;;
        *) row "Schedule" "$(cron_label "$cron")" ;;
    esac
    if [[ -z "$retention" || "$retention" == "-1" ]]; then
        row "Retention" "keep forever"
    else
        row "Retention" "$retention days"
    fi
    if [[ -n "$(env_value "$env" GPG_PASSPHRASE)$(env_value "$env" GPG_PUBLIC_KEY_RING)$(env_value "$env" AGE_PASSPHRASE)$(env_value "$env" AGE_PUBLIC_KEYS)" ]]; then
        row "Encryption" "on"
    else
        row "Encryption" "off"
    fi
    local notify; notify=$(env_value "$env" NOTIFICATION_URLS)
    row "Failure alerts" "$( [[ -n "$notify" ]] && echo "on ($(env_value "$env" NOTIFICATION_LEVEL))" || echo off)"

    # the backup container runs the schedule, without it nothing happens
    local state
    need_ps
    state=$(awk -F'\t' '$1 == "backup" { print $2 }' <<< "$U_PS")
    row "Backup service" "${state:-not created}"
    if [[ -z "$dest" ]]; then
        warn "No backup destination is configured, this account isn't backed up."
    elif [[ "$state" != running ]]; then
        err "Backups go to $dest but the backup service isn't running, scheduled backups don't happen."
    else
        upodman exec backup test -f /var/run/lock/dockervolumebackup.lock 2>/dev/null && row "In progress" "yes"
        # offen/docker-volume-backup logs one line per run
        local logs last_ok last_err
        logs=$(upodman logs --since 720h backup 2>&1 | tail -n 500)
        last_ok=$(grep -i 'Finished running backup tasks' <<< "$logs" | tail -n1 | grep -oE '[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]+' | head -n1)
        last_err=$(grep -E 'level=ERROR' <<< "$logs" | tail -n1)
        row "Last successful run" "${last_ok:-none in the last 30 days}"
        if [[ -n "$last_err" ]]; then
            local err_time; err_time=$(grep -oE '[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]+' <<< "$last_err" | head -n1)
            if [[ -z "$last_ok" || "$err_time" > "$last_ok" ]]; then
                err "Last backup run failed: $(grep -oP 'error="?\K[^"]+' <<< "$last_err" | head -n1 | cut -c1-150)"
            fi
        fi
        [[ -z "$last_ok" ]] && warn "No backup finished in the last 30 days."
    fi

    # index the Backups page builds of what's at the destination
    local index="/home/$U_CONTEXT/available_backups.json"
    if [[ -n "$dest" && -f "$index" ]]; then
        local idx_err count newest
        idx_err=$(jq -r 'if type == "object" then .error // empty else empty end' "$index" 2>/dev/null)
        if [[ -n "$idx_err" ]]; then
            row "Backups at destination" "unknown ($idx_err)"
        else
            count=$(jq 'length' "$index" 2>/dev/null)
            newest=$(jq -r '.[].backup_file' "$index" 2>/dev/null | grep -oE '[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}' | sort | tail -n1)
            row "Backups at destination" "${count:-0}, newest ${newest:-unknown} (as of $(date -r "$index" '+%Y-%m-%d %H:%M'))"
        fi
    fi

    user_backup_archives
}

# full account archives an admin made with 'opencli user-backup', plus the server's config backup
user_backup_archives() {
    # 'opencli backup' saves panel config for all accounts (domains, dns, settings), not website files or databases
    local runs="/var/log/openpanel/admin/system-backup-runs.jsonl" last_run
    last_run=$(grep '"action":"backup"' "$runs" 2>/dev/null | tail -n1)
    if [[ -n "$last_run" ]]; then
        row "Server config backup" "$(jq -r '"\(.timestamp) \(.status)"' <<< "$last_run" 2>/dev/null)"
        [[ "$(jq -r .status <<< "$last_run" 2>/dev/null)" == failed ]] && warn "Last server config backup failed: $(jq -r .detail <<< "$last_run")"
    else
        row "Server config backup" "never ran"
    fi

    local dir="/home/$U_CONTEXT/docker-data/volumes/${U_CONTEXT}_html_data/_data/_backups" files
    files=$(ls -1t "$dir"/"${U_NAME}"_*.tar.gz 2>/dev/null)
    if [[ -n "$files" ]]; then
        local n newest size
        n=$(grep -c . <<< "$files"); newest=$(head -n1 <<< "$files")
        size=$(du -ck $files 2>/dev/null | tail -n1 | cut -f1)
        row "Full account archives" "$n in /var/www/html/_backups, $(human_kb "$size"), newest $(basename "$newest")"
    else
        row "Full account archives" "none"
    fi
}


section_domains() {
    section "DOMAINS"
    local rows
    rows="$U_DOMAIN_ROWS"
    need_du
    if [[ -z "$rows" ]]; then
        row "Domains" "none"
        return
    fi

    local html="/home/$U_CONTEXT/docker-data/volumes/${U_CONTEXT}_html_data/_data"
    local all; all=$(cut -f1 <<< "$rows")
    local n_total n_sub=0 n_susp=0 n_redir=0
    n_total=$(grep -c . <<< "$all")

    printf '  %s%-36s %-6s %-10s %-11s %s%s\n' "$C_DIM" "Domain" "Type" "Size" "Status" "Docroot / redirect" "$C_RESET"
    local domain docroot php conf type size status extra host_root d comment suspended
    while IFS=$'\t' read -r domain docroot php; do
        conf="/etc/openpanel/caddy/domains/${domain}.conf"

        # subdomain when another of the user's domains is its parent, same as the panel's Categorize
        type="main"
        while read -r d; do
            [[ "$d" != "$domain" && "$domain" == *".$d" ]] && { type="sub"; ((n_sub++)); break; }
        done <<< "$all"

        host_root="$html${docroot#/var/www/html}"
        size="-"
        if [[ -d "$host_root" ]]; then
            # top level docroots are in the shared du, deeper ones get their own
            local kb; kb=$(du_of "$host_root")
            [[ -n "$kb" ]] || kb=$(timeout 30 du -sk "$host_root" 2>/dev/null | cut -f1)
            size=$(human_kb "$kb")
        fi

        # same rules as the domains list: the last reverse_proxy/file_server line decides, file_server means suspended
        status="active" comment="" extra=""
        if [[ ! -f "$conf" ]]; then
            status="no config"
        else
            suspended=$(awk '/^[[:space:]]*reverse_proxy/ { s = 0 } /^[[:space:]]*file_server/ { s = 1 } END { print s + 0 }' "$conf")
            if [[ "$suspended" == 1 ]]; then
                status="suspended"
                comment=$(head -n1 "$conf" | grep '^# comment:' | cut -d: -f2- | sed 's/^ *//')
                ((n_susp++))
            fi
            extra=$(grep -m1 'redir ' "$conf" | awk '{ print $2 }')
            [[ -n "$extra" ]] && { extra="-> $extra"; ((n_redir++)); }
        fi
        if [[ -z "$extra" ]]; then
            [[ "$docroot" == "/var/www/html/$domain" ]] && extra="default" || extra="$docroot"
        fi
        printf '  %-36s %-6s %-10s %-11s %s\n' "$domain" "$type" "$size" "$status" "$extra"

        [[ ! -f "$conf" ]] && err "$domain has no Caddy config ($conf), it doesn't load."
        [[ -d "$host_root" ]] || warn "$domain docroot $docroot doesn't exist."
        [[ "$status" == suspended ]] && warn "$domain is suspended${comment:+: $comment}."
    done <<< "$rows"

    echo
    row "Total" "$n_total ($((n_total - n_sub)) main, $n_sub subdomains)"
    row "Redirects" "$n_redir"
    row "Suspended" "$n_susp"
}


readonly DNS_RESOLVER="1.1.1.1"

# one public lookup, quotes of split TXT chunks joined back
pdig() {
    dig +short +time=2 +tries=1 "@$DNS_RESOLVER" "$2" "$1" 2>/dev/null | grep -v '^;' | sed -E 's/" "//g; s/^"//; s/"$//'
}

# all lookups for one domain in KEY=value lines, run in the background per domain
dns_lookup() {
    local d="$1" out="$2"
    # independent lookups at the same time, only NS has to wait for the zone apex
    (
        # the zone that answers for this name is the SOA owner, which is the parent zone for names that aren't delegated
        local apex
        apex=$(dig +noall +answer +authority +time=2 +tries=1 "@$DNS_RESOLVER" SOA "$d" 2>/dev/null | awk '$4 == "SOA" { print $1; exit }' | sed 's/\.$//')
        echo "APEX=$apex"
        echo "NS=$( [[ -n "$apex" ]] && pdig "$apex" NS | sed 's/\.$//' | sort | paste -sd' ')"
    ) > "$out.1" &
    (
        local full; full=$(dig +time=2 +tries=1 "@$DNS_RESOLVER" A "$d" 2>/dev/null)
        echo "STATUS=$(grep -oP 'status: \K\w+' <<< "$full")"
        echo "A=$(awk '$4 == "A" { print $5 }' <<< "$full" | paste -sd' ')"
    ) > "$out.2" &
    echo "SPF=$(pdig "$d" TXT | grep -i '^v=spf1' | head -n1)" > "$out.3" &
    echo "DKIM=$(pdig "mail._domainkey.$d" TXT | head -n1)" > "$out.4" &
    echo "DMARC=$(pdig "_dmarc.$d" TXT | grep -i '^v=dmarc1' | head -n1)" > "$out.5" &
    wait
    cat "$out".[1-5] > "$out"
    rm -f "$out".[1-5]
}

section_dns() {
    section "DNS"

    need_nameservers
    local ns_conf=("${NS_CONF[@]}") n
    if (( ${#ns_conf[@]} )); then
        row "Nameservers" "$(printf '%s ' "${ns_conf[@]}")"
        local ns
        for ns in "${NS_BROKEN[@]}"; do
            warn "Nameserver $ns doesn't resolve, domains using it won't work."
        done
    else
        row "Nameservers" "not set"
        warn "No nameservers are set (ns1/ns2), new zones get no NS records."
    fi

    local ip
    ip=$(jq -r '.ip // empty' "$USERS_CORE_DIR/$U_NAME/ip.json" 2>/dev/null)
    [[ -n "$ip" ]] || ip=$(server_ip)
    row "Expected IP" "$ip"
    local dns_state
    need_root_ps
    dns_state=$(root_state openpanel_dns)
    row "DNS server" "${dns_state:-not installed}"
    [[ "$dns_state" == running ]] || err "DNS server container openpanel_dns isn't running, zones on this server aren't served."

    local domains
    domains="$U_DOMAINS"
    if [[ -z "$domains" ]]; then
        row "Zones" "none, no domains"
        return
    fi

    local tmp d; tmp=$(mktemp -d /tmp/user-report-dns.XXXXXX)
    while read -r d; do dns_lookup "$d" "$tmp/$d" & done <<< "$domains"
    wait

    printf '\n  %s%-36s %-8s %-8s %-10s %-10s %-9s %-9s %s%s\n' "$C_DIM" "Domain" "Zone" "Records" "NS" "Points to" "SPF" "DKIM" "DMARC" "$C_RESET"
    local zone records zstate check live_ns live_a spf dkim dmarc ns_state a_state spf_s dkim_s dmarc_s expected dyn=()
    local bad_mail=() not_ours=() not_here=()
    while read -r d; do
        zone="/etc/bind/zones/$d.zone"
        records="-" zstate="missing"
        if [[ -f "$zone" ]]; then
            records=$(grep -cE '^[^;$]*[[:space:]]IN[[:space:]]' "$zone")
            zstate="ok"
            if [[ "$dns_state" == running ]]; then
                check=$(timeout 10 podman exec openpanel_dns named-checkzone "$d" "$zone" 2>&1) || {
                    zstate="invalid"
                    err "Zone for $d has a syntax error: $(grep -v '^zone' <<< "$check" | head -n1 | cut -c1-120)"
                }
            fi
            # dynamic dns entries carry a webcall token comment
            while read -r line; do
                [[ -n "$line" ]] && dyn+=("$(awk '{ print $1 "." d " " $4 " " $5 }' d="$d" <<< "$line") (updated $(grep -oP 'updated=\K\S+' <<< "$line"))")
            done < <(grep '; webcall=' "$zone")
        else
            # subdomains usually live in the parent's zone
            local parent=""
            while read -r p; do [[ "$d" == *".$p" ]] && parent="$p"; done <<< "$domains"
            if [[ -n "$parent" ]]; then
                zstate="parent"
            else
                warn "$d has no DNS zone on this server."
            fi
        fi

        live_ns=$(grep -oP '^NS=\K.*' "$tmp/$d") live_a=$(grep -oP '^A=\K.*' "$tmp/$d")
        local status apex
        status=$(grep -oP '^STATUS=\K.*' "$tmp/$d") apex=$(grep -oP '^APEX=\K.*' "$tmp/$d")
        spf=$(grep -oP '^SPF=\K.*' "$tmp/$d") dkim=$(grep -oP '^DKIM=\K.*' "$tmp/$d") dmarc=$(grep -oP '^DMARC=\K.*' "$tmp/$d")

        # our NS if any configured nameserver is among the live ones
        ns_state="other"
        if [[ "$status" == NXDOMAIN || ( -z "$apex" && -z "$live_a" ) ]]; then
            ns_state="none"
        else
            for n in "${ns_conf[@]}"; do [[ " $live_ns " == *" ${n%.} "* ]] && ns_state="ours"; done
        fi
        if [[ -z "$live_a" ]]; then a_state="nothing"
        elif [[ " $live_a " == *" $ip "* ]]; then a_state="this"
        else a_state="${live_a%% *}"
        fi

        # same rules as the Email Deliverability page
        spf_s="missing"; [[ -n "$spf" ]] && { [[ "$spf" == *"ip4:$ip"* ]] && spf_s="ok" || spf_s="wrong_ip"; }
        expected=$(grep -o '"[^"]*"' "/usr/local/mail/openmail/docker-data/dms/config/opendkim/keys/$d/mail.txt" 2>/dev/null | tr -d '"\n')
        dkim_s="missing"; [[ -n "$dkim" ]] && { [[ -n "$expected" && "${dkim// /}" == "${expected// /}" ]] && dkim_s="ok" || dkim_s="mismatch"; }
        dmarc_s="missing"; [[ -n "$dmarc" ]] && dmarc_s="ok"

        printf '  %-36s %-8s %-8s %-10s %-10s %-9s %-9s %s\n' "$d" "$zstate" "$records" "$ns_state" "$a_state" "$spf_s" "$dkim_s" "$dmarc_s"

        case "$ns_state" in
            none) warn "$d doesn't exist in public DNS, it isn't registered or has no nameservers." ;;
            other) not_ours+=("${apex:-$d}") ;;
        esac
        [[ "$a_state" != this && "$a_state" != nothing ]] && not_here+=("$d ($live_a)")
        [[ "$spf_s$dkim_s$dmarc_s" != okokok ]] && bad_mail+=("$d")
    done <<< "$domains"
    rm -rf "$tmp"

    echo
    # grouped by the zone that really answers, e.g. "14 in openpanel.org"
    if (( ${#not_ours[@]} )); then
        info "$(printf '%s\n' "${not_ours[@]}" | sort | uniq -c | awk '{ printf "%s%d in zone %s", (NR > 1 ? ", " : ""), $1, $2 }') answered by other nameservers, edits to these zones here have no effect."
    fi
    if (( ${#not_here[@]} )); then
        warn "Pointing to another IP, websites aren't served from this server: $(printf '%s, ' "${not_here[@]}" | sed 's/, $//')."
    fi
    if (( ${#bad_mail[@]} )); then
        if (( ${#bad_mail[@]} > 3 )); then
            warn "${#bad_mail[@]} domains have SPF/DKIM/DMARC problems, emails from them may land in spam."
        else
            warn "SPF/DKIM/DMARC problems on ${bad_mail[*]}, emails from them may land in spam."
        fi
    fi
    if (( ${#dyn[@]} )); then
        local label="Dynamic DNS" e
        for e in "${dyn[@]}"; do row "$label" "$e"; label=""; done
    else
        row "Dynamic DNS" "none"
    fi
    echo "  ${C_DIM}Live records checked through $DNS_RESOLVER.${C_RESET}"
}


readonly ACME_DIR="/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory"

# caddy's recent tls/acme errors as "domain<TAB>error", only read when a domain has a problem since caddy's log is big
TLS_ERRORS="" TLS_ERRORS_LOADED=0
job_tls() {
    local raw
    # newest 30k lines read backwards, scanning a whole day of a busy caddy log takes seconds
    raw=$(timeout 60 journalctl CONTAINER_NAME=caddy -r -n 30000 -o cat 2>/dev/null | grep -F '"level":"error"' | grep -E 'tls|acme' | tac)
    [[ -n "$raw" ]] || raw=$(timeout 60 podman logs --tail 30000 caddy 2>&1 | grep -F '"level":"error"' | grep -E 'tls|acme')
    jq -Rr 'fromjson? | [(.identifier // .server_name // ""), (.error // .msg // "")] | @tsv' <<< "$raw"
}
tls_errors_load() {
    (( TLS_ERRORS_LOADED )) && return
    TLS_ERRORS=$(gfetch tls)
    TLS_ERRORS_LOADED=1
}

tls_error_for() {
    awk -F'\t' -v d="$1" '$1 == d || $1 == "www." d { r = $2 } END { print r }' <<< "$TLS_ERRORS" | cut -c1-200
}

section_ssl() {
    section "SSL"
    local domains
    domains="$U_DOMAINS"
    if [[ -z "$domains" ]]; then
        row "Certificates" "none, no domains"
        return
    fi

    local now; now=$(date +%s)
    printf '  %s%-36s %-8s %-22s %-12s %s%s\n' "$C_DIM" "Domain" "Type" "Issuer" "Expires" "Days left" "$C_RESET"
    local d conf type cert issuer end end_secs days reason n_ok=0
    while read -r d; do
        conf="/etc/openpanel/caddy/domains/${d}.conf"
        cert="" type="none"
        # same detection as 'opencli domains-ssl'
        if grep -q "fullchain.pem" "$conf" 2>/dev/null; then
            type="custom" cert="/etc/openpanel/caddy/ssl/custom/${d}/fullchain.pem"
        elif grep -q "on_demand" "$conf" 2>/dev/null; then
            type="auto" cert="$ACME_DIR/${d}/${d}.crt"
            # no Let's Encrypt one yet, caddy serves its own self-signed
            [[ -f "$cert" ]] || { [[ -f "/etc/openpanel/caddy/ssl/local/${d}/${d}.crt" ]] && type="self" cert="/etc/openpanel/caddy/ssl/local/${d}/${d}.crt"; }
        fi

        reason=""
        if [[ -z "$cert" || ! -f "$cert" ]]; then
            [[ "$type" == auto ]] && { tls_errors_load; reason=$(tls_error_for "$d"); }
            printf '  %-36s %-8s %-22s %-12s %s\n' "$d" "$type" "-" "-" "-"
            if [[ "$type" == auto ]]; then
                err "$d has no SSL certificate yet, AutoSSL hasn't issued one${reason:+: $reason}."
            else
                err "No SSL certificate is configured for $d."
            fi
            continue
        fi

        issuer=$(openssl x509 -issuer -noout -in "$cert" 2>/dev/null | grep -oP '(O = |O=)\K[^,/]+' | head -n1)
        end=$(openssl x509 -enddate -noout -in "$cert" 2>/dev/null | cut -d= -f2)
        end_secs=$(date -d "$end" +%s 2>/dev/null || echo 0)
        days=$(( (end_secs - now) / 86400 ))
        printf '  %-36s %-8s %-22s %-12s %s\n' "$d" "$type" "${issuer:0:22}" "$(date -d "@$end_secs" +%Y-%m-%d)" "$days"

        # same thresholds as the SSL page
        if (( days <= 14 )) || [[ "$type" == self ]]; then
            [[ "$type" != custom ]] && { tls_errors_load; reason=$(tls_error_for "$d"); }
        fi
        if (( end_secs < now )); then
            err "SSL certificate for $d expired on $(date -d "@$end_secs" +%Y-%m-%d)${reason:+, renewal error: $reason}."
        elif (( days <= 3 )); then
            err "SSL certificate for $d expires in $days days${reason:+, renewal error: $reason}."
        elif (( days <= 14 )); then
            warn "SSL certificate for $d expires in $days days$( [[ $type == auto ]] && echo ", AutoSSL should have renewed it already")${reason:+: $reason}."
        elif [[ "$type" == self ]]; then
            warn "$d uses a self-signed certificate, browsers show a security warning${reason:+. AutoSSL error: $reason}."
        else
            ((n_ok++))
        fi
    done <<< "$domains"
    echo
    row "Valid" "$n_ok of $(grep -c . <<< "$domains")"
}


readonly WAF_LOG_DIR="/var/log/caddy/coraza_waf"

# site type -> waf profile key and name, same catalog as the panel's WAF page
waf_profile_for_type() {
    case "${1,,}" in
        wordpress) echo "wordpress WordPress" ;;
        drupal) echo "drupal Drupal" ;;
        nextcloud) echo "nextcloud Nextcloud" ;;
        dokuwiki) echo "dokuwiki DokuWiki" ;;
        phpbb) echo "phpbb phpBB" ;;
    esac
}

section_waf() {
    section "WAF"

    if [[ -f "/home/$U_CONTEXT/waf.disabled" ]]; then
        row "New domains" "WAF off"
        info "WAF is off by default for new domains on this account."
    else
        row "New domains" "WAF on"
    fi

    local domains
    domains="$U_DOMAINS"
    if [[ -z "$domains" ]]; then
        row "Domains" "none"
        return
    fi

    # apps found per domain, for recommending profiles
    local -A detected
    local dom typ prof
    while IFS=$'\t' read -r dom typ; do
        prof=$(waf_profile_for_type "$typ")
        [[ -n "$prof" ]] && detected[$dom]+="$prof"$'\n'
    done < <(awk -F'\t' '{ print $5 "\t" $2 }' <<< "$U_SITE_ROWS")

    local since=$(( $(date +%s) - 86400 ))

    printf '  %s%-36s %-10s %-14s %-22s %-10s %s%s\n' "$C_DIM" "Domain" "WAF" "Level" "Profiles" "Matched" "Blocked (24h)" "$C_RESET"
    local d conf status level profiles par inb checks blocks off=() monitor=() key name all_logs=()
    while read -r d; do
        conf="/etc/openpanel/caddy/domains/${d}.conf"
        if [[ ! -f "$conf" ]]; then
            printf '  %-36s %-10s\n' "$d" "no config"
            continue
        fi
        status=$(grep -oP '^\s*SecRuleEngine\s+\K(On|Off|DetectionOnly)' "$conf" | head -n1)
        status="${status:-Unknown}"

        # same level names as the WAF page, no SecAction line means standard
        level="standard"
        local lvl_line; lvl_line=$(grep -m1 'blocking_paranoia_level=' "$conf")
        if [[ -n "$lvl_line" ]]; then
            par=$(grep -oP 'blocking_paranoia_level=\K\d+' <<< "$lvl_line") inb=$(grep -oP 'inbound_anomaly_score_threshold=\K\d+' <<< "$lvl_line")
            case "$par/$inb" in
                1/10) level="compatibility" ;; 1/5) level="standard" ;; 2/5) level="strict" ;; *) level="custom" ;;
            esac
        fi
        profiles=$(grep -E '^\s*Include' "$conf" | grep -oP '/\K[a-z0-9]+(?=-rule-exclusions-plugin/plugins/)' | sort -u | paste -sd, -)

        checks=0 blocks=0
        if [[ -s "$WAF_LOG_DIR/$d.log" ]]; then
            all_logs+=("$WAF_LOG_DIR/$d.log")
            read -r checks blocks < <(jq -rn --argjson s "$since" '[inputs | select((.transaction.unix_timestamp // 0) / 1e9 >= $s)] | "\(length) \(map(select(.transaction.is_interrupted)) | length)"' "$WAF_LOG_DIR/$d.log" 2>/dev/null)
        fi
        printf '  %-36s %-10s %-14s %-22s %-10s %s\n' "$d" "$status" "$level" "${profiles:--}" "${checks:-0}" "${blocks:-0}"

        case "$status" in
            Off) off+=("$d") ;;
            DetectionOnly) monitor+=("$d") ;;
        esac
        # recommend profiles for apps found on the domain that aren't turned on
        while read -r key name; do
            [[ -n "$key" ]] || continue
            [[ ",$profiles," == *",$key,"* ]] || info "$name found on $d, turn on the $name WAF profile to avoid false blocks."
        done < <(sort -u <<< "${detected[$d]}")
    done <<< "$domains"

    # same wording as the WAF page toast
    if (( ${#off[@]} == 1 )); then
        warn "WAF is disabled for ${off[0]}."
    elif (( ${#off[@]} > 1 )); then
        warn "WAF is disabled for ${#off[@]} domains: ${off[*]}."
    fi
    (( ${#monitor[@]} )) && info "WAF only monitors ${monitor[*]}, nothing is blocked there."

    # account-wide top rules and IPs from blocked requests
    if (( ${#all_logs[@]} )); then
        local blocked
        blocked=$(jq -rc --argjson s "$since" 'select((.transaction.unix_timestamp // 0) / 1e9 >= $s and .transaction.is_interrupted)' "${all_logs[@]}" 2>/dev/null)
        if [[ -n "$blocked" ]]; then
            subsection "TOP BLOCKED (24h)"
            echo "  ${C_DIM}Rules:${C_RESET}"
            # skip the 949/959/980 score summary rules, they fire on every block
            jq -r '.messages[]?.error_message // empty' <<< "$blocked" \
                | grep -oP '\[id "\K\d+(?="\] \[rev "[^"]*"\] \[msg "[^"]*")|\[msg "\K[^"]*' | paste - - \
                | grep -vE '^(949|959|980)[0-9]+' | sort | uniq -c | sort -rn | head -n 5 \
                | while read -r n id msg; do printf '  %7s  %-8s %s\n' "$n" "$id" "${msg:0:70}"; done
            echo "  ${C_DIM}IPs:${C_RESET}"
            jq -r '.transaction.client_ip // empty' <<< "$blocked" | sort | uniq -c | sort -rn | head -n 5 \
                | while read -r n ip; do printf '  %7s  %s\n' "$n" "$ip"; done
        fi
    fi
}


# value from the user's .env, quotes stripped
user_env() {
    if (( U_ENV_LOADED )); then
        echo "${UENV[$1]-}"
    else
        grep -m1 "^$1=" "/home/$U_CONTEXT/.env" 2>/dev/null | cut -d= -f2- | sed -E "s/^[\"']//; s/[\"'][[:space:]]*$//"
    fi
}

section_webserver() {
    section "WEBSERVER"
    local ws; ws=$(user_env WEB_SERVER)
    if [[ -z "$ws" ]]; then
        err "WEB_SERVER isn't set in /home/$U_CONTEXT/.env, the account has no web server."
        return
    fi

    local state image
    IFS=$'\t' read -r state image < <(upodman inspect -f '{{.State.Status}}\t{{.ImageName}}' "$ws" 2>/dev/null)
    row "Web server" "$ws${image:+ ($image)}"
    row "Status" "${state:-not created}"
    [[ "$state" == running ]] || err "Web server $ws isn't running, websites are down."

    # varnish sits in front when PROXY_HTTP_PORT is active, same check as 'opencli user-varnish'
    if grep -q "^PROXY_HTTP_PORT=" "/home/$U_CONTEXT/.env" 2>/dev/null; then
        need_ps
        local vstate; vstate=$(awk -F'\t' '$1 == "varnish" { print $2 }' <<< "$U_PS")
        row "Varnish" "on (${vstate:-not created})"
        [[ "$vstate" == running ]] || err "Varnish is on but not running, websites are down."
    else
        row "Varnish" "off"
    fi

    # same config test as the panel's webserver page
    if [[ "$state" == running ]]; then
        local out
        case "$ws" in
            nginx|openresty) out=$(upodman exec "$ws" nginx -t 2>&1)
                grep -q successful <<< "$out" && row "Config test" "ok" || { row "Config test" "failed"; err "$ws config test failed: $(grep -m1 -iE 'emerg|error' <<< "$out" | cut -c1-150)"; } ;;
            apache) out=$(upodman exec "$ws" apachectl configtest 2>&1)
                grep -q 'Syntax OK' <<< "$out" && row "Config test" "ok" || { row "Config test" "failed"; err "apache config test failed: $(grep -m1 -v '^AH00558' <<< "$out" | cut -c1-150)"; } ;;
            *) row "Config test" "not available for $ws" ;;
        esac
    fi

    subsection "VHOSTS"
    local vdir="/home/$U_CONTEXT/docker-data/volumes/${U_CONTEXT}_webserver_data/_data"
    local html="/home/$U_CONTEXT/docker-data/volumes/${U_CONTEXT}_html_data/_data"
    local domains d vhost roots r n_ok=0 missing=0
    domains="$U_DOMAINS"
    while read -r d; do
        [[ -n "$d" ]] || continue
        vhost="$vdir/$d.conf"
        if [[ ! -f "$vhost" ]]; then
            err "$d has no vhost file ($d.conf), the web server doesn't serve it."
            ((missing++))
            continue
        fi
        # the folder the web server really serves, apache DocumentRoot, nginx root, openlitespeed docRoot
        roots=$(grep -oP '^\s*(DocumentRoot|root|docRoot|vhRoot)\s+\K[^;\s]+' "$vhost" | sort -u)
        local bad=0
        while read -r r; do
            [[ "$r" == /var/www/html* ]] || continue
            [[ -d "$html${r#/var/www/html}" ]] || { err "$d vhost serves $r, which doesn't exist, the website shows an error."; bad=1; }
        done <<< "$roots"
        (( bad )) || ((n_ok++))
    done <<< "$domains"
    row "Vhosts" "$n_ok of $(grep -c . <<< "$domains") ok"

    # files nobody uses anymore
    local f extra=()
    for f in "$vdir"/*; do
        [[ -f "$f" ]] || continue
        f="${f##*/}"
        grep -qx "${f%.conf}" <<< "$domains" || extra+=("$f")
    done
    (( ${#extra[@]} )) && row_list "Not for any domain" "${extra[@]}"

    subsection "ERROR LOG (24h)"
    if [[ "$state" != running ]]; then
        row "Log" "web server isn't running"
        return
    fi
    local logs severe errors
    logs=$(upodman logs --since 24h "$ws" 2>&1)
    # apache tags lines [module:level], nginx [level]
    severe=$(grep -E '(\[[a-z_]+:|\[)(crit|alert|emerg)\]' <<< "$logs")
    errors=$(grep -E '(\[[a-z_]+:|\[)error\]' <<< "$logs")
    row "Errors" "$(grep -c . <<< "$errors")"
    row "Critical" "$(grep -c . <<< "$severe")"
    [[ -n "$severe" ]] && warn "$ws logged $(grep -c . <<< "$severe") critical error(s) in 24h, last: $(tail -n1 <<< "$severe" | cut -c1-150)"
    if [[ -n "$errors" ]]; then
        echo "  ${C_DIM}Most common:${C_RESET}"
        # strip timestamps, pids and clients so the same error groups together
        sed -E 's/^\[[^]]*\] //; s/\[pid [^]]*\] //; s/\[client [^]]*\] //; s/^[0-9\/: ]+\[error\] [0-9#]+: \*?[0-9]* ?//' <<< "$errors" \
            | cut -c1-110 | sort | uniq -c | sort -rn | head -n 5 | while read -r n msg; do printf '  %7s  %s\n' "$n" "$msg"; done
    fi
}


readonly PHP_API_FILE="/etc/openpanel/openpanel/php/api_versions.json"

# good/secure/unsupported per version, same classification as the PHP page, read once
declare -A PHP_LEVELS=()
PHP_LEVELS_LOADED=0
need_php_levels() {
    (( PHP_LEVELS_LOADED )) && return
    local v l
    while IFS=$'\t' read -r v l; do
        [[ -n "$v" ]] && PHP_LEVELS[$v]="$l"
    done < <(jq -r 'to_entries[] | [.key, (.value | if (.isLatestVersion or .isFutureVersion or .isNextVersion) then "good" elif .isSecureVersion then "secure" else "unsupported" end)] | @tsv' "$PHP_API_FILE" 2>/dev/null)
    PHP_LEVELS_LOADED=1
}

# versions the API doesn't list count as unsupported
php_level() {
    echo "${PHP_LEVELS[$1]:-unsupported}"
}

# changed/added/removed keys between a default and a user php.ini, as "kind|key|default|user" split by \x1f
# comments, quotes and spacing are ignored, On/1/true and Off/0/false count as the same value
php_ini_diff() {
    awk -v SEP=$'\x1f' '
        function norm(v) {
            sub(/\r$/, "", v); gsub(/^[ \t]+|[ \t]+$/, "", v)
            if (v ~ /^".*"$/) v = substr(v, 2, length(v) - 2)
            l = tolower(v)
            if (l == "on" || l == "1" || l == "true" || l == "yes") return "On"
            if (l == "off" || l == "0" || l == "false" || l == "no" || l == "none") return "Off"
            return v
        }
        { sub(/\r$/, "") }
        /^[ \t]*([;#]|\[|$)/ { next }
        {
            i = index($0, "="); if (!i) next
            k = substr($0, 1, i - 1); gsub(/[ \t]/, "", k)
            v = norm(substr($0, i + 1))
            if (FNR == NR) d[k] = v; else u[k] = v
        }
        END {
            for (k in u) {
                if (!(k in d)) print "added" SEP k SEP "" SEP u[k]
                else if (d[k] != u[k]) print "changed" SEP k SEP d[k] SEP u[k]
            }
            for (k in d) if (!(k in u)) print "removed" SEP k SEP d[k] SEP ""
        }' "$1" "$2" | sort -t$'\x1f' -k2,2
}

section_php() {
    section "PHP"
    need_php_levels
    local ws; ws=$(user_env WEB_SERVER)
    local default; default=$(user_env DEFAULT_PHP_VERSION)
    row "Default version" "${default:-not set}"
    [[ -f "$PHP_API_FILE" ]] || echo "  ${C_DIM}No PHP versions API cache ($PHP_API_FILE), support levels can't be checked.${C_RESET}"

    if [[ "${ws,,}" == *litespeed* ]]; then
        row "Web server" "$ws, PHP runs inside it (lsphp)"
    fi

    local available running
    need_services; need_ps
    available=$(grep -oP '^php-fpm-\K[0-9.]+' <<< "$U_SERVICES" | sort -V)
    running=$(grep -oP '^php-fpm-\K[0-9.]+' <<< "$U_RUNNING" | sort -V)
    row_list "Available" $(echo $available)
    row_list "Running" $(echo ${running:-none})
    [[ -n "$default" && -n "$available" ]] && ! grep -qx "$default" <<< "$available" && err "Default PHP $default isn't in docker-compose.yml, new domains can't use it."

    # outdated versions still installed, update suggestions from the API
    local latest; latest=$(jq -r 'to_entries[] | select(.value.isLatestVersion) | .key' "$PHP_API_FILE" 2>/dev/null | head -n1)
    local old=()
    for v in $available; do [[ "$(php_level "$v")" == unsupported ]] && old+=("$v"); done
    (( ${#old[@]} )) && info "Unsupported PHP versions installed: ${old[*]}${latest:+, latest is $latest}."

    subsection "DOMAINS"
    local vdir="/home/$U_CONTEXT/docker-data/volumes/${U_CONTEXT}_webserver_data/_data" d v lvl
    local -A per_version
    local unsupported=0 missing=()
    printf '  %s%-36s %-9s %s%s\n' "$C_DIM" "Domain" "PHP" "Support" "$C_RESET"
    while read -r d; do
        [[ -n "$d" ]] || continue
        # the version the vhost really sends requests to, same as the PHP page
        v=$(grep -oP 'php-fpm-\K[0-9]+\.[0-9]+(?=:\d+)' "$vdir/$d.conf" 2>/dev/null | head -n1)
        if [[ -z "$v" ]]; then
            printf '  %-36s %-9s %s\n' "$d" "-" "-"
            continue
        fi
        lvl=$(php_level "$v")
        per_version[$v]=$(( ${per_version[$v]:-0} + 1 ))
        [[ "$lvl" == unsupported ]] && ((unsupported++))
        grep -qx "$v" <<< "$available" || missing+=("$d ($v)")
        printf '  %-36s %-9s %s\n' "$d" "$v" "$lvl"
    done <<< "$U_DOMAINS"

    # same thresholds as the PHP page toast
    if (( unsupported >= 5 )); then
        err "$unsupported domains are running an outdated/unsupported PHP version."
    elif (( unsupported > 0 )); then
        warn "$unsupported domain(s) are running an outdated/unsupported PHP version."
    fi
    (( ${#missing[@]} )) && err "PHP version not in docker-compose.yml for: ${missing[*]}, those sites can't run PHP."

    subsection "PHP.INI CHANGES"
    local f diff_out any=0 kind key dv uv
    for v in $available; do
        f="/home/$U_CONTEXT/php.ini/$v.ini"
        [[ -f "$f" && -f "/etc/openpanel/php/ini/$v.ini" ]] || continue
        diff_out=$(php_ini_diff "/etc/openpanel/php/ini/$v.ini" "$f")
        [[ -n "$diff_out" ]] || continue
        any=1
        echo "  ${C_BOLD}$v${C_RESET} ${C_DIM}($(grep -c . <<< "$diff_out") vs /etc/openpanel/php/ini/$v.ini)${C_RESET}"
        while IFS=$'\x1f' read -r kind key dv uv; do
            case "$kind" in
                changed) printf '    %-30s %s -> %s\n' "$key" "${dv:-\"\"}" "${uv:-\"\"}" ;;
                added)   printf '    %-30s %s (added)\n' "$key" "${uv:-\"\"}" ;;
                removed) printf '    %-30s removed, default %s\n' "$key" "${dv:-\"\"}" ;;
            esac
        done <<< "$diff_out"
        # exec/shell_exec off breaks the image's php entrypoint, it can't write the fpm pool
        if grep -qP '^\w+\x1fdisable_functions\x1f[^\x1f]*\x1f.*\b(exec|shell_exec)\b' <<< "$diff_out"; then
            warn "PHP $v php.ini disables exec/shell_exec, the php-fpm-$v container can't start with that."
        fi
    done
    (( any )) || ok "All php.ini files match the defaults."

    [[ "${ws,,}" == *litespeed* ]] && return

    # every running version is checked in parallel, capped at 5s since a busy container can take much longer to start php
    local tmp sock; tmp=$(mktemp -d /tmp/user-report-php.XXXXXX)
    sock=$(podman_user_socket "$U_CONTEXT")
    for v in $running; do
        (
            CONTAINER_HOST="$sock" timeout 5 podman --remote exec "php-fpm-$v" sh -c \
                "php -r 'echo PHP_VERSION;'; echo; echo ---INI; php -c /etc/php/$v/fpm/php.ini -d display_errors=0 -r 'exit(0);' 2>&1 >/dev/null; echo ---MODS; php -m; echo ---END" > "$tmp/$v.exec" 2>/dev/null
        ) &
        ( CONTAINER_HOST="$sock" timeout 5 podman --remote logs --since 6h --tail 3000 "php-fpm-$v" > "$tmp/$v.log" 2>&1 ) &
    done
    wait

    for v in $running; do
        local out logs
        subsection "PHP $v"
        out=$(cat "$tmp/$v.exec" 2>/dev/null)
        if ! grep -q '^---END$' <<< "$out"; then
            row "Version" "unknown, php didn't answer in 5s (container busy or restarting)"
        else
            row "Version" "$(head -n1 <<< "$out")"
            # same check as the php.ini editor
            local ini_err
            ini_err=$(sed -n '/^---INI$/,/^---MODS$/p' <<< "$out" | grep -v '^---' | head -n1)
            if [[ -n "$ini_err" ]]; then
                row "php.ini" "error"
                err "Syntax error in the PHP $v ini file: ${ini_err:0:150}"
            else
                row "php.ini" "ok"
            fi
            local mods=()
            mapfile -t mods < <(sed -n '/^---MODS$/,/^---END$/p' <<< "$out" | sed -n '/^\[PHP Modules\]/,/^$/p' | grep -v '^\[\|^$')
            if grep -qi 'ionCube' <<< "$out"; then row "ionCube Loader" "enabled"; else row "ionCube Loader" "off"; fi
            row_list "Extensions (${#mods[@]})" "${mods[@]}"
        fi

        # fpm warnings worth knowing about, max_children means requests queue up
        local maxch fatal
        logs=$(cat "$tmp/$v.log" 2>/dev/null)
        maxch=$(grep -c 'pm.max_children' <<< "$logs")
        fatal=$(grep -E 'PHP Fatal error|ERROR:|ALERT:' <<< "$logs")
        (( maxch )) && warn "PHP $v hit pm.max_children $maxch time(s) in 6h, visitors had to wait, raise it in PHP options."
        # fpm's real reason is the ALERT line before the generic "failed to post process"
        if grep -q 'FPM initialization failed' <<< "$fatal"; then
            local why; why=$(grep 'ALERT:' <<< "$fatal" | tail -n1)
            [[ -n "$why" ]] || why=$(grep -v 'initialization failed' <<< "$fatal" | tail -n1)
            err "PHP $v can't start: $(sed -E 's/^\[[^]]*\] //' <<< "$why" | cut -c1-120)"
        fi
        row "Errors (6h)" "$(grep -c . <<< "$fatal")"
        [[ -n "$fatal" ]] && sed -E 's/^\[[^]]*\] //' <<< "$fatal" | cut -c1-110 | sort | uniq -c | sort -rn | head -n 3 | while read -r n msg; do printf '  %7s  %s\n' "$n" "$msg"; done
    done
    rm -rf "$tmp"
}


job_latest() { latest_versions; }
readonly LATEST_VERSIONS_CACHE="/tmp/openpanel-report-latest-versions.json"

# latest release per CMS from the same sources as the Sites page update check, cached 6h
latest_versions() {
    if [[ -s "$LATEST_VERSIONS_CACHE" ]] && (( $(date +%s) - $(stat -c %Y "$LATEST_VERSIONS_CACHE") < 21600 )); then
        cat "$LATEST_VERSIONS_CACHE"
        return
    fi
    local tmp; tmp=$(mktemp -d /tmp/user-report-latest.XXXXXX)
    local gh="curl -sf --max-time 10 -H User-Agent:OpenPanel"
    ( $gh https://api.wordpress.org/core/version-check/1.7/ | jq -r '.offers[0].current // empty' > "$tmp/wordpress" ) &
    for pair in joomla:joomla/joomla-cms opencart:opencart/opencart prestashop:PrestaShop/PrestaShop matomo:matomo-org/matomo; do
        ( $gh "https://api.github.com/repos/${pair#*:}/releases/latest" | jq -r '.tag_name // empty' | sed 's/^v//' > "$tmp/${pair%%:*}" ) &
    done
    for pair in drupal:drupal/drupal moodle:moodle/moodle; do
        ( $gh "https://api.github.com/repos/${pair#*:}/tags?per_page=100" | jq -r '.[].name' | sed 's/^v//' | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n1 > "$tmp/${pair%%:*}" ) &
    done
    ( $gh https://download.nextcloud.com/server/releases/ | grep -oP 'nextcloud-\K\d+\.\d+\.\d+(?=\.zip")' | sort -V | tail -n1 > "$tmp/nextcloud" ) &
    (
        local branch
        branch=$($gh https://releases.wikimedia.org/mediawiki/ | grep -oP 'href="\K\d+\.\d+(?=/")' | sort -V | tail -n1)
        [[ -n "$branch" ]] && $gh "https://releases.wikimedia.org/mediawiki/$branch/" | grep -oP 'mediawiki-\K\d+\.\d+\.\d+(?=\.tar\.gz")' | sort -V | tail -n1 > "$tmp/mediawiki"
    ) &
    wait
    local f json="{}"
    for f in "$tmp"/*; do
        [[ -s "$f" ]] && json=$(jq --arg k "${f##*/}" --arg v "$(head -n1 "$f")" '. + {($k): $v}' <<< "$json")
    done
    rm -rf "$tmp"
    [[ "$json" != "{}" ]] && echo "$json" > "$LATEST_VERSIONS_CACHE"
    echo "$json"
}

# a 403 right after our request in the domain's waf log means coraza blocked it, name the rules
waf_block_reason() {
    local site="$1" d="${1%%/*}" last ids
    last=$(tail -n 5 "$WAF_LOG_DIR/$d.log" 2>/dev/null | jq -c --argjson s "$(( $(date +%s) - 60 ))" 'select(.transaction.is_interrupted and (.transaction.unix_timestamp // 0) / 1e9 >= $s)' 2>/dev/null | tail -n1)
    if [[ -n "$last" ]]; then
        ids=$(jq -r '.messages[]?.error_message // empty' <<< "$last" | grep -oP '\[id "\K\d+' | grep -vE '^(949|959|980)' | sort -u | paste -sd, -)
        err "$site is blocked by the WAF for normal visitors (HTTP 403, rules ${ids:-unknown})."
    else
        warn "$site returns HTTP 403 on its homepage."
    fi
}

section_websites() {
    section "WEBSITES"
    local rows
    rows=$(cut -f1-4 <<< "$U_SITE_ROWS" | grep .)
    if [[ -z "$rows" ]]; then
        row "Websites" "none"
        return
    fi

    local latest; latest=$(gfetch latest)

    # every site fetched through the local caddy at once, "code seconds" per site
    local tmp site; tmp=$(mktemp -d /tmp/user-report-sites.XXXXXX)
    local i=0
    while IFS=$'\t' read -r site _; do
        # browser-like headers so the WAF doesn't block the check itself, plain http when https hangs (no certificate yet)
        (
            local hdr=(-A "Mozilla/5.0 (OpenPanel report)" -H "Accept: text/html,*/*;q=0.8" -H "Accept-Language: en")
            # both at once, 5s each, http only counts when https gave nothing
            curl -sk -o /dev/null --max-time 5 "${hdr[@]}" -w '%{http_code} %{time_total}' --resolve "${site%%/*}:443:127.0.0.1" "https://$site/" > "$tmp/$i.https" 2>/dev/null &
            curl -s -o /dev/null --max-time 5 "${hdr[@]}" -w '%{http_code} %{time_total}' --resolve "${site%%/*}:80:127.0.0.1" "http://$site/" > "$tmp/$i.http" 2>/dev/null &
            wait
            r=$(cat "$tmp/$i.https" 2>/dev/null)
            if [[ -z "$r" || "$r" == 000* ]]; then
                r=$(cat "$tmp/$i.http" 2>/dev/null)
                [[ -n "$r" && "$r" != 000* ]] && r="$r http"
            fi
            echo "$r" > "$tmp/$i"
        ) &
        ((i++))
    done <<< "$rows"
    wait

    need_ps
    local running="$U_RUNNING"
    printf '  %s%-40s %-16s %-14s %-12s %s%s\n' "$C_DIM" "Site" "Type" "Version" "Latest" "HTTP" "$C_RESET"
    local typ ver ctr lat code secs http outdated=() down=() i=0
    while IFS=$'\t' read -r site typ ver ctr; do
        [[ "$ver" == NULL ]] && ver=""
        lat=$(jq -r --arg t "${typ,,}" '.[$t] // empty' <<< "$latest")
        local proto
        read -r code secs proto < "$tmp/$i"; ((i++))
        if [[ -z "$code" || "$code" == 000 ]]; then
            http="timeout"
        else
            http="$code $(LC_ALL=C awk -v s="$secs" 'BEGIN { printf (s < 10 ? "%.1fs" : "%.0fs"), s }')${proto:+ (http only)}"
        fi
        printf '  %-40s %-16s %-14s %-12s %s\n' "$site" "$typ" "${ver:0:14}" "${lat:--}" "$http"

        # sites with their own container, like node or python apps
        if [[ -n "$ctr" ]] && ! grep -qix "$ctr" <<< "$running"; then
            err "$site app container ${ctr,,} isn't running."
        fi
        if [[ -n "$lat" && -n "$ver" && "$ver" =~ ^v?[0-9] ]] && [[ "$(printf '%s\n%s\n' "${ver#v}" "$lat" | sort -V | tail -n1)" != "${ver#v}" ]]; then
            outdated+=("$site ($ver -> $lat)")
        fi
        case "$code" in
            ""|000) down+=("$site"); err "$site didn't answer in 10s." ;;
            5*) down+=("$site"); err "$site returns HTTP $code, the website is broken." ;;
            403) waf_block_reason "$site" ;;
            4*) warn "$site returns HTTP $code on its homepage." ;;
        esac
        [[ "$proto" == http ]] && warn "$site only answers over http, https hangs (see SSL)."
    done <<< "$rows"
    rm -rf "$tmp"

    echo
    row "Websites" "$(grep -c . <<< "$rows"), $(( $(grep -c . <<< "$rows") - ${#down[@]} )) answering"
    if (( ${#outdated[@]} )); then
        row "Updates available" "${#outdated[@]}"
        local o; for o in "${outdated[@]}"; do echo "    $o"; done
        warn "${#outdated[@]} website(s) have updates available, outdated apps are the most common way sites get hacked."
    fi
    [[ "$latest" == "{}" ]] && echo "  ${C_DIM}Couldn't fetch latest versions, update check skipped.${C_RESET}"
}


# sql against the user's own mysql/mariadb, --defaults-file so /etc/my.cnf's tcp host isn't used
umysql() {
    timeout 5 mariadb --defaults-file="/home/$U_CONTEXT/my.cnf" --socket="/home/$U_CONTEXT/sockets/mysqld/mysqld.sock" -N -B -e "$1" 2>/dev/null
}

# "on, port 32770" or "off" from a .env port mapping like 127.0.0.1:32770:3306
remote_state() {
    local port; port=$(awk -F: '{ print $(NF - 1) }' <<< "$1")
    [[ "$1" == 127.0.0.1:* ]] && echo "off" || echo "on, port $port"
}

# established tcp connections to a host port, the client ips
remote_conns() {
    [[ "$1" =~ ^[0-9]+$ ]] || return
    ss -Htn state established "( sport = :$1 )" 2>/dev/null | awk '{ sub(/:[0-9]+$/, "", $4); print $4 }' | sort | uniq -c
}

# podman against the user's instance with its own timeout
upodman_t() {
    local t="$1" sock; shift
    sock=$(podman_user_socket "$U_CONTEXT") || return 1
    CONTAINER_HOST="$sock" timeout "$t" podman --remote "$@"
}

# lines between ##NAME and the next ## marker of a batched query output
part() {
    awk -v n="##$1" '$0 == n { f = 1; next } /^##/ { f = 0 } f' <<< "$2"
}

# every mysql query the section needs in one connection
job_mysql_all() {
    local rdbs rusers
    rdbs=$(panel_config mysql_restricted_databases "information_schema performance_schema mysql phpmyadmin sys mariadb.sys" | awk '{ for (i = 1; i <= NF; i++) printf "%s'\''%s'\''", (i > 1 ? "," : ""), $i }')
    rusers=$(panel_config mysql_restricted_usernames "mysql.sys mysql sys mariadb.sys phpmyadmin mysql.session mysql.infoschema root debian-sys-maint healthcheck percona.telemetry" | awk '{ for (i = 1; i <= NF; i++) printf "%s'\''%s'\''", (i > 1 ? "," : ""), $i }')
    umysql "SELECT '##VERSION'; SELECT VERSION();
        SELECT '##DBS';
        SELECT s.schema_name, IFNULL(SUM(t.data_length + t.index_length), 0), IFNULL(SUM(t.data_free), 0), COUNT(t.table_name)
            FROM information_schema.schemata s LEFT JOIN information_schema.tables t ON t.table_schema = s.schema_name
            WHERE s.schema_name NOT IN ($rdbs) GROUP BY s.schema_name ORDER BY s.schema_name;
        SELECT '##USERS';
        SELECT REPLACE(Db, '\\\\_', '_'), GROUP_CONCAT(DISTINCT User SEPARATOR ', ') FROM mysql.db WHERE User NOT LIKE 'mysql.s%' AND User NOT IN ($rusers) GROUP BY Db;
        SELECT '##UCOUNT';
        SELECT COUNT(DISTINCT User) FROM mysql.user WHERE User NOT LIKE 'mysql.s%' AND User NOT IN ($rusers);
        SELECT '##SLOW';
        SELECT ID, USER, IFNULL(DB, '-'), TIME, LEFT(REPLACE(IFNULL(INFO, ''), '\n', ' '), 60) FROM information_schema.processlist
            WHERE COMMAND NOT IN ('Sleep', 'Daemon', 'Binlog Dump') AND INFO IS NOT NULL AND ID != CONNECTION_ID() ORDER BY TIME DESC LIMIT 5;"
}

# every postgres query in one exec
job_pg_all() {
    upodman_t 5 exec postgres psql -U "$(user_env POSTGRES_USER)" -At -F $'\t' \
        -c "SELECT '##VERSION'" -c 'SHOW server_version' \
        -c "SELECT '##DBS'" -c "SELECT datname, pg_database_size(datname), pg_get_userbyid(datdba) FROM pg_database WHERE NOT datistemplate AND datname != 'postgres' ORDER BY 1" \
        -c "SELECT '##ACTIVE'" -c "SELECT pid, usename, datname, EXTRACT(EPOCH FROM now() - query_start)::int, LEFT(query, 60) FROM pg_stat_activity WHERE state = 'active' AND pid != pg_backend_pid() ORDER BY 4 DESC LIMIT 5"
}

job_mongo() {
    upodman_t 5 exec mongodb mongosh --quiet --norc -u "$(user_env MONGODB_ROOT_USER)" -p "$(user_env MONGODB_ROOT_PASSWORD)" --authenticationDatabase admin \
        --eval 'const l = db.adminCommand({listDatabases: 1}); print(db.version()); l.databases.filter(d => !["admin","config","local"].includes(d.name)).forEach(d => print(d.name + "\t" + d.sizeOnDisk))'
}

job_dblog() {
    upodman_t 5 logs --since 24h --tail 3000 "$1" 2>&1
}

section_databases() {
    section "DATABASES"
    need_ps
    local running="$U_RUNNING"

    # mysql, postgres and mongo are asked at the same time, each capped at 5s
    local mtype; mtype=$(user_env MYSQL_TYPE)
    local my_on=0 pg_on=0 mg_on=0
    grep -qx "${mtype:-mysql}" <<< "$running" && [[ -S "/home/$U_CONTEXT/sockets/mysqld/mysqld.sock" ]] && my_on=1
    grep -qx postgres <<< "$running" && pg_on=1
    grep -qx mongodb <<< "$running" && mg_on=1
    (( my_on )) && { uasync mysql_all; uasync dblog "${mtype:-mysql}"; }
    (( pg_on )) && { uasync pg_all; uasync dblog postgres; }
    (( mg_on )) && { uasync mongo; uasync dblog mongodb; }

    local mtype; mtype=$(user_env MYSQL_TYPE)
    local mname="${mtype:-mysql}"
    subsection "${mname^^}"
    local mport; mport=$(user_env MYSQL_PORT)
    if (( ! my_on )); then
        row "Status" "not running (starts when the Databases page is opened)"
    else
        local my; my=$(ufetch mysql_all)
        [[ -n "$my" ]] || warn "${mname} didn't answer in 5s."
        row "Version" "$(part VERSION "$my")"
        row "Host for websites" "localhost (socket) or ${mname}:3306"
        row "Remote access" "$(remote_state "$mport")"
        if [[ "$mport" != 127.0.0.1:* ]]; then
            local p; p=$(awk -F: '{ print $(NF - 1) }' <<< "$mport")
            row "Remote address" "$(server_ip):$p"
            local conns; conns=$(remote_conns "$p")
            row "Remote connections" "$( [[ -n "$conns" ]] && awk '{ printf "%s%s (%d)", (NR > 1 ? ", " : ""), $2, $1 }' <<< "$conns" || echo none)"
        fi

        # same filters as the Databases page, applied in job_mysql_all
        local dbs users
        dbs=$(part DBS "$my")
        users=$(part USERS "$my")
        row "Users" "$(part UCOUNT "$my")"

        if [[ -z "$dbs" ]]; then
            row "Databases" "none"
        else
            printf '  %s%-30s %-10s %-8s %-12s %s%s\n' "$C_DIM" "Database" "Size" "Tables" "Reclaimable" "Users" "$C_RESET"
            local db size free tables who nouser=() frag=()
            while IFS=$'\t' read -r db size free tables; do
                who=$(awk -F'\t' -v d="$db" '$1 == d { print $2 }' <<< "$users")
                printf '  %-30s %-10s %-8s %-12s %s\n' "$db" "$(human_bytes "$size")" "$tables" "$( (( free > 0 )) && human_bytes "$free" || echo -)" "${who:--}"
                [[ -z "$who" ]] && nouser+=("$db")
                # worth an OPTIMIZE when over 50MB and a fifth of the size is free space
                (( free > 52428800 && free * 5 > size )) && frag+=("$db ($(human_bytes "$free"))")
            done <<< "$dbs"
            # same wording as the Databases page toast
            case ${#nouser[@]} in
                0) ;;
                1) warn "Database ${nouser[0]} has no users assigned." ;;
                2) warn "Databases ${nouser[0]}, ${nouser[1]} have no users assigned." ;;
                *) warn "${#nouser[@]} databases have no users assigned." ;;
            esac
            (( ${#frag[@]} )) && info "Optimizing these would free space: ${frag[*]}."
        fi

        local slow
        slow=$(part SLOW "$my")
        row "Running queries" "$(grep -c . <<< "$slow")"
        if [[ -n "$slow" ]]; then
            local id u d t q
            while IFS=$'\t' read -r id u d t q; do
                printf '    %ss  %s@%s  %s\n' "$t" "$u" "$d" "$q"
                (( t >= 60 )) && warn "Query $id on $d has been running for ${t}s."
            done <<< "$slow"
        fi
        mysql_log_errors "${mtype:-mysql}"
    fi

    subsection "POSTGRESQL"
    if (( ! pg_on )); then
        row "Status" "not running"
    else
        local pg; pg=$(ufetch pg_all)
        [[ -n "$pg" ]] || warn "PostgreSQL didn't answer in 5s."
        local pport; pport=$(user_env POSTGRES_PORT)
        row "Version" "$(part VERSION "$pg")"
        row "Host for websites" "postgres:5432"
        row "Remote access" "$(remote_state "$pport")"
        if [[ "$pport" != 127.0.0.1:* ]]; then
            local p; p=$(awk -F: '{ print $(NF - 1) }' <<< "$pport")
            row "Remote address" "$(server_ip):$p"
            local conns; conns=$(remote_conns "$p")
            row "Remote connections" "$( [[ -n "$conns" ]] && awk '{ printf "%s%s (%d)", (NR > 1 ? ", " : ""), $2, $1 }' <<< "$conns" || echo none)"
        fi
        local pdbs
        pdbs=$(part DBS "$pg")
        if [[ -z "$pdbs" ]]; then
            row "Databases" "none"
        else
            printf '  %s%-30s %-10s %s%s\n' "$C_DIM" "Database" "Size" "Owner" "$C_RESET"
            local db size owner
            while IFS=$'\t' read -r db size owner; do printf '  %-30s %-10s %s\n' "$db" "$(human_bytes "$size")" "$owner"; done <<< "$pdbs"
        fi
        local active
        active=$(part ACTIVE "$pg")
        row "Running queries" "$(grep -c . <<< "$active")"
        if [[ -n "$active" ]]; then
            local id u d t q
            while IFS=$'\t' read -r id u d t q; do
                printf '    %ss  %s@%s  %s\n' "$t" "$u" "$d" "$q"
                (( t >= 60 )) && warn "PostgreSQL query $id on $d has been running for ${t}s."
            done <<< "$active"
        fi
        mysql_log_errors postgres
    fi

    subsection "MONGODB"
    if (( ! mg_on )); then
        row "Status" "not running"
    else
        local out; out=$(ufetch mongo)
        if [[ -z "$out" ]]; then
            row "Status" "running, but didn't answer"
            warn "MongoDB didn't answer in 5s with the root login from .env."
        else
            row "Version" "$(head -n1 <<< "$out")"
            row "Host for websites" "mongodb:27017"
            local mdbs; mdbs=$(tail -n +2 <<< "$out")
            if [[ -z "$mdbs" ]]; then
                row "Databases" "none"
            else
                printf '  %s%-30s %s%s\n' "$C_DIM" "Database" "Size" "$C_RESET"
                local db size
                while IFS=$'\t' read -r db size; do printf '  %-30s %s\n' "$db" "$(human_bytes "$size")"; done <<< "$mdbs"
            fi
        fi
        mysql_log_errors mongodb
    fi
}

# error lines of a database container in the last 24h, most common first
mysql_log_errors() {
    local logs errors
    logs=$(ufetch dblog "$1")
    errors=$(grep -iE '\[ERROR\]|FATAL|PANIC|"s":"E"' <<< "$logs")
    row "Log errors (24h)" "$(grep -c . <<< "$errors")"
    [[ -n "$errors" ]] || return
    sed -E 's/^[0-9-]+[ T][0-9:.+Z-]+ +//; s/^[0-9]+ //; s/^[A-Z]{3,4} \[[0-9]+\] //' <<< "$errors" | cut -c1-100 | sort | uniq -c | sort -rn | head -n 3 \
        | while read -r n msg; do printf '  %7s  %s\n' "$n" "$msg"; done
    grep -qiE 'FATAL|PANIC|crash|corrupt' <<< "$errors" && warn "$1 logged fatal errors in the last 24h, check its log."
}


# service log lines matching the same words the Services page flags
job_svclog() {
    # not inside names like bf-error-rate or error_log
    upodman_t 5 logs --since 24h --tail 3000 "$1" 2>&1 | grep -iP '(?<![-_\w])(error|fatal|failed|denied|refused|exception|panic|critical)(?![-_\w])'
}
service_log_errors() {
    ufetch svclog "$1"
}

# what a cache or search service reports about itself, 5s at most
job_cacheinfo() {
    case "$1" in
        redis) upodman_t 5 exec redis redis-cli info ;;
        valkey) upodman_t 5 exec valkey valkey-cli info || upodman_t 5 exec valkey redis-cli info ;;
        memcached) upodman_t 5 exec memcached sh -c 'echo stats | nc -w1 127.0.0.1 11211' ;;
        *) upodman_t 5 exec "$1" curl -s --max-time 4 localhost:9200/_cluster/health ;;
    esac | tr -d '\r'
}

section_cache() {
    section "CACHE & SEARCH"
    local defined running
    need_services; need_ps
    defined="$U_SERVICES"
    running=$(cut -f1-3 <<< "$U_PS")

    # every running service asked at once
    local svc
    for svc in redis valkey memcached elasticsearch opensearch; do
        grep -qx "$svc" <<< "$defined" && grep -qP "^$svc\trunning\t" <<< "$running" && { uasync cacheinfo "$svc"; uasync svclog "$svc"; }
    done

    printf '  %s%-15s %-30s %-22s %s%s\n' "$C_DIM" "Service" "Status" "Host for websites" "Details" "$C_RESET"
    local port state status details errors
    for svc in redis valkey memcached elasticsearch opensearch; do
        case "$svc" in
            redis|valkey) port=6379 ;;
            memcached) port=11211 ;;
            *) port=9200 ;;
        esac
        if ! grep -qx "$svc" <<< "$defined"; then
            continue
        fi
        IFS=$'\t' read -r _ state status < <(awk -F'\t' -v s="$svc" '$1 == s' <<< "$running")
        details=""
        if [[ "$state" != running ]]; then
            printf '  %-15s %-30s %-22s %s\n' "$svc" "${state:-not started}" "$svc:$port" "-"
            [[ -n "$state" && "$state" != created ]] && warn "$svc is $state, apps using it get connection errors."
            continue
        fi
        case "$svc" in
            redis|valkey)
                local inf used max evicted keys
                inf=$(ufetch cacheinfo "$svc")
                used=$(grep -oP '^used_memory_human:\K.*' <<< "$inf") max=$(grep -oP '^maxmemory_human:\K.*' <<< "$inf")
                evicted=$(grep -oP '^evicted_keys:\K.*' <<< "$inf")
                keys=$(grep -oP '^db\d+:keys=\K\d+' <<< "$inf" | paste -sd+ - | bc 2>/dev/null)
                details="${used:-?} used, ${keys:-0} keys"
                [[ -n "$max" && "$max" != 0B ]] && details+=", max $max"
                (( ${evicted:-0} > 0 )) && details+=", $evicted evicted"
                ;;
            memcached)
                local st
                st=$(ufetch cacheinfo memcached)
                details="$(human_bytes "$(grep -oP 'STAT bytes \K\d+' <<< "$st")") of $(human_bytes "$(grep -oP 'STAT limit_maxbytes \K\d+' <<< "$st")"), $(grep -oP 'STAT curr_items \K\d+' <<< "$st") items"
                ;;
            elasticsearch|opensearch)
                local health cstatus
                health=$(ufetch cacheinfo "$svc")
                cstatus=$(jq -r '.status // empty' <<< "$health" 2>/dev/null)
                details="cluster ${cstatus:-unknown}"
                [[ "$cstatus" == red ]] && err "$svc cluster is red, some indexes are unavailable."
                [[ -z "$cstatus" ]] && warn "$svc didn't answer on port 9200 in 5s."
                ;;
        esac
        printf '  %-15s %-30s %-22s %s\n' "$svc" "${status:0:30}" "$svc:$port" "$details"
        [[ "$status" == *unhealthy* ]] && warn "$svc is unhealthy."

        # same check as the Services page log button
        errors=$(service_log_errors "$svc")
        if [[ -n "$errors" ]]; then
            warn "Service $svc log contains $(grep -c . <<< "$errors") error line(s), last: $(tail -n1 <<< "$errors" | cut -c1-120)"
        fi
    done
    local socks; socks=$(cd "/home/$U_CONTEXT/sockets" 2>/dev/null && ls -1 */*.sock 2>/dev/null | grep -E '^(redis|valkey|memcached)/' | paste -sd' ' -)
    [[ -n "$socks" ]] && row "Sockets" "$socks (in /home/$U_CONTEXT/sockets)"
}


section_emails() {
    section "EMAILS"
    local domains="$U_DOMAINS"
    if [[ -z "$domains" ]]; then
        row "Email" "no domains"
        return
    fi
    # only addresses on this user's domains, the mailserver lists every account on the server
    local dom_re; dom_re="@($(sed 's/\./\\./g' <<< "$domains" | paste -sd'|'))$"

    need_root_ps; need_email_data
    local ms; ms=$(root_state openadmin_mailserver)
    row "Mail server" "${ms:-not installed}"
    if [[ "$ms" != running ]]; then
        [[ ",$(panel_config enabled_modules)," == *",emails,"* ]] && err "Mail server isn't running, no email is sent or received."
        return
    fi
    local web; web=$(gfetch webmail)
    if [[ "$web" =~ ^[0-9.]+$ ]]; then web="http://$web:8080/"; elif [[ -n "$web" && "$web" != *Community* ]]; then web="https://$web/"; fi
    local wstate; wstate=$(awk -F'\t' '$2 == "running" && tolower($1) ~ /roundcube|snappymail|sogo/ { print $1; exit }' <<< "$ROOT_PS")
    row "Webmail" "${web:-unknown}${wstate:+ ($wstate running)}"
    [[ -n "$wstate" ]] || warn "No webmail container is running, users can't open webmail."
    row "Incoming (IMAP/POP3)" "$(hostname -f 2>/dev/null): 993 (IMAP SSL), 995 (POP3 SSL)"
    row "Outgoing (SMTP)" "$(hostname -f 2>/dev/null): 465 (SSL), 587 (STARTTLS)"

    # lines look like: * user@example.com ( 950M / 1G ) [95%], unlimited quota shows as ~
    local list accounts
    list="$EMAIL_LIST"
    accounts=$(sed -nE 's/^\* ([^ ]+) \( ([^ ]+) \/ ([^ ]+) \) \[([0-9]+)%\].*/\1\t\2\t\3\t\4/p' <<< "$list" | DOM_RE="$dom_re" awk -F'\t' '$1 ~ ENVIRON["DOM_RE"]')

    subsection "ACCOUNTS"
    local n=0
    if [[ -z "$accounts" ]]; then
        row "Accounts" "0"
    else
        n=$(grep -c . <<< "$accounts")
        printf '  %s%-40s %-10s %-10s %s%s\n' "$C_DIM" "Address" "Used" "Quota" "Filters" "$C_RESET"
        local addr used quota pct full=() sieve filt base
        base=$(grep -oP '^email_storage_location=\K.*' /etc/openpanel/openadmin/config/admin.ini 2>/dev/null); base="${base:-/var/mail}"
        while IFS=$'\t' read -r addr used quota pct; do
            sieve="${base%/}/${addr#*@}/${addr%@*}/home/.dovecot.sieve"
            # the default "if true { stop; }" placeholder isn't a filter
            filt=$(grep -cE '^\s*(if|elsif)\b' "$sieve" 2>/dev/null)
            grep -qE '^\s*if true \{ stop; \}\s*$' "$sieve" 2>/dev/null && ((filt--))
            printf '  %-40s %-10s %-10s %s\n' "$addr" "$used" "$( [[ "$quota" == "~" ]] && echo unlimited || echo "$quota")" "$(( ${filt:-0} > 0 ? filt : 0 ))"
            # same threshold as the Email Accounts page toast
            [[ "$quota" != "~" ]] && (( pct > 80 )) && full+=("$addr ($pct%)")
        done <<< "$accounts"
        case ${#full[@]} in
            0) ;;
            1) warn "Email ${full[0]% (*} is reaching its quota (${full[0]##*(}." ;;
            *) warn "${#full[@]} emails are reaching their quota: ${full[*]}." ;;
        esac
    fi
    local limit="${PLAN[emails]-}"
    row "Total" "$n of $(limit_label "$limit")"

    subsection "ALIASES & FORWARDERS"
    local aliases
    aliases=$(sed -nE 's/^\* ([^ ]+) (.*)/\1\t\2/p' <<< "$ALIAS_LIST" | DOM_RE="$dom_re" awk -F'\t' '$1 ~ ENVIRON["DOM_RE"]')
    if [[ -z "$aliases" ]]; then
        row "Aliases" "0"
    else
        local src dst
        while IFS=$'\t' read -r src dst; do printf '  %-40s -> %s\n' "$src" "${dst//,/, }"; done <<< "$aliases"
    fi

    # rejects by the hourly limit, logged by the mailserver with the sender address
    local hourly="${PLAN[hourly]-}"
    row "Hourly send limit" "$(limit_label "$hourly")"
    local rejects
    rejects=$(grep -E "$dom_re" <<< "$MAIL_REJECTS" | sort | uniq -c | sort -rn)
    if [[ -n "$rejects" ]]; then
        warn "$(awk '{ s += $1 } END { print s }' <<< "$rejects") email(s) were rejected by the hourly limit in 24h: $(awk '{ printf "%s%s (%d)", (NR > 1 ? ", " : ""), $2, $1 }' <<< "$rejects")."
    fi
}


# next run of a 6 field (seconds first) or @descriptor schedule, in the cron's timezone, empty when it can't tell
cron_next_run() {
    TZ="$2" python3 - "$1" 2>/dev/null <<'PYEOF'
import sys, time, datetime
spec = sys.argv[1].split()
alias = {"@yearly": "0 0 0 1 1 *", "@annually": "0 0 0 1 1 *", "@monthly": "0 0 0 1 * *", "@weekly": "0 0 0 * * 0",
         "@daily": "0 0 0 * * *", "@midnight": "0 0 0 * * *", "@hourly": "0 0 * * * *"}
if spec and spec[0] == "@every":
    print("every " + spec[1]); sys.exit()
if spec and spec[0] in alias:
    spec = alias[spec[0]].split()
if len(spec) != 6:
    sys.exit()
def parse(f, lo, hi):
    out = set()
    for part in f.split(","):
        step = 1
        if "/" in part:
            part, step = part.split("/"); step = int(step)
        if part in ("*", "?"):
            a, b = lo, hi
        elif "-" in part:
            a, b = map(int, part.split("-"))
        else:
            a = b = int(part)
            if step > 1: b = hi
        out.update(range(a, b + 1, step))
    return out
try:
    sec, mins, hrs, dom, mon, dow = (parse(spec[0], 0, 59), parse(spec[1], 0, 59), parse(spec[2], 0, 23),
                                     parse(spec[3], 1, 31), parse(spec[4], 1, 12), {d % 7 for d in parse(spec[5], 0, 7)})
except ValueError:
    sys.exit()
t = datetime.datetime.now().replace(microsecond=0) + datetime.timedelta(seconds=1)
end = t + datetime.timedelta(days=400)
while t < end:
    if t.month in mon and t.day in dom and (t.weekday() + 1) % 7 in dow and t.hour in hrs and t.minute in mins:
        s = min((x for x in sec if x >= t.second), default=None)
        if s is not None:
            print(t.replace(second=s).strftime("%Y-%m-%d %H:%M:%S")); sys.exit()
    t = (t + datetime.timedelta(minutes=1)).replace(second=0)
PYEOF
}

# the cron service's timezone, same rules as the Cron Jobs page: TZ in its environment, else the host's when /etc/localtime is mounted, else UTC
cron_timezone() {
    local compose="/home/$U_CONTEXT/docker-compose.yml" block tz
    block=$(awk '/^ +cron:$/ { f = 1; match($0, /^ +/); lvl = RLENGTH; next } f && /^ +[A-Za-z0-9_.-]+:$/ { match($0, /^ +/); if (RLENGTH <= lvl) exit } f' "$compose" 2>/dev/null)
    tz=$(grep -oP '\bTZ[=:]\s*\K\S+' <<< "$block" | head -n1 | tr -d "\"'")
    if [[ "$tz" =~ ^\$\{([A-Za-z0-9_]+)(:-([^}]*))?\}$ ]]; then
        local v; v=$(user_env "${BASH_REMATCH[1]}")
        tz="${v:-${BASH_REMATCH[3]}}"
    fi
    if [[ -n "$tz" ]]; then echo "$tz"
    elif grep -q '/etc/localtime' <<< "$block"; then timedatectl show -p Timezone --value 2>/dev/null || cat /etc/timezone 2>/dev/null || echo UTC
    else echo UTC
    fi
}

section_crons() {
    section "CRONS"
    local file="/home/$U_CONTEXT/crons.ini"
    local tz; tz=$(cron_timezone)
    row "Timezone" "$tz"
    row "Server time" "$(TZ="$tz" date '+%Y-%m-%d %H:%M:%S %Z')"

    # [job-exec "name"] blocks, a disabled job has every line commented out
    local jobs
    jobs=$(awk '
        function flush() { if (name != "") print name "\t" dis "\t" sched "\t" ctr "\t" cmd; name = ""; sched = ctr = cmd = "" }
        {
            line = $0; d = 0
            if (line ~ /^[ \t]*[#;]/) { sub(/^[ \t]*[#;][ \t]*/, "", line); d = 1 }
            if (match(line, /^\[job-[a-z-]+ +"[^"]*"\]/)) { flush(); name = line; sub(/^\[job-[a-z-]+ +"/, "", name); sub(/"\].*/, "", name); dis = d; next }
            if (name == "") next
            if (line ~ /^[ \t]*schedule[ \t]*=/) { sub(/^[^=]*=[ \t]*/, "", line); sched = line }
            else if (line ~ /^[ \t]*container[ \t]*=/) { sub(/^[^=]*=[ \t]*/, "", line); ctr = line }
            else if (line ~ /^[ \t]*command[ \t]*=/) { sub(/^[^=]*=[ \t]*/, "", line); cmd = line }
        }
        END { flush() }' "$file" 2>/dev/null)

    if [[ -z "$jobs" ]]; then
        row "Jobs" "none"
        return
    fi

    need_ps
    local state; state=$(awk -F'\t' '$1 == "cron" { print $2 }' <<< "$U_PS")
    row "Cron service" "${state:-not created}"
    local logs=""
    [[ -n "$state" ]] && logs=$(upodman logs --since 168h --tail 5000 cron 2>&1)

    printf '\n  %s%-24s %-18s %-14s %-20s %-20s %s%s\n' "$C_DIM" "Job" "Schedule" "Container" "Last run" "Next run" "Result" "$C_RESET"
    local name dis sched ctr cmd last result next n_on=0 bad=()
    while IFS=$'\t' read -r name dis sched ctr cmd; do
        # ofelia logs [Job "name" (id)] Started, then Finished in ..., failed: true|false
        local jlog; jlog=$(grep -F "[Job \"$name\"" <<< "$logs")
        last=$(grep -m1 -oP '^\S+' <<< "$(grep 'Started' <<< "$jlog" | tail -n1)" | cut -c1-19 | tr T ' ')
        result="-"
        if grep -q 'Finished' <<< "$jlog"; then
            grep 'Finished' <<< "$jlog" | tail -n1 | grep -q 'failed: true' && result="failed" || result="ok"
        fi
        if [[ "$dis" == 1 ]]; then
            next="disabled"
        else
            ((n_on++))
            next=$(cron_next_run "$sched" "$tz")
            # same check as the Cron Jobs page, ofelia reads 5 fields seconds first so "0 * * * *" runs every minute, not hourly
            local fields; fields=$(wc -w <<< "$sched")
            if [[ "$sched" != @* && "$fields" == 5 ]]; then
                next=$(cron_next_run "$sched *" "$tz")
                warn "Cron job \"$name\" schedule \"$sched\" has 5 fields, cron reads the first one as seconds, so it doesn't run when a normal 5 field cron would. Add a seconds field first."
            elif [[ "$sched" != @* && "$fields" != 6 ]]; then
                next="invalid"
                err "Cron job \"$name\" schedule \"$sched\" is invalid, it needs 6 fields (seconds first) or an @daily style descriptor."
            fi
        fi
        printf '  %-24s %-18s %-14s %-20s %-20s %s\n' "${name:0:24}" "${sched:0:18}" "${ctr:0:14}" "${last:--}" "${next:--}" "$result"
        [[ "$result" == failed ]] && bad+=("$name")
        if [[ "$dis" != 1 && -n "$ctr" ]] && ! grep -qx "$ctr" <<< "$U_RUNNING"; then
            warn "Cron job \"$name\" runs in $ctr, which isn't running, so it fails."
        fi
    done <<< "$jobs"

    echo
    row "Jobs" "$(grep -c . <<< "$jobs") ($n_on enabled)"
    (( n_on > 0 )) && [[ "$state" != running ]] && err "Cron service isn't running, none of the $n_on enabled job(s) run."
    (( ${#bad[@]} )) && warn "Last run failed for: ${bad[*]}."

    # same check as the Cron Jobs page, last 200 log lines
    local errs; errs=$(tail -n 200 <<< "$logs" | grep -iP '(?<![-_\w])(error|fatal|failed|denied|refused|exception|panic|critical)(?![-_\w])' | grep -v 'failed: false')
    [[ -n "$errs" ]] && warn "$(grep -c . <<< "$errs") cron log entries contain errors, last: $(tail -n1 <<< "$errs" | cut -c1-120)"
}


# size limit like 100M or 1G in bytes
size_to_bytes() {
    LC_ALL=C awk -v s="${1^^}" 'BEGIN { n = s + 0; u = substr(s, length(s)); m = (u == "K") ? 1024 : (u == "M") ? 1048576 : (u == "G") ? 1073741824 : 1; printf "%.0f\n", n * m }'
}

section_stats() {
    section "STATISTICS"

    # 'opencli domains-stats' builds the goaccess pages, from cron
    local sched; sched=$(grep -h "domains-stats" /etc/cron.d/* 2>/dev/null | grep -v '^\s*#' | awk '{ print $1, $2, $3, $4, $5 }' | head -n1)
    row "Stats generated" "$( [[ -n "$sched" ]] && cron_label "$sched" || echo never)"
    if [[ -z "$sched" && ",$(panel_config enabled_modules)," == *",goaccess,"* ]]; then
        warn "No cron runs 'opencli domains-stats', visitor statistics are never updated."
    fi

    local rot limit limit_b
    rot=$(panel_config logrotate_enable yes) limit=$(panel_config logrotate_size_limit 100M)
    limit_b=$(size_to_bytes "$limit")
    if [[ "$rot" == yes && -f /etc/logrotate.d/caddy-logs ]]; then
        row "Log rotation" "at $limit, keeps $(panel_config logrotate_retention 10) files for $(panel_config logrotate_keep_days 30) days"
    elif [[ "$rot" == yes ]]; then
        row "Log rotation" "on in settings, not installed"
        warn "Log rotation is on but /etc/logrotate.d/caddy-logs is missing, run 'opencli server-logrotate'."
    else
        row "Log rotation" "off"
    fi

    local domains="$U_DOMAINS"
    if [[ -z "$domains" ]]; then
        row "Domains" "none"
        return
    fi
    printf '\n  %s%-36s %-11s %-18s %-11s %s%s\n' "$C_DIM" "Domain" "Access log" "Last request" "WAF log" "Stats page" "$C_RESET"
    local d log waf html size wsize last stat total=0 big=()
    while read -r d; do
        log="/var/log/caddy/domlogs/$d/access.log" waf="/var/log/caddy/coraza_waf/$d.log" html="/var/log/caddy/stats/$U_NAME/$d.html"
        size=$(stat -c %s "$log" 2>/dev/null || echo 0) wsize=$(stat -c %s "$waf" 2>/dev/null || echo 0)
        total=$(( total + size + wsize ))
        last="-"; (( size > 0 )) && last=$(date -r "$log" '+%Y-%m-%d %H:%M')
        stat="none"; [[ -f "$html" ]] && stat=$(date -r "$html" '+%Y-%m-%d %H:%M')
        printf '  %-36s %-11s %-18s %-11s %s\n' "$d" "$(human_bytes "$size")" "$last" "$(human_bytes "$wsize")" "$stat"
        # rotation should keep logs under the limit, twice over means it isn't running
        (( limit_b > 0 && (size > limit_b * 2 || wsize > limit_b * 2) )) && big+=("$d")
    done <<< "$domains"
    echo
    row "Total log size" "$(human_bytes "$total")"
    (( ${#big[@]} )) && warn "Logs over twice the $limit rotation limit, rotation isn't working for: ${big[*]}."
}


section_security() {
    section "SECURITY"
    # shellcheck disable=SC1091
    . /usr/local/opencli/lib/redis.sh 2>/dev/null

    subsection "LOGIN"
    if [[ "$U_TWOFA" == 1 ]]; then
        row "Two-factor auth" "on"
    else
        row "Two-factor auth" "off"
        if [[ "$(panel_config twofa_enforce no)" == yes ]]; then
            warn "Two-factor authentication is enforced on this server but not set up for this account."
        else
            info "Two-factor authentication is off, a stolen password is enough to log in."
        fi
    fi
    local pk; pk=$(user_rows "$DB_PASSKEYS"); pk="${pk:-0$'\t'}"
    row "Passkeys" "${pk%%$'\t'*}$( [[ -n "${pk#*$'\t'}" ]] && echo ", last used ${pk#*$'\t'}")"

    # .lastlogin keeps "IP: x - Country: y - Login Time: z", newest last
    local ll="$USERS_CORE_DIR/$U_NAME/.lastlogin"
    if [[ -s "$ll" ]]; then
        row "Last login" "$(tail -n1 "$ll" | sed -E 's/^IP: ([^ ]+) - Country: ([^ ]*) - Login Time: (.*)/\3 from \1 (\2)/')"
        local ips; ips=$(grep -oP '^IP: \K\S+' "$ll" | sort | uniq -c | sort -rn)
        row "Logins kept" "$(grep -c . "$ll"), from $(grep -c . <<< "$ips") IP(s)"
        awk '{ printf "    %-6s %s\n", $1 "x", $2 }' <<< "$ips" | head -n 5
    else
        row "Last login" "never"
    fi

    # live sessions are redis hashes session:<user id>:<token>, all read in one call
    local sess n
    sess=$(redis_cli EVAL "local r = {} for _, k in ipairs(redis.call('KEYS', ARGV[1])) do local v = redis.call('HMGET', k, 'created_at', 'ip_address') r[#r + 1] = (v[1] or '') .. ' ' .. (v[2] or '') end return r" 0 "session:${U_ID}:*" 2>/dev/null | grep .)
    n=$(grep -c . <<< "$sess")
    row "Active sessions" "$n"
    [[ -n "$sess" ]] && awk '{ printf "    %s  %s\n", substr($1, 1, 19), $2 }' <<< "$sess" | tr T ' ' | sort -r | head -n 5

    subsection "ACCESS"
    local deny="/etc/openpanel/caddy/deny/$U_CONTEXT.ips" blocked
    blocked=$(grep -oP 'remote_ip\s+\K[\d./:a-fA-F]+' "$deny" 2>/dev/null)
    row "Blocked IPs" "$(grep -c . <<< "$blocked")"
    [[ -n "$blocked" ]] && row_list "" $blocked

    # domains that only accept traffic through cloudflare
    local cf=() d
    while read -r d; do
        grep -qE '^\s*import[ \t]+cloudflare-only([ \t]|$)' "/etc/openpanel/caddy/domains/$d.conf" 2>/dev/null && cf+=("$d")
    done <<< "$U_DOMAINS"
    row "Cloudflare only" "$( (( ${#cf[@]} )) && echo "${cf[*]}" || echo none)"

    local api_on; api_on=$(panel_config api off)
    row "API" "$( [[ "$api_on" == on ]] && echo "on (server)" || echo "off (server)")"
    local mcp; mcp=$(user_rows "$DB_MCP")
    row "MCP tokens" "$(grep -c . <<< "$mcp")"
    if [[ -n "$mcp" ]]; then
        local name pre mode used exp
        while IFS=$'\t' read -r name pre mode used exp; do
            printf '    %-20s %-12s %-11s last used %s\n' "${name:0:20}" "$pre…" "$mode" "$used"
            [[ -n "$exp" && "$exp" != NULL ]] && [[ "$exp" < "$(date '+%Y-%m-%d %H:%M:%S')" ]] && info "MCP token $name expired on $exp, it can be removed."
        done <<< "$mcp"
    fi

    # shell login on the host, openpanel users normally have no ssh
    local shell; shell=$(getent passwd "$U_CONTEXT" | cut -d: -f7)
    if [[ "$shell" =~ (nologin|false)$ || -z "$shell" ]]; then
        row "Shell access" "off"
    else
        local keys_n; keys_n=$(grep -cE '^(ssh|ecdsa)-' "/home/$U_CONTEXT/.ssh/authorized_keys" 2>/dev/null)
        row "Shell access" "$shell, ${keys_n:-0} SSH key(s)"
        (( ${keys_n:-0} > 0 )) && info "System user $U_CONTEXT has a login shell and SSH keys, it can log in to the server over SSH."
    fi

    # same check as the Activity page, 100+ actions from one ip in one minute
    local bots
    bots=$(awk '{ k = $3 " " substr($1 " " $2, 1, 16); c[k]++ } END { for (k in c) if (c[k] >= 100) { split(k, p, " "); print p[1] } }' "$USERS_CORE_DIR/$U_NAME/activity.log" 2>/dev/null | sort -u)
    if [[ -n "$bots" ]]; then
        err "Suspicious activity from $(paste -sd, <<< "$bots" | sed 's/,/, /g') (100+ actions in one minute)."
    fi
}

section_activity() {
    section "ACTIVITY"
    local log="$USERS_CORE_DIR/$U_NAME/activity.log"
    if ! grep -q . "$log" 2>/dev/null; then
        row "Activity" "none recorded"
        return
    fi
    local today; today=$(date +%Y-%m-%d)
    row "Entries" "$(grep -c . "$log"), $(grep -c "^$today" "$log") today"
    row "First / last" "$(grep -m1 . "$log" | cut -c1-19) / $(grep . "$log" | tail -n1 | cut -c1-19)"
    echo "  ${C_DIM}Last 15:${C_RESET}"
    # "2026-10-02 10:00:00  1.2.3.4 User name action", the user name is the same on every line
    grep . "$log" | tail -n 15 | awk '{ t = $1 " " $2; ip = $3; $1 = $2 = $3 = $4 = $5 = ""; sub(/^ +/, ""); printf "    %s  %-15s %s\n", t, ip, substr($0, 1, 80) }'
}


# ======================================================================
# Report

print_summary() {
    local errors=0 warnings=0 infos=0 line sev sec msg
    for line in "${ISSUES[@]}"; do
        case "${line%%$'\t'*}" in
            error) ((errors++)) ;;
            warning) ((warnings++)) ;;
            info) ((infos++)) ;;
        esac
    done

    section "SUMMARY"
    printf '  %s%d error(s)%s, %s%d warning(s)%s, %s%d info%s\n' "$C_RED" "$errors" "$C_RESET" "$C_YELLOW" "$warnings" "$C_RESET" "$C_CYAN" "$infos" "$C_RESET"
    if (( ${#ISSUES[@]} == 0 )); then
        ok "No problems found."
        return
    fi
    echo
    # errors first, then warnings, then info
    local want
    for want in error warning info; do
        for line in "${ISSUES[@]}"; do
            IFS=$'\t' read -r sev sec msg <<< "$line"
            [[ "$sev" == "$want" ]] || continue
            case "$sev" in
                error) printf '  %s%-7s%s %s: %s\n' "$C_RED" "[ERROR]" "$C_RESET" "$sec" "$msg" ;;
                warning) printf '  %s%-7s%s %s: %s\n' "$C_YELLOW" "[WARN]" "$C_RESET" "$sec" "$msg" ;;
                info) printf '  %s%-7s%s %s: %s\n' "$C_CYAN" "[INFO]" "$C_RESET" "$sec" "$msg" ;;
            esac
        done
    done
    (( errors > 0 )) && any_error=1
}

# live progress on stderr, only when it's a terminal so cron, pipes and files get the plain report
if [[ -t 2 ]]; then
    PROGRESS=1
    P_RED=$'\e[31m' P_YELLOW=$'\e[33m' P_GREEN=$'\e[32m' P_CYAN=$'\e[36m' P_DIM=$'\e[2m' P_BOLD=$'\e[1m' P_RESET=$'\e[0m'
else
    PROGRESS=0 P_RED="" P_YELLOW="" P_GREEN="" P_CYAN="" P_DIM="" P_BOLD="" P_RESET=""
fi

progress() {
    (( PROGRESS )) && printf '\r\e[K%s' "$*" >&2
}

# one line per finished section with its time, counts and the worst thing it found
progress_done() {
    local name="$1" ms="$2" issues="$3"
    local e w n first cols
    e=$(grep -c '^error' "$issues" 2>/dev/null); w=$(grep -c '^warning' "$issues" 2>/dev/null); n=$(grep -c '^info' "$issues" 2>/dev/null)
    first=$( { grep -m1 '^error' "$issues"; grep -m1 '^warning' "$issues"; } 2>/dev/null | head -n1 | cut -f3)
    local found=""
    (( e )) && found+="${P_RED}${e} error$( (( e > 1 )) && echo s)${P_RESET} "
    (( w )) && found+="${P_YELLOW}${w} warning$( (( w > 1 )) && echo s)${P_RESET} "
    (( n )) && found+="${P_CYAN}${n} info${P_RESET} "
    [[ -n "$found" ]] || found="${P_GREEN}ok${P_RESET} "
    cols=$(tput cols 2>/dev/null || echo 100)
    local mark="${P_GREEN}✓${P_RESET}"
    (( e )) && mark="${P_RED}✗${P_RESET}"
    (( !e && w )) && mark="${P_YELLOW}!${P_RESET}"
    printf '\r\e[K  %s %-11s %s%5.1fs%s  %s' "$mark" "$name" "$P_DIM" "$(LC_ALL=C awk -v m="$ms" 'BEGIN { print m / 1000 }')" "$P_RESET" "$found" >&2
    printf '\n' >&2
    # the most serious finding on its own line, cut to the terminal width
    [[ -n "$first" ]] && printf '      %s%s%s\n' "$P_DIM" "${first:0:$(( cols > 20 ? cols - 7 : 13 ))}" "$P_RESET" >&2
}

# waits for the background sections, printing each as it finishes and a status line with what's still running
progress_wait() {
    local tmp="$1"; shift
    local secs=("$@") n=$# done=0 i t0 spin='|/-\' k=0 running
    local -A shown=()
    t0=$(date +%s%N)
    while (( done < n )); do
        for ((i = 0; i < n; i++)); do
            [[ -n "${shown[$i]-}" ]] && continue
            [[ -f "$tmp/$i.done" ]] || continue
            shown[$i]=1; ((done++))
            [[ -s "$tmp/$i.done" ]] && progress_done "${secs[$i]}" "$(<"$tmp/$i.done")" "$tmp/$i.issues"
        done
        (( done < n )) || break
        running=""
        for ((i = 0; i < n; i++)); do [[ -n "${shown[$i]-}" ]] || running+="${secs[$i]} "; done
        progress "  ${P_CYAN}${spin:k++%4:1}${P_RESET} ${done}/${n} done ${P_DIM}$(( ($(date +%s%N) - t0) / 1000000000 ))s, checking: ${running% }${P_RESET}"
        sleep 0.2
    done
    progress ""
}

report_user() {
    if ! load_user "$1"; then
        echo "User $1 not found."
        any_error=1
        return
    fi
    user_data_reset
    need_env
    (( PROGRESS )) && printf '%sChecking %s%s %s(%s)%s\n' "$P_BOLD" "$U_NAME" "$P_RESET" "$P_DIM" "${REPORT_POS:-1 user}" "$P_RESET" >&2
    local t_user; t_user=$(date +%s%N)

    ISSUES=()
    local sections=("${selected_sections[@]}")
    (( ${#sections[@]} )) || sections=("${ALL_SECTIONS[@]}")

    # this user's slow lookups start now, next to the server-wide ones started once at the top
    uasync ps
    wants files || wants domains && uasync du

    # every section runs in the background into its own files and waits only for the data it reads, joined in report order
    local tmp i=0 s line
    tmp=$(mktemp -d /tmp/user-report.XXXXXX)
    for s in "${sections[@]}"; do
        if declare -F "section_$s" >/dev/null; then
            (
                local t; t=$(date +%s%N)
                ISSUE_FILE="$tmp/$i.issues"
                (( JSON_MODE )) && ROW_FILE="$tmp/$i.rows"
                "section_$s" > "$tmp/$i.out" 2>/dev/null
                echo $(( ($(date +%s%N) - t) / 1000000 )) > "$tmp/$i.done"
            ) &
        else
            (( ${#selected_sections[@]} )) && { section "${s^^}"; echo "  ${C_DIM}Not available yet.${C_RESET}"; } > "$tmp/$i.out"
            : > "$tmp/$i.done"
        fi
        ((i++))
    done
    (( PROGRESS )) && progress_wait "$tmp" "${sections[@]}"
    wait
    (( PROGRESS )) && printf '  %sDone in %ss, report below.%s\n\n' "$P_DIM" "$(LC_ALL=C awk -v m="$(( ($(date +%s%N) - t_user) / 1000000 ))" 'BEGIN { printf "%.1f", m / 1000 }')" "$P_RESET" >&2
    for ((i = 0; i < ${#sections[@]}; i++)); do
        [[ -f "$tmp/$i.issues" ]] || continue
        while IFS= read -r line; do ISSUES+=("$line"); done < "$tmp/$i.issues"
    done

    if (( JSON_MODE )); then
        local ms=$(( ($(date +%s%N) - t_user) / 1000000 ))
        if [[ "$target" == "--all" ]]; then
            report_json "$tmp" "$ms" "${sections[@]}" | jq -c . >> "$ALL_JSON_TMP"
        else
            # written next to the final file and moved, so a reader never sees half a report
            local out="/home/$U_CONTEXT/user_report_status.json"
            if report_json "$tmp" "$ms" "${sections[@]}" > "$out.tmp" && mv -f "$out.tmp" "$out"; then
                chmod 644 "$out"
                echo "Report for $U_NAME saved to $out"
            else
                rm -f "$out.tmp"
                echo "Couldn't write $out"
                any_error=1
            fi
        fi
        local e; for e in "${ISSUES[@]}"; do [[ "$e" == error$'\t'* ]] && any_error=1; done
        rm -rf "$tmp"
        return
    fi

    printf '%s################################################################################%s\n' "$C_BOLD" "$C_RESET"
    printf '%s Report for %s, generated %s%s\n' "$C_BOLD" "$U_NAME" "$(date '+%Y-%m-%d %H:%M:%S %Z')" "$C_RESET"
    printf '%s################################################################################%s\n' "$C_BOLD" "$C_RESET"
    print_summary
    for ((i = 0; i < ${#sections[@]}; i++)); do
        [[ -f "$tmp/$i.out" ]] && cat "$tmp/$i.out"
    done
    rm -rf "$tmp"
    echo
}

# one user's report as a json object, built from the files every section left in <tmp>
report_json() {
    local tmp="$1" ms="$2"; shift 2
    local secs=("$@") i parts=()
    for ((i = 0; i < ${#secs[@]}; i++)); do
        [[ -f "$tmp/$i.out" ]] || continue
        # rows with an empty label continue the value above them
        parts+=("$(jq -n --arg key "${secs[$i]}" --rawfile text "$tmp/$i.out" \
            --rawfile rows <(cat "$tmp/$i.rows" 2>/dev/null) --rawfile issues <(cat "$tmp/$i.issues" 2>/dev/null) \
            --arg ms "$(cat "$tmp/$i.done" 2>/dev/null)" '
            def tsv: split("\n") | map(select(length > 0) | split("\t"));
            {
                key: $key,
                title: ($text | capture("== (?<t>[^=]+) ==") | .t // ($key | ascii_upcase)),
                duration_ms: (($ms | tonumber?) // null),
                rows: (reduce ($rows | tsv)[] as $r ([];
                    if $r[1] == "" and length > 0 then .[-1].value += "\n" + ($r[2] // "")
                    else . + [{group: $r[0], label: $r[1], value: ($r[2] // "")}] end)),
                issues: ($issues | tsv | map({severity: .[0], section: .[1], message: (.[2:] | join("\t"))})),
                text: $text
            }')")
    done
    printf '%s\n' "${parts[@]}" | jq -s \
        --arg user "$U_NAME" --arg context "$U_CONTEXT" --arg owner "$U_OWNER" --arg plan "$U_PLAN" \
        --arg generated "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --argjson ms "$ms" --arg suspended "$U_SUSPENDED" '
        (map(.issues[]) ) as $all |
        {
            username: $user, context: $context, owner: $owner, plan: $plan,
            suspended: ($suspended != ""),
            generated_at: $generated, duration_ms: $ms,
            summary: {
                errors: ($all | map(select(.severity == "error")) | length),
                warnings: ($all | map(select(.severity == "warning")) | length),
                info: ($all | map(select(.severity == "info")) | length)
            },
            issues: (($all | map(select(.severity == "error"))) + ($all | map(select(.severity == "warning"))) + ($all | map(select(.severity == "info")))),
            sections: .
        }'
}

load_panel_config
server_ip >/dev/null

RUN_SECTIONS=("${selected_sections[@]}")
(( ${#RUN_SECTIONS[@]} )) || RUN_SECTIONS=("${ALL_SECTIONS[@]}")
wants() { [[ " ${RUN_SECTIONS[*]} " == *" $1 "* ]]; }

# server wide lookups, started once in the background, sections wait for the ones they read
gasync root_ps
wants info && gasync server_facts
wants info || wants files && gasync quota
wants dns && gasync ns
wants ssl && gasync tls
wants websites && gasync latest
if wants emails; then gasync email_list; gasync alias_list; gasync mail_rejects; gasync webmail; fi

if [[ "$target" == "--all" ]]; then
    # everyone's rows in one query per table, sections pick their user's rows from these
    db_prefetch_all
    users=$(cut -f2 <<< "$DB_USERS")
    [[ -n "$users" ]] || { echo "No users."; exit 0; }
    total=$(grep -c . <<< "$users") pos=0
    (( JSON_MODE )) && { ALL_JSON_TMP="$ALL_JSON_FILE.tmp"; : > "$ALL_JSON_TMP"; }
    while read -r u <&3; do
        [[ "$u" == SUSPENDED_* ]] && u="${u##*_}"
        REPORT_POS="$((++pos)) of $total users"
        report_user "$u"
    done 3<<< "$users"
    if (( JSON_MODE )); then
        mv -f "$ALL_JSON_TMP" "$ALL_JSON_FILE" && chmod 644 "$ALL_JSON_FILE"
        echo "Reports for $(grep -c . "$ALL_JSON_FILE") user(s) saved to $ALL_JSON_FILE"
    fi
else
    report_user "$target"
fi

exit "$any_error"
