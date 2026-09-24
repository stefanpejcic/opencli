#!/bin/bash
################################################################################
# Script Name: sentinel.sh
# Description: Check system services, traffic and resource usage, and log/send custom notifications on request.
# Usage: opencli sentinel [--startup] [--report] [--action=<name> --title=<title> --message=<msg>]
# Author: Stefan Pejcic
# Created: 01.11.2023
# Last Modified: 24.09.2026
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
. /usr/local/opencli/lib/requirement.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/podman.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/notifications.sh

# config
readonly CONF_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
readonly INI_FILE="/etc/openpanel/openadmin/config/notifications.ini"
readonly NOTIFICATIONS_PAUSE_FILE="/tmp/openpanel_notifications_paused"

DISPLAY_TIME=$(date +"%Y-%m-%d %H:%M:%S")
readonly DISPLAY_TIME
HOSTNAME=$(hostname)

# lock files
readonly SNAPSHOT_FILE="/var/log/openpanel/admin/sentinel_snapshots.jsonl"
readonly SNAPSHOT_MAX_LINES=8640  # 30d
readonly LOCK_FILE_FOR_DNS_CHECK="/tmp/sentinel.dns"
readonly LOCK_FILE_FOR_OOM_CHECK="/tmp/sentinel.oom"
readonly LOCK_FILE_FOR_DOCKER_PRUNE="/tmp/sentinel.docker"
readonly LOCK_FILE_FOR_SWAP_CLEANUP="/tmp/sentinel.swap"
readonly LOCK_FILE_FOR_REDIS_STUCK="/tmp/sentinel.redis_stuck"
readonly LOCK_FILE_FOR_USER_CONTAINERS="/tmp/sentinel.user_containers"

[ ! -f "$INI_FILE" ] && { echo "Error: OpenAdmin notifications settings file not found: $INI_FILE"; exit 1; }
[ ! -f "$CONF_FILE" ] && { echo "Error: OpenPanel main configuration file not found: $CONF_FILE"; exit 1; }


STATUS=0 PASS=0 WARN=0 FAIL=0

conf_get() { awk -F= "/^${1}=/{print \$2; exit}" "$CONF_FILE" 2>/dev/null; }
ini_get()  { awk -F= "/^${1}=/{print \$2; exit}" "$INI_FILE"  2>/dev/null; }

validate_yes_no() { [[ "$1" == "yes" || "$1" == "no" ]] && echo "$1" || echo "yes"; }
validate_number() { [[ "$1" =~ ^([1-9][0-9]?|100)$ ]] && echo "$1" || echo "$2"; }

EMAIL=$(conf_get email)
EMAIL_ALERT=$([[ -n "$EMAIL" ]] && echo "yes" || echo "no")

WEBHOOK_URL=$(ini_get webhook_url)

REBOOT=$(validate_yes_no "$(ini_get reboot)")
MAIN_DOMAIN_AND_NS=$(validate_yes_no "$(ini_get dns)")
LOGIN=$(validate_yes_no "$(ini_get login)")
SSH_LOGIN=$(validate_yes_no "$(ini_get ssh)")
SERVICES=$(ini_get services); SERVICES="${SERVICES:-admin,podman,mysql,csf,panel}"

LIMIT=$(validate_yes_no "$(ini_get limit)")
ATTACK=$(validate_yes_no "$(ini_get attack)")
MAX_TOTAL_CONN=$(validate_number "$(ini_get max_total_conn)"   5000)
MAX_CONN_PER_IP=$(validate_number "$(ini_get max_conn_per_ip)" 500)

LOAD_THRESHOLD=$(validate_number "$(ini_get load)" 20)
CPU_THRESHOLD=$(validate_number  "$(ini_get cpu)"  90)
RAM_THRESHOLD=$(validate_number  "$(ini_get ram)"  85)
DISK_THRESHOLD=$(validate_number "$(ini_get du)"   85)
SWAP_THRESHOLD=$(validate_number "$(ini_get swap)" 40)

is_unread_message_present() { notification_is_unread "$1"; }

# alerts of a full run are queued here and sent as one email/webhook at the end, see flush_notification_queue
SENTINEL_QUEUE=""

# consecutive over-threshold runs needed before load/cpu/ram alert, so short spikes don't page the admin
readonly STREAK_RUNS=2

# marks unread alerts with this title as read and resolved once the issue is gone, --prefix matches titles with dynamic parts (ips, domains)
resolve_notification() {
  local title="$1" first
  first=$(notification_resolve "$@") || return 0
  title="${title% }"
  echo -e "\e[32m[✔]\e[0m Issue resolved, marked notification as read: $title"

  # the admin got the alert by email/webhook, so tell them it's over too
  title_snoozed "$title" && return 0
  local since="" first_epoch
  first_epoch=$(date -d "$first" +%s 2>/dev/null)
  [[ -n "$first_epoch" ]] && since=" It was first reported at $first, $(format_duration $(( $(date +%s) - first_epoch ))) ago."
  send_notification resolved "Resolved: $title" "Sentinel no longer detects this issue on $HOSTNAME.$since"
}

format_duration() {
  local s=$1
  if (( s >= 86400 )); then echo "$(( s / 86400 ))d $(( s % 86400 / 3600 ))h"
  elif (( s >= 3600 )); then echo "$(( s / 3600 ))h $(( s % 3600 / 60 ))m"
  else echo "$(( s / 60 ))m"
  fi
}

# bumps the streak for a check and succeeds once it has been over threshold STREAK_RUNS runs in a row, sets STREAK
over_threshold_streak() {
  local f="/tmp/sentinel.streak.$1" n
  n=$(cat "$f" 2>/dev/null); [[ "$n" =~ ^[0-9]+$ ]] || n=0
  STREAK=$(( n + 1 ))
  echo "$STREAK" > "$f"
  (( STREAK >= STREAK_RUNS ))
}
clear_streak() { rm -f "/tmp/sentinel.streak.$1"; }

readonly IP_CACHE_FILE="/tmp/public.ipv4"

get_public_ip() {
  if [[ -f "$IP_CACHE_FILE" ]]; then
    local age=$(( $(date +%s) - $(stat -c %Y "$IP_CACHE_FILE") ))
    if (( age < 21600 )); then #6h
      cat "$IP_CACHE_FILE"; return
    fi
  fi
  local ip
  ip=$(curl --silent --max-time 3 -4 "https://ip.openpanel.com" 2>/dev/null \
    || curl --silent --max-time 3 -4 "https://ifconfig.me/ip" 2>/dev/null)
  if [[ -z "$ip" ]]; then
    ip=$(ip -4 addr show scope global | awk '/inet /{split($2,a,"/"); print a[1]; exit}')
  fi
  [[ -n "$ip" ]] && echo "$ip" > "$IP_CACHE_FILE"
  echo "$ip"
}


# notifications_paused checks the pause flag file admins set from OpenAdmin > Settings > Notifications; deletes it once expired, same as the timestamp inside it says
notifications_paused() {
  [[ -f "$NOTIFICATIONS_PAUSE_FILE" ]] || return 1
  local until; until=$(cat "$NOTIFICATIONS_PAUSE_FILE" 2>/dev/null)
  if [[ -z "$until" || ! "$until" =~ ^[0-9]+$ || "$until" -le "$(date +%s)" ]]; then
    rm -f "$NOTIFICATIONS_PAUSE_FILE"
    return 1
  fi
  return 0
}

# title_snoozed checks the per-alert snooze flag an admin sets by clicking "Snooze" on one notification row in OpenAdmin; hashes the title the same way OpenAdmin does so both sides agree on the flag filename
title_snoozed() {
  local title="$1"
  local hash; hash=$(printf '%s' "$title" | md5sum | cut -d' ' -f1)
  local flag="/tmp/openpanel_notification_snooze_${hash}"
  [[ -f "$flag" ]] || return 1
  local until; until=$(cat "$flag" 2>/dev/null)
  if [[ -z "$until" || ! "$until" =~ ^[0-9]+$ || "$until" -le "$(date +%s)" ]]; then
    rm -f "$flag"
    return 1
  fi
  return 0
}

webhook_notification() {
  local title=$1 message=$2
  notifications_paused && return
  [[ -z "$WEBHOOK_URL" ]] && return
  local clean_msg; clean_msg=$(printf '%s' "$message" | sed 's/"/\\"/g')
  clean_msg="${clean_msg//$'\n'/\\n}"
  local payload="{\"text\": \"*${title}*\n${clean_msg}\", \"username\": \"OpenAdmin-$HOSTNAME\", \"content\": \"**${title}**\n${clean_msg}\"}"
  curl -X POST -H "Content-Type: application/json" -d "$payload" --max-time 1 "$WEBHOOK_URL" >/dev/null 2>&1
}

email_notification() {
  local title=$1 message=$2 items=${3:-[]}
  notifications_paused && return
  local token; token=$(tr -dc 'a-zA-Z0-9' </dev/urandom | head -c 64)
  awk -v t="$token" '/^mail_security_token=/{$0="mail_security_token="t} 1' "$CONF_FILE" > "${CONF_FILE}.tmp" && mv "${CONF_FILE}.tmp" "$CONF_FILE"

  local domain; domain=$(opencli domain)
  local cert_path_on_hosts="/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/${domain}/${domain}.crt"
  local key_path_on_hosts="/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/${domain}/${domain}.key"
  local fallback_cert_path="/etc/openpanel/caddy/ssl/custom/${domain}/${domain}.crt"
  local fallback_key_path="/etc/openpanel/caddy/ssl/custom/${domain}/${domain}.key"

  local proto="http"
  if { [ -f "$cert_path_on_hosts" ] && [ -f "$key_path_on_hosts" ]; } || { [ -f "$fallback_cert_path" ] && [ -f "$fallback_key_path" ]; }; then
      proto="https"
  fi

  local -a auth_opt=()
  local admin_ini="/etc/openpanel/openadmin/config/admin.ini"
  if grep -qx 'basic_auth=yes' "$admin_ini" 2>/dev/null; then
    local u p
    u=$(awk -F= '/^basic_auth_username=/{print $2; exit}' "$admin_ini")
    p=$(awk -F= '/^basic_auth_password=/{print $2; exit}' "$admin_ini")
    auth_opt=(--user "${u}:${p}")
  fi

  local admin_port resp
  admin_port=$(awk '/# START HOSTNAME DOMAIN #/{flag=1; next} /# END HOSTNAME DOMAIN #/{flag=0} flag' "/etc/openpanel/caddy/Caddyfile" | grep -oP 'localhost:\K[0-9]+' | head -n 1)
  resp=$(curl -4 --max-time 5 -ksf -X POST "$proto://$domain:$admin_port/send_email" "${auth_opt[@]}" --form-string "transient=$token" --form-string "recipient=$EMAIL" --form-string "subject=$title" --form-string "body=$message" --form-string "notifications=$items" 2>/dev/null)

  case "$resp" in
    *'"error"'*)             echo "Error sending email: $resp" ;;
    *'"sent successfully"'*) echo "Email sent." ;;
  esac
}

