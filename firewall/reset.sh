#!/bin/bash
################################################################################
# Script Name: firewall/reset.sh
# Description: Deletes all docker related ports from CSF/UFW and opens exposed ports.
#              Use: opencli firewall-reset
# Author: Stefan Pejcic
# Created: 01.11.2023
# Last Modified: 20.02.2025
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




function open_port_csf() {
    local port=$1
    local csf_conf="/etc/csf/csf.conf"
    
    # Check if port is already open
    port_opened=$(grep "TCP_IN = .*${port}" "$csf_conf")
    if [ -z "$port_opened" ]; then
        # Open port
        sed -i "s/TCP_IN = \"\(.*\)\"/TCP_IN = \"\1,${port}\"/" "$csf_conf"
        echo "Port ${port} opened in CSF."
        ports_opened=1
    else
        echo "Port ${port} is already open in CSF."
    fi
}



# Function to extract port number from a file
function extract_port_from_file() {
    local file_path=$1
    local pattern=$2
    local port=$(grep -Po "(?<=${pattern}[ =])\d+" "$file_path")
    echo "$port"
}


function open_out_port_csf() {
        port="3306"
        local csf_conf="/etc/csf/csf.conf"
        # Check if port is already open
        port_opened=$(grep "TCP_OUT = .*${port}" "$csf_conf")
        if [ -z "$port_opened" ]; then
            # Open port
            sed -i "s/TCP_OUT = \"\(.*\)\"/TCP_OUT = \"\1,${port}\"/" "$csf_conf"
            ports_opened=1
        fi
}



















ensure_jq_installed() {
    # Check if jq is installed
    if ! command -v jq &> /dev/null; then
        # Detect the package manager and install jq
        if command -v apt-get &> /dev/null; then
            sudo apt-get update > /dev/null 2>&1
            sudo apt-get install -y -qq jq > /dev/null 2>&1
        elif command -v yum &> /dev/null; then
            sudo yum install -y -q jq > /dev/null 2>&1
        elif command -v dnf &> /dev/null; then
            sudo dnf install -y -q jq > /dev/null 2>&1
        else
            echo "Error: No compatible package manager found. Please install jq manually and try again."
            exit 1
        fi

        # Check if installation was successful
        if ! command -v jq &> /dev/null; then
            echo "Error: jq installation failed. Please install jq manually and try again."
            exit 1
        fi
    fi
}



# Variable to track whether any ports were opened
ports_opened=0
echo "Opening ports:"
echo ""

# Check and open ports
if [ "$FIREWALL" = "CSF" ]; then

    # TODO: reuse install_csf() from https://github.com/stefanpejcic/OpenPanel/edit/main/version/0.2.3/INSTALL.sh
    
    open_port_csf 53 #dns
    open_port_csf 80 #http
    open_port_csf 443 #https
    
    ######for emails we wil add:
    # open_port_csf 25
    # open_port_csf 587
    # open_port_csf 465
    # open_port_csf 993

    open_port_csf 21 #ftp
    open_port_csf 21000-21010 #passive ftp

    
    open_port_csf $(extract_port_from_file "/etc/openpanel/openpanel/conf/openpanel.config" "port") #openpanel
    open_port_csf 2087
    open_port_csf $(extract_port_from_file "/etc/ssh/sshd_config" "Port") #ssh
    open_port_csf 32768:60999 #docker
    
    open_out_port_csf #mysql out
        
elif [ "$FIREWALL" = "UFW" ]; then
    ufw allow 80/tcp #http
    ufw allow 53  #dns
    ufw allow 443/tcp # https
    
    ufw allow 21/tcp #ftp
    ufw allow 21000-21010/tcp #passive ftp
    
    ######for emails we wil add:
    # ufw allow 25/tcp
    # ufw allow 587/tcp
    # ufw allow 465/tcp
    # ufw allow 993/tcp
    
    ufw allow $(extract_port_from_file "/etc/openpanel/openpanel/conf/openpanel.config" "port")/tcp #openpanel

    ensure_jq_installed
    
    # Step 1: List all container names
    container_names=$(opencli user-list --json | jq -r '.[].username')
    # Function to extract the host port from 'docker port' output for a specific container
    extract_host_port() {
        local container_name="$1"
        local port_number="$2"
        local host_port

get_user_info() {
    local user="$1"
    local query="SELECT id, server FROM users WHERE username = '${user}';"
    
    # Retrieve both id and context
    user_info=$(mysql -se "$query")
    
    # Extract user_id and context from the result
    user_id=$(echo "$user_info" | awk '{print $1}')
    context=$(echo "$user_info" | awk '{print $2}')
    
    echo "$user_id,$context"
}


result=$(get_user_info "$container_name")
user_id=$(echo "$result" | cut -d',' -f1)
context=$(echo "$result" | cut -d',' -f2)

#echo "User ID: $user_id"
#echo "Context: $context"



if [ -z "$user_id" ]; then
    echo "FATAL ERROR: user $container_name does not exist."
    exit 1
fi

        host_port=$(docker--context $context port "$container_name" | grep "${port_number}/tcp" | awk -F: '{print $2}' | awk '{print $1}')
        echo "$host_port"
    }

    echo ""
    echo "Opening docker ports for OpenPanel users:"
    echo ""

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

echo ""
