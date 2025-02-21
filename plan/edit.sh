#!/bin/bash
################################################################################
# Script Name: plan/edit.sh
# Description: Edit an existing hosting plan (Package) and modify its parameters.
# Usage: opencli plan-edit plan_id new_plan_name new_description new_email_limit new_ftp_limit new_domains_limit new_websites_limit new_disk_limit new_inodes_limit new_db_limit new_cpu new_ram new_docker_image new_bandwidth
# Example: opencli plan-edit 1 sad_se_zove_ovako "novi plan skroz" 0 0 0 0 10 500000 1 1 1 openpanel_nginx 500
# Author: Radovan Jecmenica
# Created: 10.04.2024
# Last Modified: 21.02.2025
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
source /usr/local/opencli/db.sh

flags=()

DEBUG=false


# Apply rate limit using tc command for the gateway of existing Docker network
edit_docker_network() {
    local name="$1"
    local bandwidth="$2"
    gateway_interface=$(docker network inspect "$name" -f '{{(index .IPAM.Config 0).Gateway}}')
    tc qdisc change dev "$gateway_interface" root tbf rate "$bandwidth"mbit burst "$bandwidth"mbit latency 3ms
}





check_if_we_need_to_edit_docker_containers() {

if [ "$old_cpu" == "$cpu" ] && [ "$old_ram" == "$ram" ]; then
    if [ "$DEBUG" = true ]; then
        echo "DEBUG: CPU & RAM limits are not changed."
    fi
elif [ "$old_cpu" != "$cpu" ] && [ "$old_ram" != "$ram" ]; then
    if [ "$DEBUG" = true ]; then
        echo "DEBUG: Both CPU or RAM limits are changed, applying new limits."
    fi
    flags+=( "--cpu" )
    flags+=( "--ram" )
elif [ "$old_cpu" != "$cpu" ] && [ "$old_ram" == "$ram" ]; then
    if [ "$DEBUG" = true ]; then
        echo "DEBUG: CPU limits are changed."
    fi
    flags+=( "--cpu" )
elif [ "$old_cpu" == "$cpu" ] && [ "$old_ram" != "$ram" ]; then
    if [ "$DEBUG" = true ]; then
        echo "DEBUG: RAM limits are changed."
    fi
    flags+=( "--ram" )
fi

# BANDWIDTH CHANGE OR PLAN NAME CHANGE
if [ "$old_bandwidth" == "$bandwidth" ] && [ "$old_plan_name" == "$new_plan_name" ]; then
    if [ "$DEBUG" = true ]; then
        echo "DEBUG: Port speed and plan name have not changed, skipping renaming docker network."
    fi
elif [ "$old_bandwidth" != "$bandwidth" ] && [ "$old_plan_name" == "$new_plan_name" ]; then
    if [ "$DEBUG" = true ]; then
        echo "DEBUG: Port speed limit is changed, applying new bandwidth limit to the docker network."
    fi
    edit_docker_network "$old_plan_name" "$bandwidth"
elif [ "$old_plan_name" != "$new_plan_name" ]; then
    if [ "$DEBUG" = true ]; then
        echo "DEBUG: Plan name is changed, renaming docker network is not possible, so creating new network, detaching existing docker containers from old network and atttach to new one."
    fi
    #CREATE NEW NETWORK, REMOVE PREVIOUS AND REATACH ALL CONTAINERS
    flags+=( "--net" )
fi
}