# alerts that need the admin: logged as unread and sent by email/webhook, a repeat of an unread one only bumps its count
# usage: write_notification <critical|warning> <category> <title> <message> [details json]
write_notification() {
  local severity="$1" category="$2" title="$3" message="$4" details="${5:-null}"

  # snoozed alerts skip logging and email/webhook entirely -- unlike the global pause, which still logs
  title_snoozed "$title" && { echo "[!] This alert is snoozed: $title"; return; }

  notification_add yes unread "$severity" "$category" sentinel "$title" "$message" "$details" || return
  send_notification "$severity" "$title" "$message"
}

# user actions logged by other opencli commands via --action, only if the admin enabled that action
write_action_notification() {
  local action="$1" title="$2" message="$3"
  title_snoozed "$title" && { echo "[!] This alert is snoozed: $title"; return; }
  [[ -n "$RUN_ACTION_LOCKED" ]] && return
  export RUN_ACTION_LOCKED=1

  ACTION=$(validate_yes_no "$(ini_get "$action")")
  if [[ "$ACTION" == "no" ]]; then
    echo "[!] Notifications are disabled for action: $action"; return
  fi
  notification_add no unread info action "$action" "$title" "$message" "$(jq -nc --arg a "$action" '{action: $a}')"
  send_notification info "$title" "$message"
}

# for things sentinel already fixed on its own: kept on the Notifications page as history, no email/webhook
write_info_notification() {
  local category="$1" title="$2" message="$3"
  title_snoozed "$title" && return
  notification_add no read info "$category" sentinel "$title" "$message"
}

# queued during a full run, sent right away for --startup and --action
send_notification() {
  local severity="$1" title="$2" message="$3"
  local item; item=$(jq -nc --arg s "$severity" --arg t "$title" --arg m "$message" '{severity: $s, title: $t, message: $m}')
  if [[ -n "$SENTINEL_QUEUE" ]]; then
    { flock -x 201; echo "$item" >> "$SENTINEL_QUEUE"; } 201>"$SENTINEL_QUEUE.lock"
    return
  fi
  deliver_notification "$title" "$message" "[$item]"
}

deliver_notification() {
  local subject="$1" body="$2" items="$3"
  [[ "$EMAIL_ALERT" == "yes" ]] && email_notification "$subject" "$body" "$items"
  [[ -n "$WEBHOOK_URL" ]] && webhook_notification "$subject" "$body"
}

# sends everything this run queued as one email and one webhook
flush_notification_queue() {
  [[ -n "$SENTINEL_QUEUE" && -s "$SENTINEL_QUEUE" ]] || { rm -f "$SENTINEL_QUEUE" "$SENTINEL_QUEUE.lock"; return; }
  local items count subject body
  items=$(jq -sc . "$SENTINEL_QUEUE")
  rm -f "$SENTINEL_QUEUE" "$SENTINEL_QUEUE.lock"
  SENTINEL_QUEUE=""

  count=$(jq length <<< "$items")
  if (( count == 1 )); then
    subject=$(jq -r '.[0].title' <<< "$items")
    body=$(jq -r '.[0].message' <<< "$items")
  else
    subject="$count notifications from Sentinel on $HOSTNAME"
    body=$(jq -r 'to_entries | map("\(.key + 1). [\(.value.severity | ascii_upcase)] \(.value.title)\n\(.value.message)") | join("\n\n")' <<< "$items")
  fi
  deliver_notification "$subject" "$body" "$items"
}


ip_in_cidr() {
  local ip=$1 cidr=$2 network mask
  local i1 i2 i3 i4 n1 n2 n3 n4
  IFS=/ read -r network mask <<< "$cidr"
  IFS=. read -r i1 i2 i3 i4 <<< "$ip"
  IFS=. read -r n1 n2 n3 n4 <<< "$network"
  local ipbin=$(( (i1<<24)|(i2<<16)|(i3<<8)|i4 ))
  local netbin=$(( (n1<<24)|(n2<<16)|(n3<<8)|n4 ))
  local maskbin=$(( (0xFFFFFFFF << (32-mask)) & 0xFFFFFFFF ))
  (( (ipbin & maskbin) == (netbin & maskbin) ))
}

