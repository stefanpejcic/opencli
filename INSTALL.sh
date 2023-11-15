#!/bin/bash
################################################################################
# Script Name: INSTALL.sh
# Description: Create crons and folders needed for various openpanel cli scripts.
# Usage: bash /usr/local/admin/scripts/INSTALL.sh
# Author: Stefan Pejcic
# Created: 08.10.2023
# Last Modified: 01.11.2023
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

# Define your cron job entries
cron_jobs=(
  "0 * * * * opencli docker-collect_stats.sh"
  "* 2 * * * opencli docker-usage_stats_cleanup"
  "0 */3 * * * certbot renew --post-hook 'systemctl reload nginx'"
  "0 1 * * * opencli backup-create"
  "15 0 * * * opencli update"
  "@reboot opencli server-on_startup"
)

# Create a temporary file to store the cron job entries
cron_temp_file=$(mktemp)

# Add the cron jobs to the temporary file
for job in "${cron_jobs[@]}"; do
    echo "$job" >> "$cron_temp_file"
done

# Install the crontab for the root user from the temporary file
crontab "$cron_temp_file"

# Remove the temporary file
rm "$cron_temp_file"

# aliases
ln -s /usr/local/admin/scripts/version.sh /usr/local/admin/scripts/v.sh

# Make all bash scripts in this directory executable for our user and root only
find /usr/local/admin/scripts -type f -name "*.sh" -exec chmod 700 {} \;
chown root:root /usr/local/admin/scripts/*.sh

cp /usr/local/admin/scripts/opencli.sh /usr/local/bin/opencli
chmod +x /usr/local/bin/opencli

# Generate a list of commands for the admin panel
bash /usr/local/admin/scripts/commands.sh


