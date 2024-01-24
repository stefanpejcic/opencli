#!/bin/bash
################################################################################
# Script Name: docker/is_port_in_use.sh
# Description: Check if certain port is currently used in the users docker container.
# Usage: opencli docker-is_port_in_use
# Author: Stefan Pejcic
# Created: 01.11.2023
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

# Check if the script is run with root/sudo privileges
if [ "$EUID" -ne 0 ]; then
  echo "This script requires superuser privileges to access Docker."
  exit 1
fi

# Check if the correct number of arguments is provided
if [ "$#" -ne 2 ]; then
  echo "Usage: $0 <username> <port>"
  exit 1
fi

# Assign provided arguments to variables
USERNAME="$1"
PORT="$2"




source /usr/local/admin/scripts/db.sh
ports_in_use=$(mysql --defaults-extra-file=$config_file -D $mysql_database -e "SELECT DISTINCT s.site_name, s.ports
FROM sites s
JOIN domains d ON s.domain_id = d.domain_id
JOIN users u ON d.user_id = u.id
WHERE u.username = '$USERNAME' AND s.ports = $PORT;")

# Use `docker exec` to run the lsof command inside the existing container
#docker exec "$USERNAME" apt-get install lsof -qq -y
docker exec  "$USERNAME" bash -c "command -v lsof"
  if [ "$?" -eq 1 ]; then
    docker exec "$USERNAME" apt-get install lsof -qq -y
  fi

docker exec "$USERNAME" lsof -i :"$PORT"


# Check the exit code to determine if the port is in use or not
if [ "$?" -eq 0 ]; then
  echo "Port $PORT is in use in the container $USERNAME."
else
  if echo "$ports_in_use" | grep -q "\<$PORT\>"; then
     echo "Port $PORT is in use in the container $USERNAME."
  else 
    echo "Port $PORT is not in use in the container $USERNAME."
  fi
fi