is_ip_whitelisted() {
  local ip=$1
  local wl_file="/etc/openpanel/openadmin/ssh_whitelist.conf"
  [[ -f "$wl_file" ]] || return 1
  local e
  while IFS= read -r e; do
    [[ -z "$e" ]] && continue
    if [[ "$e" == */* ]]; then
      ip_in_cidr "$ip" "$e" && return 0
    else
      [[ "$e" == "$ip" ]] && return 0
    fi
  done < "$wl_file"
  return 1
}

# starts/recovers containers for root or a user's podman socket -- handles plain exited containers and ones wedged in a transitional state that plain `podman ps` never surfaces, podman start is tried first, falling back to restart
start_containers_for_socket() {
    local socket="$1"
    local label="$2"
    local results_file="$3"

    if [ ! -S "$socket" ]; then
        echo "no socket at $socket for $label, skipping"
        { flock -x 201; echo "${label}:0" >> "$results_file"; } 201>>"$results_file.lock"
        return
    fi

    local dead
    dead=$(CONTAINER_HOST="unix://$socket" timeout 20 podman ps -a --format '{{.ID}} {{.State}}' 2>/dev/null | awk '$2!="running"{print $1"|"$2}' || true)

    if [ -z "$dead" ]; then
        echo -e "\e[32m[✔]\e[0m $label: no dead containers"
        { flock -x 201; echo "${label}:0" >> "$results_file"; } 201>>"$results_file.lock"
        return
    fi

    local count=0
    local entry id state name
    for entry in $dead; do
        id="${entry%%|*}"
        state="${entry##*|}"
        name=$(CONTAINER_HOST="unix://$socket" timeout 10 podman inspect "$id" --format '{{.Name}}' 2>/dev/null || echo "$id")
        echo "$label: starting $name (was: $state)"
        if CONTAINER_HOST="unix://$socket" timeout 30 podman start "$id" &>/dev/null || CONTAINER_HOST="unix://$socket" timeout 30 podman restart "$id" &>/dev/null; then
            ((count++))
        else
            echo -e "\e[31m[✘]\e[0m $label: FAILED to start $name"
        fi
    done
    { flock -x 201; echo "${label}:${count}" >> "$results_file"; } 201>>"$results_file.lock"
}


# starts per-user containers a currently-enabled feature needs but that podman never saw "die" -- e.g. one never created because the feature was off at provisioning time needs a plain podman-compose up, not a restart
start_conditional_containers_for_user() {
    local user="$1"
    local user_sock="$2"
    local results_file="$3"
    local home="/home/$user"

    local -a needed=()

    # cron if crons.ini has any content
    if [[ -f "$home/crons.ini" && -n "$(tr -d '[:space:]' < "$home/crons.ini" 2>/dev/null)" ]]; then
        needed+=("docker-proxy" "cron")
    fi

    # backup if backup.env for any active remote backup config
    if [[ -f "$home/backup.env" ]] && grep -qE '^(WEBDAV_URL|AWS_S3_BUCKET_NAME|SSH_HOST_NAME|AZURE_STORAGE_ACCOUNT_NAME|DROPBOX_REMOTE_PATH)=' "$home/backup.env"; then
      needed+=("docker-proxy" "backup")
    fi

    # dedupe while preserving order
    if (( ${#needed[@]} )); then
        local -A seen=()
        local -a deduped=()
        for svc in "${needed[@]}"; do
            [[ -n "${seen[$svc]}" ]] && continue
            seen[$svc]=1
            deduped+=("$svc")
        done
        needed=("${deduped[@]}")
    fi

    # https://github.com/stefanpejcic/OpenPanel/issues/1132
    local cron_needed=0 backup_needed=0
    [[ " ${needed[*]} " == *" cron "* ]] && cron_needed=1
    [[ " ${needed[*]} " == *" backup "* ]] && backup_needed=1

    local stopped=0

    if (( ! cron_needed )) && CONTAINER_HOST="unix://$user_sock" podman_is_running "cron"; then
        echo "$user: stopping cron (no longer needed)"
        CONTAINER_HOST="unix://$user_sock" timeout 20 podman stop "cron" &>/dev/null && ((stopped++))
    fi

    if (( ! backup_needed )) && CONTAINER_HOST="unix://$user_sock" podman_is_running "backup"; then
        echo "$user: stopping backup (no longer needed)"
        CONTAINER_HOST="unix://$user_sock" timeout 20 podman stop "backup" &>/dev/null && ((stopped++))
    fi

    if (( ! cron_needed && ! backup_needed )) && CONTAINER_HOST="unix://$user_sock" podman_is_running "docker-proxy"; then
        echo "$user: stopping docker-proxy (no longer needed)"
        CONTAINER_HOST="unix://$user_sock" timeout 20 podman stop "docker-proxy" &>/dev/null && ((stopped++))
    fi

    (( stopped > 0 )) && { flock -x 201; echo "${user}:-${stopped}" >> "$results_file"; } 201>>"$results_file.lock"

    (( ${#needed[@]} == 0 )) && return

    local count=0 svc
    for svc in "${needed[@]}"; do
        CONTAINER_HOST="unix://$user_sock" podman_is_running "$svc" && continue
        echo "$user: starting required container $svc"
        if CONTAINER_HOST="unix://$user_sock" podman_ensure_running "$svc" "$home" "$svc" 20; then
            ((count++))
        else
            echo -e "\e[31m[✘]\e[0m $user: FAILED to start required container $svc"
        fi
    done

    (( count > 0 )) && { flock -x 201; echo "${user}:${count}" >> "$results_file"; } 201>>"$results_file.lock"
}

start_containers_for_user() {
    local user="$1"
    local results_file="$2"
    id "$user" &>/dev/null || return
    local uid
    uid=$(id -u "$user")
    local user_sock="/run/user/$uid/podman/podman.sock"
    [ -S "$user_sock" ] || { echo -e "\e[38;5;214m[!]\e[0m $user: no active podman socket at $user_sock, skipping"; return; }
    start_containers_for_socket "$user_sock" "$user" "$results_file"    
    start_conditional_containers_for_user "$user" "$user_sock" "$results_file"
}

# loops root then every non-suspended user starting/recovering dead containers, sets RESTART_ROOT_COUNT/RESTART_USER_TOTAL/RESTART_USER_LINES[]/RESTART_ELAPSED for the caller to build a summary from
restart_dead_user_containers() {
  local START_TIME; START_TIME=$(date +%s)
  local RESULTS_FILE; RESULTS_FILE=$(mktemp /tmp/sentinel.container_restart_results.XXXXXX)

  # root first
  local ROOT_SOCK="/run/podman/podman.sock"
  start_containers_for_socket "$ROOT_SOCK" "root" "$RESULTS_FILE"

  # try opencli user-list --json to get suspended users that we will exclude
  local USERLIST_JSON SUSPENDED_CONTEXTS="" VALID_JSON=false
  USERLIST_JSON=$(timeout 15 opencli user-list --json 2>/dev/null)

  if [ -n "$USERLIST_JSON" ]; then
      if command -v jq &>/dev/null; then
          if echo "$USERLIST_JSON" | jq -e '.data' &>/dev/null; then
              VALID_JSON=true
              SUSPENDED_CONTEXTS=$(echo "$USERLIST_JSON" | jq -r '.data[] | select(.username | startswith("SUSPENDED_")) | .context')
          fi
      fi
  fi

  local USERS_TO_PROCESS=()

  if [ "$VALID_JSON" = true ]; then
      for userhome in /home/*/; do
          user=$(basename "$userhome")
          if echo "$SUSPENDED_CONTEXTS" | grep -qx "$user"; then
              echo "$user: suspended, skipping"
              continue
          fi
          USERS_TO_PROCESS+=("$user")
      done
  else
      for userhome in /home/*/; do
          USERS_TO_PROCESS+=("$(basename "$userhome")")
      done
  fi

  # if <=3 users run sequentially, else in parallel batches of cores x 2
  local NUM_USERS=${#USERS_TO_PROCESS[@]}

  if [ "$NUM_USERS" -le 3 ]; then
      for user in "${USERS_TO_PROCESS[@]}"; do
          start_containers_for_user "$user" "$RESULTS_FILE"
      done
  else
      local CORES PARALLEL_JOBS
      CORES=$(nproc)
      PARALLEL_JOBS=$((CORES * 2))
      echo "$NUM_USERS users to process, running in parallel (max $PARALLEL_JOBS jobs, $CORES cores x2)"
      export -f start_containers_for_user
      export -f start_containers_for_socket
      export -f start_conditional_containers_for_user
      export -f podman_is_running
      export -f podman_ensure_running
      printf '%s\n' "${USERS_TO_PROCESS[@]}" | xargs -I{} -P "$PARALLEL_JOBS" bash -c 'start_containers_for_user "$@"' _ {} "$RESULTS_FILE"
  fi

  # results
  RESTART_ROOT_COUNT=0
  RESTART_USER_TOTAL=0
  RESTART_USER_LINES=()
  local label count
  while IFS=: read -r label count; do
      [[ -z "$label" ]] && continue
      if [[ "$label" == "root" ]]; then
          RESTART_ROOT_COUNT=$count
      else
          (( RESTART_USER_TOTAL += count ))
          (( count > 0 )) && RESTART_USER_LINES+=("${label}: ${count}")
      fi
  done < "$RESULTS_FILE"
  rm -f "$RESULTS_FILE" "$RESULTS_FILE.lock"

  local END_TIME; END_TIME=$(date +%s)
  RESTART_ELAPSED=$((END_TIME - START_TIME))
}

perform_startup_actions() {
  mkdir -p /tmp/redis ; chmod 777 /tmp/redis    # for redis
  mkdir -p /tmp/ssh_cm ; chmod 700 /tmp/ssh_cm  # for ssh connections to slave servers
  IP=$(hostname -I | awk '{print $1}')
  sed -i -E 's#^( *- *")([0-9.]+:)?(53:53/(tcp|udp)")#\1'"$IP"':\3#' /root/docker-compose.yml

  restart_dead_user_containers
  touch "$LOCK_FILE_FOR_USER_CONTAINERS"  # starts the hourly window from reboot, so the next cron tick doesn't re-sweep immediately

  local summary_msg="Started ${RESTART_ROOT_COUNT} container(s) for root and ${RESTART_USER_TOTAL} container(s) across ${#RESTART_USER_LINES[@]} user(s) in ${RESTART_ELAPSED}s."
  if (( ${#RESTART_USER_LINES[@]} > 0 )); then
      local IFS=', '
      summary_msg+=" Per user: ${RESTART_USER_LINES[*]}."
  fi

  if [[ "$REBOOT" == "no" ]]; then
    ((WARN++)); echo "[!] Reboot notifications are disabled."; return
  fi
  local title="SYSTEM REBOOT!"
  local message; message="System was rebooted. $(uptime)"$'\n'"$summary_msg"
  write_notification warning system "$title" "$message"
}

remove_dependent_openpanel() {
  podman inspect openpanel &>/dev/null || return
  echo "  - Removing openpanel first (has a dependency on this container)"
  podman kill openpanel &>/dev/null
  podman container cleanup openpanel &>/dev/null
  podman rm -f openpanel &>/dev/null; podman rm -f --storage openpanel &>/dev/null
}

check_user_containers() {
  hr
  echo "Checking container status for openpanel users..."
  local flag_tt=3600
  if [[ -f "$LOCK_FILE_FOR_USER_CONTAINERS" ]]; then
    local age=$(( $(date +%s) - $(stat -c %Y "$LOCK_FILE_FOR_USER_CONTAINERS") ))
    if (( age < flag_tt )); then
      ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m User container check skipped (last run $((age/60))m ago)."; return
    fi
  fi
  touch "$LOCK_FILE_FOR_USER_CONTAINERS"

  restart_dead_user_containers

  if (( RESTART_ROOT_COUNT == 0 && RESTART_USER_TOTAL == 0 )); then
    ((PASS++)); echo -e "\e[32m[✔]\e[0m No dead user containers found (checked in ${RESTART_ELAPSED}s)."
    return
  fi

  ((WARN++))
  local summary_msg="Started/restarted ${RESTART_ROOT_COUNT} container(s) for root and ${RESTART_USER_TOTAL} container(s) across ${#RESTART_USER_LINES[@]} user(s) in ${RESTART_ELAPSED}s."
  if (( ${#RESTART_USER_LINES[@]} > 0 )); then
    local IFS=', '
    summary_msg+=" Per user: ${RESTART_USER_LINES[*]}."
  fi
  echo -e "\e[38;5;214m[!]\e[0m $summary_msg"
  write_info_notification service "Dead/required user containers started" "$summary_msg"
}

email_daily_report() {
  if [[ "$EMAIL_ALERT" == "no" ]]; then
    echo "Email alerts disabled."; return
  fi
  email_notification "Daily Usage Report" "Daily Usage Report"
}

# builds an alert body: what failed, what sentinel did about it, where to look next and the last log lines
alert_details() {
  local what="$1" did="$2" check="$3" logs="$4"
  local msg="$what $did Check with: $check"
  if [[ -n "$logs" ]]; then msg+=$'\n\nLast log lines:\n'"$logs"; else msg+=$'\n\nNo log output.'; fi
  printf '%s' "$msg"
}

check_service_status() {
  local svc=$1 title=$2
  if systemctl is-active --quiet "$svc"; then
    ((PASS++)); echo -e "\e[32m[✔]\e[0m $svc is active."
    resolve_notification "$title"
    return
  fi
  if [[ "$svc" == "admin" && -f /root/openadmin_is_disabled ]]; then
    ((PASS++)); echo -e "\e[32m[✔]\e[0m $svc disabled by Administrator."
    resolve_notification "$title"; return
  fi

  local log reason="was not active"
  log=$(journalctl -n 5 -u "$svc" --no-pager 2>/dev/null)
  if echo "$log" | grep -q "start-limit-hit"; then
    reason="hit the systemd start rate limit"
    echo -e "\e[31m[✘]\e[0m $svc hit start rate limit — resetting and restarting."
    systemctl reset-failed "$svc"
  elif echo "$log" | grep -q "Deactivated successfully"; then
    ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m $svc is inactive (disabled by Administrator), skipping restart."; return
  else
    echo -e "\e[31m[✘]\e[0m $svc is not active — restarting."
  fi

  systemctl restart "$svc"
  if systemctl is-active --quiet "$svc"; then
    (( STATUS < 1 )) && STATUS=1; ((WARN++))
    echo -e "\e[32m[✔]\e[0m $svc restarted successfully."
    resolve_notification "$title"
    write_info_notification service "$svc service restarted" "$svc $reason. Sentinel restarted it and it is running again."
  else
    ((FAIL++)); STATUS=2
    echo -e "\e[31m[✘]\e[0m Failed to restart $svc."
    log=$(journalctl -n 5 -u "$svc" --no-pager 2>/dev/null)
    write_notification critical service "$title" "$(alert_details "$svc $reason." "Sentinel ran 'systemctl restart $svc', but it is still not running." "systemctl status $svc and journalctl -u $svc -n 50" "$log")"
  fi
}

# Cache docker ps once per run — called 6+ times otherwise
DOCKER_PS_CACHE=""
_docker_ps_refresh() { DOCKER_PS_CACHE=$(podman ps --format "{{.Names}}" 2>/dev/null); }
_docker_ps() { echo "$DOCKER_PS_CACHE"; }
_docker_log() { podman logs --tail 10 "$1" 2>&1; }

_openpanel_http_ok() {
  local code
  code=$(curl -sko /dev/null -w "%{http_code}" --connect-timeout 2 --max-time 3 "https://localhost:2083/")
  [[ "$code" == "000" ]] && code=$(curl -so /dev/null -w "%{http_code}" --connect-timeout 2 --max-time 3 "http://localhost:2083/")
  [[ "$code" =~ ^(200|301|302|401)$ ]]
}

_caddy_http_ok() {
  local code
  code=$(curl -so /dev/null -w "%{http_code}" --connect-timeout 1 --max-time 1 "http://localhost/check")
  [[ "$code" == "200" || "$code" == "404" ]]
}

# reason says why sentinel (re)started the container, used in both the info entry and the alert
_docker_check_after_restart() {
  local svc=$1 title=$2 reason=${3:-"was not running"}
  _docker_ps_refresh
  if _docker_ps | grep -wq "$svc"; then
    ((WARN--)); echo -e "\e[32m[✔]\e[0m $svc restarted successfully."
    resolve_notification "$title"
    write_info_notification service "$svc container restarted" "Container $svc $reason. Sentinel started it and it is running again."
    return 0
  fi
  ((WARN--)); ((FAIL++)); STATUS=2
  echo -e "\e[31m[✘]\e[0m $svc failed to restart."
  write_notification critical service "$title" "$(alert_details "Container $svc $reason." "Sentinel tried to start it, but it is still not running." "podman ps -a --filter name=$svc and podman logs $svc" "$(_docker_log "$svc")")"
  return 1
}

docker_containers_status() {
  local svc=$1 title=$2

  if _docker_ps | grep -wq "$svc"; then
    if [[ "$svc" == "caddy" ]]; then
      CADDY_IS_ACTIVE=true
      if _caddy_http_ok; then
        ((PASS++)); echo -e "\e[32m[✔]\e[0m caddy is active and responding."
        resolve_notification "$title"
      else
        ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m caddy running but unresponsive — restarting."
        podman restart caddy &>/dev/null
        podman rm -f caddy &>/dev/null; podman rm -f --storage caddy &>/dev/null
        cd /root && podman-compose up -d caddy &>/dev/null
        sleep 2
        _docker_ps_refresh
        if _caddy_http_ok; then
          ((PASS++)); ((WARN--)); echo -e "\e[32m[✔]\e[0m caddy recovered."
          resolve_notification "$title"
          write_info_notification service "Caddy restarted and websites are up!" "Caddy was running but not responding on http://localhost/check. Sentinel recreated the container and it responds again."
        else
          ((WARN--)); ((FAIL++)); STATUS=2
          echo -e "\e[31m[✘]\e[0m caddy still unresponsive after restart."
          write_notification critical service "$title" "$(alert_details "Caddy is running but not responding on http://localhost/check, so websites may be down." "Sentinel recreated the container, but it still does not respond." "podman logs caddy" "$(_docker_log caddy)")"
        fi
      fi
    elif [[ "$svc" == "openpanel" ]]; then
      if _openpanel_http_ok; then
        ((PASS++)); echo -e "\e[32m[✔]\e[0m openpanel is active and responding."
        resolve_notification "$title"
      else
        ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m openpanel running but unresponsive — restarting."
        podman rm -f openpanel &>/dev/null; podman rm -f --storage openpanel &>/dev/null
        podman rm -f clamav &>/dev/null
        podman rm -f phpmyadmin &>/dev/null
        cd /root && podman-compose up -d openpanel &>/dev/null
        sleep 2
        _docker_ps_refresh
        sleep 2
        if _openpanel_http_ok; then
          ((PASS++)); ((WARN--)); echo -e "\e[32m[✔]\e[0m openpanel recovered."
          resolve_notification "$title"
          write_info_notification service "OpenPanel restarted and responding!" "OpenPanel was running but not responding on port 2083. Sentinel recreated the container and it responds again."
        else
          ((WARN--)); ((FAIL++)); STATUS=2
          echo -e "\e[31m[✘]\e[0m openpanel still unresponsive after restart!"
          write_notification critical service "$title" "$(alert_details "OpenPanel is running but not responding on port 2083, so users can't log in." "Sentinel recreated the container, but it still does not respond." "podman logs openpanel" "$(_docker_log openpanel)")"
        fi
      fi
    else
      ((PASS++)); echo -e "\e[32m[✔]\e[0m $svc container is active."
      resolve_notification "$title"
    fi
    return
  fi

  ((WARN++))
  case "$svc" in
    openpanel)
      local users; users=$(opencli user-list --json 2>/dev/null | awk -F'"' '/username/{print $4}' | grep -v SUSPENDED)
      if [[ -z "$users" || "$users" == "No users." ]]; then
        ((WARN--)); echo "  - No users found; $svc not needed."; resolve_notification "$title"
      else
        podman rm -f openpanel &>/dev/null; podman rm -f --storage openpanel &>/dev/null
        podman rm -f clamav &>/dev/null
        podman rm -f phpmyadmin &>/dev/null
        cd /root && podman-compose up -d openpanel &>/dev/null
        _docker_check_after_restart "$svc" "$title"
      fi ;;
    openpanel_dns)
      enabled_modules_line=$(grep '^enabled_modules=' "$CONF_FILE")
      if [[ "$enabled_modules_line" == *"dns"* ]]; then
          if ls /etc/bind/zones/*.zone &>/dev/null; then
              podman rm -f bind9 &>/dev/null; podman rm -f --storage bind9 &>/dev/null
              cd /root && podman-compose up -d bind9 &>/dev/null
              _docker_check_after_restart "$svc" "$title"
          else
              ((WARN--))
              echo "  - No DNS zones; bind9 not needed."; resolve_notification "$title"
          fi
      else
          ((WARN--))
          echo "  - DNS module not enabled; bind9 not starting."; resolve_notification "$title"
      fi ;;
    phpmyadmin)
      enabled_modules_line=$(grep '^enabled_modules=' "$CONF_FILE")
      if [[ "$enabled_modules_line" == *"phpmyadmin"* ]]; then
          if ls /home/*/sockets/mysqld/mysqld.sock &>/dev/null; then
              podman rm -f phpmyadmin &>/dev/null; podman rm -f --storage phpmyadmin &>/dev/null
              cd /root && podman-compose up -d phpmyadmin &>/dev/null
              _docker_check_after_restart "$svc" "$title"
          else
              ((WARN--))
              echo "  - No mysql/mariadb services yet; phpmyadmin not needed."; resolve_notification "$title"
          fi
      else
          ((WARN--))
          echo "  - phpmyadmin module not enabled; phpmyadmin not starting."; resolve_notification "$title"
      fi ;;
    caddy)
      if ls /etc/openpanel/caddy/domains &>/dev/null; then
        podman rm -f caddy &>/dev/null; podman rm -f --storage caddy &>/dev/null
        cd /root && podman-compose up -d caddy &>/dev/null
        _docker_check_after_restart "$svc" "$title"
      else
        ((WARN--)); echo "  - No domains; caddy not needed."; resolve_notification "$title"
      fi ;;
    *)
      podman restart "$svc" &>/dev/null
      _docker_check_after_restart "$svc" "$title" ;;
  esac
}

