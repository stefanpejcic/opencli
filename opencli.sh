#!/bin/bash
################################################################################
# Script Name: /usr/local/bin/opencli
# Description: Makes all OpenCLI commands available on the terminal.
# Usage: opencli <COMMAND-NAME>
# Author: Stefan Pejcic
# Created: 15.11.2023
# Last Modified: 15.11.2023
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

# Check if a command is provided
if [ -z "$1" ]; then
    echo "Usage: opencli <command>"
    exit 1
fi

# Replace dashes with slashes
COMMAND_WITH_SLASHES=$(echo "$1" | tr '-' '/')

# Define the scripts directory
SCRIPTS_DIR="/usr/local/admin/scripts"

# Build the full path to the script
# ovo za ne enkodirano samo SCRIPT_PATH="$SCRIPTS_DIR/$COMMAND_WITH_SLASHES.sh"
SCRIPT_PATH="$SCRIPTS_DIR/$COMMAND_WITH_SLASHES"

# Check if the script exists
if [ -e "$SCRIPT_PATH" ]; then
    # Execute the script with the provided arguments
    shift # remove the first argument (the script name)
    bash "$SCRIPT_PATH" "$@"
else
    echo "Error: Command not found"
    exit 1
fi
