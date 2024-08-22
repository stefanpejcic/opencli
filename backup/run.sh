#!/bin/bash
################################################################################
# Script Name: run.sh
# Description: Run backup job
# Usage: opencli backup-run ID --all [--debug]
# Author: Stefan Pejcic
# Created: 26.01.2024
# Last Modified: 19.06.2024
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

# TODO: edit to also backup csf rules!

# check for server wide options
config_file="/etc/openpanel/openadmin/config/backups/config.ini"

if [ -e "$config_file" ]; then
    # enable debug
    debug_value=$(awk -F'=' '/^\[GENERAL\]/{f=1} f&&/^debug/{print $2; f=0}' "$config_file")
    
    if [ -n "$debug_value" ]; then
        if [ "$debug_value" = "no" ]; then
            DEBUG=false
        elif [ "$debug_value" = "yes" ]; then
            echo "DEBUG: Debug mode is enabled in server configuration."
            DEBUG=true
        fi
    fi

    #user sepcified temp dir
    workplace_dir=$(awk -F'=' '/^\[GENERAL\]/{f=1} f&&/^workplace_dir/{print $2; f=0}' "$config_file")

    if [ -n "$workplace_dir" ]; then
        local_temp_dir="$workplace_dir"
        if [ "$DEBUG" = true ]; then
        echo "DEBUG: Using $local_temp_dir as a workplace directory to store temporary backup files."
        fi
    else
        local_temp_dir="/tmp/openpanel_backup_temp_dir/"
        if [ "$DEBUG" = true ]; then
        echo "DEBUG: Workplace directory is not set, using $local_temp_dir as a workplace directory to store temporary backup files."
        fi
    fi

    #server laod limit 
    avg_load_limit=$(awk -F'=' '/^\[PERFORMANCE\]/{f=1} f&&/^avg_load_limit/{print $2; f=0}' "$config_file")

    if [ -n "$avg_load_limit" ]; then
        current_load=$(uptime | awk -F'[a-z]:' '{print $2}' | tr -d '[:space:]')
        one_minute_load=$(echo "$current_load" | awk -F, '{print $1}')
        
        if [ "$(echo "$one_minute_load >= $avg_load_limit" | bc -l)" -eq 1 ]; then
            echo "Current server load ($one_minute_load) is above the average load limit ($avg_load_limit) in server settings. Aborting backup..."
            exit 1
        else
            if [ "$DEBUG" = true ]; then
            echo "DEBUG: Server load ($one_minute_load) is below the average load limit ($avg_load_limit). Proceeding..."
            fi
        fi
        
    else
        if [ "$DEBUG" = true ]; then
        echo "DEBUG: Error: 'avg_load_limit' setting not found in $config_file. backup will start regardless of the current server load."
        fi
        
    fi
    
else
#when config file is missing..
DEBUG=false
local_temp_dir="/tmp/openpanel_backup_temp_dir/"
fi


TIMESTAMP=$(date +"%Y%m%d%H%M%S")
# Initialize all flags to false by default
FILES=false
ENTRYPOINT=false
WEBSERVER_CONF=false
MYSQL_CONF=false
MYSQL_DATA=false
PHP_VERSIONS=false
CRONTAB=false
USER_DATA=false
CORE_USERS=false
STATS_USERS=false
APACHE_SSL_CONF=false
DOMAIN_ACCESS_REPORTS=false
TIMEZONE=false
SSH_PASS=false
DOCKER=false
# settings
LOG_FILE="/var/log/openpanel/admin/notifications.log"

# Set a trap for CTRL+C to properly exit
trap "echo CTRL+C Pressed!; read -p 'Press Enter to exit...'; exit 1;" SIGINT SIGTERM


# Parse optional flags to skip specific actions
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        --files)
            FILES=true
            ;;
        --entrypoint)
            ENTRYPOINT=true
            ;;
        --apache-conf)
            WEBSERVER_CONF=true
            ;;
        --nginx-conf)
            WEBSERVER_CONF=true
            ;;
        --mysql-data)
            MYSQL_DATA=true
            ;;
        --mysql-conf)
            MYSQL_CONF=true
            ;;
        --php-versions)
            PHP_VERSIONS=true
            ;;
        --crontab)
            CRONTAB=true
            ;;
        --user-data)
            USER_DATA=true
            ;;
        --core-users)
            CORE_USERS=true
            ;;
        --stats-users)
            STATS_USERS=true
            ;;
        --apache-ssl-conf)
            APACHE_SSL_CONF=true
            ;;
        --domain-access-reports)
            DOMAIN_ACCESS_REPORTS=true
            ;;
        --ssh)
            SSH_PASS=true
            ;;
        --timezone)
            TIMEZONE=true
            ;;
        --docker)
            DOCKER=true
            ;;
        --all)
            # Set all flags to true if all flag is present
            FILES=true
            ENTRYPOINT=true
            WEBSERVER_CONF=true
            MYSQL_CONF=true
            MYSQL_DATA=true
            PHP_VERSIONS=true
            CRONTAB=true
            USER_DATA=true
            CORE_USERS=true
            STATS_USERS=true
            APACHE_SSL_CONF=true
            DOMAIN_ACCESS_REPORTS=true
            TIMEZONE=true
            SSH_PASS=true
            ;;
    esac
done



# Check if the correct number of command line arguments is provided
if [ "$#" -lt 2 ]; then
    echo "Usage: opencli backup-run <JOB_ID> --all [--force-run]"
    exit 1
fi

NUMBER=$1
FORCE_RUN=false

# Check if the --force-run flag is provided
for arg in "$@"; do
    if [ "$arg" == "--force-run" ]; then
        FORCE_RUN=true
        break
    fi
done

JSON_FILE="/etc/openpanel/openadmin/config/backups/jobs/$NUMBER.json"

# Check if the JSON file exists
if [ ! -f "$JSON_FILE" ]; then
    echo "Error: File $JSON_FILE does not exist."
    exit 1
fi

