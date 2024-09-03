#!/bin/bash
################################################################################
# Script Name: ssl/hostname.sh
# Description: Generate an SSL for the hostname and use it to access OpenPanel and OpenAdmin.
# Usage: opencli ssl-hostname
# Author: Stefan Pejcic
# Created: 16.10.2023
# Last Modified: 03.09.2024
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

##### TODO: testirati samo sa --nginx umesto standalone i izbaciit ngins stop, restart

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
RESET='\033[0m'

# Check if Certbot and Nginx services are available
if ! docker ps --filter "name=nginx" --filter "status=running" --format "{{.Names}}"; then
    DISABLE_AFTERWARDS="YES" # if nginx was off, disable it after generation
    echo -e "${YELLOW}WARNING: Docker container 'nginx' is not running. Starting container...${RESET}"
    cd /root && docker compose up -d nginx
fi

# if admin was running, restart it.
if ! systemctl status admin &> /dev/null; then
	DO_NOT_START_ADMIN=true
else
 	DO_NOT_START_ADMIN=false
fi


# Detect current server hostname
hostname=$(hostname)

# Detect the public IP address
ip_address=$(hostname -I | awk '{print $1}')

# Path to Certbot certificates directory
certbot_cert_dir="/etc/letsencrypt/live/$hostname"


# check and renew ssl
renew_ssl_check() {
    mkdir -p /var/www/html/.well-known/acme-challenge/
    chown -R www-data:www-data /var/www/html/
    echo "1" | certbot certonly --webroot -w /var/www/html -d $hostname
}

# update OpenPanel configuration
update_openpanel_config() {
    local config_file="/etc/openpanel/openpanel/conf/openpanel.config"

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

        cd /root && docker compose restart nginx &> /dev/null
	
	# start admin panel only if it was already running
 	if [ DO_NOT_START_ADMIN ]; then
	        service admin reload &> /dev/null
	else
		service admin restart &> /dev/null
 	fi
  
        echo ""
	if ! docker ps --filter "name=openpanel" --filter "status=running" --format "{{.Names}}"; then
	        echo -e "- OpenPanel  is now available on: ${GREEN}https://$hostname:$port${RESET}"
	fi
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
        
        # try renew
        renew_ssl_check

        # set the panel to use the existing ssl
        update_openpanel_config
    else
        echo "No SSL certificate found for $hostname. Proceeding to generate a new certificate..."


mkdir -p /usr/share/nginx/html

# Get server ipv4 from ip.openpanel.co
current_ip=$(curl --silent --max-time 2 -4 https://ip.openpanel.co || wget --timeout=2 -qO- https://ip.openpanel.co || curl --silent --max-time 2 -4 https://ifconfig.me)
	
# If site is not available, get the ipv4 from the hostname -I
if [ -z "$current_ip" ]; then
    current_ip=$(ip addr|grep 'inet '|grep global|head -n1|awk '{print $2}'|cut -f1 -d/)
fi



# Create the Nginx configuration file
cat <<EOL > "/etc/nginx/sites-enabled/${hostname}.conf"
server {
    listen 80;
    server_name ${hostname};
    root /usr/share/nginx/html;
    location ^~ /.well-known {
        allow all;
        default_type "text/plain";
    }
    
}
EOL

docker exec nginx sh -c "nginx -t && nginx -s reload"

      certbot_command=(
        "docker" "run" "--rm" "--network" "host"
        "-v" "/etc/letsencrypt:/etc/letsencrypt"
        "-v" "/var/lib/letsencrypt:/var/lib/letsencrypt"
        "-v" "/etc/nginx/sites-available:/etc/nginx/sites-available"
        "-v" "/etc/nginx/sites-enabled:/etc/nginx/sites-enabled"
        "-v" "/usr/share/nginx/html/:/usr/share/nginx/html/"
        "certbot/certbot" "certonly" "--webroot"
        "--webroot-path=/usr/share/nginx/html"
        "--non-interactive" "--agree-tos"
        "-m" "webmaster@${hostname}" "-d" "${hostname}"
    )





    # Run Certbot command
    "${certbot_command[@]}"
    status=$?

# delete file always
rm -rf /usr/share/nginx/html
rm /etc/nginx/sites-enabled/${hostname}.conf


# if certbot was not running, disable it after generation
if [ "$DISABLE_AFTERWARDS" = "YES" ]; then
    echo -e "${YELLOW}Stopping the Nginx container...${RESET}"
    cd /root && docker compose down nginx
else
    docker exec nginx sh -c "nginx -t && nginx -s reload"
fi


        # Check if the Certbot command was successful
        if [ $status -eq 0 ]; then
            update_openpanel_config
        else
                # If certbot command fails
                echo -e "${RED}ERROR: Failed to generate SSL certificate. Check Certbot logs in '/var/log/letsencrypt/' for more details.${RESET}"
                echo -e  "Is ${YELLOW}A${RESET} record for domain ${YELLOW}$hostname${RESET} pointed to the IP address of this server: ${YELLOW}$ip_address${RESET} ?"
            exit 1
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
