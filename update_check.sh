#!/bin/bash
################################################################################
# Script Name: update_check.sh
# Description: Checks if an update is available from update.openpanel.co servers.
# Usage: opencli update_check
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

# Define the route to check for updates
update_check() {
    # Read the local version from /usr/local/panel/version
    if [ -f "/usr/local/panel/version" ]; then
        local_version=$(cat "/usr/local/panel/version")
    else
        echo '{"error": "Local version file not found"}' >&2
        exit 1
    fi

    # Fetch the remote version from https://update.openpanel.co/
    remote_version=$(curl -s "https://update.openpanel.co/")

    if [ -z "$remote_version" ]; then
        echo '{"error": "Error fetching remote version"}' >&2
        exit 1
    fi

    # Compare the local and remote versions
    if [ "$local_version" == "$remote_version" ]; then
        echo '{"status": "Up to date", "installed_version": "'"$local_version"'"}'
    elif [ "$local_version" \> "$remote_version" ]; then
        echo '{"status": "Local version is greater", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
    else
        echo '{"status": "Update available", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
    fi
}

# Call the function and print the result
update_check
