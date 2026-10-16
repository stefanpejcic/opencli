#!/bin/bash
################################################################################
# Script Name: imunify.sh
# Description: Install and manage ImunifyAV service.
# Usage: opencli imunify [status|start|stop|install|update|uninstall]
# Docs: https://docs.openpanel.com
# Author: Stefan Pejcic
# Created: 04.08.2025
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



# ======================================================================
# Helpers
# shellcheck disable=SC1091
source /usr/local/opencli/lib/requirement.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/email.sh
require_command jq

update_version() {
  local PANEL_INFO_SH="/etc/sysconfig/imunify360/get-panel-info.sh"

  cat <<'EOF' > "$PANEL_INFO_SH"
#!/usr/bin/env bash
opencli_version="$(opencli version 2>/dev/null || echo unknown)"
cat <<JSON
{
  "data": {
    "name": "OpenPanel",
    "version": "$opencli_version"
  },
  "metadata": {
    "result": "ok"
  }
}
JSON
EOF

  chmod 0755 "$PANEL_INFO_SH"
  chmod +x "$PANEL_INFO_SH"
}

IAV_HOOK="/etc/sysconfig/imunify360/iav_hook.sh"
IAV_SPOOL="/var/spool/openpanel-imunify"
IAV_UNIT="openpanel-imunify-notify"

# imunify runs the hook as _imunify which can't use opencli, so it spools the event and a root path unit picks it up
configure_av_hook() {
  mkdir -p "$IAV_SPOOL/tmp" "$IAV_SPOOL/new"
  chown -R _imunify:root "$IAV_SPOOL"
  chmod -R 0700 "$IAV_SPOOL"

  cat > "$IAV_HOOK" <<EOT
#!/bin/bash
tmp=\$(mktemp "$IAV_SPOOL/tmp/XXXXXX") || exit 1
cat > "\$tmp" && mv "\$tmp" "$IAV_SPOOL/new/\${tmp##*/}.json"
EOT
  chmod 0755 "$IAV_HOOK"

  cat > "/etc/systemd/system/$IAV_UNIT.path" <<EOT
[Unit]
Description=Pass ImunifyAV scan events to OpenPanel Sentinel

[Path]
DirectoryNotEmpty=$IAV_SPOOL/new

[Install]
WantedBy=multi-user.target
EOT

  cat > "/etc/systemd/system/$IAV_UNIT.service" <<EOT
[Unit]
Description=Pass ImunifyAV scan events to OpenPanel Sentinel

[Service]
Type=oneshot
ExecStart=/usr/local/bin/opencli imunify notify
EOT

  systemctl daemon-reload
  systemctl enable --now "$IAV_UNIT.path"

  local rule
  # finished already carries the malicious files, so malware found would only duplicate it
  for rule in USER_SCAN_MALWARE_FOUND CUSTOM_SCAN_MALWARE_FOUND; do
    imunify-antivirus notifications-config update "{\"rules\": {\"$rule\": {\"SCRIPT\": {\"enabled\": false}}}}"
  done
  for rule in USER_SCAN_STARTED CUSTOM_SCAN_STARTED USER_SCAN_FINISHED CUSTOM_SCAN_FINISHED; do
    imunify-antivirus notifications-config update "{\"rules\": {\"$rule\": {\"SCRIPT\": {\"scripts\": [\"$IAV_HOOK\"], \"enabled\": true}}}}"
  done
}

# 75 -> 1m 15s
human_duration() {
  local s="$1"
  if (( s < 60 )); then echo "${s}s"
  elif (( s < 3600 )); then echo "$((s / 60))m $((s % 60))s"
  else echo "$((s / 3600))h $((s % 3600 / 60))m"
  fi
}