mysql_docker_containers_status() {
  local title="MariaDB service not active!"
  local mdb_ok mdb_tries

  if _docker_ps | grep -q "openpanel_mysql"; then
    if timeout 10 mariadb -Ne "SELECT 'PONG' AS PING;" 2>/dev/null | grep -q "PONG"; then
      ((PASS++)); echo -e "\e[32m[✔]\e[0m MariaDB container active and responding."
      resolve_notification "$title"; resolve_notification "MariaDB service restarted!"
      return
    fi

    ((WARN++))
    echo -e "\e[31m[✘]\e[0m MariaDB running but not responding — restarting."
    remove_dependent_openpanel
    podman rm -f openpanel_mysql &>/dev/null; podman rm -f --storage openpanel_mysql &>/dev/null
    cd /root && podman-compose up -d openpanel_mysql &>/dev/null

    mdb_ok=0
    for mdb_tries in 1 2 3 4 5 6; do
      sleep 5
      if timeout 10 mariadb -Ne "SELECT 'PONG' AS PING;" 2>/dev/null | grep -q "PONG"; then
        mdb_ok=1; break
      fi
    done

    if (( mdb_ok )); then
      ((WARN--)); ((PASS++))
      echo "    MariaDB is back online."
      resolve_notification "$title"
      write_info_notification service "MariaDB restarted successfully!" "MariaDB was running but not answering queries. Sentinel recreated the container and it responds again."
    else
      ((WARN--)); ((FAIL++)); STATUS=2
      echo "    Error: MariaDB still not responding!"
      write_notification critical service "$title" "$(alert_details "MariaDB is running but not answering queries, so websites and OpenPanel can't reach their databases." "Sentinel recreated the container, but it still does not respond after 30s." "podman logs openpanel_mysql" "$(_docker_log openpanel_mysql)")"
    fi

  else
    ((FAIL++)); STATUS=2
    echo -e "\e[31m[✘]\e[0m MariaDB container not running — restarting."
    remove_dependent_openpanel
    podman rm -f openpanel_mysql &>/dev/null; podman rm -f --storage openpanel_mysql &>/dev/null
    cd /root && podman-compose up -d openpanel_mysql &>/dev/null

    mdb_ok=0
    for mdb_tries in 1 2 3 4 5 6; do
      sleep 5
      if timeout 10 mariadb -Ne "SELECT 'PONG' AS PING;" 2>/dev/null | grep -q "PONG"; then
        mdb_ok=1; break
      fi
    done

    if (( mdb_ok )); then
      ((FAIL--)); (( STATUS < 1 )) && STATUS=1
      echo "    MariaDB is back online."
      resolve_notification "$title"
      write_info_notification service "MariaDB restarted successfully!" "The MariaDB container was not running. Sentinel started it and it responds again."
    else
      echo "    Error: MariaDB still not responding!"
      write_notification critical service "$title" "$(alert_details "The MariaDB container was not running, so websites and OpenPanel can't reach their databases." "Sentinel started it, but it still does not respond after 30s." "podman ps -a --filter name=openpanel_mysql and podman logs openpanel_mysql" "$(_docker_log openpanel_mysql)")"
    fi
  fi
}

