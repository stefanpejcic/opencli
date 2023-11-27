#!/bin/bash
################################################################################
# Script Name: ssl/domain.sh
# Description: Create a new user with the provided plan_id.
# Usage: opencli ssl-domain [-d] <domain_url>
# Author: Radovan Ječmenica
# Created: 27.11.2023
# Last Modified: 27.11.2023
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

#!/bin/bash

# Function to print usage information
print_usage() {
    echo "Usage: $0 [-d] <domain_url>"
    echo "Options:"
    echo "  -d     Delete SSL for the specified domain"
    echo "  <domain_url>   Domain URL for SSL operations"
}

# Function to generate SSL
generate_ssl() {
    domain_url=$1

    echo "Generating SSL for domain: $domain_url"
    
    # Certbot command for SSL generation
    certbot_command=("python3" "/usr/bin/certbot" "certonly" "--nginx" "--non-interactive" "--agree-tos" "-m" "webmaster@$domain_url" "-d" "$domain_url")

    # Run Certbot command
    "${certbot_command[@]}"
    echo "SSL generation completed successfully"
}

# Function to modify Nginx configuration
modify_nginx_conf() {
    domain_url=$1

    # Nginx configuration path
    nginx_conf_path="/etc/nginx/sites-available/$domain_url.conf"

    echo "Modifying Nginx configuration for domain: $domain_url"

    # Nginx configuration content to be added
    nginx_config_content="
    if (\$scheme != \"https\"){
        #return 301 https://\$host\$request_uri;
    } #forceHTTPS

    listen 185.119.90.240:443 ssl http2;
    server_name $domain_url;
    ssl_certificate /etc/letsencrypt/live/$domain_url/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$domain_url/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;
    "

    # Use awk to append content after the specified line
    awk -v content="$nginx_config_content" '/location \// {print; print content; next}1' "$nginx_conf_path" > temp_conf_file
    mv temp_conf_file "$nginx_conf_path"

    echo "Nginx configuration modification completed successfully"
}


# Function to delete SSL
delete_ssl() {
    domain_url=$1

    echo "Deleting SSL for domain: $domain_url"

    # Certbot delete command
    delete_command=("python3" "/usr/bin/certbot" "delete" "--cert-name" "$domain_url" "--non-interactive")

    # Run Certbot delete command
    "${delete_command[@]}"

    # Add your logic for modifying Nginx configuration here
    # ...

    echo "SSL deletion completed successfully"
}
# Function to revert Nginx configuration
revert_nginx_conf() {
    domain_url=$1

    # Nginx configuration path
    nginx_conf_path="/etc/nginx/sites-available/$domain_url.conf"

    echo "Reverting Nginx configuration for domain: $domain_url"

    # Use sed to remove the added content from the Nginx configuration file
    sed -i '/if (\$scheme != "https"){/,/ssl_dhparam \/etc\/letsencrypt\/ssl-dhparams.pem;/d' "$nginx_conf_path"

    echo "Nginx configuration reversion completed successfully"
}

# Main script

# Check the number of arguments
if [ "$#" -lt 1 ]; then
    print_usage
    exit 1
fi

# Parse options
while getopts ":d" opt; do
    case $opt in
        d)
            delete_flag=true
            ;;
        \?)
            echo "Invalid option: -$OPTARG"
            print_usage
            exit 1
            ;;
    esac
done

# Remove options from the argument list
shift "$((OPTIND-1))"

# Get the domain URL
domain_url=$1

# Check if domain URL is provided
if [ -z "$domain_url" ]; then
    echo "Error: Domain URL is missing"
    print_usage
    exit 1
fi

# Perform actions based on options
if [ "$delete_flag" = true ]; then
    delete_ssl "$domain_url"
    revert_nginx_conf "$domain_url"
else
    generate_ssl "$domain_url"
    modify_nginx_conf "$domain_url"
fi
