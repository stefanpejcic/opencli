#!/bin/bash
################################################################################
# Script Name: files/fix_permissions.sh
# Description: Fix permissions for users /home directory files inside the container.
# Usage: opencli files-fix_permissions [USERNAME] [PATH]
# Author: Stefan Pejcic
# Created: 15.11.2023
# Last Modified: 31.08.2024
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
        directory="$path"
    else   
        directory="/home/$container_name"
    fi

  # Check if the container exists
  if docker inspect -f '{{.State.Running}}' "$container_name" &>/dev/null; then
        
        # Apply group permissions
        docker exec $container_name bash -c "chmod -R g+w $path"
        group_result=$?
        
        # Apply owner permissions
        docker exec $container_name bash -c "chown -R 1000:33 $path"
        owner_result=$?
        
        # Apply file permissions
        docker exec -u 0 -it "$container_name" bash -c "find $path -type f -exec chmod 644 {} \;"
        files_result=$?
        
        # Apply folder permissions
        docker exec -u 0 -it "$container_name" bash -c "find $path -type d -exec chmod 755 {} \;"
        folders_result=$?
        
        # Check if all commands were successful
        if [ $group_result -eq 0 ] && [ $owner_result -eq 0 ] && [ $files_result -eq 0 ] && [ $folders_result -eq 0 ]; then
            echo "Permissions applied successfully."
        else
            echo "Error applying permissions to $path."
        fi
  else
    echo "Container for user $container_name not found or is not running."
  fi
}


# Check if the --all flag is provided
if [ "$1" == "--all" ]; then
  if [ $# -eq 1 ]; then
    ensure_jq_installed # now we need jq 
    
    # Apply changes to all active users
    for container in $(opencli user-list --json | jq -r '.[].username'); do
      apply_permissions_in_container "$container"
    done
  else
    echo "Usage: opencli files-fix_permissions --all"
    exit 1
  fi
elif [ $# -ge 1 ]; then
  username="$1"
  path="$2"
  apply_permissions_in_container "$username" "$path"
else
  echo "Usage:"
  echo ""
  echo "opencli files-fix_permissions <USERNAME> [PATH]          Fix permissions for a single user."
  echo "opencli files-fix_permissions --all                      Fix permissions for all active users."
  exit 1
fi
