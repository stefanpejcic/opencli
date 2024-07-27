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


CONFIG_FILE_PATH='/etc/openpanel/openpanel/conf/openpanel.config'
WHMCS_URL="https://my.openpanel.co/modules/servers/licensing/verify.php"
IP_URL="https://ip.openpanel.co"

GREEN='\033[0;32m'
RED='\033[0;31m'
RESET='\033[0m'


# Display usage information
usage() {
    echo "Usage: opencli license [options]"
    echo ""
    echo "Commands:"
    echo "  key                                           View current license key."
    echo "  enterprise-XXXXXXXXXX                         Save the license key."
    echo "  verify                                        Verify the license key."
    echo "  info                                          Display information about the license owner and expiration."
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
        # Check if --json flag is present
        if [[ " $@ " =~ " --json " ]]; then
          license_key="No License Key"
        else
          license_key="${RED}No License Key${RESET}"
        fi
    else
        # Check if --json flag is present
        if [[ " $@ " =~ " --json " ]]; then
          echo "$license_key"
        else
          echo -e "${GREEN}$license_key${RESET}"
        fi
    fi
    
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
        ip_address=$(curl -sS $IP_URL)  # Get the public IP address
        check_token=$(openssl rand -hex 16)  # Generate a random token
        
        response=$(curl -sS -X POST -d "licensekey=$license_key&ip=$ip_address&check_token=$check_token" $WHMCS_URL)
        license_status=$(echo "$response" | grep -oP '(?<=<status>).*?(?=</status>)')
        
        if [ "$license_status" = "Active" ]; then

            # Check if --json flag is present
            if [[ " $@ " =~ " --json " ]]; then
                  echo "License is valid"
            else
                echo -e "${GREEN}License is valid${RESET}"
            fi
            
            service admin restart

        else
            # Check if --json flag is present
            if [[ " $@ " =~ " --json " ]]; then
                  echo "License is invalid"
            else
                echo -e "${RED}License is invalid${RESET}"
            fi
            exit 0
        fi
    fi
}


get_license_key_and_verify_on_my_openpanel_then_show_info() {
    config=$(read_config)
    license_key=$(echo "$config" | grep -i 'key' | cut -d'=' -f2)

    if [ -z "$license_key" ]; then
   
        # Check if --json flag is present
        if [[ " $@ " =~ " --json " ]]; then
          echo "No License Key"
        else
          echo -e "${RED}No License Key. Please add the key first: opencli config update key XXXXXXXXXX${RESET}"
        fi
        exit 0
    else
        ip_address=$(curl -sS $IP_URL)  # Get the public IP address
        check_token=$(openssl rand -hex 16)  # Generate a random token
        
        response=$(curl -sS -X POST -d "licensekey=$license_key&ip=$ip_address&check_token=$check_token" $WHMCS_URL)
        license_status=$(echo "$response" | grep -oP '(?<=<status>).*?(?=</status>)')
        
        if [ "$license_status" = "Active" ]; then
            #echo -e "${GREEN}License is valid${RESET}"
            registered_name=$(echo "$response" | grep -oP '(?<=<registeredname>).*?(?=</registeredname>)')
            company_name=$(echo "$response" | grep -oP '(?<=<companyname>).*?(?=</companyname>)')
            email=$(echo "$response" | grep -oP '(?<=<email>).*?(?=</email>)')
            #service_id=$(echo "$response" | grep -oP '(?<=<serviceid>).*?(?=</serviceid>)')
            #product_id=$(echo "$response" | grep -oP '(?<=<productid>).*?(?=</productid>)')
            product_name=$(echo "$response" | grep -oP '(?<=<productname>).*?(?=</productname>)')
            reg_date=$(echo "$response" | grep -oP '(?<=<regdate>).*?(?=</regdate>)')
            next_due_date=$(echo "$response" | grep -oP '(?<=<nextduedate>).*?(?=</nextduedate>)')
            billing_cycle=$(echo "$response" | grep -oP '(?<=<billingcycle>).*?(?=</billingcycle>)')
            #valid_domain=$(echo "$response" | grep -oP '(?<=<validdomain>).*?(?=</validdomain>)')
            valid_ip=$(echo "$response" | grep -oP '(?<=<validip>).*?(?=</validip>)')
            #valid_directory=$(echo "$response" | grep -oP '(?<=<validdirectory>).*?(?=</validdirectory>)')
            #config_options=$(echo "$response" | grep -oP '(?<=<configoptions>).*?(?=</configoptions>)')
            #custom_fields=$(echo "$response" | grep -oP '(?<=<customfields>).*?(?=</customfields>)')
            #addons=$(echo "$response" | grep -oP '(?<=<addons>).*?(?=</addons>)')
            #md5_hash=$(echo "$response" | grep -oP '(?<=<md5hash>).*?(?=</md5hash>)')



            # Check if --json flag is present
            if [[ " $@ " =~ " --json " ]]; then
                echo '{"Owner": "'"$registered_name"'","Company Name": "'"$company_name"'","Email": "'"$email"'","License Type": "'"$product_name"'","Registration Date": "'"$reg_date"'","Next Due Date": "'"$next_due_date"'","Billing Cycle": "'"$billing_cycle"'","Valid IP": "'"$valid_ip"'"}'

                
            else
                echo "Owner: $registered_name"
                echo "Company Name: $company_name"
                echo "Email: $email"
                #echo "Service ID: $service_id"
                #echo "Product ID: $product_id"
                echo "License Type: $product_name"
                echo "Registration Date: $reg_date"
                echo "Next Due Date: $next_due_date"
                echo "Billing Cycle: $billing_cycle"
                #echo "Valid Domain: $valid_domain"
                echo "Valid IP: $valid_ip"
                #echo "Valid Directory: $valid_directory"
                #echo "Config Options: $config_options"
                #echo "Custom Fields: $custom_fields"
                #echo "Addons: $addons"
                #echo "MD5 Hash: $md5_hash"
            fi


        else


            # Check if --json flag is present
            if [[ " $@ " =~ " --json " ]]; then
              echo "License is invalid"
            else
              echo -e "${RED}License is invalid${RESET}"
            fi
            exit 0
        fi
    fi
}








case "$1" in
    "key")
        # Display the key
        license_key=$(get_license_key "$@")
        echo $license_key
        ;;
    "info")
        # display license info from whmcs
        get_license_key_and_verify_on_my_openpanel_then_show_info "$@"
        ;;
    "verify")
        # check license on whmcs
        get_license_key_and_verify_on_my_openpanel "$@"
        ;;
    "delete")
        # remove the key and reload admin
        opencli config update key "" > /dev/null
        service admin restart
        ;;
    "enterprise"*)
        # Update the license key "enterprise-"
        new_key=$1
        if opencli config update key "$new_key" > /dev/null; then
            # Check if --json flag is present
            if [[ " $@ " =~ " --json " ]]; then
                echo "License key ${new_key} added."
            else
                echo -e "License key ${GREEN}${new_key}${RESET} added."
            fi

            service admin reload  #might fail!

        else
            # Check if --json flag is present
            if [[ " $@ " =~ " --json " ]]; then
                echo "Failed to save the license key ${new_key}"
            else
                echo -e "${RED}Failed to save the license key.${RESET}"
            fi

            
        fi
        ;;
    *)
        echo -e "${RED}Invalid command.${RESET}"
        usage
        exit 1
        ;;
esac



