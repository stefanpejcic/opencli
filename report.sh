#!/bin/bash
################################################################################
# Script Name: report.sh
# Description: Generate a system report and send it to OpenPanel support team.
# Usage: opencli report
#        opencli report --public [--cli] [--csf|--ufw]
# Author: Stefan Pejcic
# Created: 07.10.2023
# Last Modified: 23.02.2025
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

# todo: ufw flag with firewallflag to cover also csf

# Create directory if it doesn't exist
output_dir="/var/log/openpanel/admin/reports"
mkdir -p "$output_dir"

output_file="$output_dir/system_info_$(date +'%Y%m%d%H%M%S').txt"

# Function to run a command and print its output with a custom message
run_command() {
  echo "# $2:" >> "$output_file"
  $1 >> "$output_file" 2>&1
  echo >> "$output_file"
}

# Function to run OpenCLI commands if --cli flag is provided
run_opencli() {
  echo "=== OpenCLI Information ===" >> "$output_file"
  run_command "opencli commands" "Available OpenCLI Commands"
}

run_ufw_rules() {
  echo "=== Firewall Rules ===" >> "$output_file"
  run_command "ufw status numbered" "Firewall Rules"
}

run_csf_rules() {
  echo "=== Firewall Rules ===" >> "$output_file"
  run_command "csf -l" "Firewall Rules"
}


# Function to check the status of services
check_services_status() {
  echo "=== Services Status ===" >> "$output_file"
  run_command "docker compose ls" "OpenPanel Stack"
  run_command "systemctl status admin" "OpenAdmin Service"
  run_command "systemctl status docker" "Docker Status"
  run_command "systemctl status csf" "ConfigServer Firewall Status"
}

# Function to display OpenPanel settings
display_openpanel_settings() {
  echo "=== OpenPanel Settings ===" >> "$output_file"
  run_command "cat /etc/openpanel/openpanel/conf/openpanel.config" "OpenPanel Configuration file"
}

# admin in 0.2.3
display_openadmin_settings() {
  echo "=== OpenAdmin Service ===" >> "$output_file"
  run_command "cat /etc/openpanel/openadmin/config/admin.ini" "OpenAdmin Configuration file"
  run_command "python3 -m pip list" "Installed PIP packages"
  run_command "service admin status" "Admin service status"
  run_command "tail -30 /var/log/openpanel/admin/error.log" "OpenAdmin error log"
}


# Function to display MySQL information
display_mysql_information() {
  echo "=== MySQL Information ===" >> "$output_file"
  run_command "docker logs --tail 30 openpanel_mysql" "openpanel_mysql docker container logs"
  run_command "cat /etc/openpanel/mysql/db.cnf" "MySQL login information for OpenCLI scripts"
}

# Default values
cli_flag=false
ufw_flag=false
csf_flag=false
upload_flag=false

# Parse command line arguments
for arg in "$@"; do
  if [ "$arg" = "--cli" ]; then
    cli_flag=true
  elif [ "$arg" = "--csf" ]; then
    csf_flag=true
  elif [ "$arg" = "--ufw" ]; then
    ufw_flag=true
  elif [ "$arg" = "--public" ]; then
    upload_flag=true
  else
    echo "Unknown option: $arg"
    exit 1
  fi
done

# Create directory if it doesn't exist
output_dir="/var/log/openpanel/admin/reports"
mkdir -p "$output_dir"

# Collect system information
os_info=$(awk -F= '/^(NAME|VERSION_ID)/{gsub(/"/, "", $2); printf("%s ", $2)}' /etc/os-release)
run_command "echo $os_info" "OS"
run_command "uptime" "Uptime Information"
run_command "free -h" "Memory Information"
run_command "df -h" "Disk Information"

# Collect application information
run_command "opencli --version" "OpenPanel version"
run_command "mysql --protocol=tcp --version" "MySQL Version"
run_command "python3 --version" "Python version"
run_command "docker info" "Docker Information"

# Run OpenCLI commands if --cli flag is provided
if [ "$cli_flag" = true ]; then
  run_opencli
fi

if [ "$csf_flag" = true ]; then
  run_csf_rules
fi

if [ "$ufw_flag" = true ]; then
  run_ufw_rules
fi

# Display OpenPanel settings
display_openpanel_settings

# Display OpenAdmin settings
display_openadmin_settings

# Display MySQL information
display_mysql_information

# Check the status of services
check_services_status

# Check users
for dir in /home/*; do
    file="$dir/docker-compose.yml"
    user=$(basename "$dir")
    if [[ -f "$file" ]]; then
      echo "Services for context: $user"
      docker --context=$user compose -f  $dir/docker-compose.yml config --services
    else
      echo "No services."
    fi
done



if [ "$upload_flag" = true ]; then
  response=$(curl -F "file=@$output_file" https://support.openpanel.org/opencli_server_info.php 2>/dev/null)
  if echo "$response" | grep -q "File upload failed."; then
    echo -e "Information collected successfully but uploading to support.openpanel.org failed. Please provide content from the following file to the support team:\n$output_file"
  else
    LINKHERE=$(echo "$response" | grep -o 'http[s]\?://[^ ]*')
    echo -e "Information collected successfully. Please provide the following link to the support team:\n$LINKHERE"
  fi
else
  # Print a message about the output file
  echo -e "Information collected successfully. Please provide content of the following file to the support team:\n$output_file"
fi

exit 0
