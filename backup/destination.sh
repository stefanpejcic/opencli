#!/bin/bash
################################################################################
# Script Name: destination.sh
# Description: Create, edit, delete, validate backup destinations.
# Usage: opencli backup-destination create|edit|delete|validate ID
# Author: Stefan Pejcic
# Created: 26.01.2024
# Last Modified: 26.01.2024
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

backup_dir="/usr/local/admin/backups/destinations/"
jobs_file="/usr/local/admin/backups/jobs.json"
DEBUG=false  # Default value for DEBUG

# crveno na radost
error() {
    echo -e "\033[41;97m$1\033[0m"
}

# zelena pozadina
success() {
    echo -e "\033[42;97m$1\033[0m"
}



# Parse optional flags to enable debug mode when needed!
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        *)
            ;;
    esac
done




# Check if the directory exists
if [ ! -d "$backup_dir" ]; then
  error "Directory $backup_dir does not exist."
  exit 1
fi







# Function to find job with the specified destination
is_destination_used() {

if [ ! -f "$jobs_file" ]; then
  error "File $jobs_file does not exist."
  exit 1
fi



  local destination_number="$1"
  local used=false
  local job_name=""
  local destination_line_number=$(grep -n "\"destination\":\s*\"$destination_number\"" "$jobs_file" | cut -d: -f1)

  if [ -n "$destination_line_number" ]; then
    local job_name=$(awk -v dest_line="$destination_line_number" 'NR==dest_line-1 {gsub(/.*"name":\s*"/, ""); gsub(/".*$/, ""); print}' "$jobs_file")
    used=true
  else
    used=false
    #echo "No backup jobs are currently using destination ID: '$destination_number'. Proceeding with delete."
  fi

  echo "$used|$job_name"
}





# Function to delete a .json file
delete_backup() {
  local filename="$1.json"

  # Check if the file exists
  if [ ! -f "$backup_dir$filename" ]; then
    error "Destination ID: $(basename "$filename" .json) does not exist!"
    # get list of all destination IDs
    json_files=$(find "$backup_dir" -type f -name "*.json" -exec basename {} \; | sed 's/\.json$//')
    printf '%*s\n' "${COLUMNS:-$(tput cols)}" '' | tr ' ' -
    echo "Available destination IDs:"
    echo "$json_files"
    exit 1
  fi

  #local destination_number="${filename%.*}"
  local destination_number="$(basename "$filename" .json)"


  # Check if the destination is used by any job
  result=$(is_destination_used "$destination_number")
  used=$(echo "$result" | cut -d '|' -f1)
  job_name=$(echo "$result" | cut -d '|' -f2)

  if [ "$used" = "true" ]; then
    error "Destination ID '$destination_number' could not be deleted as it is used by the backup job: '$job_name'."
    exit 1
  fi



  # Read the content of the file before deleting
  file_content=$(cat "$backup_dir$filename")

  # Delete the file
  rm "$backup_dir$filename"

  success "Deleted destination ID: $(basename "$filename" .json)"
  echo "Deleted content:"
  echo "$file_content"
}


# Function to validate input parameters
validate_parameters_for_delete() {
  local filename_regex="^[1-9][0-9]*$"

  # Validate filename
  if [[ ! "$1" =~ $filename_regex ]]; then
    error "Invalid destination ID."
    exit 1
  fi
}


# Change to the backup directory
cd "$backup_dir" || exit 1

# Function to find the last number in existing .json files
get_last_number() {
  local last_number=$(ls -1 *.json 2>/dev/null | grep -oP '\d+' | sort -n | tail -n 1)
  echo "$last_number"
}

