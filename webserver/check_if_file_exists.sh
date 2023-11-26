#!/bin/bash
################################################################################
# Script Name: webserver/check_if_file_exists.sh
# Description: Check if a certain file exists in users home directory.
# Usage: opencli webserver-check_if_file_exists <username> <file_path>
# Author: Stefan Pejcic
# Created: 10.10.2023
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
  echo "Usage: $0 <username> <file_path>"
  exit 1
fi

# Assign provided arguments to variables
USERNAME="$1"
FILE_PATH="$2"

# Construct the full path to the file inside the container
FULL_PATH="/home/$USERNAME/$FILE_PATH"

# Use `docker exec` to check if the file exists inside the container
docker exec "$USERNAME" test -f "$FULL_PATH"
#this checks for both files and folders
#docker exec "$USERNAME" test -e "$FULL_PATH"

# Check the exit code to determine if the file exists or not
if [ "$?" -eq 0 ]; then
  echo "$FULL_PATH exists in the container $USERNAME."
else
  echo "$FULL_PATH does not exist in the container $USERNAME."
fi
