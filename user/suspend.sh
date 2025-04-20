#!/bin/bash
################################################################################
# Script Name: user/suspend.sh
# Description: Suspend user: stop container and suspend domains.
# Usage: opencli user-suspend <USERNAME>
# Author: Stefan Pejcic
# Created: 01.10.2023
# Last Modified: 23.02.2025
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

# Check if the correct number of command-line arguments is provided
if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
	echo "Usage: opencli user-suspend <username>"
    exit 1
fi

# Get username from command-line argument
username="$1"
DEBUG=false  # Default value for DEBUG

suspended_dir="/etc/openpanel/caddy/suspended_domains/"
conf_template="/etc/openpanel/caddy/templates/suspended_user.conf"
mkdir -p $suspended_dir


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

# DB
source /usr/local/opencli/db.sh




get_docker_context_for_user() {
    # GET CONTEXT NAME FOR DOCKER COMMANDS
    server_name=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "SELECT server FROM users WHERE username='$username';" -N)
    
    context_flag="--context $server_name"

}



check_and_add_to_enabled() {
    if docker --context default exec caddy caddy validate --config /etc/caddy/Caddyfile >/dev/null 2>&1; then
        docker --context default exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1
        return 0
    else
        return 1
    fi
}

validate_conf() {

  	if [ $(docker --context default ps -q -f name=caddy) ]; then
        :
	else
        cd /root && docker --context default compose up -d caddy  >/dev/null 2>&1
     fi
     
	check_and_add_to_enabled
 
	if [ $? -eq 0 ]; then
		:
  #echo "Domain suspended successfully."
	else
        mv ${suspended_dir}${domain_name}.conf  $domain_vhost > /dev/null 2>&1
        check_and_add_to_enabled
		#echo "ERROR: Failed to validate conf after suspend, changes reverted."
	fi
}


suspend_user_websites() {
    user_id=$(mysql "$mysql_database" -e "SELECT id FROM users WHERE username='$username';" -N)
    if [ -z "$user_id" ]; then
        echo "ERROR: user $username not found in the database"
        exit 1
    fi

    
    domain_names=$(mysql -D "$mysql_database" -e "SELECT domain_url FROM domains WHERE user_id='$user_id';" -N)
    for domain_name in $domain_names; do
       domain_vhost="/etc/openpanel/caddy/domains/$domain_name.conf"

       if [ -f "${suspended_dir}${domain_name}.conf" ]; then
	    	:
       else       
           cp $domain_vhost ${suspended_dir}${domain_name}.conf
       fi

domain_conf=$(cat "$conf_template" | sed -e "s|<DOMAIN_NAME>|$domain_name|g")
      
echo "$domain_conf" > "$domain_vhost"
	        
    done
    
        validate_conf
}



stop_docker_container() {
    if [ "$DEBUG" = true ]; then
        echo "Pausing containers for user: $username"
        docker $context_flag ps --filter "name=${username}" --format "{{.Names}}" | while read -r container; do
            echo "- Pausing container: $container"
            docker $context_flag pause "$container"
        done
    else
        docker $context_flag ps --filter "name=${username}" --format "{{.Names}}" | while read -r container; do
            docker $context_flag pause "$container" > /dev/null 2>&1
        done
    fi
}


# Function to pause (suspend) a user
rename_user() {
    # Add a suspended timestamp prefix to the username in the database
    suspended_username="SUSPENDED_$(date +"%Y%m%d%H%M%S")_$username"

    # Update the username in the database with the suspended prefix
    mysql_query="UPDATE users SET username='$suspended_username' WHERE username='$username';"
    
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$mysql_query"

    if [ $? -eq 0 ]; then
        echo "User '$username' paused (suspended) successfully."
    else
        echo "Error: User pause (suspend) failed."
    fi
}

get_docker_context_for_user     # node ip and slave/master name
suspend_user_websites           # redirect domains to suspended_user.html page
stop_docker_container           # stop docker containerrename
rename_user                     # rename username in database
