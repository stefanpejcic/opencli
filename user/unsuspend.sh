#!/bin/bash
################################################################################
# Script Name: user/unsuspend.sh
# Description: Activate the currently suspended user account.
# Usage: opencli user-unsuspend <USERNAME>
# Author: Stefan Pejcic
# Created: 01.10.2023
# Last Modified: 01.01.2023
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

if [ "$#" -ne 1 ]; then
    echo "Usage: $0 <username>"
    exit 1
fi

# Get username from command-line argument
username="$1"

# DB
source /usr/local/admin/scripts/db.sh

# Function to unpause (unsuspend) a user
unpause_user() {
    # Query the database to get the suspended username
    suspended_username=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -s -N -e "SELECT username FROM users WHERE username LIKE 'SUSPENDED\_%$username';")

    if [ -n "$suspended_username" ]; then
        # Remove the suspended timestamp prefix from the username
        unsuspended_username=$(echo "$suspended_username" | sed 's/^SUSPENDED_[0-9]\{14\}_//')

        # Start the Docker container
        docker start "$unsuspended_username"

        # Update the username in the database without the suspended prefix
        mysql_query="UPDATE users SET username='$unsuspended_username' WHERE username='$suspended_username';"

        mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$mysql_query"

        if [ $? -eq 0 ]; then
            echo "User '$username' unsuspended successfully."
        else
            echo "Error: User unpause (unsuspend) failed."
        fi
    else
        echo "Error: User '$username' not found or not suspended in the database."
    fi
}

# Unpause (unsuspend) the user
unpause_user
