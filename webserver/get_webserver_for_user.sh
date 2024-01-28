#!/bin/bash
################################################################################
# Script Name: create.sh
# Description: Generate a full backup for all active users.
# Usage: opencli backup-create
#        opencli backup-create username [--debug]
# Author: Stefan Pejcic
# Created: 08.10.2023
# Last Modified: 28.01.2024
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


DEBUG=false # Default value for DEBUG
SINGLE_CONTAINER=false

# Parse optional flags to enable debug mode when needed!
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        *)
            SINGLE_CONTAINER=true
            container_name="$arg"
            ;;
    esac
done

# Function to log messages to the user-specific log file for the user
log_user() {
    local user_log_file="/usr/local/panel/core/users/$1/backup.log"
    local log_message="$2"
    # Ensure the log directory exists
    mkdir -p "$(dirname "$user_log_file")"
    # Append the log message with a timestamp
    echo "$(date +"%Y-%m-%d %H:%M:%S") - $log_message" >> "$user_log_file"
}

# DB
source /usr/local/admin/scripts/db.sh

backup_files() {
# Create the backup directory
mkdir -p "$backup_dir"
tar -czvf "$backup_file" "/home/$container_name"
}


backup_mysql_databases() {

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


backup_mysql_users() {

mkdir -p "$backup_dir/mysql/users/"

# Get a list of MySQL users (excluding root and other system users)
USERS=$(docker exec "$container_name" mysql -u root -Bse "SELECT user FROM mysql.user WHERE user NOT LIKE 'root' AND host='%'")

#docker exec "$container_name" bash -c "mysqldump -u root -e --skip-comments --skip-lock-tables --skip-set-charset --no-create-info mysql user > $backup_dir/mysql/users/mysql_users_and_permissions.sql"

for USER in $USERS
do
    # Generate a filename based on the username
    OUTPUT_FILE="$backup_dir/mysql/users/${USER}.sql"

    # Use mysqldump to export user accounts and their permissions
    docker exec "$container_name" mysqldump -u root -e --skip-comments --skip-lock-tables --skip-set-charset --no-create-info mysql user --where="user='$USER'" > $OUTPUT_FILE

    echo "Exported mysql user '$USER' and their permissions to $OUTPUT_FILE."
done


backup_mysql_conf_file() {

mkdir -p "$backup_dir/mysql/conf/"

docker cp $container_name:/etc/mysql/mysql.conf.d/mysqld.cnf $backup_dir/mysql/conf/

echo "Saved MySQL configuration file /etc/mysql/mysql.conf.d/mysqld.cnf"
}




export_webserver_main_conf_file() {

#get webserver for user
output=$(opencli webserver-get_webserver_for_user)

# Check if the output contains "nginx"
if [[ $output == *nginx* ]]; then
    ws="nginx"
# Check if the output contains "apache"
elif [[ $output == *apache* ]]; then
    ws="apache2"
else
    # Set a default value if neither "nginx" nor "apache" is found
    ws="unknown"
fi

mkdir -p "$backup_dir/$ws/"


docker cp $container_name:/etc/$ws/$ws.conf $backup_dir/$ws/

}




export_entrypoint_file() {
mkdir -p "$backup_dir/docker/"
docker cp $container_name:/etc/entrypoint.sh $backup_dir/docker/
}



users_local_files_in_core_users() {
mkdir -p "$backup_dir/core/"
cp -r /usr/local/panel/core/users/$container_name/ $backup_dir/core/
}

users_local_files_in_stats_users() {
mkdir -p "$backup_dir/stats/"
cp -r /usr/local/panel/core/users/$container_name/ $backup_dir/stats/
}



backup_php_versions_in_container(){

# Run the command and capture the output
default_php_version=$(opencli php-default_php_version $container_name)

# Check if the command was successful
if [ $? -eq 0 ]; then
    mkdir -p "$backup_dir/php/"
    # Save the output to a file
    echo "$default_php_version" > $backup_dir/php/default.txt
    echo "Default PHP version saved for user."
else
    echo "Error running the command, default PHP version for user is not saved."
fi


# Run the command and capture the output
output=$(opencli php-enabled_php_versions $container_name)

# Check if the command was successful
if [ $? -eq 0 ]; then
    mkdir -p "$backup_dir/php/"
    # Save the output to a file
    echo "$output" > $backup_dir/php/php_versions.txt
    echo "PHP versions saved to php_versions.txt"

    version_numbers=$(echo "$output" | grep -oP 'php\d+\.\d+' | sed 's/php//')
    for version in $version_numbers; do
        # Copy php-fpm.conf file
        if [ -f "/etc/php/$version/fpm/php-fpm.conf" ]; then
            cp "/etc/php/$version/fpm/php-fpm.conf" "php-fpm_$version.conf"
            echo "php-fpm.conf for PHP $version copied to php-fpm_$version.conf"
        else
            echo "Error: php-fpm.conf file not found for PHP $version"
        fi
    done

else
    echo "Error running the command, no PHP versions are backed up for the user."
fi
}



export_user_data_from_database() {
    user_id=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT id FROM users WHERE username='$container_name';" -N)

    if [ -z "$user_id" ]; then
        echo "ERROR: export_user_data_to_sql: User '$container_name' not found in the database."
        exit 1
    fi

    # Create a single SQL dump file
    backup_file="$backup_dir/user_data_dump.sql"
    
    # Use mysqldump to export data from the 'sites', 'domains', and 'users' tables
    mysqldump --defaults-extra-file="$config_file" --no-create-info --no-tablespaces --skip-extended-insert "$mysql_database" users -w "id='$user_id'" >> "$backup_file"
    mysqldump --defaults-extra-file="$config_file" --no-create-info --no-tablespaces --skip-extended-insert --single-transaction "$mysql_database" domains -w "user_id='$user_id'" >> "$backup_file"
    mysqldump --defaults-extra-file="$config_file" --no-create-info --no-tablespaces --skip-extended-insert --single-transaction "$mysql_database" sites -w "domain_id IN (SELECT domain_id FROM domains WHERE user_id='$user_id')" >> "$backup_file"

    echo "User '$container_name' data exported to $backup_file successfully."
}


# Function to backup Apache .conf files and SSL certificates for domain names associated with a user
backup_apache_conf_and_ssl() {

    # Step 1: Get the user_id from the 'users' table
    user_id=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT id FROM users WHERE username='$container_name';" -N)
    
    if [ -z "$user_id" ]; then
        echo "ERROR: backup_apache_conf_and_ssl: User '$container_name' not found in the database."
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
        local backup_dns_zones_dir="$backup_dir/dns"
        local zone_file="/etc/bind/zones/$domain_name.zone"

        # Check if the zone file exists and copy it
        if [ -f "$zone_file" ]; then
            mkdir -p "$backup_dns_zones_dir"
            cp "$zone_file" "$backup_dns_zones_dir"
            echo "Backed up DNS zone file for domain '$domain_name' to $backup_dns_zones_dir"
        else
            echo "DNS zone file for domain '$domain_name' not found."
        fi

        # Check if the Apache .conf file exists and copy it
        if [ -f "$apache_conf_dir/$apache_conf_file" ]; then
            mkdir -p "$backup_apache_conf_dir"
            cp "$apache_conf_dir/$apache_conf_file" "$backup_apache_conf_dir/$apache_conf_file"
            echo "Backed up Apache .conf file for domain '$domain_name' to $backup_apache_conf_dir"
        else
            echo "Apache .conf file for domain '$domain_name' not found."
        fi

        # Check if Certbot SSL certificates exist and copy them
        if [ -d "$certbot_ssl_dir" ]; then
            mkdir -p "$backup_certbot_ssl_dir"
            cp -r "$certbot_ssl_dir"/* "$backup_certbot_ssl_dir/"
            echo "Backed up Certbot SSL certificates for domain '$domain_name' to $backup_certbot_ssl_dir"
        else
            echo "Certbot SSL certificates for domain '$domain_name' not found."
        fi
    done
}

backup_crontab_for_root_user(){
    file_path="/var/spool/cron/crontabs/root"

    if [ -e "$file_path" ]; then
        mkdir -p "$backup_dir/crons/"
        docker cp $container_name:$file_path $backup_dir/crons/
    else
        echo "Crontab is empty, no cronjobs to backup."
    fi

}

backup_timezone(){
    mkdir -p "$backup_dir/timezone/"
    docker cp $container_name:/etc/timezone $backup_dir/timezone/
    docker cp $container_name:/etc/localtime $backup_dir/timezone/
}

sudo cp /etc/timezone_backup /etc/timezone
sudo cp /etc/localtime_backup /etc/localtime


# Check if a container name is provided as an argument
if [ -z "$container_name" ]; then
    if [ "$DEBUG" = true ]; then
        # No container name provided, so loop through all running containers
        for container_name in $(docker ps --format '{{.Names}}'); do
            echo "Running backup for user: $container_name"
            timestamp=$(date +"%Y%m%d%H%M%S")
            backup_dir="/backup/$container_name/$timestamp"
            backup_file="/backup/$container_name/$timestamp/files_${container_name}_${timestamp}.tar.gz"
            
            log_user "$container_name" "Scheduled backup job started."

            # files
            backup_files "$container_name"

            # configuration
            export_entrypoint_file "$container_name"
            export_webserver_main_conf_file "$container_name"
            backup_mysql_conf_file "$container_name"
            backup_timezone "$container_name"
            backup_php_versions_in_container "$container_name"

            #crons
            backup_crontab_for_root_user "$container_name"
            
            #mysql
            backup_mysql_databases "$container_name"
            backup_mysql_users "$container_name"

            #panel data
            export_user_data_from_database "$container_name"
            users_local_files_in_core_users "$container_name"
            users_local_files_in_stats_users "$container_name"

            #domains
            backup_apache_conf_and_ssl "$container_name"

            log_user "$container_name" "Backup job successfully completed."
        done
    else
        # No container name provided, so loop through all running containers
        for container_name in $(docker ps --format '{{.Names}}'); do
            timestamp=$(date +"%Y%m%d%H%M%S")
            backup_dir="/backup/$container_name/$timestamp"
            backup_file="/backup/$container_name/$timestamp/files_${container_name}_${timestamp}.tar.gz"
            
            log_user "$container_name" "Scheduled backup job started." > /dev/null 2>&1
            # files
            backup_files "$container_name" > /dev/null 2>&1

            # configuration
            export_entrypoint_file "$container_name" > /dev/null 2>&1
            export_webserver_main_conf_file "$container_name" > /dev/null 2>&1
            backup_mysql_conf_file "$container_name" > /dev/null 2>&1
            backup_timezone "$container_name" > /dev/null 2>&1
            backup_php_versions_in_container "$container_name" > /dev/null 2>&1

            #crons
            backup_crontab_for_root_user "$container_name" > /dev/null 2>&1
            
            #mysql
            backup_mysql_databases "$container_name" > /dev/null 2>&1
            backup_mysql_users "$container_name" > /dev/null 2>&1

            #panel data
            export_user_data_from_database "$container_name" > /dev/null 2>&1
            users_local_files_in_core_users "$container_name" > /dev/null 2>&1
            users_local_files_in_stats_users "$container_name" > /dev/null 2>&1

            #domains
            backup_apache_conf_and_ssl "$container_name" > /dev/null 2>&1
            log_user "$container_name" "Backup job successfully completed."
        done
    fi
else
    if [ "$DEBUG" = true ]; then
        # Container name is provided as an argument, backup only that user files..
        echo "Running backup for user: $container_name"
        timestamp=$(date +"%Y%m%d%H%M%S")
        backup_dir="/backup/$container_name/$timestamp"
        backup_file="/backup/$container_name/$timestamp/files_${container_name}_${timestamp}.tar.gz"
        log_user "$container_name" "Backup on demand started."
            # files
            backup_files "$container_name"

            # configuration
            export_entrypoint_file "$container_name"
            export_webserver_main_conf_file "$container_name"
            backup_mysql_conf_file "$container_name"
            backup_timezone "$container_name"
            backup_php_versions_in_container "$container_name"

            #crons
            backup_crontab_for_root_user "$container_name"
            
            #mysql
            backup_mysql_databases "$container_name"
            backup_mysql_users "$container_name"

            #panel data
            export_user_data_from_database "$container_name"
            users_local_files_in_core_users "$container_name"
            users_local_files_in_stats_users "$container_name"

            #domains
            backup_apache_conf_and_ssl "$container_name"
        log_user "$container_name" "Backup successfully completed."
    else
        # Container name is provided as an argument, backup only that user files..
        timestamp=$(date +"%Y%m%d%H%M%S")
        backup_dir="/backup/$container_name/$timestamp"
        backup_file="/backup/$container_name/$timestamp/files_${container_name}_${timestamp}.tar.gz"
        log_user "$container_name" "Backup on demand started." > /dev/null 2>&1
            # files
            backup_files "$container_name" > /dev/null 2>&1

            # configuration
            export_entrypoint_file "$container_name" > /dev/null 2>&1
            export_webserver_main_conf_file "$container_name" > /dev/null 2>&1
            backup_mysql_conf_file "$container_name" > /dev/null 2>&1
            backup_timezone "$container_name" > /dev/null 2>&1
            backup_php_versions_in_container "$container_name" > /dev/null 2>&1

            #crons
            backup_crontab_for_root_user "$container_name" > /dev/null 2>&1
            
            #mysql
            backup_mysql_databases "$container_name" > /dev/null 2>&1
            backup_mysql_users "$container_name" > /dev/null 2>&1

            #panel data
            export_user_data_from_database "$container_name" > /dev/null 2>&1
            users_local_files_in_core_users "$container_name" > /dev/null 2>&1
            users_local_files_in_stats_users "$container_name" > /dev/null 2>&1
            
            #domains
            backup_apache_conf_and_ssl "$container_name" > /dev/null 2>&1
        log_user "$container_name" "Backup successfully completed."
    fi
fi
