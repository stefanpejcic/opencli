#!/bin/bash
################################################################################
# Script Name: update_check.sh
# Description: Checks if an update is available from update.openpanel.co servers.
# Usage: opencli update_check
# Author: Stefan Pejcic
# Created: 10.10.2023
# Last Modified: 15.11.2023
# Company: openpanel.co
# Copyright (c) openpanel.co
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


LOG_FILE="/var/log/openpanel/admin/notifications.log"

# Function to get the last message content from the log file
get_last_message_content() {
  tail -n 1 "$LOG_FILE" 2>/dev/null
}

# Function to check if an unread message with the same content exists in the log file
is_unread_message_present() {
  local unread_message_content="$1"
  grep -q "UNREAD.*$unread_message_content" "$LOG_FILE" && return 0 || return 1
}
# Function to write notification to log file if it's different from the last message content
write_notification() {
  local title="$1"
  local message="$2"
  local current_message="$(date '+%Y-%m-%d %H:%M:%S') UNREAD $title MESSAGE: $message"
  local last_message_content=$(get_last_message_content)

  # Check if the current message content is the same as the last one and has "UNREAD" status
  if [ "$message" != "$last_message_content" ] && ! is_unread_message_present "$title"; then
    echo "$current_message" >> "$LOG_FILE"
  fi
}











# Define the route to check for updates
update_check() {
    # Read the local version from /usr/local/panel/version
    if [ -f "/usr/local/panel/version" ]; then
        local_version=$(cat "/usr/local/panel/version")
    else
        echo '{"error": "Local version file not found"}' >&2
        exit 1
    fi

    # Fetch the remote version from https://update.openpanel.co/
    #remote_version=$(curl -s "https://update.openpanel.co/")
    remote_version=$(curl -s "https://update.openpanel.co/" | tr -d '\r')

    if [ -z "$remote_version" ]; then
        echo '{"error": "Error fetching remote version"}' >&2
        write_notification "Update check failed" "Failed connecting to https://update.openpanel.co/"
        exit 1
    fi

    # Compare the local and remote versions
    if [ "$local_version" == "$remote_version" ]; then
        echo '{"status": "Up to date", "installed_version": "'"$local_version"'"}'
    elif [ "$local_version" \> "$remote_version" ]; then
        #write_notification "New OpenPanel update is available" "Installed version: $local_version | Available version: $remote_version"
        echo '{"status": "Local version is greater", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
    else
        # Check if skip_versions file exists and if remote version matches
        if [ -f "/etc/openpanel/upgrade/skip_versions" ]; then
            if grep -q "$remote_version" "/etc/openpanel/upgrade/skip_versions"; then
                echo '{"status": "Skipped version", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
                exit 0
            fi
        fi
        write_notification "New OpenPanel update is available" "Installed version: $local_version | Available version: $remote_version"
        echo '{"status": "Update available", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
    fi
}

# Call the function and print the result
update_check
