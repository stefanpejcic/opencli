#!/bin/bash
################################################################################
# Script Name: user/login.sh
# Description: Login as a user container.
# Usage: opencli user-login <USERNAME>
# Author: Stefan Pejcic
# Created: 21.10.2023
# Last Modified: 17.12.2024
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

install_fzf() {
    if ! command -v fzf &> /dev/null; then
        echo "Attempting to install fzf..."
        apt install -y fzf > /dev/null 2>&1 || dnf install -y fzf
        if ! command -v fzf &> /dev/null; then
            echo "Failed to install fzf. Please install it manually."
            exit 1
        fi
    fi   
}


get_all_users(){
    users=$(mysql -Bse "SELECT username FROM users")
    if [ -z "$users" ]; then
      echo "No users found in the database."
      exit 1
    fi
}


if [ $# -gt 0 ]; then
    selected_user="$1"
    if [ -z "$selected_user" ]; then
        echo "Invalid user."
        exit 1
    fi
else
    install_fzf
    get_all_users
    selected_user=$(echo "$users" | fzf --prompt="Select a user: ")
    if [[ -z "$selected_user" || ! "$users" =~ (^|[[:space:]])"$selected_user"($|[[:space:]]) ]]; then
        echo "Invalid selection or no user selected."
        exit 1
    fi
fi

if [ $(docker ps -q -f name=$selected_user) ]; then
    docker exec -it "$selected_user" /bin/bash
else
    if id "$selected_user" &>/dev/null; then
       #su "$selected_user"      # log as the user on host os
        su "$selected_user" -c "docker exec -it $selected_user /bin/bash" # log as user then in container
       #sudo su -l "$selected_user" -s /bin/bash # log as user with bash but docker env not enabled!
    else
        echo "Neither container nor the user $selected_user exist on the server."
    fi
fi
