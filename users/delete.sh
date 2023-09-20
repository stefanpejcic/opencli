#!/bin/bash

# Check if the correct number of command-line arguments is provided
if [ "$#" -ne 1 ] && [ "$#" -ne 2 ]; then
    echo "Usage: $0 <username> [-y]"
    exit 1
fi

# Get username from a command-line argument
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

    read -r -p "This will permanently delete user '$username' and all of its data from the server. Please confirm [Y/n]: " response
    response=${response,,} # Convert to lowercase
    if [[ $response =~ ^(yes|y| ) ]]; then
        return 0
    else
        echo "Operation canceled."
        exit 0
    fi
}

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




# Function to remove Docker container, mysql volume and all user files
remove_docker_container_and_volume() {
    docker stop "$username"
    docker rm "$username"
    docker volume rm "mysql-$username"
    rm -rf /home/$username
}

# Delete all users domains vhosts files from Apache
delete_vhosts_files() {
    # Get the user_id from the 'users' table
    user_id=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "SELECT id FROM users WHERE username='$username';" -N)

    if [ -z "$user_id" ]; then
        echo "Error: User '$username' not found in the database."
        exit 1
    fi

    # Get all domain_names associated with the user_id from the 'domains' table
    domain_names=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "SELECT domain_name FROM domains WHERE user_id='$user_id';" -N)

    # Disable Apache virtual hosts, delete SSL and configuration files for each domain
    for domain_name in $domain_names; do
        # Revoke SSL and delete associated $domain_name-le-ssl.conf file
        certbot revoke -n --cert-name $domain_name
        # Delete the SSLs for that domain
        certbot delete -n --cert-name $domain_name
        # Disable the virtual host file
        sudo a2dissite "$domain_name"
        # Delete the configuration file
        sudo rm -f "/etc/apache2/sites-available/$domain_name.conf"
    done

    # Reload Apache to apply changes
    systemctl reload apache2

    echo "SSL Certificates, Apache Virtual hosts and configuration files for all of user '$username' domains deleted successfully."
}

# Function to delete user from the database
delete_user_from_database() {

    # Step 1: Get the user_id from the 'users' table
    user_id=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "SELECT id FROM users WHERE username='$username';" -N)
    
    if [ -z "$user_id" ]; then
        echo "Error: User '$username' not found in the database."
        exit 1
    fi

    # Step 2: Get all domain_ids associated with the user_id from the 'domains' table
    domain_ids=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "SELECT domain_id FROM domains WHERE user_id='$user_id';" -N)

    # Step 3: Delete rows from the 'sites' table based on the domain_ids
    for domain_id in $domain_ids; do
        mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "DELETE FROM sites WHERE domain_id='$domain_id';"
    done

    # Step 4: Delete rows from the 'domains' table based on the user_id
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "DELETE FROM domains WHERE user_id='$user_id';"

    # Step 5: Delete the user from the 'users' table
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "DELETE FROM users WHERE username='$username';"

    echo "User '$username' and associated data deleted from MySQL database successfully."
}




# Function to disable UFW rules for ports containing the username
disable_ports_in_ufw() {
  # Get the line numbers to delete
  line_numbers=$(ufw status numbered | awk -v keyword="stefan" '$NF ~ keyword' | awk -F '[][]' '/\[/{print $2}')
                #ufw status numbered | awk '$NF ~ /stefan/' | awk -F '[][]' '/\[/{print "[" $2 "]"}' | sed 's/[][]//g'

  # Loop through each line number and delete the corresponding rule
  for line_number in $line_numbers; do
    ufw --force delete $line_number
    echo "Deleted rule #$line_number"
  done
}

# Confirm actions
confirm_action

# Disable ports in UFW, remove Docker container, user data and volume, and delete user from the database
disable_ports_in_ufw

ufw reload

delete_vhosts_files

remove_docker_container_and_volume

delete_user_from_database

echo "User $username deleted."
