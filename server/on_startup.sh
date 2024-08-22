#!/bin/bash
################################################################################
# Script Name: server/on_startup.sh
# Description: Runs on system startup and configures files, docker and firewall.
# Usage: opencli server-on_startup
# Author: Stefan Pejcic
# Created: 15.11.2023
# Last Modified: 22.08.2024
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

# todo: should floatingip service instead
opencli server-recreate_hosts --after-reboot

# deprecated from 0.2.6, uses fstab instead
##########opencli files-remount


# deprecated from 0.2.6, nginx is containerized
##########docker exec nginx nginx -s reload

# deprecated from 0.2.6, to prevent conflicts with ufw and cloudflare only mode
##########opencli firewall-reset

# Get the op version 
timeout 5 docker cp openpanel:/usr/local/panel/version /usr/local/panel/version > /dev/null 2>&1 #5 sec max
