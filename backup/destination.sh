#!/bin/bash
################################################################################
# Script Name: destination.sh
# Description: Create, edit, delete, validate backup destinations.
# Usage: opencli backup-destination create|edit|delete|validate ID
# Author: Stefan Pejcic
# Created: 26.01.2024
# Last Modified: 22.02.2025
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

backup_dir="/etc/openpanel/openadmin/config/backups/destinations/"
DEBUG=false  # Default value for DEBUG


# IP SERVERS
SCRIPT_PATH="/usr/local/admin/core/scripts/ip_servers.sh"
if [ -f "$SCRIPT_PATH" ]; then
    source "$SCRIPT_PATH"
else
    IP_SERVER_1=IP_SERVER_2=IP_SERVER_3="https://ip.openpanel.com"
fi










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

ensure_jq_installed() {
    # Check if jq is installed
    if ! command -v jq &> /dev/null; then
        # Detect the package manager and install jq
        if command -v apt-get &> /dev/null; then
            sudo apt-get update > /dev/null 2>&1
            sudo apt-get install -y -qq jq > /dev/null 2>&1
        elif command -v yum &> /dev/null; then
            sudo yum install -y -q jq > /dev/null 2>&1
        elif command -v dnf &> /dev/null; then
            sudo dnf install -y -q jq > /dev/null 2>&1
        else
            echo "Error: No compatible package manager found. Please install jq manually and try again."
            exit 1
        fi

        # Check if installation was successful
        if ! command -v jq &> /dev/null; then
            echo "Error: jq installation failed. Please install jq manually and try again."
            exit 1
        fi
    fi
}

# Check if the directory exists
if [ ! -d "$backup_dir" ]; then
  error "Directory $backup_dir does not exist."
  exit 1
fi







