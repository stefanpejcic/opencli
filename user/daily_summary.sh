#!/bin/bash
################################################################################
# Script Name: user/daily_summary.sh
# Description: Save yesterday's summary for users with the Summary module and email it to those who turned it on.
# Usage: opencli user-daily_summary <USERNAME> [--date YYYY-MM-DD] [--dry-run] OR opencli user-daily_summary --all [--date YYYY-MM-DD] [--dry-run]
# Author: Stefan Pejcic
# Created: 16.10.2026
# Last Modified: 16.10.2026
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

readonly CONF_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
readonly USERS_DIR="/etc/openpanel/openpanel/core/users"

usage() {
    echo "Usage: opencli user-daily_summary <USERNAME> [--date YYYY-MM-DD] [--dry-run] OR opencli user-daily_summary --all [--date YYYY-MM-DD] [--dry-run]"
    echo ""
    echo "Saves the day's summary (yesterday by default) for every user with the Summary module,"
    echo "shown in OpenPanel > Statistics > Daily Summary, and emails it to users who turned on"
    echo "'Daily summary' in Account > Notifications, at most once a day."
    echo "--dry-run prints the email instead of saving and sending it."
    exit 1
}

TARGET="" DATE="" DRY_RUN=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run) DRY_RUN=true ;;
        --date)
            [[ "${2:-}" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || { echo "ERROR: --date needs a date like 2026-10-15."; exit 1; }
            DATE="$2"; shift ;;
        -h|--help) usage ;;
        *) [[ -z "$TARGET" ]] && TARGET="$1" || usage ;;
    esac
    shift
done
[[ -n "$TARGET" ]] || usage

if ! grep -E '^enabled_modules=' "$CONF_FILE" 2>/dev/null | grep -qE '[",]summary[",]'; then
    echo "The Summary module is not enabled, nothing to do."
    exit 0
fi

# the panel always serves https, with a self-signed certificate when the domain has none
BASE_URL="https://$(opencli domain 2>/dev/null):$(opencli port 2>/dev/null)/"

summarize_user() {
    local username="$1" args out sent_state
    [[ "$username" =~ ^[A-Za-z0-9_.-]+$ ]] || { echo "  Invalid username: $username"; return 1; }

    args=(daily-summary "$username" --base-url "$BASE_URL")
    [[ -n "$DATE" ]] && args+=(--date "$DATE")
    $DRY_RUN && args+=(--no-save)

    # the panel builds and saves the summary, same data as the Statistics pages
    if ! out=$(timeout 600 podman exec openpanel /openpanel "${args[@]}" 2>/dev/null) || ! jq -e . >/dev/null 2>&1 <<< "$out"; then
        echo "  Could not build the summary for $username."
        return 1
    fi
    if [[ "$(jq -r '.allowed' <<< "$out")" != "true" ]]; then
        echo "  $username doesn't have the Summary module."
        return 0
    fi
    echo "  $(jq -r '"Summary for \(.date): \(.attention) need attention, \(.look) worth a look"' <<< "$out")$($DRY_RUN || echo ", saved")."

    if $DRY_RUN; then
        echo "  Subject: $(jq -r '.subject' <<< "$out")"
        jq -r '.body' <<< "$out" | sed 's/^/  /'
        return 0
    fi

    # off by default, so a missing key means no
    grep -qx "notify_daily_summary=1" "$USERS_DIR/$username/notifications.yaml" 2>/dev/null || return 0

    sent_state="$USERS_DIR/$username/.daily_summary_sent"
    if [[ "$(cat "$sent_state" 2>/dev/null)" == "$(jq -r '.date' <<< "$out")" ]]; then
        echo "  $username already got this summary."
        return 0
    fi
    # disk and inode use come along as bars, drawn by OpenAdmin's email template
    if send_user_email "$username" "$(jq -r '.subject' <<< "$out")" "$(jq -r '.body' <<< "$out")" notify_daily_summary "$(jq -c '.usage // []' <<< "$out")" "" "" \
        "To stop these emails, turn off 'Daily summary' in Account > Notifications. The summaries stay in Statistics > Daily Summary either way."; then
        jq -r '.date' <<< "$out" > "$sent_state"
        echo "  $username notified by email."
    else
        echo "  Could not email $username."
    fi
}

if [[ "$TARGET" == "--all" ]]; then
    users=$(opencli user-list --json | grep -v 'SUSPENDED' | awk -F'"' '/username/ {print $4}')
    if [[ -z "$users" || "$users" == "No users." ]]; then
        echo "No users found in the database."
        exit 0
    fi
    for user in $users; do
        echo "Checking $user"
        summarize_user "$user"
    done
else
    summarize_user "$TARGET"
fi
