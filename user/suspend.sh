#!/bin/bash
################################################################################
# Script Name: user/suspend.sh
# Description: Suspend the user account and pause their docker container.
# Usage: opencli user-suspend <USERNAME>
# Author: Stefan Pejcic
# Created: 01.10.2023
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

# Check if the correct number of command-line arguments is provided
if [ "$#" -ne 1 ]; then
    echo "Usage: $0 <username>"
    exit 1
fi

# Get username from command-line argument
username="$1"


#########################################################################
############################### DB LOGIN ################################ 
#########################################################################
    # MySQL database configuration
    config_file="/usr/local/admin/db.cnf"

    # Check if the config file exists
    if [ ! -f "$config_file" ]; then
        echo "Config file $config_file not found."
        exit 1
    fi

    mysql_database="panel"

#########################################################################



# Function to pause (suspend) a user
pause_user() {
    # Pause the Docker container
    docker stop "$username"

    # Add a suspended timestamp prefix to the username in the database
    suspended_username="SUSPENDED_$(date +"%Y%m%d%H%M%S")_$username"

    # Update the username in the database with the suspended prefix
    mysql_query="UPDATE users SET username='$suspended_username' WHERE username='$username';"
    
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$mysql_query"

    if [ $? -eq 0 ]; then
        echo "User '$username' paused (suspended) successfully."
    else
        echo "Error: User pause (suspend) failed."
    fi
}

# Pause (suspend) the user
pause_user
