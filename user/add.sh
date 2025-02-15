#!/bin/bash
################################################################################
# Script Name: user/add.sh
# Description: Create a new user with the provided plan_name.
# Usage: opencli user-add <USERNAME> <PASSWORD|generate> <EMAIL> "<PLAN_NAME>" [--send-email] [--debug] [--server=<IP_ADDRESS>]
# Docs: https://docs.openpanel.co/docs/admin/scripts/users#add-user
# Author: Stefan Pejcic
# Created: 01.10.2023
# Last Modified: 15.02.2025
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

if [ "$#" -lt 4 ] || [ "$#" -gt 7 ]; then
    echo "Usage: opencli user-add <username> <password|generate> <email> <plan_name> [--send-email] [--debug] [--server=<IP_ADDRESS>]"
    exit 1
fi

username="${1,,}"
password="$2"
email="$3"
plan_name="$4"
DEBUG=false             # Default value for DEBUG
SEND_EMAIL=false        # Don't send email by default
server=""               # Default value for context

# Parse flags for --debug, --send-email, and --context
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        --send-email)
            SEND_EMAIL=true
            ;;
        --server=*)
            server="${arg#*=}"
            # todo: tests ssh
            ;;
    esac
done

log() {
    if $DEBUG; then
        echo "$1"
    fi
}



hostname=$(hostname)



cleanup() {
  echo "[✘] Script failed. Cleaning up..."
  rm /var/lock/openpanel_user_add.lock > /dev/null 2>&1
  # todo: remove user, files, container..
  exit 1
}

trap cleanup EXIT


