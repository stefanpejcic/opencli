#!/bin/bash
################################################################################
# Script Name: user/error_report.sh
# Description: Email users the errors found in their websites' logs in the last 24 hours.
# Usage: opencli user-error_report <USERNAME> [--dry-run] OR opencli user-error_report --all [--dry-run]
# Author: Stefan Pejcic
# Created: 15.10.2026
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

# shellcheck disable=SC1091
source /usr/local/opencli/lib/requirement.sh
require_command jq
# shellcheck disable=SC1091
. /usr/local/opencli/lib/email.sh

readonly HOURS=24
readonly CONF_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
readonly USERS_DIR="/etc/openpanel/openpanel/core/users"

usage() {
    echo "Usage: opencli user-error_report <USERNAME> [--dry-run] OR opencli user-error_report --all [--dry-run]"
    echo ""
    echo "Emails each user who turned on 'Daily error report' in OpenPanel > Account > Notifications"
    echo "the errors found in their websites' logs in the last ${HOURS} hours, at most once a day."
    echo "--dry-run prints the email instead of sending it."
    exit 1
}

TARGET="" DRY_RUN=false
for arg in "$@"; do
    case "$arg" in
        --dry-run) DRY_RUN=true ;;
        -h|--help) usage ;;
        *) [[ -z "$TARGET" ]] && TARGET="$arg" || usage ;;
    esac
done
[[ -n "$TARGET" ]] || usage

module_enabled() {
    grep -E '^enabled_modules=' "$CONF_FILE" 2>/dev/null | grep -qE "[\",]$1[\",]"
}

# off by default, so unlike most notifications a missing key means no
opted_in() {
    grep -qx "notify_error_logs=1" "$USERS_DIR/$1/notifications.yaml" 2>/dev/null
}

report_user() {
    local username="$1" today state digest total body tips
    [[ "$username" =~ ^[A-Za-z0-9_.-]+$ ]] || { echo "  Invalid username: $username"; return 1; }

    if ! opted_in "$username"; then
        echo "  $username has the daily error report turned off."
        return 0
    fi

    today=$(date +%Y-%m-%d)
    state="$USERS_DIR/$username/.error_report_sent"
    if ! $DRY_RUN && [[ "$(cat "$state" 2>/dev/null)" == "$today" ]]; then
        echo "  $username already got today's report."
        return 0
    fi

    # the panel collects the errors, same parsing as the Error Logs page
    if ! digest=$(timeout 300 podman exec openpanel /openpanel error-logs-digest "$username" --hours "$HOURS" 2>/dev/null) \
        || ! jq -e . >/dev/null 2>&1 <<< "$digest"; then
        echo "  Could not read the error logs for $username."
        return 1
    fi

    if [[ "$(jq -r '.allowed' <<< "$digest")" != "true" ]]; then
        echo "  $username doesn't have the Error Logs feature."
        return 0
    fi

    total=$(jq -r '.total' <<< "$digest")
    if [[ "$total" == "0" ]]; then
        echo "  No errors for $username in the last ${HOURS} hours."
        return 0
    fi

    body="In the last ${HOURS} hours, ${total} error(s) were found in the logs of account ${username}."$'\n\n'
    body+="By website:"$'\n'
    body+=$(jq -r '.websites[] | "- \(if .website == "" then "other services" else .website end): \(.count)"' <<< "$digest")$'\n\n'
    body+="Latest errors:"$'\n'
    body+=$(jq -r '.entries[] | "- \(.time[0:16] | sub("T"; " ")) UTC [\(.level)] \(.service)\(if .website then " (\(.website))" else "" end): \(.message)"' <<< "$digest")
    (( total > $(jq '.entries | length' <<< "$digest") )) && body+=$'\n'"- and $(( total - $(jq '.entries | length' <<< "$digest") )) more"
    tips="Open Error Logs in OpenPanel to see the full messages and stack traces, filter by website and copy them for your developer or hosting support. To stop these emails, turn off 'Daily error report' in Account > Notifications."

    if $DRY_RUN; then
        echo "  To: $(user_email_address "$username")"
        echo "  Subject: Daily error report for account $username"
        sed 's/^/  /' <<< "$body"
        return 0
    fi

    if send_user_email "$username" "Daily error report for account $username" "$body" notify_error_logs "" "" "" "$tips"; then
        echo "$today" > "$state"
        echo "  $username notified by email about $total error(s)."
    else
        echo "  Could not email $username."
    fi
}

if ! module_enabled error_logs; then
    echo "The Error Logs module is not enabled, nothing to send."
    exit 0
fi

if [[ "$TARGET" == "--all" ]]; then
    users=$(opencli user-list --json | grep -v 'SUSPENDED' | awk -F'"' '/username/ {print $4}')
    if [[ -z "$users" || "$users" == "No users." ]]; then
        echo "No users found in the database."
        exit 0
    fi
    for user in $users; do
        echo "Checking $user"
        report_user "$user"
    done
else
    report_user "$TARGET"
fi
