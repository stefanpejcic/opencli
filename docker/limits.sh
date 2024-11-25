#!/bin/bash
################################################################################
# Script Name: limits.sh
# Description: Set global docker limits for all containers combined.
# Usage: opencli docker-limits [--apply | --apply SIZE | --read]
# Author: Stefan Pejcic
# Created: 09.05.2024
# Last Modified: 25.11.2024
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

# Function to read config values from panel.config file
read_config() {
    config_file="/etc/openpanel/openpanel/conf/openpanel.config"
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
    DISK_LIMIT=$(df --block-size=1G /dev/loop0 | awk 'NR==2 {print $2}' | sed 's/G//') # GB!
    else
        echo "Error: Config file $config_file not found."
        exit 1
    fi
}


apply_new_disk_limit_for_docker() {
    new_limit="$1"
    if [[ "$new_limit" =~ ^[0-9]+$ ]]; then
        if [ "$new_limit" -gt "$DISK_LIMIT" ]; then
            echo "- Storage allocated to Docker: $DISK_LIMIT GB"
            echo "- New limit defined for Docker: $new_limit GB"
            DIFF=$((new_limit - DISK_LIMIT))
            echo ""
            echo "Starting increasing the storage file /var/lib/docker.img for $DIFF GB"
            echo "Please wait.."
            echo "if this process gets interupted and Docker is not working, continue it from the terminal with command: 'opencli docker-limits --apply $new_limit'"
            echo ""
            echo "STEP 1. - Stop Docker service"
            service docker stop > /dev/null 2>&1

            echo "STEP 2. - Check if loop device is correctly set up"
            initial_size=$(stat --format="%s" /var/lib/docker.img)
            echo "STEP 3. - Check initial size of /var/lib/docker.img: $initial_size bytes"
            dd if=/dev/zero bs=1G count=$new_limit of=/var/lib/docker.img status=progress > /dev/null 2>&1
            final_size=$(stat --format="%s" /var/lib/docker.img)
            echo "STEP 3. - Check final size of /var/lib/docker.img: $final_size bytes"
            # Compare initial and final sizes
            if [ "$final_size" -gt "$initial_size" ]; then
                echo -e "File size successfully increased by $DIFF GB."
            else
                echo -e "Error: File size not increased as expected. Please contact support."
                exit 1
            fi
            echo "STEP 4. - Check if loop device is correctly set up"
            losetup -c /dev/loop0 > /dev/null 2>&1
            echo "STEP 5. - Resize the file system"
            xfs_growfs /var/lib/docker > /dev/null 2>&1
            echo "STEP 6. - Start Docker service"
            service docker start > /dev/null 2>&1
            echo "✔ Storage increase complete and Docker service restarted."
            exit 0
            
        else
            echo "✘ Error: storage size can not be decreased for Docker!"
            exit 1
        fi
        echo "✘ Error: storage size must be defined as a number!"
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
  "experimental": true,
  "storage-driver": "devicemapper",
  "cgroup-parent": "docker_limit.slice",
  "log-driver": "local",
  "log-opts": {
    "max-size": "5m"
  }
}
EOF

    sudo systemctl daemon-reload
    sudo systemctl start docker_limit.slice
    systemctl restart docker
    echo "✔ Docker limits updated successfully"
}

# Main script logic
if [ "$1" == "--apply" ]; then
    read_config
    if [[ "$2" =~ ^[0-9]+$ ]]; then
        apply_new_disk_limit_for_docker $2
    else
        apply_config
    fi
    exit 0
elif [ "$1" == "--read" ]; then
    read_config
    echo "[DOCKER]"
    echo "max_ram=$RAM_PERCENTAGE"
    echo "max_cpu=$CPU_PERCENTAGE"
    echo "max_disk=$DISK_LIMIT"
    exit 0
else
    echo "Usage: $0 [--apply | --read]"
    exit 1
fi
