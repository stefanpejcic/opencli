#!/bin/bash
################################################################################
# Script Name: ssl/user.sh
# Description: Check SSL status for all domains owned by user and update cache files.
# Usage: opencli ssl-user <username|--all>
# Author: Stefan Pejcic
# Created: 22.11.2023
# Last Modified: 22.11.2023
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

# Check if username is provided as an argument
if [ $# -eq 0 ]; then
    echo "Usage: opencli ssl-user <username> OR opencli ssl-user --all"
  exit 1
fi

process_user_domains(){
    # Set the username and file path
    local username="$1"
    file_path="/etc/openpanel/openpanel/core/users/$username/.ssl"
    mkdir -p "$(dirname "$file_path")"
    
    # Get list of user domains
    domains=$(opencli domains-user "$username")

      # Check if no domains found
      if [[ -z "$domains" || "$domains" =~ ^No\ domains\ found\ for\ user ]]; then
        echo "No domains found in the database or opencli command error."
      else
       
        # Get certificates information
        certificates_info=$(certbot certificates 2>&1)
        
        # Process and save the result to a file
        echo -n > "$file_path"
        
        while IFS= read -r domain; do
            # Extract the expiry date for the current domain
            expiry_date=$(echo "$certificates_info" | grep -A 5 "Certificate Name: $domain" | grep "Expiry Date" | cut -d ":" -f 2-)
        
            # Save the result to the file
            if [ -z "$expiry_date" ]; then
                echo "$domain: None" >> "$file_path"
                echo "$domain: None"
            else
                echo "$domain: $expiry_date" >> "$file_path"
                echo "$domain: $expiry_date"
            fi
        done <<< "$domains"
        
      fi
}


if [[ "$1" == "--all" ]]; then
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
    process_user_domains "$user"
    echo "------------------------------"
    ((current_user_index++))
  done
  echo "DONE."
  
elif [ $# -eq 1 ]; then
  process_user_domains "$1"
fi
