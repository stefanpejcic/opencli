#!/bin/bash
################################################################################
# Script Name: files/fix_permissions.sh
# Description: Fix permissions for users /home directory files inside the container.
# Usage: opencli files-fix_permissions [USERNAME] [PATH]
# Author: Stefan Pejcic
# Created: 15.11.2023
# Last Modified: 18.01.2025
# Company: openpanel.com
# Copyright (c) openpanel.com
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

# Set verbose to null
verbose=""

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

# Function to apply permissions and ownership changes within a Docker container
apply_permissions_in_container() {
  local container_name="$1"
  local path="$2"

    if [ -n "$path" ]; then
        # this is also checked on the backend
        if [[ $path != /home/$container_name/files/* ]]; then
            path="${path#/}" # strip / from beginning if relative path is sued
            path="/home/$container_name/files/$path" # prepend user home directory
        fi
        directory="$path"
        emails=false
    else   
        directory="/home/$container_name/files/"
        emails=true
    fi


        if id "$container_name" &>/dev/null; then
            user_id=$(id -u $container_name)
        else
            echo "User '$container_name' does not exist."
            exit 1
        fi




get_user_info() {
    local user="$1"
    local query="SELECT id, server FROM users WHERE username = '${user}';"
    user_info=$(mysql -se "$query")
    
    user_id=$(echo "$user_info" | awk '{print $1}')
    context=$(echo "$user_info" | awk '{print $2}')
    
    echo "$user_id,$context"
}


result=$(get_user_info "$container_name")
user_id=$(echo "$result" | cut -d',' -f1)
context=$(echo "$result" | cut -d',' -f2)



if [ -z "$user_id" ]; then
    echo "FATAL ERROR: user $container_name does not exist."
    exit 1
fi


  # Check if the container exists
  if docker --context $context inspect -f '{{.State.Running}}' "$container_name" &>/dev/null; then
        
        # WWW-DATA GROUP
        docker --context $context exec $container_name bash -c "cd $directory && xargs -d$'\n' -r chmod $verbose -R g+w $directory"
        group_result=$?
        
        # USERNAME OWNER
        docker --context $context exec $container_name bash -c "chown -R $verbose $user_ud:33 $directory"
        owner_result=$?
        
        if [ "$emails" = "true" ]; then
            docker --context $context exec $container_name bash -c "chown -R $verbose $user_id:1000 /home/$container_name/mail/"
            mail_result=$?
        fi
        
        # FILES
        docker --context $context exec -u 0 -it "$container_name" bash -c "find $directory -type f -print0 | xargs -0 chmod $verbose 644"
        files_result=$?
        
        # FOLDERS
        docker --context $context exec -u 0 -it "$container_name" bash -c "find $directory -type d -print0 | xargs -0 chmod $verbose 755"
        folders_result=$?
        
        # CHECK ALL 5
        if [ "$emails" = "true" ]; then
            if [ $group_result -eq 0 ] && [ $mail_result -eq 0 ] && [ $owner_result -eq 0 ] && [ $files_result -eq 0 ] && [ $folders_result -eq 0 ]; then
                echo "Permissions applied successfully to $directory"
            else
                echo "Error applying permissions to $directory"
            fi
        else
        # CHECK ALL 4
            if [ $group_result -eq 0 ] && [ $owner_result -eq 0 ] && [ $files_result -eq 0 ] && [ $folders_result -eq 0 ]; then
                echo "Permissions applied successfully to $directory"
            else
                echo "Error applying permissions to $directory"
            fi
        fi
  else
    echo "Container for user $container_name not found or is not running."
  fi
}


# Check if the --all flag is provided
if [ "$1" == "--all" ]; then
  if [ $# -le 2 ]; then
    ensure_jq_installed # Ensure jq is installed
    
    # Handle optional --debug flag
    [ "$2" == "--debug" ] && verbose="-v"

    # Apply changes to all active users
    for container in $(opencli user-list --json | jq -r '.[].username'); do
      apply_permissions_in_container "$container"
    done
  else
    echo "Usage: opencli files-fix_permissions --all [--debug]"
    exit 1
  fi
else
  # Fix permissions for a single user
  if [ $# -ge 1 ]; then
    username="$1"

    # Check if $2 is a path or --debug
    if [ "$2" == "--debug" ]; then
      verbose="-v"
      path=""
    else
      path="$2"
      [ "$3" == "--debug" ] && verbose="-v"
    fi

    apply_permissions_in_container "$username" "$path"
  else
    echo "Usage:"
    echo "opencli files-fix_permissions <USERNAME> [--debug]          Fix permissions for all files owned by single user."
    echo "opencli files-fix_permissions <USERNAME> [PATH] [--debug]   Fix permissions for the specified path owned by user."
    echo "opencli files-fix_permissions --all [--debug]               Fix permissions for all active users."
    exit 1
  fi
fi

