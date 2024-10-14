#!/bin/bash
################################################################################
# Script Name: domains/user.sh
# Description: Move domain from one user to another.
# Usage: opencli domains-move <DOMAIN> <USERNAME>
# Author: Stefan Pejcic
# Created: 18.10.2024
# Last Modified: 18.10.2024
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


# DB
source /usr/local/admin/scripts/db.sh


# Ensure the script is run with two arguments: domain and username
if [ $# -ne 2 ]; then
    echo "Usage: opencli domains-move <domain> <username>"
    exit 1
fi

# Accept command line arguments
domain=$1
username=$2

# Query to fetch the user_id for the specified username
username_query="SELECT id FROM users WHERE username = '$username'"

# Execute the query and fetch the user_id
user_id=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "$username_query" -sN)

if [ -z "$user_id" ]; then
    echo "User '$username' not found in the database."
    exit 1
fi


#todo:
# cp vhost file inside contianer, transfer nginx to apache of needed
# replace username in vhost outside
# create dir for new domain
#

# Query to update user_id in the domains table for the specified domain
update_query="UPDATE domains SET user_id = '$user_id' WHERE domain_url = '$domain'"

# Execute the update query
mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "$update_query"

# Check if the update was successful
if [ $? -eq 0 ]; then
    echo "Successfully moved domain '$domain' to user '$username'."
else
    echo "Failed to update user_id for domain '$domain'."
fi
