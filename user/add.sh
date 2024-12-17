#!/bin/bash
################################################################################
# Script Name: user/add.sh
# Description: Create a new user with the provided plan_name.
# Usage: opencli user-add <USERNAME> <PASSWORD|generate> <EMAIL> "<PLAN_NAME>" [--send-email] [--debug]
# Docs: https://docs.openpanel.co/docs/admin/scripts/users#add-user
# Author: Stefan Pejcic
# Created: 01.10.2023
# Last Modified: 17.12.2024
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

# Constants
FORBIDDEN_USERNAMES_FILE="/etc/openpanel/openadmin/config/forbidden_usernames.txt"
DB_CONFIG_FILE="/usr/local/admin/scripts/db.sh"
SEND_EMAIL_FILE="/usr/local/admin/scripts/send_mail"
PANEL_CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"




if [ "$#" -lt 4 ] || [ "$#" -gt 6 ]; then
    echo "Usage: opencli user-add <username> <password|generate> <email> <plan_name> [--send-email] [--debug]"
    exit 1
fi

username="${1,,}"
password="$2"
email="$3"
plan_name="$4"
DEBUG=false             # Default value for DEBUG
SEND_EMAIL=false        # dont send email by default


# lowercase and replace spaces with _
docker_network_name="${plan_name// /_}"  # Replace spaces with underscores
docker_network_name="${docker_network_name,,}"  # Convert to lowercase


if [ "$5" = "--debug" ] || [ "$6" = "--debug" ]; then
    DEBUG=true
fi

if [ "$5" = "--send-email" ] || [ "$6" = "--send-email" ]; then
    SEND_EMAIL=true
fi

log() {
    if $DEBUG; then
        echo "$1"
    fi
}



