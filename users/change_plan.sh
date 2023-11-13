#!/bin/bash

# Check if the correct number of parameters is provided
if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <container_name> <new_plan_id>"
    exit 1
fi

container_name=$1
new_plan_id=$2

# MySQL database configuration
config_file="/usr/local/admin/db.cnf"

# Check if the config file exists
if [ ! -f "$config_file" ]; then
    echo "Config file $config_file not found."
    exit 1
fi

mysql_database="panel"

# Function to fetch the current plan ID for the container
get_current_plan_id() {
    local container="$1"
    local query="SELECT plan_id FROM users WHERE username = '$container'"
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$query"
}

# Function to fetch plan limits for a given plan ID
get_plan_limits() {
    local plan_id="$1"
    local query="SELECT cpu, ram, docker_image, disk_limit, inodes_limit, bandwidth FROM plans WHERE id = '$plan_id'"
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$query"
}

# Fetch current plan ID for the container
current_plan_id=$(get_current_plan_id "$container_name")

# Check if the container exists
if [ -z "$current_plan_id" ]; then
    echo "Error: Container '$container_name' not found in the database."
    exit 1
fi

# Fetch limits for the current plan
current_plan_limits=$(get_plan_limits "$current_plan_id")

# Check if the current plan limits were retrieved
if [ -z "$current_plan_limits" ]; then
    echo "Error: Unable to fetch limits for the current plan ('$current_plan_id')."
    exit 1
fi

# Fetch limits for the new plan
new_plan_limits=$(get_plan_limits "$new_plan_id")

# Check if the new plan limits were retrieved
if [ -z "$new_plan_limits" ]; then
    echo "Error: Unable to fetch limits for the new plan ('$new_plan_id')."
    exit 1
fi

# Compare limits and list the differences
diff_output=$(diff -u <(echo "$current_plan_limits") <(echo "$new_plan_limits"))

# Display differences with column names
echo "Differences in plan limits for container '$container_name':"
while read -r line; do
    if [[ $line =~ ^[@+\-] ]]; then
        # Parse the line to get column name and values
        column_name=$(echo "$line" | awk -F': ' '{print $1}' | sed 's/^[+-] //')
        current_value=$(echo "$line" | grep '^-' | awk -F': ' '{print $2}')
        new_value=$(echo "$line" | grep '^\+' | awk -F': ' '{print $2}')

        # Perform actions based on the column name and values
        case "$column_name" in
            "cpu")
                echo "Difference in CPU: $current_value to $new_value"
                # Execute command for CPU difference
                docker update --cpus="$new_value" "$container_name"
                ;;
            "ram")
                echo "Difference in RAM: $current_value to $new_value"
                # Execute command for RAM difference
                docker update --memory="$new_value" "$container_name"
                ;;
            "docker_image")
                echo "Difference in Docker Image: $current_value to $new_value"
                # Execute command for Docker Image difference
                # Example: command_for_docker_image_difference
                ;;
            "disk_limit")
                echo "Difference in Disk Limit: $current_value to $new_value"
                # Execute command for Disk Limit difference
                # Example: command_for_disk_limit_difference
                ;;
            "inodes_limit")
                echo "Difference in Inodes Limit: $current_value to $new_value"
                # Execute command for Inodes Limit difference
                # Example: command_for_inodes_limit_difference
                ;;
            "bandwidth")
                echo "Difference in Bandwidth: $current_value to $new_value"
                # Execute command for Bandwidth difference
                # Example: command_for_bandwidth_difference
                ;;
            *)
                echo "Unknown difference in column: $column_name"
                ;;
        esac
    fi
done <<< "$diff_output"
