#!/bin/bash

# Function to run a command and print its output with a custom message
run_command() {
  echo "# $2:"
  $1
  echo
}

# Function to run OpenCLI commands if --cli flag is provided
run_opencli() {
  if [ "$cli_flag" = true ]; then
    echo "=== OpenCLI Information ==="
    run_command "opencli commands" "Available OpenCLI Commands"
  fi
}

# Parse command line arguments
cli_flag=false
while [ "$#" -gt 0 ]; do
  case "$1" in
    --cli)
      cli_flag=true
      shift
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done


# Create directory if it doesn't exist
output_dir="/usr/local/admin/static/reports"
mkdir -p "$output_dir"

# Collect system information
echo "=== System Information ==="
os_info=$(awk -F= '/^(NAME|VERSION_ID)/{gsub(/"/, "", $2); printf("%s ", $2)}' /etc/os-release)
run_command "echo $os_info" "OS"
run_command "uptime" "Uptime Information"
run_command "free -h" "Memory Information"
run_command "df -h" "Disk Information"

# Collect application information
echo "=== Application Information ==="
run_command "opencli v" "OpenPanel version"
run_command "mysql --version" "MySQL Version"
run_command "python3 --version" "Python Version"
run_command "docker info" "Docker Information"

# Run OpenCLI commands if --cli flag is provided
run_opencli

# Count the number of running Docker containers
docker_container_count=$(docker ps -q | wc -l)
echo "=== Number of Docker Containers ==="
echo "Running Containers: $docker_container_count"

# Save the information to a file
output_file="$output_dir/system_info_$(date +'%Y%m%d%H%M%S').txt"
exec > >(tee -a "$output_file") 2>&1

# Print a message about the output file
echo -e "\nInformation collected successfully. Please provide the following file to the support team:"
echo "$output_file"
