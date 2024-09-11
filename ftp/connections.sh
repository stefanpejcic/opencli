#!/bin/bash
################################################################################
# Script Name: ftp/add.sh
# Description: Display all active FTP connection or for particular OpenPanel user.
# Usage: opencli ftp-add <NEW_USERNAME> <NEW_PASSWORD> <FOLDER> <OPENPANEL_USERNAME>
# Docs: https://docs.openpanel.co/docs/admin/scripts/ftp#connections
# Author: Stefan Pejcic
# Created: 11.09.2024
# Last Modified: 11.09.2024
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

if [ "$#" -gt 1 ]; then
    echo "Usage: opencli ftp-connections [openpanel_username]"
    exit 1
fi

# Define the container name
CONTAINER_NAME="openadmin_ftp"

# Check if a parameter is provided
if [ -n "$1" ]; then
    # Execute the command with the parameter
    docker exec "$CONTAINER_NAME" sh -c "ps | grep 'vsftpd:' | grep '$1' | grep -w -v grep"
else
    # Execute the command without the parameter
    docker exec "$CONTAINER_NAME" sh -c 'ps | grep "vsftpd:" | grep -w -v grep'
fi
