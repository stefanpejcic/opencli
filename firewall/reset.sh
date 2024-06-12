#!/bin/bash
################################################################################
# Script Name: firewall/reset.sh
# Description: Deletes all docker related ports from CSF/UFW and opens exposed ports.
#              Use: opencli firewall-reset
# Author: Stefan Pejcic
# Created: 01.11.2023
# Last Modified: 12.06.2024
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




# Export current UFW rules to ports.txt
ufw status > ports.txt

ensure_jq_installed() {
    # Check if jq is installed
    if ! command -v jq &> /dev/null; then
        # Install jq using apt
        sudo apt-get update > /dev/null 2>&1
        sudo apt-get install -y -qq jq > /dev/null 2>&1
        # Check if installation was successful
        if ! command -v jq &> /dev/null; then
            echo "Error: jq installation failed. Please install jq manually and try again."
            exit 1
        fi
    fi
}

ensure_jq_installed

# Step 1: List all container names
container_names=$(opencli user-list --json | jq -r '.[].username')
# Function to extract the host port from 'docker port' output for a specific container
extract_host_port() {
    local container_name="$1"
    local port_number="$2"
    local host_port
    host_port=$(docker port "$container_name" | grep "${port_number}/tcp" | awk -F: '{print $2}' | awk '{print $1}')
    echo "$host_port"
}

# Define the list of container ports to check and open
container_ports=("22" "3306" "7681" "8080")

# Variable to track whether any ports were opened
ports_opened=0


if [ "$FIREWALL" = "CSF" ]; then
    CSF_CONF="/etc/csf/csf.conf"
    
    # delete ALL docker ports from TCP_IN of csf conf
    remove_docker_ports() {
      local ports="$1"
      local new_ports=""
    
      # Iterate through the list of ports
      IFS=',' read -ra ADDR <<< "$ports"
      for port in "${ADDR[@]}"; do
        # Check if the port is not within the Docker port range
        if ! [[ "$port" =~ ^[0-9]+$ ]] || [ "$port" -lt 32768 ] || [ "$port" -gt 65535 ]; then
          # Append the port to the new ports list
          if [ -z "$new_ports" ]; then
            new_ports="$port"
          else
            new_ports="$new_ports,$port"
          fi
        fi
      done
    
      echo "$new_ports"
    }

    # Read the current TCP_IN setting
    TCP_IN=$(grep "^TCP_IN" $CSF_CONF | cut -d'=' -f2 | tr -d ' ')
    
    # Remove Docker-related ports from TCP_IN
    NEW_TCP_IN=$(remove_docker_ports "$TCP_IN")
    
    # Update the CSF configuration file with the new TCP_IN value
    sed -i "s/^TCP_IN = .*/TCP_IN = \"$NEW_TCP_IN\"/" $CSF_CONF

    echo "Docker-related ports have been removed from TCP_IN of csf.conf"
    # dont reload yet, we will do that later!
    #csf -r
fi

# Function to add a port to tcp_in for csf
add_csf_port() {
    CSF_CONF="/etc/csf/csf.conf"
    local PORT=$1

    if grep -q "TCP_IN.*$PORT" $CSF_CONF; then
        echo "Port $PORT is already in TCP_IN"
    else
        sudo sed -i "/^TCP_IN/ s/\"$/,$PORT\"/" $CSF_CONF
        echo "Port $PORT added to TCP_IN"
    fi
}


# Loop through the list of container names
for container_name in $container_names; do
    for port in "${container_ports[@]}"; do
        host_port=$(extract_host_port "$container_name" "$port")

        if [ -n "$host_port" ]; then
               
            if [ "$FIREWALL" = "UFW" ]; then
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
                
            elif [ "$FIREWALL" = "CSF" ]; then
                add_csf_port ${host_port}
            fi
            ports_opened=1
            
        else
            echo "Port ${port} not found in container ${container_name}"
        fi
    done
done


# Restart UFW if ports were opened
if [ $ports_opened -eq 1 ]; then
    echo "Restarting $FIREWALL"
    if [ "$FIREWALL" = "UFW" ]; then 
        ufw reload
    elif [ "$FIREWALL" = "CSF" ]; then
        csf -r
    fi
fi
