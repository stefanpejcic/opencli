#!/bin/bash
################################################################################
# Script Name: php/get_available_php_versions.sh
# Description: View or update the list of PHP versions that user can currently install.
# Usage: opencli php-available_versions <username>
#        opencli php-available_versions <username> --show
# Author: Stefan Pejcic
# Created: 07.10.2023
# Last Modified: 11.03.2025
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

# Initialize a flag to determine whether to show file content
show_content=false

# Check for optional flag "--show"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --show)
            show_content=true
            shift
            ;;
        *)
            username="$1"
            shift
            ;;
    esac
done


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

# Check if a username is provided as an argument
if [ -z "$username" ]; then
    echo "Usage: $0 [--show] <username>"
    exit 1
fi

# Define the directory and file path
directory="/etc/openpanel/openpanel/core/users/$username/php"
file_path="$directory/php_available_versions.json"

# Ensure the directory exists
if ! mkdir -p "$directory"; then
    echo "Error: Unable to create directory $directory"
    exit 1
: '
    else
    chown -R  $directory
'
fi

get_available_php_versions() {
# Run the command to fetch PHP versions and store them in a JSON file
if docker --context $context exec "$username" apt-get update > /dev/null 2>&1 && \
    available_versions=$(docker --context $context exec "$username" bash -c "apt-cache search php-fpm | grep -v '^php-fpm' | awk '{print \$1}' | grep -vFf <(dpkg -l | awk '/^ii/ {print \$2}')"
    ); then

    # Format the versions into JSON
    json_data="{ \"available_for_install\": [ $(echo "$available_versions" | sed 's/^/\"/; s/$/\"/' | tr '\n' ',' | sed 's/,$//') ] }"

    # Save JSON data to the specified file
    echo "$json_data" > "$file_path"


    # Display dots while the process is running
    while true; do
        echo -n "."
        sleep 1
        if ! kill -0 "$!" 2>/dev/null; then
            break
        fi
    done
    echo  # Print a newline after the dots

    # Check if the background process was completed successfully
    if wait $!; then
        if [ "$show_content" = true ]; then
            jq -r '.available_for_install[]' "$file_path"
            cat "$file_path"
        else
            echo "PHP versions for user $username have been updated and stored in $file_path."
        fi
    else
        echo "Error: Failed to update PHP versions."
        exit 1
    fi
else
    echo "Error: Failed to run the update command in a Docker container for user $username."
    exit 1
fi
}

get_context_for_user() {

     source /usr/local/opencli/db.sh
     
        username_query="SELECT server FROM users WHERE username = '$username'"
        context=$(mysql -D "$mysql_database" -e "$username_query" -sN)
        if [ -z "$context" ]; then
            context=$username
        fi
}

ensure_jq_installed
get_context_for_user
get_available_php_versions
