#!/bin/bash
################################################################################
# Script Name: version.sh
# Description: Displays the current (installed) version of OpenPanel docker image.
# Usage: opencli version 
#        opencli v
# Author: Stefan Pejcic
# Created: 15.11.2023
# Last Modified: 21.01.2025
# Company: openpanel.com
# Copyright (c) openpanel.com
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

version_check() {
    if [ -f "/root/docker-compose.yml" ]; then
        image_version=$(grep -A 1 "openpanel:" /root/docker-compose.yml | grep "image:" | awk -F':' '{print $3}' | xargs)
        
        if [ -n "$image_version" ]; then
            echo $image_version
        else
            echo '{"error": "OpenPanel service or image version not found"}' >&2
            exit 1
        fi
    else
        echo '{"error": "Docker Compose file not found"}' >&2
        exit 1
    fi
}

version_check
