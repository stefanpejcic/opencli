#!/bin/bash
################################################################################
# Script Name: report.sh
# Description: Generate a system report and send it to OpenPanel support team.
# Usage: opencli report [--public|--link|--upload] [--non-interactive] [--user <USERNAME>]
# Author: Stefan Pejcic
# Created: 07.10.2023
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

# ======================================================================
# Constants
GREEN='\033[0;32m'
RESET='\033[0m'
CMD_TIMEOUT=10
LOG_LINES=50

# ======================================================================
# Variables
upload_flag=false
non_interactive=false
report_user=""


# ======================================================================
# Helpers

usage() {
	echo "Usage: opencli report [--public|--link|--upload] [--non-interactive] [--user <USERNAME>]"
	echo ""
	echo "  --public, --link, --upload  upload the report to support.openpanel.org and print a key to share"
	echo "  --non-interactive           no progress messages, screen clearing or colors"
	echo "  --user <USERNAME>           also collect containers, logs and config for this user"
}

create_local_path() {
	output_dir="/var/log/openpanel/admin/reports"
	mkdir -p "$output_dir"
	chmod 700 "$output_dir"
	output_file="$output_dir/system_info_$(date +'%Y%m%d%H%M%S').txt"
	( umask 077; : > "$output_file" )
}

parse_args() {
	while [[ $# -gt 0 ]]; do
	    case $1 in
	        --non-interactive) non_interactive=true ;;
	        --public|--link|--upload) upload_flag=true ;;
	        --user) report_user="$2"; shift ;;
	        --user=*) report_user="${1#*=}" ;;
	        -h|--help) usage; exit 0 ;;
	        *) echo "Unknown option: $1"; usage; exit 1 ;;
	    esac
	    shift
	done

	if [ -n "$report_user" ] && [ ! -d "/home/$report_user" ]; then
	    echo "Error: user $report_user does not exist (no /home/$report_user)."
	    exit 1
	fi
}

# masks values of password/secret/token/key lines but keeps plain yes/no/number values so settings stay readable
redact() {
  awk '{
    line = tolower($0)
    if (match(line, /^[[:space:]"-]*[a-z0-9_.]*(pass|secret|token|key|pwd)[a-z0-9_.]*"?[[:space:]]*[=:][[:space:]]*/)) {
      val = substr($0, RLENGTH + 1)
      if (val != "" && tolower(val) !~ /^"?(yes|no|on|off|true|false|[0-9]+)?"?$/) { print substr($0, 1, RLENGTH) "***REDACTED***"; next }
    }
    print
  }'
}

run_command() {
  local cmd="$1"
  local label="$2"
  local tmpfile="$3"
  {
    echo "# $label:"
    echo "\$ $cmd"
    timeout "$CMD_TIMEOUT" bash -c "$cmd" 2>&1
    echo "# ======================================================================"
    echo
  } >> "$tmpfile"
}

section() {
  echo "=== $1 ===" >> "$2"
}

# each collect_* writes to its own temp file, they run in parallel and get merged in order

collect_quick_checks() {
  local tmp="$1" out=""
  section "Quick Checks (possible problems)" "$tmp"

  out+=$(df -hP -x tmpfs -x devtmpfs -x overlay -x squashfs 2>/dev/null | awk 'NR>1 && $5+0 >= 90 {print "[!] disk " $6 " is " $5 " full"}')$'\n'
  out+=$(df -iP -x tmpfs -x devtmpfs -x overlay -x squashfs 2>/dev/null | awk 'NR>1 && $5+0 >= 90 {print "[!] inodes on " $6 " are " $5 " used"}')$'\n'
  out+=$(awk '/MemTotal/{t=$2} /MemAvailable/{a=$2} END{if (t && a*100/t < 10) printf "[!] only %d%% RAM available\n", a*100/t}' /proc/meminfo)$'\n'
  out+=$(awk -v c="$(nproc)" '{if ($2 > c*2) printf "[!] 5min load %s is high for %d cpus\n", $2, c}' /proc/loadavg)$'\n'
  out+=$(systemctl --failed --no-legend --plain 2>/dev/null | awk '{print "[!] failed unit: " $1}')$'\n'
  for svc in admin podman.socket csf; do
    systemctl list-unit-files "$svc*" &>/dev/null || continue
    systemctl is-active --quiet "$svc" || out+="[!] service $svc is not active"$'\n'
  done
  out+=$(timeout "$CMD_TIMEOUT" podman ps -a --format '{{.Names}} {{.State}}' 2>/dev/null | awk '$2!="running"{print "[!] container " $1 " is " $2}')$'\n'
  out+=$(journalctl -k --since "-7 days" --no-pager 2>/dev/null | grep -ciE "out of memory|oom-kill" | awk '$1>0{print "[!] " $1 " OOM kills in the last 7 days"}')$'\n'
  [ -f /root/docker-compose.yml ] || out+="[!] /root/docker-compose.yml is missing"$'\n'
  [ -f /etc/openpanel/openpanel/conf/openpanel.config ] || out+="[!] openpanel.config is missing"$'\n'

  out=$(printf '%s' "$out" | sed '/^$/d')
  { [ -n "$out" ] && echo "$out" || echo "No problems detected."; echo; } >> "$tmp"
}

