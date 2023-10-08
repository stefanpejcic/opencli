#!/bin/bash

# Check if domain argument is provided
if [ $# -lt 1 ]; then
    echo "Usage: $0 <domain>"
    exit 1
fi

domain="$1"
#echo -e "Provided domain: $domain\n"

# Determine the owner of the domain
whoowns_output=$(bash /usr/local/admin/scripts/domains/whoowns.sh "$domain")
owner=$(echo "$whoowns_output" | awk -F "Owner of '$domain': " '{print $2}')

if [ -n "$owner" ]; then
    container_name="$owner" # Assuming the container name is the same as the owner's username

    # Determine the web server type for the user
    web_server_info=$(bash /usr/local/admin/scripts/webserver/get_webserver_for_user.sh "$owner")
    web_server_type=$(echo "$web_server_info" | awk '{print $NF}')

    if [ -n "$web_server_type" ]; then
        if [ "$web_server_type" == "nginx" ]; then
            # Use Nginx configuration path
            nginx_conf_path="/etc/nginx/sites-available/$domain.conf"
            php_version=$(docker exec "$container_name" grep -o "php[0-9.]\+" "$nginx_conf_path" | head -n 1)
        elif [ "$web_server_type" == "apache" ]; then
            # Use Apache2 configuration path
            apache_conf_path="/etc/apache2/sites-available/$domain.conf"
            php_version=$(docker exec "$container_name" grep -o "php[0-9.]\+" "$apache_conf_path" | head -n 1)
        else
            echo "Unknown web server type '$web_server_type' for user '$owner'." >&2
            exit 1
        fi

        if [ -n "$php_version" ]; then
            echo "Domain '$domain' (owned by user: $owner) uses PHP version: $php_version"
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