# Function to validate input parameters
validate_parameters() {
  local hostname_regex="^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])\.([a-zA-Z]{2,}|[a-zA-Z0-9-]*[a-zA-Z0-9]\.[a-zA-Z]{2,})$"
  local ipv4_regex="^([0-9]{1,3}\.){3}[0-9]{1,3}$"
  local domain_regex="^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])$"
  local password_regex="^\S+$"
  local port_regex="^([1-9]|[1-9][0-9]{1,4}|[1-2][0-9]{1,4}|3[0-4][0-9]{1,3}|35000)$"
  local ssh_user_regex="^\S+$"
  local destination_dir_regex="^([1-9]|[1-9][0-9]|100)$"

    # Validate hostname
    if [[ ! "$1" =~ $hostname_regex && ! "$1" =~ $ipv4_regex && "$1" != "localhost" && "$1" != "127.0.0.1" && "$1" != "$(curl -s https://ip.openpanel.co || wget -qO- https://ip.openpanel.co)" && "$1" != "$(hostname)" ]]; then
        error "Invalid hostname. For remote destinations it must be a valid IPv4 address or a domain name. For local backup destination use: localhost or 127.0.0.1, or current machine's hostname or public IP."
        exit 1
    fi

  # Validate password
  if [[ ! "$2" =~ $password_regex ]]; then
    error "Invalid password. Must be one word only."
    exit 1
  fi

  # Validate ssh port
  if [[ ! "$3" =~ $port_regex ]]; then
    error "Invalid SSH port number. Must be a number between 1 and 35000."
    exit 1
  fi

  # Validate ssh user
  if [[ ! "$4" =~ $ssh_user_regex ]]; then
    error "Invalid SSH user. Must be one word only."
    exit 1
  fi


    # Validate ssh key path
    if [ "$1" != "localhost" ] && [ "$1" != "127.0.0.1" ] && [ "$1" != "$(curl -s https://ip.openpanel.co || wget -qO- https://ip.openpanel.co)" ] && [ "$1" != "$(hostname)" ]; then
      # Validate ssh key path
      if [ ! -f "$5" ]; then
        echo "SSH key path does not exist."
        exit 1
      fi
    fi

  # Check and set permissions for key file
  if [ "$(stat -c %a "$5")" != "600" ]; then
    chmod 600 "$5"
    error "SSH key has incorrect permissions, setting permissions to 600."
  fi

}

# Function to create a new .json file
create_backup() {
  local last_number=$(get_last_number)
  local new_number=$((last_number + 1))
  local new_file="${new_number}.json"

  # Check if enough parameters are provided
  if [ "$#" -ne 7 ]; then
    error "Usage: opencli backup-destination create hostname password ssh_port ssh_user ssh_key_path"
    exit 1
  fi

  # Validate parameters
  validate_parameters "$@"

  # Construct JSON content
  json_content=$(cat <<EOF
{
  "hostname": "$1",
  "password": "$2",
  "ssh_port": $3,
  "ssh_user": "$4",
  "ssh_key_path": "$5",
  "storage_limit": "$6"
}
EOF
)

  # Create the new .json file with the provided content
  echo "$json_content" > "$new_file"
  success "Successfully created $(basename "$new_file" .json)"
}



# Function to edit an existing destiantion .json file
edit_backup() {
  local filename="$1.json"

  # Check if the file exists
  if [ ! -f "$backup_dir$filename" ]; then
    error "Destination ID: $(basename "$filename" .json) does not exist!"
    # get list of all destination IDs
    json_files=$(find "$backup_dir" -type f -name "*.json" -exec basename {} \; | sed 's/\.json$//')
    printf '%*s\n' "${COLUMNS:-$(tput cols)}" '' | tr ' ' -
    echo "Available destination IDs:"
    echo "$json_files"
    exit 1
  fi



  # Validate parameters
  validate_parameters "${@:2}"

  # Read the content of the existing file before editing
  old_file_content=$(cat "$backup_dir$filename")

  # Create new JSON content
  json_content=$(cat <<EOF
{
  "hostname": "$2",
  "password": "$3",
  "ssh_port": $4,
  "ssh_user": "$5",
  "ssh_key_path": "$6",
  "storage_limit": "$7"
}
EOF
)

  # Overwrite the existing .json file with the new content
  echo "$json_content" > "$backup_dir$filename"

  success "Destination  ID: '$(basename "$filename" .json)' edited successfully!"
  echo "Previous destination configuration:"
  echo "$old_file_content"
  echo "New destination configuration:"
  echo "$json_content"
}