set_docker_context_for_container() {
    log "Checking if clustering is enabled and which node to use for the new container"
    default_context=$(grep "^default_context=" "$PANEL_CONFIG_FILE" | cut -d'=' -f2-)
    
    if [ -z "$default_context" ] || [ "$default_context" == "default" ]; then
        server_name='default'                                                                                   # use as fallback
        context_flag=""                                                                                         # empty
    else
        server_name="$default_context"                                                                          # use the context name from the file
        context_flag="--context $server_name"                                                                   # add to all docker exec commands
        context_info=$(docker context ls --format '{{.Name}} {{.DockerEndpoint}}' | grep "$server_name")  # get ipv4 and use it for all ssh commands
    
        if [ -n "$context_info" ]; then
            endpoint=$(echo "$context_info" | awk '{print $2}')
            if [[ "$endpoint" == ssh://* ]]; then
                node_ip_address=$(echo "$endpoint" | cut -d'@' -f2 | cut -d':' -f1)
            else
                echo "ERROR: valid IPv4 address for context $server_name not found!"
                echo "       User container is located on node $server_name and there is a docker context with the same name but it has no valid IPv4 in the endpoint."
                echo "       Make sure that the docker context named $server_nam has valid IPv4 address in format: 'SERVER ssh://USERNAME@IPV4' and that you can establish ssh connection using those credentials."
                exit 1
            fi
        else
            echo "ERROR: docker context with name $server_name does not exist!"
            echo "       User container is located on node $server_name but there is no docker context with that name."
            echo "       Make sure that the docker context exists and is available via 'docker context ls' command."
            exit 1
        fi



        
    fi       

    if [ -n "$node_ip_address" ]; then
        hostname=$(ssh "root@$node_ip_address" "hostname")
    else
        hostname=$(hostname)
    fi
    
}





validate_password_in_lists() {
    local password_to_check="$1"
    weakpass=$(grep -E "^weakpass=" "$PANEL_CONFIG_FILE" | awk -F= '{print $2}')

    if [ -z "$weakpass" ]; then
      if [ "$DEBUG" = true ]; then
        echo "weakpass value not found in openpanel.config. Defaulting to 'yes'."
      fi
      weakpass="yes"
    fi
    
    # https://weakpass.com/wordlist
    # https://github.com/steveklabnik/password-cracker/blob/master/dictionary.txt
    
    if [ "$weakpass" = "no" ]; then
      if [ "$DEBUG" = true ]; then
        echo "Checking the password against weakpass dictionaries"
      fi

       wget -O /tmp/weakpass.txt https://github.com/steveklabnik/password-cracker/blob/master/dictionary.txt > /dev/null 2>&1
       
       if [ -f "/tmp/weakpass.txt" ]; then
            DICTIONARY="dictionary.txt"
            local input_lower=$(echo "$password_to_check" | tr '[:upper:]' '[:lower:]')
        
            # Check if input contains any common dictionary word
            if grep -qi "^$input_lower$" "$DICTIONARY"; then
                echo "ERROR: password contains a common dictionary word from https://weakpass.com/wordlist"
                echo "       Please use stronger password or disable weakpass check with: 'opencli config update weakpass no'."
                rm dictionary.txt
                exit 1
            fi
            rm dictionary.txt
       else
	       echo "WARNING: Error downloading dictionary from https://weakpass.com/wordlist"
       fi
    elif [ "$weakpass" = "yes" ]; then
      :
    else
      if [ "$DEBUG" = true ]; then
        echo "Invalid weakpass value '$weakpass'. Defaulting to 'yes'."
      fi
      weakpass="yes"
      :
    fi
}


check_username_is_valid() {
    is_username_forbidden() {
        local check_username="$1"
        log "Checking if username $username is in the forbidden usernames list"
        readarray -t forbidden_usernames < "$FORBIDDEN_USERNAMES_FILE"
    
        # Check against forbidden usernames
        for forbidden_username in "${forbidden_usernames[@]}"; do
            if [[ "${check_username,,}" == "${forbidden_username,,}" ]]; then
                return 0
            fi
        done
    
        return 1
    }



    is_username_valid() {
        local check_username="$1"
        log "Checking if username $username is valid"
        # Check if the username meets all criteria
        if [[ "$check_username" =~ [[:space:]] ]] || [[ "$check_username" =~ [-_] ]] || \
           [[ ! "$check_username" =~ ^[a-zA-Z0-9]+$ ]] || \
           (( ${#check_username} < 3 || ${#check_username} > 20 )); then
            return 0
        fi
    
        return 1
    }


    
    # Validate username
    if is_username_valid "$username"; then
        echo "Error: The username '$username' is not valid. Ensure it is a single word with no hyphens or underscores, contains only letters and numbers, and has a length between 3 and 20 characters."
        echo "       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openpanel"
        exit 1
    elif is_username_forbidden "$username"; then
        echo "Error: The username '$username' is not allowed."
        echo "       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#reserved-usernames"
        exit 1
    fi
}


# Source the database config file
. "$DB_CONFIG_FILE"

check_running_containers() {
    log "Checking if there is already a docker container with the exact same name"
    # Check if Docker container with the exact username exists
    container_id=$(docker $context_flag ps -a --filter "name=^${username}$" --format "{{.ID}}")
    
    if [ -n "$container_id" ]; then
        echo "ERROR: Docker container with the same name '$username' already exists on this server. Aborting."
        exit 1
    fi
}


get_existing_users_count() {
    
    # added in 0.2.0
    key_value=$(grep "^key=" $PANEL_CONFIG_FILE | cut -d'=' -f2-)
    
    # Check if 'enterprise edition'
    if [ -n "$key_value" ]; then
        :
        log "Enterprise edition detected: unlimited number of users can be created"
    else
       log "Checking if the limit of 3 users on Community edition is reached"
        # Check the number of users from the database
        user_count_query="SELECT COUNT(*) FROM users"
        user_count=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$user_count_query" -sN)
    
        # Check if successful
        if [ $? -ne 0 ]; then
            echo "ERROR: Unable to get total user count from the database. Is mysql running?"
            exit 1
        fi
    
        # Check if the number of users is >= 3
        if [ "$user_count" -gt 2 ]; then
            echo "ERROR: OpenPanel Community edition has a limit of 3 user accounts - which should be enough for private use. If you require more than 3 accounts, please consider purchasing the Enterprise version that allows unlimited number of users and domains/websites."
            exit 1
        fi
    fi

}

# Function to check if username already exists in the database
check_username_exists() {
    local username_exists_query="SELECT COUNT(*) FROM users WHERE username = '$username'"
    local username_exists_count=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$username_exists_query" -sN)

    # Check if successful
    if [ $? -ne 0 ]; then
        echo "Error: Unable to check username existence in the database. Is mysql running?"
        exit 1
    fi

    # Return the count of usernames found
    echo "$username_exists_count"
}


# Check if the username exists in the database
username_exists_count=$(check_username_exists)

# Check if the username exists
if [ "$username_exists_count" -gt 0 ]; then
    echo "Error: Username '$username' already exists in the database."
    exit 1
fi


#########################################
# TODO
# USE REMOTE CONTEXT! context_flag
#
#########################################
#
#

#Get CPU, DISK, INODES and RAM limits for the plan
get_plan_info_and_check_requirements() {
    log "Getting information from the database for plan $plan_name"
    # Fetch DOCKER_IMAGE, DISK, CPU, RAM, INODES, BANDWIDTH and NAME for the given plan_name from the MySQL table
    query="SELECT cpu, ram, docker_image, disk_limit, inodes_limit, bandwidth, storage_file, id FROM plans WHERE name = '$plan_name'"
    
    # Execute the MySQL query and store the results in variables
    cpu_ram_info=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$query" -sN)
    
    # Check if the query was successful
    if [ $? -ne 0 ]; then
        echo "ERROR: Unable to fetch plan information from the database."
        exit 1
    fi
    
    # Check if any results were returned
    if [ -z "$cpu_ram_info" ]; then
        echo "ERROR: Plan with name $plan_name not found. Unable to fetch Docker image and CPU/RAM limits information from the database."
        exit 1
    fi
    
    # Extract DOCKER_IMAGE, DISK, CPU, RAM, INODES, BANDWIDTH and NAME,values from the query result
    cpu=$(echo "$cpu_ram_info" | awk '{print $1}')
    ram=$(echo "$cpu_ram_info" | awk '{print $2}')
    docker_image=$(echo "$cpu_ram_info" | awk '{print $3}')
    disk_limit=$(echo "$cpu_ram_info" | awk '{print $4}' | sed 's/ //;s/B//')
    # 5. is GB in disk_limit
    inodes=$(echo "$cpu_ram_info" | awk '{print $6}')
    bandwidth=$(echo "$cpu_ram_info" | awk '{print $7}')
    storage_file=$(echo "$cpu_ram_info" | awk '{print $8}' | sed 's/ //;s/B//')
    # 9. is GB in storage_file
    plan_id=$(echo "$cpu_ram_info" | awk '{print $10}')
    disk_size_needed_for_docker_and_storage=$((disk_limit + storage_file))
    
    # Get the available free space on the disk
    if [ -n "$node_ip_address" ]; then
        # TODO: Use a custom user or configure SSH instead of using root
        current_free_space=$(ssh "root@$node_ip_address" "df -BG / | awk 'NR==2 {print \$4}' | sed 's/G//'")
    else
        current_free_space=$(df -BG / | awk 'NR==2 {print $4}' | sed 's/G//')
    fi

    
    
    # Compare the available free space with the disk limit of the plan
    if [ "$current_free_space" -lt "$disk_size_needed_for_docker_and_storage" ]; then
        echo "WARNING: Insufficient disk space on the server. Required: ${disk_size_needed_for_docker_and_storage}GB, Available: ${current_free_space}GB"
       #### exit 1
    fi


    
    # Get the maximum available CPU cores on the server
    if [ -n "$node_ip_address" ]; then
        # TODO: Use a custom user or configure SSH instead of using root
        max_available_cores=$(ssh "root@$node_ip_address" "nproc")
    else
        max_available_cores=$(nproc)
    fi
    
    # Compare the specified CPU cores with the maximum available cores
    if [ "$cpu" -gt "$max_available_cores" ]; then
        echo "ERROR: Requested CPU cores ($cpu) exceed the maximum available cores on this server ($max_available_cores). Cannot create user."
        exit 1
    fi
    
    

    # Get the maximum available RAM on the server in GB
    if [ -n "$node_ip_address" ]; then
        # TODO: Use a custom user or configure SSH instead of using root
        max_available_cores=$(ssh "root@$node_ip_address" "free -g | awk '/^Mem:/{print $2}'")
    else
        max_available_ram_gb=$(free -g | awk '/^Mem:/{print $2}')
    fi    
    numram="${ram%"g"}"

    
    # Compare the specified RAM with the maximum available RAM
    if [ "$numram" -gt "$max_available_ram_gb" ]; then
        echo "ERROR: Requested RAM ($ram GB) exceeds the maximum available RAM on this server ($max_available_ram_gb GB). Cannot create user."
        exit 1
    fi
}





# DEBUG
print_debug_info_before_starting_creation() {
    if [ "$DEBUG" = true ]; then
        echo "Started creating new user account"
        echo "Docker context to be used for the new container: $server_name" 
        echo "Selected plan limits from database:"
        echo "- plan id:           $plan_id" 
        echo "- plan name:         $plan_name"
        echo "- docker image:      $docker_image"
        echo "- cpu limit:         $cpu"
        echo "- memory limit:      $ram"
        echo "- container size:    $disk_limit"
        echo "- storage file:      $storage_file"
        echo "- inodes limit:      $inodes"
        echo "- port speed:        $bandwidth"
        echo "- docker network:    $docker_network_name"
        echo "- total disk needed: $disk_size_needed_for_docker_and_storage"
    fi
}




# Check if the Docker image exists locally
check_if_docker_image_exists() {
    log "Checking if docker image $docker_image exists on server"
    if ! docker $context_flag images -q "$docker_image" >/dev/null 2>&1; then
        echo "WARNING: Docker image '$image_to_use' does not exist on this server. Attempting to pull..."
            if docker $context_flag pull "$image_to_use"; then
            echo "Image '$image_to_use' pulled successfully."
        else
            echo "ERROR: Docker image '$image_to_use' does not exist and pull failed."
            exit 1 
        fi
    fi   
}


# TODO:
# check if remote server
# and execute there!

# create storage file
create_storage_file_and_mount_if_needed() {
                if [ -n "$node_ip_address" ]; then
                    log "Creating user $username on server $node_ip_address"
                    ssh "root@$node_ip_address" "useradd -m -d /home/$username $username"
		    user_id=$(ssh "root@$node_ip_address" "id -u $username")
			if [ $? -ne 0 ]; then
			    echo "Failed creating linux user $username on server $node_ip_address"
			    exit 1
			fi
                else
                   log "Creating user $username"
		    useradd -m -d /home/$username $username
      		    user_id=$(id -u $username)	
			if [ $? -ne 0 ]; then
			    echo "Failed creating linux user $username"
			    exit 1
			fi
                fi

    log "Configuring disk and inodes limits for the user"


enable_mount_quotas() {
	sudo mount -o remount,rw /dev/vda1 > /dev/null 2>&1
	sudo quotacheck -cug /dev/vda1 > /dev/null 2>&1
	sudo quotacheck -m -cug /dev/vda1 > /dev/null 2>&1
}


    if [ "$storage_file" -ne 0 ]; then
    	storage_in_blocks=$((storage_file * 1024000))
                if [ -n "$node_ip_address" ]; then
                    log "Setting storage size of ${storage_file}GB and $inodes inodes for the user on server $node_ip_address"
                    # TODO: Use a custom user or configure SSH instead of using root
                    ssh "root@$node_ip_address" "setquota -u $username $storage_in_blocks $storage_in_blocks $inodes $inodes /"
		    # TODO: run enable_mount_quotas on ssh!

                else
                    log "Setting storage size of ${storage_file}GB and $inodes inodes for the user"
		    enable_mount_quotas # must be before setquota!
      		    setquota -u $username $storage_in_blocks $storage_in_blocks $inodes $inodes /
	    	    
                fi
    else

                if [ -n "$node_ip_address" ]; then
                    log "Setting unlimited storage and inodes for the user on server $node_ip_address"
                    # TODO: Use a custom user or configure SSH instead of using root
                    ssh "root@$node_ip_address" "setquota -u $username 0 0 0 0 /"
		    # TODO: run enable_mount_quotas on ssh!
                else
                    log "Setting unlimited storage and inodes for the user"
		    enable_mount_quotas # must be before setquota!
      		    setquota -u $username 0 0 0 0 /
                fi
    fi
    
    # Create and set permissions for user directory
    if [ -n "$node_ip_address" ]; then
        log "Creating directories for the user on server $node_ip_address"
    # TODO: Use a custom user or configure SSH instead of using root
        ssh "root@$node_ip_address" "mkdir -p /home/$username && chown 1000:33 /home/$username && chmod 755 /home/$username && chmod g+s /home/$username"
    else
        log "Creating directories for the user"
        mkdir -p /home/$username
        chown 1000:33 /home/$username
        chmod 755 /home/$username
        chmod g+s /home/$username
    fi


    
         ensure_sshfs_is_installed() {
                if ! command -v sshfs &> /dev/null; then
                log "SSHFS command is not available on the master server, installing.."
                    if command -v apt-get &> /dev/null; then
                        sudo apt-get update > /dev/null 2>&1
                        sudo apt-get install -y -qq sshfs > /dev/null 2>&1
                    elif command -v yum &> /dev/null; then
                        sudo yum install -y -q sshfs > /dev/null 2>&1
                    elif command -v dnf &> /dev/null; then
                        sudo dnf install -y -q sshfs > /dev/null 2>&1
                    else
                        echo "EROOR: No compatible package manager found. Please install sshfs manually and try again."
                        exit 1
                    fi
            
                    if ! command -v sshfs &> /dev/null; then
                        echo "ERROR: sshfs installation failed. Please install sshfs manually and try again."
                        exit 1
                    fi
                fi
       }






    
    # Mount storage file if needed
    if [ "$storage_file" -ne 0 ] && [ "$disk_limit" -ne 0 ]; then
        if [ -n "$node_ip_address" ]; then
            log "Mounting the /home/$username partition for the user on server $node_ip_address"
            # TODO: Use a custom user or configure SSH instead of using root
            ensure_sshfs_is_installed
            sshfs root@$node_ip_address:/home/$username/ /home/$username/
            echo "root@$node_ip_address:/home/$username/ /home/$username/ fuse.sshfs defaults,_netdev,allow_other 0 0" >> /etc/fstab   # mount on master for openpanel container
        fi
    fi
}



## Function to create a Docker network with bandwidth limiting
check_or_create_network() {
   
     ensure_tc_is_installed() {
            # Check if tc is installed
            if ! command -v tc &> /dev/null; then
                # Detect the package manager and install tc
                if command -v apt-get &> /dev/null; then
                    sudo apt-get update > /dev/null 2>&1
                    sudo apt-get install -y -qq iproute2 > /dev/null 2>&1
                elif command -v yum &> /dev/null; then
                    sudo yum install -y -q iproute2 > /dev/null 2>&1
                elif command -v dnf &> /dev/null; then
                    sudo dnf install -y -q iproute-tc > /dev/null 2>&1
                else
                    echo "Error: No compatible package manager found. Please install tc manually and try again."
                    exit 1
                fi
        
                # Check if installation was successful
                if ! command -v tc &> /dev/null; then
                    echo "Error: tc installation failed. Please install tc manually and try again."
                    exit 1
                fi
            fi
   }


    
    create_docker_network() {
    
      for ((i = 18; i < 255; i++)); do
        subnet="172.$i.0.0/16"
        gateway="172.$i.0.1"
    
        # Check if the subnet is already in use
        used_subnets=$(docker $context_flag network ls --format "{{.Name}}" | while read -r network_name; do
          docker $context_flag network inspect --format "{{range .IPAM.Config}}{{.Subnet}}{{end}}" "$network_name"
        done)
    
        if [[ $used_subnets =~ $subnet ]]; then
          continue  # Skip if the subnet is already in use
        fi
        # Create the Docker network
        docker $context_flag network create --driver bridge --subnet "$subnet" --gateway "$gateway" "$docker_network_name"


        # Extract the network interface name for the gateway IP
        if [ -n "$node_ip_address" ]; then
            # TODO: Use a custom user or configure SSH instead of using root
            gateway_interface=$(ssh "root@$node_ip_address" "ip route | grep $gateway | awk '{print $3}'")
        else
            gateway_interface=$(ip route | grep "$gateway" | awk '{print $3}')
        fi

        ensure_tc_is_installed
        # TODO : ON REMOTE SERVER!

        # Limit the gateway bandwidth
        if [ -n "$node_ip_address" ]; then
            # TODO: Use a custom user or configure SSH instead of using root
            gateway_interface=$(ssh "root@$node_ip_address" "tc qdisc add dev $gateway_interface root tbf rate ${bandwidth}mbit burst ${bandwidth}mbit latency 3ms")
        else
            tc qdisc add dev "$gateway_interface" root tbf rate "$bandwidth"mbit burst "$bandwidth"mbit latency 3ms
        fi

        found_subnet=1  # Set the flag to indicate success
        break
      done
      if [ $found_subnet -eq 0 ]; then
        echo "ERROR: No available subnet found for docker. Exiting."
        exit 1  # Exit with an error code
      fi
    }

    log "Checking if docker network $docker_network_name exists for plan"
    # Check if DEBUG is true and the Docker network exists
    if [ "$DEBUG" = true ] && docker $context_flag network inspect "$docker_network_name" >/dev/null 2>&1; then
        # Docker network exists, DEBUG is true so show message
        echo "Docker network '$docker_network_name' already exists"
    elif [ "$DEBUG" = false ] && docker $context_flag network inspect "$docker_network_name" >/dev/null 2>&1; then
        # Docker network exists, but DEBUG is not true so we dont show anything
        :
    elif [ "$DEBUG" = false ]; then
        # Docker network does not exist, we need to create it but dont show any output..
        create_docker_network "$docker_network_name" "$bandwidth"  >/dev/null 2>&1
    else
        # Docker network does not exist, we need to create it..
        echo "Docker network '$docker_network_name' does not exist. Creating..."
        create_docker_network "$docker_network_name" "$bandwidth"
    fi

}



get_webserver_from_plan_name() {
    log "Checking webserver for specified plan"
    # Determine the web server based on the Docker image name
    if [[ "$docker_image" == *"nginx"* ]]; then
      path="nginx"
      web_server="nginx"
    elif [[ "$docker_image" == *"litespeed"* ]]; then
      path="litespeed"
      web_server="litespeed"
    elif [[ "$docker_image" == *"apache"* ]]; then
      path="apache2"
      web_server="apache"
    else
      path="nginx"
      web_server="nginx"
    fi

    log "Checking mysql version for specified plan"
    
    # 0.2.7
    docker_image_labels_json=$(docker image inspect --format='{{json .Config.Labels}}' "$docker_image")
    if echo "$docker_image_labels_json" | grep -q 'mariadb'; then
      mysql_version="mariadb"
    else
      mysql_version="mysql" # fallback
    fi

    # 0.3.3
    if echo "$docker_image_labels_json" | grep -q 'nginx'; then
      web_server="nginx"
      path="nginx"
    elif echo "$docker_image_labels_json" | grep -q 'apache'; then
      web_server="apache"
      path="apache"
    fi


    
    #0.1.7
    if [ "$DEBUG" = true ]; then
        echo "Based on the docker image $docker_image the following data will be used:"
        echo "- webserver:      $web_server"
        echo "- mysql version:  $mysql_version"
    fi
    # then create a container
}




docker_rootless() {

mkdir -p /home/$username/docker-data /home/$username/.config/docker
touch /home/$username/.config/docker/daemon.json

echo '{
  "data-root": "/home/$username/docker-data"
}' > /home/$username/.config/docker/daemon.json

mkdir -p /home/$username/bin
#chmod 0777 /home/$username/bin
#chmod 0777 /home/$username
chmod 777 -R /home/
##############chown -R $user_id:$user_id /home/$username # treba id iz kontejnera!
#chown -R $username:$username /home/$username/docker-data



cat <<EOT | sudo tee "/etc/apparmor.d/home.$username.bin.rootlesskit"
# ref: https://ubuntu.com/blog/ubuntu-23-10-restricted-unprivileged-user-namespaces
abi <abi/4.0>,
include <tunables/global>

/home/$username/bin/rootlesskit flags=(unconfined) {
  userns,

  # Site-specific additions and overrides. See local/README for details.
  include if exists <local/home.$username.bin.rootlesskit>
}
EOT





 filename=$(echo $HOME/bin/rootlesskit | sed -e s@^/@@ -e s@/@.@g)



 cat <<EOF > ~/${filename}
abi <abi/4.0>,
include <tunables/global>

"$HOME/bin/rootlesskit" flags=(unconfined) {
  userns,

  include if exists <local/${filename}>
}
EOF


mv ~/${filename} /etc/apparmor.d/${filename}


# Define the sudoers file path
SUDOERS_FILE="/etc/sudoers"

# Backup the sudoers file before editing
cp "$SUDOERS_FILE" "$SUDOERS_FILE.bak"
echo "Backup of the sudoers file created at $SUDOERS_FILE.bak."

# Add user to sudoers file with no password requirement
echo "$username ALL=(ALL) NOPASSWD:ALL" >> "$SUDOERS_FILE"

# Check if the change was successful
if grep -q "$username ALL=(ALL) NOPASSWD:ALL" "$SUDOERS_FILE"; then
    echo "Successfully added $username to sudoers file with passwordless sudo permissions."
else
    echo "Failed to update the sudoers file. Please check the syntax."
    exit 1
fi

# Verify the sudoers file using visudo
visudo -c
if [[ $? -eq 0 ]]; then
    echo "sudoers file syntax is valid. The changes have been applied."
else
    echo "The sudoers file contains syntax errors. Restoring the backup."
    mv "$SUDOERS_FILE.bak" "$SUDOERS_FILE"
    exit 1
fi


####loginctl enable-linger $username

sudo systemctl restart apparmor.service

machinectl shell $username@ /bin/bash -c "


 	sudo loginctl enable-linger $USER
  
	mkdir -p /home/$username/.docker/run
	chmod 700 /home/$username/.docker/run
	sudo chmod 755 -R /home/$username/
	chown -R $username:$username /home/$username/

	#sudo chown $username:$username /home/$username/.docker/run
	#sudo chown -R $username:$username /home/$username ##################

 
    cd /home/$username/bin
    # Install Docker rootless
    wget -O /home/$username/bin/dockerd-rootless-setuptool.sh https://get.docker.com/rootless
   
    # Setup environment for rootless Docker
    source ~/.bashrc
    echo \$PATH

    chmod +x /home/$username/bin/dockerd-rootless-setuptool.sh
    /home/$username/bin/dockerd-rootless-setuptool.sh install

    echo 'export XDG_RUNTIME_DIR=/home/$username/.docker/run' >> ~/.bashrc
    echo 'export PATH=/home/$username/bin:\$PATH' >> ~/.bashrc
    echo 'export DOCKER_HOST=unix:///home/$username/.docker/run/docker.sock' >> ~/.bashrc

    # Source the updated bashrc and start Docker rootless
    source ~/.bashrc
    
	mkdir -p ~/.config/systemd/user/
	cat > ~/.config/systemd/user/docker.service <<EOF
	[Unit]
	Description=Docker Application Container Engine (Rootless)
	After=network.target
	
	[Service]
	Environment=PATH=$HOME/bin:$PATH
	Environment=DOCKER_HOST=unix://%t/docker.sock
	ExecStart=$HOME/bin/dockerd-rootless.sh
	Restart=on-failure
	StartLimitBurst=3
	StartLimitInterval=60s
	
	[Install]
	WantedBy=default.target
	EOF

	systemctl --user daemon-reload
	systemctl --user enable docker
	systemctl --user start docker
"


# PATH=/home/pretragua/bin:/sbin:/usr/sbin:/usr/bin:\$PATH /home/pretragua/bin/dockerd-rootless.sh

}



change_default_email_and_allow_email_network () {
    # set default sender email address
    log "Setting ${username}@${hostname} as the default email address to be used for outgoing emails in /etc/msmtprc"
    
    docker $context_flag exec "$username" bash -c "sed -i 's/^from\s\+.*/from       ${username}@${hostname}/' /etc/msmtprc"  >/dev/null 2>&1
    docker $context_flag network connect openmail_network "$username"  >/dev/null 2>&1
}


temp_fix_for_nginx_default_site_missing() {
    if [ -n "$node_ip_address" ]; then
        # TODO: Use a custom user or configure SSH instead of using root
        ssh "root@$node_ip_address" "mkdir -p /home/$username/etc/$path/sites-available && echo >> /home/$username/etc/$path/sites-available/default"
    else
         mkdir -p /home/$username/etc/$path/sites-available
         echo >> /home/$username/etc/$path/sites-available/default
    fi
}





run_docker() {

log "Checking specified disk size for docker container"

local disk_limit_param=""

# TODO:
# check ports on remote server!
#
    # added in 0.2.3 to set fixed ports for mysql and ssh services of the user!
    find_available_ports() {
      log "Checking available ports to use for the docker container"
      local found_ports=()
                  
        
            # Check if jq is installed
        if [ -n "$node_ip_address" ]; then
            # TODO: Use a custom user or configure SSH instead of using root
            ssh "root@$node_ip_address" 'if ! command -v lsof &> /dev/null; then
                echo "lsof is not installed but needed for setting ports. Installing lsof..."
        
                # Detect the package manager and install lsof
                if command -v apt-get &> /dev/null; then
                    sudo apt-get update > /dev/null 2>&1
                    sudo apt-get install -y lsof > /dev/null 2>&1
                elif command -v yum &> /dev/null; then
                    sudo yum install -y lsof > /dev/null 2>&1
                elif command -v dnf &> /dev/null; then
                    sudo dnf install -y lsof > /dev/null 2>&1
                else
                    echo "Error: No compatible package manager found. Please install lsof manually and try again."
                    exit 1
                fi
        
                # Check if installation was successful
                if ! command -v lsof &> /dev/null; then
                    echo "Error: lsof installation failed. Please install lsof manually and try again."
                    exit 1
                fi
            fi'
        else
            if ! command -v lsof &> /dev/null; then
                echo "lsof is not installed but needed for setting ports. Installing lsof..."
        
                # Detect the package manager and install lsof
                if command -v apt-get &> /dev/null; then
                    sudo apt-get update > /dev/null 2>&1
                    sudo apt-get install -y lsof > /dev/null 2>&1
                elif command -v yum &> /dev/null; then
                    sudo yum install -y lsof > /dev/null 2>&1
                elif command -v dnf &> /dev/null; then
                    sudo dnf install -y lsof > /dev/null 2>&1
                else
                    echo "Error: No compatible package manager found. Please install lsof manually and try again."
                    exit 1
                fi
        
                # Check if installation was successful
                if ! command -v lsof &> /dev/null; then
                    echo "Error: lsof installation failed. Please install lsof manually and try again."
                    exit 1
                fi
            fi
        fi
        
        
        if [ -n "$node_ip_address" ]; then
            # TODO: Use a custom user or configure SSH instead of using root
            ssh "root@$node_ip_address" '
              declare -a found_ports=()
              for ((port=32768; port<=65535; port++)); do
                  if ! lsof -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
                      found_ports+=("$port")
                      if [ ${#found_ports[@]} -ge 4 ]; then
                          break
                      fi
                  fi
              done
        
              # Print the found ports to return them back to the local script
              echo "${found_ports[@]}"
            '
        else
            declare -a found_ports=()
            for ((port=32768; port<=65535; port++)); do
                if ! lsof -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
                    found_ports+=("$port")
                    if [ ${#found_ports[@]} -ge 4 ]; then
                        break
                    fi
                fi
            done
            echo "${found_ports[@]}"
        fi



    }
    
    validate_port() {
      local port=$1
      if [[ "$port" =~ ^[0-9]+$ ]] && [ "$port" -ge 32768 ] && [ "$port" -le 65535 ]; then
        return 0  # Port is valid
      else
        return 1  # Port is invalid
      fi
    }


    # Find available ports
    AVAILABLE_PORTS=$(find_available_ports)

    
    # Split the ports into variables
    FIRST_NEXT_AVAILABLE=$(echo $AVAILABLE_PORTS | awk '{print $1}')
    SECOND_NEXT_AVAILABLE=$(echo $AVAILABLE_PORTS | awk '{print $2}')
    THIRD_NEXT_AVAILABLE=$(echo $AVAILABLE_PORTS | awk '{print $3}')
    FOURTH_NEXT_AVAILABLE=$(echo $AVAILABLE_PORTS | awk '{print $4}')
    
    # todo: better validation!
    if validate_port "$FIRST_NEXT_AVAILABLE" && validate_port "$SECOND_NEXT_AVAILABLE" && validate_port "$THIRD_NEXT_AVAILABLE" && validate_port "$FOURTH_NEXT_AVAILABLE"; then
      # for fixed ports! local ports_param="-p 9022:22 -p 33600:3306 -p 33681:7681 -p 33080:8080" #custom ports for 22 3306 7681 8080
      local ports_param="-p $FIRST_NEXT_AVAILABLE:22 -p $SECOND_NEXT_AVAILABLE:3306 -p $THIRD_NEXT_AVAILABLE:7681 -p $FOURTH_NEXT_AVAILABLE:8080"
    else
      #echo "DEBUG: Error: some ports are invalid."
      local ports_param="-P"
    fi

local docker_cmd="docker $context_flag run --network $docker_network_name -d --name $username $ports_param $disk_limit_param --cpus=$cpu --memory=$ram \
      -v /home/$username/var/crons:/var/spool/cron/crontabs \
      -v /home/$username:/home/$username \
      -v /home/$username/etc/$path/sites-available:/etc/$path/sites-available \
      -v /etc/openpanel/skeleton/motd:/etc/motd:ro \
      -v /etc/openpanel/nginx/default_page.html:/etc/$path/default_page.html:ro \
      --restart unless-stopped \
      --hostname $hostname $docker_image"

if [ "$DEBUG" = true ]; then
    echo "$AVAILABLE_PORTS"

    log "Creating container with the docker run command:"
    echo "docker $context_flag run -d --name $username $ports_param \\"
    echo "      --network $docker_network_name \\"
    echo "      --cpus=$cpu --memory=$ram $disk_limit_param \\"
    echo "      -v /home/$username/var/crons:/var/spool/cron/crontabs \\"
    echo "      -v /home/$username:/home/$username \\"
    echo "      -v /home/$username/etc/$path/sites-available:/etc/$path/sites-available \\"
    echo "      -v /etc/openpanel/skeleton/motd:/etc/motd:ro \\"
    echo "      -v /etc/openpanel/nginx/default_page.html:/etc/$path/default_page.html:ro \\"
    echo "      --restart unless-stopped \\"
    echo "      --hostname $hostname $docker_image"
fi
        machinectl shell $username@ /bin/bash -c "$docker_cmd" > /dev/null 2>&1
}







# Check the status of the created container
check_container_status() {
    log "Checking if the docker container is running.."
    container_status=$(docker $context_flag inspect -f '{{.State.Status}}' "$username")
    
    if [ "$container_status" != "running" ]; then
        echo "ERROR: Container status is not 'running'. Cleaning up..."
        if [ -n "$node_ip_address" ]; then
            # TODO: Use a custom user or configure SSH instead of using root
            ssh "root@$node_ip_address" "umount /home/$username"
             umount /home/$username
        fi
        
        docker $context_flag rm -f "$username"
        
        if [ -n "$node_ip_address" ]; then
            # TODO: Use a custom user or configure SSH instead of using root
            ssh "root@$node_ip_address" "setquota -u $username 0 0 0 0 / && userdel -r $username && rm -rf /home/$username"
        else
            setquota -u $username 0 0 0 0 /
	    userdel -r $username
	    rm -rf /home/$username
        fi
        
        exit 1
    fi
}





display_private_ip_on_debug_only() {
    ip_address=$(docker $context_flag container inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$username")
    log "Private IP address that will be used for the user: $ip_address"
}





# TODO:
# OPEN ON REMOTE FIREWALL!!!

# Open ports on firewall
open_ports_on_firewall() {
    log "Opening ports on the firewall for the user"
    # Function to extract the host port from 'docker port' output
    extract_host_port() {
        local port_number="$1"
        local host_port
        host_port=$(docker $context_flag port "$username" | grep "${port_number}/tcp" | awk -F: '{print $2}' | awk '{print $1}')
        echo "$host_port"
    }
    
    # Define the list of container ports to check and open
    container_ports=("22" "3306" "7681" "8080")
    
    # Variable to track whether any ports were opened
    ports_opened=0
    

            # Check for CSF
            if command -v csf >/dev/null 2>&1; then
                #echo "CSF is installed."
                FIREWALL="CSF"
                log "Detected ConfigServer Firewall (CSF) - docker port range is already opened"
            # Check for UFW
            elif command -v ufw >/dev/null 2>&1; then
                #echo "UFW is installed."
                FIREWALL="UFW"
            else
                echo "Danger! Neither CSF nor UFW are installed, all user ports will be exposed to the internet, without any protection."
            fi
    
    
    # TODO: edit this for fixed ports!
    
    # Loop through the container_ports array and open the ports on firewall
    for port in "${container_ports[@]}"; do
        host_port=$(extract_host_port "$port")
            if [ -n "$host_port" ]; then
                if [ "$FIREWALL" = "CSF" ]; then
                    # range is already opened..
                    ports_opened=0
                elif [ "$FIREWALL" = "UFW" ]; then
                    if [ -n "$node_ip_address" ]; then
                        log "Opening port ${host_port} on UFW for the server $node_ip_address"
                        ssh "root@$node_ip_address" "ufw allow ${host_port}/tcp  comment ${username}" >/dev/null 2>&1
                    else
                        log "Opening port ${host_port} on UFW"
                        ufw allow ${host_port}/tcp  comment "${username}" >/dev/null 2>&1
                    fi                   
                fi
                ports_opened=1
            fi
    done
    
    # Restart UFW if ports were opened
    if [ $ports_opened -eq 1 ]; then
            if [ "$FIREWALL" = "UFW" ]; then
                    if [ -n "$node_ip_address" ]; then
                        log "Reloading UFW service on server $node_ip_address"
                        ssh "root@$node_ip_address" "ufw reload" >/dev/null 2>&1
                    else
                        log "Reloading UFW service"
                        ufw reload >/dev/null 2>&1
                    fi  
            fi        
    fi
    
}







set_ssh_user_password_inside_container() {
    log "Setting ssh password for the user $username inside the docker container"
    
    # Generate password if needed
    if [ "$password" = "generate" ]; then
        password=$(openssl rand -base64 12)
        log "Generated password: $password" 
    fi
    
    # Hash password
    hashed_password=$(python3 -c "from werkzeug.security import generate_password_hash; print(generate_password_hash('$password'))")
    
    
    
    uid_1000_user=$(docker $context_flag exec $username getent passwd 1000 | cut -d: -f1)
    
    if [ -n "$uid_1000_user" ]; then
        log "User with UID 1000 exists: $uid_1000_user"
        log "Renaming user $uid_1000_user to $username and setting its password..."   
      docker $context_flag exec $username usermod -l $username -d /home/$username -m $uid_1000_user > /dev/null 2>&1
      echo "$username:$password" | docker $context_flag exec -i "$username" chpasswd
      docker $context_flag exec $username usermod -aG www-data $username
      docker $context_flag exec $username chmod -R g+w /home/$username
      if [ "$DEBUG" = true ]; then
        echo "User $uid_1000_user renamed to $username with password: $password"
      fi
    else
        log "Creating SSH user $username inside the docker container..."
        docker $context_flag exec $username useradd -m -s /bin/bash -d /home/$username $username > /dev/null 2>&1
        echo "$username:$password" | docker $context_flag exec -i "$username" chpasswd > /dev/null 2>&1
        docker $context_flag exec $username usermod -aG www-data $username > /dev/null 2>&1
        docker $context_flag exec $username chmod -R g+w /home/$username > /dev/null 2>&1
        log "SSH user $username created with password: $password"
    fi
}






phpfpm_config() {
        log "Changing the username used for php-fpm services inside the docker container..."
        docker $context_flag exec $username find /etc/php/ -type f -name "www.conf" -exec sed -i 's/user = .*/user = '"$username"'/' {} \;  > /dev/null 2>&1
        # restart version
        log "Setting container services..."
        docker $context_flag exec $username bash -c 'for phpv in $(ls /etc/php/); do if [[ -d "/etc/php/$phpv/fpm" ]]; then service php${phpv}-fpm restart; fi done'  > /dev/null 2>&1
}




copy_skeleton_files() {
    log "Creating configuration files for the newly created user"
    
    # Use grep and awk to extract the value of default_php_version
    default_php_version=$(grep -E "^default_php_version=" "$PANEL_CONFIG_FILE" | awk -F= '{print $2}')

    # Check if default_php_version is empty (in case the panel.config file doesn't exist)
    if [ -z "$default_php_version" ]; then
      if [ "$DEBUG" = true ]; then
        echo "Default PHP version not found in $PANEL_CONFIG_FILE using the fallback default version.."
      fi
      default_php_version="php8.2"
    fi


        cp -r /etc/openpanel/skeleton/ /etc/openpanel/openpanel/core/users/$username/  > /dev/null 2>&1
        echo "web_server: $web_server" > /etc/openpanel/openpanel/core/users/$username/server_config.yml
        echo "default_php_version: $default_php_version" >> /etc/openpanel/openpanel/core/users/$username/server_config.yml
        echo "mysql_version: $mysql_version" >> /etc/openpanel/openpanel/core/users/$username/server_config.yml  
        opencli php-available_versions $username  > /dev/null 2>&1 &
    
    # Create files and folders needed for the user account
    log "- web server:          $web_server"
    log "- default php version: $default_php_version"
    log "- mysql client:        $mysql_version"

# TODO:
# opencli php-get_available_php_versions  run on remote server!
#

}




# TODO:
# opencli server-recreate_hosts run on remote server

# add user to hosts file and reload nginx
recreate_hosts_file() {
    if [ -n "$node_ip_address" ]; then
        log "Recreating the /etc/hosts file on server $node_ip_address"
        ssh "root@$node_ip_address" "opencli server-recreate_hosts" > /dev/null 2>&1
    else
        log "Recreating the /etc/hosts file"
        opencli server-recreate_hosts > /dev/null 2>&1
    fi
    log "Reloading Nginx service"
    docker $context_flag restart nginx  > /dev/null 2>&1 # must restart, reload does not remount /etc/hosts
    ######docker exec nginx bash -c "nginx -t && nginx -s reload"  > /dev/null 2>&1
}






start_panel_service() {
	# from 0.2.5 panel service is not started until acc is created
	log "Checking if OpenPanel service is already running, or starting it.."
	
	if [ "$server_name" = 'default' ]; then
		cd /root && docker compose up -d openpanel > /dev/null 2>&1
	else
		# added on 0.3.7 to start panel on cluster slave
  		ssh "root@$node_ip_address" "cd /root && docker compose up -d openpanel > /dev/null 2>&1"
	fi
}






save_user_to_db() {
    log "Saving new user to database"
    
        # Insert data into MySQL database
    mysql_query="INSERT INTO users (username, password, email, plan_id, server) VALUES ('$username', '$hashed_password', '$email', '$plan_id', '$server_name');"
    
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$mysql_query"
    
    if [ $? -eq 0 ]; then
        if [ "$server_name" = 'default' ]; then
            echo "Successfully added user $username with password: $password"
        else
            echo "Successfully added user $username with password: $password and container on server: $server_name"
        fi
    else
        echo "Error: Data insertion failed."
        exit 1
    fi

}

create_backup_dirs_for_each_index() {
    log "Creating backup jobs directories for the user"
    for dir in /etc/openpanel/openadmin/config/backups/index/*/; do
      mkdir -p "${dir}${username}"
    done
}




send_email_to_new_user() {
    if $SEND_EMAIL; then
        echo "Sending email to $email with login information"
            # Check if the provided email is valid
            if [[ $email =~ ^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$ ]]; then
                . "$SEND_EMAIL_FILE"
                email_notification "New OpenPanel account information" "OpenPanel URL: $login_url | username: $username  | password: $password"
            else
                echo "$email is not a valid email address. Login infomration can not be sent to the user."
            fi       
    fi
}

check_username_is_valid                      # validate username first
validate_password_in_lists $password         # compare with weakpass dictionaries
set_docker_context_for_container             # get context and use slave server if set
check_running_containers                     # make sure container name is available
get_existing_users_count                     # list users from db
get_plan_info_and_check_requirements         # list plan from db and check available resources
print_debug_info_before_starting_creation    # print debug info
check_or_create_network                      # check network exists or create it
check_if_docker_image_exists                 # if no image, exit
get_webserver_from_plan_name                 # apache or nginx, mariad or mysql
create_storage_file_and_mount_if_needed      # create home fodler and storage mount
docker_rootless

run_docker                                   # run docker container
#check_container_status                       # run docker container
display_private_ip_on_debug_only             # get ipv4 of container
open_ports_on_firewall                       # open ports on csf or ufw
set_ssh_user_password_inside_container       # create/rename ssh user and set password
change_default_email_and_allow_email_network # added in 0.2.5 to allow users to send email, IF mailserver network exists
phpfpm_config                                # edit phpfpm username in container
copy_skeleton_files                          # get webserver, php version and mysql type for user
create_backup_dirs_for_each_index            # added in 0.3.1 so that new users immediately show with 0 backups in :2087/backups#restore
recreate_hosts_file                          # write username and private docker ip in /etc/hosts
start_panel_service                          # start user panel if not running
save_user_to_db                              # finally save user to mysql db
send_email_to_new_user                       # added in 0.3.2 to optionally send login info to new user

# if we made it this far
exit 0 
