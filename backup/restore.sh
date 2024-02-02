#!/bin/bash
################################################################################
# Script Name: restore.sh
# Description: Restore a full backup for a single user.
# Use: opencli backup-restore <backup_directory>
# Author: Stefan Pejcic
# Created: 08.10.2023
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


NUMBER=$1
PATH_ON_REMOTE_SERVER=$2
CONTAINER_NAME=$3

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
            WEBSERVER_FOR_USER_IN_DOCKER_CONTAINER="apache2"
            ;;
        --nginx-conf)
            WEBSERVER_CONF=true
            WEBSERVER_FOR_USER_IN_DOCKER_CONTAINER="nginx"
            ;;
        --mysql-conf)
            MYSQL_CONF=true
            ;;
        --mysql-data)
            MYSQL_DATA=true
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
if [ "$#" -lt 3 ]; then
    echo "Usage: opencli backup-restore <PATH_ON_DESTINATION> <USERNAME> [--all]"
    exit 1
fi


DEST_JSON_FILE="/usr/local/admin/backups/destinations/$NUMBER.json"

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
    echo "Destination is local. Restoring files locally to $directory folder"
    LOCAL=true
    REMOTE=false
else
    echo "Destination is not local. Restoring files from $dest_hostname"
    LOCAL=false
    REMOTE=true
fi


if [ "$DEBUG" = true ]; then
# backupjob json
echo "Status: $status"
echo "Destination: $destination"
# destination json
echo "Destination Hostname: $dest_hostname"
echo "Destination Password: $dest_password"
echo "Destination SSH Port: $dest_ssh_port"
echo "Destination SSH User: $dest_ssh_user"
echo "Destination SSH Key Path: $dest_ssh_key_path"
echo "Destination Storage Limit: $dest_storage_limit"
fi


local_temp_dir="/tmp/openpanel_restore_temp_dir/$CONTAINER_NAME"
mkdir -p $local_temp_dir

run_restore() {
    source_path_restore=$1
    local_destination=$2
    path_in_docker_container=$3
    
    #remove / from beginning
    source_path_restore="${source_path_restore#/}"
    source_path_restore="${source_path_restore%/}"
    local_destination="${local_destination#/}"

    # Check if the path_in_docker_container is provided
    if [ "$LOCAL" != true ]; then
        rsync -e "ssh -i $dest_ssh_key_path -p $dest_ssh_port" -r -p "$dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$source_path_restore" "$local_temp_dir"
        if [ "$DEBUG" = true ]; then
            echo "rsync command: rsync -e ssh -i $dest_ssh_key_path -p $dest_ssh_port -r -p $dest_ssh_user@$dest_hostname:$dest_destination_dir_name/$source_path_restore $local_temp_dir"
        fi
        if [ -z "$path_in_docker_container" ]; then
             cp -Lr "$local_temp_dir" "/$local_destination"
        else
            docker_source_path="${path_in_docker_container#docker:}"
            docker cp "$local_temp_dir/." "$docker_source_path/"
        fi
    else
        if [ -z "$path_in_docker_container" ]; then
             cp -Lr "$source_path_restore" "/$local_destination"
        else
            docker_source_path="${path_in_docker_container#docker:}"
            docker cp "$source_path_restore/." "$docker_source_path/"
        fi
    fi





}







# Main Restore Function
perform_restore_of_selected_files() {

    if [ "$FILES" = true ]; then
        local_destination="/home/$CONTAINER_NAME"
        remote_path_to_download="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/files/ ."
        run_restore "$remote_path_to_download" "$local_destination"
        # ovde untar na putanju
        ## BACA BAJLOVE U /HOME/$CONTAINER_NAME/$CONTAINER_NAME
        #bash restore.sh 1 20240131131407 nesto --files
    fi

    if [ "$ENTRYPOINT" = true ]; then
        path_in_docker_container="docker:$CONTAINER_NAME:/etc"
        run_restore "$PATH_ON_REMOTE_SERVER" "$local_destination" "$path_in_docker_container"
        #bash restore.sh 1 20240129005258 nesto --entrypoint
    fi

    if [ "$WEBSERVER_CONF" = true ]; then
        #export_webserver_main_conf_file
        remote_path_to_download="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/docker/nginx.conf"
        path_in_docker_container="docker:$CONTAINER_NAME:/etc/$WEBSERVER_FOR_USER_IN_DOCKER_CONTAINER/"
        local_destination="/etc/$WEBSERVER_FOR_USER_IN_DOCKER_CONTAINER/"
        run_restore "$remote_path_to_download" "$local_destination" "$path_in_docker_container"
        docker exec $CONTAINER_NAME bash -c "service $WEBSERVER_FOR_USER_IN_DOCKER_CONTAINER reload"
        #bash restore.sh 1 20240129005258 nesto --apache-conf | --nginx-conf
    fi

    if [ "$MYSQL_CONF" = true ]; then
        #backup_mysql_conf_file
        path_in_docker_container="docker:$CONTAINER_NAME:/etc/mysql/mysql.conf.d/"
        local_destination="/etc/mysql/mysql.conf.d/"
        run_restore "$PATH_ON_REMOTE_SERVER" "$local_destination" "$path_in_docker_container"
         #bash restore.sh 1 20240131131407/mysql/mysqld.cnf nesto --mysql-conf
        docker exec $CONTAINER_NAME bash -c "service mysql restart"
    fi

    if [ "$TIMEZONE" = true ]; then
        #backup_timezone
        path_in_docker_container="docker:$CONTAINER_NAME:/etc/timezone/"
        remote_path_to_download="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/timezone/"
        run_restore "$remote_path_to_download" "$path_in_docker_container"
        #bash restore.sh 1 20240131131407 nesto --timezone
    fi

    if [ "$PHP_VERSIONS" = true ]; then
        backup_php_versions_in_container
    fi

    if [ "$CRONTAB" = true ]; then
        #backup_crontab_for_root_user
		path_to_cron_file_in_backup="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/crons/ ."
        path_in_docker_container="docker:$CONTAINER_NAME:/var/spool/cron/crontabs/"
        local_destination="/var/spool/cron/crontabs/"
        run_restore "$path_to_cron_file_in_backup" "$local_destination" "$path_in_docker_container"       
        
        #bash restore.sh 1 20240202101012 pera2 --crontab
    fi

    if [ "$MYSQL_DATA" = true ]; then
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

    # Delete local_temp_dir after successful copy
    rm -r "$local_temp_dir"  
}




perform_restore_of_selected_files
