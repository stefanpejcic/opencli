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

for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
    esac
done


if [ "$DEBUG" = true ]; then
 docker pull openpanel/nginx
 docker pull openpanel/apache
else
 docker pull openpanel/nginx > /dev/null 2>&1
 docker pull openpanel/apache > /dev/null 2>&1
fi

