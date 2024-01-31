#!/bin/bash

read_dest_json_file() {
    local dest_json_file="$1"
    jq -r '.hostname, .password, .ssh_port, .ssh_user, .ssh_key_path, .destination_dir_name, .storage_limit' "$dest_json_file"
}

job_id=$1
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
echo $destination_id
dest_json_file="/usr/local/admin/backups/destinations/$destination_id.json"
echo $dest_json_file
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

# Delete the temporary backup directory if it exists


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

    # Use rsync to copy .index files from destination to origin server
    rsync -e "ssh -p $dest_ssh_port -i $dest_ssh_key_path" -avz "$dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$container_name/*/*.index" "$INDEX_DIR/$container_name/"
    rm -r $bak_dir 2>/dev/null
done


