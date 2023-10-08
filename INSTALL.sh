#!/bin/bash
################################################################################
# Script Name: INSTALL.sh
# Description: Create crons and folders needed for various openpanel cli scripts
#              Use: bash /usr/local/admin/scripts/INSTALL.sh
# Author: Stefan Pejcic
# Created: 08.10.2023
# Last Modified: 08.10.2023
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

# Collect docker stats for all users every 60 minutes
echo "0 * * * * bash /usr/local/admin/scripts/docker/collect_stats.sh" | sudo tee -a /var/spool/cron/crontabs/root

# Make all bash scripts in this directory executable for root only
find /usr/local/admin/scripts -type f -name "*.sh" -exec chmod 700 {} \;
