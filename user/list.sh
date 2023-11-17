#!/bin/bash

# Function to print usage
print_usage() {
    echo "Usage: $0 [--json]"
    exit 1
}

# Parse command-line options
while [[ $# -gt 0 ]]; do
    case $1 in
        --json)
            json_output=true
            shift
            ;;
        *)
            print_usage
            ;;
    esac
done

# MySQL database configuration
config_file="/usr/local/admin/db.cnf"
mysql_database="panel"

# Check if the config file exists
if [ ! -f "$config_file" ]; then
    echo "Config file $config_file not found."
    exit 1
fi


# Fetch all user data from the users table
if [ "$json_output" ]; then
    # For JSON output without --table option
    users_data=$(mysql --defaults-extra-file=$config_file -D $mysql_database -e "SELECT users.id, users.username, users.email, users.registered_date, plans.name AS plan_name FROM users INNER JOIN plans ON users.plan_id = plans.id;" | tail -n +2)
    json_output=$(echo "$users_data" | jq -R 'split("\n") | map(split("\t") | {id: .[0], username: .[1], email: .[2], registered_date: .[3], plan_name: .[4]})' )
    echo "Users:"
    echo "$json_output"
else
    # For Terminal output with --table option
    users_data=$(mysql --defaults-extra-file=$config_file -D $mysql_database --table -e "SELECT users.id, users.username, users.email, users.registered_date, plans.name AS plan_name FROM users INNER JOIN plans ON users.plan_id = plans.id;")
    # Check if any data is retrieved
    if [ -n "$users_data" ]; then
        # Display data in tabular format
        echo "$users_data"
    else
        echo "No users."
    fi
fi