redis_docker_container_status() {
  local title="Redis service not active!"
  local container="openpanel_redis"

  if ! podman inspect "$container" &>/dev/null; then
    ((WARN++))
    echo -e "\e[31m[✘]\e[0m Redis container not found — starting."
    cd /root && podman-compose up -d openpanel_redis &>/dev/null
    sleep 2
    _docker_check_after_restart "$container" "$title" "did not exist"
    return
  fi

  local state; state=$(podman inspect "$container" --format '{{.State.Status}}' 2>/dev/null)

  case "$state" in
    running)
      if podman exec "$container" redis-cli PING 2>/dev/null | grep -q PONG; then
        rm -f "$LOCK_FILE_FOR_REDIS_STUCK"
        ((PASS++)); echo -e "\e[32m[✔]\e[0m Redis container active and responding."
        resolve_notification "$title"; resolve_notification "Redis service restarted!"; resolve_notification "Redis container stuck"
      else
        ((WARN++))
        echo -e "\e[31m[✘]\e[0m Redis running but not responding — restarting."
        remove_dependent_openpanel
        podman rm -f "$container" &>/dev/null; podman rm -f --storage "$container" &>/dev/null
        cd /root && podman-compose up -d openpanel_redis &>/dev/null
        _docker_check_after_restart "$container" "$title" "was running but not responding to PING"
      fi
      ;;
    exited|stopped)
      rm -f "$LOCK_FILE_FOR_REDIS_STUCK"
      ((WARN++))
      echo -e "\e[38;5;214m[!]\e[0m Redis container is $state — restarting."
      cd /root && podman-compose up -d openpanel_redis &>/dev/null
      _docker_check_after_restart "$container" "$title" "had stopped (state: $state)"
      ;;
    *)
      # anything else is treated as wedged -- podman occasionally hangs mid-transition and never recovers, so only force-recreate once the same stuck state shows up on two consecutive runs, to avoid nuking a container that's simply mid-startup
      local now; now=$(date +%s)
      local stuck_since=""
      if [[ -f "$LOCK_FILE_FOR_REDIS_STUCK" ]]; then
        local prev_state prev_time
        IFS=: read -r prev_state prev_time < "$LOCK_FILE_FOR_REDIS_STUCK"
        [[ "$prev_state" == "$state" ]] && stuck_since=$prev_time
      fi

      if [[ -z "$stuck_since" ]]; then
        echo "$state:$now" > "$LOCK_FILE_FOR_REDIS_STUCK"
        ((WARN++))
        echo -e "\e[38;5;214m[!]\e[0m Redis container in transitional state '$state' — will re-check next run."
        return
      fi

      local stuck_age=$(( now - stuck_since ))
      if (( stuck_age < 180 )); then
        ((WARN++))
        echo -e "\e[38;5;214m[!]\e[0m Redis container still in '$state' (${stuck_age}s) — waiting."
        return
      fi

      ((WARN++))
      echo -e "\e[31m[✘]\e[0m Redis container stuck in '$state' for ${stuck_age}s — forcing removal and recreation."
      rm -f "$LOCK_FILE_FOR_REDIS_STUCK"
      remove_dependent_openpanel
      podman kill "$container" &>/dev/null
      podman rm -f "$container" &>/dev/null; podman rm -f --storage "$container" &>/dev/null
      cd /root && podman-compose up -d openpanel_redis &>/dev/null
      sleep 2
      _docker_check_after_restart "$container" "$title" "was stuck in state '$state' for ${stuck_age}s"
      ;;
  esac
}

check_services() {
  local svc
  # "docker" kept as an accepted alias for "podman" so existing services= ini entries from before the podman migration keep working
  for svc in caddy csf admin docker podman mysql panel phpmyadmin named; do
    [[ ",$SERVICES," != *",$svc,"* ]] && continue
    case "$svc" in
      caddy)  docker_containers_status  'caddy'         'Caddy not active — websites down!'             ;;
      phpmyadmin)  docker_containers_status  'phpmyadmin'         'phpmyadmin not active — users can not access databases!'             ;;
      csf)    check_service_status      'csf'           'CSF Firewall not active — server unprotected!' ;;
      admin)  check_service_status      'admin'         'OpenAdmin service not accessible!'             ;;
      mysql)  mysql_docker_containers_status                                                            ;;
      docker|podman) check_service_status 'podman.socket' 'Podman not active — user websites down!'     ;;
      panel)  redis_docker_container_status
              docker_containers_status  'openpanel'     'OpenPanel container not running!'               ;;
      named)  docker_containers_status  'openpanel_dns' 'BIND9 not active — DNS broken!'                ;;
    esac
  done
}

