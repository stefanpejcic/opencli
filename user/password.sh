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

# Source the database configuration
source /usr/local/admin/scripts/db.sh

# Check if a new password should be randomly generated
random_flag=false
if [ "$new_password" == "random" ]; then
    new_password=$(generate_random_password)
    random_flag=true
fi

# Hash password
hashed_password=$(python3 -c "from werkzeug.security import generate_password_hash; print(generate_password_hash('$new_password'))")

# Detect the old user_id and other required fields
detektuj_stari="SELECT id, email FROM users WHERE username='$username';"
read -r stari_id email < <(mysql --defaults-extra-file=$config_file -D "$mysql_database" -Bse "$detektuj_stari")

if [ -z "$stari_id" ]; then
    echo "Error: Unable to find user ID for username $username."
    exit 1
fi

# Generate new user_id
new_user_id=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -Bse "SELECT MAX(id) + 1 FROM users;")

# Temporarily disable foreign key checks
disable_fk_checks="SET FOREIGN_KEY_CHECKS = 0;"
mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$disable_fk_checks"

# Insert a new user with the new user_id and the hashed password
insert_new_user="INSERT INTO users (id, username, password, email) VALUES ('$new_user_id', '$username', '$hashed_password', '$email');"
mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$insert_new_user"

if [ $? -eq 0 ]; then
    # Update the domains table to reference the new user_id
    update_domains="UPDATE domains SET user_id='$new_user_id' WHERE user_id='$stari_id';"
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$update_domains"

    if [ $? -eq 0 ]; then
        # Delete the old user entry
        delete_old_user="DELETE FROM users WHERE id='$stari_id';"
        mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$delete_old_user"

        if [ $? -eq 0 ]; then
            echo "Successfully changed password and updated user ID for user $username."
            if [ "$random_flag" = true ]; then
                echo "New generated password is: $new_password"
            fi
        else
            echo "Error: Deleting old user entry failed."
            exit 1
        fi
    else
        echo "Error: Updating domains table with new user_id failed."
        exit 1
    fi
else
    echo "Error: Inserting new user entry failed."
    exit 1
fi

# Re-enable foreign key checks
enable_fk_checks="SET FOREIGN_KEY_CHECKS = 1;"
mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$enable_fk_checks"

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
