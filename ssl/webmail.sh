#!/bin/bash
################################################################################
# Script Name: ssl/webmail.sh
# Description: Generate an SSL for the webmail domain
# Usage: opencli ssl-webmail
# Author: Stefan Pejcic
# Created: 15.10.2024
# Last Modified: 15.10.2024
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

# Check if domain argument is provided
if [ $# -lt 1 ]; then
    echo "Usage: opencli ssl-webmail <domain>"
    exit 1
fi

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
RESET='\033[0m'


# IP SERVERS
SCRIPT_PATH="/usr/local/admin/core/scripts/ip_servers.sh"
if [ -f "$SCRIPT_PATH" ]; then
    source "$SCRIPT_PATH"
else
    IP_SERVER_1=IP_SERVER_2=IP_SERVER_3="https://ip.openpanel.com"
fi



webmail_domain=$1

# Detect the public IP address
ip_address=$(hostname -I | awk '{print $1}')


# Path to Certbot certificates directory
certbot_cert_dir="/etc/letsencrypt/live/$webmail_domain"


# check and renew ssl
renew_ssl_check() {
    mkdir -p /var/www/html/.well-known/acme-challenge/
    chown -R www-data:www-data /var/www/html/
    echo "1" | certbot certonly --webroot -w /var/www/html -d $webmail_domain
}



replace_proxy_webmail() {
    local conf="/etc/openpanel/nginx/vhosts/openpanel_proxy.conf"
    sed -i "/# roundcube/,/}/s|https\?://[^/]\+/|https://$webmail_domain/|" "$conf"
    docker exec nginx sh -c "nginx -t && nginx -s reload" > /dev/null 2>&1
    echo "- /webmail on every domain redirects to https://${webmail_domain}/"
}



overwrite_conf(){
# Create the Nginx configuration file

        current_ip=$(curl --silent --max-time 2 -4 $IP_SERVER_1 || wget --timeout=2 -qO- $IP_SERVER_2 || curl --silent --max-time 2 -4 $IP_SERVER_3)
        
        # If site is not available, get the ipv4 from the hostname -I
        if [ -z "$current_ip" ]; then
            current_ip=$(ip addr|grep 'inet '|grep global|head -n1|awk '{print $2}'|cut -f1 -d/)
        fi

touch /etc/nginx/sites-enabled/${webmail_domain}.conf
cat <<EOL > "/etc/nginx/sites-enabled/${webmail_domain}.conf"
server {
    listen ${current_ip}:80;
    server_name ${webmail_domain};
    root /usr/share/nginx/html;
    location ^~ /.well-known {
        allow all;
        default_type "text/plain";
    }

    location / {
        proxy_pass http://localhost:8080;
    }
}
EOL
}

restart_nginx() {
    #docker exec nginx sh -c "nginx -t && nginx -s reload" > /dev/null 2>&1
    docker exec nginx sh -c "nginx -t > /dev/null 2>&1 && nginx -s reload > /dev/null 2>&1"
}



add_ssl_to_nginx() {
        # Nginx configuration content to be added
        nginx_config_content="
        if (\$scheme != \"https\"){
            return 301 https://\$host\$request_uri;
        } #forceHTTPS

        # Advertise HTTP/3 QUIC support (required)
        add_header X-protocol $server_protocol always;
        add_header Alt-Svc 'h3=":$server_port"; ma=86400';
        quic_retry on;

        listen $current_ip:443 ssl; #HTTP/2
        listen $current_ip:443 quic; #HTTP/3
        http2 on;
        ssl_certificate /etc/letsencrypt/live/$webmail_domain/fullchain.pem;
        ssl_certificate_key /etc/letsencrypt/live/$webmail_domain/privkey.pem;
        include /etc/letsencrypt/options-ssl-nginx.conf;
        ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;
        "
    
        marker="ssl_certificate_key /etc/letsencrypt/live/$webmail_domain/privkey.pem;"
        nginx_conf_path="/etc/nginx/sites-enabled/${webmail_domain}.conf"

    if grep -qF "$marker" "$nginx_conf_path"; then
        :
        #echo "Configuration already exists. No changes made."
    else 
        # Find the position of the last closing brace
        last_brace_position=$(awk '/\}/{y=x; x=NR} END{print y}' "$nginx_conf_path")
    
        # Insert the Nginx configuration content before the last closing brace
        awk -v content="$nginx_config_content" -v pos="$last_brace_position" 'NR == pos {print $0 ORS content; next} {print}' "$nginx_conf_path" > temp_file
        mv temp_file "$nginx_conf_path"
        restart_nginx
    fi
}





# Check if Certbot has an SSL certificate for the webmail domain
if [ -n "$webmail_domain" ] && [[ $webmail_domain == *.*.* ]]; then
    if [ -d "$certbot_cert_dir" ]; then
        echo "SSL certificate already exists for $webmail_domain."
        overwrite_conf
        add_ssl_to_nginx
        replace_proxy_webmail
    else       
        echo "No SSL certificate found for $webmail_domain. Proceeding to generate a new certificate..."
  
        mkdir -p /usr/share/nginx/html/.well-known/acme-challenge
        chown -R 0777 /usr/share/nginx/html/
        
      # create nginx conf
      overwrite_conf
      restart_nginx
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
        "-m" "webmaster@${webmail_domain}" "-d" "${webmail_domain}"
    )


    # Run Certbot command
    "${certbot_command[@]}"
    status=$?

        # Check if the Certbot command was successful
        if [ $status -eq 0 ]; then
            echo "$webmail_domain is now configured (with SSL) for accessing webmail client."
            replace_proxy_webmail
            add_ssl_to_nginx
        else
            # If certbot command fails
            rm /etc/nginx/sites-enabled/${webmail_domain}.conf
            echo -e "${RED}ERROR: Failed to generate SSL certificate for $webmail_domain. Check Certbot logs in '/var/log/letsencrypt/' for more details.${RESET}"
            echo -e  "Is ${YELLOW}A${RESET} record for domain ${YELLOW}$webmail_domain${RESET} pointed to the IP address of this server: ${YELLOW}$ip_address${RESET} ?"
            exit 1
        fi
   
    fi
else
    echo -e "${RED}ERROR: $webmail_domain is not a valid FQDN in format sub.domain.tld${RESET}"
    echo ""
    echo "A fully qualified domain name (FQDN), also known as an absolute domain name, specifies all domain levels written in the hostname.domain.tld format."
    echo "examples: webmail.example.net email.example.net roundcube.example.net"
    echo "Make sure to point your webmail domain A record to the IP address of this server $ip_address and then try again."
fi
