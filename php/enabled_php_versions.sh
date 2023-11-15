#!/bin/bash
################################################################################
# Script Name: php/enabled_php_versions.sh
# Description: View PHP versions installed for a specific user.
# Usage: opencli php-enabled_php_versions <username>
# Author: Stefan Pejcic
# Created: 07.10.2023
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

# Check if the correct number of arguments are provided
if [ "$#" -ne 1 ]; then
  echo "Usage: $0 <username>"
  exit 1
fi

container_name="$1"

# Check if the Docker container with the given name exists
if ! docker ps -a --format '{{.Names}}' | grep -q "^$container_name$"; then
  echo "Error: Docker container with the name '$container_name' does not exist."
  exit 1
fi

# Run the command to list installed PHP versions inside the Docker container,
# then filter out the "default" version
docker exec -it "$container_name" update-alternatives --list php | awk -F'/' '{print $NF}' | grep -v 'default'
