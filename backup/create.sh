#!/bin/bash
################################################################################
# Script Name: create.sh
# Description: Generate a full backup for all active users.
# Use: opencli backup-create
# Author: Stefan Pejcic
# Created: 08.10.2023
# Last Modified: 15.11.2023
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

RED="\e[31m"
GREEN="\e[32m"
ENDCOLOR="\e[0m"

# Function to log messages to the user-specific log file
log_user() {
    local user_log_file="/usr/local/panel/core/users/$1/backup.log"
    local log_message="$2"
    # Ensure the log directory exists
    mkdir -p "$(dirname "$user_log_file")"
    # Append the log message with a timestamp
    echo "$(date +"%Y-%m-%d %H:%M:%S") - $log_message" >> "$user_log_file"
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


backup_files() {
# Create the backup directory
mkdir -p "$backup_dir"
tar -czvf "$backup_file" "/home/$container_name"
}

#echo "Creating a backup of user container.."
# Export the Docker container to a tar file
#docker export "$container_name" > "$backup_file"


# Check if the export was successful
#if [ $? -eq 0 ]; then
#  echo "${GREEN}[ ✓ ]${END} Exported $container_name to $backup_file"
#else
#  echo "${RED}ERROR${ENDCOLOR}: exporting $container_name"
#fi

backup_mysql_data() {

mkdir -p "$backup_dir/mysql"

# Get a list of databases with the specified prefix
databases=$(docker exec "$container_name" mysql -u root -e "SHOW DATABASES LIKE '$container_name\_%';" | awk 'NR>1')

# Iterate through the list of databases and export each one
for db in $databases
do
  echo "Exporting database: $db"
  docker exec "$container_name" mysqldump -u root "$db" > "$backup_dir/mysql/$db.sql"
done

echo "All MySQL databases have been exported to '$backup_dir/mysql/'."
}



export_user_data_from_database() {
    user_id=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT id FROM users WHERE username='$container_name';" -N)

    if [ -z "$user_id" ]; then
        echo "${RED}ERROR${ENDCOLOR}: export_user_data_to_sql: User '$container_name' not found in the database."
        exit 1
    fi

    # Create a single SQL dump file
    backup_file="$backup_dir/user_data_dump.sql"
    
    # Use mysqldump to export data from the 'sites', 'domains', and 'users' tables
    mysqldump --defaults-extra-file="$config_file" --no-create-info --no-tablespaces --skip-extended-insert "$mysql_database" users -w "id='$user_id'" >> "$backup_file"
    mysqldump --defaults-extra-file="$config_file" --no-create-info --no-tablespaces --skip-extended-insert --single-transaction "$mysql_database" domains -w "user_id='$user_id'" >> "$backup_file"
    mysqldump --defaults-extra-file="$config_file" --no-create-info --no-tablespaces --skip-extended-insert --single-transaction "$mysql_database" sites -w "domain_id IN (SELECT domain_id FROM domains WHERE user_id='$user_id')" >> "$backup_file"

    echo "${GREEN}[ ✓ ]${END}User '$container_name' data exported to $backup_file successfully."
}


# Function to backup Apache .conf files and SSL certificates for domain names associated with a user
backup_apache_conf_and_ssl() {

    # Step 1: Get the user_id from the 'users' table
    user_id=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT id FROM users WHERE username='$container_name';" -N)
    
    if [ -z "$user_id" ]; then
        echo "${RED}ERROR${ENDCOLOR}: backup_apache_conf_and_ssl: User '$container_name' not found in the database."
        exit 1
    fi
    
    # Get domain names associated with the user_id from the 'domains' table
    local domain_names=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT domain_name FROM domains WHERE user_id='$user_id';" -N)
    echo "Getting Apache configuration for user's domains.."
    # Loop through domain names
    for domain_name in $domain_names; do
        local apache_conf_dir="/etc/apache2/sites-available"
        
        local apache_conf_file="$domain_name.conf"
        
        local backup_apache_conf_dir="$backup_dir/apache_conf"
        
        local certbot_ssl_dir="/etc/letsencrypt/live/$domain_name"
        
        local backup_certbot_ssl_dir="$backup_dir/ssl/$domain_name"

        # Check if the Apache .conf file exists and copy it
        if [ -f "$apache_conf_dir/$apache_conf_file" ]; then
            mkdir -p "$backup_apache_conf_dir"
            cp "$apache_conf_dir/$apache_conf_file" "$backup_apache_conf_dir/$apache_conf_file"
            echo "${GREEN}[ ✓ ]${END} Backed up Apache .conf file for domain '$domain_name' to $backup_apache_conf_dir"
        else
            echo "Apache .conf file for domain '$domain_name' not found."
        fi

        # Check if Certbot SSL certificates exist and copy them
        if [ -d "$certbot_ssl_dir" ]; then
            mkdir -p "$backup_certbot_ssl_dir"
            cp -r "$certbot_ssl_dir"/* "$backup_certbot_ssl_dir/"
            echo "${GREEN}[ ✓ ]${END} Backed up Certbot SSL certificates for domain '$domain_name' to $backup_certbot_ssl_dir"
        else
            echo "Certbot SSL certificates for domain '$domain_name' not found."
        fi
    done
}





# Check if a container name is provided as an argument
if [ -z "$1" ]; then
  # No container name provided, so loop through all running containers
  for container_name in $(docker ps --format '{{.Names}}'); do
    echo "Running backup for user: $container_name"
    timestamp=$(date +"%Y%m%d%H%M%S")
    backup_dir="/backup/$container_name/$timestamp"
    backup_file="/backup/$container_name/$timestamp/files_${container_name}_${timestamp}.tar.gz"
    
    log_user "$container_name" "Scheduled backup job started."
    backup_files "$container_name"
    backup_mysql_data "$container_name"
    export_user_data_from_database "$container_name"
    backup_apache_conf_and_ssl "$container_name"
    log_user "$container_name" "Backup job successfully completed."
  done
else
  # Container name is provided as an argument, backup only that user files..
  container_name="$1"
  timestamp=$(date +"%Y%m%d%H%M%S")
  backup_dir="/backup/$container_name/$timestamp"
  backup_file="/backup/$container_name/$timestamp/files_${container_name}_${timestamp}.tar.gz"
  echo "Running backup for user: $container_name"
  log_user "$container_name" "Backup on demand started."
  backup_files "$container_name"
  backup_mysql_data "$container_name"
  export_user_data_from_database "$container_name"
  backup_apache_conf_and_ssl "$container_name"
  log_user "$container_name" "Backup successfully completed."
fi
