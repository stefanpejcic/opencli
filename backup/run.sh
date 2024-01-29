#!/bin/bash
################################################################################
# Script Name: run.sh
# Description: Run backup job
# Usage: opencli backup-run ID --all [--debug]
# Author: Stefan Pejcic
# Created: 26.01.2024
# Last Modified: 29.01.2024
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


TIMESTAMP=$(date +"%Y%m%d%H%M%S")
# Initialize all flags to false by default
DEBUG=false
SINGLE_CONTAINER=false
FILES=false
ENTRYPOINT=false
WEBSERVER_CONF=false
MYSQL_CONF=false
PHP_VERSIONS=false
CRONTAB=false
USER_DATA=false
CORE_USERS=false
STATS_USERS=false
APACHE_SSL_CONF=false
DOMAIN_ACCESS_REPORTS=false
TIMEZONE=false
SSH_PASS=false

# settings
local_temp_dir="/tmp/openpanel_backup_temp_dir"
LOG_FILE="/usr/local/admin/logs/notifications.log"

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
        --all)
            # Set all flags to true if all flag is present
            DEBUG=true
            SINGLE_CONTAINER=true
            FILES=true
            ENTRYPOINT=true
            WEBSERVER_CONF=true
            MYSQL_CONF=true
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
if [ "$#" -ne 1 ] && [ "$#" -ne 2 ]; then
    echo "Usage: opencli backup-run <JOB_ID> --all [--force-run]"
    exit 1
fi

NUMBER=$1
FORCE_RUN=false

# Check if the --force-run flag is provided
if [ "$#" -eq 2 ] && [ "$2" == "--force-run" ]; then
    FORCE_RUN=true
fi

JSON_FILE="/usr/local/admin/backups/jobs/$NUMBER.json"

# Check if the JSON file exists
if [ ! -f "$JSON_FILE" ]; then
    echo "Error: File $JSON_FILE does not exist."
    exit 1
fi

# Read and parse the JSON file
read_json_file() {
    local json_file="$1"
    jq -r '.status, .destination, .directory, .type[]?, .schedule, .retention, .filters[]?' "$json_file"
}

# Extract data from the JSON file
data=$(read_json_file "$JSON_FILE")

# Assign variables to extracted values
status=$(echo "$data" | awk 'NR==1')
destination=$(echo "$data" | awk 'NR==2')
directory=$(echo "$data" | awk 'NR==3')
types=($(echo "$data" | awk 'NR>=4 && NR<=6'))
schedule=$(echo "$data" | awk 'NR==7')
retention=$(echo "$data" | awk 'NR==8')
filters=($(echo "$data" | awk 'NR>=9'))

# Check if the status is "off" and --force-run flag is not provided
if [ "$status" == "off" ] && [ "$FORCE_RUN" == false ]; then
    echo "Backup job is disabled. Use --force-run to run the backup job anyway."
    exit 0
fi


DEST_JSON_FILE="/usr/local/admin/backups/destinations/$destination.json"

# Check if the destination JSON file exists
if [ ! -f "$DEST_JSON_FILE" ]; then
    echo "Error: Destination JSON file $DEST_JSON_FILE does not exist."
    exit 1
fi

# Read and parse the destination JSON file
read_dest_json_file() {
    local dest_json_file="$1"
    jq -r '.hostname, .password, .ssh_port, .ssh_user, .ssh_key_path, .destination_dir_name, .storage_limit' "$dest_json_file"
}

# Extract data from the destination JSON file
dest_data=$(read_dest_json_file "$DEST_JSON_FILE")

# Assign variables to extracted values
dest_hostname=$(echo "$dest_data" | awk 'NR==1')
dest_password=$(echo "$dest_data" | awk 'NR==2')
dest_ssh_port=$(echo "$dest_data" | awk 'NR==3')
dest_ssh_user=$(echo "$dest_data" | awk 'NR==4')
dest_ssh_key_path=$(echo "$dest_data" | awk 'NR==5')
dest_destination_dir_name=$(echo "$dest_data" | awk 'NR==6')
dest_storage_limit=$(echo "$dest_data" | awk 'NR==7')

