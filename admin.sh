#!/bin/bash
################################################################################
# Script Name: admin.sh
# Description: Manage OpenAdmin service and admin configuration.
# Usage: opencli admin <setting_name> 
# Author: Stefan Pejcic
# Created: 01.11.2023
# Last Modified: 05.09.2024
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
service_name="admin"
admin_logs_file="/var/log/openpanel/admin/error.log"
#logins_file_path="/usr/local/admin/config.py"
db_file_path="/etc/openpanel/openadmin/users.db"
GREEN='\033[0;32m'
RED='\033[0;31m'
RESET='\033[0m'



# IP SERVERS
SCRIPT_PATH="/usr/local/admin/core/scripts/ip_servers.sh"
if [ -f "$SCRIPT_PATH" ]; then
    source "$SCRIPT_PATH"
else
    IP_SERVER_1=IP_SERVER_2=IP_SERVER_3="https://ip.openpanel.com"
fi



# Display usage information
usage() {
    echo "Usage: opencli admin <command> [options]"
    echo ""
    echo "Commands:"
    echo "  on                                            Enable and start the OpenAdmin service."
    echo "  off                                           Stop and disable the OpenAdmin service."
    echo "  log                                           Display the last 25 lines of the OpenAdmin error log."
    echo "  list                                          List all current admin users."
    echo "  new <user> <pass>                             Add a new user with the specified username and password."
    echo "  password <user> <pass>                        Reset the password for the specified admin user."
    echo "  rename <old> <new>                            Change the admin username."
    echo "  suspend <user>                                Suspend admin user."
    echo "  unsuspend <user>                              Unsuspend admin user."
    echo "  notifications <command> <param> [value]       Control notification preferences."
    echo ""
    echo "  Notifications Commands:"
    echo "    check                                       Check and write notifications."
    echo "    get <param>                                 Get the value of the specified notification parameter."
    echo "    update <param> <value>                      Update the specified notification parameter with the new value."
    echo ""
    echo "Examples:"
    echo "  opencli admin on"
    echo "  opencli admin off"
    echo "  opencli admin log"
    echo "  opencli admin list"
    echo "  opencli admin new stefan SuperStrong1"
    echo "  opencli admin password stefan SuperStrong2"
    echo "  opencli admin rename stefan pejcic"
    echo "  opencli admin suspend pejcic"
    echo "  opencli admin unsuspend pejcic"
    echo "  opencli admin notifications check"
    echo "  opencli admin notifications get ssl"
    echo "  opencli admin notifications update ssl true"
    exit 1
}


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
    ip=$(curl --silent --max-time 2 -4 $IP_SERVER_1 || wget --timeout=2 -qO- $IP_SERVER_2 || curl --silent --max-time 2 -4 $IP_SERVER_3)
        
    # Check if IP is empty or not a valid IPv4
    if [ -z "$ip" ] || ! [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        ip=$(hostname -I | awk '{print $1}')
    fi
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
    echo -e "${GREEN}●${RESET} OpenAdmin is running and is available on: $admin_url"
else
     echo -e "${RED}×${RESET} OpenAdmin is not running. To enable it run 'opencli admin on' "
fi
}


