#!/bin/bash
################################################################################
# Script Name: create_plan.sh
# Description: Add a new user to openpanel
#              Use: bash /usr/local/admin/scripts/users/create_plan.sh name description domains_limit websites_limit disk_limit inodes_limit db_limit cpu ram docker_image bandwidth
# name= Name of the plan
# description= Plan description, multiple words allowed inside ""
# domains_limit= How many domains will the plan have (0 is unlimited).
# websites_limit= How many websites will the plan have (0 is unlimited).
# disk_limit=Disk space limit in GB.
# inodes_limit= inodes limit, it will be automatically set to 500000 if the value is less than 500000.
# db_limit= Database number limit (0 is unlimited).
# cpu= number of cores limit
# ram= Ram space limit in GB.
# docker_image=can be either apache/nginx
# bandwidth=bandwidth limit, expressed in mbit/s
# Exsample: bash /usr/local/admin/scripts/users/create_plan.sh plan "new plan" 10 5 10 500000 5 2 4 nginx 1500
# Author: Radovan Jecmenica
# Created: 06.11.2023
# Last Modified: 13.11.2023
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
#!/bin/bash

# MySQL database configuration
config_file="/usr/local/admin/db.cnf"
mysql_database="panel"

# Check if the config file exists
if [ ! -f "$config_file" ]; then
    echo "Config file $config_file not found."
    exit 1
fi

# Function to insert values into the database
insert_plan() {
  local name="$1"
  local description="$2"
  local domains_limit="$3"
  local websites_limit="$4"
  local disk_limit="$5"
  local inodes_limit="$6"
  local db_limit="$7"
  local cpu="$8"
  local ram="$9"
  local docker_image="${10}"
  local bandwidth="${11}"
  
# Format disk_limit with 'GB' 
disk_limit="${disk_limit} GB"

  # Ensure inodes_limit is not less than 500000
  if [ "$inodes_limit" -lt 500000 ]; then
    inodes_limit=500000
  fi

  # Format ram with 'g' at the end
  ram="${ram}g"

  # Insert the plan into the 'plans' table
  local sql="INSERT INTO plans (name, description, domains_limit, websites_limit, disk_limit, inodes_limit, db_limit, cpu, ram, docker_image, bandwidth) VALUES ('$name', '$description', $domains_limit, $websites_limit, '$disk_limit', $inodes_limit, $db_limit, $cpu, '$ram', '$docker_image', $bandwidth);"

  mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$sql"
  if [ $? -eq 0 ]; then
    echo "Inserted: $name into plans"
  else
    echo "Failed to insert: $name into plans"
  fi
}
## Function to create a Docker network with bandwidth limiting
create_docker_network() {
  local name="$1"
  local bandwidth="$2"

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

# Check for command-line arguments
if [ "$#" -ne 11 ]; then
  echo "Usage: $0 name description domains_limit websites_limit disk_limit inodes_limit db_limit cpu ram docker_image bandwidth"
  exit 1
fi

# Capture command-line arguments
name="$1"
description="$2"
domains_limit="$3"
websites_limit="$4"
disk_limit="$5"
inodes_limit="$6"
db_limit="$7"
cpu="$8"
ram="$9"
docker_image="${10}"
bandwidth="${11}"

# Check if docker_image is either "nginx" or "apache"
if [ "$docker_image" != "nginx" ] && [ "$docker_image" != "apache" ]; then
  echo "docker_image must be 'nginx' or 'apache'"
  exit 1
fi

# Function to check if the plan name already exists in the database
check_plan_exists() {
  local name="$1"
  local sql="SELECT name FROM plans WHERE name='$name';"
  local result=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$sql")
  echo "$result"
}

# Determine the appropriate table name based on the docker_image value
if [ "$docker_image" == "nginx" ]; then
  docker_image="dev_plan_nginx"
else
  docker_image="dev_plan_apache"
fi

# Check if the plan name already exists in the database
existing_plan=$(check_plan_exists "$name")
if [ -n "$existing_plan" ]; then
  echo "Plan name '$name' already exists. Please choose another name."
  exit 1
fi

# Call the create_docker_network function to create the Docker network
create_docker_network "$name" "$bandwidth"

# Call the insert_plan function with the provided values
insert_plan "$name" "$description" "$domains_limit" "$websites_limit" "$disk_limit" "$inodes_limit" "$db_limit" "$cpu" "$ram" "$docker_image" "$bandwidth"
