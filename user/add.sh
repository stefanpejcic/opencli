#!/bin/bash
################################################################################
# Script Name: user/add.sh
# Description: Create a new user with the provided plan_name.
# Usage: opencli user-add <USERNAME> <PASSWORD|generate> <EMAIL> <PLAN_NAME> [--debug]
# Docs: https://docs.openpanel.co/docs/admin/scripts/users#add-user
# Author: Stefan Pejcic
# Created: 01.10.2023
# Last Modified: 04.06.2024
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

# Constants
FORBIDDEN_USERNAMES_FILE="/etc/openpanel/openadmin/config/forbidden_usernames.txt"
DB_CONFIG_FILE="/usr/local/admin/scripts/db.sh"
PANEL_CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"




if [ "$#" -lt 4 ] || [ "$#" -gt 5 ]; then
    echo "Usage: opencli user-add <username> <password|generate> <email> <plan_name> [--debug]"
    exit 1
fi

username="${1,,}"
password="$2"
email="$3"
plan_name="$4"
DEBUG=false  # Default value for DEBUG
hostname=$(hostname) # Get the hostname dynamically
storage_driver=$(docker info --format '{{.Driver}}')


# Parse optional flags to enable debug mode when needed
if [ "$5" = "--debug" ]; then
    DEBUG=true
fi




