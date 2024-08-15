#!/bin/bash
################################################################################
# Script Name: user/ip.sh
# Description: Assing or remove dedicated IP to a user.
# Usage: opencli user-ip <USERNAME> <IP | DELETE> [-y] [--debug]
# Author: Radovan Jecmenica
# Created: 23.11.2023
# Last Modified: 03.01.2024
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

# TODO: handle csf instead of ufw for dedi ip port rules!

USERNAME=$1
ACTION=$2
CONFIRM_FLAG=$3
SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
ALL_DOMAINS=$(opencli domains-user $USERNAME)
NGINX_CONF_PATH="/etc/nginx/sites-available/"
JSON_FILE="/etc/openpanel/openpanel/core/users/$USERNAME/ip.json"
DEBUG=false  # Default value for DEBUG

# Check if DEBUG flag is set
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        *)
            ;;
    esac
done

# Check if username is provided
if [ -z "$USERNAME" ]; then
    echo "Usage: $0 <USERNAME> <ACTION> [ -y ] [--debug]"
    exit 1
fi
# Print only the allowed IP addresses
ALLOWED_IP_ADDRESSES=$(hostname -I | tr ' ' '\n' | grep -v '^172\.' | tr '\n' ' '  )


# Function to check if the IP is allowed
check_ip_validity() {
    CHECK_IP=$1
    if ! echo "$ALLOWED_IP_ADDRESSES" | grep -q "$CHECK_IP"; then
        echo "Error: The provided IP address is not allowed. It must be one of the addresses $ALLOWED_IP_ADDRESSES"
        exit 1
    fi
}
ensure_jq_installed() {
    # Check if jq is installed
    if ! command -v jq &> /dev/null; then
        # Install jq using apt
        sudo apt-get update > /dev/null 2>&1
        sudo apt-get install -y -qq jq > /dev/null 2>&1
        # Check if installation was successful
        if ! command -v jq &> /dev/null; then
            echo "Error: jq installation failed. Please install jq manually and try again."
            exit 1
        fi
    fi
}
# Function to check if the IP is used by another user
check_ip_usage() {
    CHECK_IP=$1
    ALL_USERS=$(ls /etc/openpanel/openpanel/core/users)
    for USER in $ALL_USERS; do
        if [ "$USER" != "$USERNAME" ]; then
            USER_JSON="/etc/openpanel/openpanel/core/users/$USER/ip.json"
            if [ -e "$USER_JSON" ]; then
                USER_IP=$(jq -r '.ip' "$USER_JSON")
                if [ "$USER_IP" = "$CHECK_IP" ]; then
                    if [ "$CONFIRM_FLAG" != "-y" ]; then
                        echo "Error: The IP address is already associated with user $USER."

                        read -p "Are you sure you want to continue? (y/n): " CONFIRM
                        if [ "$CONFIRM" != "y" ]; then
                            echo "Script aborted."
                            exit 1
                        fi
                    fi
                fi
            fi
        fi
    done
}

#Function to delete user's IP configuration
delete_ip_config() {
    JSON_FILE="/etc/openpanel/openpanel/core/users/$USER/ip.json"
    if [ -e "$JSON_FILE" ]; then
        rm -f "$JSON_FILE"
        echo "IP configuration deleted for user $USERNAME."
    else
        echo "No IP configuration found for user $USERNAME."
    fi
}


