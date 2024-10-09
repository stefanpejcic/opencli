#!/bin/bash
################################################################################
# Script Name: user/password.sh
# Description: Reset password for a user.
# Usage: opencli user-password <USERNAME> <NEW_PASSWORD | RANDOM> [--ssh]
# Docs: https://docs.openpanel.co/docs/admin/scripts/users#change-password
# Author: Stefan Pejcic
# Created: 30.11.2023
# Last Modified: 30.11.2023
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

# Function to generate a random password
generate_random_password() {
    tr -dc 'a-zA-Z0-9' < /dev/urandom | head -c 12
}

# Function to print usage
print_usage() {
    echo "Usage: $0 <username> <new_password | random> [--ssh] [--debug]"
    exit 1
}

# Check if username and new password are provided as arguments
if [ $# -lt 2 ]; then
    print_usage
fi

# Parse command line options
username=$1
new_password=$2
ssh_flag=false
random_flag=false  # Flag to check if the new password is initially set as "random"
DEBUG=false  # Default value for DEBUG

# Parse optional flags to enable debug mode when needed!
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        --ssh)
            ssh_flag=true
            ;;
    esac
done

# DB
source /usr/local/admin/scripts/db.sh

# Check if new password should be randomly generated
if [ "$new_password" == "random" ]; then
    new_password=$(generate_random_password)
    random_flag=true
fi

# Hash password
hashed_password=$(python3 -c "from werkzeug.security import generate_password_hash; print(generate_password_hash('$new_password'))")

# Insert hashed password into MySQL database
change_user_password="UPDATE users SET password='$hashed_password' WHERE username='$username';"
mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$change_user_password"

if [ $? -eq 0 ]; then
    # Detect the old user_id
    detektuj_id_stari="SELECT id FROM users WHERE username='$username';"
    stari_id=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -Bse "$detektuj_id_stari")

    if [ -z "$stari_id" ]; then
        echo "Error: Unable to find user ID, user $username does not exist or is suspended."
        exit 1
    fi

    # Update the domains table with the old user_id
    zameni_za_sve_domene="UPDATE domains SET user_id=(SELECT MAX(id) + 1 FROM users) WHERE user_id='$stari_id';"
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$zameni_za_sve_domene"

    if [ $? -eq 0 ]; then
        # Invalidate all existing user sessions
        change_user_id="UPDATE users SET id = (SELECT MAX(id) + 1 FROM (SELECT id FROM users) AS subquery) WHERE username='$username';"
        mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$change_user_id"

        if [ $? -eq 0 ]; then
            if [ "$random_flag" = true ]; then
                echo "Successfully changed password for user $username, new generated password is: $new_password"
            else
                echo "Successfully changed password for user $username."
            fi
        else
            echo "Warning: Terminating existing user sessions failed. Run the command manually: mysql -D \"$mysql_database\" -e \"$change_user_id\""
        fi
    else
        echo "Error: Updating domains table failed."
        exit 1
    fi
else
    echo "Error: Data insertion failed."
    exit 1
fi

# Check if --ssh flag is provided
if [ "$ssh_flag" = true ]; then
    # Change the user password in the Docker container
    echo "$username:$new_password" | docker exec -i "$username" chpasswd
    if [ "$random_flag" = true ]; then
        echo "SSH user $username in Docker container now also has password: $new_password"
    else
        echo "SSH user $username password changed."
    fi
fi
