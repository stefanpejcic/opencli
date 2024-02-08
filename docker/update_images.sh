#!/bin/bash
################################################################################
# Script Name: docker/check_image_files
# Description: Create a new user with the provided plan_id.
# Usage: opencli docker-update_images
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

REMOTE_BASE_URL="https://hub.openpanel.co/_/ubuntu_22.04"
LOCAL_BASE_DIR="/usr/local/panel/DOCKER/images"

mkdir -p $LOCAL_BASE_DIR

# Function to download and update files if they are different
download_then_check_and_update() {
    local file_prefix="$1"
    local local_dir="$LOCAL_BASE_DIR"

    if [[ "$file_prefix" == "apache_info" ]]; then
         file="apache"
    elif [[ "$file_prefix" == "nginx_info" ]]; then
         file="nginx"
    fi


    # Download the remote file
    curl -o "$local_dir/tmp_$file_prefix" "$REMOTE_BASE_URL/${file_prefix}"  > /dev/null 2>&1
    
    # Check the exit status of curl
    if [ $? -eq 0 ]; then
        #echo "Curl command was successful." #this on debug only!
    
        # Open the file and check its content
        file_content=$(cat "$local_dir/tmp_$file_prefix")
    
        # Check if content matches the expected format (32 hex characters followed by space and hyphen)
        #if [[ "$file_content" =~ ^[0-9a-f]{32} - ]]; then
        if [[ "$file_content" =~ "^[0-9a-f]{32} -" ]]; then
            #echo "File content is in the expected format."

            # Compare the downloaded file with the local file
            if ! diff -q "$local_dir/tmp_$file_prefix" "$local_dir/$file_prefix" > /dev/null; then
            
                echo "Newer docker image is available, downloading openpanel_$file Docker image."
                mv "$local_dir/tmp_$file_prefix" "$local_dir/$file_prefix"
        
                # If not, download the Docker image
                if curl -o "$local_dir/$file.tar.gz" "$REMOTE_BASE_URL/$file.tar.gz"; then
                    echo "Download successful, importing Docker image."
                else
                    echo "Error: Downloading newer docker image $file failed."
                    exit 1
                fi
        
                # Check if the Docker image was built successfully
                if docker load < "$local_dir/${file}.tar.gz"; then
                    echo "Docker image openpanel_$file was built successfully."
                else
                    echo "Error: Docker image openpanel_$file failed to load."
                    exit 1
                fi
                rm $local_dir/${file}.tar.gz # delete downlaoded .tar.gz file
            else
                echo "No newer docker image available. No update needed."
            fi

        else
            echo "Checksum failed: File content is not a valid MD5 checksum, received content:"
            echo " "
            echo $file_content
            echo " "
            echo "Please contact support at: https://community.openpanel.co/t/openadmin"
            exit 1
        fi
        rm "$local_dir/tmp_$file_prefix" # Remove temporary file
    else
        echo "Curl command failed. Make sure that your server can connect to https://hub.openpanel.co/ in order to download new docker images."
        echo "Please contact support at: https://community.openpanel.co/t/openadmin"
    fi
}




# Function to download and update files if they are different
download_and_install() {
    local file_prefix="$1"
    local local_dir="$LOCAL_BASE_DIR"
    
    if [[ "$file_prefix" == "apache_info" ]]; then
         file="apache"
    elif [[ "$file_prefix" == "nginx_info" ]]; then
         file="nginx"
    fi
    
    # Check if the Docker image exists locally
    if ! docker image inspect "openpanel_$file" > /dev/null 2>&1; then
        # If not, download and import the Docker image
        echo "Downloading and importing openpanel_$file Docker image."
        curl -o "$local_dir/${file}.tar.gz" "$REMOTE_BASE_URL/${file}.tar.gz" > /dev/null 2>&1
        curl -o "$local_dir/${file_prefix}" "$REMOTE_BASE_URL/${file_prefix}" > /dev/null 2>&1
        echo "curl -o "$local_dir/${file}.tar.gz" "$REMOTE_BASE_URL/${file}.tar.gz""
        docker load < "$local_dir/${file}.tar.gz"
    else
        echo "Docker image openpanel_$file_prefix already exists. Checking if newer image is available on hub.openpanel.co"
        curl -o "$local_dir/${file_prefix}" "$REMOTE_BASE_URL/${file_prefix}" > /dev/null 2>&1
        download_then_check_and_update "$file_prefix"
    fi
}




# Compare and update apache_info and associated tar.gz
download_and_install "apache_info"
# Compare and update nginx_info and associated tar.gz
download_and_install "nginx_info"