update_nginx_conf() {
    USERNAME=$1
    JSON_FILE="/etc/openpanel/openpanel/core/users/$USERNAME/ip.json"
    NGINX_CONF_PATH="/etc/nginx/sites-available"
    SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
    ALL_DOMAINS=$(opencli domains-user $USERNAME)

    # Check if the JSON file for the user exists
    if [ -e "$JSON_FILE" ]; then
        IP_TO_CHANGE=$(jq -r '.ip' "$JSON_FILE")
    else
        IP_TO_CHANGE="$SERVER_IP"
    fi

    # Loop through Nginx configuration files for the user
    if [ "$DEBUG" = true ]; then
        for domain in $ALL_DOMAINS; do
            DOMAIN_CONF="$NGINX_CONF_PATH/$domain.conf"
            if [ -f "$DOMAIN_CONF" ]; then
                # Update the server IP using sed
                sed -i "s/listen [0-9.]\+/listen $IP_TO_CHANGE/g" "$DOMAIN_CONF"
                echo "Server IP updated for $DOMAIN_CONF to $IP_TO_CHANGE."
            fi
        done
    else
        for domain in $ALL_DOMAINS; do
            DOMAIN_CONF="$NGINX_CONF_PATH/$domain.conf"
            if [ -f "$DOMAIN_CONF" ]; then
                # Update the server IP using sed
                sed -i "s/listen [0-9.]\+/listen $IP_TO_CHANGE/g" "$DOMAIN_CONF"
            fi
        done
    fi
    

    # Restart Nginx to apply changes
    docker exec nginx nginx -s reload
}

# Create or overwrite the JSON file
create_ip_file() {
    USERNAME=$1
    IP=$2
    JSON_FILE="/etc/openpanel/openpanel/core/users/$USERNAME/ip.json"
    echo "{ \"ip\": \"$IP\" }" > "$JSON_FILE"
    if [ "$DEBUG" = true ]; then
        echo "IP file created/updated for user $USERNAME with IP $IP."
    fi
}

update_firewall_rules() {
    USERNAME=$1
    # Delete existing rules for the specified user
    if [ "$DEBUG" = true ];then
        ufw status numbered | awk -F'[][]' -v user="$USERNAME" '$NF ~ " " user "$" {gsub(/^[ \t]+|[ \t]+$/, "", $2); print $2}' | sort -rn| \
        while read -r rule_number; do
            yes | ufw delete "$rule_number"
        done
    else
        ufw status numbered | awk -F'[][]' -v user="$USERNAME" '$NF ~ " " user "$" {gsub(/^[ \t]+|[ \t]+$/, "", $2); print $2}' | sort -rn| \
        while read -r rule_number; do
            yes | ufw delete "$rule_number"  >/dev/null 2>&1
        done
    fi


}

current_ip () {
    USERNAME=$1
    JSON_FILE="/etc/openpanel/openpanel/core/users/$USERNAME/ip.json"
    SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
    # Check if the JSON file for the user exists
    if [ "$DEBUG" = true ]; then
        if [ -e "$JSON_FILE" ]; then
            CURRENT_IP=$(jq -r '.ip' "$JSON_FILE")
            echo "$CURRENT_IP"
        else
            CURRENT_IP="$SERVER_IP"
            echo "$CURRENT_IP"
        fi
    else
        if [ -e "$JSON_FILE" ]; then
            CURRENT_IP=$(jq -r '.ip' "$JSON_FILE")
        else
            CURRENT_IP="$SERVER_IP"
        fi
    fi
}

update_dns_zone_file() {
    USERNAME=$1
    JSON_FILE="/etc/openpanel/openpanel/core/users/$USERNAME/ip.json"
    SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
    ALL_DOMAINS=$(opencli domains-user $USERNAME)
    ZONE_FILE="/etc/bind/zones"

    # Check if the JSON file for the user exists
    if [ -e "$JSON_FILE" ]; then
        IP_TO_CHANGE=$(jq -r '.ip' "$JSON_FILE")
    else
        IP_TO_CHANGE="$SERVER_IP"
    fi

    # Loop through Nginx configuration files for the user
    if [ "$DEBUG" = true ]; then
        for domain in $ALL_DOMAINS; do
            ZONE_CONF="$ZONE_FILE/$domain.zone"
            if [ -f "$DOMAIN_CONF" ]; then
                # Update the server IP using sed
                sed -i "s/$CURRENT_IP/$IP_TO_CHANGE/g" "$ZONE_CONF"
                echo "Server IP updated for $ZONE_CONF to $IP_TO_CHANGE."
            fi
        done
    else
        for domain in $ALL_DOMAINS; do
            ZONE_CONF="$ZONE_FILE/$domain.zone"
            if [ -f "$DOMAIN_CONF" ]; then
                # Update the server IP using sed
                sed -i "s/$CURRENT_IP/$IP_TO_CHANGE/g" "$ZONE_CONF"        
            fi
        done
    fi
}



