#!/bin/bash
################################################################################
# Script Name: jobs.sh
# Description: Create, edit, delete, backup jobs.
# Usage: opencli backup-jobs create|edit|delete
# Author: Radovan Jecmenica
# Created: 29.01.2024
# Last Modified: 29.01.2024
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

# Define paths
backup_dir="/usr/local/admin/backups/jobs/"

# Define functions
error() {
    echo -e "\033[41;97m$1\033[0m"
}

success() {
    echo -e "\033[42;97m$1\033[0m"
}

# Function to find the last number in existing .json files
get_last_number() {
    local last_number=$(ls -1 "$backup_dir"*.json 2>/dev/null | grep -oP '\d+' | sort -n | tail -n 1)
    echo "$last_number"
}

# Create a backup job
create_backup_job() {
    local last_number=$(get_last_number)
    local new_number=$((last_number + 1))
    local new_file="${backup_dir}${new_number}.json"

    # Check if enough parameters are provided
    if [ "$#" -ne 7 ]; then
        error "Usage: opencli backup-job create name destination directory schedule retention status filters"
        exit 1
    fi

    # Construct JSON content
    json_content=$(cat <<EOF
{
  "name": "$1",
  "destination": "$2",
  "directory": "$3",
  "schedule": "$4",
  "retention": "$5",
  "status": "$6",
  "filters": "$7"
}
EOF
)

    # Create the new .json file with the provided content
    echo "$json_content" > "$new_file"
    success "Successfully created $(basename "$new_file" .json)"
}

# Edit a backup job
edit_backup_job() {
    local filename="$backup_dir$1.json"

    # Check if the file exists
    if [ ! -f "$filename" ]; then
        error "Destination ID: $1 does not exist!"
        # get list of all destination IDs
        json_files=$(find "$backup_dir" -type f -name "*.json" -exec basename {} \; | sed 's/\.json$//')
        printf '%*s\n' "${COLUMNS:-$(tput cols)}" '' | tr ' ' -
        echo "Available destination IDs:"
        echo "$json_files"
        exit 1
    fi

    # Validate parameters if needed
    # validate_parameters "${@:2}"

    # Read the content of the existing file before editing
    old_file_content=$(cat "$filename")

    # Create new JSON content
    json_content=$(cat <<EOF
{
  "name": "$2",
  "destination": "$3",
  "directory": "$4",
  "schedule": "$5",
  "retention": "$6",
  "status": "$7",
  "filters": "$8"
}
EOF
)

    # Overwrite the existing .json file with the new content
    echo "$json_content" > "$filename"

    success "Destination ID: '$1' edited successfully!"
    echo "Previous destination configuration:"
    echo "$old_file_content"
    echo "New destination configuration:"
    echo "$json_content"
}

# Delete a backup job
delete_backup_job() {
    local job_file="$backup_dir$1.json"

    # Check if the job file exists
    if [ ! -f "$job_file" ]; then
        error "Job with ID: $1 does not exist."
        exit 1
    fi

    # Delete the job file
    rm "$job_file"
    success "Backup job $1 deleted successfully."
}

# Main script
if [ "$#" -lt 1 ]; then
    error "Usage: opencli backup-job [create|edit|delete] [ID]"
    exit 1
fi

action="$1"

case "$action" in
    create)
        create_backup_job "${@:2}"
        ;;
    edit)
        edit_backup_job "${@:2}"
        ;;
    delete)
        delete_backup_job "$2"
        ;;
    *)
        error "Invalid action. Use: create, edit, or delete."
        exit 1
        ;;
esac
