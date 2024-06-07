#!/bin/bash
################################################################################
# Script Name: license.sh
# Description: Manage OpenPanel Enterprise license.
# Usage: opencli license verify 
# Author: Stefan Pejcic
# Created: 01.11.2023
# Last Modified: 08.06.2024
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


CONFIG_FILE_PATH='/usr/local/panel/conf/panel.config'


GREEN='\033[0;32m'
RED='\033[0;31m'
RESET='\033[0m'


# Display usage information
usage() {
    echo "Usage: opencli license [options]"
    echo ""
    echo "Commands:"
    echo "  key                                           View current license key."
    echo "  verify                                        Verify the license key."
    echo "  delete                                        Delete the license key and downgrade OpenPanel to Community edition."
    exit 1
}


# open conf file
read_config() {
    config=$(awk -F '=' '/\[LICENSE\]/{flag=1; next} /\[/{flag=0} flag{gsub(/^[ \t]+|[ \t]+$/, "", $1); gsub(/^[ \t]+|[ \t]+$/, "", $2); print $1 "=" $2}' $CONFIG_FILE_PATH)
    echo "$config"
}

# read key from the main conf file
get_license_key() {
    config=$(read_config)
    license_key=$(echo "$config" | grep -i 'key' | cut -d'=' -f2)

    if [ -z "$license_key" ]; then
        license_key="${RED}No License Key${RESET}"
    fi
    echo -e "${GREEN}$license_key${RESET}"
}



# dummy verification for terminal only to check on WHMCS directly
#
# OpenAdmin uses a different method
#
get_license_key_and_verify_on_my_openpanel() {
    config=$(read_config)
    license_key=$(echo "$config" | grep -i 'key' | cut -d'=' -f2)

    if [ -z "$license_key" ]; then
        echo -e "${RED}No License Key. Please add the key first: opencli config update key XXXXXXXXXX${RESET}"
        exit 1
    else
        ip_address=$(curl -sS https://ip.openpanel.co)  # Get the public IP address
        check_token=$(openssl rand -hex 16)  # Generate a random token
        
        response=$(curl -sS -X POST -d "licensekey=$license_key&ip=$ip_address&check_token=$check_token" https://panel.hostio.rs/modules/servers/licensing/verify.php)
        license_status=$(echo "$response" | grep -oP '(?<=<status>).*?(?=</status>)')
        #echo "curl -sS -X POST -d "licensekey=$license_key&ip=$ip_address&check_token=$check_token" https://panel.hostio.rs/modules/servers/licensing/verify.php"
        #echo $response
        if [ "$license_status" = "Active" ]; then
            echo -e "${GREEN}License is valid${RESET}"
        else
            echo -e "${RED}License is invalid${RESET}"
            exit 1
        fi
    fi
}




case "$1" in
    "key")
        # Display the key
        license_key=$(get_license_key)
        echo $license_key
        ;;
    "verify")
        # check license on whmcs
        get_license_key_and_verify_on_my_openpanel
        ;;
    "delete")
        # remove the key and reload admin
        opencli config update key "" > /dev/null
        service admin restart
        ;;
    "enterprise-"*)
        # Update the license key "enterprise-"
        new_key=$1
        opencli config update key "$new_key" > /dev/null
        ;;
    *)
        echo -e "${RED}Invalid command.${RESET}"
        usage
        exit 1
        ;;
esac



