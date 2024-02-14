################################################################################
# Script Name: user_list.py
# Description: Display all users: id, username, email, plan, registered date.
# Usage: opencli user-list [--json]
# Docs: https://docs.openpanel.co/docs/admin/scripts/users#list-users
# Author: Stefan Pejcic
# Created: 16.10.2023
# Last Modified: 14.02.2024
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

# imports
import subprocess
import json
import os
import configparser


# Print usage information
def print_usage():
    print(f"Usage: opencli user-list [--json]")
    exit(1)

# Check command line arguments for --json flag
json_output = False
for arg in os.sys.argv[1:]:
    if arg == "--json":
        json_output = True
    else:
        print_usage()

# DB
config_file = "/usr/local/admin/db.cnf"
config = configparser.ConfigParser()
config.read(config_file)
mysql_database = config.get('database', 'name', fallback='panel')

# Fetch all user data from the users table
if json_output:
    # For JSON output without --table option
    command = f"mysql --defaults-extra-file={config_file} -D {mysql_database} -e \"SELECT users.id, users.username, users.email, plans.name AS plan_name, users.registered_date FROM users INNER JOIN plans ON users.plan_id = plans.id;\" | tail -n +2"
    users_data = subprocess.run(command, shell=True, capture_output=True, text=True).stdout.strip()
    users_list = [line.split('\t') for line in users_data.split('\n')]
    json_output = json.dumps(
        [{'id': user[0], 'username': user[1], 'email': user[2], 'plan_name': user[3], 'registered_date': user[4]} for user in users_list])
    print(json_output)
else:
    # For Terminal output with --table option
    command = f"mysql --defaults-extra-file={config_file} -D {mysql_database} --table -e \"SELECT users.id, users.username, users.email, plans.name AS plan_name, users.registered_date FROM users INNER JOIN plans ON users.plan_id = plans.id;\""
    users_data = subprocess.run(command, shell=True, capture_output=True, text=True).stdout.strip()
    if users_data:
        print(users_data)
    else:
        print("No users.")
