#!/bin/bash
################################################################################
# Script Name: update.sh
# Description: Checks if updates are enabled and then if an update is available.
# Usage: opencli update
#        opencli update --force
# Author: Stefan Pejcic
# Created: 10.10.2023
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


ensure_jq_installed() {
    # Check if jq is installed
    if ! command -v jq &> /dev/null; then
        # Install jq using apt
        sudo apt-get update > /dev/null 2>&1
        sudo apt-get install -y -qq jq > /dev/null 2>&1
        # Check if installation was successful
        if ! command -v jq &> /dev/null; then
            echo "Error: jq installation failed. Please install jq manually and try again."
            exit 1
        fi
    fi
}

ensure_jq_installed


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
                        echo "Updating to version $local_version"
                        wget -q -O - "https://update.openpanel.co/versions/$local_version" | bash
                    fi
                else
                    echo "Updating to version $local_version"
                    wget -q -O - "https://update.openpanel.co/versions/$local_version" | bash
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
                            echo "Updating to version $local_version"
                            wget -q -O - "https://update.openpanel.co/versions/$local_version" | bash
                        fi
                    else
                        echo "Updating to version $local_version"
                        wget -q -O - "https://update.openpanel.co/versions/$local_version" | bash
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

    local array=($version)

    # Increment the last segment
    array[${#array[@]}-1]=$((array[${#array[@]}-1] + 1))

    echo "${array[*]}"
}






# Call the function to check for updates, pass any additional arguments to it
check_update "$@"
