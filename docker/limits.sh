#!/bin/bash
################################################################################
# Script Name: limits.sh
# Description: Set global docker limits for all containers combined.
# Usage: opencli docker-limits [--apply | --read]
# Author: Stefan Pejcic
# Created: 09.05.2024
# Last Modified: 09.05.2024
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

# Function to read config values from panel.config file
read_config() {
    config_file="/usr/local/panel/conf/panel.config"
    if [ -f "$config_file" ]; then
        while IFS='=' read -r key value; do
            case "$key" in
                max_ram)
                    RAM_PERCENTAGE="$value"
                    ;;
                max_cpu)
                    CPU_PERCENTAGE="$value"
                    ;;
                *)
                    ;;
            esac
        done < "$config_file"
    else
        echo "Error: Config file $config_file not found."
        exit 1
    fi
}

# Function to create or update systemd slice file and Docker daemon configuration
apply_config() {
    # Get total RAM in bytes
    total_ram=$(grep MemTotal /proc/meminfo | awk '{print $2}')

    # Calculate % of RAM
    memory_limit=$(echo "scale=2; $total_ram * $RAM_PERCENTAGE / 100 / 1024 / 1024" | bc) # Convert to GB

    # Create the systemd slice file
    cat <<EOF | sudo tee /etc/systemd/system/docker_limit.slice
[Unit]
Description=Slice that limits docker resources
Before=slices.target

[Slice]
CPUAccounting=true
CPUQuota=${CPU_PERCENTAGE}%
MemoryAccounting=true
MemoryLimit=${memory_limit}G
EOF

    # Create or update the Docker daemon configuration file
    cat <<EOF | sudo tee /etc/docker/daemon.json
{
  "storage-driver": "devicemapper",
  "cgroup-parent": "docker_limit.slice"
}
EOF

    # Reload systemd daemon
    sudo systemctl daemon-reload

    # Start the docker_limit slice
    sudo systemctl start docker_limit.slice

    # Restart Docker
    systemctl restart docker

    # echo what the route expects:
    echo "Docker limits updated successfully"
}

# Main script logic
if [ "$1" == "--apply" ]; then
    read_config
    apply_config
    exit 0
elif [ "$1" == "--read" ]; then
    read_config
    echo "[DOCKER]"
    echo "max_ram=$RAM_PERCENTAGE"
    echo "max_cpu=$CPU_PERCENTAGE"
    exit 0
else
    echo "Usage: $0 [--apply | --read]"
    exit 1
fi