# Check if the destination hostname is local
if [[ "$dest_hostname" == "localhost" || "$dest_hostname" == "127.0.0.1" || "$dest_hostname" == "$(curl -s https://ip.openpanel.co || wget -qO- https://ip.openpanel.co)" || "$dest_hostname" == "$(hostname)" ]]; then
    echo "Destination is local. Backing up files locally to $directory folder"
    LOCAL=true
    REMOTE=false
else
    echo "Destination is not local. Backing files using SSH connection to $dest_hostname"
    LOCAL=false
    REMOTE=true
fi


if [ "$DEBUG" = true ]; then
# backupjob json
echo "Status: $status"
echo "Destination: $destination"
echo "Directory: $directory"
echo "Types: ${types[@]}"
echo "Schedule: $schedule"
echo "Retention: $retention"
echo "Filters: ${filters[@]}"
# destination json
echo "Destination Hostname: $dest_hostname"
echo "Destination Password: $dest_password"
echo "Destination SSH Port: $dest_ssh_port"
echo "Destination SSH User: $dest_ssh_user"
echo "Destination SSH Key Path: $dest_ssh_key_path"
echo "Destination Directory Name: $dest_destination_dir_name"
echo "Destination Storage Limit: $dest_storage_limit"
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
        
        # First, copy from Docker container to local temp directory
        docker cp "$docker_source_path" "$local_temp_dir"

        # Update source_path to the local temp directory
        source_path="$local_temp_dir"
    fi

    if [ "$LOCAL" != true ]; then

        # Step 1: Create the remote directory
        ssh -i "$dest_ssh_key_path" -p "$dest_ssh_port" "$dest_ssh_user@$dest_hostname" "mkdir -p $dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"
        
        # Step 2: Rsync the files
        # use parallel for hoem dir files only for now, and only for remote destination
        if [[ "$source_path" == /home/* ]]; then
            if ! command -v parallel &> /dev/null; then
                if [ "$DEBUG" = true ]; then
                    echo "parallel is not installed. Installing moreutils..."
                    sudo apt-get install -y moreutils
                else
                    sudo apt-get install -y moreutils > /dev/null 2>&1
                fi
            fi
            
            find /home/{username}/ -mindepth 1 -maxdepth 1 -print0 | parallel -0 -j 16 | rsync -e "ssh -i $dest_ssh_key_path -p $dest_ssh_port" -r -p "$source_path" "$dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"
        else
            rsync -e "ssh -i $dest_ssh_key_path -p $dest_ssh_port" -r -p "$source_path" "$dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"
        fi

        if [ "$DEBUG" = true ]; then
        # Print commands for debugging
        echo "ssh -i $dest_ssh_key_path -p $dest_ssh_port $dest_ssh_user@$dest_hostname 'mkdir -p $dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path'"
        echo "rsync -e 'ssh -i $dest_ssh_key_path -p $dest_ssh_port' -r -p $source_path $dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"
        fi

    else
        # for local lets just use cp for now, no need for paraller either..
        cp -Lr "$source_path" "$destination_path"
    fi

    # Clean up local temp directory if used
    [ -n "$local_temp_dir" ] && rm -rf "$local_temp_dir"
}


# Example usage:
#copy_files "docker:/container/path" "/path/to/destination"
























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


copy_files "$BACKUP_DIR/mysql/" "mysql"

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
copy_files "docker:$container_name:/etc/mysql/mysql.conf.d/mysqld.cnf" "mysql/users/"
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
copy_files "/usr/local/panel/core/users/$container_name/" "core/"
}

users_local_files_in_stats_users() {
mkdir -p "$BACKUP_DIR/stats/"
#cp -r /usr/local/panel/core/stats/$container_name/ $BACKUP_DIR/stats/
copy_files "/usr/local/panel/core/stats/$container_name/" "stats/"
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
    echo "PHP versions saved to php_versions.txt"
    rm $BACKUP_DIR/php/php_versions.txt

    version_numbers=$(echo "$output" | grep -oP 'php\d+\.\d+' | sed 's/php//')
    for version in $version_numbers; do
        # Copy php-fpm.conf file
        #docker cp $container_name:"/etc/php/$version/fpm/php-fpm.conf" "$BACKUP_DIR/php/php-fpm_$version.conf"
        copy_files "docker:$container_name:/etc/php/$version/fpm/php-fpm.conf" "php/"
        echo "php-fpm.conf for PHP $version copied to $BACKUP_DIR/php/php-fpm_$version.conf"
        rm "$BACKUP_DIR/php/php-fpm_$version.conf"
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
    backup_file="$BACKUP_DIR/user_data_dump.sql"
    
    # Use mysqldump to export data from the 'sites', 'domains', and 'users' tables
    mysqldump --defaults-extra-file="$config_file" --no-create-info --no-tablespaces --skip-extended-insert "$mysql_database" users -w "id='$user_id'" >> "$backup_file"
    mysqldump --defaults-extra-file="$config_file" --no-create-info --no-tablespaces --skip-extended-insert --single-transaction "$mysql_database" domains -w "user_id='$user_id'" >> "$backup_file"
    mysqldump --defaults-extra-file="$config_file" --no-create-info --no-tablespaces --skip-extended-insert --single-transaction "$mysql_database" sites -w "domain_id IN (SELECT domain_id FROM domains WHERE user_id='$user_id')" >> "$backup_file"
    copy_files "$BACKUP_DIR/user_data_dump.sql" "/"
    rm $BACKUP_DIR/user_data_dump.sql
    echo "User '$container_name' data exported to $backup_file successfully."
}


backup_domain_access_reports() {
    mkdir -p $BACKUP_DIR/nginx/stats/
    #cp -r /var/log/nginx/stats/$container_name/ $BACKUP_DIR/nginx/stats/
    copy_files "/var/log/nginx/stats/$container_name/" "/nginx/stats/"
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
        copy_files "docker:@container_name:/etc/$ws/sites-available/" "/nginx/container/"
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
            #cp "$apache_conf_dir/$apache_conf_file" "$backup_apache_conf_dir/$apache_conf_file"
            copy_files "$apache_conf_dir/$apache_conf_file" "/nginx/sites-available/"
            echo "Backed up Nginx .conf file for domain '$domain_name' to $backup_apache_conf_dir"
        else
            echo "Nginx .conf file for domain '$domain_name' not found."
        fi

        # Check if Certbot SSL certificates exist and copy them
        if [ -d "$certbot_ssl_dir" ]; then
            #mkdir -p "$backup_certbot_ssl_dir"
            #cp -Lr "$certbot_ssl_dir"/* "$backup_certbot_ssl_dir/"
            copy_files "$certbot_ssl_dir/" "/ssl/$domain_name/"
            echo "Backed up Certbot SSL certificates for domain '$domain_name' to $backup_certbot_ssl_dir"
        else
            echo "Certbot SSL certificates for domain '$domain_name' not found."
        fi
    done
}

backup_crontab_for_root_user(){
    file_path="/var/spool/cron/crontabs/root"

    if [ -e "$file_path" ]; then
        #mkdir -p "$BACKUP_DIR/crons/"
        #docker cp $container_name:$file_path $BACKUP_DIR/crons/
        copy_files "docker:$container_name:$file_path" "/crons/"
    else
        echo "Crontab is empty, no cronjobs to backup."
    fi

}

backup_timezone(){
    copy_files "docker:$container_name:/etc/timezone" "/timezone/"
    copy_files "docker:$container_name:/etc/localtime" "/timezone/"
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
    local destination_dir="$BACKUP_DIR/files"
    
    mkdir -p "$destination_dir"

    if [ "$DEBUG" = true ]; then
        tar -czvf "$destination_dir/files_${container_name}_${TIMESTAMP}.tar.gz" "$source_dir"
        check_command_success "Error while creating files backup."
    else
        tar -czvf "$destination_dir/files_${container_name}_${TIMESTAMP}.tar.gz" "$source_dir"  > /dev/null 2>&1
    fi

    copy_files "$destination_dir/files_${container_name}_${TIMESTAMP}.tar.gz" "/files/"
    rm "$destination_dir/files_${container_name}_${TIMESTAMP}.tar.gz"
}



# Main Backup Function
perform_backup() {
    log_user "$container_name" "Backup started."

    BACKUP_DIR="/backup/$container_name/$TIMESTAMP"

    mkdir -p "$BACKUP_DIR"
    
    if [ "$FILES" = true ]; then
        backup_files 
    fi

    if [ "$ENTRYPOINT" = true ]; then
        export_entrypoint_file
    fi

    if [ "$WEBSERVER_CONF" = true ]; then
        export_webserver_main_conf_file
    fi

    if [ "$MYSQL_CONF" = true ]; then
        backup_mysql_conf_file
    fi

    if [ "$TIMEZONE" = true ]; then
        backup_timezone
    fi

    if [ "$PHP_VERSIONS" = true ]; then
        backup_php_versions_in_container
    fi

    if [ "$CRONTAB" = true ]; then
        backup_crontab_for_root_user
    fi

    if [ "$MYSQL_CONF" = true ]; then
        backup_mysql_databases
        backup_mysql_users
    fi

    if [ "$USER_DATA" = true ]; then
        export_user_data_from_database
    fi

    if [ "$CORE_USERS" = true ]; then
        users_local_files_in_core_users
    fi

    if [ "$STATS_USERS" = true ]; then
        users_local_files_in_stats_users
    fi

    if [ "$APACHE_SSL_CONF" = true ]; then
        backup_apache_conf_and_ssl
    fi

    if [ "$DOMAIN_ACCESS_REPORTS" = true ]; then
        backup_domain_access_reports
    fi

    if [ "$SSH_PASS" = true ]; then
        backup_ssh_conf_and_pass
    fi
    
    log_user "$container_name" "Backup completed successfully."




}





# log to the main log file for the job
log_dir="/usr/local/admin/backups/logs/$NUMBER"
mkdir -p $log_dir
log_file="$log_dir/$(( $(ls -l "$log_dir" | grep -c '^-' ) + 1 )).log"
process_id=$$
start_time=$(date -u +"%a %b %d %T UTC %Y")

# Determine type based on conditions
if [ "$FILES" = true ] && [ "$ENTRYPOINT" = true ] && [ "$WEBSERVER_CONF" = true ] && \
   [ "$MYSQL_CONF" = true ] && [ "$PHP_VERSIONS" = true ] && [ "$CRONTAB" = true ] && \
   [ "$USER_DATA" = true ] && [ "$CORE_USERS" = true ] && [ "$STATS_USERS" = true ] && \
   [ "$APACHE_SSL_CONF" = true ] && [ "$DOMAIN_ACCESS_REPORTS" = true ] && \
   [ "$TIMEZONE" = true ] && [ "$SSH_PASS" = true ]; then
    type="Full Backup"
else
    # List of conditions to check individually
    conditions=("FILES" "ENTRYPOINT" "WEBSERVER_CONF" "MYSQL_CONF" "PHP_VERSIONS" "CRONTAB" "USER_DATA" "CORE_USERS" "STATS_USERS" "APACHE_SSL_CONF" "DOMAIN_ACCESS_REPORTS" "TIMEZONE" "SSH_PASS")
    
    # Initialize type as empty
    type=""

    # Check each condition and append to type if false
    for condition in "${conditions[@]}"; do
        if [ "${!condition}" = false ]; then
            if [ -n "$type" ]; then
                type+=" | "
            fi
            type+="$condition"
        fi
    done
fi










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


container_count=0
# Get the total number of running containers
total_containers=$(docker ps -q | wc -l)

# Loop through containers or backup a specific container if name provided in command
if [ -z "$container_name" ]; then
    for container_name in $(docker ps --format '{{.Names}}'); do

        ((container_count++))
        echo "Starting backup for user: $container_name (Account: $container_count/$total_containers)"
        perform_backup "$container_name"
    done
else
    echo "Running backup for user: $container_name"
    perform_backup "$container_name"
fi


# Update log with end time, total execution time, and status
end_time=$(date -u +"%a %b %d %T UTC %Y")
total_exec_time=$(($(date -u +"%s") - $(date -u -d "$start_time" +"%s")))
status="Completed"

# Update the initial log content
sed -i -e "s/end_time=/end_time=$end_time/" -e "s/total_exec_time=/total_exec_time=$total_exec_time/" -e "s/status=In progress../status=$status/" "$log_file"

echo "Backup Job finished at $end_time - Total execution time: $total_exec_time"

# write notification to notifications center
write_notification "Backup Job ID: $NUMBER finished" "Accounts: $total_containers - Total execution time: $total_exec_time"
