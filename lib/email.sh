#!/bin/bash
################################################################################
# Script Name: lib/email.sh
# Description: Shared helpers for sending emails through OpenAdmin's /send_email.
# Usage: . /usr/local/opencli/lib/email.sh
# Author: Stefan Pejcic
# Created: 25.09.2026
# Last Modified: 25.09.2026
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

# OpenAdmin sends the email with the SMTP settings from Settings > Notifications,
# the one-time mail_security_token in openpanel.config proves the request is local.
# This file only defines functions, so it's safe to source from any script.

EMAIL_CONF_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
EMAIL_ADMIN_INI="/etc/openpanel/openadmin/config/admin.ini"
EMAIL_USERS_DIR="/etc/openpanel/openpanel/core/users"

# sends one email, returns 0 only when OpenAdmin says it was sent, type "user" gets OpenAdmin's panel user template
# usage: send_email <recipient> <subject> <body> [type] [usage JSON, drawn as usage bars in the user template] [upgrade plan name] [upgrade text] [tips shown under the upgrade section]
send_email() {
  local recipient="$1" subject="$2" body="$3" type="${4-}" usage="${5-}" upgrade_plan="${6-}" upgrade_text="${7-}" tips="${8-}"
  [[ -n "$recipient" ]] || return 1

  local token; token=$(tr -dc 'a-zA-Z0-9' </dev/urandom | head -c 64)
  awk -v t="$token" '/^mail_security_token=/{$0="mail_security_token="t} 1' "$EMAIL_CONF_FILE" > "${EMAIL_CONF_FILE}.tmp" && mv "${EMAIL_CONF_FILE}.tmp" "$EMAIL_CONF_FILE"

  local domain; domain=$(opencli domain)
  local proto="http"
  if { [[ -f "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/${domain}/${domain}.crt" ]] && [[ -f "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/${domain}/${domain}.key" ]]; } \
    || { [[ -f "/etc/openpanel/caddy/ssl/custom/${domain}/${domain}.crt" ]] && [[ -f "/etc/openpanel/caddy/ssl/custom/${domain}/${domain}.key" ]]; }; then
    proto="https"
  fi

  local -a auth_opt=()
  if grep -qx 'basic_auth=yes' "$EMAIL_ADMIN_INI" 2>/dev/null; then
    local u p
    u=$(awk -F= '/^basic_auth_username=/{print $2; exit}' "$EMAIL_ADMIN_INI")
    p=$(awk -F= '/^basic_auth_password=/{print $2; exit}' "$EMAIL_ADMIN_INI")
    auth_opt=(--user "${u}:${p}")
  fi

  local admin_port resp
  admin_port=$(awk '/# START HOSTNAME DOMAIN #/{flag=1; next} /# END HOSTNAME DOMAIN #/{flag=0} flag' "/etc/openpanel/caddy/Caddyfile" | grep -oP 'localhost:\K[0-9]+' | head -n 1)
  resp=$(curl -4 --max-time 10 -ks -X POST "$proto://$domain:$admin_port/send_email" "${auth_opt[@]}" \
    --form-string "transient=$token" --form-string "recipient=$recipient" \
    --form-string "subject=$subject" --form-string "body=$body" --form-string "type=$type" --form-string "usage=$usage" \
    --form-string "upgrade_plan=$upgrade_plan" --form-string "upgrade_text=$upgrade_text" --form-string "tips=$tips" 2>/dev/null)

  [[ "$resp" == *'sent successfully'* ]]
}

# the user's email address from the panel database
user_email_address() {
  local username="$1"
  [[ "$username" =~ ^[A-Za-z0-9_.-]+$ ]] || return 1
  mariadb --defaults-extra-file=/etc/my.cnf -D panel -N -B -e "SELECT email FROM users WHERE username = '$username' LIMIT 1" 2>/dev/null
}

# true unless the user has <key>=0 in their notifications.yaml, a missing key is on like the Account > Notifications page shows it
user_wants_notification() {
  local username="$1" key="$2"
  ! grep -qx "${key}=0" "$EMAIL_USERS_DIR/$username/notifications.yaml" 2>/dev/null
}