check_oom_logs() {
  if [[ "$LIMIT" == "no" ]]; then
    ((WARN++)); echo "[!] OOM errors check disabled."; return
  fi

  local NOW EPOCH_LAST DIFF

  NOW=$(date +%s)
  if [[ -f "$LOCK_FILE_FOR_OOM_CHECK" ]]; then
    EPOCH_LAST=$(cat "$LOCK_FILE_FOR_OOM_CHECK" 2>/dev/null)
    DIFF=$((NOW - EPOCH_LAST))
    [[ "$DIFF" -lt 86400 ]] && return
  fi

  echo "$NOW" > "$LOCK_FILE_FOR_OOM_CHECK"

  local TODAY LOG
  TODAY=$(date +%Y-%m-%d)
  if [[ -f /var/log/syslog ]]; then
    LOG="/var/log/syslog"
  elif [[ -f /var/log/messages ]]; then
    LOG="/var/log/messages"
  else
    return
  fi

  local SYSTEM_COUNT=0
  local USER_COUNT=0
  local -a SYSTEM_LINES=() USER_LINES=()

  while read -r line; do
    uid=$(echo "$line" | sed -n 's/.*UID:\([0-9]\+\).*/\1/p')
    [[ -z "$uid" ]] && continue

    if [[ "$uid" -eq 0 ]]; then
        ((SYSTEM_COUNT++))
        SYSTEM_LINES+=("$line")
    elif [[ "$uid" -ge 1002 ]]; then
        ((USER_COUNT++))
        user=$(getent passwd "$uid" | cut -d: -f1)
        [[ -z "$user" ]] && continue
        USER_LINES+=("$user"$'\t'"$line")
    fi

  done < <(grep "Memory cgroup out of memory: Killed process" "$LOG" | grep "^$TODAY")

  if [[ "$SYSTEM_COUNT" -eq 0 && "$USER_COUNT" -eq 0 ]]; then
    ((PASS++)); echo -e "\e[32m[✔]\e[0m No OOM errors detected."; return
  else
    ((FAIL++)); STATUS=2
  fi

  title_parts=()

  [[ "$SYSTEM_COUNT" -gt 0 ]] && title_parts+=("System: $SYSTEM_COUNT")
  [[ "$USER_COUNT" -gt 0 ]] && title_parts+=("User: $USER_COUNT")

  title="OOM Alert - $TODAY - $(IFS=' | '; echo "${title_parts[*]}")"
  message=""

  if [[ "$SYSTEM_COUNT" -gt 0 ]]; then
    message+="$SYSTEM_COUNT system service(s) killed by OOM today."$'\n'
    echo -e "\e[31m[✘]\e[0m $SYSTEM_COUNT system service(s) killed by OOM in the last 24 hours"
  fi

  if [[ "$USER_COUNT" -gt 0 ]]; then
    message+="$USER_COUNT user process(es) killed by OOM today."
    echo -e "\e[31m[✘]\e[0m $USER_COUNT user process(es) killed by OOM in the last 24 hours"
  fi

  message+=$'\n'"Check with: grep 'Killed process' $LOG"

  local details
  details=$(jq -nc --arg sys "$(printf '%s\n' "${SYSTEM_LINES[@]}")" --arg usr "$(printf '%s\n' "${USER_LINES[@]}")" '
    {kind: "oom",
     system: ($sys | split("\n") | map(select(. != ""))),
     users: ($usr | split("\n") | map(select(. != "") | split("\t") | {username: .[0], entry: .[1]})
             | group_by(.username) | map({username: .[0].username, entries: map(.entry)}))}')
  write_notification warning resources "$title" "${message%$'\n'}" "$details"
}

check_new_logins() {
  if [[ "$LOGIN" == "no" ]]; then
    ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m Login check disabled."; return
  fi

  local login_log="/var/log/openpanel/admin/login.log"
  local watermark_file="/tmp/sentinel.login_watermark"
  [[ ! -f "$login_log" ]] && : > "$login_log"

  local last_count=0
  [[ -f "$watermark_file" ]] && last_count=$(cat "$watermark_file" 2>/dev/null)
  local current_count; current_count=$(wc -l < "$login_log")
  echo "$current_count" > "$watermark_file"

  if (( current_count <= last_count )); then
    ((PASS++)); echo -e "\e[32m[✔]\e[0m No new logins to OpenAdmin."; return
  fi

  local new_lines; new_lines=$(tail -n +"$((last_count + 1))" "$login_log")

  if [[ -z "$new_lines" ]]; then
    ((PASS++)); echo -e "\e[32m[✔]\e[0m No new logins to OpenAdmin."; return
  fi

  local -A seen_pairs
  if (( last_count > 0 )); then
    while read -r seen_user seen_ip; do
      seen_pairs["$seen_user $seen_ip"]=1
    done < <(head -n "$last_count" "$login_log" | awk '{print $(NF-1), $NF}')
  fi

  local found_new=0
  while IFS= read -r line; do
    local username ip_address
    read -r _ _ username ip_address _ <<< "$line"

    [[ ! "$ip_address" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] && continue
    [[ "$ip_address" == "127.0.0.1" ]] && continue

    local pair="$username $ip_address"
    if [[ -z "${seen_pairs[$pair]}" ]]; then
      if is_ip_whitelisted "$ip_address"; then
        echo -e "\e[32m[✔]\e[0m $username from new but whitelisted IP: $ip_address"
      else
        ((FAIL++)); STATUS=2; found_new=1
        echo -e "\e[31m[✘]\e[0m $username logged in from new IP: $ip_address"
        write_notification warning security "Admin $username accessed from new IP: $ip_address" "Admin account $username was accessed from new IP: $ip_address. If this wasn't you, change the password for $username right away."
      fi
      # remember it so repeat logins for this IP in the same run aren't flagged again
      seen_pairs["$pair"]=1
    else
      echo -e "\e[32m[✔]\e[0m $username from known IP: $ip_address"
    fi
  done <<< "$new_lines"

  (( found_new == 0 )) && ((PASS++))
}

check_ssh_logins() {
  [[ "$SSH_LOGIN" == "no" ]] && return

  if is_unread_message_present "Suspicious SSH login detected"; then
    ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m Unread SSH notification exists. Skipping."; return
  fi

  local ssh_ips
  ssh_ips=$(who | awk '/pts/{gsub(/[():]/, "", $5); n=split($5,a,":"); print a[1]}')
  if [[ -z "$ssh_ips" ]]; then
    ((PASS++)); echo -e "\e[32m[✔]\e[0m No active SSH sessions."; return
  fi

  local login_log="/var/log/openpanel/admin/login.log"
  local login_ips; login_ips=$(awk '{print $NF}' "$login_log" 2>/dev/null)
  if [[ -z "$login_ips" ]]; then
    ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m SSH user detected; postponing check until OpenAdmin is ready."
    return
  fi

  local -a suspicious safe
  local ip
  for ip in $ssh_ips; do
    [[ "$ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || continue
    if ! is_ip_whitelisted "$ip" && ! grep -qF "$ip" <<< "$login_ips"; then
      suspicious+=("$ip")
    else
      safe+=("$ip")
    fi
  done

  if (( ${#suspicious[@]} > 0 )); then
    ((FAIL++)); STATUS=2
    echo -e "\e[31m[✘]\e[0m Suspicious SSH IPs: ${suspicious[*]}"
    write_notification critical security "Suspicious SSH login detected" "Active SSH session(s) from IP(s) that never logged into OpenAdmin and are not whitelisted: ${suspicious[*]}. Check with: who. If these are yours, add them to the SSH whitelist in OpenAdmin > Settings > Notifications."
  else
    ((PASS++))
    echo -e "\e[32m[✔]\e[0m ${#safe[@]} SSH session(s) from known IPs: ${safe[*]}"
  fi
}

disk_details() {
  jq -nc --argjson p "$1" --arg parts "$(df -h | sort -r -k 5 -i)" '{kind: "disk", percent: $p, partitions: $parts}'
}

check_disk_usage() {
  local flag_tt=86400
  local title="Running out of Disk Space!"
  local pct; pct=$(df --output=pcent / | awk 'NR==2{gsub(/%/,"",$1); print $1+0}')
  if (( pct > DISK_THRESHOLD )); then
    if is_unread_message_present "$title"; then
      ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m Unread DU notification. Skipping."; return
    fi
    # Try cleanup if not done in last 24h
    if [ -f "$LOCK_FILE_FOR_DOCKER_PRUNE" ]; then
      local age=$(( $(date +%s) - $(stat -c %Y "$LOCK_FILE_FOR_DOCKER_PRUNE") ))
      if [ "$age" -lt "$flag_tt" ]; then
        write_notification warning resources "$title" "Disk usage is ${pct}% (threshold ${DISK_THRESHOLD}%)." "$(disk_details "$pct")"
        return
      fi
    fi

    local kb_before; kb_before=$(df / | awk 'NR==2 {print $3}')

    timeout 30 podman system prune -f --filter "until=24h" > /dev/null 2>&1
    for context in /home/*; do
       [ -d "$context" ] || continue
       # timeout execs a binary directly and can't invoke podman_user (a bash function), so the socket is inlined via CONTAINER_HOST instead
       context_uid=$(stat -c '%u' "$context" 2>/dev/null) || continue
       timeout 15 env CONTAINER_HOST="unix:///hostfs/run/user/${context_uid}/podman/podman.sock" podman --remote system prune -f --filter "until=24h" > /dev/null 2>&1
    done
    touch "$LOCK_FILE_FOR_DOCKER_PRUNE"

    local kb_after; kb_after=$(df / | awk 'NR==2 {print $3}')
    local freed_gb=$(( (kb_before - kb_after) / 1024 / 1024 ))
    local pct_after; pct_after=$(df --output=pcent / | awk 'NR==2{gsub(/%/,"",$1); print $1+0}')

    if (( freed_gb >= 1 )); then
      write_notification warning resources "$title" "Disk was ${pct}%, Sentinel pruned unused podman data and freed ${freed_gb}GB, it is now at ${pct_after}% (threshold ${DISK_THRESHOLD}%)." "$(disk_details "$pct_after")"
      ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m Disk was ${pct}%, sentinel freed ${freed_gb}GB, disk is now at ${pct_after}%."; return   
    else
      ((FAIL++)); STATUS=2
      echo -e "\e[31m[✘]\e[0m Disk was ${pct}% > threshold ${DISK_THRESHOLD}%"
      write_notification warning resources "$title" "Disk usage is ${pct}% (threshold ${DISK_THRESHOLD}%). Sentinel pruned unused podman data but it freed less than 1GB. Check with: du -xh / --max-depth=2 | sort -rh | head" "$(disk_details "$pct")"
    fi
  else
    ((PASS++)); echo -e "\e[32m[✔]\e[0m Disk ${pct}% < threshold ${DISK_THRESHOLD}%"
    resolve_notification "$title"
  fi
}

check_system_load() {
  local title="High System Load!"
  local load_raw; read -r load_raw _ < /proc/loadavg
  local load=${load_raw%%.*}
  if (( load > LOAD_THRESHOLD )); then
    if ! over_threshold_streak load; then
      ((WARN++)); (( STATUS < 1 )) && STATUS=1
      echo -e "\e[38;5;214m[!]\e[0m Load ${load} > threshold ${LOAD_THRESHOLD} (${STREAK}/${STREAK_RUNS} checks), alerting if it stays high."; return
    fi
    ((FAIL++)); STATUS=2
    echo -e "\e[31m[✘]\e[0m Load ${load} > threshold ${LOAD_THRESHOLD} for ${STREAK} checks in a row. Generating crash report."
    is_unread_message_present "$title" && return
    generate_crashlog_report
    write_notification warning resources "$title" "Load average is ${load_raw} (threshold ${LOAD_THRESHOLD}) for ${STREAK} checks in a row." "$(jq -nc --arg l "$load_raw" --arg c "$REPORT" '{kind: "load", load: $l, crashlog: $c}')"
  else
    clear_streak load
    ((PASS++)); echo -e "\e[32m[✔]\e[0m Load ${load} < threshold ${LOAD_THRESHOLD}."
    resolve_notification "$title"
  fi
}

check_ram_usage() {
  local title="High Memory Usage!"
  local _ total used _rest
  read -r _ total used _rest < <(free -m | awk '/^Mem:/')
  local pct=$(( used * 100 / total ))
  if (( pct > RAM_THRESHOLD )); then
    if ! over_threshold_streak ram; then
      ((WARN++)); (( STATUS < 1 )) && STATUS=1
      echo -e "\e[38;5;214m[!]\e[0m RAM ${pct}% > threshold ${RAM_THRESHOLD}% (${STREAK}/${STREAK_RUNS} checks), alerting if it stays high."; return
    fi
    if is_unread_message_present "$title"; then
      ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m Unread RAM notification. Skipping."; return
    fi
    ((FAIL++)); STATUS=2
    echo -e "\e[31m[✘]\e[0m RAM ${pct}% > threshold ${RAM_THRESHOLD}%"
    local procs; procs=$(ps ax --sort=-%mem -o pid:7,pmem:6,comm:20 | head -10)
    write_notification warning resources "$title" "RAM usage is ${pct}% (${used}MB of ${total}MB, threshold ${RAM_THRESHOLD}%)." \
      "$(jq -nc --argjson p "$pct" --argjson u "$used" --argjson t "$total" --arg procs "$procs" '{kind: "ram", percent: $p, used_mb: $u, total_mb: $t, processes: $procs}')"
  else
    clear_streak ram
    ((PASS++)); echo -e "\e[32m[✔]\e[0m RAM ${pct}% < threshold ${RAM_THRESHOLD}%"
    resolve_notification "$title"
  fi
}

check_cpu_usage() {
  local title="High CPU Usage!"
  local -a f1 f2
  # 1s sample so a single busy moment doesn't count, runs in parallel with the other checks anyway
  read -ra f1 < <(grep '^cpu ' /proc/stat)
  sleep 1
  read -ra f2 < <(grep '^cpu ' /proc/stat)
  local idle1=${f1[4]} idle2=${f2[4]}
  local total1=0 total2=0 v
  for v in "${f1[@]:1}"; do (( total1 += v )); done
  for v in "${f2[@]:1}"; do (( total2 += v )); done
  local diff_total=$(( total2 - total1 ))
  local pct=$(( diff_total > 0 ? 100*(diff_total - (idle2-idle1)) / diff_total : 0 ))
  if (( pct > CPU_THRESHOLD )); then
    if ! over_threshold_streak cpu; then
      ((WARN++)); (( STATUS < 1 )) && STATUS=1
      echo -e "\e[38;5;214m[!]\e[0m CPU ${pct}% > threshold ${CPU_THRESHOLD}% (${STREAK}/${STREAK_RUNS} checks), alerting if it stays high."; return
    fi
    ((FAIL++)); STATUS=2
    echo -e "\e[31m[✘]\e[0m CPU ${pct}% > threshold ${CPU_THRESHOLD}%"
    local procs; procs=$(ps ax --sort=-%cpu -o pid:7,pcpu:6,comm:20 | head -10)
    write_notification warning resources "$title" "CPU usage is ${pct}% (threshold ${CPU_THRESHOLD}%)." \
      "$(jq -nc --argjson p "$pct" --arg procs "$procs" '{kind: "cpu", percent: $p, processes: $procs}')"
  else
    clear_streak cpu
    ((PASS++)); echo -e "\e[32m[✔]\e[0m CPU ${pct}% < threshold ${CPU_THRESHOLD}%"
    resolve_notification "$title"
  fi
}

check_https_traffic() {
  if [[ "$ATTACK" == "no" ]]; then
    ((WARN++)); echo "[!] Website traffic checks are disabled."; return
  fi
  if [[ "$CADDY_IS_ACTIVE" != "true" ]]; then
    ((WARN++)); echo "[!] Skipping website traffic checks because Caddy is not running."; return
  fi
  local ALERT=0 SYN_ALERT=0
  local ALL_CONNS
  ALL_CONNS=$(ss -tn '( sport = :80 or sport = :443 )' | tail -n +2)

  # SYN flooding
  while read -r COUNT PORT; do
    if (( COUNT >= 1000 )); then
      echo -e "\e[31m[✘]\e[0m Possible SYN flood on :$PORT — $COUNT in SYN_RECV"
      write_notification warning traffic "Possible SYN flood" "Port $PORT: $COUNT connections in SYN_RECV state"
      ALERT=1; SYN_ALERT=1
    fi
  done < <(awk '/SYN-RECV/{split($4,a,":"); print a[length(a)]}' <<< "$ALL_CONNS" | sort | uniq -c | awk '{print $1, $2}')

  local HIGH_TRAFFIC_LINES=()
  while read -r COUNT PORT IP; do
    if (( COUNT >= MAX_CONN_PER_IP )); then
      echo -e "\e[31m[✘]\e[0m High connections from $IP on port $PORT: $COUNT"

      local DOMAINS
      DOMAINS=$(
        find /var/log/caddy/domlogs -type f -name "access.log" 2>/dev/null |
        while read -r f; do
          # shellcheck disable=SC2016 # intentional: $1/$2 are this subshell's own positional args (bound below), not outer vars
          if timeout 1s bash -c '
            tail -n 200 "$1" | grep -q "$2"
          ' _ "$f" "$IP"; then
            echo "$f"
          fi
        done | sed 's|/var/log/caddy/domlogs/||; s|/access\.log||' | sort -u | tr '\n' ', ' | sed 's/,$//'
      )

      if [[ -n "$DOMAINS" ]]; then
        echo -e "    \e[33m[→]\e[0m Domain hit: $DOMAINS"
        HIGH_TRAFFIC_LINES+=("$IP (port $PORT: $COUNT conns | domains: $DOMAINS)")
      else
        echo -e "    \e[33m[→]\e[0m No matching domain logs found, check manually with: 'grep -Rli --include=access.log $IP /var/log/caddy/domlogs/ | xargs -n1 dirname | xargs -n1 basename'"
        HIGH_TRAFFIC_LINES+=("$IP (port $PORT: $COUNT conns)")
      fi

      ALERT=1
    fi
  done < <(
    awk '/ESTAB/{
      split($4, a, ":")
      port = a[length(a)]
      peer = $5
      if (peer ~ /^\[.*\]:[0-9]+$/) {
          sub(/^\[/, "", peer)
          sub(/\]:[0-9]+$/, "", peer)
          ip = peer
      } else {
          sub(/:[0-9]+$/, "", peer)
          ip = peer
      }
      sub(/^::ffff:/, "", ip)
      print port, ip
    }' <<< "$ALL_CONNS" |
    sort | uniq -c | awk '{print $1, $2, $3}'
  )

  if (( ${#HIGH_TRAFFIC_LINES[@]} > 0 )); then
    local NOTIF_BODY
    NOTIF_BODY=$(printf '%s\n' "${HIGH_TRAFFIC_LINES[@]}")
    if (( ${#HIGH_TRAFFIC_LINES[@]} == 1 )); then
      write_notification warning traffic "High traffic from ${HIGH_TRAFFIC_LINES[0]%%(*}" "$NOTIF_BODY"
    else
      write_notification warning traffic "High traffic from ${#HIGH_TRAFFIC_LINES[@]} IPs" "$NOTIF_BODY"
    fi
  else
    resolve_notification "High traffic from " --prefix
  fi
  (( SYN_ALERT == 0 )) && resolve_notification "Possible SYN flood"

  # Total established
  local TOTAL_CONN
  TOTAL_CONN=$(awk '/ESTAB/' <<< "$ALL_CONNS" | wc -l)
  if (( TOTAL_CONN >= MAX_TOTAL_CONN )); then
    echo -e "\e[31m[✘]\e[0m High total connections: $TOTAL_CONN"
    write_notification warning traffic "High total connections" "$TOTAL_CONN total connections on ports 80/443"
    ALERT=1
  else
    resolve_notification "High total connections"
  fi

  if [[ $ALERT -eq 0 ]]; then
    ((PASS++)); echo -e "\e[32m[✔]\e[0m No unusual traffic detected on web ports (80|443)."
  else
    ((FAIL++)); STATUS=2
  fi
}

check_swap_usage() {
  local title="High SWAP usage!"
  local _ stotal sused _rest
  read -r _ stotal sused _rest < <(free -m | awk '/^Swap:/')

  if (( stotal == 0 )); then
    local swap_devices
    swap_devices=$(swapon --show=NAME --noheadings 2>/dev/null)
    if [[ -z "$swap_devices" ]]; then
      local fstab_swap
      fstab_swap=$(awk '$3=="swap"{print $1}' /etc/fstab)
      if [[ -n "$fstab_swap" ]]; then
        echo -e "\e[38;5;214m[!]\e[0m SWAP is off but fstab entries exist. Attempting to re-enable..."
        local swapon_err
        swapon_err=$(swapon -a 2>&1 >/dev/null)
        if [[ -z "$swapon_err" ]]; then
          read -r _ stotal sused _rest < <(free -m | awk '/^Swap:/')
          if (( stotal > 0 )); then
            ((WARN++))
            resolve_notification "SWAP re-enable failed on $HOSTNAME"
            write_info_notification resources "SWAP re-enabled on $HOSTNAME" "Sentinel detected swap was off on $HOSTNAME at $DISPLAY_TIME and re-enabled it via swapon -a. Now: ${stotal}MB total."
            echo -e "\e[32m[✔]\e[0m SWAP successfully re-enabled (${stotal}MB total)."
          else
            ((WARN++))
            echo -e "\e[38;5;214m[!]\e[0m swapon -a ran but swap still reports 0. Check swap device health."
            write_notification warning resources "SWAP re-enable failed on $HOSTNAME" "swapon -a returned success but swap is still 0."
            return
          fi
        else
          ((WARN++))
          echo -e "\e[31m[✘]\e[0m Failed to re-enable SWAP: $swapon_err"
          write_notification warning resources "SWAP re-enable failed on $HOSTNAME" "swapon -a failed on $HOSTNAME at $DISPLAY_TIME. Error: $swapon_err"
          return
        fi
      else
        ((PASS++)); echo -e "\e[32m[✔]\e[0m No SWAP configured."
        resolve_notification "SWAP re-enable failed on $HOSTNAME"; return
      fi
    else
      ((PASS++)); echo -e "\e[32m[✔]\e[0m No SWAP configured."
      resolve_notification "SWAP re-enable failed on $HOSTNAME"; return
    fi
  fi

  local pct=$(( sused * 100 / stotal ))
  if (( pct <= SWAP_THRESHOLD )); then
    ((PASS++)); echo -e "\e[32m[✔]\e[0m SWAP ${pct}% < threshold ${SWAP_THRESHOLD}%"
    rm -f "$LOCK_FILE_FOR_SWAP_CLEANUP"
    resolve_notification "$title"; resolve_notification "SWAP high but cannot safely clear"; resolve_notification "URGENT: SWAP not cleared on $HOSTNAME"
    return
  fi

  if [[ -f "$LOCK_FILE_FOR_SWAP_CLEANUP" ]]; then
    local age=$(( $(date +%s) - $(date -r "$LOCK_FILE_FOR_SWAP_CLEANUP" +%s) ))
    if (( age <= 86400 )); then
      ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m SWAP cleanup already in progress. Skipping."; return
    fi
    rm -f "$LOCK_FILE_FOR_SWAP_CLEANUP"
  fi

  local available_mb
  available_mb=$(awk '/^MemAvailable:/{print int($2/1024)}' /proc/meminfo)
  if (( available_mb < sused )); then
    echo "Not enough free RAM to safely clear swap (${available_mb}MB available, ${sused}MB in swap). Skipping."
    write_notification warning resources "SWAP high but cannot safely clear" "SWAP: ${pct}%, only ${available_mb}MB RAM available vs ${sused}MB in swap."
    ((WARN++)); return
  fi

  echo -e "\e[31m[✘]\e[0m SWAP ${pct}% > threshold ${SWAP_THRESHOLD}%. Clearing..."
  touch "$LOCK_FILE_FOR_SWAP_CLEANUP"
  sync ; echo 3 > /proc/sys/vm/drop_caches
  swapoff -a ; swapon -a

  local stotal2 sused2
  read -r _ stotal2 sused2 _rest < <(free -m | awk '/^Swap:/')
  local pct2=$(( stotal2 > 0 ? sused2*100/stotal2 : 0 ))
  if (( pct2 < SWAP_THRESHOLD )); then
    rm -f "$LOCK_FILE_FOR_SWAP_CLEANUP"
    resolve_notification "$title"
    write_info_notification resources "SWAP cleared — now ${pct2}%" "Sentinel cleared SWAP on $HOSTNAME at $DISPLAY_TIME. Was ${sused}MB/${stotal}MB (${pct}%), now ${sused2}MB/${stotal2}MB (${pct2}%)."
    echo -e "\e[32m[✔]\e[0m SWAP cleared successfully. Now: ${pct2}%"
  else
    ((FAIL++)); STATUS=2
    echo -e "\e[31m[✘]\e[0m SWAP still high after cleanup: ${pct2}%"
    write_notification critical resources "URGENT: SWAP not cleared on $HOSTNAME" "SWAP was ${pct}% (threshold ${SWAP_THRESHOLD}%). Sentinel dropped caches and ran swapoff/swapon at $DISPLAY_TIME, but usage is still ${pct2}%. Check with: free -m and top -o %MEM"
  fi
}

generate_crashlog_report() {
  local dir="/var/log/openpanel/admin/crashlog"
  mkdir -p "$dir"
  REPORT="$dir/$(date +%s).txt"
  {
    echo "=== GENERAL === Hostname: $HOSTNAME | Date: $DISPLAY_TIME"
    echo "=== LOAD ===";     cat /proc/loadavg
    echo "=== TOP (MEM) ==="; ps -eo pid:7,%mem:5,comm:20 --sort=-%mem | head -11
    echo "=== TOP (CPU) ==="; ps -eo pid:7,%cpu:5,comm:20 --sort=-%cpu | head -11
    echo "=== SWAP TOP ===";  grep VmSwap /proc/*/status 2>/dev/null | sort -k2 -hr | head -10
    echo "=== DISKSTATS ==="; head -10 /proc/diskstats
  } > "$REPORT"
}

check_if_panel_domain_and_ns_resolve_to_server() {
  if [[ "$MAIN_DOMAIN_AND_NS" == "no" ]]; then
    ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m DNS check disabled."; return
  fi

  if [[ -f "$LOCK_FILE_FOR_DNS_CHECK" ]]; then
    local age=$(( $(date +%s) - $(date -r "$LOCK_FILE_FOR_DNS_CHECK" +%s) ))
    if (( age < 3600 )); then
      ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m DNS check skipped (last run $((age/60))m ago)."; return
    fi
  fi
  touch "$LOCK_FILE_FOR_DNS_CHECK"

  local FORCED_DOMAIN; FORCED_DOMAIN=$(opencli domain 2>/dev/null)
  local CHECK_DOMAIN="no" CHECK_NS="no"
  [[ "$FORCED_DOMAIN" =~ ^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$ ]] && CHECK_DOMAIN="yes"

  local NS1; NS1=$(conf_get ns1)
  if [[ "$NS1" =~ ^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$ ]]; then
    CHECK_NS="yes"
    local NS2 NS3 NS4
    NS2=$(conf_get ns2)
    # shellcheck disable=SC2034 # read indirectly below via ${!ns_var}
    NS3=$(conf_get ns3)
    # shellcheck disable=SC2034 # read indirectly below via ${!ns_var}
    NS4=$(conf_get ns4)
  fi

  if [[ "$CHECK_DOMAIN" == "no" && "$CHECK_NS" == "no" ]]; then
    ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m No valid domain/NS configured. Skipping DNS check."
    return
  fi

  local GNS="8.8.8.8"
  local SERVER_IP; SERVER_IP=$(get_public_ip)

  if [[ "$CHECK_DOMAIN" == "yes" ]]; then
    require_command dig bind-utils
    local domain_ip; domain_ip=$(dig +short @"$GNS" "$FORCED_DOMAIN" 2>/dev/null)
    if [[ "$domain_ip" == "$SERVER_IP" ]]; then
      ((PASS++)); echo -e "\e[32m[✔]\e[0m $FORCED_DOMAIN → $SERVER_IP"
      resolve_notification "$FORCED_DOMAIN does not resolve to " --prefix
    else
      local ns_rec; ns_rec=$(dig +short @"$GNS" NS "$FORCED_DOMAIN" 2>/dev/null)
      if [[ "${ns_rec,,}" == *cloudflare* ]]; then
        ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m $FORCED_DOMAIN uses Cloudflare proxy — skipping IP check."
        resolve_notification "$FORCED_DOMAIN does not resolve to " --prefix
      else
        ((FAIL++)); STATUS=2
        echo -e "\e[31m[✘]\e[0m $FORCED_DOMAIN resolves to $domain_ip, expected $SERVER_IP"
        write_notification warning dns "$FORCED_DOMAIN does not resolve to $SERVER_IP" "$FORCED_DOMAIN should point to $SERVER_IP but resolves to ${domain_ip:-nothing}. Update its A record at your DNS provider."
      fi
    fi
  else
    ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m Domain not set; skipping domain DNS check."
  fi

  if [[ "$CHECK_NS" == "yes" ]]; then
    if [[ -z "$NS2" ]]; then
      ((WARN++)); echo -e "\e[38;5;214m[!]\e[0m Only one NS set — add at least two for redundancy."
      return
    fi
    local all_ips; all_ips=$(hostname -I)
    local -a failed
    local ns_var ns_host ns_ip
    for ns_var in NS1 NS2 NS3 NS4; do
      ns_host="${!ns_var}"
      [[ -z "$ns_host" ]] && continue
      ns_ip=$(dig +short @"$GNS" "$ns_host" 2>/dev/null)
      grep -qw "$ns_ip" <<< "$all_ips" || \
        failed+=("$ns_var ($ns_host) → $ns_ip (expected: $all_ips)")
    done
    if (( ${#failed[@]} == 0 )); then
      ((PASS++)); echo -e "\e[32m[✔]\e[0m All nameservers resolve to local IPs."
      resolve_notification "Nameservers do not resolve to local IPs"
    else
      ((FAIL++)); STATUS=2
      printf '    %s\n' "${failed[@]}"
      write_notification warning dns "Nameservers do not resolve to local IPs" "$(printf '%s\n' "${failed[@]}")"$'\n'"Update the glue/A records of these nameservers at your domain registrar."
    fi
  else
    ((PASS++)); echo -e "\e[32m[✔]\e[0m No nameservers configured; skipping NS check."
  fi
}

write_snapshot() {
  mkdir -p "$(dirname "$SNAPSHOT_FILE")"

  local load_1m load_5m load_15m
  read -r load_1m load_5m load_15m _ < /proc/loadavg

  local mem_total mem_used mem_pct=0
  read -r _ mem_total mem_used _ _ < <(free -m | awk '/^Mem:/')
  (( mem_total > 0 )) && mem_pct=$(( mem_used * 100 / mem_total ))

  local swap_total=0 swap_used=0 swap_pct=0
  read -r _ swap_total swap_used _ < <(free -m | awk '/^Swap:/')
  (( swap_total > 0 )) && swap_pct=$(( swap_used * 100 / swap_total ))

  local disk_pct; disk_pct=$(df --output=pcent / | awk 'NR==2{gsub(/%/,"",$1); print $1+0}')

  local cpu_pct=0
  local -a c1 c2
  read -ra c1 < <(grep '^cpu ' /proc/stat)
  sleep 0.1
  read -ra c2 < <(grep '^cpu ' /proc/stat)
  local t1=0 t2=0 v
  for v in "${c1[@]:1}"; do (( t1 += v )); done
  for v in "${c2[@]:1}"; do (( t2 += v )); done
  local dt=$(( t2 - t1 ))
  (( dt > 0 )) && cpu_pct=$(( 100 * (dt - (c2[4]-c1[4])) / dt ))

  local total_conn=0
  total_conn=$(ss -tn '( sport = :80 or sport = :443 )' 2>/dev/null | grep -c ESTAB || true)

  local json
  printf -v json \
    '{"ts":"%s","load":{"1m":"%s","5m":"%s","15m":"%s"},"cpu_pct":%d,"mem":{"total_mb":%d,"used_mb":%d,"pct":%d},"swap":{"total_mb":%d,"used_mb":%d,"pct":%d},"disk_pct":%d,"web_conn":%d,"status":%d,"pass":%d,"warn":%d,"fail":%d}' \
    "$DISPLAY_TIME" "$load_1m" "$load_5m" "$load_15m" \
    "$cpu_pct" \
    "$mem_total" "$mem_used" "$mem_pct" \
    "$swap_total" "$swap_used" "$swap_pct" \
    "$disk_pct" \
    "$total_conn" \
    "$STATUS" "$PASS" "$WARN" "$FAIL"

  local utc_json
  # shellcheck disable=SC2001 # regex removal of the "ts" field isn't a plain substring, sed is clearer than a bash pattern here
  utc_json=$(echo "$json" | sed 's/"ts":"[^"]*"/"ts":"'"$(date -u +"%Y-%m-%d %H:%M:%S")"'"/')
  echo "$utc_json" >> "$SNAPSHOT_FILE"


  local line_count; line_count=$(wc -l < "$SNAPSHOT_FILE")
  if (( line_count > SNAPSHOT_MAX_LINES )); then
    local tmp; tmp=$(mktemp)
    tail -n "$SNAPSHOT_MAX_LINES" "$SNAPSHOT_FILE" > "$tmp" && mv "$tmp" "$SNAPSHOT_FILE"
  fi
}


hr() { printf '%*s\n' "${COLUMNS:-80}" '' | tr ' ' '-'; }

summary() {
  hr
  case $STATUS in
    0) echo -e "\e[32mAll Tests Passed!\e[0m" ;;
    1) echo -e "\e[93mSome non-critical tests failed.\e[0m" ;;
    *) echo -e "\e[41mOne or more tests failed.\e[0m" ;;
  esac
  hr
  echo -e "\e[1m${PASS} PASS  ${WARN} WARN  ${FAIL} FAIL\e[0m"
  hr
}


for arg in "$@"; do
  case "$arg" in
    --startup) perform_startup_actions; exit 0 ;;
    --report) email_daily_report; exit 0 ;;
    --action=*) action="${arg#*=}" ;;
    --message=*) message="${arg#*=}" ;;
    --title=*) title="${arg#*=}";;
  esac
done

if [[ -n "$action" ]]; then
  write_action_notification "$action" "$title" "$message"
  exit 0
fi

if [ "${FLOCKED}" != "1" ]; then
  exec env FLOCKED=1 flock -n /root/sentinel_run.lock "$0" "$@"
  echo "Error: Another instance is already running."
  exit 1
fi
hr
echo "  Sentinel - OpenPanel server health monitor"
hr

# an update restarts containers on purpose, so don't alert on it or recreate them mid-update
if ! flock -n /var/lock/openpanel_update.lock true 2>/dev/null; then
  echo -e "\e[38;5;214m[!]\e[0m OpenPanel update in progress, skipping checks until it finishes."
  hr
  exit 0
fi

SENTINEL_QUEUE=$(mktemp /tmp/sentinel.queue.XXXXXX)
trap 'rm -f "$SENTINEL_QUEUE" "$SENTINEL_QUEUE.lock"' EXIT

echo "Checking services:"
_docker_ps_refresh
check_services
check_oom_logs

hr
echo "Checking traffic:"
check_https_traffic

hr
echo "Checking logins, resources, and DNS..."

declare -A _pids _outfiles
_parallel_tasks=(
  check_new_logins
  check_ssh_logins
  check_disk_usage
  check_system_load
  check_ram_usage
  check_cpu_usage
  check_swap_usage
  check_if_panel_domain_and_ns_resolve_to_server
  check_user_containers
)

for _task in "${_parallel_tasks[@]}"; do
  _out=$(mktemp /tmp/sentinel.par.XXXXXX)
  _outfiles[$_task]="$_out"
  (
    # shellcheck disable=SC2030 # intentional: each task's counters are isolated in this subshell, then reconciled below by reading the printed __COUNTERS__ line
    STATUS=0 PASS=0 WARN=0 FAIL=0
    $_task
    echo "__COUNTERS__ $STATUS $PASS $WARN $FAIL"
  ) > "$_out" 2>&1 &
  _pids[$_task]=$!
done

for _task in "${_parallel_tasks[@]}"; do
  wait "${_pids[$_task]}" 2>/dev/null
  _out="${_outfiles[$_task]}"
  [[ -f "$_out" ]] || continue
  grep -v '^__COUNTERS__' "$_out"
  read -r _ _s _p _w _f < <(grep '^__COUNTERS__' "$_out")
  # shellcheck disable=SC2031 # reconciling the parent's counters from the subshell's printed values is the intended pattern here, not a lost update
  (( STATUS  = STATUS  > _s ? STATUS  : _s ))
  # shellcheck disable=SC2031
  (( PASS   += _p ))
  # shellcheck disable=SC2031
  (( WARN   += _w ))
  # shellcheck disable=SC2031
  (( FAIL   += _f ))
  rm -f "$_out"
done

flush_notification_queue
write_snapshot
summary
