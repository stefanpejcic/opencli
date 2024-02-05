#!/bin/bash
################################################################################
# Script Name: install.sh
# Description: Create cronjobs and configuration files needed for openpanel.
# Usage: opencli install
# Author: Stefan Pejcic
# Created: 08.10.2023
# Last Modified: 16.01.2024
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

# Cron job entries
cron_jobs=(
  "0 * * * * opencli docker-collect_stats"
  "* 2 * * * opencli docker-usage_stats_cleanup"
  "0 */3 * * * certbot renew --post-hook 'systemctl reload nginx'"
  "15 0 * * * opencli update"
  "30 2 * * * opencli domains-stats"
  "0 0 12 * * opencli server-ips"
  "0 7 * * * opencli backup-check"
  "0 8 * * * opencli backup-scheduler"
  "* * * * * bash /usr/local/admin/service/notifications.sh"
  "@reboot bash /usr/local/admin/service/notifications.sh --startup"
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

# set aliases
ln -s /usr/local/admin/scripts/version /usr/local/admin/scripts/v

# Make all bash scripts in this directory executable for root only
chown root:root /usr/local/admin/scripts/*

# Only opencli binary is added to path and is used to call all other scripts
cp /usr/local/admin/scripts/opencli /usr/local/bin/opencli
chmod +x /usr/local/bin/opencli

# Generate a list of commands for the opencli
opencli commands

# Set autocomplete for all available opencli commands
echo "# opencli aliases
ALIASES_FILE=\"/usr/local/admin/scripts/aliases.txt\"
generate_autocomplete() {
    awk '{print \$NF}' \"\$ALIASES_FILE\"
}
complete -W \"\$(generate_autocomplete)\" opencli" >> ~/.bashrc

source ~/.bashrc
