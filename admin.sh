#!/bin/bash

CONFIG_FILE_PATH='/usr/local/panel/conf/panel.config'
service_name="admin"
#logins_file_path="/usr/local/admin/config.py"
db_file_path="/usr/local/admin/users.db"
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
    local password_hash=$(python3 /usr/local/admin/core/users/hash.py $password) 
    local user_exists=$(sqlite3 "$db_file_path" "SELECT COUNT(*) FROM user WHERE username='$username';")

    if [ "$user_exists" -gt 0 ]; then
        echo -e "${RED}Error${RESET}: Username '$username' already exists."
    else
        sqlite3 /usr/local/admin/users.db 'CREATE TABLE IF NOT EXISTS user (id INTEGER PRIMARY KEY, username TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT "user", is_active BOOLEAN DEFAULT 1 NOT NULL);' 'INSERT INTO user (username, password_hash) VALUES ("'$username'", "'$password_hash'");'
        
        service admin reload
        
        echo "User '$username' created."
    fi
}






# Function to update the username for provided user
update_username() {
    local old_username="$1"
    local new_username="$2"
    local user_exists=$(sqlite3 "$db_file_path" "SELECT COUNT(*) FROM user WHERE username='$old_username';")
    local new_user_exists=$(sqlite3 "$db_file_path" "SELECT COUNT(*) FROM user WHERE username='$new_username';")

    if [ "$user_exists" -gt 0 ]; then
        if [ "$new_user_exists" -gt 0 ]; then
            echo -e "${RED}Error${RESET}: Username '$new_username' already taken."
        else
            sqlite3 /usr/local/admin/users.db "UPDATE user SET username='$new_username' WHERE username='$old_username';"
            service admin reload
            echo "User '$old_username' renamed to '$new_username'."
        fi
    else
        echo -e "${RED}Error${RESET}: User '$old_username' not found."
    fi
}   

# Function to update the password for provided user
update_password() {
    local username="$1"
    local user_exists=$(sqlite3 "$db_file_path" "SELECT COUNT(*) FROM user WHERE username='$username';")
    local password_hash=$(python3 /usr/local/admin/core/users/hash.py $new_password) 

    if [ "$user_exists" -gt 0 ]; then
        sqlite3 /usr/local/admin/users.db "UPDATE user SET password_hash='$password_hash' WHERE username='$username';"        
        service admin reload
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
users=$(sqlite3 "$db_file_path" "SELECT username, role, is_active FROM user;")
echo "$users"
}


delete_existing_users() {
    local username="$1"
    local user_exists=$(sqlite3 "$db_file_path" "SELECT COUNT(*) FROM user WHERE username='$username';")
    local is_admin=$(sqlite3 "$db_file_path" "SELECT COUNT(*) FROM user WHERE username='$username' AND role='admin';")

    if [ "$user_exists" -gt 0 ]; then
        if [ "$is_admin" -gt 0 ]; then
            echo -e "${RED}Error${RESET}: Cannot delete user '$username' with 'admin' role."
        else
            sqlite3 /usr/local/admin/users.db "DELETE FROM user WHERE username='$username';"            
            service admin reload
            echo "User '$username' deleted successfully."
        fi
    else
        echo -e "${RED}Error${RESET}: User '$username' does not exist."
    fi
}



config_file="/usr/local/admin/service/notifications.ini"

# Function to get the current configuration value for a parameter
get_config() {
    param_name="$1"
    param_value=$(grep "^$param_name=" "$config_file" | cut -d= -f2-)
    
    if [ -n "$param_value" ]; then
        echo "$param_value"
    elif grep -q "^$param_name=" "$config_file"; then
        echo "Parameter $param_name has no value."
    else
        echo "Parameter $param_name does not exist. Docs: https://openpanel.co/docs/admin/scripts/openpanel_config#get"
    fi
}

# Function to update a configuration value
update_config() {
    param_name="$1"
    new_value="$2"

    # Check if the parameter exists in the config file
    if grep -q "^$param_name=" "$config_file"; then
        # Update the parameter with the new value
        sed -i "s/^$param_name=.*/$param_name=$new_value/" "$config_file"
        echo "Updated $param_name to $new_value"
        
    else
        echo "Parameter $param_name not found in the configuration file. Docs: https://openpanel.co/docs/admin/scripts/openpanel_config#update"
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
        user_flag="$2"
        new_password="$3"


        # Check if the file exists
        if [ -f "$db_file_path" ]; then
            if [ "$user_flag" ]; then
                # Use provided username
                update_password "$user_flag"
            else
                # Default to 'admin' user
                update_password "admin"
            fi
        else
            echo "Error: File $db_file_path does not exist, password not changed for user."
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
    "notifications")
        # COntrol notification preferences
        command="$2"
        param_name="$3"


case "$command" in
    get)
        get_config "$param_name"
        ;;
    update)
        if [ "$#" -ne 4 ]; then
            echo "Usage: $0 notifications update <parameter_name> <new_value>"
            exit 1
        fi
        new_value="$4"
        update_config "$param_name" "$new_value"
        
        case "$param_name" in
            ssl)
                update_ssl_config "$new_value"
                ;;
            port)
                update_port_config "$new_value"
                ;;
            openpanel_proxy)
                update_openpanel_proxy_config "$new_value"
                service nginx reload
                ;;
        esac
        ;;
    *)
        echo "Invalid command. Usage: $0 [get|update] <parameter_name> [new_value]"
        exit 1
        ;;
esac




        
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
