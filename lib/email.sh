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

# sends one email, returns 0 only when OpenAdmin says it was sent
# usage: send_email <recipient> <subject> <body>
send_email() {
  local recipient="$1" subject="$2" body="$3"
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
    --form-string "subject=$subject" --form-string "body=$body" 2>/dev/null)

  [[ "$resp" == *'sent successfully'* ]]
}

# the user's email address from the panel database
user_email_address() {
  local username="$1"
  [[ "$username" =~ ^[A-Za-z0-9_.-]+$ ]] || return 1
  mariadb --defaults-extra-file=/etc/my.cnf -D panel -N -B -e "SELECT email FROM users WHERE username = '$username' LIMIT 1" 2>/dev/null
}

# true when the user has <key>=1 in their notifications.yaml, same file the Account > Notifications page saves
user_wants_notification() {
  local username="$1" key="$2"
  grep -qx "${key}=1" "$EMAIL_USERS_DIR/$username/notifications.yaml" 2>/dev/null
}

# emails a panel user if the notifications module is enabled and, when a key is given, the user opted into it
# usage: send_user_email <username> <subject> <body> [notification key, e.g. notify_disk_limit]
send_user_email() {
  local username="$1" subject="$2" body="$3" key="${4-}"

  grep -E '^enabled_modules=' "$EMAIL_CONF_FILE" 2>/dev/null | grep -qE '[",]notifications[",]' || return 1
  if [[ -n "$key" ]] && ! user_wants_notification "$username" "$key"; then
    return 1
  fi

  local email; email=$(user_email_address "$username")
  [[ -n "$email" ]] || return 1
  send_email "$email" "$subject" "$body"
}
