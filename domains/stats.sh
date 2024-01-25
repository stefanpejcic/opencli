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
# Last Modified: 25.01.2024
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

if [ -z "$username" ]; then
    # If no username provided, process logs for all active users
    usernames=$(opencli user-list --json | grep -v 'SUSPENDED' | awk -F'"' '/username/ {print $4}')
else
    # If username provided, process logs only for that user
    usernames=("$username")
fi


# Iterate through users
for username in $usernames; do

    # Check if the excluded IPs file exists for the current user
    excluded_ips_file="/usr/local/panel/core/users/$username/domains/excluded_ips_for_goaccess"
    # exclude docker container private ip also
    container_ip=$(docker inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $username)
  if [ "$DEBUG" = true ]; then 
    if [ -f "$excluded_ips_file" ] && [ -s "$excluded_ips_file" ]; then
        echo "Excluded IPs file found for user $username, excluding them along with private IP $container_ip"
        excluded_ips=$(cat "$excluded_ips_file")

        # Get the domains for the current user
        domains=$(opencli domains-user "$username")
        
        for domain in $domains; do
            log_file="/var/log/nginx/domlogs/${domain}.log"
            output_dir="/var/log/nginx/stats/${username}/"
            html_output="${output_dir}/${domain}.html"

            # Ensure the output directory exists
            mkdir -p "$output_dir"

            # Run goaccess command with exclusion flags
            goaccess "$log_file" -a -o "$html_output" -e "$excluded_ips" -e "$container_ip" --ignore-panel=KEYPHRASES

            # Replace "Dashboard" with the domain name in the HTML file
            sed -i "s/Dashboard/$domain/g" "$html_output"

            echo "Processed domain $domain for user $username with IP exclusions"
        done
    else
        echo "No excluded IPs file found for user $username. Only private IP $container_ip will be excluded from report."

        # Get the domains for the current user
        domains=$(opencli domains-user "$username")

        # Check if the result contains "No domains found for user '$username'"
        if [[ "$domains" == *"No domains found for user '$username'"* ]]; then
            echo "No domains found for user $username. Skipping."
        else
            # Iterate through each domain and run goaccess command
            for domain in $domains; do
            log_file="/var/log/nginx/domlogs/${domain}.log"
            output_dir="/var/log/nginx/stats/${username}/"
            html_output="${output_dir}/${domain}.html"

            # Ensure the output directory exists
            mkdir -p "$output_dir"

            # Run goaccess command
            goaccess "$log_file" -a -o "$html_output" -e "$container_ip" --ignore-panel=KEYPHRASES

            # Replace "Dashboard" with the domain name in the HTML file
            sed -i "s/Dashboard/$domain/g" "$html_output"

            echo "Processed domain $domain for user $username"
            done
        fi
    fi
  else
    if [ -f "$excluded_ips_file" ] && [ -s "$excluded_ips_file" ]; then
      excluded_ips=$(cat "$excluded_ips_file")

        # Get the domains for the current user
        domains=$(opencli domains-user "$username")
        
        for domain in $domains; do
            log_file="/var/log/nginx/domlogs/${domain}.log"
            output_dir="/var/log/nginx/stats/${username}/"
            html_output="${output_dir}/${domain}.html"

            # Ensure the output directory exists
            mkdir -p "$output_dir"

            # Run goaccess command with exclusion flags
            goaccess "$log_file" -a -o "$html_output" -e "$excluded_ips" -e "$container_ip" --ignore-panel=KEYPHRASES > /dev/null 2>&1

            # Replace "Dashboard" with the domain name in the HTML file
            sed -i "s/Dashboard/$domain/g" "$html_output" > /dev/null 2>&1
        done
    else
        # Get the domains for the current user
        domains=$(opencli domains-user "$username")

        # Check if the result contains "No domains found for user '$username'"
        if [[ "$domains" == *"No domains found for user '$username'"* ]]; then
            continue
        else
            # Iterate through each domain and run goaccess command
            for domain in $domains; do
            log_file="/var/log/nginx/domlogs/${domain}.log"
            output_dir="/var/log/nginx/stats/${username}/"
            html_output="${output_dir}/${domain}.html"

            # Ensure the output directory exists
            mkdir -p "$output_dir"

            # Run goaccess command
            goaccess "$log_file" -a -o "$html_output" -e "$container_ip" --ignore-panel=KEYPHRASES > /dev/null 2>&1

            # Replace "Dashboard" with the domain name in the HTML file
            sed -i "s/Dashboard/$domain/g" "$html_output" > /dev/null 2>&1
            done
        fi
    fi
  fi
done
