#!/bin/bash
################################################################################
# Script Name: domains/stats.sh
# Description: Parse nginx access logs for users domains and generate static html
# Usage: opencli domains-stats
#        opencli domains-stats --debug
#        opencli domains-stats <USERNAME>
#        opencli domains-stats <USERNAME> --debug
# Author: Radovan Jecmenica
# Created: 14.12.2023
# Last Modified: 11.06.2024
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

DEBUG=false # Default value for DEBUG
SINGLE_USER=false
OPENPANEL_CONF_DIR="/etc/openpanel/goaccess"

# Parse optional flags to enable debug mode when needed!
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        *)
            SINGLE_USER=true
            username="$arg"
            ;;
    esac
done

configure_goaccess() {
    # GoAccess
    tar -xzvf "${OPENPANEL_CONF_DIR}/GeoLite2-City_20231219.tar.gz" -C "${OPENPANEL_CONF_DIR}/" > /dev/null
    mkdir -p /usr/local/share/GeoIP/GeoLite2-City_20231219
    cp -r "${OPENPANEL_CONF_DIR}/GeoLite2-City_20231219/"* /usr/local/share/GeoIP/GeoLite2-City_20231219 
}

# Main function to process logs for each user
process_logs() {
    local username="$1"
    local excluded_ips_file="/usr/local/panel/core/users/$username/domains/excluded_ips_for_goaccess"
    local container_ip=$(docker inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $username)
    local excluded_ips=""

    if [ -f "$excluded_ips_file" ] && [ -s "$excluded_ips_file" ]; then
        excluded_ips=$(<"$excluded_ips_file")
    fi

    local domains=$(opencli domains-user "$username")

    if [[ "$domains" == *"No domains found for user '$username'"* ]]; then
        echo "No domains found for user $username. Skipping."
    else

        for domain in $domains; do
            local log_file="/var/log/nginx/domlogs/${domain}.log"
            local output_dir="/var/log/nginx/stats/${username}/"
            local html_output="${output_dir}/${domain}.html"
            local sed_command="s/Dashboard/$domain/g"
    
            mkdir -p "$output_dir"
    
            cat $log_file | docker run --memory="256m" --cpus="0.5" -v /usr/local/share/GeoIP/GeoLite2-City_20231219/GeoLite2-City.mmdb:/GeoLite2-City.mmdb --rm -i -e LANG=EN allinurl/goaccess -e "$excluded_ips" -e "$container_ip" --ignore-panel=KEYPHRASES -a -o html --log-format COMBINED - > $html_output
    
            sed -i "$sed_command" "$html_output" > /dev/null 2>&1
    
            if [ "$DEBUG" = true ]; then
                echo "Processed domain $domain for user $username with IP exclusions"
            else
                echo "Processed domain $domain for user $username"
            fi
        done
        
    fi
}

configure_goaccess

if [ "$SINGLE_USER" = true ]; then
    process_logs "$username"
else
    usernames=$(opencli user-list --json | grep -v 'SUSPENDED' | awk -F'"' '/username/ {print $4}')
    for username in $usernames; do
        process_logs "$username"
    done
fi
