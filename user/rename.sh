#!/bin/bash
################################################################################
# Script Name: user/rename.sh
# Description: Rename username.
# Usage: opencli user-rename <old_username> <new_username>
# Author: Radovan Jecmenica
# Created: 23.11.2023
# Last Modified: 10.06.2024
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
# Check if the correct number of arguments is provided
if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
    echo "Usage: $0 <old_username> <new_username>"
    exit 1
fi

old_username="$1"
new_username="$2"
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

#1. check for forbidden usernames
readarray -t forbidden_usernames < /etc/openpanel/openadmin/config/forbidden_usernames.txt

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

is_username_forbidden() {

    for forbidden_username in "${forbidden_usernames[@]}"; do
        if [ "$new_username" == "$forbidden_username" ]; then
            return 0 # Username is forbidden
        fi
    done
    return 1 # not forbidden
}

if is_username_forbidden "$new_username"; then
    echo "Error: Username '$new_username' is not allowed."
    exit 1
fi



# Check if Docker container with the same username exists
if docker inspect "$new_username" >/dev/null 2>&1; then
    echo "Error: Docker container with the same username '$new_username' already exists. Aborting."
    exit 1
fi


# DB
source /usr/local/admin/scripts/db.sh

# Check if the username already exists in the users table
username_exists_query="SELECT COUNT(*) FROM users WHERE username = '$new_username'"
username_exists_count=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$username_exists_query" -sN)

# Check if successful
if [ $? -ne 0 ]; then
    echo "Error: Unable to check username existence in the database."
    exit 1
fi

# count > 0) show error and exit
if [ "$username_exists_count" -gt 0 ]; then
    echo "Error: Username '$new_username' already exists."
    exit 1
fi








########### DONE CHECKS, RUN THE REMOUNT
umount /home/storage_file_$old_username > /dev/null 2>&1
mv /home/storage_file_$old_username /home/storage_file_$new_username > /dev/null 2>&1
mv /home/$old_username /home/$new_username > /dev/null 2>&1
mount -o loop /home/storage_file_$new_username /home/$new_username > /dev/null 2>&1
sed -i.bak "/\/home\/storage_file_$old_username \/home\/$old_username ext4 loop 0 0/c\/home\/storage_file_$new_username \/home\/$new_username ext4 loop 0 0" /etc/fstab  > /dev/null 2>&1


# Check if the container exists
if docker ps -a --format '{{.Names}}' | grep -q "^${old_username}$"; then
    # Rename the Docker container

# ove treba za nginx 1 za apache 2 da se radi!!!
        
    # Execute commands inside the container
    if [ "$DEBUG" = true ]; then
        docker exec "$old_username" \
            bash -c "usermod -l $new_username $old_username && \
            sed -i 's#/home/$old_username#/home/$new_username#g' /etc/apache2/sites-available/* && \
            sed -i 's#/home/$old_username#/home/$new_username#g' /etc/nginx/sites-available/* && \
            service nginx reload && \
            service apache2 reload"
        
        docker rename "$old_username" "$new_username"
        # Rename the folder outside the container

        echo "Container renamed successfully."
    else
            docker exec "$old_username" \
            bash -c "usermod -l $new_username $old_username && \
            sed -i 's#/home/$old_username#/home/$new_username#g' /etc/apache2/sites-available/* && \
            sed -i 's#/home/$old_username#/home/$new_username#g' /etc/nginx/sites-available/* && \
            service nginx reload && \
            service apache2 reload" > /dev/null 2>&1
        
        docker rename "$old_username" "$new_username" > /dev/null 2>&1
        # Rename the folder outside the container
    fi

else
    echo "Error: Container '$old_username' not found."
    exit 1
fi

if [ "$DEBUG" = true ]; then
    mv /etc/openpanel/openpanel/core/users/"$old_username" /etc/openpanel/openpanel/core/users/"$new_username" 
    rm /etc/openpanel/openpanel/core/users/$new_username/data.json
