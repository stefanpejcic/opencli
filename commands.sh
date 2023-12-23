#!/bin/bash
################################################################################
# Script Name: commands.sh
# Description: Lists all available OpenCLI commands.
# Usage: opencli commands
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

# ANSI escape code for green color
GREEN='\033[0;32m'
# ANSI escape code to reset color
RESET='\033[0m'


# Loop through all scripts in the directory and its subdirectories
find "$SCRIPTS_DIR" -type f -executable ! -name "opencli.sh" ! -name "commands_OLD.sh" | while read -r script; do
    # Check if the script is executable
    if [ -x "$script" ]; then
        # Get the script name without the directory and extension
        script_name=$(basename "$script" | sed 's/\.sh$//')

        # Get the directory name without the full path
        dir_name=$(dirname "$script" | sed 's:.*/::')

        if [ "$dir_name" = "scripts" ]; then
            dir_name=""
        else
            dir_name="${dir_name}-"
        fi

        # Combine directory name and script name for the alias
        alias_name="${dir_name}${script_name}"

        # Add the "opencli " prefix to the alias
        full_alias="opencli $alias_name"

	# Extract the description from the script if available
	description=$(grep -E "^# Description:" "$script" | sed 's/^# Description: //')

	# Extract the usage from the script if available
	usage=$(grep -E "^# Usage:" "$script" | sed 's/^# Usage: //')
 
	# Print a message indicating the alias creation
	echo -e "${GREEN}$full_alias${RESET}` #for $script`"

	# Display the description only if it is found
	if [ -n "$description" ]; then
	echo "Description: $description"
	fi
	if [ -n "$usage" ]; then
	echo "Usage: $usage"
	fi
	#echo ""
	echo "------------------------"
	#echo ""

	# Add the alias and description to the alias file
	echo "$full_alias" >> "$ALIAS_FILE"
    fi
done

# Sort the aliases in the file by names
sort -o "$ALIAS_FILE" "$ALIAS_FILE"
