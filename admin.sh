#!/bin/bash

CONFIG_FILE_PATH='/usr/local/panel/conf/panel.config'
service_name="admin"
logins_file_path="/usr/local/admin/config.py"

GREEN='\033[0;32m'
RED='\033[0;31m'
RESET='\033[0m'



read_config() {
    config=$(awk -F '=' '/\[DEFAULT\]/{flag=1; next} /\[/{flag=0} flag{gsub(/^[ \t]+|[ \t]+$/, "", $1); gsub(/^[ \t]+|[ \t]+$/, "", $2); print $1 "=" $2}' $CONFIG_FILE_PATH)
    echo "$config"
}

get_ssl_status() {
    config=$(read_config)
    ssl_status=$(echo "$config" | grep -i 'ssl' | cut -d'=' -f2)
    [[ "$ssl_status" == "yes" ]] && echo true || echo false
}

get_force_domain() {
    config=$(read_config)
    force_domain=$(echo "$config" | grep -i 'force_domain' | cut -d'=' -f2)

    if [ -z "$force_domain" ]; then
        ip=$(get_public_ip)
        force_domain="$ip"
    fi
    echo "$force_domain"
}

get_public_ip() {
    ip=$(curl -s https://ip.openpanel.co || wget -qO- https://ip.openpanel.co)
    echo "$ip"
}

detect_service_status() {
if systemctl is-active --quiet $service_name; then
    if [ "$(get_ssl_status)" == true ]; then
        hostname=$(get_force_domain)
        admin_url="https://${hostname}:2087/"
    else
        ip=$(get_public_ip)
        admin_url="http://${ip}:2087/"
    fi
    echo -e "${GREEN}●${RESET} AdminPanel is running and is available on: $admin_url"
else
     echo -e "${RED}×${RESET} AdminPanel is not running. To enable it run 'opencli admin on' "
fi
}


add_new_user() {
    local username="$1"
    local password="$2"

    local user_exists=$(grep -c "'$username':" "$logins_file_path")

    if [ "$user_exists" -gt 0 ]; then
        echo -e "${RED}Error${RESET}: Username '$username' already exists."
    else
        # Remove the last line from the file
        sed -i '$d' "$logins_file_path"
    
        # Add the new user to the config file
        echo "        '$username': {'password': '$password', 'roles': ['user']}," >> "$logins_file_path"
    
        # Add the closing bracket '}' on a new line
        echo "}" >> "$logins_file_path"
    
        echo "User '$username' added to $logins_file_path"
        fi
}





# Function to update the password for provided user
update_username() {
    local old_username="$1"
    local new_username="$2"
    local user_exists=$(grep -c "'$new_username':" "$logins_file_path")

    if [ "$user_exists" -gt 0 ]; then
        echo -e "${RED}Error${RESET}: Username '$username' already taken."
    else
        sed -i "s/'$old_username': {/'$new_username': {/; s/: '$old_username'/: '$new_username'/" "$logins_file_path"
        echo "User '$old_username' renamed to '$new_username'."
    fi
}

# Function to update the password for provided user
update_password() {
    local username="$1"
    local user_exists=$(grep -c "'$username':" "$logins_file_path")

    if [ "$user_exists" -gt 0 ]; then
        sed -i "s/\('$username': {'password': '\).*\(', 'roles': \['.*'\]}\)/\1$new_password\2/" "$logins_file_path"
        echo "Password for user '$username' changed."
        echo ""
        printf "=%.0s"  $(seq 1 63)
        echo ""
        detect_service_status
        echo ""
        echo "- username: $username"
        echo "- password: $new_password"
        echo ""
        printf "=%.0s"  $(seq 1 63)
        echo ""
    else
        echo -e "${RED}Error${RESET}: User '$username' not found."
    fi
}



list_current_users() {
users=$(grep -E "^\s+'[^']+': {'password':" "$logins_file_path" | awk -F"'" '{print $2}')
echo "$users"
}


delete_existing_users() {
    local username="$1"
    if grep -q "'$username': {'password':" "$logins_file_path"; then

        if grep -q "'$username': {'password': '.*', 'roles': \['admin'\]}," "$logins_file_path"; then
            echo -e "${RED}Error${RESET}: Cannot delete user '$username' with 'admin' role."
        else
            sed -i "/'$username': {'password'/d" "$logins_file_path"
            echo "User '$username' deleted successfully."
        fi
    else
        echo -e "${RED}Error${RESET}: User '$username' does not exist."
    fi
}




case "$1" in
    "on")
        # Enable admin panel service
        echo "Enabling the AdminPanel..."
        systemctl enable --now $service_name > /dev/null 2>&1
        detect_service_status
        ;;
    "off")
        # Disable admin panel service
        echo "Disabling the AdminPanel..."
        systemctl disable --now $service_name > /dev/null 2>&1
        detect_service_status
        ;;
    "password")
        # Reset password for admin user
        new_password="$2"
        user_flag="$3"

        # Check if the file exists
        if [ -f "$logins_file_path" ]; then
            if [ "$user_flag" ]; then
                # Use provided username
                update_password "$user_flag"
            else
                # Default to 'admin' user
                update_password "admin"
            fi
        else
            echo "Error: File $logins_file_path does not exist, password not changed for user."
        fi
                
        ;;
    "rename")
        # Change username
        old_username="$2"
        new_username="$3"
        update_username "$old_username" "$new_username"
        echo "Changing username from $old_username to $new_username"
        ;;
    "list")
        # List users
        list_current_users
        ;;
    "new")
        # Add a new user
        new_username="$2"
        new_password="$3"
        add_new_user "$new_username" "$new_password"
        ;;
    "delete")
        # Add a new user
        username="$2"
        delete_existing_users "$username"
        ;;
    *)
        # Display current service status
        detect_service_status
        ;;
esac

exit 0
