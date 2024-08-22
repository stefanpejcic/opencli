#!/bin/bash
################################################################################
# Script Name: files/fix_permissions.sh
# Description: Fix permissions for users files in their docker container.
# Usage: opencli files-fix_permissions [USERNAME] [PATH]
# Author: Stefan Pejcic
# Created: 15.11.2023
# Last Modified: 15.01.2024
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


ensure_jq_installed

# Function to apply permissions and ownership changes within a Docker container
apply_permissions_in_container() {
  local container_name="$1"
  local path="$2"

  # Check if the container exists
  if docker inspect -f '{{.State.Running}}' "$container_name" &>/dev/null; then
    if [ -n "$path" ]; then
      # Apply changes only to the specified path within the container
      if docker exec -u 0 -it "$container_name" bash -c "find $path -type f -exec chmod 644 {} \;"; then
        chown -R 1000:33 $path
        #chown 1000:33 $path
        echo "Permissions applied successfully."
      else
        echo "Error applying permissions to $path."
      fi
      # i grupa
      #chmod -R g+w $path
      docker exec $container_name bash -c "chmod -R g+w $path"
    else
      # Apply changes to the entire home directory within the container
      if docker exec -u 0 -it "$container_name" bash -c "find /home/$container_name -type f -exec chmod 644 {} \;"; then
      chown -R 1000:33 /home/$container_name
      #chown 1000:33 /home/$container_name
        echo "Permissions applied successfully."
      else
        echo "Error applying permissions to /home/$container_name."
      fi
      # i grupa
      chmod -R g+w /home/$container_name
      docker exec $container_name bash -c "chmod -R g+w /home/$container_name"
    fi
  else
    echo "Container $container_name not found or is not running."
  fi
}


# Check if the --all flag is provided
if [ "$1" == "--all" ]; then
  if [ $# -eq 1 ]; then
    # Apply changes to all running Docker containers
    for container in $(opencli user-list --json | jq -r '.[].username'); do
      apply_permissions_in_container "$container"
    done
  else
    echo "Usage: $0 --all"
    exit 1
  fi
elif [ $# -ge 1 ]; then
  # Check if a username is provided as an argument
  username="$1"
  
  # Check if a path is provided as an argument
  path="$2"
  
  # Apply changes to a specific user's Docker container
  apply_permissions_in_container "$username" "$path"
else
  echo "Usage: $0 <username> [path] OR $0 --all"
  exit 1
fi
