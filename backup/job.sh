#!/bin/bash
################################################################################
# Script Name: job.sh
# Description: Create, edit, delete, backup jobs.
# Usage: opencli backup-job create|edit|delete
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

mkdir -p $backup_dir

if [ "$#" -lt 1 ]; then
    echo "Usage: opencli backup-job create|edit|delete"
    exit 1
fi


# Function to find the last number in existing .json files
get_last_number() {
    local last_number=$(ls -1 "$backup_dir"*.json 2>/dev/null | grep -oP '\d+' | sort -n | tail -n 1)
    echo "$last_number"
}

validate_parameters() {
    # Define regex patterns
    #local name_regex='^[[:alnum:]_]+$'
    #local destination_regex='^[0-9]+$'
    local schedule_regex="^(daily|weekly|monthly)$"
    local retention_regex="^[0-9]+$"
    local status_regex="^(on|off)$"
    local filters_regex="^.*$"

    # Validation rules
    if [[ ! "$1" =~ $name_regex ]]; then
        echo "Error: Invalid name. Please provide a valid word (letters, digits, underscores)." >&2
        exit 1
    fi

    if [[ ! "$2" =~ $destination_regex ]]; then
        echo "Error: Invalid destination. Please provide a valid number." >&2
        exit 1
    fi

    # Assuming directory can be any string, no validation needed

    if [[ ! "$4" =~ $schedule_regex ]]; then
        echo "Received schedule value: $4"
        echo "Error: Invalid schedule. It can only be 'daily', 'weekly', or 'monthly'." >&2
        exit 1
    fi

    if [[ ! "$5" =~ $retention_regex ]]; then
        echo "Error: Invalid retention. Please provide a valid number." >&2
        exit 1
    fi

    if [[ ! "$6" =~ $status_regex ]]; then
        echo "Error: Invalid status. It can only be 'on' or 'off'." >&2
        exit 1
    fi

    # Assuming filters can be any string, no validation needed

    # If all validations pass, continue with the script
    echo "All parameters are valid."
}




# Create a backup job
create_backup_job() {
    local last_number=$(get_last_number)
    local new_number=$((last_number + 1))
    local new_file="${backup_dir}${new_number}.json"

    # Check if enough parameters are provided
    if [ "$#" -ne 8 ]; then
        echo "Usage: opencli backup-job create name destination directory type schedule retention status filters"
        exit 1
    fi

    # Validate destination
    local destination_file="/usr/local/admin/backups/destinations/$2.json"

    # Check if the file exists
    if [ ! -f "$destination_file" ]; then
        echo "Destination file $destination_file does not exist."
        exit 1
    fi

    # Run the command
    response=$(opencli backup-destination validate "$2")

    # Check if the response contains "success"
    if echo "$response" | grep -q "success"; then
        echo "Destination validation successful."
    else
        echo "Destination validation failed: $response"
        exit 1
    fi

    # Validate parameters
    #validate_parameters "$@"

    # Construct filters
    filters=""
    for ((i=8; i<=$#; i++)); do
        filters+="\"${!i}\", "
    done
    filters="${filters%, }" # Remove trailing comma and space

    # Construct type
    backup_type="${4//,/\", \"}" # Replace commas with ", "

    # Construct JSON content
    json_content=$(cat <<EOF
{
  "name": "$1",
  "destination": "$2",
  "directory": "$3",
  "type": [ "$backup_type" ],
  "schedule": "$5",
  "retention": "$6",
  "status": "$7",
  "filters": [ $filters ]
}
EOF
)

    # Create the new .json file with the provided content
    echo "$json_content" > "$new_file"
    echo "Successfully created $(basename "$new_file" .json)"
}

# Edit a backup job
edit_backup_job() {
    local filename="$backup_dir$1.json"

    # Check if the file exists
    if [ ! -f "$filename" ]; then
        echo "Destination ID: $1 does not exist!"
        # get list of all destination IDs
        json_files=$(find "$backup_dir" -type f -name "*.json" -exec basename {} \; | sed 's/\.json$//')
        printf '%*s\n' "${COLUMNS:-$(tput cols)}" '' | tr ' ' -
        echo "Available destination IDs:"
        echo "$json_files"
        exit 1
    fi

    # Validate parameters if needed
    #validate_parameters "${@:2}"

    # Read the content of the existing file before editing
    old_file_content=$(cat "$filename")

    # Construct filters
    filters=""
    for ((i=9; i<=$#; i++)); do
        filters+="\"${!i}\", "
    done
    filters="${filters%, }" # Remove trailing comma and space

    # Construct type
    backup_type="${4//,/\", \"}" # Replace commas with ", "


    # Create new JSON content
    json_content=$(cat <<EOF
{
  "name": "$2",
  "destination": "$3",
  "directory": "$4",
  "type": [ "$backup_type" ],
  "schedule": "$6",
  "retention": "$7",
  "status": "$8",
  "filters": [ $filters ]
}
EOF
)

    # Overwrite the existing .json file with the new content
    echo "$json_content" > "$filename"

    echo "Destination ID: '$1' edited successfully!"
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
        echo "Job with ID: $1 does not exist."
        exit 1
    fi

    # Check if job is running
    log_dir="/var/log/openpanel/admin/backups/$1"
    last_number_log=$(ls -1 "$log_dir"*.json 2>/dev/null | grep -oP '\d+' | sort -n | tail -n 1)
    echo $last_number_log
    last_log="${log_dir}${last_number_log}.json"
    # Check if last_log exists
    if [ ! -f "$last_log" ]; then
        rm "$job_file"
        echo "Backup job $1 deleted successfully." 
    else
        # Check if last_log contains valid data
        log_content=$(head -6 "$last_log")
        if [ -z "$log_content" ]; then
            # Delete the job file
            rm "$job_file"
            echo "Backup job $1 deleted successfully."            
            :
            #echo "DEBUG: Log file is empty."
        else            
            # Extract status and process_id from log content
            status=$(echo "$log_content" | grep -Po 'status=\K\S+')
            process_id=$(echo "$log_content" | grep -Po 'process_id=\K\S+')
        
            # Check if status is Completed
            if [ "$status" != "Completed" ]; then
                :
                #echo "DEBUG: Backup status is not Completed."
                # Check if process with given pid is running
                if ! ps -p "$process_id" > /dev/null; then
                    #echo "DEBUG: Process with PID $process_id is not running."
                    # Delete the job file
                    rm "$job_file"
                    echo "Backup job $1 deleted successfully."
                fi
            else
                # Delete the job file
                rm "$job_file"
                echo "Backup job $1 deleted successfully."
            fi
        
        fi
    fi
}

# Main script
if [ "$#" -lt 1 ]; then
    echo "Usage: opencli backup-job [create|edit|delete] [ID]"
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
        echo "Invalid action. Use: create, edit, or delete."
        exit 1
        ;;
esac