# emails the account owner when a finished scan of their home found malware, path is /home/<context>/...
notify_av_user() {
  local context="$1" malicious="$2" path="$3" files_json="$4" username list
  [[ "$context" =~ ^[A-Za-z0-9_.-]+$ ]] || return 0
  username=$(mariadb --defaults-extra-file=/etc/my.cnf -D panel -N -B -e "SELECT username FROM users WHERE server = '$context' LIMIT 1" 2>/dev/null)
  [[ -n "$username" && "$username" != SUSPENDED_* ]] || return 0

  # the user only knows their files by the path inside the panel, not the host volume path
  list=$(jq -r --arg c "$context" '.[:20][] | sub("^/home/\($c)/docker-data/volumes/\($c)_html_data/_data/"; "/var/www/html/") | "- " + .' <<< "$files_json")
  (( malicious > 20 )) && list+=$'\n'"- and $((malicious - 20)) more"

  send_user_email "$username" "Malware found on account $username" \
    "An ImunifyAV malware scan found $malicious infected file(s) on account $username:"$'\n\n'"${list}"$'\n\n'"Scanned: $(date '+%Y-%m-%d %H:%M:%S %Z')" \
    notify_malware_found "" "" "" \
    "Remove or clean these files, or ask your hosting provider for help if you're not sure what they are. Malware usually gets in through outdated plugins, themes or stolen passwords, so update your websites and change your FTP and admin passwords." \
    && echo "$username notified by email about $malicious malicious file(s)."
}

