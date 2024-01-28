#!/bin/bash

# Check if the correct number of command line arguments is provided
if [ "$#" -ne 1 ] && [ "$#" -ne 2 ]; then
    echo "Usage: $0 <NUMBER> [--force-run]"
    exit 1
fi

NUMBER=$1
FORCE_RUN=false

# Check if the --force-run flag is provided
if [ "$#" -eq 2 ] && [ "$2" == "--force-run" ]; then
    FORCE_RUN=true
fi

JSON_FILE="/usr/local/admin/backups/jobs/$NUMBER.json"

# Check if the JSON file exists
if [ ! -f "$JSON_FILE" ]; then
    echo "Error: File $JSON_FILE does not exist."
    exit 1
fi

# Read and parse the JSON file
read_json_file() {
    local json_file="$1"
    jq -r '.status, .destination, .directory, .type[]?, .schedule, .retention, .filters[]?' "$json_file"
}

# Extract data from the JSON file
data=$(read_json_file "$JSON_FILE")

# Assign variables to extracted values
status=$(echo "$data" | awk 'NR==1')
destination=$(echo "$data" | awk 'NR==2')
directory=$(echo "$data" | awk 'NR==3')
types=($(echo "$data" | awk 'NR>=4 && NR<=6'))
schedule=$(echo "$data" | awk 'NR==7')
retention=$(echo "$data" | awk 'NR==8')
filters=($(echo "$data" | awk 'NR>=9'))

# Check if the status is "off" and --force-run flag is not provided
if [ "$status" == "off" ] && [ "$FORCE_RUN" == false ]; then
    echo "Backup job is disabled. Use --force-run to run the backup job anyway."
    exit 0
fi


DEST_JSON_FILE="/usr/local/admin/backups/destinations/$destination.json"

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
    # Add your logic for Action A here
    # ...
else
    echo "Destination is not local. Backing files using SSH connection to $dest_hostname"
    # Add your logic for Action B here
    # ...
fi

# Display the extracted values
echo "Status: $status"
echo "Destination: $destination"
echo "Directory: $directory"
echo "Types: ${types[@]}"
echo "Schedule: $schedule"
echo "Retention: $retention"
echo "Filters: ${filters[@]}"



    # Display the extracted values from the destination JSON file
    echo "Destination Hostname: $dest_hostname"
    echo "Destination Password: $dest_password"
    echo "Destination SSH Port: $dest_ssh_port"
    echo "Destination SSH User: $dest_ssh_user"
    echo "Destination SSH Key Path: $dest_ssh_key_path"
    echo "Destination Directory Name: $dest_destination_dir_name"
    echo "Destination Storage Limit: $dest_storage_limit"

# Add your logic here based on the extracted values
# For example, you can add commands to perform backup operations.
# ...
