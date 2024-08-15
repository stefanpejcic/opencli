#!/bin/bash
################################################################################
# Script Name: domain/enable_modsec.sh
# Description: Add nginx modsec lines to nginx conf
# Usage: opencli domains-enable_modsec <USERNAME>
# Author: Radovan Jecmenica
# Created: 14.12.2023
# Last Modified: 21.12.2023
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

if [ -z "$1" ]; then
    # If no username provided, insert modsec lines for all active users
    usernames=$(opencli user-list --json | grep -v 'SUSPENDED' | awk -F'"' '/username/ {print $4}')
else
    # If username provided, process logs only for that user
    usernames=("$1")
fi

# Iterate through users
for username in $usernames; do
    echo "Processing user: $username"

    # Get the domains for the current user
    domains=$(opencli domains-user "$username")

    # Check if the result contains "No domains found for user '$username'"
    if [[ "$domains" == *"No domains found for user '$username'"* ]]; then
        echo "No domains found for user $username. Skipping."
    else
        # Iterate through each domain and update Nginx configuration
        for domain in $domains; do
            echo "Processing domain: $domain"

            # Define Nginx conf file path
            nginx_conf="/etc/nginx/sites-available/$domain.conf"

            # Check if modsecurity lines exist in the conf file
            if  grep -q "modsecurity_rules_file /etc/nginx/modsec/main.conf;" "$nginx_conf"; then
                echo "ModSecurity lines already exist in $nginx_conf. Skipping."
            else
                # Use sed to update the content after ONLY FIRST server_name line
                #sed -i "/server_name /s/$/\\n\tmodsecurity on;\\n\tmodsecurity_rules_file \/etc\/nginx\/modsec\/main.conf;/" "$nginx_conf"
                awk '/server_name / && !done { print "    modsecurity on;\n    modsecurity_rules_file /etc/nginx/modsec/main.conf;"; done=1 } 1' "$nginx_conf" > temp && mv temp "$nginx_conf"
                echo "ModSecurity lines added to $nginx_conf."
            fi
        done
    fi
done

docker exec nginx bash -c "nginx -t && nginx -s reload"  > /dev/null 2>&1
