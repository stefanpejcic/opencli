#!/bin/bash
################################################################################
# Script Name: run.sh
# Description: Run backup job
# Usage: opencli backup-run ID [--debug|--force-run]
# Author: Stefan Pejcic
# Created: 26.01.2024
# Last Modified: 21.02.2025
# Company: openpanel.com
# Copyright (c) openpanel.com
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



# IP SERVERS
SCRIPT_PATH="/usr/local/admin/core/scripts/ip_servers.sh"
if [ -f "$SCRIPT_PATH" ]; then
    source "$SCRIPT_PATH"
else
    IP_SERVER_1=IP_SERVER_2=IP_SERVER_3="https://ip.openpanel.com"
fi





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
LOG_FILE="/var/log/openpanel/admin/notifications.log"

# Set a trap for CTRL+C to properly exit
trap "echo CTRL+C Pressed!; read -p 'Press Enter to exit...'; exit 1;" SIGINT SIGTERM

# Check if the correct number of command line arguments is provided
if [ "$#" -lt 1 ]; then
    echo "Usage: opencli backup-run <JOB_ID> [--force-run]"
    exit 1
fi

NUMBER=$1
FORCE_RUN=false

# Check if the --force-run flag is provided
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        --force-run)
            FORCE_RUN=true
            ;;            
    esac
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
if [[ "$dest_destination_dir_name" != /* ]]; then
  dest_destination_dir_name="/$dest_destination_dir_name"
fi
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
if [[ "$dest_hostname" == "localhost" || "$dest_hostname" == "127.0.0.1" || "$dest_hostname" == "$(curl --silent --max-time 2 -4 $IP_SERVER_1 || wget --timeout=2 -qO- $IP_SERVER_2 || curl --silent --max-time 2 -4 $IP_SERVER_3)" || "$dest_hostname" == "$(hostname)" ]]; then
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

    # if starts with "docker:" then cp from docker to local first
    if [[ "$source_path" == docker:* ]]; then
        docker_source_path="${source_path#docker:}"
        mkdir -p "$local_temp_dir/$container_name"
        if [ "$DEBUG" = true ]; then
            echo "DEBUG: Copying files from the docker container to workplace directory. Command used: docker cp  $docker_source_path $local_temp_dir/$container_name"
        fi
        docker cp "$docker_source_path" "$local_temp_dir/$container_name"
        source_path="$local_temp_dir/$container_name/$(basename "$docker_source_path")"
    fi

    if [[ "$source_path" == "/etc/letsencrypt/live/"* ]]; then
            cp -LTr "$source_path" "$local_temp_dir/$container_name"
            source_path="$local_temp_dir/$container_name"
    fi

    # REMOTE DESTINATION
    if [ "$LOCAL" != true ]; then

        # Step 1: Create the remote directory
        if ! timeout 10s ssh -i "$dest_ssh_key_path" -p "$dest_ssh_port" "$dest_ssh_user@$dest_hostname" "mkdir -p $dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"; then
            echo "SSH command timed out or failed."
        fi

        if [ "$DEBUG" = true ]; then
        echo "DEBUG: timeout 10s ssh -i $dest_ssh_key_path -p $dest_ssh_port $dest_ssh_user@$dest_hostname 'mkdir -p $dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path'"
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
        # LOCAL BACKUPS
        mkdir -p "$dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"

        if [ -d "$source_path" ]; then
            cp -LTr "$source_path" "/$dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"
        else
            cp -L "$source_path" "/$dest_destination_dir_name/$container_name/$TIMESTAMP/$destination_path"
            # TODO: cp: '/backup/sdjnjrz3/20240619142028/20240619142028.tar.gz' and '/backup/sdjnjrz3/20240619142028/20240619142028.tar.gz' are the same file
        fi
        
    fi

    # Clean up local temp directory if used
    [ -n "$local_temp_dir/$container_name" ] && rm -rf "$local_temp_dir/$container_name"
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
source /usr/local/opencli/db.sh








# Function to check command success and exit on failure
check_command_success() {
    if [ $? -ne 0 ]; then
        echo "Error: $1"
        exit 1
    fi
}


# Function to backup files
run_the_actual_generation_for_user() {

    copy_domain_zones() {
        local caddy_dir="/etc/openpanel/caddy/domains/"
        local caddy_suspended_dir="/etc/openpanel/caddy/suspended_domains/"
        local zones_dir="/etc/bind/zones/"
        local domain_names=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT domain_url FROM domains WHERE user_id='$user_id';" -N)
    
        
        for domain_name in $domain_names; do
          cp ${caddy_dir}${domain_name}.conf ${caddy_vhosts}${domain_name}.conf > /dev/null 2>&1 
          cp ${caddy_suspended_dir}${domain_name}.conf ${caddy_suspended_vhosts}${domain_name}.conf > /dev/null 2>&1 
          cp ${zones_dir}${domain_name}.zone ${dns_zones}${domain_name}.zone > /dev/null 2>&1 
        done        
    
    }
    
    
    
    export_user_data_from_database() {
    
        echo "Exporting user data from OpenPanel database.."
        user_id=$(mysql -e "SELECT id FROM users WHERE username='$username';" -N)
    
        if [ -z "$user_id" ]; then
            echo "ERROR: export_user_data_to_sql: User '$username' not found in the database."
            exit 1
        fi
    
        
    
    
        check_success() {
          if [ $? -eq 0 ]; then
            echo "- Exporting $1 from database successful"
          else
            echo "ERROR: Exporting $1 from database failed"
          fi
        }
    
    # Export User Data with INSERT INTO
    mysql --defaults-extra-file=$config_file -N -e "
        SELECT CONCAT('INSERT INTO panel.users (id, username, password, email, owner, user_domains, twofa_enabled, otp_secret, plan, registered_date, server, plan_id) VALUES (',
            id, ',', QUOTE(username), ',', QUOTE(password), ',', QUOTE(email), ',', QUOTE(owner), ',', QUOTE(user_domains), ',', twofa_enabled, ',', QUOTE(otp_secret), ',', QUOTE(plan), ',', IFNULL(QUOTE(registered_date), 'NULL'), ',', QUOTE(server), ',', plan_id, ');')
        FROM panel.users WHERE id = $user_id
    " > $openpanel_database/users.sql
    check_success "User data export"
    
    
    # Export User's Plan Data with INSERT INTO
    mysql --defaults-extra-file=$config_file -N -e "
        SELECT CONCAT('INSERT INTO panel.plans (id, name, description, domains_limit, websites_limit, email_limit, ftp_limit, disk_limit, inodes_limit, db_limit, cpu, ram, docker_image, bandwidth) VALUES (',
            p.id, ',', QUOTE(p.name), ',', QUOTE(p.description), ',', p.domains_limit, ',', p.websites_limit, ',', p.email_limit, ',', p.ftp_limit, ',', QUOTE(p.disk_limit), ',', p.inodes_limit, ',', p.db_limit, ',', QUOTE(p.cpu), ',', QUOTE(p.ram), ',', QUOTE(p.docker_image), ',', p.bandwidth, ');')
        FROM panel.plans p
        JOIN panel.users u ON u.plan_id = p.id
        WHERE u.id = $user_id
    " > $openpanel_database/plans.sql
    check_success "Plan data export"
    
    
    # Export Domains Data for User with INSERT INTO
    mysql --defaults-extra-file=$config_file -N -e "
        SELECT CONCAT('INSERT INTO panel.domains (domain_id, user_id, domain_url, docroot, php_version) VALUES (',
            domain_id, ',', user_id, ',', QUOTE(domain_url), ',', QUOTE(docroot), ',', QUOTE(php_version), ');')
        FROM panel.domains WHERE user_id = $user_id
    " > $openpanel_database/domains.sql
    check_success "Domains data export"
    
    
    # Export Sites Data for User with INSERT INTO
    mysql --defaults-extra-file=$config_file -N -e "
        SELECT CONCAT('INSERT INTO panel.sites (id, domain_id, site_name, admin_email, version, created_date, type, ports, path) VALUES (',
            s.id, ',', s.domain_id, ',', QUOTE(s.site_name), ',', QUOTE(s.admin_email), ',', QUOTE(s.version), ',', QUOTE(s.created_date), ',', QUOTE(s.type), ',', s.ports, ',', QUOTE(s.path), ');')
        FROM panel.sites s
        JOIN panel.domains d ON s.domain_id = d.domain_id
        WHERE d.user_id = $user_id
    " > $openpanel_database/sites.sql
    check_success "Sites data export"
    
    
        # no need for sessions!
    
        echo ""
        echo "User '$username' data exported to $openpanel_database successfully."
    }
    
    
    # get user ID from the database
    get_user_info() {
        local user="$1"
        local query="SELECT id, server FROM users WHERE username = '${user}';"
        
        # Retrieve both id and context
        user_info=$(mysql -se "$query")
        
        # Extract user_id and context from the result
        user_id=$(echo "$user_info" | awk '{print $1}')
        context=$(echo "$user_info" | awk '{print $2}')
        
        echo "$user_id,$context"
    }
    
    
    
    mkdirs() {
    
      apparmor_dir="/home/"$context"/apparmor/"
      openpanel_core="/home/"$context"/op_core/"
      openpanel_database="/home/"$context"/op_db/"
      caddy_vhosts="/home/"$context"/caddy/"
      dns_zones="/home/"$context"/dns/"  
      caddy_suspended_vhosts="/home/"$context"/caddy_suspended/"
      
      mkdir -p $apparmor_dir $openpanel_core $openpanel_database $caddy_vhosts $dns_zones $caddy_suspended_vhosts
    
    }
    
    
    tar_everything() {
      echo "Creating archive for all user files.."
      # home files
      archive_name="backup_${username}_$(date +%Y%m%d_%H%M%S).tar.gz"
      tar czpf "$archive_name" -C /home/"$context" --exclude='*/.sock' .
    }
    
    
    copy_files_temporary_to_user_home() {
    
      # database
      export_user_data_from_database
      
      # apparmor profile
      echo "Collectiong AppArmor profile.."
      cp /etc/apparmor.d/home.$context.bin.rootlesskit $apparmor_dir
      # https://media2.giphy.com/media/v1.Y2lkPTc5MGI3NjExYWx1MjY4YXB0YTRla3dlazMxYmhkM3k2MWV0eDVsNDUxcHQ1aW9jNyZlcD12MV9pbnRlcm5hbF9naWZfYnlfaWQmY3Q9Zw/uNE1fngZuYhIQ/giphy.gif
      #cp /etc/apparmor.d/$(echo /home/pejcic/bin/rootlesskit | sed -e s@^/@@ -e s@/@.@g) $apparmor_dir
    
      # core panel data
      echo "Collectiong core OpenPanel files.."
      cp -r /etc/openpanel/openpanel/core/users/$context/  $openpanel_core
    
      # caddy and bind9
      echo "Collectiong DNS zones and Caddy files.."
      copy_domain_zones
      
      echo "Collectiong Docker context information.."
      echo "$context" > /home/$context/context
    
    }
    
    
    clean_tmp_files() {
        echo "Cleaning up temporary files.."
        rm -rf $apparmor_dir $openpanel_core ${caddy_vhosts} ${caddy_suspended_vhosts} ${dns_zones} #> /dev/null 2>&1 
    
    }
    
    
    result=$(get_user_info "$username")
    user_id=$(echo "$result" | cut -d',' -f1)
    context=$(echo "$result" | cut -d',' -f2)
    mkdirs
    copy_files_temporary_to_user_home
    tar_everything
    clean_tmp_files



    local source_dir="/home/$username"

    echo "Processing $source_dir"

    copy_files "$archive_name" "/"
}



