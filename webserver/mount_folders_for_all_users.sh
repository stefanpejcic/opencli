#!/bin/bash
################################################################################
# Script Name: login.sh
# Description: Login as root user inside a users container.
#              Use: bash /usr/local/admin/scripts/webserver/mount_folders_for_all_users.sh
# Author: Stefan Pejcic
# Created: 01.11.2023
# Last Modified: 01.11.2023
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


# Step 1: List all container names
container_names=$(docker ps -a --format '{{.Names}}')

# Step 2: Loop through container names and check for storage files
for container_name in $container_names; do
    storage_file="/home/storage_file_$container_name"
    
    # Step 3: Check if the storage file exists
    if [ -e "$storage_file" ]; then
        # Step 4: Mount the storage file for the user
        mount -o loop "$storage_file" "/home/$container_name"
        echo "Mounted storage file for user: $container_name"
    else
        echo "Storage file does not exist for user: $container_name"
    fi
done