ensure_jq_installed() {
    # Check if jq is installed
    if ! command -v jq &> /dev/null; then
        # Detect the package manager and install jq
        if command -v apt-get &> /dev/null; then
            sudo apt-get update > /dev/null 2>&1
            sudo apt-get install -y -qq jq > /dev/null 2>&1
        elif command -v yum &> /dev/null; then
            sudo yum install -y -q jq > /dev/null 2>&1
        elif command -v dnf &> /dev/null; then
            sudo dnf install -y -q jq > /dev/null 2>&1
        else
            echo "Error: No compatible package manager found. Please install jq manually and try again."
            exit 1
        fi

        # Check if installation was successful
        if ! command -v jq &> /dev/null; then
            echo "Error: jq installation failed. Please install jq manually and try again."
            exit 1
        fi
    fi
}

# Read and parse the JSON file
read_json_file() {
    ensure_jq_installed
    local json_file="$1"
    jq -r '.status, .destination, .directory, .type[]?, .schedule, .retention, .filters[]?' "$json_file"
}

# Extract data from the JSON file
data=$(read_json_file "$JSON_FILE")

# Assign variables to extracted values
status=$(echo "$data" | awk 'NR==1')
destination=$(echo "$data" | awk 'NR==2')
dest_destination_dir_name=$(echo "$data" | awk 'NR==3')
types=($(echo "$data" | awk 'NR==4'))
retention=$(echo "$data" | awk 'NR==6')
filters=$(echo "$data" | awk 'NR==7')

# Check if the status is "off" and --force-run flag is not provided
if [ "$status" == "off" ] && [ "$FORCE_RUN" == false ]; then
    echo "Backup job is disabled. Use --force-run to run the backup job anyway."
    exit 0
fi


DEST_JSON_FILE="/etc/openpanel/openadmin/config/backups/destinations/$destination.json"

# Check if the destination JSON file exists
if [ ! -f "$DEST_JSON_FILE" ]; then
    echo "Error: Destination JSON file $DEST_JSON_FILE does not exist."
    exit 1
fi

# Read and parse the destination JSON file
read_dest_json_file() {
    ensure_jq_installed
    local dest_json_file="$1"
    jq -r '.hostname, .password, .ssh_port, .ssh_user, .ssh_key_path, .storage_limit' "$dest_json_file"
}

# Extract data from the destination JSON file
dest_data=$(read_dest_json_file "$DEST_JSON_FILE")

# Assign variables to extracted values
dest_hostname=$(echo "$dest_data" | awk 'NR==1')
dest_password=$(echo "$dest_data" | awk 'NR==2')
dest_ssh_port=$(echo "$dest_data" | awk 'NR==3')
dest_ssh_user=$(echo "$dest_data" | awk 'NR==4')
dest_ssh_key_path=$(echo "$dest_data" | awk 'NR==5')
dest_storage_limit=$(echo "$dest_data" | awk 'NR==6')

# Check if the destination hostname is local
if [[ "$dest_hostname" == "localhost" || "$dest_hostname" == "127.0.0.1" || "$dest_hostname" == "$(curl -s https://ip.openpanel.co || wget -qO- https://ip.openpanel.co)" || "$dest_hostname" == "$(hostname)" ]]; then
    echo "Destination is local. Backing up files locally to $dest_destination_dir_name folder"
    LOCAL=true
    REMOTE=false
else
    echo "Remote Destination, backing files using SSH connection to $dest_hostname"
    LOCAL=false
    REMOTE=true
fi


if [ "$DEBUG" = true ]; then
# backupjob json
    echo ""
    echo "------------------------------------------------------------------------"
    echo ""
    echo "DEBUG: Backup job configuration:"
    echo ""
    echo "DEBUG: Status: $status"
    echo "DEBUG: Destination ID: $destination"
    echo "DEBUG: Destination Directory Name: $dest_destination_dir_name"
    echo "DEBUG: Types: ${types[@]}"
    #echo "Schedule: $schedule"
    echo "DEBUG: Retention: $retention"
    echo "DEBUG: Filters: ${filters[@]}"
    # destination json
    echo ""
    echo "------------------------------------------------------------------------"
    echo ""
    echo "DEBUG: Backup destination configuration:"
    echo ""
    echo "DEBUG: Hostname: $dest_hostname"
    echo "DEBUG: Password: $dest_password"
    echo "DEBUG: SSH Port: $dest_ssh_port"
    echo "DEBUG: SSH User: $dest_ssh_user"
    echo "DEBUG: SSH Key Path: $dest_ssh_key_path"
    echo "DEBUG: Storage Limit: $dest_storage_limit"
fi










# Function to get the last message content from the log file
get_last_message_content() {
  tail -n 1 "$LOG_FILE" 2>/dev/null
}

# Function to check if an unread message with the same content exists in the log file
is_unread_message_present() {
  local unread_message_content="$1"
  grep -q "UNREAD.*$unread_message_content" "$LOG_FILE" && return 0 || return 1
}

# Function to write notification to log file if it's different from the last message content
write_notification() {
  local title="$1"
  local message="$2"
  local current_message="$(date '+%Y-%m-%d %H:%M:%S') UNREAD $title MESSAGE: $message"
  local last_message_content=$(get_last_message_content)

  # Check if the current message content is the same as the last one and has "UNREAD" status
  if [ "$message" != "$last_message_content" ] && ! is_unread_message_present "$title"; then
    echo "$current_message" >> "$LOG_FILE"
  fi
}