# Main Backup Function
perform_backup() {
    type="full"
    username="$1"
    log_user "$username" "Backup started."

    BACKUP_DIR="/backup/$username/$TIMESTAMP"

    mkdir -p "$BACKUP_DIR"

    run_the_actual_generation_for_user

    sed -i -e "s/type=/type=$type/" "$user_index_file"
    log_user "$username" "Backup completed successfully."

}




# log to the main log file for the job
log_dir="/var/log/openpanel/admin/backups/$NUMBER"
mkdir -p $log_dir
highest_number=$(ls "$log_dir"/*.log 2>/dev/null | sed 's/[^0-9]*//g' | sort -n | tail -n 1)
# If no logs are found, start from 1
if [ -z "$highest_number" ]; then
  next_number=1
else
  next_number=$((highest_number + 1))
fi
log_file="$log_dir/$next_number.log"


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
    mkdir -p "/etc/openpanel/openadmin/config/backups/index/$NUMBER/$container_name/"
    user_index_file="/etc/openpanel/openadmin/config/backups/index/$NUMBER/$container_name/$TIMESTAMP.index"
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

    # TODO: calculate total du for this backup and store it in index file!
    
    sed -i -e "s/end_time=/end_time=$end_backup_for_user_time/" -e "s/total_exec_time=/total_exec_time=$total_exec_time_spent_for_user/" -e "s/status=.*/status=Completed/" "$user_index_file"
        echo ""
        echo "Backup completed for user: $container_name"                                          
        retention_check_and_delete_oldest_backup
        #empty_line    
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

  
    backup_openpanel_conf() {
        echo ""
        echo "## Backing up OpenPanel configuration files."
        echo ""
        mkdir -p ${CONF_DESTINATION_DIR}/openpanel
        find /etc/openpanel/
        cp -r /etc/openpanel ${CONF_DESTINATION_DIR}/openpanel
        #
       
        docker cp openpanel:/usr/local/panel/translations/ ${CONF_DESTINATION_DIR}/openpanel/translations >/dev/null 2>&1
        # here also should do the custom files for panel!
    }

    backup_named_conf() {
        echo ""
        echo "## Backing up BIND9 service configuration and DNS zones for all domains.."
        echo ""
        find /etc/bind/
        cp -r /etc/bind ${CONF_DESTINATION_DIR}/bind
    }
    
    # firewall rules
    backup_etc_ufw_and_csf(){
        echo ""
        echo "## Backing up firewall rules."
        echo ""
        if command -v csf >/dev/null 2>&1; then
            echo "ConfigServer Firewall detected, copying /etc/csf/"
            cp -r /etc/csf ${CONF_DESTINATION_DIR}/csf
        elif command -v ufw >/dev/null 2>&1; then
            echo "Uncomplicated Firewall detected, copying /etc/ufw/"
            cp -r /etc/ufw ${CONF_DESTINATION_DIR}/ufw
        else
            echo "Warning: Neither CSF nor UFW are installed, not backing firewall configuration."
        fi
        
    }
    

   
    # docker conf
    backup_docker_daemon(){
        echo ""
        echo "## Backing up Docker configuration."
        echo ""
        cat /etc/docker/daemon.json
        cp /etc/docker/daemon.json ${CONF_DESTINATION_DIR}/docker_daemon.json
        # this is symlink, check if it follows
    }

    
    
    # panel db
    backup_mysql_panel_database() {
        echo ""
        echo "## Backing up MySQL database for OpenPanel."
        echo ""
        mkdir -p ${CONF_DESTINATION_DIR}/mysql_data/
        
        MY_CNF="/etc/my.cnf"
        DB_NAME=$(grep -oP '(?<=^database = ).*' "$MY_CNF")
        DB_PASSWORD=$(grep -oP '(?<=^password = ).*' "$MY_CNF")
        DB_USER="root"
        CONTAINER_NAME="openpanel_mysql"

        # Check if the container is running
        if docker ps --filter "name=$CONTAINER_NAME" --filter "status=running" | grep -q "$CONTAINER_NAME"; then
            echo "MySQL container is running, generating sql file export with mysqldump..."
            docker exec $CONTAINER_NAME mysqldump -u "$DB_USER" -p"$DB_PASSWORD" "$DB_NAME" > "${CONF_DESTINATION_DIR}/mysql_data/database.sql"
            # Check if the dump was successful
            if [ $? -eq 0 ]; then
                echo "Successfully exported database."
                # Get the size of the backup file
                BACKUP_SIZE=$(du -sh "${CONF_DESTINATION_DIR}/mysql_data/database.sql" | cut -f1)
                # Display the size of the backup
                echo "Database backup file size: $BACKUP_SIZE"
            else
                echo "ERROR: Database backup failed."
            fi
        else
            echo "MySQL container is not running, generating files backup using the docker volume..."
            docker run --rm --volumes-from $CONTAINER_NAME -v ${CONF_DESTINATION_DIR}/mysql_data:/backup ubuntu tar czvf /backup/mysql_volume_data.tar.gz /var/lib/mysql
        fi
               
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
        find /etc/nginx/sites-available -type f -name \*.conf
        cp -r /etc/nginx/sites-available ${NGINX_DESTINATION_DIR}sites-available
    }

    # /root/docker-compose.yml
    backup_docker_compose() {
        echo ""
        echo "## Backing up docker compose file for OpenPanel, Nignx and MySQL."
        echo ""
        COMPOSE_DESTINATION_DIR="${CONF_DESTINATION_DIR}/compose/"
        mkdir -p $COMPOSE_DESTINATION_DIR
        find /root/docker-compose.yml
        cp /root/docker-compose.yml ${COMPOSE_DESTINATION_DIR}docker-compose.yml
        cp /root/.env ${COMPOSE_DESTINATION_DIR}.env
        cp /etc/my.cnf ${COMPOSE_DESTINATION_DIR}my.cnf
    }


    # backup server data only
    echo "------------------------------------------------------------------------"
    backup_openpanel_conf
    echo "------------------------------------------------------------------------"
    backup_mysql_panel_database
    echo "------------------------------------------------------------------------"
    backup_nginx_data
    echo "------------------------------------------------------------------------"
    backup_docker_daemon
    echo "------------------------------------------------------------------------"
    backup_etc_ufw_and_csf
    echo "------------------------------------------------------------------------"
    backup_named_conf
    echo "------------------------------------------------------------------------"
    backup_docker_compose


copy_files_server_conf_only $CONF_DESTINATION_DIR $dest_destination_dir_name


type="OpenPanel configuration"
sed -i -e "s/type=.*/type=${type}/" "$log_file"
    
}

        empty_line() {
            echo ""
            echo "------------------------------------------------------------------------"
            echo""
        }


        

