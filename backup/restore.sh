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
#petar
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

ssh_list_sql_files() {
    source_path_restore=$1
    source_path_restore="${source_path_restore#/}"
    ssh -i "$dest_ssh_key_path" -p "$dest_ssh_port" "$dest_ssh_user@$dest_hostname" "ls -p \"$dest_destination_dir_name/$source_path_restore\" | awk -F'.sql' '{print \$1}'"
    }





# Main Restore Function
perform_restore_of_selected_files() {

    if [ "$FILES" = true ]; then
        local_destination="/home/$CONTAINER_NAME"
        remote_path_to_download="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/files/ ."
        run_restore "$remote_path_to_download" "$local_destination"
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
        
        #bash restore.sh 1 20240129005258 nesto --apache-conf | --nginx-conf

	if [ "$DEBUG" = true ]; then
	docker exec $CONTAINER_NAME bash -c "service $WEBSERVER_FOR_USER_IN_DOCKER_CONTAINER reload"
 	else
	docker exec $CONTAINER_NAME bash -c "service $WEBSERVER_FOR_USER_IN_DOCKER_CONTAINER reload" > /dev/null 2>&1
 	fi
    fi

    if [ "$MYSQL_CONF" = true ]; then
        #backup_mysql_conf_file
        path_in_docker_container="docker:$CONTAINER_NAME:/etc/mysql/mysql.conf.d/"
        local_destination="/etc/mysql/mysql.conf.d/"
        run_restore "$PATH_ON_REMOTE_SERVER" "$local_destination" "$path_in_docker_container"
        
	#bash restore.sh 1 20240131131407/mysql/mysqld.cnf nesto --mysql-conf
        
	if [ "$DEBUG" = true ]; then
	docker exec $CONTAINER_NAME bash -c "service mysql restart"
 	else
	docker exec $CONTAINER_NAME bash -c "service mysql restart" > /dev/null 2>&1
 	fi
 
    fi

    if [ "$TIMEZONE" = true ]; then
        #backup_timezone
        path_in_docker_container="docker:$CONTAINER_NAME:/etc/"
        remote_path_to_download="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/timezone/."
	local_destination="/"
        run_restore "$remote_path_to_download" "$local_destination" "$path_in_docker_container"
        #bash restore.sh 1 20240202102435 pera2 --timezone
    fi

    if [ "$PHP_VERSIONS" = true ]; then
          remote_path_to_download="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/php/php_versions.txt"
          local_destination="/php"

         run_restore "$remote_path_to_download" "$local_destination"


        version_numbers=$(cat /$local_destination/php_versions.txt | grep -oP 'php\d+\.\d+' | sed 's/php//')
        echo $version_numbers
        for version in $version_numbers; do 
            status=$(docker exec "$CONTAINER_NAME" bash -c "service php$version-fpm status")
                if [[ $status == *"unrecognized service"* ]]; then
                    echo "php$version-fpm is an unrecognized service"
                    opencli php-install_php_version "$CONTAINER_NAME" "$version"
                    docker exec "$CONTAINER_NAME" bash -c "service php$version-fpm start"
                else
                    echo "$version-fpm is already installed. Restoring php.ini file /etc/php/$version/fpm/php-fpm.conf"

		    path_in_docker_container="docker:$CONTAINER_NAME:/etc/php/$version/fpm/"
                    local_destination="/tmp/openpanel_restore_temp_dir/$CONTAINER_NAME/php"
                    remote_path_to_download="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/php/$version/"
                    run_restore "$remote_path_to_download" "$local_destination" "$path_in_docker_container"
                    
                    docker exec "$CONTAINER_NAME" bash -c "service php$version-fpm restart"
                fi
        done
    fi

    if [ "$CRONTAB" = true ]; then
        #backup_crontab_for_root_user
	path_to_cron_file_in_backup="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/crons/."
        path_in_docker_container="docker:$CONTAINER_NAME:/var/spool/cron/crontabs/"
        local_destination="/var/spool/cron/crontabs/"
        run_restore "$path_to_cron_file_in_backup" "$local_destination" "$path_in_docker_container"  

        #bash restore.sh 1 20240202101012 pera2 --crontab

	if [ "$DEBUG" = true ]; then
	docker exec $CONTAINER_NAME bash -c "chown $CONTAINER_NAME:$CONTAINER_NAME $local_destination/$CONTAINER_NAME"
 	docker exec $CONTAINER_NAME bash -c "service crond restart"
 	else
	docker exec $CONTAINER_NAME bash -c "chown $CONTAINER_NAME:$CONTAINER_NAME $local_destination/$CONTAINER_NAME" > /dev/null 2>&1
 	docker exec $CONTAINER_NAME bash -c "service crond restart" > /dev/null 2>&1
 	fi
    fi

#petar
    if [ "$MYSQL_DATA" = true ]; then
        remote_path_to_download="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER"
        userpath="$remote_path_to_download/mysql/users"
        dbpath="$remote_path_to_download/mysql/databases"
        local_destination="/usr/local/panel/core/users/$CONTAINER_NAME/mysql/"
	    path_in_docker_container="docker:$CONTAINER_NAME:/tmp/"

        Mstatus=$(docker exec "$CONTAINER_NAME" bash -c "service mysql status | grep -i copyright")
        if [[ $Mstatus ]]; then
            usr_files=$(ssh_list_sql_files $userpath)
            db_files=$(ssh_list_sql_files $dbpath)

            echo $usr_files
            echo $db_files

                for usr in $usr_files; do 

                run_restore "$userpath/$usr.sql" $local_destination $path_in_docker_container
                
                done
        else
        echo "Mysql is not running!"
       fi
       
    fi
   


    if [ "$USER_DATA" = true ]; then
        export_user_data_from_database
    fi

    if [ "$CORE_USERS" = true ]; then
        local_destination="/usr/local/panel/core/users/$CONTAINER_NAME/"
        remote_path_to_download="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/core/."
        run_restore "$remote_path_to_download" "$local_destination"
	#bash restore.sh 1 20240202115324 nesto --core-users
    fi

    if [ "$STATS_USERS" = true ]; then
        local_destination="/usr/local/panel/core/stats/$CONTAINER_NAME/"
        remote_path_to_download="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/stats/."
        run_restore "$remote_path_to_download" "$local_destination"
	#bash restore.sh 1 20240202115324 nesto --stats-users
 
    fi

    if [ "$APACHE_SSL_CONF" = true ]; then
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

 	#in container
	path_in_docker_container="docker:$CONTAINER_NAME:/etc/$ws/sites-available/"
	path_to_domain_files_in_backup="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/$ws/container/sites-available/."
        local_destination="/$ws/sites-available/"
        run_restore "$path_in_docker_container" "$local_destination" "$path_to_domain_files_in_backup"
	docker exec -it bash -c "service $ws reload"
	# on host server
	path_to_domain_files_in_backup="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/$ws/sites-available/."
        local_destination="/etc/nginx/sites-available/"
        run_restore "$path_in_docker_container" "$local_destination"

	# ssl certificates
 	# todo: check if symlinks are repalced, then check if certbot renews them properly
	path_to_domain_files_in_backup="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/ssl/."
        local_destination="/etc/letsencrypt/live/"
        run_restore "$path_to_domain_files_in_backup" "$local_destination"
	service nginx reload

	# dns zones
 	# todo: check and add in named.conf.local each domain after adding its zone file!
	path_to_domain_files_in_backup="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/dns/."
        local_destination="/etc/bind/zones/"
        run_restore "$path_to_domain_files_in_backup" "$local_destination"
	service named reload
 
    fi

    if [ "$DOMAIN_ACCESS_REPORTS" = true ]; then
        local_destination="/var/log/nginx/stats/"
        remote_path_to_download="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/nginx/stats/."
        run_restore "$remote_path_to_download" "$local_destination"
	#bash restore.sh 1 20240202115324 nesto --domain-access-reports 
    fi

    if [ "$SSH_PASS" = true ]; then
        #backup_ssh_conf_and_pass
	path_to_ssh_shadow_in_backup="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/docker/shadow"
	path_to_ssh_passwd_in_backup="/$CONTAINER_NAME/$PATH_ON_REMOTE_SERVER/docker/passwd"
        path_in_docker_container="docker:$CONTAINER_NAME:/etc/"
        local_destination="/etc/"
	
	if [ "$DEBUG" = true ]; then
        run_restore "$path_to_ssh_shadow_in_backup" "$local_destination" "$path_in_docker_container"  
	run_restore "$path_to_ssh_passwd_in_backup" "$local_destination" "$path_in_docker_container"  
 	docker exec $CONTAINER_NAME bash -c "service ssh restart"
 	else
        run_restore "$path_to_ssh_shadow_in_backup" "$local_destination" "$path_in_docker_container"  > /dev/null 2>&1 
	run_restore "$path_to_ssh_passwd_in_backup" "$local_destination" "$path_in_docker_container"   > /dev/null 2>&1
 	docker exec $CONTAINER_NAME bash -c "service ssh restart" > /dev/null 2>&1
 	fi

    fi

    # Delete local_temp_dir after successful copy
    rm -r "$local_temp_dir"  
}




perform_restore_of_selected_files
