#!/bin/bash
################################################################################
# Script Name: email/add.sh
# Description: Create an email address.
# Usage: opencli email-add <EMAIL> <PASSWORD> [--debug]
# Docs: https://docs.openpanel.co/docs/admin/scripts/emails#add
# Author: Stefan Pejcic
# Created: 18.08.2024
# Last Modified: 18.08.2024
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

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
    echo "Usage: opencli email-add <EMAIL> <PASSWORD> [--debug]"
    exit 1
fi


email="${1}"
password="$2"
DEBUG=false  # Default value for DEBUG

# Parse optional flags to enable debug mode when needed
if [ "$3" = "--debug" ]; then
    DEBUG=true
fi



# added in 0.2.5
key_value=$(grep "^key=" $PANEL_CONFIG_FILE | cut -d'=' -f2-)

# Check if 'enterprise edition'
if [ -n "$key_value" ]; then
    :
else
    echo "Error: OpenPanel Community edition does not support emails. Please consider purchasing the Enterprise version that allows unlimited number of email addresses."
    exit 1
fi



# Check if DEBUG is true before printing debug messages
if [ "$DEBUG" = true ]; then
    echo ""
    echo "----------------- DEBUG INFORMATION ------------------"
    echo ""
    echo "Creating new email address:"
    echo ""
    echo "- EMAIL ADDRESS: $email" 
    echo "- PASSWORD: $password"
    echo ""
fi


docker exec -it openadmin_mailserver setup email add "$email" "$password"






