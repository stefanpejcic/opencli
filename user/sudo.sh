#!/bin/bash
################################################################################
# Script Name: user/add.sh
# Description: Create a new user with the provided plan_id.
# Usage: opencli user-sudo <username> <enable|disable|status>
# Docs: https://dev.openpanel.com/cli/users.html#Grant-root
# Author: Stefan Pejcic
# Created: 1.005.2024
# Last Modified: 22.11.2024
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


if [ $# -lt 2 ] || [ $# -gt 3 ]; then
    echo "Usage: opencli user-sudo <username> <enable/disable/status>"
    exit 1
fi

USERNAME="$1"
action="$2"
entrypoint_path="/etc/entrypoint.sh"


get_context_for_user() {
     source /usr/local/admin/scripts/db.sh
        username_query="SELECT server FROM users WHERE username = '$USERNAME'"
        context=$(mysql -D "$mysql_database" -e "$username_query" -sN)
        if [ -z "$context" ]; then
            context=$USERNAME
        fi
}


set_root_user_passwd(){
    USERNAME="$1"
    password_hash=$(docker --context $context exec "$container_id" bash -c "getent shadow $USERNAME | cut -d: -f2")
    if [ -z "$password_hash" ]; then
        echo "ERROR: Failed to retrieve password hash for user $user_1000."
        exit 1
    else
        escaped_password_hash=$(echo "$password_hash" | sed 's/\$/\\\$/g')
        docker --context $context exec "$container_id" bash -c "sed -i 's|^root:[^:]*:|root:$escaped_password_hash:|' /etc/shadow"
        
        if [ $? -eq 0 ]; then
            docker --context $context exec "$container_id" bash -c "sed -i 's/SUDO=\"[^\"]*\"/SUDO=\"YES\"/' \"$entrypoint_path\""
            if [ $? -eq 0 ]; then
                #echo "Successfully set the password for root user to match password of user $USERNAME."
                echo "'su -' access enabled for user $USERNAME."
            else 
                echo "Failed to update root's password to match the user."
            fi
        else
            echo "Failed to update root's password to match the user."
        fi
    fi
}

reset_root_user_password() {
    strong_password=$(openssl rand -base64 32)
    password_hash=$(docker --context $context exec "$container_id" bash -c "echo $strong_password | openssl passwd -1 -stdin")
    docker --context $context exec "$container_id" bash -c 'sed -i "s/^root:[^:]*:/root:$password_hash:/" /etc/shadow'
    if [ $? -eq 0 ]; then
        #echo "User $ Successfully set a strong password for the root user."
        docker --context $context exec "$container_id" sed -i "s/SUDO=\"[^\"]*\"/SUDO=\"NO\"/" "$entrypoint_path"
        docker --context $context exec "$container_id" sed -i "/^sudo:.*$USERNAME/d" /etc/group 
        echo "'su -' access disabled for user $USERNAME."
    else
        echo "ERROR: Failed to update root's password to a strong password."
    fi
}

check_sudo_status(){

        status=$(docker --context $context exec "$container_id" grep -m 1 -o 'SUDO="[^"]*"' "$entrypoint_path" | cut -d'"' -f2)
        if [ "$status" == "YES" ]; then
            echo "'su -' is enabled for user ${USERNAME}."
        elif [ "$status" == "NO" ]; then
            echo "'su -' is disabled for user ${USERNAME}."
        else
            echo "Unknown status."
            exit 1
        fi
}


manage_sudo_access() {
    if [ "$action" == "enable" ]; then
        set_root_user_passwd "$USERNAME"
    elif [ "$action" == "disable" ]; then
        reset_root_user_password
    elif [ "$action" == "status" ]; then
        check_sudo_status
    else
        echo "Invalid action. Please choose 'enable', 'disable', or 'status'."
        exit 1
    fi
}


get_context_for_user
manage_sudo_access
