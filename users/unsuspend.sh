#!/bin/bash

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



# Function to unpause (unsuspend) a user
unpause_user() {
    # Query the database to get the suspended username
    suspended_username=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -s -N -e "SELECT username FROM users WHERE username LIKE 'SUSPENDED_%stefan';")

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

echo "Script completed"
