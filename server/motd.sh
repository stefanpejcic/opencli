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

DOCS_LINK="https://openpanel.com/docs/user/intro/"
FORUM_LINK="https://community.openpanel.com/"
DISCORD_LINK="https://discord.openpanel.com/"


# IP SERVERS
SCRIPT_PATH="/usr/local/admin/core/scripts/ip_servers.sh"
if [ -f "$SCRIPT_PATH" ]; then
    source "$SCRIPT_PATH"
else
    IP_SERVER_1=IP_SERVER_2=IP_SERVER_3="https://ip.openpanel.com"
fi





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
    ip=$(curl --silent --max-time 2 -4 $IP_SERVER_1 || wget --timeout=2 -qO- $IP_SERVER_2 || curl --silent --max-time 2 -4 $IP_SERVER_3)

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
    echo -e  "        - 📚 User Docs: $DOCS_LINK"
    echo -e  "        - 💬 Forums:    $FORUM_LINK"
    echo -e  "        - 👉 Discord:   $DISCORD_LINK"
    echo -e  ""
    echo -e  "================================================================"
} > $OUTPUT_FILE