is_username_forbidden() {
    local check_username="$1"
    readarray -t forbidden_usernames < "$FORBIDDEN_USERNAMES_FILE"

    # Check if the username meets all criteria
    if [[ "$check_username" =~ [[:space:]] ]] || [[ "$check_username" =~ [-_] ]] || \
       [[ ! "$check_username" =~ ^[a-zA-Z0-9]+$ ]] || \
       (( ${#check_username} < 3 || ${#check_username} > 20 )); then
        return 0
    fi

    # Check against forbidden usernames
    for forbidden_username in "${forbidden_usernames[@]}"; do
        if [[ "${check_username,,}" == "${forbidden_username,,}" ]]; then
            return 0
        fi
    done

    return 1
}

# Validate username
if is_username_forbidden "$username"; then
    echo "Error: The username '$username' is not valid. Ensure it is a single word with no hyphens or underscores, contains only letters and numbers, and has a length between 3 and 20 characters."
    exit 1
fi



# Source the database config file
source "$DB_CONFIG_FILE"



# Check if Docker container with the same username exists
if docker inspect "$username" >/dev/null 2>&1; then
    echo "Error: Docker container with the same username '$username' already exists. Aborting."
    exit 1
fi


# added in 0.2.0
key_value=$(grep "^key=" $PANEL_CONFIG_FILE | cut -d'=' -f2-)

# Check if 'enterprise edition'
if [ -n "$key_value" ]; then
    :
else
    # Check the number of users from the database
    user_count_query="SELECT COUNT(*) FROM users"
    user_count=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$user_count_query" -sN)

    # Check if successful
    if [ $? -ne 0 ]; then
        echo "Error: Unable to get user count from the database. Is mysql running?"
        exit 1
    fi

    # Check if the number of users is >= 3
    if [ "$user_count" -gt 2 ]; then
        echo "Error: OpenPanel Community edition has a limit of 3 user accounts - which should be enough for private use. If you require more than 3 accounts, please consider purchasing the Enterprise version that allows unlimited number of users and domains/websites."
        exit 1
    fi
fi


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




#Get CPU, DISK, INODES and RAM limits for the plan

# Fetch DOCKER_IMAGE, DISK, CPU, RAM, INODES, BANDWIDTH and NAME for the given plan_name from the MySQL table
query="SELECT cpu, ram, docker_image, disk_limit, inodes_limit, bandwidth, name, storage_file, id FROM plans WHERE name = '$plan_name'"

# Execute the MySQL query and store the results in variables
cpu_ram_info=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$query" -sN)

# Check if the query was successful
if [ $? -ne 0 ]; then
    echo "Error: Unable to fetch plan information from the database."
    exit 1
fi

# Check if any results were returned
if [ -z "$cpu_ram_info" ]; then
    echo "Error: Plan with name $plan_name not found. Unable to fetch Docker image and CPU/RAM limits information from the database."
    exit 1
fi

# Extract DOCKER_IMAGE, DISK, CPU, RAM, INODES, BANDWIDTH and NAME,values from the query result
disk_limit=$(echo "$cpu_ram_info" | awk '{print $4}' | sed 's/ //;s/B//')
cpu=$(echo "$cpu_ram_info" | awk '{print $1}')
ram=$(echo "$cpu_ram_info" | awk '{print $2}')
inodes=$(echo "$cpu_ram_info" | awk '{print $6}')
bandwidth=$(echo "$cpu_ram_info" | awk '{print $7}')
name=$(echo "$cpu_ram_info" | awk '{print $8}')
storage_file=$(echo "$cpu_ram_info" | awk '{print $9}' | sed 's/ //;s/B//')
plan_id=$(echo "$cpu_ram_info" | awk '{print $11}')
disk_size_needed_for_docker_and_storage=$((disk_limit + storage_file))

# Get the available free space on the disk
current_free_space=$(df -BG / | awk 'NR==2 {print $4}' | sed 's/G//')

# Compare the available free space with the disk limit of the plan
if [ "$current_free_space" -lt "$disk_size_needed_for_docker_and_storage" ]; then
    echo "Error: Insufficient disk space. Required: ${disk_size_needed_for_docker_and_storage}GB, Available: ${current_free_space}GB"
    exit 1
fi

# Get the maximum available CPU cores on the server
max_available_cores=$(nproc)

# Compare the specified CPU cores with the maximum available cores
if [ "$cpu" -gt "$max_available_cores" ]; then
    echo "Error: Requested CPU cores ($cpu) exceed the maximum available cores on the server ($max_available_cores). Cannot create user."
    exit 1
fi

# Get the maximum available RAM on the server in GB
max_available_ram_gb=$(free -g | awk '/^Mem:/{print $2}')

numram="${ram%"g"}"
# Compare the specified RAM with the maximum available RAM
if [ "$numram" -gt "$max_available_ram_gb" ]; then
    echo "Error: Requested RAM ($ram GB) exceeds the maximum available RAM on the server ($max_available_ram_gb GB). Cannot create user."
    exit 1
fi

docker_image=$(echo "$cpu_ram_info" | awk '{print $3}')

# Check if DEBUG is true before printing debug messages
if [ "$DEBUG" = true ]; then
    echo ""
    echo "----------------- DEBUG INFORMATION ------------------"
    echo ""
    echo "Selected plan limits from database:"
    echo ""
    echo "- PLAN ID: $plan_id" 
    echo "- DOCKER_IMAGE: $docker_image"
    echo "- DISK QUOTA: $disk_limit"
    echo "- CPU: $cpu"
    echo "- RAM: $ram"
    echo "- INODES: $inodes"
    echo "- STORAGE FILE: $storage_file"
    echo "- BANDWIDTH: $bandwidth"
    echo "- NAME: $name"
    echo "- TOTAL DISK NEEDED: $disk_size_needed_for_docker_and_storage"
    #echo "RAM Soft Limit: $ram_soft_limit MB"
    echo ""
    echo "------------------------------------------------------"
    echo ""
fi




# Check if the Docker image exists locally
if ! docker images -q "$docker_image" >/dev/null 2>&1; then
    echo "Error: Docker image '$docker_image' does not exist locally."
    exit 1
fi


# create storage file
if [ "$storage_file" -ne 0 ]; then
    if [ "$storage_driver" == "overlay" ] || [ "$storage_driver" == "overlay2" ]; then
        [ "$DEBUG" = true ] && echo "Run without creating /home/storage_file_$username"
    elif [ "$storage_driver" == "devicemapper" ]; then
        if [ "$DEBUG" = true ]; then
            fallocate -l ${storage_file}g /home/storage_file_$username
            mkfs.ext4 -N $inodes /home/storage_file_$username
        else
            fallocate -l ${storage_file}g /home/storage_file_$username >/dev/null 2>&1
            mkfs.ext4 -N $inodes /home/storage_file_$username >/dev/null 2>&1
        fi
    fi
fi

# Create and set permissions for user directory
mkdir -p /home/$username
chown 1000:33 /home/$username
chmod 755 /home/$username
chmod g+s /home/$username

# Mount storage file if needed
if [ "$storage_file" -ne 0 ] && [ "$disk_limit" -ne 0 ]; then
    if [ "$storage_driver" == "overlay" ] || [ "$storage_driver" == "overlay2" ]; then
        [ "$DEBUG" = true ] && echo "Run without creating /home/storage_file_$username"
    elif [ "$storage_driver" == "devicemapper" ]; then
        mount -o loop /home/storage_file_$username /home/$username
        mkdir /home/$username/docker
        chown 1000:33 /home/$username/docker
        chmod 755 /home/$username/docker
        chmod g+s /home/$username/docker
    fi
fi


## Function to create a Docker network with bandwidth limiting
create_docker_network() {

  for ((i = 18; i < 255; i++)); do
    subnet="172.$i.0.0/16"
    gateway="172.$i.0.1"

    # Check if the subnet is already in use
    used_subnets=$(docker network ls --format "{{.Name}}" | while read -r network_name; do
      docker network inspect --format "{{range .IPAM.Config}}{{.Subnet}}{{end}}" "$network_name"
    done)

    if [[ $used_subnets =~ $subnet ]]; then
      continue  # Skip if the subnet is already in use
    fi
    # Create the Docker network
    docker network create --driver bridge --subnet "$subnet" --gateway "$gateway" "$name"

    # Extract the network interface name for the gateway IP
    gateway_interface=$(ip route | grep "$gateway" | awk '{print $3}')

    # Limit the gateway bandwidth
    sudo tc qdisc add dev "$gateway_interface" root tbf rate "$bandwidth"mbit burst "$bandwidth"mbit latency 3ms

    found_subnet=1  # Set the flag to indicate success
    break
  done
  if [ $found_subnet -eq 0 ]; then
    echo "No available subnet found. Exiting."
    exit 1  # Exit with an error code
  fi
}

# Check if DEBUG is true and the Docker network exists
if [ "$DEBUG" = true ] && docker network inspect "$name" >/dev/null 2>&1; then
    # Docker network exists, DEBUG is true so show message
    echo "Docker network '$name' exists."
elif [ "$DEBUG" = false ] && docker network inspect "$name" >/dev/null 2>&1; then
    # Docker network exists, but DEBUG is not true so we dont show anything
    :
elif [ "$DEBUG" = false ]; then
    # Docker network does not exist, we need to create it but dont show any output..
    create_docker_network "$name" "$bandwidth"  >/dev/null 2>&1
else
    # Docker network does not exist, we need to create it..
    echo "Docker network '$name' does not exist. Creating..."
    create_docker_network "$name" "$bandwidth"
fi

# Determine the web server based on the Docker image
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

#0.1.7
if [ "$DEBUG" = true ]; then
    echo "web server: $web_server"
    echo ""
    echo "path: $path"
fi
# then create a container


change_default_email () {
    # set default sender email address
    hostname=$(hostname)
    docker exec "$username" bash -c "sed -i 's/^from\s\+.*/from       ${username}@${hostname}/' /etc/msmtprc"
}



temp_fix_for_nginx_default_site_missing() {
 mkdir -p /home/$username/etc/$path/sites-available
 echo >> /home/$username/etc/$path/sites-available/default

}


# Function to add a port to tcp_in for csf
add_csf_port() {
    CSF_CONF="/etc/csf/csf.conf"
    local PORT=$1

    if grep -q "TCP_IN.*$PORT" $CSF_CONF; then
        echo "Port $PORT is already in TCP_IN"
    else
        sudo sed -i "/^TCP_IN/ s/\"$/,$PORT\"/" $CSF_CONF
        echo "Port $PORT added to TCP_IN"
    fi
}


run_docker() {
    # Get the storage driver used by Docker
    storage_driver=$(docker info --format '{{.Driver}}')
    local disk_limit_param=""
    if [ "$disk_limit" -ne 0 ]; then
        # Check if the storage driver is overlay or devicemapper
        if [ "$storage_driver" == "overlay" ] || [ "$storage_driver" == "overlay2" ]; then
            if [ "$DEBUG" = true ]; then
                echo "Docker is using the overlay storage driver which does not support disk limits on XFS."
                echo "Run without disk size of ${disk_limit}G."
            fi
        elif [ "$storage_driver" == "devicemapper" ]; then
            if [ "$DEBUG" = true ]; then
                echo "Docker is using the devicemapper storage driver which supports disk limits."
                echo "Run with disk size of ${disk_limit}G."
            fi
            disk_limit_param="--storage-opt size=${disk_limit}G"
        else
            echo "Docker is using a different storage driver: $storage_driver"
            echo "Run without disk size of ${disk_limit}G."
        fi
    else
        echo "Run with NO disk size limit."
    fi

    local docker_cmd="docker run --network $name -d --name $username -P $disk_limit_param --cpus=$cpu --memory=$ram \
      -v /home/$username/var/crons:/var/spool/cron/crontabs \
      -v /home/$username:/home/$username \
      -v /home/$username/etc/$path/sites-available:/etc/$path/sites-available \
      -v /etc/openpanel/skeleton/motd:/etc/motd:ro \
      --restart unless-stopped \
      --hostname $hostname $docker_image"

    if [ "$DEBUG" = true ]; then
        echo ""
        echo "------------------------------------------------------"
        echo ""
        echo "DOCKER RUN COMMAND:"
        echo "$docker_cmd"
        echo ""
        echo "------------------------------------------------------"
        $docker_cmd
    else
        $docker_cmd > /dev/null 2>&1
    fi
}

run_docker


# Check the status of the created container
container_status=$(docker inspect -f '{{.State.Status}}' "$username")

if [ "$container_status" != "running" ]; then
    echo "Error: Container status is not 'running'. Cleaning up..."
    umount /home/$username
    docker rm -f "$username"
    rm -rf /home/$username
    rm /home/storage_file_$username
    
    exit 1
fi





# Check if DEBUG is true before printing private ip
if [ "$DEBUG" = true ]; then
    ip_address=$(docker container inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$username")
    echo "IP ADDRESS: $ip_address"
fi

# Open ports on firewall

# Function to extract the host port from 'docker port' output
extract_host_port() {
    local port_number="$1"
    local host_port
    host_port=$(docker port "$username" | grep "${port_number}/tcp" | awk -F: '{print $2}' | awk '{print $1}')
    echo "$host_port"
}

# Define the list of container ports to check and open
container_ports=("22" "3306" "7681" "8080")

# Variable to track whether any ports were opened
ports_opened=0

if [ "$DEBUG" = true ]; then
    echo ""
    echo "------------------------------------------------------"
    echo ""
    echo "OPENING PORTS ON FIREWAL FOR THE NEW USER:" 
    echo ""
fi


            # Check for CSF
            if command -v csf >/dev/null 2>&1; then
                #echo "CSF is installed."
                FIREWALL="CSF"
            # Check for UFW
            elif command -v ufw >/dev/null 2>&1; then
                #echo "UFW is installed."
                FIREWALL="UFW"
            else
                echo "Danger! Neither CSF nor UFW are installed, all user ports will be exposed to the internet, without any protection."
            fi



# Loop through the container_ports array and open the ports on firewall
for port in "${container_ports[@]}"; do
    host_port=$(extract_host_port "$port")

    if [ "$DEBUG" = true ]; then
        if [ -n "$host_port" ]; then
            # Debug mode: Print debug message            
            echo "Opening port ${host_port} for port ${port} in $FIREWALL"
    
            if [ "$FIREWALL" = "CSF" ]; then
                # range is already opened..
                ports_opened=0
                #add_csf_port ${host_port}
            elif [ "$FIREWALL" = "UFW" ]; then
                ufw allow ${host_port}/tcp  comment "${username}"
            fi
            ports_opened=1
        fi
    else
        if [ -n "$host_port" ]; then
            if [ "$FIREWALL" = "CSF" ]; then
                # range is already opened..
                ports_opened=0
                #add_csf_port ${host_port} >/dev/null 2>&1
            elif [ "$FIREWALL" = "UFW" ]; then
                ufw allow ${host_port}/tcp  comment "${username}" >/dev/null 2>&1
            fi
            ports_opened=1
        fi
    fi
done

# Restart UFW if ports were opened
if [ $ports_opened -eq 1 ]; then
    if [ "$DEBUG" = true ]; then

        if [ "$FIREWALL" = "CSF" ]; then
            :
            #echo "Reloading ConfigServer Firewall"
            #csf -r
        elif [ "$FIREWALL" = "UFW" ]; then
            echo "Reloading UFW"
            ufw reload
        fi

    else
        if [ "$FIREWALL" = "CSF" ]; then
            :
            #csf -r >/dev/null 2>&
        elif [ "$FIREWALL" = "UFW" ]; then
            ufw reload >/dev/null 2>&1
        fi        
    fi
fi


if [ "$DEBUG" = true ]; then
    echo ""
    echo "------------------------------------------------------"
    echo ""
fi





if [ "$DEBUG" = true ]; then
    echo ""
    echo "------------------------------------------------------"
    echo ""
    echo "SETTING SSH USER INSIDE DOCKER CONTIANER:"
    echo ""
fi

# Generate password if needed
if [ "$password" = "generate" ]; then
    password=$(openssl rand -base64 12)
fi

# Hash password
hashed_password=$(python3 -c "from werkzeug.security import generate_password_hash; print(generate_password_hash('$password'))")



uid_1000_user=$(docker exec $username getent passwd 1000 | cut -d: -f1)

if [ -n "$uid_1000_user" ]; then
  if [ "$DEBUG" = true ]; then
    echo "User with UID 1000 exists: $uid_1000_user"
    echo "Renaming user $uid_1000_user to $username and setting its password..."
  fi

  docker exec $username usermod -l $username -d /home/$username -m $uid_1000_user > /dev/null 2>&1
  echo "$username:$password" | docker exec -i "$username" chpasswd
  docker exec $username usermod -aG www-data $username
  docker exec $username chmod -R g+w /home/$username
  if [ "$DEBUG" = true ]; then
    echo "User $uid_1000_user renamed to $username with password: $password"
  fi
else
  if [ "$DEBUG" = true ]; then
    echo "Creating SSH user $username inside the docker container..."
    docker exec $username useradd -m -s /bin/bash -d /home/$username $username
    echo "$username:$password" | docker exec -i "$username" chpasswd
    docker exec $username usermod -aG www-data $username
    docker exec $username chmod -R g+w /home/$username
    echo "SSH user $username created with password: $password"
  else
    docker exec $username useradd -m -s /bin/bash -d /home/$username $username > /dev/null 2>&1
    echo "$username:$password" | docker exec -i "$username" chpasswd > /dev/null 2>&1
    docker exec $username usermod -aG www-data $username > /dev/null 2>&1
    docker exec $username chmod -R g+w /home/$username > /dev/null 2>&1
  fi
fi


change_default_email


if [ "$DEBUG" = true ]; then
    # change user in www.conf file for each php-fpm verison
    echo "Changing the username for php-fpm services inside the docker container..."
    docker exec $username find /etc/php/ -type f -name "www.conf" -exec sed -i 's/user = .*/user = '"$username"'/' {} \;

    # restart version
    echo "Setting container services..."
    docker exec $username bash -c 'for phpv in $(ls /etc/php/); do if [[ -d "/etc/php/$phpv/fpm" ]]; then service php${phpv}-fpm restart; fi done'
else
    docker exec $username find /etc/php/ -type f -name "www.conf" -exec sed -i 's/user = .*/user = '"$username"'/' {} \;  > /dev/null 2>&1
    docker exec $username bash -c 'for phpv in $(ls /etc/php/); do if [[ -d "/etc/php/$phpv/fpm" ]]; then service php${phpv}-fpm restart; fi done'  > /dev/null 2>&1
fi



if [ "$DEBUG" = true ]; then
    echo ""
    echo "------------------------------------------------------"
    echo ""
    echo "CREATING CONFIGURATION FILES FOR NEW USER:"
    echo ""
fi

# Use grep and awk to extract the value of default_php_version
default_php_version=$(grep -E "^default_php_version=" "$PANEL_CONFIG_FILE" | awk -F= '{print $2}')

# NEED CHECK IF 8.2 or php8.2 format expected from python!

# Check if default_php_version is empty (in case the panel.config file doesn't exist)
if [ -z "$default_php_version" ]; then
  if [ "$DEBUG" = true ]; then
    echo "Default PHP version not found in $PANEL_CONFIG_FILE using the fallback default version.."
  fi
  default_php_version="php8.2"
fi

# Create files and folders needed for the user account
if [ "$DEBUG" = true ]; then
    cp -r /etc/openpanel/skeleton/ /etc/openpanel/openpanel/core/users/$username/
    echo "web_server: $web_server" > /etc/openpanel/openpanel/core/users/$username/server_config.yml
    echo "default_php_version: $default_php_version" >> /etc/openpanel/openpanel/core/users/$username/server_config.yml
    opencli php-get_available_php_versions $username &
else
    cp -r /etc/openpanel/skeleton/ /etc/openpanel/openpanel/core/users/$username/  > /dev/null 2>&1
    echo "web_server: $web_server" > /etc/openpanel/openpanel/core/users/$username/server_config.yml
    echo "default_php_version: $default_php_version" >> /etc/openpanel/openpanel/core/users/$username/server_config.yml
    opencli php-get_available_php_versions $username  > /dev/null 2>&1 &
fi


if [ "$DEBUG" = true ]; then
    echo ""
    echo "------------------------------------------------------"
    echo ""
    echo "SAVING NEW USER TO DATABASE:"
    echo ""
fi


# Insert data into MySQL database
mysql_query="INSERT INTO users (username, password, email, plan_id) VALUES ('$username', '$hashed_password', '$email', '$plan_id');"

mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$mysql_query"

if [ $? -eq 0 ]; then
    echo "Successfully added user $username password: $password"
else
    echo "Error: Data insertion failed."
    exit 1
fi

exit 0
