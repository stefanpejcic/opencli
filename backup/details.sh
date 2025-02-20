#!/bin/bash
################################################################################
# Script Name: backup/details.sh
# Description: List details about a specific backup for a user.
# Usage: opencli backup-details <USERNAME> <BACKUP_JOB_ID> <DATE> [--json]
# Author: Stefan Pejcic
# Created: 02.02.2024
# Last Modified: 20.02.2025
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
JSON_FLAG=false

# Function to print output in JSON format
print_json() {
    local file_content=$1

    # Parse file content into JSON
    json_output="{"
    while IFS= read -r line; do
        key=$(echo "$line" | cut -d '=' -f1)
        value=$(echo "$line" | cut -d '=' -f2-)
        json_output+="\"$key\":\"$value\","
    done <<< "$file_content"
    json_output=${json_output%,}  # Remove trailing comma
    json_output+="}"

    echo "$json_output"
}

for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        --json)
            JSON_FLAG=true
            DEBUG=false
            ;;
    esac
done

# Check if the correct number of command line arguments is provided
if [ "$#" -lt 2 ]; then
    echo "Usage: opencli backup-list <USERNAME> <JOB_ID> <DATE>"
    exit 1
fi

USERNAME=$1
JOB_ID=$2
DATE=$3
index_file_path="/etc/openpanel/openadmin/config/backups/index/$JOB_ID/$USERNAME/$DATE.index"

# Check if the index file exists
if [ ! -f "$index_file_path" ]; then
    echo "Index file not found: $index_file_path"
    exit 1
fi

file_content=$(cat "$index_file_path")

# Print the content in JSON format if the --json option is passed
if $JSON_FLAG; then
    json_output=$(print_json "$file_content")
    echo "$json_output"
else
    # Print the content as-is
    echo "$file_content"
fi
