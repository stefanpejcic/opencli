#!/bin/bash
################################################################################
# Script Name: user/2fa.sh
# Description: Check or disable 2FA for a user.
# Usage: opencli user-2fa <username> [disable]
# Author: Stefan Pejcic
# Created: 16.11.2023
# Last Modified: 17.03.2025
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

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
RESET='\033[0m'

# Check if username is provided as an argument
if [ $# -eq 0 ]; then
    echo "Usage: opencli user-2fa <username> [disable]"
    exit 1
fi


# DB
source /usr/local/opencli/db.sh

# Get the username and optional action (disable)
username=$1
action=$2

# If action is provided, update the twofa value
if [ "$action" == "disable" ]; then
    mysql --defaults-extra-file=$config_file -D $mysql_database -e "UPDATE users SET twofa_enabled='0' WHERE username='$username';"
    echo -e "Two-factor authentication for $username is now ${RED}DISABLED${RESET}."
else
    # Get the twofa value for the provided username
    twofa=$(mysql --defaults-extra-file=$config_file -D $mysql_database -se "SELECT twofa_enabled FROM users WHERE username='$username';")

    # Check the value of twofa and display the status
    if [ "$twofa" == "0" ]; then
        echo -e "Two-factor authentication for $username is ${RED}DISABLED${RESET}."
    elif [ "$twofa" == "1" ]; then
        echo -e "Two-factor authentication for $username is ${GREEN}ENABLED${RESET}."
    else
        echo -e "${RED}Invalid twofa value for $username.${RESET}"
    fi
fi
