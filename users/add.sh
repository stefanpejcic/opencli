#!/bin/bash

# Check if the correct number of command-line arguments is provided
if [ "$#" -ne 4 ]; then
    echo "Usage: $0 <username> <password|generate> <email> <plan_id>"
    exit 1
fi

username="$1"
password="$2"
email="$3"
plan_id="$4"


#1. check for forbidden usernames
forbidden_usernames=("test" "restart" "reboot" "shutdown" "exec" "root" "admin" "ftp" "vsftpd" "apache2" "apache" "nginx" "php" "mysql" "mysqld")

is_username_forbidden() {
    local check_username="$1"
    for forbidden_username in "${forbidden_usernames[@]}"; do
        if [ "$check_username" == "$forbidden_username" ]; then
            return 0 # Username is forbidden
        fi
    done
    return 1 # not forbidden
}

if is_username_forbidden "$username"; then
    echo "Error: Username is not allowed."
    exit 1
fi


#########################################################################
############################### DB LOGIN ################################ 
#########################################################################
    # MySQL database configuration
    config_file="/usr/local/admin/db.cnf"

    # Check if the config file exists
    if [ ! -f "$config_file" ]; then
        echo "Config file $config_file not found."
        exit 1
    fi

    mysql_database="panel"

#########################################################################

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


#Get CPU, DISK and RAM limits for the plan

# Fetch disk_limit, CPU, RAM, and Docker image for the given plan_id from the MySQL table
query="SELECT cpu, ram, docker_image FROM plans WHERE id = '$plan_id'"

# add disk_limit later on..

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

# Extract DOCKER_IMAGE, DISK, CPU, and RAM values from the query result
#disk_limit=$(echo "$cpu_ram_info" | awk '{print $1}')
cpu=$(echo "$cpu_ram_info" | awk '{print $1}')
ram=$(echo "$cpu_ram_info" | awk '{print $2}')

# RAM memory reservation = 90% of RAM allocated
#ram_no_suffix=${ram_raw%g}  # Remove the 'g' suffix
#ram_mb=$((ram_no_suffix * 1024))  # Convert GB to MB (1 GB = 1024 MB)
#ram_soft_limit=$((ram_mb * 90 / 100))

docker_image=$(echo "$cpu_ram_info" | awk '{print $3}')

echo "DOCKER_IMAGE: $docker_image"
#echo "DISK: $disk_limit"
echo "CPU: $cpu"
echo "RAM: $ram"
echo "RAM: $ram"
#echo "RAM Soft Limit: $ram_soft_limit MB"



# Check if the Docker image exists locally
if docker images -q "$docker_image" 2>/dev/null; then
    echo "Docker image '$docker_image' exists locally."
else
    echo "Docker image '$docker_image' does not exist locally."
    exit 1
fi


# Run a docker container for the user with those limits

# create MySQL volume first
docker volume create mysql-$username

# then create a container
docker run -d --name $username -P --cpus="$cpu" --memory="$ram" -v /home/$username/var/crons:/var/spool/cron/crontabs -v /home/$username/etc/nginx/sites-available:/etc/nginx/sites-available   -v mysql-$username:/var/lib/mysql -v /home/$username:/home/$username --restart unless-stopped  --hostname $username $docker_image

# --memory-reservation="$ram_soft_limit"
# dd $disk_limit

# Open ports on firewall

# Function to extract the host port from 'docker port' output
extract_host_port() {
    local port_number="$1"
    local host_port
    host_port=$(docker port "$username" | grep "${port_number}/tcp" | awk -F: '{print $2}' | awk '{print $1}')
    echo "$host_port"
}

# Define the list of container ports to check and open
container_ports=("21" "22" "3306" "8080")

# Variable to track whether any ports were opened
ports_opened=0

# Loop through the container_ports array and open the ports in UFW if not already open
for port in "${container_ports[@]}"; do
    host_port=$(extract_host_port "$port")

    if [ -n "$host_port" ]; then
        # Open the port in CSF
        echo "Opening port ${host_port} for port ${port} in CSF"
        #csf -a "0.0.0.0" "${host_port}" "TCP" "Allow incoming traffic for port ${host_port}"
        ufw allow ${host_port}/tcp  comment "${username}"
        ports_opened=1
    else
        echo "Port ${port} not found in container ${container_name}"
    fi
done

# Restart CSF if ports were opened
if [ $ports_opened -eq 1 ]; then
    echo "Restarting UFW"
    ufw reload
fi


#Insert data into the database

# Generate a random password if the second argument is "generate"
if [ "$password" == "generate" ]; then
    password=$(openssl rand -base64 12)
fi

# Hash password
hashed_password=$(python3 -c "from werkzeug.security import generate_password_hash; print(generate_password_hash('$password'))")

# Insert data into MySQL database
mysql_query="INSERT INTO users (username, password, email, plan_id) VALUES ('$username', '$hashed_password', '$email', '$plan_id');"

mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$mysql_query"

if [ $? -eq 0 ]; then
    echo "Successfully added user $username password: $password"
else
    echo "Error: Data insertion failed."
    exit 1
fi
