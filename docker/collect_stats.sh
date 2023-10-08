#!/bin/bash
################################################################################
# Script Name: collect_stats.sh
# Description: Collect docker usage information using docker stats command and store in json files per user.
#              Used with cron: 0 * * * * /usr/local/admin/scripts/docker/collect_stats.sh
# Author: Petar Curic
# Created: 07.10.2023
# Last Modified: 10.10.2023
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

# Define the output directory
output_dir="/usr/local/panel/core/stats"

# Get the current date and time in the desired format
current_datetime=$(date +'%Y-%m-%d-%H-%M-%S')

# Loop through the Docker containers and extract data
docker stats --no-stream --format '{{json .}}' | while read -r container_stats; do
  # Extract relevant data from the JSON
  cpu_percent=$(echo "$container_stats" | jq -r '.CPUPerc' | sed 's/%//')
  mem_percent=$(echo "$container_stats" | jq -r '.MemPerc' | sed 's/%//')
  net_io=$(echo "$container_stats" | jq -r '.NetIO' | awk '{print $1}' | sed 's/B//')
  block_io=$(echo "$container_stats" | jq -r '.BlockIO' | awk '{print $1}' | sed 's/B//')

  # Extract the username (Name field from the Docker stats)
  username=$(echo "$container_stats" | jq -r '.Name')

  # Define the output file path
  output_file="$output_dir/$username/$current_datetime.json"

  # Create the directory if it doesn't exist
  mkdir -p "$(dirname "$output_file")"

  # Create the JSON data and write it to the output file
  json_data="{\"cpu_percent\": $cpu_percent, \"mem_percent\": $mem_percent, \"net_io\": \"$net_io\", \"block_io\": \"$block_io\"}"
  # NOTE: net_io and block_io also contain the unit so should be used as strings.
  echo "$json_data" > "$output_file"

  echo "Data for $username written to $output_file"

  echo "$json_data"
done
