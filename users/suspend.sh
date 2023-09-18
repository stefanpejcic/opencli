#!/bin/bash

# Check if the correct number of command-line arguments is provided
if [ "$#" -ne 1 ]; then
    echo "Usage: $0 <username>"
    exit 1
fi

# Get username from command-line argument
username="$1"

# Function to pause (suspend) a user
pause_user() {
    # Pause the Docker container
    docker stop "$username"

    # Add a suspended timestamp prefix to the username in the database
    suspended_username="SUSPENDED_$(date +"%Y%m%d%H%M%S")_$username"
    
    # MySQL database configuration (same as in your original script)
    config_file="config.json"

    # Check if the config file exists
    if [ ! -f "$config_file" ]; then
        echo "Config file $config_file not found."
        exit 1
    fi

    # Read MySQL login credentials from the JSON configuration file
    mysql_user=$(jq -r .mysql_user "$config_file")
    mysql_password=$(jq -r .mysql_password "$config_file")
    mysql_database=$(jq -r .mysql_database "$config_file")

    # Update the username in the database with the suspended prefix
    mysql_query="UPDATE users SET username='$suspended_username' WHERE username='$username';"
    
    mysql -u "$mysql_user" -p"$mysql_password" -D "$mysql_database" -e "$mysql_query"

    if [ $? -eq 0 ]; then
        echo "User '$username' paused (suspended) successfully with username '$suspended_username' in MySQL database."
    else
        echo "Error: User pause (suspend) failed."
    fi
}

# Pause (suspend) the user
pause_user

echo "Script completed"
