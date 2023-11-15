#!/bin/bash
################################################################################
# Script Name: files/fix_permissions.sh
# Description: Fix permissions for users files in their docker container.
# Usage: opencli files-fix_permissions
# Usage: opencli files-fix_permissions --all
# Author: Stefan Pejcic
# Created: 15.11.2023
# Last Modified: 15.11.2023
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

# Function to apply permissions and ownership changes within a Docker container
apply_permissions_in_container() {
  local container_name="$1"
  
  # Check if the container exists
  if docker inspect -f '{{.State.Running}}' "$container_name" &>/dev/null; then
    docker exec -u 0 -it "$container_name" bash -c "find /home/$container_name -type f -exec chown $container_name:$container_name {} \; && find /home/$container_name -type f \( -name '*.php' -o -name '*.cgi' -o -name '*.pl' \) -exec chmod 755 {} \; && find /home/$container_name -type f -name '*.log' -exec chmod 640 {} \; && find /home/$container_name -type d -exec chown $container_name:$container_name {} \; && find /home/$container_name -type d -exec chmod 755 {} \;"
  else
    echo "Container $container_name not found or is not running."
  fi
}

# Check if the --all flag is provided
if [ "$1" == "--all" ]; then
  # Apply changes to all running Docker containers
  for container in $(docker ps --format '{{.Names}}'); do
    apply_permissions_in_container "$container"
  done
elif [ $# -eq 1 ]; then
  # Check if a username is provided as an argument
  username="$1"
  
  # Apply changes to a specific user's Docker container
  apply_permissions_in_container "$username"
else
  echo "Usage: $0 <username> OR $0 --all"
  exit 1
fi
