#!/bin/bash
################################################################################
# Script Name: backup/list.sh
# Description: List all available backups for a user
# Usage: opencli backup-list <USERNAME> [--debug|--json]
# Author: Stefan Pejcic
# Created: 02.02.2024
# Last Modified: 11.03.2025
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



# Function to print output in JSON format
print_json() {
    local backup_job_id=$1
    local username=$2
    local index_files=$3

    cat <<EOF
{
  "job": "$backup_job_id",
  "username": "$username",
  "date": [$index_files]
}
EOF
}


DEBUG=false
JSON_FLAG=false

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
if [ "$#" -lt 1 ]; then
    echo "Usage: opencli backup-list <USERNAME>"
    exit 1
fi


USERNAME=$1
SEARCH_DIR="/etc/openpanel/openadmin/config/backups/index"

# Check if the main directory exists
if [ ! -d "$SEARCH_DIR" ]; then
    echo "Main directory not found: $SEARCH_DIR"
    exit 1
fi

# Find folders with the provided username
user_folders=$(find "$SEARCH_DIR" -type d -name "$USERNAME")

# Check if any matching folders are found
if [ -z "$user_folders" ]; then
    echo "No folders found for user: $USERNAME"
    exit 1
fi

# Variable to accumulate JSON output
json_output=""

# Iterate through each matching folder
for folder in $user_folders; do
    # Extract Backup job ID and Username from the directory path
    backup_job_id=$(echo "$folder" | awk -F'/' '{print $(NF-1)}')
    username=$(echo "$folder" | awk -F'/' '{print $NF}')

    # List .index files in the current folder
    index_files=$(find "$folder" -type f -name "*.index" -exec basename {} \; | sed 's/\.index$//' | paste -sd ',' -)

    # Check if --json option is passed and append to JSON output
    if $JSON_FLAG; then
        json_output+=$(print_json "$backup_job_id" "$username" "$index_files")
        json_output+=","
    else
        echo "Backup job ID: $backup_job_id"
        echo "Username: $username"

        # Check if any .index files are found
        if [ -z "$index_files" ]; then
            echo "No backups found under job id: $folder"
        else
            echo "Dates: $index_files"
        fi

        echo "-----------------------------"
    fi
done

# Remove the trailing comma in JSON output if present
json_output=${json_output%,}

# Print the accumulated JSON output
if $JSON_FLAG; then
    echo "[$json_output]"
fi
