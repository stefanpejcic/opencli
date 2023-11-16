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

echo "Downloading Nginx and Apache docker images.."

mkdir -p /usr/local/panel/DOCKER/images/

# download
wget https://hub.openpanel.co/_/ubuntu_22.04/apache.tar.gz -P /usr/local/panel/DOCKER/images/
wget https://hub.openpanel.co/_/ubuntu_22.04/nginx.tar.gz -P /usr/local/panel/DOCKER/images/

# import
docker load < /usr/local/panel/DOCKER/images/apache.tar.gz
docker load < /usr/local/panel/DOCKER/images/nginx.tar.gz
fi
