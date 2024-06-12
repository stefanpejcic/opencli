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





# Check for CSF
if command -v csf >/dev/null 2>&1; then
    echo "Checking ConfigServer Firewall configuration.."
    echo ""
    FIREWALL="CSF"
# Check for UFW
elif command -v ufw >/dev/null 2>&1; then
    echo ""
    echo "Checking UFW configuration.."
    FIREWALL="UFW"

    # Export current UFW rules to ports.txt
    ufw status > ports.txt
    
else
    echo "Error: Neither CSF nor UFW are installed, all user ports will be exposed to the internet, without any protection."
    exit 1
fi








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


# Variable to track whether any ports were opened
ports_opened=0

if [ "$FIREWALL" = "CSF" ]; then
    CSF_CONF="/etc/csf/csf.conf"

    # Check if Docker port range is already open
    docker_ports_opened=$(grep "TCP_IN = .*32768:60999" "$CSF_CONF")
    if [ -z "$docker_ports_opened" ]; then
        # Open Docker port range
        sed -i 's/TCP_IN = "\(.*\)"/TCP_IN = "\1,32768:60999"/' "$CSF_CONF" # prepend ,
        echo "Docker port range (32768:60999) opened in CSF."
        ports_opened=1
    else
        echo "Docker port range (32768:60999) is already open in $CSF_CONF"
    fi     
elif [ "$FIREWALL" = "UFW" ]; then
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

    # Define the list of container ports to check and open manually 1 by 1 in ufw..
    container_ports=("22" "3306" "7681" "8080")
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

fi

# Restart UFW if ports were opened
if [ $ports_opened -eq 1 ]; then
    echo "Restarting $FIREWALL"
    if [ "$FIREWALL" = "UFW" ]; then 
        ufw reload
    elif [ "$FIREWALL" = "CSF" ]; then
        csf -r
    fi
fi