# emails a panel user if the notifications module is enabled and, when a key is given, the user opted into it
# usage: send_user_email <username> <subject> <body> [notification key, e.g. notify_disk_limit] [usage JSON] [upgrade plan name] [upgrade text] [tips]
send_user_email() {
  local username="$1" subject="$2" body="$3" key="${4-}" usage="${5-}" upgrade_plan="${6-}" upgrade_text="${7-}" tips="${8-}"

  grep -E '^enabled_modules=' "$EMAIL_CONF_FILE" 2>/dev/null | grep -qE '[",]notifications[",]' || return 1
  if [[ -n "$key" ]] && ! user_wants_notification "$username" "$key"; then
    return 1
  fi

  local email; email=$(user_email_address "$username")
  [[ -n "$email" ]] || return 1
  send_email "$email" "$subject" "$body" user "$usage" "$upgrade_plan" "$upgrade_text" "$tips"
}

# plan size limit like "10 GB", "1G" or "500 MB" in MB, a bare number is GB like in the plan editor, prints nothing for unlimited (0 or empty)
plan_size_mb() {
  local raw="${1// /}" n unit
  [[ -z "$raw" ]] && return 0
  [[ "${raw^^}" =~ ^([0-9]+([.][0-9]+)?)([KMGT]?)B?$ ]] || return 1
  n="${BASH_REMATCH[1]}" unit="${BASH_REMATCH[3]:-G}"
  LC_ALL=C awk -v n="$n" 'BEGIN { exit !(n + 0 == 0) }' && return 0
  LC_ALL=C awk -v n="$n" -v u="$unit" 'BEGIN { m = (u == "K") ? 1 / 1024 : (u == "M") ? 1 : (u == "G") ? 1024 : 1048576; printf "%.0f\n", n * m }'
}

# plan size limit for humans, "unlimited" for 0 or empty
plan_size_label() {
  local mb; mb=$(plan_size_mb "$1") || { echo "$1"; return; }
  [[ -z "$mb" ]] && { echo "unlimited"; return; }
  LC_ALL=C awk -v m="$mb" 'BEGIN { if (m >= 1024) { v = m / 1024; printf (v == int(v) ? "%d GB\n" : "%.1f GB\n"), v } else printf "%d MB\n", m }'
}

# true when the upsell limit is higher than the current one, unlimited counts as higher, usage: limit_raised <current> <upsell> <size|count>
limit_raised() {
  local cur="$1" up="$2" kind="$3"
  if [[ "$kind" == "size" ]]; then
    cur=$(plan_size_mb "$cur") || return 1
    up=$(plan_size_mb "$up") || return 1
  else
    [[ "$cur" =~ ^[0-9]+$ && "$up" =~ ^[0-9]+$ ]] || return 1
    [[ "$cur" == 0 ]] && cur=""
    [[ "$up" == 0 ]] && up=""
  fi
  [[ -z "$cur" ]] && return 1
  [[ -z "$up" ]] && return 0
  (( up > cur ))
}

# prints the user's plan limits and its upsell plan's, tab separated, only on Enterprise and when the upsell plan and its upgrade URL are set, same rule as the panel's /dashboard/upgrade
# columns: upsell name, disk, upsell disk, inodes, upsell inodes, max email quota, upsell max email quota, max hourly email, upsell max hourly email, empty limits come back as 0 (unlimited)
user_upsell() {
  local username="$1"
  [[ "$username" =~ ^[A-Za-z0-9_.-]+$ ]] || return 1
  grep -qE '^key=enterprise' "$EMAIL_CONF_FILE" 2>/dev/null || return 1
  mariadb --defaults-extra-file=/etc/my.cnf -D panel -N -B -e "
    SELECT up.name, COALESCE(NULLIF(p.disk_limit, ''), '0'), COALESCE(NULLIF(up.disk_limit, ''), '0'),
      COALESCE(NULLIF(p.inodes_limit, ''), '0'), COALESCE(NULLIF(up.inodes_limit, ''), '0'),
      COALESCE(NULLIF(p.max_email_quota, ''), '0'), COALESCE(NULLIF(up.max_email_quota, ''), '0'),
      COALESCE(NULLIF(p.max_hourly_email, ''), '0'), COALESCE(NULLIF(up.max_hourly_email, ''), '0')
    FROM users u JOIN plans p ON p.id = u.plan_id JOIN plans up ON up.id = p.upsell_plan_id
    WHERE u.username = '$username' AND COALESCE(p.upsell_url, '') != '' LIMIT 1" 2>/dev/null | grep .
}
