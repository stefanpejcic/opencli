#!/bin/bash
################################################################################
# Script Name: docker/update_images.sh
# Description: Downloads docker images from hub.openpanel.co
# Usage: opencli docker-update_images
# Docs: https://docs.openpanel.co/docs/admin/scripts/docker#update-images
# Author: Radovan Jecmenica
# Created: 30.11.2023
# Last Modified: 09.05.2024
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

for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
    esac
done

# Check if OpenPanel Nginx image has updates
nginx_update=$(docker pull openpanel/nginx 2>&1 | grep -i "Status: Image is up to date" | wc -l)

# Check if OpenPanel Apache image has updates
apache_update=$(docker pull openpanel/apache 2>&1 | grep -i "Status: Image is up to date" | wc -l)

# If both images are already up to date, inform the user
if [ "$nginx_update" -eq 1 ] && [ "$apache_update" -eq 1 ]; then
    echo "OpenPanel Nginx and Apache images are already up to date."
# If updates are available for either image, pull them
else
    if [ "$nginx_update" -ne 1 ]; then
        echo "Newer OpenPanel Nginx image is available, updating.."
        docker pull openpanel/nginx
    fi
    if [ "$apache_update" -ne 1 ]; then
        echo "Newer OpenPanel Apache image is available, updating.."
        docker pull openpanel/apache
    fi
fi
