#!/bin/bash

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
whoowns_output=$(bash /usr/local/admin/scripts/domains/whoowns.sh "$domain")
owner=$(echo "$whoowns_output" | awk -F "Owner of '$domain': " '{print $2}')

if [ -n "$owner" ]; then
    container_name="$owner" # Assuming the container name is the same as the owner's username

    # Determine the web server type for the user
    #echo "Determining the web server type for user: $owner..."
    web_server_info=$(bash /usr/local/admin/scripts/webserver/get_webserver_for_user.sh "$owner")
    web_server_type=$(echo "$web_server_info" | awk '{print $NF}')
    #echo "Web server info: $web_server_info"
    #echo "Web server type: $web_server_type"

    if [ -n "$web_server_type" ]; then
        #echo "Web server type for user $owner: $web_server_type"

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

        #echo "PHP version: $php_version"

        if [ -n "$php_version" ]; then
            echo "Domain '$domain' (owned by user: $owner) uses PHP version: $php_version"

            if [ "$update_flag" == true ]; then
                if [ -n "$new_php_version" ]; then
                    # Use sed to replace the old PHP version with the new one in the configuration file inside the container
                    if [ "$web_server_type" == "nginx" ]; then
                        echo "Updating PHP version in the Nginx configuration file..."
                        docker exec "$container_name" sed -i "s/php[0-9.]\+/php$new_php_version/g" "$nginx_conf_path"
                        # Restart Nginx to apply the changes
                        docker exec "$container_name" service nginx reload    
                    elif [ "$web_server_type" == "apache" ]; then
                        echo "Updating PHP version in the Apache configuration file..."
                        docker exec "$container_name" sed -i "s/php[0-9.]\+/php$new_php_version/g" "$apache_conf_path"
                        # Restart Apache to apply the changes
                        docker exec "$container_name" service apache2 reload    
                    fi
                    echo "Updated PHP version in the configuration file to $new_php_version"
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