get_slave_if_set() {
     
     
	if [ -n "$server" ]; then
	    # Check if the format of the server is a valid IPv4 address
	    if [[ "$server" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
	        # Check if each octet is in the range 0-255
	        IFS='.' read -r -a octets <<< "$server"
	        if [[ ${octets[0]} -ge 0 && ${octets[0]} -le 255 &&
	              ${octets[1]} -ge 0 && ${octets[1]} -le 255 &&
	              ${octets[2]} -ge 0 && ${octets[2]} -le 255 &&
	              ${octets[3]} -ge 0 && ${octets[3]} -le 255 ]]; then
	           	
			context_flag="--context $server"     
			hostname=$(ssh "root@$server" "hostname")
			if [ -z "$hostname" ]; then
			  echo "ERROR: Unable to reach the node $server - Exiting."
     			  echo '       Make sure you can connect to the node from terminal with: "ssh root@$server -vvv"'
			  exit 1
			fi
   
   			node_ip_address=$server
      			context=$username # so we show it on debug!
	     		log "Container will be created on node: $node_ip_address ($hostname)"
	        else
	            echo "ERROR: $server is not a valid IPv4 address (octets out of range)."
	        fi
	    else
	        echo "ERROR: $server is not a valid IPv4 address (invalid format)."
	 	exit 1
	    fi
     	else
      		# local values
                context_flag="" 
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
                echo "[✘] ERROR: password contains a common dictionary word from https://weakpass.com/wordlist"
                echo "       Please use stronger password or disable weakpass check with: 'opencli config update weakpass no'."
                rm dictionary.txt
                exit 1
            fi
            rm dictionary.txt
       else
	       echo "[!] WARNING: Error downloading dictionary from https://weakpass.com/wordlist"
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
	    log "Checking if username $check_username is valid"
	    
	    if [[ "$check_username" =~ [[:space:]] ]]; then
	        echo "[✘] Error: The username cannot contain spaces."
	        return 0
	    fi
	    
	    if [[ "$check_username" =~ [-_] ]]; then
	        echo "[✘] Error: The username cannot contain hyphens or underscores."
	        return 0
	    fi
	    
	    if [[ ! "$check_username" =~ ^[a-zA-Z0-9]+$ ]]; then
	        echo "[✘] Error: The username can only contain letters and numbers."
	        return 0
	    fi
	    
	    if [[ "$check_username" =~ ^[0-9]+$ ]]; then
	        echo "[✘] Error: The username cannot consist entirely of numbers."
	        return 0
	    fi
	    
	    if (( ${#check_username} < 3 )); then
	        echo "[✘] Error: The username must be at least 3 characters long."
	        return 0
	    fi
	    
	    if (( ${#check_username} > 20 )); then
	        echo "[✘] Error: The username cannot be longer than 20 characters."
	        return 0
	    fi
	    
	    return 1
	}

    
    # Validate username
    if is_username_valid "$username"; then
    	echo "       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openpanel"
        exit 1
    elif is_username_forbidden "$username"; then
        echo "[✘] Error: The username '$username' is not allowed."
        echo "       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#reserved-usernames"
        exit 1
    fi
}


# Source the database config file
. "$DB_CONFIG_FILE"

check_running_containers() {
    log "Checking if there is already a user docker container with the exact same name"
    # Check if Docker container with the exact username exists
    container_id=$(docker $context_flag ps -a --filter "name=^${username}$" --format "{{.ID}}")
    
    if [ -n "$container_id" ]; then
        echo "[✘] ERROR: Docker container with the same name '$username' already exists on this server. Aborting."
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
            echo "[✘] ERROR: Unable to get total user count from the database. Is mysql running?"
            exit 1
        fi
    
        # Check if the number of users is >= 3
        if [ "$user_count" -gt 2 ]; then
            echo "[✘] ERROR: OpenPanel Community edition has a limit of 3 user accounts - which should be enough for private use."
	    echo "If you require more than 3 accounts, please consider purchasing the Enterprise version that allows unlimited number of users and domains/websites."
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
        echo "[✘] Error: Unable to check username existence in the database. Is mysql running?"
        exit 1
    fi

    # Return the count of usernames found
    echo "$username_exists_count"
}


# Check if the username exists in the database
username_exists_count=$(check_username_exists)

# Check if the username exists
if [ "$username_exists_count" -gt 0 ]; then
    echo "[✘] Error: Username '$username' is already taken."
    exit 1
fi


#########################################
# TODO
# USE REMOTE CONTEXT! context_flag
#
#########################################
#
#


sshfs_mounts() {
    if [ -n "$node_ip_address" ]; then

	get_server_ipv4_or_ipv6() {
		# IP SERVERS
		SCRIPT_PATH="/usr/local/admin/core/scripts/ip_servers.sh"
	 	log "Checking IPv4 address for the account"
		if [ -f "$SCRIPT_PATH" ]; then
		    source "$SCRIPT_PATH"
		else
		    IP_SERVER_1=IP_SERVER_2=IP_SERVER_3="https://ip.openpanel.com"
		fi
	 
		        log "Trying to fetch IP address..."
	
		get_ip() {
		    local ip_version=$1
		    local server1=$2
		    local server2=$3
		    local server3=$4
		
		    if [ "$ip_version" == "-4" ]; then
			    curl --silent --max-time 2 $ip_version $server1 || \
			    wget --timeout=2 -qO- $server2 || \
			    curl --silent --max-time 2 $ip_version $server3
		    else
			    curl --silent --max-time 2 $ip_version $server1 || \
			    curl --silent --max-time 2 $ip_version $server3
		    fi
	
		}
	
		# use public IPv4
		current_ip=$(get_ip "-4" "$IP_SERVER_1" "$IP_SERVER_2" "$IP_SERVER_3")
	
		# fallback from the server
		if [ -z "$current_ip" ]; then
		    log "Fetching IPv4 from local hostname..."
		    current_ip=$(ip addr | grep 'inet ' | grep global | head -n1 | awk '{print $2}' | cut -f1 -d/)
		fi
	 
	 	IPV4="yes"
	  
		# public IPv6
		if [ -z "$current_ip" ]; then
	 	    IPV4="no"
		    log "No IPv4 found. Checking IPv6 address..."
		    current_ip=$(get_ip "-6" "$IP_SERVER_1" "$IP_SERVER_2" "$IP_SERVER_3")
		    # Fallback to hostname IPv6 if no IPv6 from servers
		    if [ -z "$current_ip" ]; then
		        log "Fetching IPv6 from local hostname..."
		        current_ip=$(ip addr | grep 'inet6 ' | grep global | head -n1 | awk '{print $2}' | cut -f1 -d/)
		    fi
		fi
		
		# no :(
		if [ -z "$current_ip" ]; then
		    echo "Error: Unable to determine IP address of the master server (IPv4 or IPv6). Is server offline?"
		    exit 1
		fi
	}
















# mount openpanel dir on slave

# SSH into the slave server and check if /etc/openpanel exists
ssh root@$node_ip_address << EOF
  if [ ! -d "/etc/openpanel/openpanel" ]; then
    echo "Node is not yet configured to be used as an OpenPanel slave server. Configuring.."

    # Check for the package manager and install sshfs accordingly
    if command -v apt-get &> /dev/null; then
      apt-get update && apt-get install -y systemd-container uidmap
    elif command -v dnf &> /dev/null; then
      dnf install -y systemd-container uidmap
    elif command -v yum &> /dev/null; then
      yum install -y systemd-container uidmap
    else
      echo "[✘] ERROR: Unable to setup the slave server. Contact support."
      exit 1
    fi
    mkdir -p /etc/openpanel
    git clone https://github.com/stefanpejcic/OpenPanel-configuration /etc/openpanel
EOF


	# https://docs.docker.com/engine/security/rootless/#limiting-resources

	ssh root@$node_ip_address << EOF
 
  if [ ! -d "/etc/openpanel/openpanel" ]; then

    echo "Adding permissions for users to limit CPU% - more info: https://docs.docker.com/engine/security/rootless/#limiting-resources"
  
	mkdir -p /etc/systemd/system/user@.service.d
  
cat > /etc/systemd/system/user@.service.d/delegate.conf << EOF
[Service]
Delegate=cpu cpuset io memory pids
EOF

systemctl daemon-reload
EOF  

	ssh root@$node_ip_address << EOF
  if [ ! -d "/etc/openpanel/openpanel" ]; then
    echo "Configuring OpenPanel Slave on the server.."
    mkdir -p /etc/openpanel
    git clone https://github.com/stefanpejcic/OpenPanel-configuration /etc/openpanel
EOF



# TODO:
#  scp -r /etc/openpanel root@$node_ip_address:/etc/openpanel
# sync conf from master to slave!

    # mount home dir on master
    if command -v sshfs &> /dev/null; then
    	:
    else
	    # Check for the package manager and install sshfs accordingly
	    if command -v apt-get &> /dev/null; then
	      apt-get install -y sshfs
	    elif command -v dnf &> /dev/null; then
	      dnf install -y sshfs
	    elif command -v yum &> /dev/null; then
	      yum install -y sshfs
	    else
	      echo "[✘] ERROR: Unable to setup the slave server. Contact support."
	      exit 1
	    fi
    fi
	sshfs root@$node_ip_address:/home/$username /home/$username


fi
 
}

#Get CPU, DISK, INODES and RAM limits for the plan
get_plan_info_and_check_requirements() {
    log "Getting information from the database for plan $plan_name"
    # Fetch DOCKER_IMAGE, DISK, CPU, RAM, INODES, BANDWIDTH and NAME for the given plan_name from the MySQL table
    query="SELECT cpu, ram, docker_image, disk_limit, inodes_limit, bandwidth, id FROM plans WHERE name = '$plan_name'"
    
    # Execute the MySQL query and store the results in variables
    cpu_ram_info=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$query" -sN)
    
    # Check if the query was successful
    if [ $? -ne 0 ]; then
        echo "[✘] ERROR: Unable to fetch plan information from the database."
        exit 1
    fi
    
    # Check if any results were returned
    if [ -z "$cpu_ram_info" ]; then
        echo "[✘] ERROR: Plan with name $plan_name not found. Unable to fetch Docker image and CPU/RAM limits information from the database."
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
    plan_id=$(echo "$cpu_ram_info" | awk '{print $8}')
    
    # Get the available free space on the disk
    if [ -n "$node_ip_address" ]; then
        current_free_space=$(ssh "root@$node_ip_address" "df -BG / | awk 'NR==2 {print \$4}' | sed 's/G//'")
    else
        current_free_space=$(df -BG / | awk 'NR==2 {print $4}' | sed 's/G//')
    fi
    if [ "$current_free_space" -lt "$disk_limit" ]; then
        echo "WARNING: Insufficient disk space on the server. Required: ${disk_limit}GB, Available: ${current_free_space}GB"
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
        echo "[✘] ERROR: CPU cores ($cpu) limit on the plan exceed the maximum available cores on the server ($max_available_cores). Cannot create user."
        exit 1
    fi
    
    

    # Get the maximum available RAM on the server in GB
    if [ -n "$node_ip_address" ]; then
	max_available_ram_gb=$(ssh "root@$node_ip_address" "free -g | awk '/Mem:/{print \$2}'")
    else
        max_available_ram_gb=$(free -g | awk '/^Mem:/{print $2}')
    fi    
    numram="${ram%"g"}"

    
    # Compare the specified RAM with the maximum available RAM
    if [ "$numram" -gt "$max_available_ram_gb" ]; then
        echo "[✘] ERROR: RAM ($ram GB) limit on the plan exceeds the maximum available RAM on the server ($max_available_ram_gb GB). Cannot create user."
        exit 1
    fi
}





# DEBUG
print_debug_info_before_starting_creation() {
    if [ "$DEBUG" = true ]; then
	if [ -n "$node_ip_address" ]; then
	        echo "Node server:"
	        echo "- IP address:           $node_ip_address" 
	        echo "- Hostname:             $hostname" 	 
	        echo "- SSH user:             root" 	
                echo "- Docker context:       $context" 	
	 	echo ""
	fi
	#echo "Started creating new user account"
        #echo "Docker context to be used for the new container: $server_name" 
        echo "Selected plan limits from database:"
        echo "- plan id:           $plan_id" 
        echo "- plan name:         $plan_name"
        echo "- docker image:      $docker_image"
        echo "- cpu limit:         $cpu"
        echo "- memory limit:      $ram"
        echo "- storage:           $disk_limit GB"
        echo "- inodes:            $inodes"
        echo "- port speed:        $bandwidth"
    fi
}


create_local_user() {
	log "Creating user $username"
	useradd -m -d /home/$username $username
 	user_id=$(id -u $username)	
	if [ $? -ne 0 ]; then
		echo "Failed creating linux user $username on master server."
  		exit 1
	fi
}

create_remote_user() {
	local provided_id=$1
        if [ -n "$provided_id" ]; then
		id_flag="-u $provided_id"
 	else
		id_flag=""
 	fi
  	
   	if [ -n "$node_ip_address" ]; then
                    log "Creating user $username on server $node_ip_address"
                    ssh "root@$node_ip_address" "useradd -m -s /bin/bash -d /home/$username $id_flag $username" #-s /bin/bash needed for sourcing 
		    user_id=$(ssh "root@$node_ip_address" "id -u $username")
			if [ $? -ne 0 ]; then
			    echo "Failed creating linux user $username on node: $node_ip_address"
			    exit 1
			fi
	fi
 

}

set_user_quota(){

    if [ "$disk_limit" -ne 0 ]; then
    	storage_in_blocks=$((disk_limit * 1024000))
        log "Setting storage size of ${disk_limit}GB and $inodes inodes for the user"
      	setquota -u $username $storage_in_blocks $storage_in_blocks $inodes $inodes /
    else
    	log "Setting unlimited storage and inodes for the user"
      	setquota -u $username 0 0 0 0 /
    fi

}

# CREATE THE USER
create_user_and_set_quota() {
 	create_local_user
       	create_remote_user $user_id
	set_user_quota
}




get_webserver_from_plan_name() {
    log "Checking webserver for specified docker image"

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


: '    
# TODO AFTER CREATING!
    docker_image_labels_json=$(docker --context $context image inspect --format='{{json .Config.Labels}}' "$docker_image")
    if echo "$docker_image_labels_json" | grep -q 'nginx'; then
      web_server="nginx"
      path="nginx"
    elif echo "$docker_image_labels_json" | grep -q 'litespeed'; then
      path="litespeed"
      web_server="litespeed"
    elif echo "$docker_image_labels_json" | grep -q 'apache'; then
      path="apache2"
      web_server="apache"
   else
	   echo "[✘] ERROR: no labels detected for this docker image. Custom images must have labels:"
	   echo "'webserver', 'php', 'db'"
   exit 1
    fi
'

    log "Checking mysql version for specified docker image"
    docker_image_labels_json=$(docker --context $context image inspect --format='{{json .Config.Labels}}' "$docker_image")
    if echo "$docker_image_labels_json" | grep -q 'mariadb'; then
      mysql_version="mariadb"
    else
      mysql_version="mysql" # fallback
    fi

    log "Checking PHP version for specified docker image"

    #0.1.7
    if [ "$DEBUG" = true ]; then
        echo "Based on the docker image $docker_image  labels the following data will be used:"
        echo "- webserver:      $web_server"
        echo "- mysql version:  $mysql_version"
    fi
    # then create a container
}



docker_compose() {

   	if [ -n "$node_ip_address" ]; then
	    	log "Configuring Docker Compose for user $username on node $node_ip_address"
		ssh root@$node_ip_address "su - $username -c '
		DOCKER_CONFIG=\${DOCKER_CONFIG:-/home/$username/.docker}
		mkdir -p /home/$username/.docker/cli-plugins
		curl -sSL https://github.com/docker/compose/releases/download/v2.32.1/docker-compose-linux-x86_64 -o /home/$username/.docker/cli-plugins/docker-compose
		chmod +x /home/$username/.docker/cli-plugins/docker-compose
		docker compose version
		'"
	else
	    	log "Configuring Docker Compose for user $username"
		machinectl shell $username@ /bin/bash -c "
		DOCKER_CONFIG=${DOCKER_CONFIG:-/home/$username/.docker}
		mkdir -p /home/$username/.docker/cli-plugins
		curl -sSL https://github.com/docker/compose/releases/download/v2.32.1/docker-compose-linux-x86_64 -o /home/$username/.docker/cli-plugins/docker-compose
		chmod +x /home/$username/.docker/cli-plugins/docker-compose
		docker compose version
		"
	fi
}



docker_rootless() {

log "Configuring Docker in Rootless mode"

mkdir -p /home/$username/docker-data /home/$username/.config/docker > /dev/null 2>&1
		
echo "{
	\"data-root\": \"/home/$username/docker-data\"
}" > /home/$username/.config/docker/daemon.json
		
		
mkdir -p /home/$username/bin > /dev/null 2>&1
chmod 755 -R /home/$username/ >/dev/null 2>&1


   	if [ -n "$node_ip_address" ]; then

log "Setting AppArmor profile.."
ssh root@$node_ip_address <<EOF

# Create the AppArmor profile directly
cat > "/etc/apparmor.d/home.$username.bin.rootlesskit" <<EOT
abi <abi/4.0>,
include <tunables/global>

  /home/$username/bin/rootlesskit flags=(unconfined) {
    userns,
    include if exists <local/home.$username.bin.rootlesskit>
  }
EOT

# Generate the filename for the profile
filename=\$(echo "/home/$username/bin/rootlesskit" | sed -e 's@^/@@' -e 's@/@.@g')

# Create the rootlesskit profile for the user directly
cat > "/home/$username/\${filename}" <<EOF2
abi <abi/4.0>,
include <tunables/global>

  "/home/$username/bin/rootlesskit" flags=(unconfined) {
    userns,
    include if exists <local/\${filename}>
  }
EOF2

# Move the generated file to the AppArmor directory
mv "/home/$username/\${filename}" "/etc/apparmor.d/\${filename}"
EOF







log "Setting user pemissions.."

		ssh root@$node_ip_address "
		# Backup the sudoers file before modifying
		cp /etc/sudoers /etc/sudoers.bak
		
		# Append the user to sudoers with NOPASSWD
		echo \"$username ALL=(ALL) NOPASSWD:ALL\" >> /etc/sudoers
		
		# Check if the line was successfully added
		if grep -q \"$username ALL=(ALL) NOPASSWD:ALL\" /etc/sudoers; then
		    :
		else
		    echo \"Failed to update the sudoers file. Please check the syntax.\"
		    #exit 1
		fi
		
		# Verify the sudoers file using visudo
		visudo -c > /dev/null 2>&1
		if [[ \$? -eq 0 ]]; then
		    :
		else
		    echo \"The sudoers file contains syntax errors. Restoring the backup.\"
		    mv /etc/sudoers.bak /etc/sudoers
		fi
		"

log "Restarting services.."


		ssh root@$node_ip_address "
		# Restart apparmor service
		sudo systemctl restart apparmor.service >/dev/null 2>&1
		
		# Enable lingering for the user to keep their session alive across reboots
		loginctl enable-linger $username >/dev/null 2>&1
		
		# Create necessary directories and set permissions
		mkdir -p /home/$username/.docker/run >/dev/null 2>&1
		chmod 700 /home/$username/.docker/run >/dev/null 2>&1
		
		# Set the appropriate permissions for the user home directory
		chmod 755 -R /home/$username/ >/dev/null 2>&1
		chown -R $username:$username /home/$username/ >/dev/null 2>&1
  		"



  log "Downloading https://get.docker.com/rootless"

ssh root@$node_ip_address "
    su - $username -c 'bash -l -c \"
        cd /home/$username/bin
        wget -O /home/$username/bin/dockerd-rootless-setuptool.sh https://get.docker.com/rootless > /dev/null 2>&1
        chmod +x /home/$username/bin/dockerd-rootless-setuptool.sh
        /home/$username/bin/dockerd-rootless-setuptool.sh install > /dev/null 2>&1

        echo \\\"export XDG_RUNTIME_DIR=/home/$username/.docker/run\\\" >> ~/.bashrc
        echo \\\"export DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/\$(id -u)/bus\\\" >> ~/.bashrc
        echo \\\"export PATH=/home/$username/bin:\\\$PATH\\\" >> ~/.bashrc
	echo \\\"export DOCKER_HOST=unix:///run/user/\$(id -u)/docker.sock\\\" >> ~/.bashrc
    \"'
"

#         echo \\\"export DOCKER_HOST=unix:///home/$username/.docker/run/docker.sock\\\" >> ~/.bashrc
        
  log "Configuring Docker service.."

ssh root@$node_ip_address "
    # Switch to the user shell and execute the commands
    machinectl shell $username@ /bin/bash -c '
    mkdir -p ~/.config/systemd/user/
    cat > ~/.config/systemd/user/docker.service <<EOF
[Unit]
Description=Docker Application Container Engine (Rootless)
After=network.target

[Service]
Environment=PATH=/home/$username/bin:$PATH
Environment=DOCKER_HOST=unix:///home/$username/.docker/run/docker.sock
ExecStart=/home/$username/bin/dockerd-rootless.sh
Restart=on-failure
StartLimitBurst=3
StartLimitInterval=60s

[Install]
WantedBy=default.target
EOF
    '
"




  log "Starting user services.."

# Separate SSH command to create the systemd service file
ssh root@$node_ip_address "
    machinectl shell $username@ /bin/bash -c '

        echo \"XDG_RUNTIME_DIR=\$XDG_RUNTIME_DIR\"
        echo \"DBUS_SESSION_BUS_ADDRESS=\$DBUS_SESSION_BUS_ADDRESS\"
        echo \"PATH=\$PATH\"
        echo \"DOCKER_HOST=\$DOCKER_HOST\"
	
	systemctl --user daemon-reload > /dev/null 2>&1
        systemctl --user enable docker > /dev/null 2>&1
        systemctl --user start docker > /dev/null 2>&1

	systemctl --user status > /dev/null 2>&1
    '
"

#fork/exec /proc/self/exe: operation not permitted

# we should check with: systemctl --user status


	else

		
cat <<EOT | sudo tee "/etc/apparmor.d/home.$username.bin.rootlesskit" > /dev/null 2>&1
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

cat <<EOF > ~/${filename} 2>/dev/null
abi <abi/4.0>,
include <tunables/global>

"$HOME/bin/rootlesskit" flags=(unconfined) {
userns,

include if exists <local/${filename}>
}
EOF

  
  		mv ~/${filename} /etc/apparmor.d/${filename} > /dev/null 2>&1


		SUDOERS_FILE="/etc/sudoers"
		
		echo "$username ALL=(ALL) NOPASSWD:ALL" >> "$SUDOERS_FILE"
		if grep -q "$username ALL=(ALL) NOPASSWD:ALL" "$SUDOERS_FILE"; then
		    :
		else
		    echo "Failed to update the sudoers file. Please check the syntax."
		    #exit 1
		fi
		
		# Verify the sudoers file using visudo
		visudo -c > /dev/null 2>&1
		if [[ $? -eq 0 ]]; then
		    :
		else
		    echo "The sudoers file contains syntax errors. Restoring the backup."
		    mv "$SUDOERS_FILE.bak" "$SUDOERS_FILE"
		fi


		sudo systemctl restart apparmor.service   >/dev/null 2>&1
		loginctl enable-linger $username   >/dev/null 2>&1
		mkdir -p /home/$username/.docker/run   >/dev/null 2>&1
		chmod 700 /home/$username/.docker/run   >/dev/null 2>&1
		chmod 755 -R /home/$username/   >/dev/null 2>&1
		chown -R $username:$username /home/$username/   >/dev/null 2>&1

		machinectl shell $username@ /bin/bash -c "
		
		    cd /home/$username/bin
		    # Install Docker rootless
		    wget -O /home/$username/bin/dockerd-rootless-setuptool.sh https://get.docker.com/rootless > /dev/null 2>&1
		   
		    # Setup environment for rootless Docker
		    source ~/.bashrc
		
		    chmod +x /home/$username/bin/dockerd-rootless-setuptool.sh
		    /home/$username/bin/dockerd-rootless-setuptool.sh install > /dev/null 2>&1
		
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
		Environment=PATH=/home/$username/bin:$PATH
		Environment=DOCKER_HOST=unix://%t/docker.sock
		ExecStart=/home/$username/bin/dockerd-rootless.sh
		Restart=on-failure
		StartLimitBurst=3
		StartLimitInterval=60s
			
		[Install]
		WantedBy=default.target
		EOF
		
		systemctl --user daemon-reload > /dev/null 2>&1
		systemctl --user enable docker > /dev/null 2>&1
		systemctl --user start docker > /dev/null 2>&1
		"
	fi
}



change_default_email_and_allow_email_network () {
    # set default sender email address
    log "Setting ${username}@${hostname} as the default email address to be used for outgoing emails in /etc/msmtprc"
    
    docker $context_flag exec "$username" bash -c "sed -i 's/^from\s\+.*/from       ${username}@${hostname}/' /etc/msmtprc"  >/dev/null 2>&1

}



run_docker() {

# TODO:
# check ports on remote server!
#
    # added in 0.2.3 to set fixed ports for mysql and ssh services of the user!
    find_available_ports() {
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
                    echo "[✘] Error: No compatible package manager found. Please install lsof manually and try again."
                    exit 1
                fi
        
                # Check if installation was successful
                if ! command -v lsof &> /dev/null; then
                    echo "[✘] Error: lsof installation failed. Please install lsof manually and try again."
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
                    echo "[✘] Error: No compatible package manager found. Please install lsof manually and try again."
                    exit 1
                fi
        
                # Check if installation was successful
                if ! command -v lsof &> /dev/null; then
                    echo "[✘] Error: lsof installation failed. Please install lsof manually and try again."
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
                      if [ ${#found_ports[@]} -ge 6 ]; then
                          break
                      fi
                  fi
              done
        
              echo "${found_ports[@]}"
            '
        else
            declare -a found_ports=()
            for ((port=32768; port<=65535; port++)); do
                if ! lsof -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
                    found_ports+=("$port")
                    if [ ${#found_ports[@]} -ge 6 ]; then
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
	    return 0
	  else
	    echo "DEBUG: Invalid port detected: $port"
	    return 1
	  fi
	}

    # Find available ports
    log "Checking available ports to use for the docker container"
    AVAILABLE_PORTS=$(find_available_ports)

    # Split the ports into variables
	FIRST_NEXT_AVAILABLE=$(echo $AVAILABLE_PORTS | awk '{print $1}')
	SECOND_NEXT_AVAILABLE=$(echo $AVAILABLE_PORTS | awk '{print $2}')
	THIRD_NEXT_AVAILABLE=$(echo $AVAILABLE_PORTS | awk '{print $3}')
	FOURTH_NEXT_AVAILABLE=$(echo $AVAILABLE_PORTS | awk '{print $4}')
	FIFTH_NEXT_AVAILABLE=$(echo $AVAILABLE_PORTS | awk '{print $5}')
        SIXTH_NEXT_AVAILABLE=$(echo $AVAILABLE_PORTS | awk '{print $6}')

	#echo "DEBUG: Available ports: $AVAILABLE_PORTS"

    # todo: better validation!
    if validate_port "$FIRST_NEXT_AVAILABLE" && validate_port "$SECOND_NEXT_AVAILABLE" && validate_port "$THIRD_NEXT_AVAILABLE" && validate_port "$FOURTH_NEXT_AVAILABLE" && validate_port "$FIFTH_NEXT_AVAILABLE" && validate_port "$SIXTH_NEXT_AVAILABLE"; then
	port_1="$FIRST_NEXT_AVAILABLE:22"
	port_2="$SECOND_NEXT_AVAILABLE:3306"
	port_3="$THIRD_NEXT_AVAILABLE:7681"
	port_4="$FOURTH_NEXT_AVAILABLE:8080"
	port_5="$FIFTH_NEXT_AVAILABLE:80"
        port_6="$SIXTH_NEXT_AVAILABLE:443"
    else
	port_1=""
	port_2=""
	port_3=""
	port_4=""
	port_5=""
	port_6=""
    fi


# TODO FOR PHP
# docker --context gmqv6rqs image inspect --format='{{json .Config.Labels}}' openpanel/nginx | jq -r '.php'

cp /etc/openpanel/docker/compose/user-compose.yml /home/$username/docker-compose.yml

cat <<EOF > /home/$username/.env
# User-specific settings
username=$username
context=$username
docker_image=$docker_image:latest
hostname=$hostname

# Resources
cpu=$cpu
ram=$ram

# Ports
port_1="$port_1"
port_2="$port_2"
port_3="$port_3"
port_4="$port_4"
port_5="$port_5"
port_6="$port_6"

# Path
path=$path

web_server=$web_server
default_php_version=$default_php_version
mysql_version=$mysql_version

EOF

log ".env file created successfully"

local docker_cmd="cd /home/$username && docker compose up -d"

if [ "$DEBUG" = true ]; then
    #echo "$AVAILABLE_PORTS"
    log "Creating container with the docker compose command:"
    echo "$docker_cmd"
fi



if [ -n "$node_ip_address" ]; then
	ssh root@node_ip_address "
	    su - $username -c \"$docker_cmd\"
	" > /dev/null 2>&1
else
	machinectl shell $username@ /bin/bash -c "$docker_cmd" > /dev/null 2>&1
fi

compose_running=$(docker --context $username compose ls)

if echo "$compose_running" | grep -q "/home/$username/docker-compose.yml"; then
    :
else
    echo "docker-compose.yml for $username is not found or the container did not start!"
	# TODO!!!!!
 	#docker rm -f "$username" > /dev/null 2>&1
	#docker context rm "$username" > /dev/null 2>&1
        #killall -u $username > /dev/null 2>&1
        #deluser --remove-home $username > /dev/null 2>&1
  	exit 1
fi



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
	host_port=$(su $username -c "docker $context_flag port \"$username\" | grep \"${port_number}/tcp\" | awk -F: '{print \$2}' | awk '{print \$1}'")
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
    log "Setting ssh password for the root user inside the container"
    
    # Generate password if needed
    if [ "$password" = "generate" ]; then
        password=$(openssl rand -base64 12)
        log "Generated password: $password" 
    fi
    
    # Hash password
    venv_path="/usr/local/admin/venv"
    hashed_password=$("$venv_path/bin/python3" -c "from werkzeug.security import generate_password_hash; print(generate_password_hash('$password'))")
    
      echo "root:$password" | docker $context_flag exec $username chpasswd"
      docker $context_flag exec $username usermod -aG www-data root > /dev/null 2>&1
      docker $context_flag exec $username usermod -aG root www-data > /dev/null 2>&1
      docker $context_flag exec $username chmod -R g+w /var/www/html/" > /dev/null 2>&1
      if [ "$DEBUG" = true ]; then
        echo "SSH password set to: $password"
      fi

    
}



phpfpm_config() {
    log "Creating www-data user inside the container.."
    docker $context_flag exec $username usermod -u $user_id www-data > /dev/null 2>&1
    #log "Setting container services..."
    #su $username -c "docker $context_flag exec $username bash -c 'for phpv in \$(ls /etc/php/); do if [[ -d \"/etc/php/\$phpv/fpm\" ]]; then service php\${phpv}-fpm restart; fi; done'" > /dev/null 2>&1
}





copy_skeleton_files() {
    log "Creating configuration files for the newly created user"
    
	rm -rf /etc/openpanel/skeleton/domains > /dev/null 2>&1 #todo remove from 1.0.0!
        cp -r /etc/openpanel/skeleton/ /etc/openpanel/openpanel/core/users/$username/  > /dev/null 2>&1
        opencli php-available_versions $username  > /dev/null 2>&1 &
}



get_php_version() {
    # Use grep and awk to extract the value of default_php_version
    default_php_version=$(grep -E "^default_php_version=" "$PANEL_CONFIG_FILE" | awk -F= '{print $2}')

    # Check if default_php_version is empty (in case the panel.config file doesn't exist)
    if [ -z "$default_php_version" ]; then
      if [ "$DEBUG" = true ]; then
        echo "Default PHP version not found in $PANEL_CONFIG_FILE using the fallback default version.."
      fi
      default_php_version="php8.2"
    fi

}




start_panel_service() {
	# from 0.2.5 panel service is not started until acc is created
	log "Checking if OpenPanel service is already running, or starting it.."
	cd /root && docker compose up -d openpanel > /dev/null 2>&1
}


create_context() {

    if [ -n "$node_ip_address" ]; then

	docker context create $username \
	  --docker "host=ssh://$username@$node_ip_address" \
	  --description "$username"
   else
   	docker context create $username \
			  --docker "host=unix:///hostfs/home/$username/docker.sock" \
		   	  --description "$username"
   fi
}

save_user_to_db() {
    log "Saving new user to database"
    
        # Insert data into MySQL database
    mysql_query="INSERT INTO users (username, password, email, plan_id, server) VALUES ('$username', '$hashed_password', '$email', '$plan_id', '$username');"
    
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$mysql_query"
    
    if [ $? -eq 0 ]; then
        echo "[✔] Successfully added user $username with password: $password"
    else
        echo "[✘] Error: Data insertion failed."
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
		# todo: check nodeip, send it in email!
            else
                echo "$email is not a valid email address. Login infomration can not be sent to the user."
            fi       
    fi
}


reload_user_quotas() {
    quotacheck -avm > /dev/null
    repquota -u / > /etc/openpanel/openpanel/core/users/repquota 
}



collect_stats() {
	opencli docker-collect_stats $username  > /dev/null 2>&1
}

# MAIN

(
flock -n 200 || { echo "[✘] Error: A user creation process is already running."; echo "Please wait for it to complete before starting a new one. Exiting."; exit 1; }
check_username_is_valid                      # validate username first
validate_password_in_lists $password         # compare with weakpass dictionaries
get_slave_if_set                             # get context and use slave server if set
###############check_running_containers                     # make sure container name is available
get_existing_users_count                     # list users from db
get_plan_info_and_check_requirements         # list plan from db and check available resources
print_debug_info_before_starting_creation    # print debug info
create_user_and_set_quota
sshfs_mounts
docker_rootless
docker_compose
get_webserver_from_plan_name                 # apache or nginx, mariad or mysql
create_context
get_php_version   # must be before run_docker !
run_docker                                   # run docker container
reload_user_quotas                           # refresh their quotas
open_ports_on_firewall                       # open ports on csf or ufw
set_ssh_user_password_inside_container       # create/rename ssh user and set password
change_default_email_and_allow_email_network # added in 0.2.5 to allow users to send email, IF mailserver network exists
phpfpm_config                                # edit phpfpm username in container
copy_skeleton_files                          # get webserver, php version and mysql type for user
create_backup_dirs_for_each_index            # added in 0.3.1 so that new users immediately show with 0 backups in :2087/backups#restore
start_panel_service                          # start user panel if not running
save_user_to_db                              # save user to mysql db
collect_stats                                # must be after insert in db
send_email_to_new_user                       # added in 0.3.2 to optionally send login info to new user
)200>/var/lock/openpanel_user_add.lock
