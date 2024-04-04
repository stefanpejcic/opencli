#!/bin/bash
################################################################################
# Script Name: server/stats.sh
# Description: Count Domains, Websites and Users
# Usage: opencli server-stats --total --json --save
# Author: Stefan Pejcic
# Created: 04.04.2024
# Last Modified: 04.04.2024
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


# Counting number of users
user_count=$(opencli user-list --total --json)

# Counting number of domains
domain_count=$(opencli domains-all | awk '{ if (NF == 1) print }' | wc -l)

# Counting number of sites
site_count=$(opencli websites-all |  awk '{ if (NF == 1) print }' | wc -l)

# Flag variables
json_output=false
save_to_file=false

# Loop through command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --json)
            json_output=true
            shift
            ;;
        --save)
            save_to_file=true
            shift
            ;;
        *)
            print_usage
            ;;
    esac
done


if [ "$json_output" = true ]; then
    echo "{"
    echo "  \"users\": $user_count,"
    echo "  \"domains\": $domain_count,"
    echo "  \"websites\": $site_count"
    echo "}"
else
    echo "Users: $user_count"
    echo "Domains: $domain_count"
    echo "Websites: $site_count"
fi

if [ "$save_to_file" = true ]; then
    timestamp=$(date +"%Y-%m-%d %H:%M:%S")
    json="{\"timestamp\": \"$timestamp\", \"users\": $user_count, \"domains\": $domain_count, \"websites\": $site_count}"
    mkdir -p /usr/local/admin/logs/
    echo "$json" >> /usr/local/admin/logs/usage_stats.json
fi
