#!/bin/bash

# MySQL database configuration
config_file="/usr/local/admin/db.cnf"
mysql_database="panel"

# Function to fetch the owner username of a domain
get_domain_owner() {
    local domain="$1"
    
    # Check if the config file exists
    if [ ! -f "$config_file" ]; then
        echo "Config file $config_file not found."
        exit 1
    fi
    
    # Query to fetch the user_id for the specified domain
    user_id_query="SELECT user_id FROM domains WHERE domain_name = '$domain'"
    
    # Execute the query and fetch the user_id
    user_id=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "$user_id_query" -sN)

    if [ -z "$user_id" ]; then
        echo "Domain '$domain' not found in the database."
    else
        # Query to fetch the username using the retrieved user_id
        username_query="SELECT username FROM users WHERE id = '$user_id'"
        username=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "$username_query" -sN)
        
        if [ -z "$username" ]; then
            echo "User does not exist with that ID '$user_id'."
        else
            echo "Owner of '$domain': $username"
        fi
    fi
}

# Check for the domain argument
if [ $# -ne 1 ]; then
    echo "Usage: $0 <domain_name>"
    exit 1
fi

# Get the domain name from the command line argument
domain_name="$1"

# Call the function to fetch the owner of the domain
get_domain_owner "$domain_name"
