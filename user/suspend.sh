#!/bin/bash
################################################################################
# Script Name: user/suspend.sh
# Description: Suspend user: stop container and suspend domains.
# Usage: opencli user-suspend <USERNAME>
# Author: Stefan Pejcic
# Created: 01.10.2023
# Last Modified: 04.11.2024
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
    echo "Usage: $0 <username>"
    exit 1
fi

# Get username from command-line argument
username="$1"
DEBUG=false  # Default value for DEBUG

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
source /usr/local/admin/scripts/db.sh




get_docker_context_for_user() {
    # GET CONTEXT NAME FOR DOCKER COMMANDS
    server_name=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "SELECT server FROM users WHERE username='$username';" -N)
    
    context_flag="--context $server_name"

}






suspend_user_websites() {
    user_id=$(mysql "$mysql_database" -e "SELECT id FROM users WHERE username='$username';" -N)
    if [ -z "$user_id" ]; then
        echo "ERROR: user $username not found in the database"
        exit 1
    fi
    
    domain_names=$(mysql -D "$mysql_database" -e "SELECT domain_name FROM domains WHERE user_id='$user_id';" -N)
    for domain_name in $domain_names; do
       domain_vhost="/etc/openpanel/caddy/domains/$domain_name.conf"
       if [ -f "$domain_vhost" ]; then
            echo "- Suspending domain: $domain_name"
        else
            echo "WARNING: vhost file for domain $domain_name does not exist -Skipping"
        fi

        cp $domain_vhost /etc/openpanel/caddy/suspended_domains/$domain_name.conf
        cp $suspended_template $domain_vhost
        # todo sed
    done

        if [ "$DEBUG" = true ]; then
            echo "Reloading caddy to redirect user's suspended domains"
        fi
            docker restart caddy > /dev/null 2>&1
    
}



stop_docker_container() {
    if [ "$DEBUG" = true ]; then
        echo "Stopping docker container"
        docker $context_flag stop "$username"
    else
        docker $context_flag stop "$username" > /dev/null 2>&1
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
