#!/bin/bash
################################################################################
# Script Name: email/setup.sh
# Description: Setup email addresses, forwarders, filters..
# Usage: opencli email-setup <COMMAND> <ATTRIBUTES>
# Docs: https://docs.openpanel.com
# Author: Stefan Pejcic
# Created: 18.08.2024
# Last Modified: 04.09.2026
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
    local whoowns_output owner
    whoowns_output=$(opencli domains-whoowns "$domain" --context)
    read -r _ owner <<< "$whoowns_output"
    [[ -n "$owner" ]] && OP_UID=$(stat -c '%u' "/home/$owner")
}

reload_emails_data_file_for_user() {
    [[ -z "$owner" ]] && return
    local file_to_refresh="/etc/openpanel/openpanel/core/users/$owner/emails.yml"
    local all_domains all_emails
    all_domains=$(opencli domains-user "$owner")
    sleep 2 # for https://github.com/docker-mailserver/docker-mailserver/blob/cb76075f25e22476e8fdb45adfbea8026d4ea898/target/bin/addmailuser#L16
    all_emails=$(opencli email-setup email list)
    : > "$file_to_refresh"
    while IFS= read -r domain; do
        grep "@${domain}" <<< "$all_emails" >> "$file_to_refresh"
    done <<< "$all_domains"
}

# ======================================================================
# Offline fallback (writes directly to docker-mailserver's config files)
#
# docker-mailserver refuses to keep running with zero mail accounts: it
# shuts Dovecot down ~2 minutes after boot if none exist. That means a
# freshly provisioned mailserver can never accept the `podman exec` call
# that would create its first account - the container isn't up long
# enough to catch it. See: https://github.com/stefanpejcic/openpanel/issues/1101
#
# When the container isn't running, write straight to the same config
# files docker-mailserver itself reads on startup (bind-mounted at
# $CONFIG_DIR), using the exact file formats from its own accounts.sh/
# db.sh helpers (pipe-delimited "email|hash" in postfix-accounts.cf,
# colon-delimited "email:quota" in dovecot-quotas.cf). This breaks the
# deadlock: the account exists before the container ever starts.
# ======================================================================

# Escapes '.' for use in a sed key pattern (mirrors docker-mailserver's own _escape()).
escape_key() {
    echo "${1//./\\.}"
}

# Escapes '/' and '&' for use as a sed replacement value.
escape_value() {
    sed 's/\([\/\&]\)/\\\1/g' <<< "$1"
}

# Reads the mailserver image out of compose.yml, so the offline password
# hasher below uses whatever image is actually deployed.
get_mailserver_image() {
    awk '
        /^  mailserver:/ { in_ms=1; next }
        /^  [a-zA-Z]/    { in_ms=0 }
        in_ms && /^ *image:/ { print $2; exit }
    ' "$COMPOSE_FILE" 2>/dev/null
}

# Hashes a password the same way docker-mailserver's setup.sh does internally
# (`doveadm pw -s SHA512-CRYPT`), using a throwaway container built from the
# already-pulled mailserver image - so it works even while the persistent
# container is stopped.
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
    local email="$1" password="$2" hash key
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

# Best-effort: now that an account exists on disk, try bringing the
# container up in the background so it stops hitting the "no accounts"
# shutdown on its next boot. Not awaited - the caller shouldn't block on it.
try_start_mailserver_in_background() {
    nohup bash -c '. /usr/local/opencli/lib/podman.sh; podman_ensure_running "$1" "$2" "$3" "$4"' \
        _ "$CONTAINER" "$MAIL_DIR" mailserver 60 >/dev/null 2>&1 &
}

# Runs the requested setup command against the running container, or - if
# the container isn't up - against the config files directly (offline_* above).
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

# ======================================================================
# Run setup command
validate_first
command=("$@")
# https://docker-mailserver.github.io/docker-mailserver/latest/config/setup.sh/
run_setup_command "${command[@]}"

if [[ "$1" == "email" && "$2" =~ ^(add|update|del)$ ]] || [[ "$1" == "quota" && "$2" =~ ^(set|del)$ ]]; then
    if is_valid_email "$3"; then
        # get OpenPanel user UID and store/update it in postfix-accounts.cf
        get_openpanel_username_and_uid_for_domain "$3"
        if [[ "$2" =~ ^(add|update)$ && -n "$OP_UID" && "$OP_UID" =~ ^[0-9]+$ ]]; then
            sed -i "/^$3|/ { s/^\([^|]*|[^|]*\).*/\1|$OP_UID/}" "$ACCOUNTS_FILE"
            # TODO: after 2.0 edit to only run on 'add' and not on 'update'!
            nohup timeout 300 podman exec "$CONTAINER" bash -c "chown -R \"${OP_UID}:${OP_UID}\" \"/var/mail/${domain}/${user}\"" &
        fi

        # if email add/del OR quita set/del then we need to reload the cached user file for OpenPanel UI to display to user
        if [[ "$2" != "update" && "$5" != "--wait" ]]; then
            reload_emails_data_file_for_user
        fi
    fi
fi
