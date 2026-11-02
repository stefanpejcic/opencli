#!/bin/bash
################################################################################
# Script Name: email/setup.sh
# Description: Setup email addresses, forwarders, filters..
# Usage: opencli email-setup <COMMAND> <ATTRIBUTES>
# Docs: https://docs.openpanel.com
# Author: Stefan Pejcic
# Created: 18.08.2024
# Last Modified: 18.10.2026
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

if [ "$#" -lt 1 ]; then
    echo "Usage: opencli email-setup <command> [<args>...]"
    echo "Docs: https://openpanel.com/docs/articles/opencli/email"
    exit 1
fi

readonly CONTAINER=openadmin_mailserver
readonly MAIL_DIR="/usr/local/mail/openmail"
readonly COMPOSE_FILE="$MAIL_DIR/compose.yml"
readonly CONFIG_DIR="$MAIL_DIR/docker-data/dms/config"
readonly ACCOUNTS_FILE="$CONFIG_DIR/postfix-accounts.cf"
readonly QUOTAS_FILE="$CONFIG_DIR/dovecot-quotas.cf"

# shellcheck disable=SC1091
. /usr/local/opencli/lib/podman.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/redis.sh

is_valid_email() {
  local email="$1"
  local email_regex='^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$'
  if [[ $email =~ $email_regex ]]; then
    return 0
  else
    return 1
  fi
}

validate_first() {
    local key_value
    key_value=$(grep "^key=" -- "/etc/openpanel/openpanel/conf/openpanel.config" | cut -d'=' -f2-)

    if [ -z "$key_value" ]; then
        echo "Error: OpenPanel Community edition does not support emails. Please consider purchasing the Enterprise version that allows unlimited number of email addresses."
        local ENTERPRISE="/usr/local/opencli/lib/enterprise.sh"
        # shellcheck disable=SC1090
        . "$ENTERPRISE"
        echo "$ENTERPRISE_LINK"
        exit 1
    fi
}

get_openpanel_username_and_uid_for_domain() {
    user="${1%@*}"
    domain="${1#*@}"
    local whoowns_output
    whoowns_output=$(opencli domains-whoowns "$domain" --context)
    read -r _ owner <<< "$whoowns_output"
    [[ -n "$owner" ]] && OP_UID=$(stat -c '%u' "/home/$owner")
}

# dovecot needs ~2s to pick up a new account, listing it with an empty quota and then without its limit until then; filled in with what it will show: nothing used yet and the limit from the quotas file
fill_new_account_quotas() {
    local line email limit used used_b limit_b
    while IFS= read -r line; do
        if [[ "$line" =~ ^\*\ ([^ ]+)\ \((\ \ /\ \ \)\ \[%\]|\ 0\ /\ ~\ \)\ \[0%\])$ ]]; then
            email="${BASH_REMATCH[1]}"
            limit=$(grep -m1 "^$(escape_key "$email"):" "$QUOTAS_FILE" 2>/dev/null | cut -d: -f2)
            limit=$(numfmt --from=iec --to=iec "$limit" 2>/dev/null) || limit=""
            line="* $email ( 0 / ${limit:-~} ) [0%]"
        elif [[ "$line" =~ ^\*\ ([^ ]+)\ \(\ ([^ ]+)\ /\ ([^ ]+)\ \)\ \[[0-9]+%\]$ ]]; then
            # a changed quota also shows the old limit for a few seconds, the quotas file already has the new one
            email="${BASH_REMATCH[1]}" used="${BASH_REMATCH[2]}"
            limit=$(grep -m1 "^$(escape_key "$email"):" "$QUOTAS_FILE" 2>/dev/null | cut -d: -f2)
            if [[ -n "$limit" ]]; then
                used_b=$(numfmt --from=iec "$used" 2>/dev/null) limit_b=$(numfmt --from=iec "$limit" 2>/dev/null)
                if [[ "$used_b" =~ ^[0-9]+$ && "$limit_b" =~ ^[0-9]+$ && "$limit_b" -gt 0 ]]; then
                    line="* $email ( $used / $(numfmt --to=iec "$limit_b") ) [$(( used_b * 100 / limit_b ))%]"
                fi
            fi
        fi
        printf '%s\n' "$line"
    done
}

