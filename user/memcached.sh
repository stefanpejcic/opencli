#!/bin/bash
################################################################################
# Script Name: user/memcached.sh
# Description: Check and enable/disable Memcached for user.
# Usage: opencli user-memcached [check|enable|disable] <USERNAME>
# Docs: https://docs.openpanel.co/docs/admin/scripts/users#memcached
# Author: Stefan Pejcic
# Created: 01.10.2023
# Last Modified: 23.01.2024
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

# Function to print usage
print_usage() {
    echo "Usage: opencli user-memcached <check|enable|disable> <user>"
    exit 1
}

# Check if arguments are provided
if [ $# -ne 2 ]; then
    print_usage
fi

# Parse command-line options
action=$1
container_name=$2

# Check if the action is valid
if [[ "$action" != "check" && "$action" != "enable" && "$action" != "disable" ]]; then
    print_usage
fi

# Run the action inside the Docker container
case $action in
    check)
        response=$(docker exec "$container_name" service memcached status 2>&1)
        # Check if checking status was successful
        if [ $? -eq 0 ]; then
            echo "Memcached is running in container $container_name."
        else
            if [[ $response == *"unrecognized service"* ]]; then
                echo "Memcached is not installed in container $container_name."
            else
                echo "Failed to check Memcached status in container $container_name. Response: $response"
            fi
        fi
        ;;
    enable)
        response=$(docker exec "$container_name" service memcached start 2>&1)
        # Check if enable was successful
        if [ $? -eq 0 ]; then
            echo "Memcached enabled successfully in container $container_name."
        else
            if [[ $response == *"unrecognized service"* ]]; then
                echo "Memcached is not installed in container $container_name."
            else
                echo "Failed to enable Memcached in container $container_name. Response: $response"
            fi
        fi
        ;;
    disable)
        response=$(docker exec "$container_name" service memcached stop 2>&1)
        # Check if enable was successful
        if [ $? -eq 0 ]; then
            echo "Memcached disabled successfully in container $container_name."
        else
            if [[ $response == *"unrecognized service"* ]]; then
                echo "Memcached is not installed in container $container_name."
            else
                echo "Failed to disable Memcached in container $container_name. Response: $response"
            fi
        fi
        ;;
    *)
        print_usage
        ;;
esac
