#!/bin/bash
################################################################################
# Script Name: index.sh
# Description: Re-index destination files for a backup job
# Usage: opencli backup-index ID [--debug]
# Author: Petar Curic
# Created: 31.01.2024
# Last Modified: 31.01.2024
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


# Check if the correct number of command line arguments is provided
if [ "$#" -lt 1 ]; then
    echo "Usage: opencli backup-index <JOB_ID> [--debug]"
    exit 1
fi


DEBUG=false

read_dest_json_file() {
    local dest_json_file="$1"
    jq -r '.hostname, .password, .ssh_port, .ssh_user, .ssh_key_path, .destination_dir_name, .storage_limit' "$dest_json_file"
}



job_id=$1
log_dir="/usr/local/admin/backups/logs/$job_id"
#log_file="$log_dir/$(( ls -l "$log_dir" | grep -c '^-' )).log"
log_file=$(ls "$log_dir"/*.log 2>/dev/null | sort -V | tail -n 1)
process_id=$(grep "process_id=" "$log_file" | awk -F'=' '{print $2}') 


if kill -0 "$process_id" 2>/dev/null; then
    echo "Error: Backup process with PID $process_id exists."
    exit 0
fi


# enable debug
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
    esac
done











INDEX_DIR="/usr/local/admin/backups/index/$job_id/"
DEST_BASE_DIR="/path/to/destination/base/dir"

# Define the path to the JSON file
job_json_file="/usr/local/admin/backups/jobs/$job_id.json"

# Check if the JSON file exists
if [ ! -f "$job_json_file" ]; then
    echo "Error: Job JSON file not found at $job_json_file"
    exit 1
fi

# Parse the JSON file and extract the 'destination' field
destination_id=$(jq -r '.destination' "$job_json_file")
dest_json_file="/usr/local/admin/backups/destinations/$destination_id.json"
# Extract destination data
dest_data=$(read_dest_json_file "$dest_json_file")

# Assign variables to extracted values
dest_hostname=$(echo "$dest_data" | awk 'NR==1')
dest_password=$(echo "$dest_data" | awk 'NR==2')
dest_ssh_port=$(echo "$dest_data" | awk 'NR==3')
dest_ssh_user=$(echo "$dest_data" | awk 'NR==4')
dest_ssh_key_path=$(echo "$dest_data" | awk 'NR==5')
dest_destination_dir_name=$(echo "$dest_data" | awk 'NR==6')
dest_storage_limit=$(echo "$dest_data" | awk 'NR==7')

# Check if the destination hostname is local
if [[ "$dest_hostname" == "localhost" || "$dest_hostname" == "127.0.0.1" || "$dest_hostname" == "$(curl -s https://ip.openpanel.co || wget -qO- https://ip.openpanel.co)" || "$dest_hostname" == "$(hostname)" ]]; then
    LOCAL=true
    REMOTE=false
else
    LOCAL=false
    REMOTE=true
fi



# Initialize counters
total_users=0
user_count=0
total_backups_count=0

total_users=$(docker ps --format '{{.Names}}' | wc -l)

echo "Indexing backups for $total_users users from destination: $dest_hostname and directory: $dest_destination_dir_name"

# Iterate through each container_name
for container_name in $(docker ps --format '{{.Names}}'); do

    bak_dir="$INDEX_DIR/$container_name.bak/"
    rm -r $bak_dir 2>/dev/null
    # Delete local .index files after copying to indexes_bak
    # Copy .index files from $container_name to temporary backup directory
    if [ -d "$INDEX_DIR/$container_name" ]; then
    mv "$INDEX_DIR/$container_name" "$INDEX_DIR/$container_name.bak"
    fi
    mkdir -p "/usr/local/admin/backups/index/$job_id/$container_name/"
    ((user_count++))
    if [ "$LOCAL" != true ]; then
        echo "Processing user $container_name ($user_count/$total_users)"
        if [ "$DEBUG" = true ]; then
            rsync -e "ssh -p $dest_ssh_port -i $dest_ssh_key_path" -avz "$dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$container_name/*/*.index" "$INDEX_DIR/$container_name/"
        else
        rsync -e "ssh -p $dest_ssh_port -i $dest_ssh_key_path" -avz "$dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$container_name/*/*.index" "$INDEX_DIR/$container_name/" > /dev/null 2>&1
        fi
    else
        echo "Processing user $container_name ($user_count/$total_users)"
        if [ "$DEBUG" = true ]; then
            cp "$dest_destination_dir_name/$container_name/*/*.index" "$INDEX_DIR/$container_name/"
        else
           cp "$dest_destination_dir_name/$container_name/*/*.index" "$INDEX_DIR/$container_name/" > /dev/null 2>&1
        fi
    fi


    new_index_count_for_user=$(find "$INDEX_DIR/$container_name/" -type f | wc -l)
    ((total_backups_count += new_index_count_for_user))
    echo "Indexed $new_index_count_for_user backups for user $container_name."

    rm -r $bak_dir 2>/dev/null
done

echo "Index complete, found a total of $total_backups_count backups for all $user_count users."
