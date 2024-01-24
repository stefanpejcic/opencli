#!/bin/bash
################################################################################
# Script Name: user/add.sh
# Description: Create a new user with the provided plan_id.
# Usage: opencli user-add <USERNAME> <PASSWORD> <EMAIL> <PLAN_ID>
# Docs: https://docs.openpanel.co/docs/admin/scripts/users#add-user
# Author: Stefan Pejcic
# Created: 01.10.2023
# Last Modified: 15.01.2024
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

if [ "$#" -lt 4 ] || [ "$#" -gt 5 ]; then
    script_name=$(realpath --relative-to=/usr/local/admin/scripts/ "$0")
    script_name="${script_name//\//-}"  # Replace / with -
    script_name="${script_name%.sh}"     # Remove the .sh extension
    echo "Usage: opencli $script_name <username> <password|generate> <email> <plan_id> [--debug]"
    exit 1
fi

username="$1"
password="$2"
email="$3"
plan_id="$4"
DEBUG=false  # Default value for DEBUG
hostname=$(hostname) # Get the hostname dynamically




# Parse optional flags to enable debug mode when needed!
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        *)
            ;;
    esac
done











#1. check for forbidden usernames
forbidden_usernames=("test" "restart" "reboot" "shutdown" "exec" "root" "admin" "ftp" "lsws" "litespeed" "1000" "vsftpd" "apache2" "apache" "nginx" "php" "mysql" "mysqld" "www-data" "openpanel")

