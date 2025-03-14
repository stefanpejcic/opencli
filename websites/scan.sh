#!/bin/bash
################################################################################
# Script Name: websites/scan.sh
# Description: Scan user files for WP sites and add them to SiteManager interface.
# Usage: opencli websites-scan $username
# Author: Stefan Pejcic
# Created: 23.10.2024
# Last Modified: 14.03.2025
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


# Function to get domain ID from the database
get_domain_id() {
    local domain_name="$1"
    result=$(mysql -sse "SELECT domain_id FROM domains WHERE domain_url = '$domain_name';")
    echo  $result
}

get_context_for_user() {
     source /usr/local/opencli/db.sh
        username_query="SELECT server FROM users WHERE username = '$current_username'"
        context=$(mysql -D "$mysql_database" -e "$username_query" -sN)
        if [ -z "$context" ]; then
            context=$current_username
        fi
}


#Function to run WordPress CLI commands
run_wp_cli() {
    local username="$1"
    local path="$2"
    local command="$3"
    docker --context $context exec "$current_username" bash -c "wp --allow-root --path=${path} ${command}"
}

check_site_already_exists_in_db() {
    local site_name="$1"

    local result=$(mysql -sse "SELECT EXISTS(SELECT 1 FROM sites WHERE site_name = '$site_name');")
    
    if [[ "$result" -eq 1 ]]; then
        return 0  # exists
    else
        return 1  # not exist
    fi
}




run_for_single_user() {

current_username=$1
get_context_for_user
# Base directory to scan for wp-config.php files
base_directory="/home/${current_username}"

found_installations=()
existing_installations=()

found_count=0
existing_count=0

# Iterate through user files
while IFS= read -r -d '' config_file_path; do
    echo "- Parsing file: $config_file_path"
    
    # get sitename for manager
	# Remove /wp-config.php sufix
	site_name=${config_file_path%/wp-config.php}

	# Remove /home/$current_username/ prefix
	site_name=${site_name/#\/home\/$current_username\//}
    


    # Get domain name
    domain_name="${site_name%%/*}"

    # Check if website exists in sites table
    if check_site_already_exists_in_db "$site_name"; then
    	echo "  Site $site_name already exists in the SiteManager - Skipping"
        existing_installations+=("- $site_name - domain: $domain_name, config: ${config_file_path%/wp-config.php}")
        ((existing_count++))   	
        continue
    fi

    # Get admin email from wp-config.php
    admin_email=$(run_wp_cli "$current_username" "$(dirname "$config_file_path")" "option get admin_email 2>/dev/null")
    if [[ ! "$admin_email" =~ "@" ]]; then
        echo "  WARNING: Invalid admin email: $admin_email"
    fi
    

    # Get WordPress version
    version=$(run_wp_cli "$current_username" "$(dirname "$config_file_path")" "core version 2>/dev/null")

    # Get domain ID
    domain_id=$(get_domain_id "$domain_name")
    if ! [[ "$domain_id" =~ ^[0-9]+$ ]]; then
    	echo "  WARNING: ID not detected for domain $domain_name - make sure that domain is added for user - Skipping"
    	exit 1
    fi
    
     
    
     echo "Adding website $site_name to Site Manager"
     echo "INSERT INTO sites (site_name, domain_id, admin_email, version, type) VALUES ('$site_name', '$domain_id', '$admin_email', '$version', 'wordpress');" | mysql

     echo "Enabling auto-login to wp-admin from Site Manager interface"
     run_wp_cli "$current_username" "$(dirname "$config_file_path")" "package install aaemnnosttv/wp-cli-login-command"

    found_installations+=("- $site_name, domain: $domain_name, email: $admin_email, version: $version")
    ((found_count++))
done < <(find "$base_directory" -name 'wp-config.php' -print0)





# Summary messages
if [ ${#found_installations[@]} -gt 0 ]; then
    echo "Scan completed. Detected $found_count new WordPress installations:"
    for installation in "${found_installations[@]}"; do
        echo "$installation"
    done
elif [ ${#existing_installations[@]} -gt 0 ]; then
    echo "Scan completed. No new WordPress installations detected, but the following $existing_count existing installations are present:"

    for installation in "${existing_installations[@]}"; do
        echo "$installation"
    done
else
    echo "Scan completed. No WordPress installations detected."
fi


}






if [ $# -eq 0 ]; then
  echo "Usage: opencli websites-scan <USERNAME> OR opencli websites-scan -all"
  exit 1
elif [[ "$1" == "-all" ]]; then
# ALL USERS

  users=$(opencli user-list --json | grep -v 'SUSPENDED' | awk -F'"' '/username/ {print $4}')

  if [[ -z "$users" || "$users" == "No users." ]]; then
    echo "No users found in the database."
    exit 1
  fi
  
  total_users=$(echo "$users" | wc -w)
  current_user_index=1
  
  for user in $users; do
    echo "Processing user: $user ($current_user_index/$total_users)"
        run_for_single_user "$user"   
    echo "------------------------------"
    ((current_user_index++))
  done
  echo "DONE."

# SINGLE USER
elif [ $# -eq 1 ]; then
  run_for_single_user "$1"
else
  echo "Usage: opencli websites-scan <USERNAME> OR opencli websites-scan -all"
  exit 1
fi
