#!/bin/bash
################################################################################
# Script Name: update.sh
# Description: Checks if updates are enabled and then if an update is available.
# Usage: opencli update
#        opencli update --force
# Author: Stefan Pejcic
# Created: 10.10.2023
# Last Modified: 30.08.2024
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


LOG_FILE="/var/log/openpanel/admin/notifications.log"

write_notification() {
  local title="$1"
  local message="$2"
  local current_message="$(date '+%Y-%m-%d %H:%M:%S') UNREAD $title MESSAGE: $message"
  echo "$current_message" >> "$LOG_FILE"
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




# Function to check if an update is needed
check_update() {
    local force_update=false

    # Check if the '--force' flag is provided
    if [[ "$1" == "--force" ]]; then
        force_update=true
        echo "Forcing updates, ignoring autopatch and autoupdate settings."
    fi

    local autopatch
    local autoupdate

    if [ "$force_update" = true ]; then
        # When the '--force' flag is provided, set autopatch and autoupdate to "on"
        autopatch="on"
        autoupdate="on"
    else
        autopatch=$(awk -F= '/^autopatch=/{print $2}' /etc/openpanel/openpanel/conf/openpanel.config)
        autoupdate=$(awk -F= '/^autoupdate=/{print $2}' /etc/openpanel/openpanel/conf/openpanel.config)
    fi

    # Only proceed if autopatch or autoupdate is set to "on"
    if [ "$autopatch" = "on" ] || [ "$autoupdate" = "on" ] || [ "$force_update" = true ]; then
        # Run the update_check.sh script to get the update status
        local update_status=$(opencli update_check)

        # Extract the local and remote version from the update status
        local local_version=$(echo "$update_status" | jq -r '.installed_version')
        local remote_version=$(echo "$update_status" | jq -r '.latest_version')

        # Check if autoupdate is "no" and not forcing the update
        if [ "$autoupdate" = "off" ] && [ "$local_version" \< "$remote_version" ] && [ "$force_update" = false ]; then
            echo "Update is available, autopatch will be installed."

            # Incrementally update from local_version to remote_version
            while [ "$(compare_versions "$local_version" "$remote_version")" = -1 ]; do
                local_version=$(get_next_version "$local_version")

                # Check if skip_versions file exists and if remote version matches
                if [ -f "/etc/openpanel/upgrade/skip_versions" ]; then
                    if grep -q "$local_version" "/etc/openpanel/upgrade/skip_versions"; then
                        echo "Version $local_version is skipped due to /etc/openpanel/upgrade/skip_versions file."
                    else
                        run_update_immediately "$local_version"
                    fi
                else
                    run_update_immediately "$local_version"
                fi
            done
            
        else
            # If autoupdate is "on" or force_update is true, check if local_version is less than remote_version
            if [ "$local_version" \< "$remote_version" ] || [ "$force_update" = true ]; then
                echo "Update is available and will be automatically installed."

                # Incrementally update from local_version to remote_version
                while [ "$(compare_versions "$local_version" "$remote_version")" = -1 ]; do
                    local_version=$(get_next_version "$local_version")

                    # Check if skip_versions file exists and if remote version matches
                    if [ -f "/etc/openpanel/upgrade/skip_versions" ]; then
                        if grep -q "$local_version" "/etc/openpanel/upgrade/skip_versions"; then
                            echo "Version $local_version is skipped due to /etc/openpanel/upgrade/skip_versions file."
                        else
                            run_update_immediately "$local_version"
                        fi
                    else
                        run_update_immediately "$local_version"
                    fi
                done
                
            else
                echo "No update available."
            fi
        fi
    else
        echo "Autopatch and Autoupdate are both set to 'off'. No updates will be installed automatically."
    fi
}

# Function to compare two semantic versions
compare_versions() {
    local version1=$1
    local version2=$2
    local IFS='.'

    local array1=($version1)
    local array2=($version2)

    for ((i = 0; i < ${#array1[@]}; i++)); do
        if ((array1[i] > array2[i])); then
            echo 1  # version1 > version2
            return
        elif ((array1[i] < array2[i])); then
            echo -1  # version1 < version2
            return
        fi
    done

    echo 0  # version1 == version2
}

# Function to get the next semantic version
get_next_version() {
    local version=$1
    local IFS='.'

    # Split the version into an array
    read -r -a array <<< "$version"

    # Loop over the array from the last element backward
    for ((i=${#array[@]}-1; i>=0; i--)); do
        array[$i]=$((array[$i] + 1)) # Increment the current segment
        if [ ${array[$i]} -lt 10 ]; then
            break # No carry needed, exit loop
        else
            array[$i]=0 # Set current segment to 0 and continue to the previous segment
        fi
    done

    # Join the array back into a version string
    local next_version="${array[*]}"
    next_version=${next_version// /.}

    echo "$next_version"
}




run_update_immediately(){
    version="$1"
    log_dir="/usr/local/admin/updates"
    mkdir -p $log_dir
    log_file="$log_dir/$version.log"
    # if not first try then set timestamp in filename
    if [ -f "$log_file" ]; then
        timestamp=$(date +"%Y%m%d_%H%M%S")
        log_file="${log_dir}/${version}_${timestamp}.log"
    fi
    
    write_notification "OpenPanel update started" "Started update to version $version - Log file: $log_file"
    
    echo "Updating to version $version"
    #timeout 300 bash -c "wget -q -O - 'https://update.openpanel.com/versions/$version/UPDATE.sh' | bash" &>> "$log_file"
    # from 0.3.7
    timeout 300 bash -c "wget -q -O - 'https://raw.githubusercontent.com/stefanpejcic/OpenPanel/refs/heads/main/version/$version/UPDATE.sh' | bash" &>> "$log_file"
    if [ $? -eq 124 ]; then
        echo "Error: Update to version $version timed out after 5 minutes."
        write_notification "Update Timed Out" "The update to version $version timed out after 5 minutes."
    fi

}


ensure_jq_installed

check_update "$@"
