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

flags=()

DEBUG=false

for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        *)
            ;;
    esac
done





# Apply rate limit using tc command for the gateway of existing Docker network
edit_docker_network() {
    local name="$1"
    local bandwidth="$2"
    gateway_interface=$(docker network inspect "$name" -f '{{(index .IPAM.Config 0).Gateway}}')
    sudo tc qdisc change dev "$gateway_interface" root tbf rate "$bandwidth"mbit burst "$bandwidth"mbit latency 3ms
}





check_if_we_need_to_edit_docker_containers() {

if [ "$old_cpu" == "$cpu" ] && [ "$old_ram" == "$ram" ]; then
    echo "CPU & RAM limits are not changed."
elif [ "$old_cpu" != "$cpu" ] && [ "$old_ram" != "$ram" ]; then
    echo "Both CPU or RAM limits are changed, applying new limits."
    flags+=( "--cpu" )
    flags+=( "--ram" )
elif [ "$old_cpu" != "$cpu" ] && [ "$old_ram" == "$ram" ]; then
    echo "CPU limits are changed."
    flags+=( "--cpu" )
elif [ "$old_cpu" == "$cpu" ] && [ "$old_ram" != "$ram" ]; then
  echo "RAM limits are changed."
    #UPDATE RAM AND CPU TO EXISTING COTAINERS
    flags+=( "--ram" )
fi

# BANDWIDTH CHANGE OR PLAN NAME CHANGE
if [ "$old_bandwidth" == "$bandwidth" ] && [ "$old_plan_name" == "$new_plan_name" ]; then
    echo "Port speed and plan name have not changed, skipping renaming docker network."
elif [ "$old_bandwidth" != "$bandwidth" ] && [ "$old_plan_name" == "$new_plan_name" ]; then
    echo "Port speed limit is changed, applying new bandwidth limit to the docker network."
    edit_docker_network "$old_plan_name" "$bandwidth"
elif [ "$old_plan_name" != "$new_plan_name" ]; then
    echo "Plan name is changed, renaming docker network is not possible, so creating new network, detaching existing docker containers from old network and atttach to new one."
    #CREATE NEW NETWORK, REMOVE PREVIOUS AND REATACH ALL CONTAINERS
    flags+=( "--net" )
fi

# STORAGE FILE
if [ "$old_storage_file" == "$storage_file" ]; then
    echo "Disk limit is not changed, nothing to do."
elif [ "$int_storage_file" -gt "$int_old_storage_file" ]; then
    echo "Disk limit increased, will update all existing docker containers storage file."
    #INCREASE CONTAINERS SIZE
    flags+=( "--dsk" )
fi

# Check if there are any flags
if [ ${#flags[@]} -gt 0 ]; then
    echo "Running command: opencli plan-apply $plan_id ${flags[@]}"
    opencli plan-apply $plan_id "${flags[@]}"
fi

}









# Function to update values in the database
update_plan() {
  local plan_id="$1"

  # Get old paln data, and if different, we will initiate the `opencli plan-apply` script
  sql="SELECT name, disk_limit, inodes_limit, cpu, ram, bandwidth, storage_file FROM plans WHERE id='$plan_id'"
  result=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -N -e "$sql")
  
  old_plan_name=$(echo "$result" | awk '{print $1}')
  int_old_disk_limit=$(echo "$result" | awk '{print $2}')
  old_inodes_limit=$(echo "$result" | awk '{print $4}')
  old_cpu=$(echo "$result" | awk '{print $5}')
  old_ram=$(echo "$result" | awk '{print $6}')
  old_bandwidth=$(echo "$result" | awk '{print $7}')
  int_old_storage_file=$(echo "$result" | awk '{print $8}')
   
  new_plan_name="$2"
  description="$3"
  domains_limit="$4"
  websites_limit="$5"
  int_disk_limit="$6"
  inodes_limit="$7"
  db_limit="$8"
  cpu="$9"
  int_ram="${10}"
  docker_image="${11}"
  bandwidth="${12}"
  int_storage_file="${13}"

  # Format disk_limit and storage_file with 'GB' 
  disk_limit="${int_disk_limit} GB"
  storage_file="${int_storage_file} GB"
  
  # format without GB for old limits
  old_disk_limit="${int_old_disk_limit} GB"
  old_storage_file="${int_old_storage_file} GB"
  int_old_ram=${old_ram%"g"}
  
  # Ensure inodes_limit is not less than 500000
  if [ "$inodes_limit" -lt 250000 ]; then
      inodes_limit=250000
  fi

  # Format ram with 'g' at the end
  ram="${ram}g"


if [ "$DEBUG" = true ]; then
  echo "+===================================+"
  echo "| PLAN ID: $plan_id"
  echo "+===================================+"

  echo "Old Plan Name: $old_plan_name"
  echo "Old Disk Limit: $old_disk_limit"
  echo "Old Inodes Limit: $old_inodes_limit"
  echo "Old CPU: $old_cpu"
  echo "Old RAM: $old_ram"
  echo "Old Bandwidth: $old_bandwidth"
  echo "Old Storage File: $old_storage_file"
  echo "+===================================+"
  echo "New Plan Name: $new_plan_name"
  echo "New Disk Limit: $disk_limit"
  echo "New Inodes Limit: $inodes_limit"
  echo "New CPU: $cpu"
  echo "New RAM: $ram"
  echo "New Bandwidth: $bandwidth"
  echo "New Storage File: $storage_file"
  echo "+===================================+"
fi




### contruct opencli plan-apply command if needed!


# STORAGE FILE
if [ "$int_old_storage_file" -eq 0 ] && [ "$int_storage_file" -ne 0 ]; then
    echo "ERROR: Docker does not support changing limit if plan is already unlimited. Disk limit cannot be changed from ∞ to $int_disk_limit."
    exit 1
elif [ "$int_storage_file" -eq 0 ] && [ "$int_old_storage_file" -ne 0 ]; then
    echo "ERROR: Docker does not support changing limit from a limit to be unlimited. Disk limit cannot be changed from $int_old_storage_file to ∞."
    exit 1
elif [ "$int_storage_file" -lt "$int_old_storage_file" ]; then
    echo "ERROR: Docker does not support decreasing image size. Can not change disk usage limit from $int_old_disk_limit to $int_disk_limit."
    exit 1
fi













  
  # Update the plan in the 'plans' table
  local sql="UPDATE plans SET name='$new_plan_name', description='$description', domains_limit=$domains_limit, websites_limit=$websites_limit, disk_limit='$disk_limit', inodes_limit=$inodes_limit, db_limit=$db_limit, cpu=$cpu, ram='$ram', docker_image='$docker_image', bandwidth=$bandwidth, storage_file='$storage_file' WHERE id='$plan_id';"

  mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$sql"
  if [ $? -eq 0 ]; then

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
          echo "Plan ID '$plan_id' has been updated. You currently have $count users on this plan. To apply new limits, execute the following command: opencli plan-apply $plan_id --all"
          check_if_we_need_to_edit_docker_containers        
      fi
    
  else
    echo "ERROR: Failed to update plan id '$plan_id'"
    exit 1
  fi

}



check_cpu_cores() {
  local available_cores=$(nproc)
  
  if [ "$cpu" -gt "$available_cores" ]; then
    echo "ERROR: Insufficient CPU cores. Required: ${cpu}, Available: ${available_cores}"
    exit 1
  fi
}

check_available_ram() {
  local available_ram=$(free -g | awk '/^Mem:/{print $2}')
  if [ "$ram" -gt "$available_ram" ]; then
    echo "ERROR: Insufficient RAM. Required: ${ram}GB, Available: ${available_ram}GB"
    exit 1
  fi
}

check_plan_exists() {
  local id="$1"
  local sql="SELECT id FROM plans WHERE id='$id';"
  local result=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$sql")
  echo "$result"
}

if [ "$#" -lt 13 ]; then
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

check_cpu_cores "$cpu"
check_available_ram "$ram"

if [ "$docker_image" == "nginx" ]; then
  docker_image="openpanel_nginx"
elif [ "$docker_image" == "litespeed" ]; then
  docker_image="openpanel_litespeed"
elif [ "$docker_image" == "apache" ]; then
  docker_image="openpanel_apache"
else
  docker_image="${11}"
fi

existing_plan=$(check_plan_exists "$plan_id")
if [ -z "$existing_plan" ]; then
  echo "Plan with id '$plan_id' does not exist."
  exit 1
fi

update_plan "$plan_id" "$new_plan_name" "$description" "$domains_limit" "$websites_limit" "$disk_limit" "$inodes_limit" "$db_limit" "$cpu" "$ram" "$docker_image" "$bandwidth" "$storage_file"