collect_os_info() {
  local tmp="$1"
  section "System" "$tmp"
  run_command "date; timedatectl 2>/dev/null | grep -E 'Time zone|synchronized'" "Date and time"            "$tmp"
  run_command "hostname -f"                               "Hostname"                                  "$tmp"
  run_command "grep -E '^(PRETTY_NAME|VERSION_ID)=' /etc/os-release; uname -srm" "OS and kernel"      "$tmp"
  run_command "systemd-detect-virt"                       "Virtualization"                            "$tmp"
  run_command "nproc; grep -m1 'model name' /proc/cpuinfo" "CPU"                                      "$tmp"
  run_command "uptime"                                    "Uptime and load"                           "$tmp"
  run_command "free -h"                                   "Memory and swap"                           "$tmp"
  run_command "df -hT -x tmpfs -x devtmpfs -x overlay -x squashfs" "Disk usage"                       "$tmp"
  run_command "df -i -x tmpfs -x devtmpfs -x overlay -x squashfs"  "Inode usage"                      "$tmp"
  run_command "ps -eo pid,user,%cpu,%mem,etime,comm --sort=-%cpu | head -15" "Top processes by CPU"   "$tmp"
  run_command "ps -eo pid,user,%cpu,%mem,etime,comm --sort=-%mem | head -15" "Top processes by memory" "$tmp"
  run_command "journalctl -k --since '-7 days' --no-pager | grep -iE 'out of memory|oom-kill' | tail -20" "OOM kills (last 7 days)" "$tmp"
  run_command "systemctl --failed --no-pager"             "Failed systemd units"                      "$tmp"
}

collect_network_info() {
  local tmp="$1"
  section "Network" "$tmp"
  run_command "ip -br addr"                               "IP addresses"                              "$tmp"
  run_command "ss -tulpn"                                 "Listening ports"                           "$tmp"
  run_command "grep -v '^#' /etc/resolv.conf"             "DNS resolvers"                             "$tmp"
  run_command "getent hosts openpanel.com || echo 'DNS resolution failed'" "DNS lookup test"          "$tmp"
  run_command "curl -sS -o /dev/null -m 5 -w '%{http_code} in %{time_total}s\n' https://hub.docker.com" "Outbound HTTPS test" "$tmp"
}

collect_versions() {
  local tmp="$1"
  section "Versions" "$tmp"
  run_command "opencli --version"                         "OpenPanel version"                         "$tmp"
  run_command "podman --version; podman-compose --version 2>&1 | tail -1" "Podman versions"         "$tmp"
  run_command "mariadb --protocol=tcp --version"          "MariaDB client version"                    "$tmp"
  run_command "csf -v"                                    "Sentinel Firewall (CSF) version"           "$tmp"
  run_command "podman images --format 'table {{.Repository}}:{{.Tag}} {{.ID}} {{.Created}}'" "Host images" "$tmp"
}

collect_services_status() {
  local tmp="$1"
  section "Services" "$tmp"
  run_command "cd /root && podman-compose ps"             "OpenPanel stack"                           "$tmp"
  run_command "podman ps -a --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'" "Host containers" "$tmp"
  run_command "podman stats --no-stream"                  "Host container resource usage"             "$tmp"
  run_command "systemctl status admin --no-pager -n 20"   "OpenAdmin service"                         "$tmp"
  run_command "systemctl status podman.socket --no-pager -n 10" "Podman socket"                      "$tmp"
  run_command "systemctl status csf --no-pager -n 10"     "Sentinel Firewall (CSF)"                   "$tmp"
}

collect_openpanel_settings() {
  local tmp="$1"
  section "Configuration (secrets redacted)" "$tmp"
  run_command "redact < /etc/openpanel/openpanel/conf/openpanel.config" "openpanel.config"            "$tmp"
  run_command "redact < /etc/openpanel/openadmin/config/admin.ini"      "admin.ini"                   "$tmp"
  run_command "redact < /etc/openpanel/caddy/Caddyfile"                 "Caddyfile"                   "$tmp"
  run_command "redact < /root/.env"                                     "/root/.env"                  "$tmp"
}

collect_logs() {
  local tmp="$1"
  section "Logs (last $LOG_LINES lines)" "$tmp"
  run_command "tail -n $LOG_LINES /var/log/openpanel/admin/error.log"   "OpenAdmin error log"         "$tmp"
  run_command "tail -n $LOG_LINES /var/log/openpanel/admin/notifications.log" "Notifications"         "$tmp"
  local c
  for c in openpanel caddy openpanel_mysql openpanel_redis; do
    run_command "podman logs --tail $LOG_LINES $c"      "Container $c"                              "$tmp"
  done
  run_command "f=\$(ls -t /var/log/openpanel/updates/*.log | head -1) && echo \"\$f\" && tail -n $LOG_LINES \"\$f\"" "Latest update log" "$tmp"
}

