#!/bin/bash
################################################################################
# Script Name: plan/edit.sh
# Description: Edit an existing hosting plan (Package) and modify its parameters.
# Usage: opencli plan-edit plan_id new_plan_name new_description new_domains_limit new_websites_limit new_disk_limit new_inodes_limit new_db_limit new_cpu new_ram new_docker_image new_bandwidth new_storage_file
# Example: opencli plan-edit 1 sad_se_zove_ovako "novi plan skroz" 0 0 10 500000 1 1 1 openpanel_nginx 500 10
# Author: Radovan Jecmenica
# Created: 10.04.2024
# Last Modified: 10.04.2024
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

# DB
source /usr/local/admin/scripts/db.sh

# Function to update values in the database
update_plan() {
  local plan_id="$1"
  local new_plan_name="$2"
  local description="$3"
  local domains_limit="$4"
  local websites_limit="$5"
  local disk_limit="$6"
  local inodes_limit="$7"
  local db_limit="$8"
  local cpu="$9"
  local ram="${10}"
  local docker_image="${11}"
  local bandwidth="${12}"
  local storage_file="${13}"

# Format disk_limit and storage_file with 'GB' 
disk_limit="${disk_limit} GB"
storage_file="${storage_file} GB"

# Ensure inodes_limit is not less than 500000
if [ "$inodes_limit" -lt 250000 ]; then
    inodes_limit=250000
fi

# Format ram with 'g' at the end
ram="${ram}g"
  
  # Update the plan in the 'plans' table
  local sql="UPDATE plans SET name='$new_plan_name', description='$description', domains_limit=$domains_limit, websites_limit=$websites_limit, disk_limit='$disk_limit', inodes_limit=$inodes_limit, db_limit=$db_limit, cpu=$cpu, ram='$ram', docker_image='$docker_image', bandwidth=$bandwidth, storage_file='$storage_file' WHERE id='$plan_id';"

  mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$sql"
  if [ $? -eq 0 ]; then
    docker network prune -f
  else
    echo "Failed to update plan id '$plan_id'"
    exit 1
  fi

}



check_cpu_cores() {
  local available_cores=$(nproc)
  
  if [ "$cpu" -gt "$available_cores" ]; then
    echo "Error: Insufficient CPU cores. Required: ${cpu}, Available: ${available_cores}"
    exit 1
  fi
}

# Function to check available RAM
check_available_ram() {
  local available_ram=$(free -g | awk '/^Mem:/{print $2}')
  if [ "$ram" -gt "$available_ram" ]; then
    echo "Error: Insufficient RAM. Required: ${ram}GB, Available: ${available_ram}GB"
    exit 1
  fi
}


# Function to check if a plan exists
check_plan_exists() {
  local id="$1"
  local sql="SELECT id FROM plans WHERE id='$id';"
  local result=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$sql")
  echo "$result"
}

# Check for command-line arguments
if [ "$#" -ne 13 ]; then
    echo "Usage: opencli $script_name plan_id new_plan_name description domains_limit websites_limit disk_limit inodes_limit db_limit cpu ram docker_image bandwidth storage_file"
    exit 1
fi

# Capture command-line arguments
plan_id="$1"
new_plan_name="$2"
description="$3"
domains_limit="$4"
websites_limit="$5"
disk_limit="$6"
inodes_limit="$7"
db_limit="$8"
cpu="$9"
ram="${10}"
docker_image="${11}"
bandwidth="${12}"
storage_file="${13}"

# Check available CPU cores before creating the plan
check_cpu_cores "$cpu"

# Check available RAM before creating the plan
check_available_ram "$ram"

# Determine the appropriate table name based on the docker_image value
if [ "$docker_image" == "nginx" ]; then
  docker_image="openpanel_nginx"
elif [ "$docker_image" == "litespeed" ]; then
  docker_image="openpanel_litespeed"
elif [ "$docker_image" == "apache" ]; then
  docker_image="openpanel_apache"
else
  docker_image="${11}"
fi

# Check if the old plan exists in the database
existing_plan=$(check_plan_exists "$plan_id")
if [ -z "$existing_plan" ]; then
  echo "Plan with id '$plan_id' does not exist."
  exit 1
fi

list_users () {
  local plan_id="$1"
  
  # Construct SQL query to select plan name based on ID
  local sql="SELECT name FROM plans WHERE id='$plan_id'"
  
  # Execute MySQL query
  local result=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "$sql")
  
  # Extract plan name from query result
  local new_plan_name=$(echo "$result" | awk 'NR>1')
  



    count=$(opencli plan-usage "$new_plan_name" --json | grep -o '"username": "[^"]*' | sed 's/"username": "//' | wc -l)

    if [ "$count" -eq 0 ]; then
        echo "Updated plan id $plan_id"
    else    
        echo "successfully updated plan id '$plan_id', you curently have $count users on the plan, to apply limits to all of them run comand: opencli plan-apply $plan_id --all"
    fi
}

# Call the update_plan function with the provided values
update_plan "$plan_id" "$new_plan_name" "$description" "$domains_limit" "$websites_limit" "$disk_limit" "$inodes_limit" "$db_limit" "$cpu" "$ram" "$docker_image" "$bandwidth" "$storage_file"
list_users "$plan_id"