# List destinations
list_backup_ids() {
    # get list of all destination IDs
    json_files=$(find "$backup_dir" -type f -name "*.json" -exec basename {} \; | sed 's/\.json$//')
    echo "Available destination IDs:"
    echo "$json_files"
    exit 1
}


# Function to validate the SSH connection using data from a JSON file
validate_ssh_connection() {
  local filename="$1.json"
  local timeout_seconds=10

  # Check if the file exists
  if [ ! -f "$backup_dir$filename" ]; then
    error "Destination ID: $(basename "$filename" .json) does not exist!"
    # get list of all destination IDs
    json_files=$(find "$backup_dir" -type f -name "*.json" -exec basename {} \; | sed 's/\.json$//')
    printf '%*s\n' "${COLUMNS:-$(tput cols)}" '' | tr ' ' -
    echo "Available destination IDs:"
    echo "$json_files"
    exit 1
  fi

  # Read JSON content
  json_content=$(cat "$backup_dir$filename")

  # Extract values from JSON
  hostname=$(jq -r '.hostname' <<< "$json_content")
  ssh_user=$(jq -r '.ssh_user' <<< "$json_content")
  ssh_port=$(jq -r '.ssh_port' <<< "$json_content")
  ssh_key_path=$(jq -r '.ssh_key_path' <<< "$json_content")
  storage_limit=$(jq -r '.storage_limit' <<< "$json_content")

  # Check if the hostname is local or one of the predefined IPs
  if [ "$hostname" == "localhost" ] || [ "$hostname" == "127.0.0.1" ] || [ "$hostname" == "$(curl -s https://ip.openpanel.co || wget -qO- https://ip.openpanel.co)" ] || [ "$hostname" == "$(hostname)" ]; then
    # Perform checks on the local machine without attempting SSH connection
    pass
  else
    # Perform SSH connection and checks for a remote machine
    validate_parameters "$hostname" "dummy_password" "$ssh_port" "$ssh_user" "$ssh_key_path" "$storage_limit"
    
    if [ "$DEBUG" = true ]; then
        echo "Validating SSH connection with the destination, running command: 'ssh -i $ssh_key_path $ssh_user@$hostname -p $ssh_port'"
    fi
    
    # Attempt to establish an SSH connection with a timeout
    timeout "$timeout_seconds" ssh -i "$ssh_key_path" -p "$ssh_port" "$ssh_user"@"$hostname" echo "SSH connection successful."
    connection_status=$?

    if [ $connection_status -eq 0 ]; then
        echo "Validated! SSH connection successful to destination $hostname."
    else
      echo "Validation failed! SSH connection to $hostname failed or timed out."
        if [ "$DEBUG" = true ]; then
            echo "SSH Connection Status: $connection_status"
        fi
    fi
  fi
}








# Main script
if [ "$#" -lt 1 ]; then
  error "Usage: opencli backup-destination [create|edit|validate|delete|list] [ID]"
  exit 1
fi

case "$1" in
  list)
    list_backup_ids
    ;;
  create)
    shift  # Remove the first argument "create"
    create_backup "$@"
    ;;
  edit)
    shift  # Remove the first argument "edit"
    validate_parameters_for_delete "$1"
    edit_backup "$@"
    ;;
  delete)
    shift  # Remove the first argument "delete"
    validate_parameters_for_delete "$1"
    delete_backup "$1"
    ;;
  validate)
    shift  # Remove the first argument "validate"
    validate_parameters_for_delete "$1"
    validate_ssh_connection "$1"
    ;;
  *)
    error "Invalid option. Use: create, edit, or delete."
    exit 1
    ;;
esac