reload_emails_data_file_for_user() {
    [[ -z "$owner" ]] && return
    local file_to_refresh="/etc/openpanel/openpanel/core/users/$owner/emails.yml"
    local all_domains all_emails
    all_domains=$(opencli domains-user "$owner")
    all_emails=$(opencli email-setup email list 2>/dev/null)
    : > "$file_to_refresh"
    while IFS= read -r domain; do
        grep "@${domain}" <<< "$all_emails" >> "$file_to_refresh"
    done <<< "$all_domains"
    # the panel memoizes the list from this file for 1h (GetEmailList), drop it so /emails shows the change now
    redis_drop_key "openpanel_cache_email_list:${owner}"
}


# Escapes '.' for use in a sed key pattern (mirrors docker-mailserver's own _escape()).
escape_key() {
    echo "${1//./\\.}"
}

# Escapes '/' and '&' for use as a sed replacement value.
escape_value() {
    sed 's/\([\/\&]\)/\\\1/g' <<< "$1"
}

# reads the mailserver image out of compose.yml, so the offline password hasher below uses whatever's actually deployed
get_mailserver_image() {
    awk '
        /^  mailserver:/ { in_ms=1; next }
        /^  [a-zA-Z]/    { in_ms=0 }
        in_ms && /^ *image:/ { print $2; exit }
    ' "$COMPOSE_FILE" 2>/dev/null
}

hash_password() {
    local email="$1" password="$2" image
    image="$(get_mailserver_image)"
    if [[ -z "$image" ]]; then
        echo "Error: could not determine mailserver image from '$COMPOSE_FILE'" >&2
        return 1
    fi
    timeout 30 podman run --rm --entrypoint doveadm "$image" pw -s SHA512-CRYPT -u "$email" -p "$password" 2>/dev/null
}

offline_email_add() {
    # lowercased like docker-mailserver's own 'email add' does, dovecot looks the address up lowercased
    local email="${1,,}" password="$2" hash key
    is_valid_email "$email" || { echo "Error: '$email' is not a valid email address" >&2; return 1; }
    mkdir -p "$CONFIG_DIR"
    touch "$ACCOUNTS_FILE"
    key="$(escape_key "$email")"
    if grep -qi "^${key}|" "$ACCOUNTS_FILE"; then
        echo "Error: '$email' already exists" >&2
        return 1
    fi
    hash="$(hash_password "$email" "$password")"
    if [[ -z "$hash" ]]; then
        echo "Error: failed to hash password for '$email'" >&2
        return 1
    fi
    echo "${email}|${hash}" >> "$ACCOUNTS_FILE"
}

offline_email_update() {
    local email="$1" password="$2" hash key value
    is_valid_email "$email" || { echo "Error: '$email' is not a valid email address" >&2; return 1; }
    key="$(escape_key "$email")"
    if ! grep -qi "^${key}|" "$ACCOUNTS_FILE" 2>/dev/null; then
        echo "Error: '$email' does not exist" >&2
        return 1
    fi
    hash="$(hash_password "$email" "$password")"
    if [[ -z "$hash" ]]; then
        echo "Error: failed to hash password for '$email'" >&2
        return 1
    fi
    value="$(escape_value "${email}|${hash}")"
    sed -i "s/^${key}|.*/${value}/" "$ACCOUNTS_FILE"
}

offline_email_del() {
    local maildel=0 email key user domain
    case "$1" in
        -y|-Y) maildel=1; shift ;;
        -n|-N) maildel=0; shift ;;
    esac
    [[ -f "$ACCOUNTS_FILE" ]] || return 0
    for email in "$@"; do
        key="$(escape_key "$email")"
        sed -i "/^${key}|/Id" "$ACCOUNTS_FILE"
        [[ -f "$QUOTAS_FILE" ]] && sed -i "/^${key}:/Id" "$QUOTAS_FILE"
        if [[ "$maildel" -eq 1 ]]; then
            user="${email%@*}"
            domain="${email#*@}"
            rm -rf -- "/var/mail/${domain}/${user}"
        fi
    done
}

