#!/bin/bash
################################################################################
# Script Name: domains/suspend.sh
# Description: Suspend a domain name
# Usage: opencli domains-suspend <DOMAIN-NAME>
# Author: Stefan Pejcic
# Created: 04.11.2024
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

# DB
source /usr/local/admin/scripts/db.sh





get_docker_context_for_user(){
    # GET CONTEXT NAME FOR DOCKER COMMANDS
    server_name=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "SELECT server FROM users WHERE username='$username';" -N)
    
    if [ -z "$server_name" ]; then
        server_name="default" # compatibility with older panel versions before clustering
        context_flag=""
        node_ip_address=""
    elif [ "$server_name" == "default" ]; then
        context_flag=""
        node_ip_address=""
    else
        context_flag="--context $server_name"
        # GET IPV4 FOR SSH COMMANDS
        context_info=$(docker context ls --format '{{.Name}} {{.DockerEndpoint}}' | grep "$server_name")
    
        if [ -n "$context_info" ]; then
            endpoint=$(echo "$context_info" | awk '{print $2}')
            if [[ "$endpoint" == ssh://* ]]; then
                node_ip_address=$(echo "$endpoint" | cut -d'@' -f2 | cut -d':' -f1)
            else
                echo "ERROR: valid IPv4 address for context $server_name not found!"
                echo "       User container is located on node $server_name and there is a docker context with the same name but it has no valid IPv4 in the endpoint."
                echo "       Make sure that the docker context named $server_name has valid IPv4 address in format: 'SERVER ssh://USERNAME@IPV4' and that you can establish ssh connection using those credentials."
                exit 1
            fi
        else
            echo "ERROR: docker context with name $server_name does not exist!"
            echo "       User container is located on node $server_name but there is no docker context with that name."
            echo "       Make sure that the docker context exists and is available via 'docker context ls' command."
            exit 1
        fi
        
    fi



    # context         - node name
    # context_flag    - docker context to use in docker commands
    # node_ip_address - ipv4 to use for ssh
    
}










# Function to fetch the owner username of a domain
get_docker_context_for_user() {
    local domain="$1"
    
    # Check if the config file exists
    if [ ! -f "$config_file" ]; then
        echo "Config file $config_file not found."
        exit 1
    fi
    
    # Query to fetch the user_id for the specified domain
    user_id_query="SELECT user_id FROM domains WHERE domain_name = '$domain'"
    
    # Execute the query and fetch the user_id
    user_id=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "$user_id_query" -sN)

    if [ -z "$user_id" ]; then
        echo "Domain '$domain' not found in the database."
    else
        username_query="SELECT server FROM users WHERE id = '$user_id'"
        server_name=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "$username_query" -sN)
        if [ -z "$server_name" ]; then
            server_name="default" # compatibility with older panel versions before clustering
            context_flag=""
            node_ip_address=""
        elif [ "$server_name" == "default" ]; then
            context_flag=""
            node_ip_address=""
        else
            context_flag="--context $server_name"
            # GET IPV4 FOR SSH COMMANDS
            context_info=$(docker context ls --format '{{.Name}} {{.DockerEndpoint}}' | grep "$server_name")
        
            if [ -n "$context_info" ]; then
                endpoint=$(echo "$context_info" | awk '{print $2}')
                if [[ "$endpoint" == ssh://* ]]; then
                    node_ip_address=$(echo "$endpoint" | cut -d'@' -f2 | cut -d':' -f1)
                else
                    echo "ERROR: valid IPv4 address for context $server_name not found!"
                    echo "       User container is located on node $server_name and there is a docker context with the same name but it has no valid IPv4 in the endpoint."
                    echo "       Make sure that the docker context named $server_name has valid IPv4 address in format: 'SERVER ssh://USERNAME@IPV4' and that you can establish ssh connection using those credentials."
                    exit 1
                fi
            else
                echo "ERROR: docker context with name $server_name does not exist!"
                echo "       User container is located on node $server_name but there is no docker context with that name."
                echo "       Make sure that the docker context exists and is available via 'docker context ls' command."
                exit 1
            fi        
    fi
}


edit_nginx_vhosts() {
       if [ -f "/etc/nginx/sites-available/$domain_name.conf" ]; then
            echo "Suspending domain: $domain_name"
            if [ -n "$node_ip_address" ]; then
                # TODO: INSTEAD OF ROOT USER SSH CONFIG OR OUR CUSTOM USER!
                if [ "$DEBUG" = true ]; then
                    ssh "root@$node_ip_address" "sed -i 's/set \$suspended_website 0;/set \$suspended_website 1;/g'"
                    sed_status=$?
                else
                    ssh "root@$node_ip_address" "sed -i 's/set \$suspended_website 0;/set \$suspended_website 1;/g'" > /dev/null 2>&1
                    sed_status=$?
                fi
            else
                if [ "$DEBUG" = true ]; then
                    sed -i 's/set $suspended_website 0;/set $suspended_website 1;/g'
                    sed_status=$?
                else
                    sed -i 's/set $suspended_website 0;/set $suspended_website 1;/g' > /dev/null 2>&1
                    sed_status=$?
                fi
            fi
            
            if [ "$DEBUG" = true ]; then
                docker $context_flag exec nginx sh -c 'nginx -t && nginx -s reload'
            else
                docker $context_flag exec nginx sh -c 'nginx -t && nginx -s reload' > /dev/null 2>&1
            fi

            if [ $sed_status -eq 0 ]; then
                echo "Domain $domain_name suspended successfully."
            else
                echo "ERROR: Failed to suspend domain $domain_name."
                exit 1
            fi
            
        else
            echo "WARNING: vhost file for domain $domain_name does not exist"
        fi
}



# Check for the domain argument
if [ $# -ne 1 ]; then
    echo "Usage: $0 <domain_name>"
    exit 1
fi

# Get the domain name from the command line argument
domain_name="$1"

get_docker_context_for_user           # get node and ip
edit_nginx_vhosts                     # redirect domain to suspended_website.html
