#!/bin/bash
################################################################################
# Script Name: aliases.sh
# Description: Creates a list of all available cli commands
#              Use: bash /usr/local/admin/scripts/aliases.sh
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

# Define the scripts directory
SCRIPTS_DIR="/usr/local/admin/scripts"

# Define the alias file
ALIAS_FILE="$SCRIPTS_DIR/aliases.txt"

# Ensure the alias file is empty before appending
> "$ALIAS_FILE"

# Loop through all scripts in the directory and its subdirectories
find "$SCRIPTS_DIR" -type f -name "*.sh" ! -name "INSTALL.sh" ! -name "opencli.sh" ! -name "aliases.sh" | while read -r script; do
    # Check if the script is executable
    if [ -x "$script" ]; then
        # Get the script name without the directory and extension
        script_name=$(basename "$script" | sed 's/\.sh$//')

        # Get the directory name without the full path
        dir_name=$(dirname "$script" | sed 's:.*/::')

        # Combine directory name and script name for the alias
        alias_name="${dir_name}-${script_name}"

        # Add the "opencli " prefix to the alias
        full_alias="opencli $alias_name"

        # Append the alias to the file
        echo "$full_alias" >> "$ALIAS_FILE"

        # Print a message indicating the alias creation
        echo "Alias created: $full_alias for $script"
    fi
done
