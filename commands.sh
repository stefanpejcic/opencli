#!/bin/bash
################################################################################
# Script Name: commands.sh
# Description: Lists all available OpenCLI commands.
# Usage: opencli commands
# Author: Stefan Pejcic
# Created: 15.11.2023
# Last Modified: 19.07.2024
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

SCRIPTS_DIR="/usr/local/admin/scripts"
ALIAS_FILE="$SCRIPTS_DIR/aliases.txt"

# delete .git files
rm -rf $SCRIPTS_DIR/.git $SCRIPTS_DIR/watcher/.git 

# delete exisitng aliases first
> "$ALIAS_FILE"

GREEN='\033[0;32m'
RESET='\033[0m'

# Loop through all scripts from https://github.com/stefanpejcic/openpanel-docker-cli/
find "$SCRIPTS_DIR" -type f -executable \
  ! -name "opencli.sh" \
  ! -name "install" \
  ! -name "install.sh" \
  ! -name "watcher" \
  ! -name "watcher.sh" \
  ! -name "opencli" \
  ! -name "mysql" \
  ! -name "mysql.sh" \
  ! -name "db" \
  ! -name "db.sh" \
  ! -name "README.md" \
  ! -name "aliases.txt" \
  ! -name "*motd*" \
  ! -name "*NEW*" \
  ! -name "*TODO*" | while read -r script; do
    if [ -x "$script" ]; then
        script_name=$(basename "$script" | sed 's/\.sh$//') # strip extension
        dir_name=$(dirname "$script" | sed 's:.*/::') # folder name without the full path

        if [ "$dir_name" = "scripts" ]; then
            dir_name=""
        else
            dir_name="${dir_name}-"
        fi

        alias_name="${dir_name}${script_name}"
        full_alias="opencli $alias_name"
	description=$(grep -E "^# Description:" "$script" | sed 's/^# Description: //') # extract description if available
	usage=$(grep -E "^# Usage:" "$script" | sed 's/^# Usage: //') # extract usage if available
 
	echo -e "${GREEN}$full_alias${RESET}` #for $script`"

	if [ -n "$description" ]; then
		echo "Description: $description"
	fi
	if [ -n "$usage" ]; then
		echo "Usage: $usage"
	fi
 
	echo "------------------------"
 
	echo "$full_alias" >> "$ALIAS_FILE" # add to file
    fi
done

# Sort the aliases in the file by names
sort -o "$ALIAS_FILE" "$ALIAS_FILE"
