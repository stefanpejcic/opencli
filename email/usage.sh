#!/bin/bash
################################################################################
# Script Name: email/usage.sh
# Description: Refresh the cached email accounts and quota usage file for users.
# Usage: opencli email-usage <USERNAME|--all>
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

readonly CONTAINER=openadmin_mailserver
readonly USERS_DIR="/etc/openpanel/openpanel/core/users"

# shellcheck disable=SC1091
. /usr/local/opencli/lib/podman.sh

usage() {
    echo "Usage: opencli email-usage <USERNAME|--all>"
    echo "Docs: https://openpanel.com/docs/articles/opencli/email"
    exit 1
}

key_value=$(grep "^key=" -- "/etc/openpanel/openpanel/conf/openpanel.config" | cut -d'=' -f2-)
if [ -z "$key_value" ]; then
    echo "Error: OpenPanel Community edition does not support emails. Please consider purchasing the Enterprise version that allows unlimited number of email addresses."
    # shellcheck disable=SC1091
    . /usr/local/opencli/lib/enterprise.sh
    echo "$ENTERPRISE_LINK"
    exit 1
fi

if ! podman_is_running "$CONTAINER"; then
    echo "Error: '$CONTAINER' is not running, start it with: opencli email-server start"
    exit 1
fi

[ "$#" -ne 1 ] && usage

# one list for all users, it includes usage and quota for each mailbox
all_emails=$(timeout 60 podman exec "$CONTAINER" setup email list)
if [ $? -ne 0 ]; then
    echo "Error: failed to list email accounts from '$CONTAINER'."
    exit 1
fi

refresh_user() {
    local username="$1"
    local file="$USERS_DIR/$username/emails.yml"
    local domains tmp

    if [ ! -d "$USERS_DIR/$username" ]; then
        echo "Error: no data directory for user '$username' in $USERS_DIR"
        return 1
    fi

    domains=$(opencli domains-user "$username")
    # skip so we don't wipe the file for suspended or deleted users
    if [[ "$domains" == "User '"*"' not found in the database." ]]; then
        echo "Skipped $username: user not found in the database."
        return 1
    fi
    [[ "$domains" == "No domains found for user"* ]] && domains=""

    # write to a temp file first so the UI never reads a half-written file
    tmp=$(mktemp "$file.XXXXXX") || return 1
    if [ -f "$file" ]; then
        chmod --reference="$file" "$tmp"
        chown --reference="$file" "$tmp"
    else
        chmod 644 "$tmp"
    fi
    while IFS= read -r domain; do
        [ -n "$domain" ] && grep "@${domain}" <<< "$all_emails" >> "$tmp"
    done <<< "$domains"
    mv -f "$tmp" "$file"

    echo "Updated $file ($(grep -c '^\*' "$file") email accounts)"
}

if [ "$1" == "--all" ]; then
    for dir in "$USERS_DIR"/*/; do
        [ -d "$dir" ] || continue
        refresh_user "$(basename "$dir")"
    done
    exit 0
fi

[[ "$1" == -* ]] && usage
refresh_user "$1"
