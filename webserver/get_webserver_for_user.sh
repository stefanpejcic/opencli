#!/bin/bash
################################################################################
# Script Name: webserver/get_webserver_for_user.sh
# Description: View cached or check the installed webserver inside user container.
# Usage: opencli webserver-get_webserver_for_user <USERNAME>
#        opencli webserver-get_webserver_for_user <USERNAME> --update
# Author: Stefan Pejcic
# Created: 01.10.2023
# Last Modified: 15.11.2023
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

# Function to determine the current web server inside the user's container
determine_web_server() {
    # Check for Apache inside the container
    if docker exec "$username" which apache2 &> /dev/null; then
        echo "apache"
    # Check for Nginx inside the container
    elif docker exec "$username" which nginx &> /dev/null; then
        echo "nginx"
    else
        echo "unknown"
    fi
}

# Check if the username is provided as a command-line argument
if [ $# -lt 1 ]; then
    echo "Usage: $0 <username> [--update]"
    exit 1
fi

# Get the username from the command-line argument
username="$1"

# Construct the path to the configuration file
config_file="/usr/local/panel/core/users/$username/server_config.yml"

# Check if the --update flag is provided
if [ "$2" == "--update" ]; then
    # Determine the current web server
    current_web_server=$(determine_web_server)
    
    if [ "$current_web_server" == "unknown" ]; then
        echo "Unable to determine the web server in the container named $username."
        exit 1
    fi
    
    # Check if the file exists
    if [ -f "$config_file" ]; then
        # Update the web_server value in the configuration file
        sed -i "s/web_server:.*/web_server: $current_web_server/" "$config_file"
        echo "Web Server for user $username updated to: $current_web_server"
    else
        echo "Configuration file not found for user $username"
    fi
else
    # Check if the file exists
    if [ -f "$config_file" ]; then
        # Use grep and awk to extract the value of web_server
        web_server=$(grep "web_server:" "$config_file" | awk '{print $2}')
        
        # Check if web_server is not empty
        if [ -n "$web_server" ]; then
            echo "Web Server for user $username: $web_server"
        else
            echo "Web Server not found for user $username"
        fi
    else
        echo "Configuration file not found for user $username"
    fi
fi