else
    mv /etc/openpanel/openpanel/core/users/"$old_username" /etc/openpanel/openpanel/core/users/"$new_username" > /dev/null 2>&1
    rm /etc/openpanel/openpanel/core/users/$new_username/data.json > /dev/null 2>&1
fi

ensure_jq_installed

server_shared_ip=$(hostname -I 2>/dev/null | awk '{print $1}')
json_file="/etc/openpanel/openpanel/core/users/$new_username/ip.json"

if [ "$DEBUG" = true ]; then
    if [ -e "$json_file" ]; then
        IP_TO_USE=$(jq -r '.ip' "$json_file")
        echo "User has dedicated IP: $IP_TO_USE."
    else
        IP_TO_USE="$server_shared_ip"
        echo "User has no dedicated IP assigned, using shared IP address: $IP_TO_USE."
    fi
else
    if [ -e "$json_file" ]; then
        IP_TO_USE=$(jq -r '.ip' "$json_file")
    else
        IP_TO_USE="$server_shared_ip"
    fi
fi


####### GET USERS IP TO BE USED FOR FIREWALL

edit_nginx_files_on_host_server() {
    USERNAME=$1
    NEW_USERNAME=$2
    NGINX_CONF_PATH="/etc/nginx/sites-available"
    ALL_DOMAINS=$(opencli domains-user $USERNAME)

    if [ "$DEBUG" = true ]; then
        # Loop through Nginx configuration files for the user
        for domain in $ALL_DOMAINS; do
            DOMAIN_CONF="$NGINX_CONF_PATH/$domain.conf"
            if [ -f "$DOMAIN_CONF" ]; then
                # Update name
                sed -i 's#/home/$old_username#/home/$NEW_USERNAME#g' "$DOMAIN_CONF"
                sed -i 's#/http://$old_username#/http://$NEW_USERNAME#g' "$DOMAIN_CONF"
                sed -i 's#/https://$old_username#/https://$NEW_USERNAME#g' "$DOMAIN_CONF"
                echo "Username updated in $DOMAIN_CONF to $NEW_USERNAME."
            fi
        done
        echo "Reloading nginx.."
        opencli server-recreate_hosts
        docker exec nginx bash -c "nginx -t && nginx -s reload"
    else
        # Loop through Nginx configuration files for the user
        for domain in $ALL_DOMAINS; do
            DOMAIN_CONF="$NGINX_CONF_PATH/$domain.conf"
            if [ -f "$DOMAIN_CONF" ]; then
                # Update the server IP using sed
                sed -i 's#/home/$old_username#/home/$NEW_USERNAME#g' "$DOMAIN_CONF" > /dev/null 2>&1
                sed -i 's#/http://$old_username#/http://$NEW_USERNAME#g' "$DOMAIN_CONF" > /dev/null 2>&1
                sed -i 's#/https://$old_username#/https://$NEW_USERNAME#g' "$DOMAIN_CONF" > /dev/null 2>&1
            fi
        done
        # Restart Nginx to apply changes
        opencli server-recreate_hosts > /dev/null 2>&1
        docker exec nginx bash -c "nginx -t && nginx -s reload" > /dev/null 2>&1
    fi


}


change_default_email () {
    hostname=$(hostname)
    docker exec "$new_username" bash -c "sed -i 's/^from\s\+.*/from       ${new_username}@${hostname}/' /etc/msmtprc"
}



################## UFW CHANGE COMMENT TO NEW USERNAME

extract_host_port() {
    local port_number="$1"
    local host_port
    host_port=$(docker port "$new_username" | grep "${port_number}/tcp" | awk -F: '{print $2}' | awk '{print $1}')
    echo "$host_port"
}


# Define the list of container ports to check and open
container_ports=("21" "22" "3306" "7681" "8080")

# Variable to track whether any ports were opened
ports_opened=0