# MAIN FUNCTION FOR PARTIAL BACKUP OF INDIVIDUAL ACCOUNTS
run_backup_for_user_data() {
    container_count=0

    output=$(opencli user-list --json)
    
    # Check if the output is empty or if MySQL is not running
    if [ -z "$output" ] || [ "$output" = "null" ]; then
        echo "ERROR: No users found in the database or MySQL is not running."
        return
    fi

    # Get the total number of valid containers (excluding suspended ones)
    total_containers=$(echo "$output" | jq -c '.[] | select(.username | test("^[^_]*$"))' | wc -l)
    echo "Total active users: $total_containers"

    # Loop through each user
    user_list=($(echo "$output" | jq -r '.[] | .username'))

    # Loop through each user using a for loop
    for container_name in "${user_list[@]}"; do
        ((container_count++))

        empty_line
        echo "Starting backup for user: $container_name ($container_count/$total_containers)"
        
        check_if_suspended_user_or_in_exclude_list() {
            if [[ "$container_name" =~ [_] ]]; then
                echo "Skipping backup for suspended user: $container_name"
                continue
            fi
    
            excluded_file="/usr/local/opencli/helpers/excluded_from_backups.txt"

            if [ -f "$excluded_file" ]; then
                if grep -Fxq "$container_name" "$excluded_file"; then
                    echo "Skipping backup for excluded user: $container_name"
                    continue
                fi
            fi
        }

        get_current_number_of_backups_for_user() {
            user_indexes="/etc/openpanel/openadmin/config/backups/index/$NUMBER/$container_name/"
            
            # Check if the directory exists
            if [ -d "$user_indexes" ]; then
                number_of_backups_in_this_job_that_user_has=$(find "$user_indexes" -type f -name "*.index" | wc -l)
            else
                number_of_backups_in_this_job_that_user_has=0
            fi
        }



        retention_check_and_delete_oldest_backup() {
            # Compare with retention
            if [ "$number_of_backups_in_this_job_that_user_has" -ge "$retention" ]; then
                echo "User has a total of $number_of_backups_in_this_job_that_user_has backups, reached retention of $retention."
                #SED ERROR: TODO: retention_for_user_files_delete_oldest_files_for_job_id
            else
                echo "User has a total of $number_of_backups_in_this_job_that_user_has backups, retention limit of $retention is not reached."
            fi
        }
        
        check_if_suspended_user_or_in_exclude_list                                                 # should we run the backup?
        get_current_number_of_backups_for_user                                                     # count existing backups
        backup_for_user_started                                                                    # write log per user
        perform_backup "$container_name"                                                           # execute actual backup for the user
        backup_for_user_finished                                                                   # complete log for user
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
