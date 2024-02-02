#!/bin/bash
################################################################################
# Script Name: backup/scheduler.sh
# Description: Schedule backup jobs and execute them in time.
# Usage: opencli backup-schedule [--debug]
# Author: Stefan Pejcic
# Created: 02.02.2024
# Last Modified: 02.02.2024
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

DEBUG=false

# Directory containing JSON files
json_dir="/usr/local/admin/backups/jobs/"

for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
    esac
done


# remove previous backup schedules
sed -i '/opencli backup-run/d' /etc/crontab

# Loop through each JSON file in the directory
for file in "$json_dir"*.json; do
    # Check if the file is a regular file and has a status "on"
    if [ -f "$file" ] && grep -q '"status": "on"' "$file"; then
        # Extract destination, schedule, type, and filters values
        destination=$(jq -r '.destination' "$file")
        schedule=$(jq -r '.schedule' "$file")
        type=$(jq -r '.type | .[]' "$file")
        filters=$(jq -r '.filters | .[]' "$file")

        # Convert schedule to cron format
        case "$schedule" in
            "hourly")
                cron_schedule="0 * * * *"
                ;;
            "daily")
                cron_schedule="0 1 * * *"
                ;;
            "weekly")
                cron_schedule="0 1 * * SUN"
                ;;
            "monthly")
                cron_schedule="0 0 1 * *"
                ;;
            *)
                echo "Invalid schedule value in $file"
                continue
                ;;
        esac

        # Determine flag based on type value
        if [[ "$type" =~ "configuration" ]]; then
            flag="--conf"
        elif [[ "$type" =~ "accounts" ]]; then
            # If there are filters, add flags for each filter; otherwise, add --all
            if [ -n "$filters" ]; then
                IFS=',' read -ra filter_array <<< "$filters"
                for filter in "${filter_array[@]}"; do
                    flag+=" --${filter// /}"  # Strip spaces
                done
            else
                flag="--all"
            fi
        else
            echo "Invalid type value in $file"
            continue
        fi

        if [ "$DEBUG" = true ]; then
        echo "$cron_schedule opencli backup-run $(basename "$file" .json) $flag"
        fi
        echo "$cron_schedule opencli backup-run $(basename "$file" .json) $flag" >> /etc/crontab
    fi
done
