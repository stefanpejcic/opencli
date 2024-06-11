#!/bin/bash

##########################################################################################
# Welcome Message for OpenPanel users                                                    #
#                                                                                        #
# This script displays a welcome message to users upon logging into the server.          #
#                                                                                        #
# To edit and make this script executable, use:                                          #
# nano /etc/openpanel/skeleton/welcome.sh && chmod +x /etc/openpanel/skeleton/welcome.sh #
#                                                                                        #
# Author: Stefan Pejcic (stefan@pejcic.rs)                                               #
##########################################################################################

VERSION=$(cat /usr/local/panel/version)
CONFIG_FILE_PATH='/etc/openpanel/openpanel/conf/openpanel.config'
OUTPUT_FILE='/etc/openpanel/skeleton/motd'
GREEN='\033[0;32m'
RED='\033[0;31m'
RESET='\033[0m'

mkdir -p /etc/openpanel/skeleton/
touch $OUTPUT_FILE

read_config() {
    config=$(awk -F '=' '/\[DEFAULT\]/{flag=1; next} /\[/{flag=0} flag{gsub(/^[ \t]+|[ \t]+$/, "", $1); gsub(/^[ \t]+|[ \t]+$/, "", $2); print $1 "=" $2}' $CONFIG_FILE_PATH)
    echo "$config"
}

get_ssl_status() {
    config=$(read_config)
    ssl_status=$(echo "$config" | grep -i 'ssl' | cut -d'=' -f2)
    [[ "$ssl_status" == "yes" ]] && echo true || echo false
}

get_custom_port() {
    config=$(read_config)
    custom_port=$(echo "$config" | grep -i 'port' | cut -d'=' -f2)
    echo $custom_port
}

custom_port=$(get_custom_port)

if [ -z "$custom_port" ]; then 
    PORT="2083"
else  
    PORT="$custom_port"
fi

get_force_domain() {
    config=$(read_config)
    force_domain=$(echo "$config" | grep -i 'force_domain' | cut -d'=' -f2)

    if [ -z "$force_domain" ]; then
        ip=$(get_public_ip)
        force_domain="$ip"
    fi
    echo "$force_domain"
}

get_public_ip() {
    ip=$(curl -s https://ip.openpanel.co)
    
    # If curl fails, try wget
    if [ -z "$ip" ]; then
        ip=$(wget -qO- https://ip.openpanel.co)
    fi
    
    # Check if IP is empty or not a valid IPv4
    if [ -z "$ip" ] || ! [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        ip=$(hostname -I | awk '{print $1}')
    fi
    echo "$ip"
}

if [ "$(get_ssl_status)" == true ]; then
    hostname=$(get_force_domain)
    user_url="https://${hostname}:${PORT}/"
else
    ip=$(get_public_ip)
    user_url="http://${ip}:${PORT}/"
fi

{
    echo -e  "================================================================"
    echo -e  ""
    echo -e  "This server has installed OpenPanel 🚀"
    echo -e  ""
    echo -e  "OPENPANEL LINK: ${GREEN}${user_url}${RESET}"
    echo -e  ""
    echo -e  "Need assistance or looking to learn more? We've got you covered:"
    echo -e  "        - 📚 User Docs: https://openpanel.co/docs/user/intro/"
    echo -e  "        - 💬 Forums: https://community.openpanel.co/"
    echo -e  "        - 👉 Discord: https://discord.openpanel.co/"
    echo -e  ""
    echo -e  "================================================================"
} > $OUTPUT_FILE
