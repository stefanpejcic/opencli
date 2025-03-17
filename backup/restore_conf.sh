#!/bin/bash
################################################################################
# Script Name: restore_conf.sh
# Description: Restore OpenPanel server configuration.
# Use: opencli backup-restore_conf <destination_id> <backup_directory_on_destination>
# Author: Stefan Pejcic
# Created:28.08.2024
# Last Modified: 17.03.2025
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

NUMBER=$1
PATH_ON_REMOTE_SERVER=$2
DEBUG=false
LOG_FILE="/var/log/openpanel/admin/notifications.log"
log_dir="/var/log/openpanel/admin/backups/$NUMBER"
mkdir -p $log_dir
log_file="$log_dir/$(( $(ls -l "$log_dir" | grep -c '^-' ) + 1 )).log"
process_id=$$
start_time=$(date -u +"%a %b %d %T UTC %Y")



# IP SERVERS
SCRIPT_PATH="/usr/local/admin/core/scripts/ip_servers.sh"
if [ -f "$SCRIPT_PATH" ]; then
    source "$SCRIPT_PATH"
else
    IP_SERVER_1=IP_SERVER_2=IP_SERVER_3="https://ip.openpanel.com"
fi


# Parse optional flags to skip specific actions
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
    esac
done


# Check if the correct number of command line arguments is provided
if [ "$#" -lt 2 ]; then
    echo "Usage: opencli backup-restore_conf <DESTINATION_ID> <DIRECTORY_ON_DESTINATION>"
    exit 1
fi





DEST_JSON_FILE="/etc/openpanel/openadmin/config/backups/destinations/$NUMBER.json"

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




initial_log_content="process_id=$process_id
start_time=$start_time
end_time=
total_exec_time=
status=In progress.."



# Create log file and write initial content
echo -e "$initial_log_content" > "$log_file"

# Redirect all output to the log file
exec > >(tee -a "$log_file") 2>&1





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

ensure_jq_installed
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
if [[ "$dest_hostname" == "localhost" || "$dest_hostname" == "127.0.0.1" || "$dest_hostname" == "$(curl --silent --max-time 2 -4 $IP_SERVER_1 || wget --timeout=2 -qO- $IP_SERVER_2 || curl --silent --max-time 2 -4 $IP_SERVER_3)" || "$dest_hostname" == "$(hostname)" ]]; then
    echo "Destination is local. Restoring files locally from $dest_destination_dir_name folder"
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

# MAIN RESTORE FUNCTION USED TO DOWNLOAD REMOTE OR COPY LOCAL FILES TO SERVER OR INSIDE MYSQL CONTAINER
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


