add_new_user() {
    local username="$1"
    local password="$2"
    local password_hash=$(python3 /usr/local/admin/core/users/hash $password) 
    local user_exists=$(sqlite3 "$db_file_path" "SELECT COUNT(*) FROM user WHERE username='$username';")

    if [ "$user_exists" -gt 0 ]; then
        echo -e "${RED}Error${RESET}: Username '$username' already exists."
    else
    # Define the SQL commands
    create_table_sql="CREATE TABLE IF NOT EXISTS user (id INTEGER PRIMARY KEY, username TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'user', is_active BOOLEAN DEFAULT 1 NOT NULL);"
    insert_user_sql="INSERT INTO user (username, password_hash) VALUES ('$username', '$password_hash');"

    # Execute the SQL commands
    output=$(sqlite3 "$db_file_path" "$create_table_sql" "$insert_user_sql" 2>&1)
        if [ $? -ne 0 ]; then
            # Output the error and the exact SQL command that failed
            echo "User not created: $output"
            # TODO: on debug only! echo "Failed SQL Command: $insert_user_sql"
        else
            echo "User '$username' created."
        fi
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
            sqlite3 $db_file_path "UPDATE user SET username='$new_username' WHERE username='$old_username';"
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
    local password_hash=$(python3 /usr/local/admin/core/users/hash $new_password) 

    if [ "$user_exists" -gt 0 ]; then
        sqlite3 $db_file_path "UPDATE user SET password_hash='$password_hash' WHERE username='$username';"        
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

suspend_user() {
    local username="$1"
    local user_exists=$(sqlite3 "$db_file_path" "SELECT COUNT(*) FROM user WHERE username='$username';")
    local is_admin=$(sqlite3 "$db_file_path" "SELECT COUNT(*) FROM user WHERE username='$username' AND role='admin';")

    if [ "$user_exists" -gt 0 ]; then
        if [ "$is_admin" -gt 0 ]; then
            echo -e "${RED}Error${RESET}: Cannot suspend user '$username' with 'admin' role."
        else
            sqlite3 $db_file_path "UPDATE user SET is_active='0' WHERE username='$username';"
            echo "User '$username' suspended successfully."
        fi
    else
        echo -e "${RED}Error${RESET}: User '$username' does not exist."
    fi

}

unsuspend_user() {
    local username="$1"
    local user_exists=$(sqlite3 "$db_file_path" "SELECT COUNT(*) FROM user WHERE username='$username';")

    if [ "$user_exists" -gt 0 ]; then
            sqlite3 $db_file_path "UPDATE user SET is_active='1' WHERE username='$username';"
            echo "User '$username' unsuspended successfully."
    else
        echo -e "${RED}Error${RESET}: User '$username' does not exist."
    fi
}

delete_existing_users() {
    local username="$1"
    local user_exists=$(sqlite3 "$db_file_path" "SELECT COUNT(*) FROM user WHERE username='$username';")
    local is_admin=$(sqlite3 "$db_file_path" "SELECT COUNT(*) FROM user WHERE username='$username' AND role='admin';")

    if [ "$user_exists" -gt 0 ]; then
        if [ "$is_admin" -gt 0 ]; then
            echo -e "${RED}Error${RESET}: Cannot delete user '$username' with 'admin' role."
        else
            sqlite3 $db_file_path "DELETE FROM user WHERE username='$username';"            
            echo "User '$username' deleted successfully."
        fi
    else
        echo -e "${RED}Error${RESET}: User '$username' does not exist."
    fi
}



config_file="/etc/openpanel/openadmin/config/notifications.ini"

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

# added validation (only letters and numbers) in 0.2.8
validate_password_and_username() {
    local input="$1"
    local field_name="$2"
    
    # Check if input contains only letters and numbers
    if [[ "$input" =~ ^[a-zA-Z0-9_]{5,20}$ ]]; then
        #echo "$field_name is valid."
        :
    else
        echo "ERROR: $field_name is invalid. It must contain only letters and numbers, and be between 5 and 20 characters."
        exit 1
    fi
    
    : '
    # TODO: we will at some point include dictionary checks from lists:
    # https://weakpass.com/wordlist
    # https://github.com/steveklabnik/password-cracker/blob/master/dictionary.txt
    #
    DICTIONARY="dictionary.txt"
    # Convert input to lowercase for dictionary check
    local input_lower=$(echo "$input" | tr '[:upper:]' '[:lower:]')

    # Check if input contains any common dictionary word
    if grep -qi "^$input_lower$" "$DICTIONARY"; then
        echo "ERROR: $field_name is invalid. It contains a common dictionary word, which is not allowed."
        exit 1
    fi
    '
}


case "$1" in
    "on")
        # Enable and check
        echo "Enabling the OpenAdmin..."
        systemctl enable --now $service_name > /dev/null 2>&1
        detect_service_status
        ;;
    "log")
        # tail logs
        echo "OpenAdmin error log:"
        systemctl enable --now $service_name > /dev/null 2>&1
        echo ""
        service admin restart
        tail -25 $admin_logs_file
        echo ""
        ;;
    "off")
        # Disable admin panel service
        echo "Disabling the OpenAdmin..."
        systemctl disable --now $service_name > /dev/null 2>&1
        detect_service_status
        ;;
    "help")
        # Show usage
        usage
        ;;
    "password")
        # Reset password for admin user
        user_flag="$2"
        new_password="$3"
        # Check if the file exists
        if [ -f "$db_file_path" ]; then
            if [ "$new_password" ]; then
                # valdiate password
                validate_password_and_username "$new_password" "Password"
                # Use provided password
                update_password "$user_flag"
            fi
        else
            echo "Error: File $db_file_path does not exist, password not changed for user."
        fi
                
        ;;
    "rename")
        # Change username
        old_username="$2"
        new_username="$3"
        validate_password_and_username "$new_username" "New Username"
        update_username "$old_username" "$new_username"
        ;;
    "list")
        # List users
        list_current_users
        ;;
    "suspend")
        # List users
        username="$2"
        suspend_user "$username"
        ;;   
    "unsuspend")
        # List users
        username="$2"
        unsuspend_user "$username"
        ;;       
    "new")
        # Add a new user
        new_username="$2"
        new_password="$3"
        # Check if $2 and $3 are provided
        if [ -z "$new_username" ] || [ -z "$new_password" ]; then
            #echo "Error: Missing parameters for new admin command."
            echo "ERROR: Invalid command."
            usage
            exit 1
        fi
        validate_password_and_username "$new_username" "Username"
        validate_password_and_username "$new_password" "Password"
        add_new_user "$new_username" "$new_password"
        ;;
    "notifications")
        # COntrol notification preferences
        command="$2"
        param_name="$3"
        # Check if $2 and $3 are provided
        if [ -z "$command" ] || [ -z "$param_name" ]; then
            #echo "Error: Missing parameters for notifications command."
            echo "ERROR: Invalid command."
            usage
            exit 1
        fi

case "$command" in
    check)
        bash  /usr/local/admin/service/notifications.sh
        ;;
    get)
        get_config "$param_name"
        ;;
    update)
        if [ "$#" -ne 4 ]; then
            echo "ERROR: Usage: opencli admin notifications update <parameter_name> <new_value>"
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
        echo "ERROR: Invalid command."
        usage
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