# opencli imunify notify
notify_av() {
  local files=("$IAV_SPOOL"/new/*)
  [[ -f "${files[0]}" ]] || return 0

  # one event per scan, a finished one wins over started in case both landed in the same batch
  local events
  events=$(jq -sc 'map(select(.scan_id)) | group_by(.scan_id)[]
    | (map(select(.event_id | endswith("SCAN_FINISHED"))) | first) // (map(select(.event_id | endswith("SCAN_STARTED"))) | first) // empty' "${files[@]}" 2>/dev/null)
  rm -f "${files[@]}"

  local ev event scan_id path user kind started completed files_count malicious error
  local title message severity details no_send duration list
  while IFS= read -r ev; do
    [[ -n "$ev" ]] || continue
    event=$(jq -r '.event_id' <<< "$ev")
    scan_id=$(jq -r '.scan_id' <<< "$ev")
    path=$(jq -r '.path // ""' <<< "$ev")
    started=$(jq -r '.started // 0 | floor' <<< "$ev")
    completed=$(jq -r '.completed // 0 | floor' <<< "$ev")
    files_count=$(jq -r '.total_resources // 0' <<< "$ev")
    malicious=$(jq -r '.total_malicious // 0' <<< "$ev")
    error=$(jq -r '.error // ""' <<< "$ev")

    user=""
    [[ "$path" =~ ^/home/([^/]+) ]] && user="${BASH_REMATCH[1]}"
    kind="Malware scan"
    [[ "$event" == CUSTOM_* ]] && kind="Custom malware scan"

    severity=info
    no_send=""
    if [[ "$event" == *SCAN_STARTED ]]; then
      title="ImunifyAV scan running${user:+: $user}"
      message="$kind ($path) started at $(date -d "@$started" '+%H:%M:%S'). This notification will be updated when the scan finishes."
      # only shown in the panel, email and webhook wait for the result
      no_send="--no-send"
    else
      duration=$(human_duration $(( completed > started ? completed - started : 0 )))
      if [[ -n "$error" ]]; then
        severity=warning
        title="ImunifyAV scan failed${user:+: $user}"
        message="$kind ($path) stopped after $duration with error: $error"
      elif (( malicious > 0 )); then
        severity=warning
        title="ImunifyAV found $malicious malicious file$( (( malicious > 1 )) && echo s)${user:+: $user}"
        list=$(jq -r '.malicious_files // [] | .[:5] | join(", ")' <<< "$ev")
        (( malicious > 5 )) && list+=" and $((malicious - 5)) more"
        message="$kind ($path) checked $files_count files in $duration and found: $list. Review them in OpenAdmin > ImunifyAV."
        [[ -n "$user" ]] && notify_av_user "$user" "$malicious" "$path" "$(jq -c '.malicious_files // []' <<< "$ev")"
      else
        title="ImunifyAV scan clean${user:+: $user}"
        message="$kind ($path) checked $files_count files in $duration, no malware found."
      fi
    fi

    details=$(jq -c --arg user "$user" --arg d "${duration:-}" '{scan_id, path, user: $user, files: .total_resources, malicious: .total_malicious,
      duration: $d, malicious_files: ((.malicious_files // [])[:20]), state: (if (.event_id | endswith("STARTED")) then "running" else "finished" end)}' <<< "$ev")
    opencli sentinel --action=imunify_scan --title="$title" --message="$message" --severity="$severity" --details="$details" --merge-key=scan_id $no_send
    duration=""
  done <<< "$events"
}

remove_av_hook() {
  systemctl disable --now "$IAV_UNIT.path" 2>/dev/null
  rm -f "/etc/systemd/system/$IAV_UNIT.path" "/etc/systemd/system/$IAV_UNIT.service"
  systemctl daemon-reload
  rm -rf "$IAV_SPOOL"
}

configure_av_limits_and_email() {

  echo "Configuring ImunifyAV notifications to use 'OpenAdmin > Settings > Notifications'..."
  configure_av_hook


  # https://docs.imunifyav.com/config_file_description/
  imunify-antivirus config update '{"MALWARE_SCANNING": {"hyperscan": true}}'
  
  echo "Setting 2CPU and 1GB Memory limits for ImunifyAV service.."
  imunify-antivirus config update '{"MALWARE_SCAN_INTENSITY": {"cpu": 2}}'
  imunify-antivirus config update '{"MALWARE_SCAN_INTENSITY": {"io": 2}}'
  imunify-antivirus config update '{"MALWARE_SCAN_INTENSITY": {"ram": 1024}}'
  imunify-antivirus config update '{"MALWARE_SCAN_INTENSITY": {"user_scan_cpu": 2}}'
  imunify-antivirus config update '{"MALWARE_SCAN_INTENSITY": {"user_scan_io": 2}}'
  imunify-antivirus config update '{"MALWARE_SCAN_INTENSITY": {"user_scan_ram": 1024}}'
  
  imunify-antivirus config update '{"RESOURCE_MANAGEMENT": {"cpu_limit": 1}}'
  imunify-antivirus config update '{"RESOURCE_MANAGEMENT": {"io_limit": 1}}'
  imunify-antivirus config update '{"RESOURCE_MANAGEMENT": {"ram_limit": 500}}'
  
  # cron
  # full scan on the 1st of every month at 3am
  imunify-antivirus config update '{"MALWARE_SCAN_SCHEDULE": {"interval": "month", "day_of_month": 1, "hour": 3}}'
  imunify-antivirus config update '{"PERMISSIONS": {"allow_malware_scan": true}}'

  # exclude paths!
  echo "Adding Containers, Images and writable filesystems to ignored files list.."

  imunify360-agent malware ignore add '/home/*/docker-data/overlay'
  imunify360-agent malware ignore add '/home/*/docker-data/containers'
  imunify360-agent malware ignore add '/home/*/docker-data/image'

cat <<\EOT >> /etc/sysconfig/imunify360/malware-filters-admin-conf/ignored.txt
^/home/(.*)/docker-data/containers/(.*)
^/home/(.*)/docker-data/image/(.*)
^/home/(.*)/docker-data/overlay/(.*)
EOT

  imunify360-agent malware rebuild patterns
}




# ======================================================================
# opencli imunify status
status_av() {
  if pgrep -u _imunify -f "php -S 127.0.0.1:9000" >/dev/null; then
    echo "Imunify GUI is running."
    pgrep -u _imunify -fa "php -S 127.0.0.1:9000"
  else
    echo "Imunify GUI is not running."
  fi
}



# ======================================================================
# opencli imunify install
install_av() {
  
  uname -m | grep -qE 'x86_64|amd64' || { echo "ABORTING: ImunifyAV does NOT support your CPU architecture, only supports: AMD/x86"; exit 1; }
  
  echo "Creating directories..."
  mkdir -p /etc/sysconfig/imunify360/

  PAM_DENY_FILE="/etc/pam.d/imunify360-deny"
  if [ ! -f "$PAM_DENY_FILE" ]; then
    echo "Creating pam_deny.so file..."
    cat <<EOF > "$PAM_DENY_FILE"
auth required pam_deny.so
account required pam_deny.so
EOF
  else
    echo "$PAM_DENY_FILE already exists, skipping..."
  fi
  
  INTEGRATION_CONF="/etc/sysconfig/imunify360/integration.conf"
  if [ ! -f "$INTEGRATION_CONF" ]; then
    echo "Creating integration.conf file..."
    cat <<EOF > "$INTEGRATION_CONF"
[paths]
ui_path = /etc/sysconfig/imunify360/imav
ui_path_owner = _imunify:_imunify 

[pam]
service_name = imunify360-deny

[integration_scripts]
panel_info = /etc/sysconfig/imunify360/get-panel-info.sh
users = /usr/local/opencli/user/list.sh --json
domains = /usr/local/opencli/domains/all.sh --json

[malware]
basedir = /home
pattern_to_watch = ^/home/[^/]+/docker-data/volumes/[^/]+_html_data/_data(/.*)?$
EOF
  else
    echo "$INTEGRATION_CONF already exists, skipping..."
  fi
  
  update_version
  
  DEPLOY_SCRIPT="imav-deploy.sh"
  if [ ! -f "$DEPLOY_SCRIPT" ]; then
    echo "Downloading deploy script..."
    wget --timeout=5 --tries=3 --inet4-only https://repo.imunify360.cloudlinux.com/defence360/imav-deploy.sh -O "$DEPLOY_SCRIPT"
  else
    echo "$DEPLOY_SCRIPT already downloaded, skipping..."
  fi
  
  if ! grep -q "# Deployed by imav-deploy" "$DEPLOY_SCRIPT"; then
    echo "Running deploy script..."
    if ! bash "$DEPLOY_SCRIPT"; then
      echo
      echo "[ERROR] Installing ImunifyAV failed - please check the above output." >&2
      echo
      exit 1
    fi
  else
    echo "Deploy script already executed or invalid, skipping..."
  fi

  configure_av_limits_and_email
  
  echo "Allowing users to initiate a scan.."
  imunify360-agent config update '{"PERMISSIONS": {"allow_malware_scan": true}}'
  imunify-antivirus config update '{"PERMISSIONS": {"allow_malware_scan": true}}'

  echo "Installing PHP if not present..."
  if ! command -v php >/dev/null 2>&1; then
    if command -v apt-get >/dev/null 2>&1; then
      apt-get update
      apt-get install -y php-cli
    elif command -v yum >/dev/null 2>&1; then
      yum update
      yum install -y php-cli
    else
        echo "Unsupported package manager. Only apt-get and yum are supported."
        exit 1
    fi
  else
    echo "PHP already installed."
  fi






  
  
  # add it to openadmin > services > status
  FILE="/etc/openpanel/openadmin/config/services.json"

NEW_SERVICE='    {
        "name": "ImunifyAV",
        "type": "system",
        "real_name": "imunify-antivirus"
    }'
  
  if tail -n 1 "$FILE" | grep -q '\]'; then
      head -n -1 "$FILE" > "$FILE.tmp"

      # add comma to the last existing object if needed
      if tail -n 1 "$FILE.tmp" | grep -vq '},'; then
          sed -i '$s/}/},/' "$FILE.tmp"
      fi

      echo "$NEW_SERVICE" >> "$FILE.tmp"
      echo "]" >> "$FILE.tmp"

      mv "$FILE.tmp" "$FILE"
  
      echo "New service added successfully."
  else
      echo "Invalid JSON format in $FILE"
  fi



  echo "Install completed!"
}


