#!/bin/bash
################################################################################
# Script Name: restore.sh
# Description: Restore a full backup for a single user.
# Use: opencli backup-restore <backup_directory>
# Author: Stefan Pejcic
# Created: 08.10.2023
# Last Modified: 15.11.2023
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

RED="\e[31m"
GREEN="\e[32m"
ENDCOLOR="\e[0m"

# Get the backup directory path from the first argument
backup_dir="$1"

# Check if a backup directory path is provided
if [ -z "$backup_dir" ]; then
  echo "Usage: $0 <backup_directory>"
  exit 1
fi

# Extract the container_name from the backup directory path
container_name=$(basename "$(dirname "$backup_dir")")
backup_file="$backup_dir/docker_${container_name}_$(basename "$backup_dir").tar"
sql_dump_file="$backup_dir/user_data_dump.sql"

#echo $container_name
#echo $backup_file
#echo $sql_dump_file

# Check if the backup files exist
if [ ! -f "$backup_file" ] || [ ! -f "$sql_dump_file" ]; then
  echo "${RED}ERROR${ENDCOLOR}: Backup files not found in the specified directory."
  exit 1
fi

echo "Restoring Docker container $container_name..."
if [ ! "$(docker ps -a -q -f name=${container_name})" ]; then
    docker import "$backup_file" "$container_name-image" || { echo "${RED}ERROR${ENDCOLOR}: Failed to restore Docker container $container_name"; exit 1; }

    # Copy databases
    rsync -avR $backup_dir/mysql/ /home/$container_name/mysql/
    
    # run your container
    $$$$$$$$docker run -d --name <name> my-docker-image
else
  echo "${RED}ERROR${ENDCOLOR}: CONTAINER NAME ALREADY IN USE."
  exit 1
fi






RUN KOMANDA

copy vhosts
copy ssl
a2ensite



insert in db user
domains
sites
