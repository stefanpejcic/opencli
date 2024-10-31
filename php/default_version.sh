#!/bin/bash
################################################################################
# Script Name: php/default_php_version.sh
# Description: View or change the default PHP version used for new domains added by user.
# Usage: opencli php-default_version <username>
#        opencli php-default_version <username> --update <new_php_version>
# Author: Stefan Pejcic
# Created: 07.10.2023
# Last Modified: 07.10.2024
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

# Check if username argument is provided
if [ $# -lt 1 ]; then
    echo "Usage: $0 <username> [--update <new_php_version>]"
    exit 1
fi

username="$1"
config_file="/etc/openpanel/openpanel/core/users/$username/server_config.yml"




# Function to update PHP version in the configuration file
update_php_version() {
    local new_php_version="$1"
    local config_file="$2"

    # Use sed to update the PHP version in the configuration file
    sed -i "s/\(default_php_version:\s*\)php[0-9.]\+/\\1$new_php_version/" "$config_file"

    # set the php version to be used on terminal!
    docker exec $username bash -c "update-alternatives --set php /usr/bin/php$new_php_version"
    
}

# Function to validate the PHP version format
validate_php_version() {
    local php_version="$1"
    if [[ ! "$php_version" =~ ^[0-9]\.[0-9]$ ]]; then
        echo "Invalid PHP version format. Please use the format 'number.number' (e.g., 8.1 or 5.6)."
        exit 1
    fi
}



# Check if the configuration file exists
if [ ! -e "$config_file" ]; then
    echo "Configuration file for user '$username' not found."
    exit 1
fi

if [ "$2" == "--update" ]; then
    # Check if a new PHP version is provided
    if [ -z "$3" ]; then
        echo "Usage: $0 <username> --update <new_php_version>"
        exit 1
    fi

    new_php_version="$3"
    validate_php_version "$new_php_version"
    update_php_version "$new_php_version" "$config_file"
    echo "Default PHP version for user '$username' updated to: $new_php_version"
else
    # Use awk to extract the PHP version from the YAML file
    php_version=$(awk '/default_php_version/ {print $2}' "$config_file")

    if [ -n "$php_version" ]; then
        echo "Default PHP version for user '$username' is: $php_version"
    else
        echo "Default PHP version for user: '$username' not found in the configuration file."
        exit 1
    fi
fi
