#!/bin/bash
################################################################################
# Script Name: collect_stats.sh
# Description: Collect docker usage information for all users.
# Usage: opencli docker-collect_stats
# Author: Petar Curic, Stefan Pejcic
# Created: 07.10.2023
# Last Modified: 09.96.2024
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

output_dir="/etc/openpanel/openpanel/core/users/"
current_datetime=$(date +'%Y-%m-%d-%H-%M-%S')

usage() {
    echo "Usage: opencli docker-collect_stats <username> OR opencli docker-collect_stats --all"
    echo ""  
}


# DB
source /usr/local/admin/scripts/db.sh



process_user() {
    local username="$1"
    output_file="$output_dir/$username/docker_usage.txt"  
    current_usage=$(su $username -c "docker stats --no-stream --format '{{json .}}' $username")
    echo "$current_datetime $current_usage" >> $output_file
    echo ""
    echo $current_usage
    echo ""
}  






# Check if username is provided as an argument
if [ $# -eq 0 ]; then
  usage
  exit 1
elif [[ "$1" == "--all" ]]; then

    sync
    echo 1 > /proc/sys/vm/drop_caches

  # Fetch list of users from opencli user-list --json
  users=$(opencli user-list --json | grep -v 'SUSPENDED' | awk -F'"' '/username/ {print $4}')

  # Check if no sites found
  if [[ -z "$users" || "$users" == "No users." ]]; then
    echo "No users found in the database."
    exit 1
  fi

  # Get total user count
  total_users=$(echo "$users" | wc -w)

  # Iterate over each user
  current_user_index=1
  for user in $users; do
    echo "Processing user: $user ($current_user_index/$total_users)"
    process_user "$user"
    echo "------------------------------"
    ((current_user_index++))
  done
  echo "DONE."
    
elif [ $# -eq 1 ]; then
  process_user "$1"
else
  usage
  exit 1
fi

