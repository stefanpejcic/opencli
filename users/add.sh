#!/bin/bash

# Check if the correct number of command-line arguments is provided
if [ "$#" -ne 4 ]; then
    echo "Usage: $0 <username> <password|generate> <email> <plan>"
    exit 1
fi

# Get data from command-line arguments
username="$1"
password="$2"
email="$3"
plan="$4"


# List of usernames that are not allowed
forbidden_usernames=("test" "root" "admin" "other_username")

# Function to check if a username is in the forbidden list
is_username_forbidden() {
    local check_username="$1"
    for forbidden_username in "${forbidden_usernames[@]}"; do
        if [ "$check_username" == "$forbidden_username" ]; then
            return 0 # Username is forbidden
        fi
    done
    return 1 # Username is not forbidden
}


# Check if the username is forbidden
if is_username_forbidden "$username"; then
    echo "Error: Username is not allowed."
    exit 1
fi





#1. Run docker container for user

docker volume create mysql-$username

docker run xxxxxxxxxxxx with volume





#2. Insert data to database

# MySQL database configuration
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


# Generate a random password if the second argument is "generate"
if [ "$password" == "generate" ]; then
    password=$(openssl rand -base64 12)
fi

# Insert data into MySQL database
mysql_query="INSERT INTO users (username, password, email, plan_id) VALUES ('$username', '$password', '$email', '$plan');"

mysql -u "$mysql_user" -p"$mysql_password" -D "$mysql_database" -e "$mysql_query"

if [ $? -eq 0 ]; then
    echo "Data added to MySQL database successfully."
else
    echo "Error: Data insertion failed."
fi




#3. Open ports on firewall

# Function to extract the host port from 'docker port' output
extract_host_port() {
    local port_number="$1"
    local host_port
    host_port=$(docker port "$username" | grep "${port_number}/tcp" | awk -F: '{print $2}' | awk '{print $1}')
    echo "$host_port"
}

# Define the list of container ports to check and open
container_ports=("21" "22" "80" "3306" "8080")

# Variable to track whether any ports were opened
ports_opened=0

# Loop through the container_ports array and open the ports in CSF if not already open
for port in "${container_ports[@]}"; do
    host_port=$(extract_host_port "$port")

    if [ -n "$host_port" ]; then
        # Open the port in CSF
        echo "Opening port ${host_port} for port ${port} in CSF"
        csf -a "0.0.0.0" "${host_port}" "TCP" "Allow incoming traffic for port ${host_port}"
        ports_opened=1
    else
        echo "Port ${port} not found in container ${container_name}"
    fi
done

# Restart CSF if ports were opened
if [ $ports_opened -eq 1 ]; then
    echo "Restarting CSF"
    csf -r
fi

echo "Script completed"
