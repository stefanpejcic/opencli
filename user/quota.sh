#!/bin/bash
################################################################################
# Script Name: user/quota.sh
# Description: Enforce and recalculate disk and inodes for a user.
# Usage: opencli user-quota <username|--all>
# Author: Stefan Pejcic
# Created: 16.11.2023
# Last Modified: 07.07.2025
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



usage() {
    echo "Usage: opencli user-quota <username> OR opencli user-quota --all"
    echo ""  
}


# DB
source /usr/local/opencli/db.sh

process_user() {
  local username="$1"
  
  get_plan_limits() {
      local username="$1"
      local query="SELECT p.inodes_limit, p.disk_limit 
                   FROM users u
                   JOIN plans p ON u.plan_id = p.id
                   WHERE u.username = '$username'"
      # Fetch the results and assign them to disk_limit and file_limit
      read -r file_limit disk_limit < <(mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$query")
  }
  
  # Call the function
  get_plan_limits "$username"
  
  # Check if limits are empty
  if [ -z "$file_limit" ] || [ -z "$disk_limit" ]; then
      echo "Error: Unable to fetch limits for the user's current plan."
      exit 1
  fi
  
  block_limit="${disk_limit// GB/}"           # remove " GB"
  block_limit=$((block_limit * 1024000))       # convert to blocks
  #echo "- File Limit: $file_limit"
  #echo "- Block Limit: $block_limit"


  # Check if the username exists in the system
  if ! id "$username" &>/dev/null; then
      echo "Error: User $username does not exist."
      exit 2
  fi

  # Set the user's disk quota
  sudo setquota -u "$username" "$block_limit" "$block_limit" "$file_limit" "$file_limit" /
  if [ $? -eq 0 ]; then
      echo "Quota for user $username has been set to $block_limit blocks ($disk_limit) and $file_limit inodes."
  else
      echo "Failed to set quota for user $username."
      exit 3
  fi


  
}



# Check if username is provided as an argument
if [ $# -eq 0 ]; then
  usage
  exit 1
elif [[ "$1" == "--all" ]]; then
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
  
  repquota -u / > /etc/openpanel/openpanel/core/users/repquota
  
elif [ $# -eq 1 ]; then
  process_user "$1"
  repquota -u / > /etc/openpanel/openpanel/core/users/repquota
else
  usage
  exit 1
fi
