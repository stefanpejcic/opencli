#!/bin/bash
################################################################################
# Script Name: backup/check.sh
# Description: Check if process id is running for a backup job.
# Usage: opencli backup-check
# Author: Stefan Pejcic
# Created: 31.01.2024
# Last Modified: 14.03.2025
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

logs_dir="/var/log/openpanel/admin/backups"

# Check if the logs directory exists
if [ ! -d "$logs_dir" ]; then
  echo "Logs directory not found: $logs_dir"
  exit 1
fi

# Function to check if a process with a given process_id is running
is_process_running() {
  local process_id="$1"
  ps -p "$process_id" > /dev/null 2>&1
}

# Iterate through all subdirectories in the logs directory
for sub_dir in "$logs_dir"/*; do
  # Check if it's a directory
  if [ -d "$sub_dir" ]; then
    no_jobs_found=true 
    # Iterate through all .log files in the current subdirectory
    for log_file in "$sub_dir"/*.log; do
      # Check if there are any .log files
      if [ -e "$log_file" ]; then
        # Extract lines containing process_id, end_time, and status
        process_line=$(grep 'process_id=' "$log_file" | head -n 1)
        end_time_line=$(grep 'end_time=' "$log_file" | head -n 1)
        status_line=$(grep 'status=' "$log_file" | head -n 1)

        # Extract status value
        status=$(echo "$status_line" | grep -oP 'status=\K[^[:space:]]+')

        # Extract process_id value
        process_id=$(echo "$process_line" | grep -oP 'process_id=\K\d+')

        # Check if the status is not "Completed"
        if [ -n "$status" ] && [ "$status" != "Completed" ] && [ "$status" != "Timeout" ]; then

          echo "Found an incomplete backup job. Log file:. $log_file"
          echo "$end_time_line, $status_line"

          # Check if the process with the given process_id is not running
          if ! is_process_running "$process_id"; then
            echo "Process ID $process_id is not running. Changing backup job status to: Timeout."

            # Replace the line with status=Timeout
            sed -i "s/^status=.*/status=Timeout/" "$log_file"

            # Update end_time with the current time
            current_time=$(date -u +"%a %b %d %T UTC %Y")
            sed -i "s/^end_time=.*/end_time=$current_time/" "$log_file"

          else
            echo "Backup job is still in progress, process ID $process_id is running."
          fi
        no_jobs_found=false
        fi
      fi
    done   
  fi
done


if [ "$no_jobs_found" = true ]; then
  echo "No running backup jobs with broken pid found."
fi
