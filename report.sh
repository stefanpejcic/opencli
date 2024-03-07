#!/bin/bash

# Create directory if it doesn't exist
output_dir="/usr/local/admin/logs/reports"
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

# Function to display UFW rules if --ufw flag is provided
run_ufw_rules() {
  echo "=== Firewall Rules ===" >> "$output_file"
  run_command "cat /etc/ufw/user.rules" "Server IPv4 Firewall Rules"
}

# Function to check the status of services
check_services_status() {
  echo "=== Services Status ===" >> "$output_file"
  run_command "systemctl status nginx" "Nginx Status"
  run_command "systemctl status docker" "Docker Status"
  run_command "systemctl status mysql" "MySQL Status"
  run_command "systemctl status ufw" "UFW Status"
  run_command "systemctl status named" "BIND9 Status"
  run_command "systemctl status admin" "Admin Service Status"
  run_command "systemctl status panel" "Panel Service Status"
}

# Function to display OpenPanel settings
display_openpanel_settings() {
  echo "=== OpenPanel Settings ===" >> "$output_file"
  run_command "cat /usr/local/panel/conf/panel.config" "OpenPanel Configuration file:"
}

# Function to display MySQL information
display_mysql_information() {
  echo "=== MySQL Information ===" >> "$output_file"
  run_command "cat /usr/local/admin/config.json" "MySQL login information for OpenPanel and OpenAdmin services"
  run_command "cat /usr/local/admin/db.cnf" "MySQL login information for OpenCLI scripts"
}

# Default values
cli_flag=false
ufw_flag=false
upload_flag=false

# Parse command line arguments
for arg in "$@"; do
  if [ "$arg" = "--cli" ]; then
    cli_flag=true
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
output_dir="/usr/local/admin/static/reports"
mkdir -p "$output_dir"

# Collect system information
os_info=$(awk -F= '/^(NAME|VERSION_ID)/{gsub(/"/, "", $2); printf("%s ", $2)}' /etc/os-release)
run_command "echo $os_info" "OS"
run_command "uptime" "Uptime Information"
run_command "free -h" "Memory Information"
run_command "df -h" "Disk Information"

# Collect application information
run_command "opencli v" "OpenPanel version"
run_command "mysql --version" "MySQL Version"
run_command "python3 --version" "Python Version"
run_command "docker info" "Docker Information"

# Run OpenCLI commands if --cli flag is provided
if [ "$cli_flag" = true ]; then
  run_opencli
fi

# Display Firewall Rules if --ufw flag is provided
if [ "$ufw_flag" = true ]; then
  run_ufw_rules
fi

# Display OpenPanel settings
display_openpanel_settings

# Display MySQL information
display_mysql_information

# Check the status of services
check_services_status

if [ "$upload_flag" = true ]; then
  # Use curl to upload the file and capture the response
  response=$(curl -F "file=@$output_file" https://support.openpanel.co/opencli_server_info.php 2>/dev/null)

  # Extract the link from the response
  LINKHERE=$(echo $response | grep -o 'http[s]\?://[^ ]*')

  # Display the link to the user
  echo -e "Information collected successfully. Please provide the following link to the support team:\n$LINKHERE"
else
  # Print a message about the output file
  echo -e "Information collected successfully. Please provide the following file to the support team:\n$output_file"
fi

exit 0
