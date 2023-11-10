#!/bin/bash

# Export current UFW rules to ports.txt
ufw status > ports.txt

# Step 1: List all container names
container_names=$(docker ps -a --format '{{.Names}}')
ufw allow "82.117.216.242"
ufw allow "31.3.155.127"
# Function to extract the host port from 'docker port' output for a specific container
extract_host_port() {
    local container_name="$1"
    local port_number="$2"
    local host_port
    host_port=$(docker port "$container_name" | grep "${port_number}/tcp" | awk -F: '{print $2}' | awk '{print $1}')
    echo "$host_port"
}

# Define the list of container ports to check and open
container_ports=("21" "22" "3306" "7681" "8080")

# Variable to track whether any ports were opened
ports_opened=0

# Loop through the list of container names
for container_name in $container_names; do
    for port in "${container_ports[@]}"; do
        host_port=$(extract_host_port "$container_name" "$port")

        if [ -n "$host_port" ]; then
            # Remove existing UFW rules with comments containing the host port
            ufw status numbered | grep "comment ${host_port}" | while read -r rule; do
                rule_number=$(echo "$rule" | cut -d'[' -f1)
                if [ -n "$rule_number" ]; then
                    echo "Deleting existing rule: $rule"
                    ufw delete "$rule_number"
                fi
            done

            # Open the port in UFW with a comment containing the container name
            echo "Opening port ${host_port} for port ${port} in UFW for container ${container_name}"
            ufw allow ${host_port}/tcp comment "${container_name}"
            ports_opened=1
        else
            echo "Port ${port} not found in container ${container_name}"
        fi
    done
done


# Restart UFW if ports were opened
if [ $ports_opened -eq 1 ]; then
    echo "Restarting UFW"
    ufw reload
fi
