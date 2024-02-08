#!/bin/bash
################################################################################
# Script Name: docker/update_images.sh
# Description: Downloads docker images from hub.openpanel.co
# Usage: opencli docker-update_images
# Docs: https://docs.openpanel.co/docs/admin/scripts/docker#update-images
# Author: Radovan Jecmenica, Stefan Pejcic
# Created: 30.11.2023
# Last Modified: 08.12.2024
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

DEBUG=false
REMOTE_BASE_URL="https://hub.openpanel.co/_/ubuntu_22.04"
LOCAL_BASE_DIR="/usr/local/panel/DOCKER/images"

mkdir -p $LOCAL_BASE_DIR


for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
    esac
done


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
    if [ "$DEBUG" = true ]; then
        echo "Running command: curl -o $local_dir/tmp_$file_prefix $REMOTE_BASE_URL/${file_prefix}"
        curl -o "$local_dir/tmp_$file_prefix" "$REMOTE_BASE_URL/${file_prefix}"
    else
        curl -o "$local_dir/tmp_$file_prefix" "$REMOTE_BASE_URL/${file_prefix}"  > /dev/null 2>&1
    fi

    
    # Check the exit status of curl
    if [ $? -eq 0 ]; then
        if [ "$DEBUG" = true ]; then
        echo "Curl command was successful." #this on debug only!
        fi
        
        # Open the file and check its content
        file_content=$(cat "$local_dir/tmp_$file_prefix")
    
        # Check if content matches the expected format (32 hex characters followed by space and hyphen)
        #echo "Original content: $file_content"
        file_content=$(echo "$file_content" | tr -d '[:space:]')
        #echo "Trimmed content: $file_content"
        if [[ "$file_content" =~ ^[0-9a-f]{32}- ]]; then
            #echo "File content is in the expected format."

            # Compare the downloaded file with the local file
            if ! diff -q "$local_dir/tmp_$file_prefix" "$local_dir/$file_prefix" > /dev/null; then
            
                echo "Newer docker image is available, downloading openpanel_$file Docker image."
                mv "$local_dir/tmp_$file_prefix" "$local_dir/$file_prefix"
        
                # If not, download the Docker image
                if curl -o "$local_dir/$file.tar.gz" "$REMOTE_BASE_URL/$file.tar.gz"; then
                    if [ "$DEBUG" = true ]; then
                        echo "Download successful, importing Docker image."
                    else
                        true
                    fi
                else
                    echo "Error: Downloading newer docker image $file failed."
                    exit 1
                fi
        
                # Check if the Docker image was built successfully
                if docker load < "$local_dir/${file}.tar.gz"; then
                    echo "Docker image openpanel_$file was updated successfully."
                else
                    echo "Error: Docker image openpanel_$file failed to load."
                    exit 1
                fi
                rm $local_dir/${file}.tar.gz # delete downlaoded .tar.gz file
            else
                if [ "$DEBUG" = true ]; then
                echo "Local openpanel_$file image checksum:"
                echo " "
                cat $local_dir/$file_prefix
                echo " "
                echo "Remote openpanel_$file image checksum:"
                echo " "
                cat $local_dir/tmp_$file_prefix
                echo " "
                fi
                echo "Docker image 'openpanel_$file' is latest. No update needed."
            fi
        else
            if [ "$DEBUG" = true ]; then
                echo "Checksum failed: File content is not a valid MD5 checksum, received content:"
                echo " "
                echo $file_content
                echo " "
                echo "Please contact support at: https://community.openpanel.co/t/openadmin"
            else
                echo "Checksum failed: File content is not a valid MD5 checksum."
            fi
              rm "$local_dir/tmp_$file_prefix"
              exit 1
        fi
    else
        rm "$local_dir/tmp_$file_prefix"
        if [ "$DEBUG" = true ]; then
            echo "Curl command failed. Make sure that your server can connect to https://hub.openpanel.co/ in order to download new docker images."
            echo "Please contact support at: https://community.openpanel.co/t/openadmin"
        else
            echo "Failed to download newer docker image from hub.openpanel.co"
        fi

    fi
}




# Function to download and update if newer
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
        if [ "$DEBUG" = true ]; then
        echo "Docker image openpanel_$file does not exist locally, downloading from hub.openpanel.co"
        echo "Running command: curl -o "$local_dir/${file}.tar.gz" "$REMOTE_BASE_URL/${file}.tar.gz""
        curl -o "$local_dir/${file}.tar.gz" "$REMOTE_BASE_URL/${file}.tar.gz"
        echo "Importing the openpanel_$file docker image from file"
        docker load < "$local_dir/${file}.tar.gz"
        echo "Saving checksum to $REMOTE_BASE_URL/${file_prefix}"
        curl -o "$local_dir/tmp_$file_prefix" "$REMOTE_BASE_URL/${file_prefix}"
        else
        curl -o "$local_dir/${file}.tar.gz" "$REMOTE_BASE_URL/${file}.tar.gz" > /dev/null 2>&1
        docker load < "$local_dir/${file}.tar.gz" > /dev/null 2>&1
        curl -o "$local_dir/tmp_$file_prefix" "$REMOTE_BASE_URL/${file_prefix}" > /dev/null 2>&1
        fi
    else
        if [ "$DEBUG" = true ]; then
        echo "Docker image openpanel_$file_prefix already exists. Checking if newer image is available on hub.openpanel.co"
        fi
        download_then_check_and_update "$file_prefix"
    fi
}




# Compare and update apache_info and associated tar.gz
download_and_install "apache_info"
# Compare and update nginx_info and associated tar.gz
download_and_install "nginx_info"
