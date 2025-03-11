#!/bin/bash
################################################################################
# Script Name: php/domain.sh
# Description: View or change the PHP version used for a single domain name.
# Usage: opencli php-domain <domain_name>
#        opencli php-domain <domain_name> --update <new_php_version>
# Author: Stefan Pejcic
# Created: 07.10.2023
# Last Modified: 11.03.2025
# Company: openpanel.com
# Copyright (c) openpanel.com
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

# Check if domain argument is provided
if [ $# -lt 1 ]; then
    echo "Usage: $0 <domain> [--update <new_php_version>]"
    exit 1
fi

domain="$1"
update_flag=false
new_php_version=""

# Check for the --update flag and new PHP version argument
if [ "$2" == "--update" ]; then
    if [ -z "$3" ]; then
        echo "Error: --update flag requires a new PHP version in the format number.number."
        exit 1
    fi
    if [[ ! "$3" =~ ^[0-9]\.[0-9]$ ]]; then
        echo "Invalid PHP version format. Please use the format 'number.number' (e.g., 8.1 or 5.6)."
        exit 1
    fi

    update_flag=true
    new_php_version="$3"
fi

#echo "Provided domain: $domain"
if [ "$update_flag" == true ]; then
    echo "Updating PHP version to: $new_php_version"
fi

# Determine the owner of the domain
#echo "Determining the owner of the domain..."
whoowns_output=$(opencli domains-whoowns "$domain")
owner=$(echo "$whoowns_output" | awk -F "Owner of '$domain': " '{print $2}')


get_context_for_user() {
     source /usr/local/opencli/db.sh
        username_query="SELECT server FROM users WHERE username = '$owner'"
        context=$(mysql -D "$mysql_database" -e "$username_query" -sN)
        if [ -z "$context" ]; then
            context=$owner
        fi
}

if [ -n "$owner" ]; then
    container_name="$owner"
    
    web_server_info=$(opencli webserver-get_webserver_for_user "$owner")
    web_server_type=$(echo "$web_server_info" | awk '{print $NF}')

    if [ -n "$web_server_type" ]; then

    get_context_for_user
    
        if [ "$web_server_type" == "nginx" ]; then
            # Use Nginx configuration path
            nginx_conf_path="/etc/nginx/sites-available/$domain.conf"
            php_version=$(docker --context $context exec "$container_name" grep -o "php[0-9.]\+" "$nginx_conf_path" | head -n 1)
        elif [ "$web_server_type" == "apache" ]; then
            # Use Apache2 configuration path
            apache_conf_path="/etc/apache2/sites-available/$domain.conf"
            php_version=$(docker --context $context exec "$container_name" grep -o "php[0-9.]\+" "$apache_conf_path" | head -n 1)
        else
            echo "Unknown web server type '$web_server_type' for user '$owner'." >&2
            exit 1
        fi

        #echo "PHP version: $php_version"

        if [ -n "$php_version" ]; then
            echo "Domain '$domain' (owned by user: $owner) uses PHP version: $php_version"

            if [ "$update_flag" == true ]; then
                if [ -n "$new_php_version" ]; then
                    # Use sed to replace the old PHP version with the new one in the configuration file inside the container
                    if [ "$web_server_type" == "nginx" ]; then
                        echo "Updating PHP version in the Nginx configuration file..."
                        docker --context $context exec "$container_name" sed -i "s/php[0-9.]\+/php$new_php_version/g" "$nginx_conf_path"
                        # Restart Nginx to apply the changes
                        docker --context $context exec "$container_name" service nginx reload    
                    elif [ "$web_server_type" == "apache" ]; then
                        echo "Updating PHP version in the Apache configuration file..."
                        docker --context $context exec "$container_name" sed -i "s/php[0-9.]\+/php$new_php_version/g" "$c"
                        # Restart Apache to apply the changes
                        docker --context $context exec "$container_name" service apache2 reload    
                    fi

                    # 
                    local update_query="UPDATE domains SET php_version = '$new_php_version' WHERE domain_url = '$domain';"
                    mysql -e "$update_query"
                    
                    echo "Updated PHP version in domain VirtualHost file and database to: $new_php_version"
                else
                    echo "Error: --update flag requires a new PHP version in the format number.number."
                    exit 1
                fi
            fi
        else
            echo "Failed to determine the PHP version for the domain '$domain' (owned by user $owner)." >&2
            exit 1
        fi
    else
        echo "Failed to determine the web server type for user '$owner'." >&2
        exit 1
    fi
else
    echo "Failed to determine the owner of the domain '$domain'." >&2
    exit 1
fi