# Actuall copy to destination
copy_files() {
    source_path=$1
    destination_path=$2

    # Check if source_path starts with "docker:", use docker cp
    if [[ "$source_path" == docker:* ]]; then
        docker_source_path="${source_path#docker:}"  # Remove "docker:" prefix
        
        mkdir -p "$local_temp_dir"

        if [ "$DEBUG" = true ]; then
        echo "DEBUG: Copying files from the docker container to workplace directory. Command used: docker cp $docker_source_path $local_temp_dir"
        fi


        # First, copy from Docker container to local temp directory
        docker cp "$docker_source_path" "$local_temp_dir"

        # Update source_path to the local temp directory
        source_path="$local_temp_dir"
    fi


    if [[ "$source_path" == "/etc/letsencrypt/live/"* ]]; then
            cp -LTr "$source_path" "$local_temp_dir"
            source_path=$local_temp_dir
    fi

    if [ "$LOCAL" != true ]; then

        # Step 1: Create the remote directory
        ssh -i "$dest_ssh_key_path" -p "$dest_ssh_port" "$dest_ssh_user@$dest_hostname" "mkdir -p $dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"

        if [ "$DEBUG" = true ]; then
        echo "DEBUG: ssh -i $dest_ssh_key_path -p $dest_ssh_port $dest_ssh_user@$dest_hostname 'mkdir -p $dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path'"
        fi

        
        # Step 2: Rsync the files
        # use parallel for home dir files only for now, and only for remote destination
        if [[ "$source_path" == /home/* ]]; then
            if ! command -v parallel &> /dev/null; then
                if [ "$DEBUG" = true ]; then
                    echo "DEBUG: parallel is not installed. Installing moreutils..."
                fi
                    # Check if jq is installed
                        # Detect the package manager and install jq
                        if command -v apt-get &> /dev/null; then
                            sudo apt-get update > /dev/null 2>&1
                            sudo apt-get install -y moreutils > /dev/null 2>&1
                        elif command -v yum &> /dev/null; then
                            sudo yum install -y moreutils > /dev/null 2>&1
                        elif command -v dnf &> /dev/null; then
                            sudo dnf install -y moreutils > /dev/null 2>&1
                        else
                            echo "Error: No compatible package manager found. Please install jq manually and try again."
                            exit 1
                        fi
                
                        # Check if installation was successful
                        if ! command -v parallel &> /dev/null; then
                            echo "Error: moreutils installation failed. Please install parallel command manually and try again."
                            exit 1
                        fi
            fi
            
            find /home/$container_name/ -mindepth 1 -maxdepth 1 -print0 | parallel -j 16 | rsync -e "ssh -i $dest_ssh_key_path -p $dest_ssh_port" -r -p "$source_path" "$dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"
        else
            rsync -e "ssh -i $dest_ssh_key_path -p $dest_ssh_port" -r -p "$source_path" "$dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"
        fi

        if [ "$DEBUG" = true ]; then
        # Print commands for debugging
        echo "DEBUG: rsync -e 'ssh -i $dest_ssh_key_path -p $dest_ssh_port' -r -p $source_path $dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"
        fi

    else
        # for local lets just use cp for now, no need for paraller either..
        mkdir -p "/$dest_destination_dir_name/$container_name/$TIMESTAMP/"

        if [ -d "$source_path" ]; then
            # Source is a directory, proceed with copying its contents
            cp -LTr "$source_path" "/$dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"
        else
            # Source is a file, handle it appropriately
            cp -L "$source_path" "/$dest_destination_dir_name/$container_name/$TIMESTAMP/"
            # TODO: cp: '/backup/sdjnjrz3/20240619142028/20240619142028.tar.gz' and '/backup/sdjnjrz3/20240619142028/20240619142028.tar.gz' are the same file
        fi
        
    fi

    # Clean up local temp directory if used
    [ -n "$local_temp_dir" ] && rm -rf "$local_temp_dir"
}


# Example usage:
#copy_files "docker:/container/path" "/path/to/destination"







# Function to log messages to the user-specific log file for the user
log_user() {
    local user_log_file="/etc/openpanel/openpanel/core/users/$1/backup.log"
    local log_message="$2"
    # Ensure the log directory exists
    mkdir -p "$(dirname "$user_log_file")"
    # Append the log message with a timestamp
    echo "$(date +"%Y-%m-%d %H:%M:%S") - $log_message" >> "$user_log_file"
}

# DB
source /usr/local/admin/scripts/db.sh


backup_mysql_databases() {

mkdir -p "$BACKUP_DIR/mysql"

# Get a list of databases with the specified prefix
databases=$(docker exec "$container_name" mysql -u root -e "SHOW DATABASES LIKE '$container_name\_%';" | awk 'NR>1')

# Iterate through the list of databases and export each one
for db in $databases
do
  echo "Exporting database: $db"
  docker exec "$container_name" mysqldump -u root "$db" > "$BACKUP_DIR/mysql/$db.sql"
done


copy_files "$BACKUP_DIR/mysql/" "mysql/databases/"

echo "All MySQL databases have been exported to '$BACKUP_DIR/mysql/'."
}


backup_mysql_users() {

mkdir -p "$BACKUP_DIR/mysql/users/"

# Get a list of MySQL users (excluding root and other system users)
USERS=$(docker exec "$container_name" mysql -u root -Bse "SELECT user FROM mysql.user WHERE user NOT LIKE 'root' AND host='%'")

#docker exec "$container_name" bash -c "mysqldump -u root -e --skip-comments --skip-lock-tables --skip-set-charset --no-create-info mysql user > $BACKUP_DIR/mysql/users/mysql_users_and_permissions.sql"

for USER in $USERS
do
    # Generate a filename based on the username
    OUTPUT_FILE="$BACKUP_DIR/mysql/users/${USER}.sql"

    # Use mysqldump to export user accounts and their permissions
    docker exec "$container_name" mysqldump -u root -e --skip-comments --skip-lock-tables --skip-set-charset --no-create-info mysql user --where="user='$USER'" > $OUTPUT_FILE

    copy_files "$BACKUP_DIR/mysql/users/" "mysql/users/"

    echo "Exported mysql user '$USER' and their permissions to $OUTPUT_FILE."
done

}


backup_mysql_conf_file() {

mkdir -p "$BACKUP_DIR/docker/"

#docker cp $container_name:/etc/mysql/mysql.conf.d/mysqld.cnf $BACKUP_DIR/docker/
copy_files "docker:$container_name:/etc/mysql/mysql.conf.d/mysqld.cnf" "mysql/"
echo "Saved MySQL configuration file /etc/mysql/mysql.conf.d/mysqld.cnf"
}




export_webserver_main_conf_file() {

#get webserver for user
output=$(opencli webserver-get_webserver_for_user $container_name)

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

mkdir -p "$BACKUP_DIR/$ws/"
mkdir -p "$BACKUP_DIR/docker/"

#docker cp $container_name:/etc/$ws/$ws.conf $BACKUP_DIR/docker/
copy_files "docker:$container_name:/etc/$ws/$ws.conf" "docker/"
}




export_entrypoint_file() {
mkdir -p "$BACKUP_DIR/docker/"
#docker cp $container_name:/etc/entrypoint.sh $BACKUP_DIR/docker/entrypoint.sh
copy_files "docker:$container_name:/etc/entrypoint.sh" "docker/"
}



users_local_files_in_core_users() {
mkdir -p "$BACKUP_DIR/core/"
#cp -r /usr/local/panel/core/users/$container_name/ $BACKUP_DIR/core/
copy_files "/etc/openpanel/openpanel/core/users/$container_name/" "core/"
}

users_local_files_in_stats_users() {
mkdir -p "$BACKUP_DIR/stats/"
#cp -r /usr/local/panel/core/stats/$container_name/ $BACKUP_DIR/stats/
copy_files "/etc/openpanel/openpanel/core/stats/$container_name/" "stats/"
}



backup_php_versions_in_container(){

# Run the command and capture the output
default_php_version=$(opencli php-default_php_version $container_name)

# Check if the command was successful
if [ $? -eq 0 ]; then
    mkdir -p "$BACKUP_DIR/php/"
    # Save the output to a file
    echo "$default_php_version" > $BACKUP_DIR/php/default.txt
    copy_files "$BACKUP_DIR/php/default.txt" "php/"
    rm $BACKUP_DIR/php/default.txt
    echo "Default PHP version saved for user."
else
    echo "Error running the command, default PHP version for user is not saved."
fi


# Run the command and capture the output
output=$(opencli php-enabled_php_versions $container_name)

# Check if the command was successful
if [ $? -eq 0 ]; then
    mkdir -p "$BACKUP_DIR/php/"
    # Save the output to a file
    echo "$output" > $BACKUP_DIR/php/php_versions.txt
    copy_files "$BACKUP_DIR/php/php_versions.txt" "php/"
    echo "Saved a list of all PHP versions installed."
    rm $BACKUP_DIR/php/php_versions.txt

    version_numbers=$(echo "$output" | grep -oP 'php\d+\.\d+' | sed 's/php//')
    for version in $version_numbers; do
        if docker exec "$container_name" test -e "/etc/php/$version/fpm/php-fpm.conf"; then
        # Copy php-fpm.conf file
        copy_files "docker:$container_name:/etc/php/$version/fpm/php-fpm.conf" "php/$version/"
        echo "php-fpm.conf for PHP $version copied to $BACKUP_DIR/php/php-fpm_$version.conf"
        fi
    done
    rm -rf "$BACKUP_DIR/php/"
else
    echo "Error running the command, no PHP versions are backed up for the user."
fi
}



export_user_data_from_database() {
    user_id=$(mysql -e "SELECT id FROM users WHERE username='$container_name';" -N)

    if [ -z "$user_id" ]; then
        echo "ERROR: export_user_data_to_sql: User '$container_name' not found in the database."
        exit 1
    fi

    # Create a single SQL dump file
    backup_file="$BACKUP_DIR/DATA/"

    mkdir -p $backup_file

    # Export User Data
    mysql -e "SELECT * FROM panel.users WHERE id = $user_id" > $backup_file/users.sql
    
    # Export User's Plan Data
    mysql -e "SELECT p.* FROM panel.users u JOIN panel.plans p ON u.plan_id = p.id WHERE u.id = 1" > $backup_file/plans.sql
    
    # Export Domains Data for User
    mysql -e "SELECT * FROM panel.domains WHERE user_id = $user_id" > $backup_file/domains.sql
    
    # Export Sites Data for User
    mysql -e "SELECT s.* FROM panel.sites s JOIN panel.domains d ON s.domain_id = d.domain_id WHERE d.user_id = $user_id" > $backup_file/sites.sql

    
    # LATER FOR IMPORT
    # first drop, then do
    #mysql -u$DB_USER -p$DB_PASSWORD $DB_NAME < $EXPORT_DIR/users.sql
    #mysql -u$DB_USER -p$DB_PASSWORD $DB_NAME < $EXPORT_DIR/plans.sql
    #mysql -u$DB_USER -p$DB_PASSWORD $DB_NAME < $EXPORT_DIR/domains.sql
    #mysql -u$DB_USER -p$DB_PASSWORD $DB_NAME < $EXPORT_DIR/sites.sql
    



     copy_files "$backup_file/" "DATA"
    #rm $backup_file # FOR REMOTE WE SHOULD RM!
    echo "User '$container_name' data exported to $backup_file successfully."
}



backup_domain_access_reports() {
    mkdir -p $BACKUP_DIR/nginx/stats/
    if [ -d "$dest_destination_dir_name" ]; then
    copy_files "/var/log/nginx/stats/$container_name/" "/nginx/stats/"
    else
    echo "No resource usage stats found for user."
    fi
}

# Function to backup Apache .conf files and SSL certificates for domain names associated with a user
backup_apache_conf_and_ssl() {

    # Step 1: Get the user_id from the 'users' table
    user_id=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT id FROM users WHERE username='$container_name';" -N)
    
    if [ -z "$user_id" ]; then
        echo "ERROR: backup_apache_conf_and_ssl: User '$container_name' not found in the database."
        exit 1
    fi
    

    #get webserver for user
    output=$(opencli webserver-get_webserver_for_user $container_name)

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

    # Get domain names associated with the user_id from the 'domains' table
    local domain_names=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT domain_name FROM domains WHERE user_id='$user_id';" -N)
    echo "Getting Nginx configuration for user's domains.."
    # Loop through domain names
    for domain_name in $domain_names; do
        local apache_conf_dir="/etc/nginx/sites-available"
        
        local apache_conf_file="$domain_name.conf"
        
        local backup_apache_conf_dir="$BACKUP_DIR/nginx"
        
        local certbot_ssl_dir="/etc/letsencrypt/live/$domain_name"
        
        local backup_certbot_ssl_dir="$BACKUP_DIR/ssl/$domain_name"
        local backup_dns_zones_dir="$BACKUP_DIR/dns"
        local zone_file="/etc/bind/zones/$domain_name.zone"


        mkdir -p $backup_apache_conf_dir
        mkdir -p $backup_apache_conf_dir/container/


        #docker cp $container_name:/etc/$ws/sites-available/ $backup_apache_conf_dir/container/
        copy_files "docker:$container_name:/etc/$ws/sites-available/" "/nginx/container/"
        # Check if the zone file exists and copy it
        if [ -f "$zone_file" ]; then
            mkdir -p "$backup_dns_zones_dir"
            #cp "$zone_file" "$backup_dns_zones_dir"
            copy_files "$zone_file" "/dns"
            echo "Backed up DNS zone file for domain '$domain_name' to $backup_dns_zones_dir"
        else
            echo "DNS zone file for domain '$domain_name' not found."
        fi

        # Check if the Apache .conf file exists and copy it
        if [ -f "$apache_conf_dir/$apache_conf_file" ]; then
            mkdir -p "$backup_apache_conf_dir"
            copy_files "$apache_conf_dir/$apache_conf_file" "/nginx/sites-available/"
            echo "Backed up Nginx .conf file for domain '$domain_name' to $backup_apache_conf_dir"
        else
            echo "Nginx .conf file for domain '$domain_name' not found."
        fi

        # Check if Certbot SSL certificates exist and copy them
        if [ -d "$certbot_ssl_dir" ]; then
            copy_files "$certbot_ssl_dir/" "/ssl/"
            echo "Backed up Certbot SSL certificates for domain '$domain_name' to $backup_certbot_ssl_dir"
        else
            echo "Certbot SSL certificates for domain '$domain_name' not found."
        fi
    done
}

backup_crontab_for_root_user(){
    file_path="/var/spool/cron/crontabs/$container_name"
    if docker exec "$container_name" test -e "$file_path"; then
        copy_files "docker:$container_name:$file_path" "/crons/"
    else
        echo "Crontab file is empty, no cronjobs to backup."
    fi

}

backup_timezone(){
    copy_files "docker:$container_name:/etc/timezone" "/timezone/"
    #copy_files "docker:$container_name:/etc/localtime" "/timezone/"
}




backup_docker_container(){
    docker commit $container_name $container_name
    if [ $? -eq 0 ]; then
        if [ "$DEBUG" = true ]; then
            echo "DEBUG: image has been created, proceeding with saving image to .tar.gz file."
        fi
        docker save $container_name | gzip > $BACKUP_DIR/$container_name_$TIMESTAMP.tar.gz
        if [ $? -eq 0 ]; then
            if [ "$DEBUG" = true ]; then
                echo "DEBUG: deleting the image and uploading the $container_name_$TIMESTAMP.tar.gz file to destination."
            fi
            
            docker image rm $container_name
            
            if [ "$LOCAL" != true ]; then
                copy_files "$BACKUP_DIR/$container_name_$TIMESTAMP.tar.gz" "docker_image"
                rm $BACKUP_DIR/$container_name_$TIMESTAMP.tar.gz
            fi

        else
            echo "ERROR: Failed to save docker image $container_name"
        fi
    else
        echo "ERROR: Failed to commit docker container $container_name"
    fi
}



backup_ssh_conf_and_pass(){
    copy_files "docker:$container_name:/etc/shadow" "/docker/"
    copy_files "docker:$container_name:/etc/passwd" "/docker/"
}



# Function to check command success and exit on failure
check_command_success() {
    if [ $? -ne 0 ]; then
        echo "Error: $1"
        exit 1
    fi
}


# Function to backup files
backup_files() {
    local source_dir="/home/$container_name"
    #local destination_dir="$BACKUP_DIR/files"
    
    #mkdir -p "$destination_dir"

    copy_files "$source_dir" "files"
}



# Main Backup Function
perform_backup() {
    type=""
    log_user "$container_name" "Backup started."

    BACKUP_DIR="/backup/$container_name/$TIMESTAMP"

    mkdir -p "$BACKUP_DIR"


    if [ "$DOCKER" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Saving docker container."
            echo ""
        fi
        backup_docker_container
        type+="IMAGE,"
    fi


    
    if [ "$FILES" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up user files."
            echo ""
        fi
        backup_files
        type+="FILES,"
    fi

    if [ "$ENTRYPOINT" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up user services."
            echo ""
        fi
        export_entrypoint_file
        type+="ENTRYPOINT,"
    fi

    if [ "$WEBSERVER_CONF" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up webserver configuration file."
            echo ""
        fi
        export_webserver_main_conf_file
        type+="WEBSERVER_CONF,"
    fi

    if [ "$MYSQL_CONF" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up MySQL configuration."
            echo ""
        fi
        backup_mysql_conf_file
        type+="MYSQL_CONF,"
    fi

    if [ "$TIMEZONE" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up timezone settings."
            echo ""
        fi
        backup_timezone
        type+="TIMEZONE,"
    fi

    if [ "$PHP_VERSIONS" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up installed PHP versions and their .ini files."
            echo ""
        fi
        backup_php_versions_in_container
        type+="PHP_VERSIONS,"
    fi

    if [ "$CRONTAB" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up Cron Jobs."
            echo ""
        fi
        backup_crontab_for_root_user
        type+="CRONTAB,"
    fi

    if [ "$MYSQL_DATA" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up MySQL databases."
            echo ""
        fi
        backup_mysql_databases
        
        type+="MYSQL_DATA,"
        
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up MySQL users."
            echo ""
        fi
        backup_mysql_users
    fi

    if [ "$USER_DATA" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing list of domains and websites for user."
            echo ""
        fi
        export_user_data_from_database
        type+="USER_DATA,"
    fi

    if [ "$CORE_USERS" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up configuration files for the account."
            echo ""
        fi
        users_local_files_in_core_users
        type+="CORE_USERS,"
    fi

    if [ "$STATS_USERS" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up user resource usage statistics."
            echo ""
        fi
        users_local_files_in_stats_users
        type+="STATS_USERS,"
    fi

    if [ "$APACHE_SSL_CONF" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up VirtualHosts files and SSL Certificates."
            echo ""
        fi
        backup_apache_conf_and_ssl
        type+="APACHE_SSL_CONF,"
    fi

    if [ "$DOMAIN_ACCESS_REPORTS" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up generated HTML reports from domains access logs."
            echo ""
        fi
        backup_domain_access_reports
        type+="DOMAIN_ACCESS_REPORTS,"
    fi

    if [ "$SSH_PASS" = true ]; then
        if [ "$DEBUG" = true ]; then
            echo ""
            echo "DEBUG: ## Backing up SSH users."
            echo ""
        fi
        backup_ssh_conf_and_pass
        type+="SSH_PASS,"
    fi

    type="${type%,}"
    
    log_user "$container_name" "Backup completed successfully."

}




# log to the main log file for the job
log_dir="/var/log/openpanel/admin/backups/$NUMBER"
mkdir -p $log_dir
log_file="$log_dir/$(( $(ls -l "$log_dir" | grep -c '^-' ) + 1 )).log"
process_id=$$
start_time=$(date -u +"%a %b %d %T UTC %Y")











# Initial log content
initial_log_content="process_id=$process_id
type=$type
start_time=$start_time
end_time=
total_exec_time=
status=In progress.."

# Create log file and write initial content
echo -e "$initial_log_content" > "$log_file"

# Redirect all output to the log file
exec > >(tee -a "$log_file") 2>&1


backup_for_user_started(){
    start_backup_for_user_time=$(date -u +"%a %b %d %T UTC %Y")
    mkdir -p "$(dirname "$user_index_file")"
        
    # write that we started backup for user account
    initial_index_content="
    backup_job_id=$NUMBER
    destination_id=$destination
    destination_directory=$TIMESTAMP
    start_time=$start_backup_for_user_time
    end_time=
    total_exec_time=
    contains=$type
    status=In progress.."
    separator=""
    # Create log file and write initial content
    echo -e "$initial_index_content$separator" >> "$user_index_file"



}

backup_for_user_finished(){
    # write that we finished backup for user account
    end_backup_for_user_time=$(date -u +"%a %b %d %T UTC %Y")
    #$status="Completed"
    total_exec_time_spent_for_user=$(($(date -u +"%s") - $(date -u -d "$start_backup_for_user_time" +"%s")))
    
    sed -i -e "s/end_time=/end_time=$end_backup_for_user_time/" -e "s/total_exec_time=/total_exec_time=$total_exec_time_spent_for_user/" -e "s/status=.*/status=Completed/" "$user_index_file"
}


retention_for_user_files_delete_oldest_files_for_job_id(){
    # Get a list of all lines with "end_time="
    lines_with_end_time=$(grep -n "end_time=" "$user_index_file" | cut -d: -f1)

    # Extract the dates and destination_directory from each line and find the oldest date
    oldest_date=""
    oldest_destination_directory=""
    for line_number in $lines_with_end_time; do
        end_time=$(sed -n "${line_number}s/.*end_time=\(.*\)/\1/p" "$user_index_file")
        current_date=$(date -d "$end_time" +%s)
        
        if [ -z "$oldest_date" ] || [ "$current_date" -lt "$oldest_date" ]; then
            oldest_date="$current_date"
            oldest_line_number="$line_number"
            oldest_destination_directory=$(sed -n "${oldest_line_number} {/destination_directory=/s/.*destination_directory=\([^ ]*\).*/\1/p}" "$user_index_file")
        fi
    done

    # Find the first empty row above the oldest line
    first_empty_row_above=$(awk -v line="$oldest_line_number" 'NR < line && NF == 0 { last_empty_row = NR } END { print last_empty_row }' "$user_index_file")

    # Find the first line after the end_time line that has content "backup_job_id"
    first_line_after_end_time=$(awk -v line="$oldest_line_number" '$1 == "backup_job_id" && NR > line { print NR; exit }' "$user_index_file")

    # Delete the text between the first empty row above and the first line after end_time with "backup_job_id"
    sed -i "${first_empty_row_above},${first_line_after_end_time}d" "$user_index_file"

    # Print or use the oldest date and destination_directory as needed
    oldest_date_formatted=$(date -d "@$oldest_date" +"%c")


    if [ "$DEBUG" = true ]; then
        # Print for debugging
        echo "DEBUG: Oldest Date: $oldest_date_formatted"
        echo "DEBUG: Oldest Destination Directory: $oldest_destination_directory"
    fi


    ###### NOW DO THE ACTUAL ROTATION ON DESTINATION

    if [ "$LOCAL" != true ]; then
        ssh -i "$dest_ssh_key_path" -p "$dest_ssh_port" "$dest_ssh_user@$dest_hostname" "rm -rf $dest_destination_dir_name/$container_name/$TIMESTAMP/"
        echo "Deleted oldest backup for user $container_name from the remote destination: $dest_destination_dir_name/$container_name/$TIMESTAMP/"
    else
        # for local lets just use cp for now, no need for paraller either..
        rm -rf "$dest_destination_dir_name/$container_name/$TIMESTAMP/"
        echo "Deleted oldest backup for user $container_name from the local server: $dest_destination_dir_name/$container_name/$TIMESTAMP/"
    fi
    
    
}







































# Actuall copy to destination
copy_files_server_conf_only() {
    source_path=$1
    destination_path=$2

    if [ "$LOCAL" != true ]; then

        # Step 1: Create the remote directory
        ssh -i "$dest_ssh_key_path" -p "$dest_ssh_port" "$dest_ssh_user@$dest_hostname" "mkdir -p $dest_destination_dir_name/$TIMESTAMP/"

        if [ "$DEBUG" = true ]; then
        echo "DEBUG: ssh -i $dest_ssh_key_path -p $dest_ssh_port $dest_ssh_user@$dest_hostname 'mkdir -p $dest_destination_dir_name/$TIMESTAMP/'"
        fi

        
        # Step 2: Rsync the files
        # use parallel for home dir files only for now, and only for remote destination
        if [[ "$source_path" == /home/* ]]; then
            if ! command -v parallel &> /dev/null; then
                if [ "$DEBUG" = true ]; then
                    echo "DEBUG: parallel is not installed. Installing moreutils..."
                    sudo apt-get install -y moreutils
                else
                    sudo apt-get install -y moreutils > /dev/null 2>&1
                fi
            fi
            
            find /home/$container_name/ -mindepth 1 -maxdepth 1 -print0 | parallel -j 16 | rsync -e "ssh -i $dest_ssh_key_path -p $dest_ssh_port" -r -p "$source_path" "$dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$TIMESTAMP/"
        else
            rsync -e "ssh -i $dest_ssh_key_path -p $dest_ssh_port" -r -p "$source_path" "$dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$TIMESTAMP/"
        fi

        if [ "$DEBUG" = true ]; then
        # Print commands for debugging
        echo "DEBUG: rsync -e 'ssh -i $dest_ssh_key_path -p $dest_ssh_port' -r -p $source_path $dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$TIMESTAMP/"
        fi

    else
        # for local lets just use cp for now, no need for paraller either..
        cp -LTr "$source_path" "$destination_path"
    fi

    # Clean up local temp directory if used
    [ -n "$1" ] && rm -rf "$1/*"
}






run_backup_for_server_configuration_only() {

CONF_DESTINATION_DIR="/tmp" # FOR NOW USE /tmp/ only...




    backup_openadmin_files() {
        echo ""
        echo "## Backing up OpenAdmin files."
        echo ""
        mkdir -p ${CONF_DESTINATION_DIR}/openadmin
        cp -r /usr/local/admin/* ${CONF_DESTINATION_DIR}/openadmin  
    }
    
    backup_openpanel_conf() {
        echo ""
        echo "## Backing up MySQL database for OpenPanel."
        echo ""
        mkdir -p ${CONF_DESTINATION_DIR}/openpanel
        cp -r /etc/openpanel ${CONF_DESTINATION_DIR}/openpanel
        #
        cp -r /usr/local/admin/static/custom ${CONF_DESTINATION_DIR}/openpanel/admin_custom
        
        docker cp openpanel:/usr/local/panel/translations/ ${CONF_DESTINATION_DIR}/openpanel/translations        
        # here also should do the custom files for panel!
    }

    backup_named_conf() {
        echo ""
        echo "## Backing up BIND9 service configuration and DNS zones for all domains.."
        echo ""
        cp -r /etc/bind ${CONF_DESTINATION_DIR}/bind
    }
    
    # firewall rules
    backup_etc_ufw(){
        echo ""
        echo "## Backing up firewall rules."
        echo ""
        cp -r /etc/ufw ${CONF_DESTINATION_DIR}/ufw
    }

   
    # docker conf
    backup_docker_daemon(){
        echo ""
        echo "## Backing up Docker configuration."
        echo ""
        cp /etc/docker/daemon.json ${CONF_DESTINATION_DIR}/docker_daemon.json
        # this is symlink, check if it follows
    }

    
    
    # panel db
    backup_mysql_panel_database() {
        echo ""
        echo "## Backing up MySQL database for OpenPanel."
        echo ""
        mkdir -p ${CONF_DESTINATION_DIR}/mysql_data/
        docker run --rm --volumes-from openpanel_mysql -v ${CONF_DESTINATION_DIR}/mysql_data:/backup ubuntu tar czvf /backup/mysql_volume_data.tar.gz /var/lib/mysql
        
        #to restore we will use:
        #
        # docker run --rm -v ${CONF_DESTINATION_DIR}/mysql_data:/backup ubuntu tar xzvf /backup/mysql_volume_data.tar.gz -C /backup
    }
    
    # nginx domains
    backup_nginx_data() {
        echo ""
        echo "## Backing up Nginx web server configuration and domain files."
        echo ""
        NGINX_DESTINATION_DIR="${CONF_DESTINATION_DIR}/nginx/"
        mkdir -p $NGINX_DESTINATION_DIR
        cp -r /etc/nginx/sites-available ${NGINX_DESTINATION_DIR}sites_available
        cp -r /etc/nginx/sites-enabled ${NGINX_DESTINATION_DIR}sites_enabled
    }

    # /root/docker-compose.yml
    backup_docker_compose() {
        echo ""
        echo "## Backing up docker compose file for OpenPanel, Nignx and MySQL."
        echo ""
        COMPOSE_DESTINATION_DIR="${CONF_DESTINATION_DIR}/compose/"
        mkdir -p $COMPOSE_DESTINATION_DIR
        cp /root/docker-compose.yml ${COMPOSE_DESTINATION_DIR}docker-compose.yml
        cp /root/.env ${COMPOSE_DESTINATION_DIR}.env
        # DONT NEED AFTER INSTALL cp /root/initialize.sql ${COMPOSE_DESTINATION_DIR}initialize.sql
    }


    # backup server data only
    backup_openpanel_conf
    backup_openadmin_files
    backup_mysql_panel_database
    backup_nginx_data
    backup_docker_daemon
    backup_etc_ufw
    backup_named_conf
    backup_docker_compose


copy_files_server_conf_only $CONF_DESTINATION_DIR $dest_destination_dir_name
    
}

run_backup_for_user_data() {

    # backup user data only

    container_count=0
   
    # Get the total number of users
    output=$(opencli user-list --json)
    total_containers=$(echo "$output" | jq -c '.[] | select(.username | test("^[^_]*$"))' | wc -l)
    
    # Loop through each JSON object
    echo "$output" | jq -c '.[]' | while IFS= read -r line; do
        # Extract the username from the current JSON object
        container_name=$(echo "$line" | jq -r '.username')
        
        ((container_count++))
        
            # suspended users
            if [[ "$container_name" =~ [_] ]]; then
                container_name="${container_name#*_}"
                echo ""
                echo "------------------------------------------------------------------------"
                echo ""
                echo "Skipping backup for suspended user: $container_name (Account: $container_count/$total_containers)"
                echo ""
                continue  # Skip this container
            fi
     
            excluded_file="/usr/local/admin/scripts/helpers/excluded_from_backups.txt"
    
            # Check if container name is in the excluded list
            if [ -f "$excluded_file" ]; then
                if grep -Fxq "$container_name" "$excluded_file"; then
                    echo ""
                    echo "------------------------------------------------------------------------"
                    echo ""
                    echo "Skipping backup for excluded user: $container_name (Account: $container_count/$total_containers)"
                    echo ""
                    continue  # Skip this container
                fi
            fi


           
            echo ""
            echo "------------------------------------------------------------------------"
            echo ""
            echo "Starting backup for user: $container_name (Account: $container_count/$total_containers)"
            echo ""
            mkdir -p "/etc/openpanel/openadmin/config/backups/index/$NUMBER/$container_name/"
            user_index_file="/etc/openpanel/openadmin/config/backups/index/$NUMBER/$container_name/$TIMESTAMP.index"
            user_indexes="/etc/openpanel/openadmin/config/backups/index/$NUMBER/$container_name/"
            number_of_backups_in_this_job_that_user_has=$(find "$user_indexes" -type f -name "*.index" | wc -l)
        
            if [ -z "$number_of_backups_in_this_job_that_user_has" ]; then
                number_of_backups_in_this_job_that_user_has=0
            fi
        
            if [ "$DEBUG" = true ]; then
            # Print commands for debugging
            echo "DEBUG: Users index file: $user_index_file"
            echo "DEBUG: Number of current backups that user has in this backup job: $number_of_backups_in_this_job_that_user_has"
            fi
    
    
                backup_for_user_started
                mkdir -p "/etc/openpanel/openadmin/config/backups/index/$NUMBER/$container_name/"
                if [ "$LOCAL" = true ]; then
                    mkdir -p "/$dest_destination_dir_name/$container_name/$TIMESTAMP/"
                fi
                #cp index when we started
                copy_files "$user_index_file" "/$dest_destination_dir_name/$container_name/$TIMESTAMP/$TIMESTAMP.index"
                perform_backup "$container_name"
                backup_for_user_finished
                
   
        if [ "$LOCAL" != true ]; then
                ssh -i "$dest_ssh_key_path" -p "$dest_ssh_port" "$dest_ssh_user@$dest_hostname" "rm $dest_destination_dir_name/$container_name/$TIMESTAMP/$TIMESTAMP.index"
        #else
         # WHY ?
                #rm "$dest_destination_dir_name/$container_name/$TIMESTAMP/$TIMESTAMP.index"
        fi
                #cp index when we ended
                copy_files "$user_index_file" "/$dest_destination_dir_name/$container_name/$TIMESTAMP/$TIMESTAMP.index"
                
    
            # Compare with retention
            if [ "$number_of_backups_in_this_job_that_user_has" -ge "$retention" ]; then
                # Action A
                echo "User has a total of $number_of_backups_in_this_job_that_user_has backups, reached retention of $retention, will delete oldest user backup after generating a new one."
                #retention_for_user_files_delete_oldest_files_for_job_id
            else
                # Action B
                echo "User has a total of $number_of_backups_in_this_job_that_user_has backups, retention limit of $retention is not reached, no rotation is needed."
            fi
            
    done


}




# Check if the first element of the array is "accounts" or "partial"
if [[ ${types[0]} == "accounts" ]]; then
    echo ""
    echo "------------------------------------------------------------------------"
    echo ""
    echo "STARTING USER ACCOUNTS SNAPSHOTS BACKUP"
    echo ""

    run_backup_for_user_data

elif [[ ${types[0]} == "partial" ]]; then
    echo ""
    echo "------------------------------------------------------------------------"
    echo ""
    echo "STARTING USER ACCOUNTS PARTIAL BACKUP"
    echo ""
    echo ""
    
    run_backup_for_user_data
    
elif [[ ${types[0]} == "configuration" ]]; then
    echo ""
    echo "------------------------------------------------------------------------"
    echo ""
    echo "STARTING SERVER CONFIGURATION BACKUP"
    echo ""
    echo ""
    
    run_backup_for_server_configuration_only
else
    echo "ERROR: Backup type is unknown, supported types are 'accounts' 'partial' and 'configuration'."
    exit 1
fi




        
# Update log with end time, total execution time, and status
end_time=$(date -u +"%a %b %d %T UTC %Y")
total_exec_time=$(($(date -u +"%s") - $(date -u -d "$start_time" +"%s")))
status="Completed"


        echo ""
        echo "------------------------------------------------------------------------"
        echo ""
        echo "Backup Job finished at $end_time - Total execution time: $total_exec_time"
        echo ""
        echo ""

# Update the initial log content
sed -i -e "s/end_time=/end_time=$end_time/" -e "s/total_exec_time=/total_exec_time=$total_exec_time/" -e "s/status=In progress../status=$status/" "$log_file"



# write notification to notifications center
write_notification "Backup Job ID: $NUMBER finished" "Accounts: $total_containers - Total execution time: $total_exec_time"