offline_email_list() {
    [[ -s "$ACCOUNTS_FILE" ]] || return 0
    grep -Ev '^\s*($|#)' "$ACCOUNTS_FILE" | while IFS='|' read -r login _; do
        [[ -n "$login" ]] && printf '* %s\n\n' "$login"
    done
}

offline_quota_set() {
    local email="$1" quota="$2" key value
    is_valid_email "$email" || { echo "Error: '$email' is not a valid email address" >&2; return 1; }
    key="$(escape_key "$email")"
    if ! grep -qi "^${key}|" "$ACCOUNTS_FILE" 2>/dev/null; then
        echo "Error: '$email' does not exist" >&2
        return 1
    fi
    touch "$QUOTAS_FILE"
    value="$(escape_value "${email}:${quota}")"
    if grep -qi "^${key}:" "$QUOTAS_FILE"; then
        sed -i "s/^${key}:.*/${value}/" "$QUOTAS_FILE"
    else
        echo "${email}:${quota}" >> "$QUOTAS_FILE"
    fi
}

offline_quota_del() {
    local email="$1" key
    key="$(escape_key "$email")"
    [[ -f "$QUOTAS_FILE" ]] && sed -i "/^${key}:/Id" "$QUOTAS_FILE"
}

# best-effort: account exists on disk now, so try bringing the container up in the background so it stops hitting the "no accounts" shutdown on next boot, not awaited
try_start_mailserver_in_background() {
    nohup bash -c '. /usr/local/opencli/lib/podman.sh; podman_ensure_running "$1" "$2" "$3" "$4"' \
        _ "$CONTAINER" "$MAIL_DIR" mailserver 60 >/dev/null 2>&1 &
}

# runs the setup command against the running container, or straight against the config files (offline_* above) if it's not up
run_setup_command() {
    if podman_is_running "$CONTAINER"; then
        podman exec "$CONTAINER" setup "$@"
        return $?
    fi

    echo "Warning: '$CONTAINER' is not running - writing directly to mailserver config files instead." >&2

    case "$1 $2" in
        "email add")
            shift 2
            offline_email_add "$@" && try_start_mailserver_in_background
            ;;
        "email update")
            shift 2
            offline_email_update "$@"
            ;;
        "email del")
            shift 2
            offline_email_del "$@"
            ;;
        "email list")
            offline_email_list
            ;;
        "quota set")
            shift 2
            offline_quota_set "$@"
            ;;
        "quota del")
            shift 2
            offline_quota_del "$@"
            ;;
        *)
            echo "Error: '$1 $2' requires the mailserver container to be running, and it currently is not." >&2
            return 1
            ;;
    esac
}


