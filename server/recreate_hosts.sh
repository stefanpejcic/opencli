#!/bin/bash
################################################################################
# Script Name: server/recreate_hosts.sh
# Description: Populates /etc/hosts with containers and their private IPs.
# Usage: opencli server-recreate_hosts
# Author: Stefan Pejcic
# Created: 16.08.2024
# Last Modified: 16.08.2024
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

HOSTS_FILE="/etc/hosts"

# Clear all existing entries
grep -v 'docker-container' $HOSTS_FILE > "${HOSTS_FILE}.tmp"

# Loop through each container name
for container in $(docker ps --format '{{.Names}}'); do 
   
    # Extract the first IP address
    ip=$(docker inspect $container | jq -r '.[0].NetworkSettings.Networks | .[] | .IPAddress' | head -n 1)
    
    # Check if the IP address is not empty
    if [ ! -z "$ip" ]; then
        # Append the IP and container name to the temporary hosts file
        echo "$ip $container # docker-container" >> "${HOSTS_FILE}.tmp"
    fi
done

# now move to /etc/host
mv "${HOSTS_FILE}.tmp" $HOSTS_FILE
chmod 644 $HOSTS_FILE