# Function to find job with the specified destination
is_destination_used() {

dir="/usr/local/admin/backups/jobs"
used=false
job_name=""


# Loop through all files ending with .json in the specified directory
for file in "$dir"/*.json; do
  # Check if the file exists
  if [ ! -f "$file" ]; then
    echo "Error: File $file does not exist."
    exit 1
  fi

  # Read the destination line from the current file
  destination_number=$(jq -r '.destination' "$file")

  # Compare the destination number with the provided argument
  if [ "$destination_number" == "$1" ]; then
    used=true
    job_name=$(jq -r '.name' "$file")
    break  # Exit the loop once a match is found
  fi
done

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
  local domain_regex="^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$"
  local port_regex="^([1-9]|[1-9][0-9]{1,4}|[1-2][0-9]{1,4}|3[0-4][0-9]{1,3}|35000)$"
  local ssh_user_regex="^\S+$"

    if [[ ! "$1" =~ $hostname_regex && ! "$1" =~ $ipv4_regex && "$1" != "localhost" && "$1" != "127.0.0.1" && "$1" != "$(curl --silent --max-time 2 -4 $IP_SERVER_1 || wget --timeout=2 -qO- $IP_SERVER_2 || curl --silent --max-time 2 -4 $IP_SERVER_3)" && "$1" != "$(hostname)" ]]; then
        error "Invalid hostname. For remote destinations it must be a valid IPv4 address or a domain name. For local backup destination use: localhost or 127.0.0.1, or current machine's hostname or public IP."
        exit 1
    fi

  # Validate ssh port
  if [[ ! "$2" =~ $port_regex ]]; then
      echo $2
    error "Invalid SSH port number. Must be a number between 1 and 35000."
    exit 1
  fi

  # Validate ssh user
  if [[ ! "$3" =~ $ssh_user_regex ]]; then
    error "Invalid SSH user. Must be one word only."
    exit 1
  fi


    # Validate ssh key path for remote destination only
    if [ "$1" != "localhost" ] && [ "$1" != "127.0.0.1" ] && [ "$1" != "$(curl --silent --max-time 2 -4 $IP_SERVER_1 || wget --timeout=2 -qO- $IP_SERVER_2 || curl --silent --max-time 2 -4 $IP_SERVER_3)" ] && [ "$1" != "$(hostname)" ]; then
     
      # Check if exists
      if [ ! -f $4 ]; then
        echo "SSH key path file $4 does not exist."
        exit 1
      else
            # Check permissions and set to 600 if not already set
            current_permissions=$(stat -c %a "$4")
            if [ "$current_permissions" != "600" ]; then
                chmod 600 "$4"
            fi

            # Check if file is a valid private key
            key_type=$(ssh-keygen -y -e -f $4 2>&1 | grep -q "BEGIN" && echo "OpenSSH" || echo "Not an SSH key")
    
            if [[ $key_type == "OpenSSH" ]]; then
                #echo "File is an SSH private key."
                true
            else
                # if not, revert permissions
                chmod "$current_permissions" "$4"
                echo "File is not an SSH private key."
                exit 1
            fi
      fi
    fi



}

# Function to create a new .json file
create_backup() {
  local last_number=$(get_last_number)
  local new_number=$((last_number + 1))
  local new_file="${new_number}.json"

  # Check if enough parameters are provided
  if [ "$#" -lt 5 ]; then
    error "Usage: opencli backup-destination create hostname ssh_port ssh_user ssh_key_path treshold"
    exit 1
  fi


  # Check if the hostname is local or one of the predefined IPs
  if [ "$1" == "localhost" ] || [ "$1" == "127.0.0.1" ] || [ "$1" == "$(curl --silent --max-time 2 -4 $IP_SERVER_1 || wget --timeout=2 -qO- $IP_SERVER_2 || curl --silent --max-time 2 -4 $IP_SERVER_3)" ] || [ "$1" == "$(hostname)" ]; then
    # Perform du check only, TODO
    true
  else
    # validate
    validate_parameters "$1" "$2" "$3" "$4" "$5"

    # Perform SSH connection and checks for a remote machine
    if [ "$DEBUG" = true ]; then
        echo "Validating SSH connection with the destination, running command: 'ssh -i $4 $3@$1 -p $2'"
    fi
    
    # Attempt to establish an SSH connection with a timeout
    timeout "10" ssh -i $4 -p "$2" "$3"@"$1" echo "SSH connection successful."
    connection_status=$?

    if [ $connection_status -ne 0 ]; then
      echo "SSH connection to $1 failed or timed out."
      exit 1
    fi
    
  fi



  
  # Construct JSON content
  json_content=$(cat <<EOF
{
  "hostname": "$1",
  "ssh_port": $2,
  "ssh_user": "$3",
  "ssh_key_path": "$4",
  "storage_limit": "$5"
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
  "ssh_port": $3,
  "ssh_user": "$4",
  "ssh_key_path": "$5",
  "storage_limit": "$6"
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
  if [ "$hostname" == "localhost" ] || [ "$hostname" == "127.0.0.1" ] || [ "$hostname" == "$(curl --silent --max-time 2 -4 $IP_SERVER_1 || wget --timeout=2 -qO- $IP_SERVER_2 || curl --silent --max-time 2 -4 $IP_SERVER_3)" ] || [ "$hostname" == "$(hostname)" ]; then
    # no checks needed
    #true
    echo "success: Validated!"
  else
    # Perform SSH connection and checks for a remote machine
    validate_parameters "$hostname" "$ssh_port" "$ssh_user" "$ssh_key_path" "$storage_limit"
    
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
    ensure_jq_installed
    validate_parameters_for_delete "$1"
    delete_backup "$1"
    ;;
  validate)
    shift  # Remove the first argument "validate"
    ensure_jq_installed
    validate_parameters_for_delete "$1"
    validate_ssh_connection "$1"
    ;;
  *)
    error "Invalid option. Use: create, edit, or delete."
    exit 1
    ;;
esac