perform_restore_of_all_configuration(){


  restore_openpanel_conf(){
        echo ""
        echo "## Restoring OpenPanel configuration."
        echo ""
        local_destination="/etc/openpanel/"
        mkdir -p $local_destination
        echo "Restoring $local_destination"
        remote_path_to_download="/$PATH_ON_REMOTE_SERVER/openpanel/openpanel/."
        run_restore "$remote_path_to_download" "$local_destination"

        find /etc/openpanel/

  }

  restore_mysql_panel_database(){
        echo ""
        echo "## Restoring MySQL database."
        echo ""
        local_destination="/tmp"
        ##### from volume data is in: /mysql_data/mysql_volume_data.tar.gz
        remote_path_to_download="/$PATH_ON_REMOTE_SERVER/mysql_data/database.sql"
        run_restore "$remote_path_to_download" "$local_destination"
        
        MY_CNF="/etc/my.cnf"
        DB_NAME=$(grep -oP '(?<=^database = ).*' "$MY_CNF")
        DB_PASSWORD=$(grep -oP '(?<=^password = ).*' "$MY_CNF")
        DB_USER="root"
        CONTAINER_NAME="openpanel_mysql"


        docker start $CONTAINER_NAME
        
        # Check if the container is running
        if docker ps --filter "name=$CONTAINER_NAME" --filter "status=running" | grep -q "$CONTAINER_NAME"; then
            echo "MySQL container is running, importing sql file from the backup..."
            
            docker cp /tmp/database.sql $CONTAINER_NAME:/tmp/database.sql
            docker exec $CONTAINER_NAME mysql -uroot -p$DB_PASSWORD $DB_NAME < /tmp/database.sql
            rm -rf /tmp/database.sql && docker exec $CONTAINER_NAME sh -c "rm -rf /tmp/database.sql"       
        else
            echo "MySQL container is not running, files need to be imported manually in docker volume and attached to the container."
        fi


        echo "Testing connection to mysql and reading restored data:"
        get_row_count() {
          local table_name=$1
          local row_count=$(mysql -se "SELECT COUNT(*) FROM $table_name;")
          echo "- $row_count ${table_name^} "
        }
        
        # List total rows in each table
        get_row_count "users"
        get_row_count "plans"
        get_row_count "domains"
        get_row_count "sites"

  }

  restore_nginx_data(){
        echo ""
        echo "## Restoring Nginx configuration for domains."
        echo ""
        
        local_destination="/etc/nginx/sites-available/"
        link_destination="/etc/nginx/sites-enabled/"
        mkdir -p $local_destination
        mkdir -p $link_destination
        
        echo "Restoring $local_destination"
        remote_path_to_download="/$PATH_ON_REMOTE_SERVER/nginx/sites-available/."
        run_restore "$remote_path_to_download" "$local_destination"
                
        echo "Creating symlinks from /$local_destination to $link_destination"        
        if compgen -G "/${local_destination}*.conf" > /dev/null; then
            for conf_file in "/$local_destination"*.conf; do
                filename=$(basename "$conf_file")
                ln -sf "$conf_file" "$link_destination$filename"
                echo "$link_destination$filename > $conf_file"
            done
        else
            echo "No .conf files found in /$local_destination"
        fi
        
        echo "Recreatting /etc/hosts on the server"
        opencli server-recreate_hosts
        grep docker-container /etc/hosts
        
        echo "Reloading Nginx configuration"
        timeout 10 docker exec nginx sh -c "nginx -t && nginx -s reload" # if container is restarting we need a timeout
  }


  restore_docker_daemon(){
        echo ""
        echo "## Restoring Docker configuration."
        echo ""
        local_destination="/etc/docker/daemon.json"
        remote_path_to_download="/$PATH_ON_REMOTE_SERVER/docker_daemon.json"
        run_restore "$remote_path_to_download" "$local_destination"

        service docker restart
  }


restore_etc_ufw_and_csf(){
        echo ""
        echo "## Restoring firewall rules."
        echo ""
        if command -v csf >/dev/null 2>&1; then
            echo "ConfigServer Firewall detected, restoring /etc/csf/"
            
            local_destination="/etc/csf"
            remote_path_to_download="/$PATH_ON_REMOTE_SERVER/csf/."
            run_restore "$remote_path_to_download" "$local_destination"
            echo "Restarting CSF"
            csf -ra
        
        elif command -v ufw >/dev/null 2>&1; then
            echo "Uncomplicated Firewall detected, restoring /etc/ufw/"
            local_destination="/etc/ufw"
            remote_path_to_download="/$PATH_ON_REMOTE_SERVER/ufw/."
            run_restore "$remote_path_to_download" "$local_destination"

            echo "Restarting UFW"
             ufw reload
        else
            echo "Warning: Neither CSF nor UFW are installed, not restoring any firewall configuration."
        fi
  }


  restore_named_conf(){
        echo ""
        echo "## Restoring DNS zones and configuration."
        echo ""
            local_destination="/etc/bind"
            remote_path_to_download="/$PATH_ON_REMOTE_SERVER/bind/."
            run_restore "$remote_path_to_download" "$local_destination"
            echo "DNS zones after restore:"
            find /etc/bind/zones/
            echo "Restarting BIN9 container"
            cd /root && docker compose up -d bind9
  }



  restore_docker_compose(){
        echo ""
        echo "## Restoring docker compose."
        echo ""
            local_destination="/root/docker-compose.yml"
            remote_path_to_download="/$PATH_ON_REMOTE_SERVER/compose/docker-compose.yml"
            run_restore "$remote_path_to_download" "$local_destination"

            local_destination="/etc/my.cnf"
            remote_path_to_download="/$PATH_ON_REMOTE_SERVER/compose/my.cnf"
            run_restore "$remote_path_to_download" "$local_destination"

            local_destination="/root/.env"
            remote_path_to_download="/$PATH_ON_REMOTE_SERVER/compose/.env"
            run_restore "$remote_path_to_download" "$local_destination"

      cd /root
      docker compose up -d openpanel nginx bind9 certbot      
  }

    # backup server data only
    restore_openpanel_conf # restore my.cnf before mysql!
    echo "------------------------------------------------------------------------"
    restore_mysql_panel_database
    echo "------------------------------------------------------------------------"
    restore_docker_daemon
    echo "------------------------------------------------------------------------"
    restore_nginx_data
    echo "------------------------------------------------------------------------"
    restore_etc_ufw_and_csf
    echo "------------------------------------------------------------------------"
    restore_named_conf
    echo "------------------------------------------------------------------------"    
    restore_docker_compose #at the end, restarts entire stack
}




# MAIN
    echo ""
    echo "------------------------------------------------------------------------"
    echo ""
    echo "STARTING SYSTEM RESTORE FROM $PATH_ON_REMOTE_SERVER"
    echo ""
    
    perform_restore_of_all_configuration
    
    # Update log with end time, total execution time, and status
    end_time=$(date -u +"%a %b %d %T UTC %Y")
    total_exec_time=$(($(date -u +"%s") - $(date -u -d "$start_time" +"%s")))
    status="Completed"

        echo ""
        echo "------------------------------------------------------------------------"
        echo ""
        echo "Restore Job finished at $end_time - Total execution time: $total_exec_time"
        echo ""
        echo ""


  sed -i -e "s/end_time=/end_time=$end_time/" -e "s/total_exec_time=/total_exec_time=$total_exec_time/" -e "s/status=In progress../status=$status/" "$log_file"
  write_notification "System Restore job from $PATH_ON_REMOTE_SERVER finished" "Total execution time: $total_exec_time"
