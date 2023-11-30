#!/bin/bash
################################################################################
# Script Name: docker/check_image_files
# Description: Create a new user with the provided plan_id.
# Usage: opencli check_image_files
# Docs: https://docs.openpanel.co/docs/admin/scripts/users#add-user
# Author: Radovan Jecmenica
# Created: 30.11.2023
# Last Modified: 30.11.2023
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

REMOTE_BASE_URL="https//hub.openpanel.co/_/ubuntu_22.04"
LOCAL_BASE_DIR="/usr/local/admin/DOCKER/images"

# Function to download and update files if they are different
download_and_update() {
    local file_prefix="$1"
    local local_dir="$LOCAL_BASE_DIR"

    # Download the remote file
    curl -o "$local_dir/tmp_$file_prefix" "$REMOTE_BASE_URL/${file_prefix}"

    # Compare the downloaded file with the local file
    if ! diff -q "$local_dir/tmp_$file_prefix" "$local_dir/$file_prefix" > /dev/null; then
        echo "Updating $local_dir/$file_prefix"
        mv "$local_dir/tmp_$file_prefix" "$local_dir/$file_prefix"

        # Check if it's a tar.gz file, and if yes, download and overwrite it
        if [[ "$file_prefix" == "apache_info" ]]; then
            curl -o "$local_dir/apache.tar.gz" "$REMOTE_BASE_URL/apache.tar.gz"
        elif [[ "$file_prefix" == "nginx_info" ]]; then
            curl -o "$local_dir/nginx.tar.gz" "$REMOTE_BASE_URL/nginx.tar.gz"
        fi
    else
        echo "Files are the same. No update needed."
        rm "$local_dir/tmp_$file_prefix" # Remove temporary file if no update
    fi
}

# Compare and update apache_info and associated tar.gz
download_and_update "apache_info"
# Compare and update nginx_info and associated tar.gz
download_and_update "nginx_info"
