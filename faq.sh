#!/bin/bash

GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
PURPLE='\033[0;35m'
NC='\033[0m' #reset
CONFIG_FILE_PATH='/usr/local/panel/conf/panel.config'
service_name="admin"

read_config() {
    config=$(awk -F '=' '/\[DEFAULT\]/{flag=1; next} /\[/{flag=0} flag{gsub(/^[ \t]+|[ \t]+$/, "", $1); gsub(/^[ \t]+|[ \t]+$/, "", $2); print $1 "=" $2}' $CONFIG_FILE_PATH)
    echo "$config"
}

get_ssl_status() {
    config=$(read_config)
    ssl_status=$(echo "$config" | grep -i 'ssl' | cut -d'=' -f2)
    [[ "$ssl_status" == "yes" ]] && echo true || echo false
}

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
    if [ -z "$ip" ] || ! [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        ip=$(hostname -I | awk '{print $1}')
    fi
    echo "$ip"
}

if [ "$(get_ssl_status)" == true ]; then
        hostname=$(get_force_domain)
        admin_url="https://${hostname}:2087/"
else
        ip=$(get_public_ip)
        admin_url="http://${ip}:2087/"
fi

echo -e "
Frequently Asked Questions

${PURPLE}1.${NC} What is the login link for admin panel?

LINK: ${GREEN}${admin_url}${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}2.${NC} How to restart OpenAdmin or OpenPanel services?

- OpenPanel: ${RED}service panel restart${NC}
- OpenAdmin: ${RED}service admin restart${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}3.${NC} How to reset admin password?

execute command ${GREEN}opencli admin password USERNAME NEW_PASSWORD${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}4.${NC} How to create new admin account ?

execute command ${GREEN}opencli admin new USERNAME PASSWORD${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}5.${NC} How to list admin accounts ?

execute command ${GREEN}opencli admin list${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}6.${NC} How to check OpenPanel version ?

execute command ${GREEN}opencli --version${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}7.${NC} How to update OpenPanel ?

execute command ${GREEN}opencli update --force${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}8.${NC} How to disable automatic updates?

execute command ${GREEN}opencli config update autoupdate off${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}9.${NC} Where are the logs?

- OpenPanel: ${GREEN}/var/log/openpanel/user/error.log${NC}
- OpenAdmin: ${GREEN}/var/log/openpanel/admin/error.log${NC}
- API: ${GREEN}/var/log/openpanel/admin/api.log${NC}

${BLUE}------------------------------------------------------------${NC}
"