is_username_forbidden() {
    local check_username="$1"

    # Check if the username is a single word
    if [[ "$check_username" =~ [[:space:]] ]]; then
        return 0 # Username contains spaces, forbidden
    fi

    # Check if the username contains hyphens or underscores also dont allow usernames that start with storage_file_
    if [[ "$check_username" =~ [-_] ]]; then
        return 0 # Username contains hyphens or underscores, forbidden
    fi

    # Check if the username contains only letters and numbers
    if [[ ! "$check_username" =~ ^[a-zA-Z0-9]+$ ]]; then
        return 0 # Username contains characters other than letters and numbers, forbidden
    fi

    # Check if the username length is within the allowed range
    local username_length=${#check_username}
    if ((username_length < 3 || username_length > 20)); then
        return 0 # Username length is outside the allowed range, forbidden
    fi

    # Check against the forbidden usernames
    for forbidden_username in "${forbidden_usernames[@]}"; do
        if [[ "${check_username,,}" == "${forbidden_username,,}" ]]; then
            return 0 # Username is forbidden
        fi
    done

    return 1 # Not forbidden
}

if is_username_forbidden "$username"; then
    echo "Error: The username '$username' is not allowed. Ensure it is a single word with no hyphens or underscores, contains only letters and numbers, and has a length between 3 and 20 characters."
    exit 1
fi




# DB
source /usr/local/admin/scripts/db.sh

# Check if Docker container with the same username exists
if docker inspect "$username" >/dev/null 2>&1; then
    echo "Error: Docker container with the same username '$username' already exists. Aborting."
    exit 1
fi

# Check if the username already exists in the users table
username_exists_query="SELECT COUNT(*) FROM users WHERE username = '$username'"
username_exists_count=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$username_exists_query" -sN)

# Check if successful
if [ $? -ne 0 ]; then
    echo "Error: Unable to check username existence in the database."
    exit 1
fi

# count > 0) show error and exit
if [ "$username_exists_count" -gt 0 ]; then
    echo "Error: Username '$username' already exists."
    exit 1
fi


#Get CPU, DISK, INODES and RAM limits for the plan

# Fetch DOCKER_IMAGE, DISK, CPU, RAM, INODES, BANDWIDTH and NAME for the given plan_id from the MySQL table
query="SELECT cpu, ram, docker_image, disk_limit, inodes_limit, bandwidth, name, storage_file FROM plans WHERE id = '$plan_id'"

# Execute the MySQL query and store the results in variables
cpu_ram_info=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$query" -sN)

# Check if the query was successful
if [ $? -ne 0 ]; then
    echo "Error: Unable to fetch plan information from the database."
    exit 1
fi

# Check if any results were returned
if [ -z "$cpu_ram_info" ]; then
    echo "Error: Plan with ID $plan_id not found. Unable to fetch Docker image and CPU/RAM limits information from the database."
    exit 1
fi

# Extract DOCKER_IMAGE, DISK, CPU, RAM, INODES, BANDWIDTH and NAME,values from the query result
#disk_limit=$(echo "$cpu_ram_info" | awk '{print $4}')
disk_limit=$(echo "$cpu_ram_info" | awk '{print $4}' | sed 's/ //;s/B//')
cpu=$(echo "$cpu_ram_info" | awk '{print $1}')
ram=$(echo "$cpu_ram_info" | awk '{print $2}')
inodes=$(echo "$cpu_ram_info" | awk '{print $6}')
bandwidth=$(echo "$cpu_ram_info" | awk '{print $7}')
name=$(echo "$cpu_ram_info" | awk '{print $8}')
storage_file=$(echo "$cpu_ram_info" | awk '{print $9}' | sed 's/ //;s/B//')
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

# RAM memory reservation = 90% of RAM allocated
#ram_no_suffix=${ram_raw%g}  # Remove the 'g' suffix
#ram_mb=$((ram_no_suffix * 1024))  # Convert GB to MB (1 GB = 1024 MB)
#ram_soft_limit=$((ram_mb * 90 / 100))

docker_image=$(echo "$cpu_ram_info" | awk '{print $3}')

# Check if DEBUG is true before printing debug messages
if [ "$DEBUG" = true ]; then
    echo "DOCKER_IMAGE: $docker_image"
    echo "DISK: $disk_limit"
    echo "CPU: $cpu"
    echo "RAM: $ram"
    echo "INODES: $inodes"
    echo "STORAGE FILE: $storage_file"
    echo "BANDWIDTH: $bandwidth"
    echo "NAME: $name"
    echo "disk_size_needed_for_docker_and_storage: $disk_size_needed_for_docker_and_storage"
    #echo "RAM Soft Limit: $ram_soft_limit MB"
fi

# Check if the Docker image exists locally, if not we can not create a user on that plan..
if [ "$DEBUG" = true ] && docker images -q "$docker_image" > /dev/null 2>&1; then
    # Docker image exists locally, and debug is true
    echo "Docker image '$docker_image' exists locally."
elif [ "$DEBUG" != true ] && docker images -q "$docker_image" > /dev/null 2>&1; then
    # Docker image exists locally, but DEBUG is not true so dont print anything
    :
else
    # Docker image does not exist locally and debug is true, we print error
    echo "Docker image '$docker_image' does not exist locally."
    exit 1
fi


# Create a directory with the user's username under /home/

fallocate -l ${storage_file}g /home/storage_file_$username
mkfs.ext4 -N $inodes /home/storage_file_$username
mkdir /home/$username
chown 1000:33 /home/$username
chmod 755 /home/$username
chmod g+s /home/$username
mount -o loop /home/storage_file_$username /home/$username

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
    echo "Docker network '$name' already exists."
elif [ "$DEBUG" != true ] && docker network inspect "$name" >/dev/null 2>&1; then
    # Docker network exists, but DEBUG is not true so we dont show anything
    :
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

# then create a container



# Create the docker container
if [ "$DEBUG" = true ]; then
    docker run --network $name -d --name $username -P --storage-opt size=${disk_limit}G --cpus="$cpu" --memory="$ram" \
      -v /home/$username/var/crons:/var/spool/cron/crontabs \
      -v /home/$username/etc/$path/sites-available:/etc/$path/sites-available \
      -v /home/$username:/home/$username \
      --restart unless-stopped \
      --hostname $hostname $docker_image
else
    docker run --network $name -d --name $username -P --storage-opt size=${disk_limit}G --cpus="$cpu" --memory="$ram" \
      -v /home/$username/var/crons:/var/spool/cron/crontabs \
      -v /home/$username/etc/$path/sites-available:/etc/$path/sites-available \
      -v /home/$username:/home/$username \
      --restart unless-stopped \
      --hostname $hostname $docker_image > /dev/null 2>&1
fi



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

ip_address=$(docker container inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$username")



# Check if DEBUG is true before printing private ip
if [ "$DEBUG" = true ]; then
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
container_ports=("21" "22" "3306" "7681" "8080")

# Variable to track whether any ports were opened
ports_opened=0

# Loop through the container_ports array and open the ports in UFW if not already open
for port in "${container_ports[@]}"; do
    host_port=$(extract_host_port "$port")

    if [ "$DEBUG" = true ]; then
        if [ -n "$host_port" ]; then
            # Debug mode: Print debug message
            echo "Opening port ${host_port} for port ${port} in UFW"
            # Open the port in UFW for debug mode (without redirecting output to /dev/null)
            ufw allow ${host_port}/tcp  comment "${username}"
        else
            # Debug mode: Print debug message
            echo "Port ${port} not found in container ${container_name}"
        fi
    else
        if [ -n "$host_port" ]; then
            # Open the port in UFW (redirect all output to /dev/null)
            ufw allow ${host_port}/tcp  comment "${username}" >/dev/null 2>&1
            ports_opened=1
        fi
    fi
done

# Restart UFW if ports were opened
if [ $ports_opened -eq 1 ]; then
    if [ "$DEBUG" = true ]; then
        # Debug mode: Print debug message
        echo "Restarting UFW"
        ufw reload
    else
        # Not in debug mode: Reload UFW with output redirection to /dev/null
        ufw reload >/dev/null 2>&1
    fi
fi

# Generate a random password if the second argument is "generate"
if [ "$password" == "generate" ]; then
    password=$(openssl rand -base64 12 | tr -dc 'a-zA-Z0-9')
fi

# Hash password
hashed_password=$(python3 -c "from werkzeug.security import generate_password_hash; print(generate_password_hash('$password'))")

if [ "$DEBUG" = true ]; then
  echo "Creating SSH user $username inside the docker container..."
  docker exec $username useradd -m -s /bin/bash -d /home/$username $username
  echo "$username:$password" | docker exec -i "$username" chpasswd
  docker exec $username usermod -aG www-data $username
  chmod -R g+w /home/$username
  docker exec $username chmod -R g+w /home/$username
  echo "SSH user $username created with password: $password"
else
  docker exec $username useradd -m -s /bin/bash -d /home/$username $username > /dev/null 2>&1
  echo "$username:$password" | docker exec -i "$username" chpasswd > /dev/null 2>&1
  docker exec $username usermod -aG www-data $username > /dev/null 2>&1
  chmod -R g+w /home/$username > /dev/null 2>&1
  docker exec $username chmod -R g+w /home/$username > /dev/null 2>&1
fi

# Define the path to the main configuration file
config_file="/usr/local/panel/conf/panel.config"

# Use grep and awk to extract the value of default_php_version
default_php_version=$(grep -E "^default_php_version=" "$config_file" | awk -F= '{print $2}')

# Check if default_php_version is empty (in case the panel.config file doesn't exist)
if [ -z "$default_php_version" ]; then
  if [ "$DEBUG" = true ]; then
    echo "Default PHP version not found in $config_file using the fallback default version.."
  fi
  default_php_version="php8.2"
fi

# Create files and folders needed for the user account
if [ "$DEBUG" = true ]; then
    mkdir -p /usr/local/panel/core/stats/$username
    mkdir -p /usr/local/panel/core/users/$username
    mkdir -p /usr/local/panel/core/users/$username/domains
    touch /usr/local/panel/core/users/$username/elastic.lock #elasticsearch is not installed
    touch /usr/local/panel/core/users/$username/redis.lock #redis is not installed
    touch /usr/local/panel/core/users/$username/memcached.lock #memcached is not installed
    echo "web_server: $web_server" > /usr/local/panel/core/users/$username/server_config.yml
    echo "default_php_version: $default_php_version" >> /usr/local/panel/core/users/$username/server_config.yml
    opencli php-get_available_php_versions $username
else
    mkdir -p /usr/local/panel/core/stats/$username  > /dev/null 2>&1
    mkdir -p /usr/local/panel/core/users/$username  > /dev/null 2>&1
    mkdir -p /usr/local/panel/core/users/$username/domains  > /dev/null 2>&1
    touch /usr/local/panel/core/users/$username/elastic.lock  > /dev/null 2>&1 #elasticsearch is not installed
    touch /usr/local/panel/core/users/$username/redis.lock  > /dev/null 2>&1 #redis is not installed
    touch /usr/local/panel/core/users/$username/memcached.lock  > /dev/null 2>&1 #memcached is not installed
    echo "web_server: $web_server" > /usr/local/panel/core/users/$username/server_config.yml
    echo "default_php_version: $default_php_version" >> /usr/local/panel/core/users/$username/server_config.yml
    opencli php-get_available_php_versions $username  > /dev/null 2>&1
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
