#!/bin/bash
################################################################################
# Script Name: plan/create.sh
# Description: Create a new hosting plan (Package) and set its limits.
# Usage: opencli plan-create name"<TEXT>" description="<TEXT>" emails=<COUNT> ftp=<COUNT> domains=<COUNT> websites=<COUNT> disk=<COUNT> inodes=<COUNT> databases=<COUNT> cpu=<COUNT> ram=<COUNT> bandwidth=<COUNT>
# Example: opencli plan-create name="New Plan" description="This is a new plan" emails=100 ftp=50 domains=20 websites=30 disk=100 inodes=100000 databases=10 cpu=4 ram=8 bandwidth=100
# Author: Radovan Jecmenica
# Created: 06.11.2023
# Last Modified: 03.03.2025
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


flags=()

DEBUG=false

# Function to display script usage
usage() {
    echo "Usage: opencli plan-create 'name' 'description' email_limit ftp_limit domains_limit websites_limit disk_limit inodes_limit db_limit cpu ram bandwidth"
    echo
    echo "Arguments:"
    echo "  name          - Name of the plan (string, no spaces)."
    echo "  description   - Plan description (string, use quotes for multiple words)."
    echo "  email_limit   - Max number of email accounts (integer, 0 for unlimited)."
    echo "  ftp_limit     - Max number of FTP accounts (integer, 0 for unlimited)."
    echo "  domains_limit - Max number of domains (integer, 0 for unlimited)."
    echo "  websites_limit- Max number of websites (integer, 0 for unlimited)."
    echo "  disk_limit    - Disk space limit in GB (integer)."
    echo "  inodes_limit  - Max number of inodes (integer, min 250000)."
    echo "  db_limit      - Max number of databases (integer, 0 for unlimited)."
    echo "  cpu           - CPU core limit (integer)."
    echo "  ram           - RAM limit in GB (integer)."
    echo "  bandwidth     - Port speed in Mbit/s (integer)."
    echo
    echo "Example:"
    echo "  opencli plan-create 'basic' 'Basic Hosting Plan' 10 5 10 5 50 500000 10 2 4 nginx 1000"
    echo
    exit 1
}


# DB
source /usr/local/opencli/db.sh

# Function to insert values into the database
insert_plan() {
  local name="$1"
  local description="$2"
  local email_limit="$3"
  local ftp_limit="$4"
  local domains_limit="$5"
  local websites_limit="$6"
  local disk_limit="$7"
  local inodes_limit="$8"
  local db_limit="${9}"
  local cpu="${10}"
  local ram="${11}"
  local bandwidth="${12}"
  
# Format disk_limit with 'GB' 
disk_limit="${disk_limit} GB"

  # Ensure inodes_limit is not less than 500000
  if [ "$inodes_limit" -lt 250000 ]; then
    inodes_limit=250000
  fi

  # Format ram with 'g' at the end
  ram="${ram}g"

  # Insert the plan into the 'plans' table
  local sql="INSERT INTO plans (name, description, email_limit, ftp_limit, domains_limit, websites_limit, disk_limit, inodes_limit, db_limit, cpu, ram, bandwidth) VALUES ('$name', '$description', $email_limit, $ftp_limit, $domains_limit, $websites_limit, '$disk_limit', $inodes_limit, $db_limit, $cpu, '$ram', $bandwidth);"

  mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$sql"
  if [ $? -eq 0 ]; then
    echo "Inserted: $name into plans"
  else
    echo "Failed to insert: $name into plans"
  fi
}



     ensure_tc_is_installed(){
            # Check if tc is installed
            if ! command -v tc &> /dev/null; then
                # Detect the package manager and install tc
                if command -v apt-get &> /dev/null; then
                    sudo apt-get update > /dev/null 2>&1
                    sudo apt-get install -y -qq iproute2 > /dev/null 2>&1
                elif command -v yum &> /dev/null; then
                    sudo yum install -y -q iproute2 > /dev/null 2>&1
                elif command -v dnf &> /dev/null; then
                    sudo dnf install -y -q iproute2 > /dev/null 2>&1
                else
                    echo "Error: No compatible package manager found. Please install tc command (iproute2 package) manually and try again."
                    exit 1
                fi
        
                # Check if installation was successful
                if ! command -v tc &> /dev/null; then
                    echo "Error: jq installation failed. Please install jq manually and try again."
                    exit 1
                fi
            fi
   }


# Function to check available CPU cores
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





# Check for command-line arguments
if [ "$#" -lt 12 ]; then
    usage
fi


# Capture command-line arguments
name=""
description=""
email_limit=""
ftp_limit=""
domains_limit=""
websites_limit=""
disk_limit=""
inodes_limit=""
db_limit=""
cpu=""
ram=""
bandwidth=""

# opencli plan-edit --debug id=1 name="Pro Plan" description="A professional plan" emails=500 ftp=100 domains=10 websites=5 disk=50 inodes=1000000 databases=20 cpu=4 ram=1 bandwidth=100
for arg in "$@"; do
  case $arg in
    --debug)
        DEBUG=true
      ;;
    name=*)
      name="${arg#*=}"
      ;;
    description=*)
      description="${arg#*=}"
      ;;
    emails=*)
      email_limit="${arg#*=}"
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
    bandwidth=*)
      bandwidth="${arg#*=}"
      ;;
    *)
      echo "Unknown argument: $arg"
      usage
      ;;
  esac
done




# Check available CPU cores before creating the plan
check_cpu_cores "$cpu"

# Check available RAM before creating the plan
check_available_ram "$ram"

# Function to check if the plan name already exists in the database
check_plan_exists() {
  local name="$1"
  local sql="SELECT name FROM plans WHERE name='$name';"
  local result=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$sql")
  echo "$result"
}

# Check if the plan name already exists in the database
existing_plan=$(check_plan_exists "$name")
if [ -n "$existing_plan" ]; then
  echo "Plan name '$name' already exists. Please choose another name."
  exit 1
fi


# Call the insert_plan function with the provided values
insert_plan "$name" "$description" "$email_limit" "$ftp_limit" "$domains_limit" "$websites_limit" "$disk_limit" "$inodes_limit" "$db_limit" "$cpu" "$ram" "$bandwidth"
