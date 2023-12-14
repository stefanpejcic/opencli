#!/bin/bash
################################################################################
# Script Name: domain/stats.sh
# Description: Collects all stats for all domains
# Usage: opencli domain-stats
# Author: Radovan Jecmenica
# Created: 14.12.2023
# Last Modified: 14.12.2023
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


usernames=$(opencli user-list --json | grep -v 'SUSPENDED' | awk -F'"' '/username/ {print $4}')

# Iterate through each username
for username in $usernames; do
    echo "Processing user: $username"
    
    # Get the domains for the current user
    domains=$(opencli domains-user "$username")

    # Check if the result contains "No domains found for user '$username'"
    if [[ "$domains" == *"No domains found for user '$username'"* ]]; then
        echo "No domains found for user $username. Skipping."
    else
        # Iterate through each domain and run goaccess command
        for domain in $domains; do
            log_file="/var/log/nginx/domlogs/${domain}.log"
            output_dir="/home/${username}/stats"
            html_output="${output_dir}/${domain}.html"

            # Ensure the output directory exists
            mkdir -p "$output_dir"

            # Run goaccess command
            goaccess "$log_file" -a -o "$html_output"

            echo "Processed domain $domain for user $username"
        done
    fi
done