collect_users_overview() {
  local tmp="$1"
  section "Users" "$tmp"
  run_command "opencli user-list --total"                 "Number of users"                           "$tmp"
  {
    echo "# Containers per user (running/total):"
    local dir user uid sock
    for dir in /home/*; do
      [ -f "$dir/docker-compose.yml" ] || continue
      user=$(basename "$dir")
      uid=$(stat -c '%u' "$dir" 2>/dev/null)
      sock="/hostfs/run/user/${uid}/podman/podman.sock"
      if [ ! -S "$sock" ]; then
        echo "$user: no podman socket at $sock"
        continue
      fi
      CONTAINER_HOST="unix://$sock" timeout 5 podman --remote ps -a --format '{{.Names}} {{.State}}' 2>&1 \
        | awk -v u="$user" '{t++; if ($2=="running") r++; else bad=bad " " $1 "(" $2 ")"} END{printf "%s: %d/%d%s\n", u, r, t, (bad ? " not running:" bad : "")}'
    done
    echo "# ======================================================================"
    echo
  } >> "$tmp"
}

collect_user_details() {
  local tmp="$1" user="$report_user"
  [ -n "$user" ] || return 0
  local uid; uid=$(stat -c '%u' "/home/$user" 2>/dev/null)
  local host="CONTAINER_HOST=unix:///hostfs/run/user/${uid}/podman/podman.sock"
  section "User: $user" "$tmp"
  run_command "opencli user-list --json | jq '.data[] | select(.username==\"$user\")'" "Account info" "$tmp"
  run_command "opencli domains-user $user --docroot"      "Domains"                                   "$tmp"
  run_command "id $user; loginctl show-user $user -p State -p Linger"  "System user and linger"       "$tmp"
  run_command "systemctl status user@$uid --no-pager -n 10" "User systemd instance"                  "$tmp"
  run_command "$host podman --remote ps -a --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'" "Containers" "$tmp"
  run_command "$host podman --remote stats --no-stream"   "Container resource usage"                  "$tmp"
  run_command "redact < /home/$user/.env"                 ".env (secrets redacted)"                   "$tmp"
  run_command "redact < /home/$user/docker-compose.yml"   "docker-compose.yml (secrets redacted)"     "$tmp"
  local c
  for c in $(CONTAINER_HOST="unix:///hostfs/run/user/${uid}/podman/podman.sock" timeout 5 podman --remote ps -a --format '{{.Names}}' 2>/dev/null); do
    run_command "$host podman --remote logs --tail $LOG_LINES $c" "Container $c logs"               "$tmp"
  done
}

export -f redact


# ======================================================================
# Main

# order here is the order sections appear in the report
ORDERED_FUNCS=(
  collect_quick_checks
  collect_versions
  collect_os_info
  collect_services_status
  collect_user_details
  collect_users_overview
  collect_logs
  collect_network_info
  collect_openpanel_settings
)

main() {
  if [ "$non_interactive" = false ]; then
    echo "Collecting system information..."
  fi

  local tmpdir i=0 func
  tmpdir=$(mktemp -d)

  {
    echo "OpenPanel system report"
    echo "Generated: $(date -u +'%Y-%m-%d %H:%M:%S UTC')"
    echo "Command: opencli report $*"
    echo
  } >> "$output_file"

  for func in "${ORDERED_FUNCS[@]}"; do
    "$func" "$(printf '%s/%04d.txt' "$tmpdir" "$i")" &
    (( i++ ))
  done
  wait

  cat "$tmpdir"/*.txt >> "$output_file" 2>/dev/null
  rm -rf "$tmpdir"

  upload_report
}


upload_report() {
	if [ ! -s "$output_file" ]; then
	  echo "Information not collected! report file does not exist: $output_file"
   	else
	  if [ "$non_interactive" = false ]; then
	  	clear
	  fi
	  if [ "$upload_flag" = true ]; then
	    response=$(curl -F "file=@$output_file" https://support.openpanel.org/opencli_server_info.php 2>/dev/null)
	    if echo "$response" | grep -q "File upload failed."; then
	      echo ""
	      echo -e "Information collected successfully but uploading to support.openpanel.org failed. Please provide content from the following file to the support team:\n$output_file"
	    elif echo "$response" | grep -q "name="; then
	      FILE_NAME=$(echo "$response" | cut -d'=' -f2)
       		if [ "$non_interactive" = true ]; then
		      echo -e "Information collected successfully. Please provide the following key to the support team:\n${FILE_NAME}"
       		else
		      echo -e "Information collected successfully. Please provide the following key to the support team:\n${GREEN}${FILE_NAME}${RESET}"
  		fi
	    else
	      echo -e "Unexpected upload response:\n$response"
	      echo -e "Please send the content of the following file manually:\n$output_file"
	    fi
	  else
	    echo -e "Information collected successfully. Please provide content of the following file to the support team:\n$output_file"
	  fi
	fi
}


# ======================================================================
# flock
(
flock -n 200 || { echo "Error: Another instance of the report script is already running. Exiting."; exit 1; }
parse_args "$@"
create_local_path
main "$@"
)200>/tmp/opencli_report.lock

exit 0