ensure_jq_installed
# Check if the action is 'delete'
if [ "$ACTION" = "delete" ]; then
    current_ip "$USERNAME" 
    delete_ip_config
    update_nginx_conf "$USERNAME" 
    update_firewall_rules "$USERNAME"
    update_dns_zone_file "$USERNAME"
else
# If the action is not 'delete', continue with IP update
IP=$2
# Check if IP is provided
if [ -z "$IP" ]; then
    echo "Usage: $0 <USERNAME> <ACTION> [ -y ]"
    exit 1
fi
# Check if the IP is already used by another user
current_ip "$USERNAME" 
check_ip_validity "$IP"
check_ip_usage "$IP" "$CONFIRM_FLAG"
# Call the function to update Nginx configuration
create_ip_file "$USERNAME" "$IP"
update_nginx_conf "$USERNAME" "$IP"
update_firewall_rules "$USERNAME"
update_dns_zone_file "$USERNAME"
fi


extract_host_port() {
    local port_number="$1"
    local host_port
    host_port=$(docker port "$USERNAME" | grep "${port_number}/tcp" | awk -F: '{print $2}' | awk '{print $1}')
    echo "$host_port"
}

# Define the list of container ports to check and open
container_ports=("21" "22" "3306" "7681" "8080")

# Variable to track whether any ports were opened
ports_opened=0




# Loop through the container_ports array and open the ports in UFW if not already open
for port in "${container_ports[@]}"; do
    host_port=$(extract_host_port "$port")

    if [ "$DEBUG" = true ]; then
        if [ -n "$host_port" ]; then
            # Open the port in CSF
            echo "Opening port ${host_port} for port ${port} in CSF"
            #csf -a "0.0.0.0" "${host_port}" "TCP" "Allow incoming traffic for port ${host_port}"
            #ufw allow ${host_port}/tcp  comment "${username}"

                if [ "$ACTION" = "delete" ]; then
                    ufw allow to $SERVER_IP port "$host_port" proto tcp comment "$USERNAME"
                else
                    IP=$2 # Assuming the IP should be the second argument
                    ufw allow to "$IP" port "$host_port" proto tcp comment "$USERNAME"
                fi

            ports_opened=1
        else
            echo "Port ${port} not found in container"
        fi
    else
            if [ -n "$host_port" ]; then
            # Open the port in CSF
            echo "Opening port ${host_port} for port ${port} in CSF" >/dev/null 2>&1
            #csf -a "0.0.0.0" "${host_port}" "TCP" "Allow incoming traffic for port ${host_port}"
            #ufw allow ${host_port}/tcp  comment "${username}"

                if [ "$ACTION" = "delete" ]; then
                    ufw allow to $SERVER_IP port "$host_port" proto tcp comment "$USERNAME" >/dev/null 2>&1
                else
                    IP=$2 # Assuming the IP should be the second argument
                    ufw allow to "$IP" port "$host_port" proto tcp comment "$USERNAME" >/dev/null 2>&1
                fi

            ports_opened=1
            fi
        fi
done

# Restart UFW if ports were opened
if [ "$DEBUG" = true ]; then
    if [ $ports_opened -eq 1 ]; then
        echo "Restarting UFW"
        ufw reload
    fi
else
    if [ $ports_opened -eq 1 ]; then
        ufw reload >/dev/null 2>&1
    fi
fi

if [ $? -eq 0 ]; then
    echo "IP successfully changed for user $USERNAME to: $IP_TO_CHANGE"
else
    echo "Error: Data insertion failed."
    exit 1
fi
