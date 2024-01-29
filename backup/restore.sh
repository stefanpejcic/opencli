#!/bin/bash
################################################################################
# Script Name: restore.sh
# Description: Restore a full backup for a single user.
# Use: opencli backup-restore <backup_directory>
# Author: Stefan Pejcic
# Created: 08.10.2023
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

NUMBER=$1

DEST_JSON_FILE="/usr/local/admin/backups/destinations/$NUMBER.json"

# Check if the destination JSON file exists
if [ ! -f "$DEST_JSON_FILE" ]; then
    echo "Error: Destination JSON file $DEST_JSON_FILE does not exist."
    exit 1
fi

# Read and parse the destination JSON file
read_dest_json_file() {
    local dest_json_file="$1"
    jq -r '.hostname, .password, .ssh_port, .ssh_user, .ssh_key_path, .destination_dir_name, .storage_limit' "$dest_json_file"
}


# Extract data from the destination JSON file
dest_data=$(read_dest_json_file "$DEST_JSON_FILE")

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
    echo "Destination is local. Backing up files locally to $directory folder"
    LOCAL=true
    REMOTE=false
else
    echo "Destination is not local. Backing files using SSH connection to $dest_hostname"
    LOCAL=false
    REMOTE=true
fi


if [ "$DEBUG" = true ]; then
# backupjob json
echo "Status: $status"
echo "Destination: $destination"
# destination json
echo "Destination Hostname: $dest_hostname"
echo "Destination Password: $dest_password"
echo "Destination SSH Port: $dest_ssh_port"
echo "Destination SSH User: $dest_ssh_user"
echo "Destination SSH Key Path: $dest_ssh_key_path"
echo "Destination Storage Limit: $dest_storage_limit"
fi


local_temp_dir="/tmp/openpanel_restore_temp_dir"
mkdir -p $local_temp_dir

run_restore() {
source_path_restore=$1
local_destination=$2
#remove / from beginning
local_destination="${local_destination#/}"

if [ "$LOCAL" != true ]; then
    rsync -e "ssh -i $dest_ssh_key_path -p $dest_ssh_port" -r -p "$dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$source_path_restore" "$local_temp_dir"

    if [ "$DEBUG" = true ]; then
        # backupjob json
        echo "rsync command: rsync -e ssh -i $dest_ssh_key_path -p $dest_ssh_port -r -p $dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$source_path_restore $local_temp_dir"
    fi
    
cp -rl $local_temp_dir/ /$local_destination
else
    cp -Lr "$source_path_restore" /"$local_destination"
fi

}



source_path_restore="/backup/nesto/20240129002034/user_data_dump.sql"
local_destination="/root/backup"


run_restore  $source_path_restore $local_destination