# Function to update values in the database
update_plan() {
  local plan_id="$1"

  # Get old paln data, and if different, we will initiate the `opencli plan-apply` script
  sql="SELECT name, disk_limit, inodes_limit, cpu, ram, bandwidth FROM plans WHERE id='$plan_id'"
  result=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -N -e "$sql")
  
  old_plan_name=$(echo "$result" | awk '{print $1}')
  int_old_disk_limit=$(echo "$result" | awk '{print $2}')
  old_inodes_limit=$(echo "$result" | awk '{print $4}')
  old_cpu=$(echo "$result" | awk '{print $5}')
  old_ram=$(echo "$result" | awk '{print $6}')
  old_bandwidth=$(echo "$result" | awk '{print $7}')
  
  new_plan_name="$2"
  description="$3"
  ftp_limit="$4"
  emails_limit="$5"
  domains_limit="$6"
  websites_limit="$7"
  int_disk_limit="$8"
  inodes_limit="$9"
  db_limit="${10}"
  cpu="${11}"
  int_ram="${12}"
  docker_image="${13}"
  bandwidth="${14}"

  # Format disk_limit with 'GB' 
  disk_limit="${int_disk_limit} GB"
  
  # format without GB for old limits
  old_disk_limit="${int_old_disk_limit} GB"
  int_old_ram=${old_ram%"g"}
  
  # Ensure inodes_limit is not less than 500000
  if [ "$inodes_limit" -lt 250000 ]; then
    if [ "$DEBUG" = true ]; then
        echo "DEBUG: Inodes limit can not be lower than 250k."
    fi
      inodes_limit=250000
  fi

  # Format ram with 'g' at the end
  ram="${ram}g"


if [ "$DEBUG" = true ]; then
  echo "+===================================+"
  echo "| PLAN ID: $plan_id"
  echo "+===================================+"

  echo "Old Plan Name:    $old_plan_name"
  echo "Old Disk Limit:   $old_disk_limit"
  echo "Old Inodes Limit: $old_inodes_limit"
  echo "Old CPU:          $old_cpu"
  echo "Old RAM:          $old_ram"
  echo "Old Bandwidth:    $old_bandwidth"
  echo "+===================================+"
  echo "New plan information:"
  echo "Name:             $new_plan_name"
  echo "Description:      $description"
  echo "Disk limit:       $disk_limit"
  echo "Inodes limit:     $inodes_limit"
  echo "CPU:              $cpu cores"
  echo "RAM:              $ram"
  echo "Docker image:     $docker_image"
  echo "Bandwidth:        $bandwidth"
  echo "FTP accounts:     $ftp_limit"
  echo "Email accounts:   $emails_limit"
  echo "Total domains:    $domains_limit"
  echo "Total websites:   $websites_limit"
  echo "Total databases:  $db_limit"
  echo "+===================================+"
fi




### contruct opencli plan-apply command if needed!


if [ "$docker_image" = "None" ]; then
  local sql="UPDATE plans SET name='$new_plan_name', description='$description', ftp_limit=$ftp_limit, email_limit=$emails_limit, domains_limit=$domains_limit, websites_limit=$websites_limit, disk_limit='$disk_limit', inodes_limit=$inodes_limit, db_limit=$db_limit, cpu=$cpu, ram='$ram', bandwidth=$bandwidth WHERE id='$plan_id';"
else
  local sql="UPDATE plans SET name='$new_plan_name', description='$description', ftp_limit=$ftp_limit, email_limit=$emails_limit, domains_limit=$domains_limit, websites_limit=$websites_limit, disk_limit='$disk_limit', inodes_limit=$inodes_limit, db_limit=$db_limit, cpu=$cpu, ram='$ram', docker_image='$docker_image', bandwidth=$bandwidth WHERE id='$plan_id';"
