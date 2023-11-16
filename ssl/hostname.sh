#!/bin/bash
################################################################################
# Script Name: ssl/hostname.sh
# Description: Generate an SSL for the hostname and use it to access OpenPanel and AdminPanel.
# Usage: opencli ssl-hostname
# Author: Stefan Pejcic
# Created: 16.10.2023
# Last Modified: 16.11.2023
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

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
RESET='\033[0m'

# Check if Certbot and OpenPanel services are available
if ! command -v certbot &> /dev/null; then
    echo -e "${RED}ERROR: Certbot command not found. Install Certbot before running this script.${RESET}"
    exit 1
fi

if ! systemctl status panel &> /dev/null; then
    echo -e "${RED}ERROR: OpenPanel service not found or not running. Check OpenPanel service status and ensure it's running.${RESET}"
    echo ""
    echo -e "Run ${YELLOW}'service panel status'${RESET} to check if panel is active."
    echo -e "and ${YELLOW}'tail /var/log/openpanel/user/error.log'${RESET} if service status is ${RED}failed${RESET}."
    exit 1
fi

if ! systemctl status admin &> /dev/null; then
    echo -e "${RED}ERROR: AdminPanel service not found or not running. Check AdminPanel service status and ensure it's running.${RESET}"
    echo ""
    echo -e "Run ${YELLOW}'service admin status'${RESET} to check if admin is active."
    echo -e "and ${YELLOW}'tail /var/log/openpanel/admin/error.log'${RESET} if service status is ${RED}failed${RESET}."
    exit 1
fi


# Detect current server hostname
hostname=$(hostname)

# Detect the public IP address
ip_address=$(hostname -I | awk '{print $1}')

# Path to Certbot certificates directory
certbot_cert_dir="/etc/letsencrypt/live/$hostname"


# Function to update OpenPanel configuration
update_openpanel_config() {
    local config_file="/usr/local/panel/conf/panel.config"

    if grep -q "ssl=" "$config_file"; then
        # Enable https:// for the OpenPanel
        sed -i 's/ssl=.*/ssl=yes/' "$config_file"

        # Get port from panel.config or fallback to 2083
        local port=$(grep -Eo 'port=[0-9]+' "$config_file" | cut -d '=' -f 2)
        port="${port:-2083}"

        # Redirect all 
        sed -i "s/force_domain=.*/force_domain=$hostname/" "$config_file"
        echo "ssl is now enabled and force_domain value in $config_file is set to '$hostname'."
        echo "Restarting the panel services to apply the newly generated SSL and force domain $hostname."

        service panel restart
        service admin restart

        echo ""
        echo -e "- OpenPanel  is now available on: ${GREEN}https://$hostname:$port${RESET}"
        echo -e "- AdminPanel is now available on: ${GREEN}https://$hostname:2087${RESET}"
        echo ""
    else
        echo ""
        echo -e "${RED}ERROR: Could not find 'ssl=' in '$config_file'.${RESET}"
        echo "SSL is successfully generated for $hostname but the OpenPanel configuration file is corrupted and needs to be restored."
    fi
}


# Check if Certbot has an SSL certificate for the hostname
if [ -n "$hostname" ] && [[ $hostname == *.*.* ]]; then
    if [ -d "$certbot_cert_dir" ]; then
        echo "SSL certificate already exists for $hostname."
        # just set the panel to use the existing ssl
        update_openpanel_config
    else
        echo "No SSL certificate found for $hostname. Proceeding to generate a new certificate..."

        # Stop Nginx
        service nginx stop

        # Run certbot command to obtain a new certificate
        if certbot certonly --standalone --non-interactive --agree-tos -m admin@$hostname -d "$hostname"; then
            # Restart Nginx as soon as the ssl is generated
            service nginx restart

            # Update OpenPanel configuration to use the new ssl
            update_openpanel_config
        else
            # If certbot command fails
            service nginx restart
            echo -e "${RED}ERROR: Failed to generate SSL certificate. Check Certbot logs for more details.${RESET}"
            echo -e  "Is ${YELLOW}A${RESET} record for domain ${YELLOW}$hostname${RESET} pointed to the IP address of this server: ${YELLOW}$ip_address${RESET} ?"
        fi
    fi
else
    echo -e "${RED}ERROR: Unable to detect a valid hostname that is FQDN in format sub.domain.tld${RESET}"
    echo ""
    echo "A fully qualified domain name (FQDN), also known as an absolute domain name, specifies all domain levels written in the hostname.domain.tld format."
    echo "examples: www.example.com srv.example.net server.site.rs"
    echo -e "To set a hostname use command: '${YELLOW}hostname <hostname_here>${RESET}' for example: '${YELLOW}hostname my.domain.net${RESET}' "
    echo "and make sure to point your hostname A record to the IP address of this server $ip_address"
fi
