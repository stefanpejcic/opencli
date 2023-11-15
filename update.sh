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

# Function to check if an update is needed
check_update() {
    local force_update=false

    # Check if the '--force' flag is provided
    if [[ "$1" == "--force" ]]; then
        force_update=true
        echo "Forcing updates, ignoring autopatch and autoupdate settings."
    fi

    # Read the user settings from /usr/local/panel/conf/panel.config
    local autopatch
    local autoupdate

    if [ "$force_update" = true ]; then
        # When the '--force' flag is provided, set autopatch and autoupdate to "yes"
        autopatch="yes"
        autoupdate="yes"
    else
        autopatch=$(awk -F= '/^autopatch=/{print $2}' /usr/local/panel/conf/panel.config)
        autoupdate=$(awk -F= '/^autoupdate=/{print $2}' /usr/local/panel/conf/panel.config)
    fi

    # Only proceed if autopatch or autoupdate is set to "yes"
    if [ "$autopatch" = "yes" ] || [ "$autoupdate" = "yes" ] || [ "$force_update" = true ]; then
        # Run the update_check.sh script to get the update status
        local update_status=$(./update_check.sh)

        # Extract the local and remote version from the update status
        local local_version=$(echo "$update_status" | jq -r '.installed_version')
        local remote_version=$(echo "$update_status" | jq -r '.latest_version')

        # Check if autoupdate is "no" and not forcing the update
        if [ "$autoupdate" = "no" ] && [ "$local_version" \< "$remote_version" ] && [ "$force_update" = false ]; then
            echo "Update is available, autopatch will be installed."
            # Run the update process
            wget -q -O - https://update.openpanel.co/versions/$remote_version | bash
        else
            # If autoupdate is "yes" or force_update is true, check if local_version is less than remote_version
            if [ "$local_version" \< "$remote_version" ] || [ "$force_update" = true ]; then
                echo "Update is available and will be automatically installed."
                # Run the update process
                wget -q -O - https://update.openpanel.co/versions/$remote_version | bash
            else
                echo "No update available."
            fi
        fi
    else
        echo "Autopatch and Autoupdate are both set to 'no'. No updates will be installed automatically."
    fi
}

# Call the function to check for updates, pass any additional arguments to it
check_update "$@"
