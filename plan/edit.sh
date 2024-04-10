#!/bin/bash
################################################################################
# Script Name: plan/edit.sh
# Description: Edit an existing hosting plan (Package) and modify its parameters.
# Usage: opencli plan-edit old_plan_name new_plan_name new_description new_domains_limit new_websites_limit new_disk_limit new_inodes_limit new_db_limit new_cpu new_ram new_docker_image new_bandwidth new_storage_file
# Author: Radovan Jecmenica
# Created: 06.11.2023
# Last Modified: 17.01.2024
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

# DB
source /usr/local/admin/scripts/db.sh

# Function to update values in the database
update_plan() {
  local old_plan_name="$1"
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
  
  # Update the plan in the 'plans' table
  local sql="UPDATE plans SET name='$new_plan_name', description='$description', domains_limit=$domains_limit, websites_limit=$websites_limit, disk_limit='$disk_limit', inodes_limit=$inodes_limit, db_limit=$db_limit, cpu=$cpu, ram='$ram', docker_image='$docker_image', bandwidth=$bandwidth, storage_file='$storage_file' WHERE name='$old_plan_name';"

  mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$sql"
  if [ $? -eq 0 ]; then
    echo "Updated plan '$old_plan_name' to '$new_plan_name'"
  else
    echo "Failed to update plan '$old_plan_name' to '$new_plan_name'"
  fi
}

# Function to check if a plan exists
check_plan_exists() {
  local name="$1"
  local sql="SELECT name FROM plans WHERE name='$name';"
  local result=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$sql")
  echo "$result"
}

# Check for command-line arguments
if [ "$#" -ne 13 ]; then
    echo "Usage: opencli $script_name old_plan_name new_plan_name description domains_limit websites_limit disk_limit inodes_limit db_limit cpu ram docker_image bandwidth storage_file"
    exit 1
fi

# Capture command-line arguments
old_plan_name="$1"
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

# Check if the old plan exists in the database
existing_plan=$(check_plan_exists "$old_plan_name")
if [ -z "$existing_plan" ]; then
  echo "Old plan name '$old_plan_name' does not exist."
  exit 1
fi

# Call the update_plan function with the provided values
update_plan "$old_plan_name" "$new_plan_name" "$description" "$domains_limit" "$websites_limit" "$disk_limit" "$inodes_limit" "$db_limit" "$cpu" "$ram" "$docker_image" "$bandwidth" "$storage_file"
