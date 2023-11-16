#!/bin/bash
################################################################################
# Script Name: update/images.sh
# Description: Updates the local Apache and Nginx docker images used for new users.
# Usage: opencli update-images
# Author: Stefan Pejcic
# Created: 16.10.2023
# Last Modified: 16.11.2023
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

download_image() {
    local image_url=$1
    local local_path=$2
    
    # Clean up newly downloaded files
    rm -f "$local_path/new_image.tar.gz"

    # Download image
    wget -q "$image_url" -O "$local_path/new_image.tar.gz"
}

generate_checksum() {
    local image_url=$1

    # Download image and generate checksum
    wget -q "$image_url" -O - | sha256sum | awk '{print $1}'
}

compare_checksum() {
    local local_checksum=$1
    local downloaded_checksum=$2

    # Compare checksums
    if [ "$local_checksum" != "$downloaded_checksum" ]; then
        return 1  # Checksums are different
    else
        return 0  # Checksums are the same
    fi
}

echo "Checking if newer docker images are available.."

# Generate checksums for the images
local_apache_checksum=$(generate_checksum "https://hub.openpanel.co/_/ubuntu_22.04/apache.tar.gz")
local_nginx_checksum=$(generate_checksum "https://hub.openpanel.co/_/ubuntu_22.04/nginx.tar.gz")

# Download images
download_image "https://hub.openpanel.co/_/ubuntu_22.04/apache.tar.gz" "/usr/local/panel/DOCKER/images/"
download_image "https://hub.openpanel.co/_/ubuntu_22.04/nginx.tar.gz" "/usr/local/panel/DOCKER/images/"

echo "Comparing checksums of local and downloaded images.."

# Compare and update images
if compare_checksum "$local_apache_checksum" "$(generate_checksum "/usr/local/panel/DOCKER/images/apache.tar.gz")"; then
    echo "Apache image is up to date, no need to update."
else
    echo "Newer Apache image is available, updating.."
    docker load < "/usr/local/panel/DOCKER/images/apache.tar.gz"
    echo "Apache Docker image is updated"
fi

if compare_checksum "$local_nginx_checksum" "$(generate_checksum "/usr/local/panel/DOCKER/images/nginx.tar.gz")"; then
    echo "Nginx image is up to date, no need to update."
else
    echo "Newer Nginx image is available, updating.."
    docker load < "/usr/local/panel/DOCKER/images/nginx.tar.gz"
    echo "Nginx Docker image is updated"
fi