# drops a setup guide straight into the new mailbox with doveadm, nothing goes out over smtp
send_welcome_message() {
    local email="$1" mailbox_domain="${1#*@}" template="/etc/openpanel/openpanel/custom_code/welcome.html" mail_host webmail_url body i uid
    [[ -f "$template" ]] || return 1

    # wait for dovecot to pick up the account with the right uid, otherwise the mailbox gets created as the wrong owner
    for i in $(seq 1 45); do
        uid=$(podman exec "$CONTAINER" doveadm user -f uid "$email" 2>/dev/null) || uid=""
        [[ -n "$uid" && ( -z "$OP_UID" || "$uid" == "$OP_UID" ) ]] && break
        sleep 2
    done
    [[ -n "$uid" ]] || return 1

    # same host and ports as /emails/info in the panel
    local imap_port=143 smtp_port=587 security="STARTTLS"
    mail_host=$(opencli domain 2>/dev/null)
    if [[ -f "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/${mail_host}/${mail_host}.crt" || -f "/etc/openpanel/caddy/ssl/custom/${mail_host}/${mail_host}.crt" ]]; then
        imap_port=993 smtp_port=465 security="SSL/TLS"
    fi
    webmail_url=$(awk '/^redir @webmail/ {print $3; exit}' /etc/openpanel/caddy/redirects.conf 2>/dev/null)
    [[ -n "$webmail_url" ]] || webmail_url="http://${mail_host}:8080/"

    body=$(<"$template")
    body=${body//\{\{EMAIL\}\}/$email}
    body=${body//\{\{DOMAIN\}\}/$mailbox_domain}
    body=${body//\{\{MAIL_HOST\}\}/$mail_host}
    body=${body//\{\{WEBMAIL_URL\}\}/$webmail_url}
    body=${body//\{\{IMAP_PORT\}\}/$imap_port}
    body=${body//\{\{SMTP_PORT\}\}/$smtp_port}
    body=${body//\{\{SECURITY\}\}/$security}

    {
        printf 'From: Mail Setup <postmaster@%s>\r\n' "$mailbox_domain"
        printf 'To: <%s>\r\n' "$email"
        printf 'Subject: Set up %s on your devices\r\n' "$email"
        printf 'Date: %s\r\n' "$(date -R)"
        printf 'Message-ID: <welcome.%s.%s@%s>\r\n' "$(date +%s)" "$RANDOM" "$mailbox_domain"
        printf 'MIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n'
        printf '%s\r\n' "$body"
    } | podman exec -i "$CONTAINER" doveadm save -u "$email"
}


# ======================================================================
# Run setup command
validate_first

# --send-welcome is ours, keep it away from the mailserver's setup and keep the other args in place
SEND_WELCOME=false
args=()
for arg in "$@"; do
    if [[ "$arg" == "--send-welcome" ]]; then SEND_WELCOME=true; else args+=("$arg"); fi
done
set -- "${args[@]}"

command=("$@")
# https://docker-mailserver.github.io/docker-mailserver/latest/config/setup.sh/
if [[ "$1 $2" == "email list" ]]; then
    run_setup_command "${command[@]}" | fill_new_account_quotas
    setup_rc=${PIPESTATUS[0]}
else
    run_setup_command "${command[@]}"
    setup_rc=$?
fi

if [[ "$1" == "email" && "$2" =~ ^(add|update|del)$ ]] || [[ "$1" == "quota" && "$2" =~ ^(set|del)$ ]]; then
    target_email="$3"
    # 'email del -y|-n <email>': the address comes after the flag
    [[ "$1 $2" == "email del" && "$3" =~ ^-[yYnN]$ ]] && target_email="$4"
    # docker-mailserver stores 'Name@domain' as 'name@domain', so the uid line and the maildir use that form
    target_email="${target_email,,}"
    if is_valid_email "$target_email"; then
        # get OpenPanel user UID and store/update it in postfix-accounts.cf
        get_openpanel_username_and_uid_for_domain "$target_email"
        if [[ "$2" =~ ^(add|update)$ && -n "$OP_UID" && "$OP_UID" =~ ^[0-9]+$ ]]; then
            sed -i "/^$(escape_key "$target_email")|/ { s/^\([^|]*|[^|]*\).*/\1|$OP_UID/}" "$ACCOUNTS_FILE"
            nohup timeout 300 podman exec "$CONTAINER" bash -c "chown -R \"${OP_UID}:${OP_UID}\" \"/var/mail/${domain}/${user}\"" &
        fi

        # reload the cached user file so the OpenPanel UI picks up email/quota add/del changes
        if [[ "$2" != "update" && "$5" != "--wait" ]]; then
            reload_emails_data_file_for_user
        fi
    fi
fi

if [[ "$SEND_WELCOME" == true && "$1" == "email" && "$2" == "add" && "$setup_rc" -eq 0 ]] && is_valid_email "$3" && podman_is_running "$CONTAINER"; then
    # panel treats any stdout as failure, so this runs detached and silent
    send_welcome_message "$3" </dev/null >/dev/null 2>&1 &
    disown
fi
