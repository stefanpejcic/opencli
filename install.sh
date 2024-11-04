#!/bin/bash
################################################################################
# Script Name: install.sh
# Description: Create cronjobs and configuration files needed for openpanel.
# Usage: opencli install
# Author: Stefan Pejcic
# Created: 08.10.2023
# Last Modified: 28.04.2024
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

# Make all bash scripts in this directory executable for root only
chown root:root /usr/local/admin/scripts/*

# Only opencli binary is added to path and is used to call all other scripts
cp /usr/local/admin/scripts/opencli /usr/local/bin/opencli
chmod +x /usr/local/bin/opencli

# https://openpanel.co/docs/changelog/0.1.9/#cloudflare-only
wget -O /usr/local/admin/scripts/cloudflare https://raw.githubusercontent.com/stefanpejcic/ipset-cloudflare/main/run.sh

chmod +x -R /usr/local/admin/scripts/










# Generate a list of commands for the opencli
#opencli commands
# changed to generate manually and then put list here!

# Set autocomplete for all available opencli commands
echo "# opencli aliases
ALIASES_FILE=\"/usr/local/admin/scripts/aliases.txt\"
generate_autocomplete() {
    awk '{print \$NF}' \"\$ALIASES_FILE\"
}

# Function to get usernames for opencli user-login
_get_usernames() {
    opencli user-list --json | jq -r '.[].username'
}

# Custom completion for opencli
_opencli_completion() {
    local cmd=\"\${COMP_WORDS[1]}\"
    
    if [[ \"\$cmd\" == \"user-login\" || \"\$cmd\" == \"user-login\" ]]; then
        COMPREPLY=(\$(compgen -W \"\$(_get_usernames)\" -- \"\${COMP_WORDS[2]}\"))
    else
        # For all other commands, use generate_autocomplete
        COMPREPLY=(\$(compgen -W \"\$(generate_autocomplete)\" -- \"\${COMP_WORDS[1]}\"))
    fi
}

complete -F _opencli_completion opencli" >> ~/.bashrc


. ~/.bashrc