# ======================================================================
# opencli imunify uninstall
uninstall_av() {
  remove_av_hook
  echo "Removing files and directories..."
  rm -rf /etc/sysconfig/imunify360/
  rm -f /etc/pam.d/imunify360-deny
  rm -f imav-deploy.sh
  rm -f /var/log/imunify-php-server.log

  echo "Checking for _imunify user..."
  if id "_imunify" >/dev/null 2>&1; then
    echo "User '_imunify' exists. Not removing automatically to avoid breaking dependencies."
    echo "→ If you're sure, run: userdel _imunify"
  fi

  echo "Removing ImunifyAV from OpenAdmin > Services Status..."
  FILE="/etc/openpanel/openadmin/config/services.json"
  TMP_FILE="services.tmp.json"
  if jq 'map(select(.name != "ImunifyAV"))' "$FILE" > "$TMP_FILE"; then
      mv "$TMP_FILE" "$FILE"
      echo "Service 'ImunifyAV' removed successfully."
  else
      echo "Failed to parse or update $FILE. Check JSON format."
      rm -f "$TMP_FILE"
  fi

  echo "Uninstall complete."
}

# ======================================================================
# opencli imunify start
start_av() {
  pkill -u _imunify -f "php -S 127.0.0.1:9000" 2>/dev/null || true
  systemctl stop imunify-gui 2>/dev/null || true

  chown -R _imunify /etc/sysconfig/imunify360/
  if ! pgrep -f "php -S 127.0.0.1:9000" >/dev/null; then
    # its own unit, so restarting the admin service (which may have called this) doesn't take the gui down with it
    if command -v systemd-run >/dev/null 2>&1; then
      systemd-run --unit=imunify-gui --collect --quiet -p User=_imunify -p StandardOutput=append:/var/log/imunify-php-server.log -p StandardError=append:/var/log/imunify-php-server.log "$(command -v php)" -S 127.0.0.1:9000 -t /etc/sysconfig/imunify360/
    else
      nohup sudo -u _imunify php -S 127.0.0.1:9000 -t /etc/sysconfig/imunify360/ > /var/log/imunify-php-server.log 2>&1 &
    fi
    echo "ImunifyAV GUI started."
  else
    echo "ImunifyAV GUI already running."
  fi
}

