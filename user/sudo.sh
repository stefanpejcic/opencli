#!/bin/bash
################################################################################
# Script Name: user/add.sh
# Description: Create a new user with the provided plan_id.
# Usage: opencli user-sudo <username> <enable/disable/status>
# Docs: https://docs.openpanel.co/docs/admin/scripts/users#sudo
# Author: Stefan Pejcic
# Created: 1/.05.2024
# Last Modified: 30.05.2024
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


if [ $# -lt 2 ] || [ $# -gt 3 ]; then
    echo "Usage: opencli user-sudo <username> <enable/disable/status>"
    exit 1
fi

username="$1"
action="$2"
entrypoint_path="/etc/entrypoint.sh"

# Check if the container exists
container_id=$(docker ps -q -f name="$username")
if [ -z "$container_id" ]; then
    echo "ERROR: Docker container for username '$username' not found."
    exit 1
fi


if [ "$action" == "enable" ]; then
    docker exec "$container_id" sed -i "s/SUDO=\"[^\"]*\"/SUDO=\"YES\"/" "$entrypoint_path"
    #docker exec "$container_id" usermod -aG sudo -u "$username"
    echo "SUDO enabled for user $username."
elif [ "$action" == "disable" ]; then
    docker exec "$container_id" sed -i "s/SUDO=\"[^\"]*\"/SUDO=\"NO\"/" "$entrypoint_path"
    docker exec "$container_id" sed -i "/^sudo:.*$username/d" /etc/group
    echo "SUDO disabled for user $username."
elif [ "$action" == "status" ]; then
    status=$(docker exec "$container_id" grep -o 'SUDO="[^"]*"' "$entrypoint_path" | cut -d'"' -f2)
    if [ "$status" == "YES" ]; then
        echo "SUDO is enabled."
    elif [ "$status" == "NO" ]; then
        echo "SUDO is disabled."
    else
        echo "Unknown status."
        exit 1
    fi
else
    echo "Invalid action. Please choose 'enable', 'disable', or 'status'."
    exit 1
fi