# Delete exisitng rules for the old username
update_firewall_rules() {
    IP_TO_USE=$1
    if [ "$DEBUG" = true ]; then
        # Delete existing rules for the specified user
        ufw status numbered | awk -F'[][]' -v user="$old_username" '$NF ~ " " user "$" {gsub(/^[ \t]+|[ \t]+$/, "", $2); print $2}' | sort -rn | \
        
        while read -r rule_number; do
            yes | ufw delete "$rule_number"
        done

        # Loop through the container_ports array and open the ports in UFW if not already open
        for port in "${container_ports[@]}"; do
            host_port=$(extract_host_port "$port")
        
            if [ -n "$host_port" ]; then
                # Open the port in UFW
                echo "Opening port ${host_port} for port ${port} in UFW"
                ufw allow to $IP_TO_USE port "$host_port" proto tcp comment "$new_username"
                ports_opened=1
            else
                echo "Port ${port} not found in container"
            fi
        done

        # Restart UFW if ports were opened
        if [ $ports_opened -eq 1 ]; then
            echo "Restarting UFW"
            ufw reload
        fi
    else
        # Delete existing rules for the specified user
        while read -r rule_number; do
            yes | ufw delete "$rule_number" >/dev/null 2>&1
        done < <(ufw status numbered | awk -F'[][]' -v user="$old_username" '$NF ~ " " user "$" {gsub(/^[ \t]+|[ \t]+$/, "", $2); print $2}' | sort -rn)

        # Loop through the container_ports array and open the ports in UFW if not already open
        for port in "${container_ports[@]}"; do
            host_port=$(extract_host_port "$port")
        
            if [ -n "$host_port" ]; then
                # Open the port in UFW
                ufw allow to $IP_TO_USE port "$host_port" proto tcp comment "$new_username" >/dev/null 2>&1
                ports_opened=1
            else
                # Empty else block
                :
            fi
        done

        # Restart UFW if ports were opened
        if [ $ports_opened -eq 1 ]; then
            ufw reload >/dev/null 2>&1
        fi
    fi
}

# Function to rename user in the database
rename_user_in_db() {
    OLD_USERNAME=$1
    NEW_USERNAME=$2
    
    # Update the username in the database with the suspended prefix
    mysql_query="UPDATE users SET username='$NEW_USERNAME' WHERE username='$OLD_USERNAME';"
    
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$mysql_query"

    if [ $? -eq 0 ]; then
        echo "User '$OLD_USERNAME' successfully renamed to '$NEW_USERNAME'."
    else
        echo "Error: Changing username in database failed!"
    fi
}








replace_username_in_phpfpm_configs() {
    old_username="$1" # Assuming $1 is the old username
    new_username="$2" # Assuming $2 is the new username
    
    if [ "$DEBUG" = true ]; then
        # change user in www.conf file for each php-fpm verison
        docker exec $new_username find /etc/php/ -type f -name "www.conf" -exec sed -i 's/user = .*/user = '"$new_username"'/' {} \;
        # restart version
        docker exec $new_username bash -c 'for phpv in $(ls /etc/php/); do if [[ -d "/etc/php/$phpv/fpm" ]]; then service php${phpv}-fpm restart; fi done'
    else
        # change user in www.conf file for each php-fpm verison
        docker exec $new_username find /etc/php/ -type f -name "www.conf" -exec sed -i 's/user = .*/user = '"$new_username"'/' {} \; > /dev/null 2>&1
        # restart version
        docker exec $new_username bash -c 'for phpv in $(ls /etc/php/); do if [[ -d "/etc/php/$phpv/fpm" ]]; then service php${phpv}-fpm restart; fi done' > /dev/null 2>&1
    fi    
}








replace_username_in_phpfpm_configs "$old_username" "$new_username"
edit_nginx_files_on_host_server "$old_username" "$new_username"

# Check for CSF
if command -v csf >/dev/null 2>&1; then
    # do nothing for csf, ports are already opened..
    FIREWALL="CSF"
elif command -v ufw >/dev/null 2>&1; then
    # rename username in ufw comments
    FIREWALL="UFW"
    update_firewall_rules "$IP_TO_USE"
fi

rename_user_in_db "$old_username" "$new_username"
change_default_email