# ======================================================================
# opencli imunify stop
stop_av() {
  systemctl stop imunify-gui 2>/dev/null || true
  pkill -f "php -S 127.0.0.1:9000"
  if ! pgrep -f "php -S 127.0.0.1:9000" >/dev/null; then
    echo "ImunifyAV GUI disabled."
  else
    echo "Failed to disable ImunifyAV GUI."
  fi
}


# ======================================================================
# opencli imunify update
update_av() {
  # https://docs.imunify360.com/imunifyav/#update-instructions
  echo "Updating ImunifyAV..."
  if command -v apt-get >/dev/null 2>&1; then
      apt-get update
      apt-get install --only-upgrade -y imunify-antivirus
  elif command -v yum >/dev/null 2>&1; then
      yum update -y imunify-antivirus
  else
      echo "Unsupported package manager. Only apt-get and yum are supported."
      exit 1
  fi
}


# ======================================================================
case "$1" in
    status)
        status_av
        exit 0
        ;;
    install)
        echo "Installing ImunifyAV..."
        install_av
        exit 0
        ;;
    uninstall)
        echo "Uninstalling ImunifyAV..."
        stop_av
        uninstall_av
        exit 0
        ;;
    start)
        echo "Starting ImunifyAV..."
        update_version
        start_av
        exit 0
        ;;
    stop)
        echo "Stopping ImunifyAV..."
        stop_av
        exit 0
        ;;
    update)
        update_av
        exit 0
        ;;
    notify)
        notify_av
        exit 0
        ;;
    *)
        echo "Usage: opencli imunify {status|install|update|uninstall|start|stop}"
        exit 1
        ;;
esac
