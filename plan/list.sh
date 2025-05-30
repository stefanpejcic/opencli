#!/bin/bash
################################################################################
# Script Name: plans.sh
# Description: Display all plans: id, name, description, limits..
# Usage: opencli plan-list [--json]
# Docs: https://docs.openpanel.com
# Author: Stefan Pejcic
# Created: 30.11.2023
# Last Modified: 30.05.2025
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

# Usage function
print_usage() {
    script_name=$(realpath --relative-to=/usr/local/opencli/ "$0")
    script_name="${script_name//\//-}"  # Replace / with -
    script_name="${script_name%.sh}"     # Remove the .sh extension
    echo "Usage: opencli $script_name [--json]"
    exit 1
}

# Command line argument handling
while [[ $# -gt 0 ]]; do
    case $1 in
        --json)
            json_output=true
            shift
            ;;
        *)
            print_usage
            ;;
    esac
done

# DB
source /usr/local/opencli/db.sh

ensure_jq_installed() {
    # Check if jq is installed
    if ! command -v jq &> /dev/null; then
        # Detect the package manager and install jq
        if command -v apt-get &> /dev/null; then
            sudo apt-get update > /dev/null 2>&1
            sudo apt-get install -y -qq jq > /dev/null 2>&1
        elif command -v yum &> /dev/null; then
            sudo yum install -y -q jq > /dev/null 2>&1
        elif command -v dnf &> /dev/null; then
            sudo dnf install -y -q jq > /dev/null 2>&1
        else
            echo "Error: No compatible package manager found. Please install jq manually and try again."
            exit 1
        fi

        # Check if installation was successful
        if ! command -v jq &> /dev/null; then
            echo "Error: jq installation failed. Please install jq manually and try again."
            exit 1
        fi
    fi
}

# Fetch all plan data from the plans table
if [ "$json_output" ]; then
    # For JSON output without --table option
    ensure_jq_installed
    plans_data=$(mysql --defaults-extra-file=$config_file -D $mysql_database -e "SELECT * FROM plans;" | tail -n +2)
    json_output=$(echo "$plans_data" | jq -R 'split("\n") | map(split("\t") | {id: .[0], name: .[1], description: .[2], email_limit: .[3], ftp_limit: .[4], domains_limit: .[5], websites_limit: .[6], disk_limit: .[7], inodes_limit: .[8], db_limit: .[9], cpu: .[10], ram: .[11], bandwidth: .[12]})')
    echo "Plans:"
    echo "$json_output"
else
    # For Terminal output with --table option
    plans_data=$(mysql --defaults-extra-file=$config_file -D $mysql_database --table -e "SELECT * FROM plans;")
    # Check if any data is retrieved
    if [ -n "$plans_data" ]; then
        # Display data in tabular format
        echo "$plans_data"
    else
        echo "No plans."
    fi
fi
