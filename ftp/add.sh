#!/bin/bash
################################################################################
# Script Name: ftp/add.sh
# Description: Create frp sub-user for openpanel user.
# Usage: opencli ftp-add <NEW_USERNAME> <NEW_PASSWORD> <FOLDER> <OPENPANEL_USERNAME>
# Docs: https://docs.openpanel.co/docs/admin/scripts/ftp#add
# Author: Stefan Pejcic
# Created: 22.05.2024
# Last Modified: 22.05.2024
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

if [ "$#" -lt 4 ] || [ "$#" -gt 5 ]; then
    script_name=$(realpath --relative-to=/usr/local/admin/scripts/ "$0")
    script_name="${script_name//\//-}"  # Replace / with -
    script_name="${script_name%.sh}"     # Remove the .sh extension
    echo "Usage: opencli $script_name <new_username> <new_password> '<directory>' <openpanel_username> [--debug]"
    exit 1
fi

username="${1,,}"
password="$2"
directory="$3"
openpanel_username="$4"
DEBUG=false  # Default value for DEBUG



# Parse optional flags to enable debug mode when needed!
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        *)
            ;;
    esac
done

if [ "$DEBUG" = true ]; then

else

fi



: '
EXAMPLES

user1|password1|/home/user1
user2|password2|/home/user2

user1|password1|/home/user1|1001|1001
user2|password2|/home/user2|1002|1002

user1|password1|/home/user1||1001|users
user2|password2|/home/user2||1002|admins

'