fi
 
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
          echo "Successfully updated plan id $plan_id"
      else    
          check_if_we_need_to_edit_docker_containers
            # do it!
            if [ ${#flags[@]} -gt 0 ]; then
                echo "Plan ID $plan_id updated successfully. There are currently $count users on this plan. Applying new limits to all users on this plan:"
                echo ""
                echo "Even if you leave this page, the process will continue. You can track progress using the command:"
                timestamp=$(date +"%Y%m%d_%H%M%S")
                echo "tail -f /tmp/opencli_plan_apply_$timestamp.log"
                if [ "$DEBUG" = true ]; then
                    echo "DEBUG: Running command: opencli plan-apply $plan_id ${flags[@]} --all --debug"
                    nohup opencli plan-apply $plan_id ${flags[@]} --all --debug > /tmp/opencli_plan_apply_$timestamp.log 2>&1 &
                else
                    nohup opencli plan-apply $plan_id ${flags[@]} --all > /tmp/opencli_plan_apply_$timestamp.log 2>&1 &
                fi
            else
                echo "Successfully updated plan id $plan_id. You currently have $count users on this plan. New limits have been applied."
            fi
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
    exit 0
  fi
}

check_available_ram() {
  local available_ram=$(free -g | awk '/^Mem:/{print $2}')
  if [ "$ram" -gt "$available_ram" ]; then
    echo "ERROR: Insufficient RAM. Required: ${ram}GB, Available: ${available_ram}GB"
    exit 0
  fi
}

check_plan_exists() {
  local id="$1"
  local sql="SELECT id FROM plans WHERE id='$id';"
  local result=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$sql")
  echo "$result"
}

if [ "$#" -lt 13 ]; then
    echo "ERROR: Usage: opencli $script_name plan_id new_plan_name description domains_limit websites_limit disk_limit inodes_limit db_limit cpu ram docker_image bandwidth"
    exit 1
fi








# Initialize default values
plan_id=""
new_plan_name=""
description=""
emails_limit=""
ftp_limit=""
domains_limit=""
websites_limit=""
disk_limit=""
inodes_limit=""
db_limit=""
cpu=""
ram=""
docker_image=""
bandwidth=""

# opencli plan-edit --debug id=1 name="Pro Plan" description="A professional plan" emails=500 ftp=100 domains=10 websites=5 disk=50 inodes=1000000 databases=20 cpu=4 ram=1 docker_image="nginx:latest" bandwidth=100
for arg in "$@"; do
  case $arg in
    --debug)
        DEBUG=true
      ;;
    id=*)
      plan_id="${arg#*=}"
      ;;
    name=*)
      new_plan_name="${arg#*=}"
      ;;
    description=*)
      description="${arg#*=}"
      ;;
    emails=*)
      emails_limit="${arg#*=}"
      ;;
    ftp=*)
      ftp_limit="${arg#*=}"
      ;;
    domains=*)
      domains_limit="${arg#*=}"
      ;;
    websites=*)
      websites_limit="${arg#*=}"
      ;;
    disk=*)
      disk_limit="${arg#*=}"
      ;;
    inodes=*)
      inodes_limit="${arg#*=}"
      ;;
    databases=*)
      db_limit="${arg#*=}"
      ;;
    cpu=*)
      cpu="${arg#*=}"
      ;;
    ram=*)
      ram="${arg#*=}"
      ;;
    docker_image=*)
      docker_image="${arg#*=}"
      ;;
    bandwidth=*)
      bandwidth="${arg#*=}"
      ;;
    *)
      echo "Unknown argument: $arg"
      ;;
  esac
done





check_cpu_cores "$cpu"
check_available_ram "$ram"

if [ "$docker_image" == "nginx" ]; then
  docker_image="openpanel/nginx"
elif [ "$docker_image" == "litespeed" ]; then
  docker_image="openpanel/litespeed"
elif [ "$docker_image" == "apache" ]; then
  docker_image="openpanel/apache"
fi

existing_plan=$(check_plan_exists "$plan_id")
if [ -z "$existing_plan" ]; then
  echo "ERROR: Plan with id '$plan_id' does not exist."
  exit 1
fi

update_plan "$plan_id" "$new_plan_name" "$description" "$ftp_limit" "$emails_limit" "$domains_limit" "$websites_limit" "$disk_limit" "$inodes_limit" "$db_limit" "$cpu" "$ram" "$docker_image" "$bandwidth"
