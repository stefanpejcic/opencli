#!/bin/bash
################################################################################
# Script Name: nginx/modsec.sh
# Description: List ModSecurity rules, configuration files
# Usage: opencli nginx-modsec
# Author: Stefan Pejcic
# Created: 22.03.2024
# Last Modified: 22.03.2024
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


# https://nature.berkeley.edu/~casterln/modsecurity/modsecurity2-apache-reference.html

# specific for OpenPanel
SEARCH_DIR="/usr/local/coreruleset-*/rules/"
OUTPUT_JSON=0
SEARCH_RULES=0
UPDATE_RULES=0

# Process flags
while [[ "$#" -gt 0 ]]; do
    case $1 in
        --json) OUTPUT_JSON=1 ;;
        --rules) SEARCH_RULES=1 ;;
        --update) UPDATE_RULES=1 ;;
        *) echo "Unknown parameter passed: $1"; exit 1 ;;
    esac
    shift
done


if [ "$UPDATE_RULES" -eq 1 ]; then
    CRS_REPO="https://github.com/coreruleset/coreruleset.git"
    CRS_DIR=$(mktemp -d)
    UPDATE_DIR="/usr/local/coreruleset-3.3.5/rules"

    git clone "$CRS_REPO" "$CRS_DIR"

    while IFS= read -r -d '' conf_file; do
        filename=$(basename -- "$conf_file")

        # Check if a corresponding .conf.disabled exists
        if [ -f "${UPDATE_DIR}/${filename}.disabled" ]; then
            # Rename the new .conf file to .conf.disabled before copying
            mv "$conf_file" "${conf_file}.disabled"
            # Copy the now renamed .conf.disabled file to UPDATE_DIR, overwriting the existing one
            cp "${conf_file}.disabled" "${UPDATE_DIR}/${filename}.disabled"
            echo "Updating disabled rules file ${UPDATE_DIR}/${filename}.disabled"
        elif [ -f "${UPDATE_DIR}/${filename}" ]; then
            # Directly overwrite the .conf file in the UPDATE_DIR
            echo "Updating existing active rules file ${UPDATE_DIR}/${filename}"
            cp "$conf_file" "${UPDATE_DIR}/${filename}"
        else
            # Directly overwrite the .conf file in the UPDATE_DIR
            echo "Downloading new active rules file ${UPDATE_DIR}/${filename}"
            cp "$conf_file" "${UPDATE_DIR}/${filename}"
        fi

    done < <(find "$CRS_DIR/rules" -type f -name "*.conf" -print0)
    
    echo "OWASP ModSecurity Core Rule Set updated successfully."
    exit 0
fi







# Initialize an array to hold the output data
OUTPUT_DATA=()


if [ "$SEARCH_RULES" -eq 1 ]; then
    # Special handling for --rules flag
    if [ "$OUTPUT_JSON" -eq 1 ]; then
        # If outputting JSON, remove the 'id:' prefix
        while IFS= read -r line; do
            OUTPUT_DATA+=("${line//id:/}") # Use parameter expansion to strip 'id:' prefix
        done < <(grep -Rohs 'id:[0-9]*' $SEARCH_DIR | sed 's/id://g' | sort | uniq)
    else
        while IFS= read -r line; do
            OUTPUT_DATA+=("$line")
        done < <(grep -Rohs 'id:[0-9]*' $SEARCH_DIR | sort | uniq)
    fi
else
    # Default behavior: find .conf files
    for dir in $SEARCH_DIR; do
        if [ -d "$dir" ]; then
            while IFS= read -r file; do
                OUTPUT_DATA+=("$file")
            done < <(find "$dir" -type f \( -name "*.conf" -o -name "*.conf.disabled" \)) # .disbaled
        fi
    done
fi


# Output processing
if [ "$OUTPUT_JSON" -eq 1 ]; then
    printf '%s\n' "${OUTPUT_DATA[@]}" | jq -R . | jq -s .
else
    for item in "${OUTPUT_DATA[@]}"; do
        echo "$item"
    done
fi
