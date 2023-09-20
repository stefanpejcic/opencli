#!/bin/bash

# Check if the correct number of command-line arguments is provided
if [ "$#" -ne 1 ] && [ "$#" -ne 2 ]; then
    echo "Usage: $0 <username> [-y]"
    exit 1
fi

# Get username from command-line argument
username="$1"

# Check if the -y flag is provided to skip confirmation
if [ "$#" -eq 2 ] && [ "$2" == "-y" ]; then
    skip_confirmation=true
else
    skip_confirmation=false
fi

# Function to confirm actions with the user
confirm_action() {
    if [ "$skip_confirmation" = true ]; then
        return 0
    fi

    read -r -p "Are you sure you want to proceed with these actions for user '$username'? [Y/n]: " response
    response=${response,,} # Convert to lowercase
    if [[ $response =~ ^(yes|y| ) ]]; then
        return 0
    else
        echo "Operation canceled."
        exit 0
    fi
}

# Function to remove Docker container, mysql volume and all user files
remove_docker_container_and_volume() {
    docker stop "$username"
    docker rm "$username"
    docker volume rm "mysql-$username"
    rm -rf /home/$username
}

# Function to delete user from the database
delete_user_from_database() {
    # MySQL database configuration (same as in your original script)
    config_file="/usr/local/admin/config.json"

    # Check if the config file exists
    if [ ! -f "$config_file" ]; then
        echo "Config file $config_file not found."
        exit 1
    fi

    # Read MySQL login credentials from the JSON configuration file
    mysql_user=$(jq -r .mysql_user "$config_file")
    mysql_password=$(jq -r .mysql_password "$config_file")
    mysql_database=$(jq -r .mysql_database "$config_file")

    # Delete user from the database
    mysql_query="DELETE FROM users WHERE username='$username';"
    
    mysql -u "$mysql_user" -p"$mysql_password" -D "$mysql_database" -e "$mysql_query"

    if [ $? -eq 0 ]; then
        echo "User '$username' deleted from MySQL database successfully."
    else
        echo "Error: User deletion from database failed."
    fi
}


# Function to disable UFW rules for ports containing the username
disable_ports_in_ufw() {
  # Get the line numbers to delete
  line_numbers=$(ufw status numbered | awk -v keyword="$username" '$NF ~ keyword' | awk -F '[][]' '/\[/{print $2}')
                #ufw status numbered | awk '$NF ~ /stefan/' | awk -F '[][]' '/\[/{print "[" $2 "]"}' | sed 's/[][]//g'

  # Loop through each line number and delete the corresponding rule
  for line_number in $line_numbers; do
    ufw delete $line_number
  done
}

# Confirm actions
confirm_action

# Disable ports in UFW, remove Docker container, user data and volume, and delete user from the database
disable_ports_in_ufw

ufw reload

remove_docker_container_and_volume

delete_user_from_database

echo "User $username deleted."
