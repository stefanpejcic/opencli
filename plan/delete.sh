#!/bin/bash
################################################################################
# Script Name: plan/delete
# Description: Delete hosting plan
# Usage: opencli plan-delete <PLAN_NAME>
# Docs: https://docs.openpanel.co/docs/admin/scripts/users#add-user
# Author: Radovan Jecmenica
# Created: 01.12.2023
# Last Modified: 01.12.2023
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

# Function to print usage instructions
print_usage() {
    script_name=$(basename "$0")
    echo "Usage: $script_name <plan_name>"
    exit 1
}

# Initialize variables
plan_name=""

# Command-line argument processing
if [ "$#" -lt 1 ]; then
    print_usage
fi

plan_name=$1

# Source database configuration
source /usr/local/admin/scripts/db.sh

# Check if there are users on the plan
users_count=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT COUNT(*) FROM users INNER JOIN plans ON users.plan_id = plans.id WHERE plans.name = '$plan_name';" | tail -n +2)

if [ "$users_count" -gt 0 ]; then
    echo "Cannot delete plan '$plan_name' as there are users assigned to it. List of users:"
    
    # List users on the plan
    #users_data=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" --table -e "SELECT users.id, users.username, users.email, plans.name AS plan_name, users.registered_date FROM users INNER JOIN plans ON users.plan_id = plans.id WHERE plans.name = '$plan_name';")
    users_data=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" --table -e "SELECT users.username FROM users INNER JOIN plans ON users.plan_id = plans.id WHERE plans.name = '$plan_name';")

    if [ -n "$users_data" ]; then
        echo "$users_data"
    else
        echo "No users on plan '$plan_name'."
    fi

    exit 1
else
    # Delete the plan data
    mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "DELETE FROM plans WHERE name = '$plan_name';"

    # Delete the Docker network
    docker network rm "$plan_name"

    echo "Docker network '$plan_name' deleted successfully."
    echo "Plan '$plan_name' deleted successfully."
fi
