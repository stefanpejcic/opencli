#!/bin/bash
################################################################################
# Script Name: nginx/update_vhosts.sh
# Description: Replace private IP address in all nginx configuration files (domains) for the user with its username *(added in 0.2.5)
# Usage: opencli nginx-update_vhosts <username> [-nginx-reload]
# Author: Stefan Pejcic
# Created: 01.11.2023
# Last Modified: 15.08.2024
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


nginx_reload=false  # Default to not reload Nginx
container_name=""

# basic usage information
usage() {
  echo "Usage: $0 <container_name> [--nginx-reload]"
  exit 1
}

# Check if at least one argument is provided
if [ "$#" -lt 1 ]; then
  usage
fi

# Parse the arguments
while [ "$#" -gt 0 ]; do
  case "$1" in
    --nginx-reload)
      nginx_reload=true
      shift
      ;;
    *)
      # Assuming this is the container name
      if [ -z "$container_name" ]; then
        container_name="$1"
      else
        usage
      fi
      shift
      ;;
  esac
done

# Check if a container name is provided
if [ -z "$container_name" ]; then
  usage
fi


# Check if the container exists
if ! docker inspect -f '{{.State.Running}}' "$container_name" &> /dev/null; then
  echo "Container '$container_name' does not exist or is not running."
  exit 1
fi

# Run the command to get the list of domains
domains=$(opencli domains-user "$container_name")

# Check if the command was successful
if [ $? -ne 0 ]; then
  echo "Failed to retrieve the list of domains."
  exit 1
fi

# Loop through each domain and update its Nginx configuration
while read -r domain; do
  nginx_conf_file="/etc/nginx/sites-available/$domain.conf"

  if [ -f "$nginx_conf_file" ]; then
    # Replace 'proxy_pass http(s)://<IP>;' with 'proxy_pass http(s)://<CONTAINER_NAME>;'
    sed -i "s/proxy_pass http:\/\/[0-9.]\+;/proxy_pass http:\/\/$container_name;/g; s/proxy_pass https:\/\/[0-9.]\+;/proxy_pass https:\/\/$container_name;/g" "$nginx_conf_file"

    # Replace 'listen <IP>:80;' with 'listen 80;'
    #########sed -i -E 's/listen [0-9]+\.[0-9]+\.[0-9]+\.[0-9]+:80;/listen 80;/' "$nginx_conf_file"
    
    # Replace 'listen <IP>:443 ssl http2;' with 'listen 443 ssl http2;'
    #########sed -i -E 's/listen [0-9]+\.[0-9]+\.[0-9]+\.[0-9]+:443 ssl http2;/listen 443 ssl http2;/' "$nginx_conf_file"
    
    # Replace 'listen <IP>; (without port)' with 'listen 80;'
    #########sed -i -E 's/listen [0-9]+\.[0-9]+\.[0-9]+\.[0-9]+;/listen 80;/' "$nginx_conf_file"
    
        
    echo "Updated Nginx configuration for domain: $domain"
  else
    echo "Nginx configuration file not found for domain: $domain"
  fi
done <<< "$domains"

# Reload Nginx if the --nginx-reload flag is provided
if [ "$nginx_reload" = true ]; then
  docker exec nginx nginx -s reload
  echo "Nginx configuration updated and reloaded."
else
  echo "Nginx configuration updated. To apply the changes, use '--nginx-reload' flag."
fi
