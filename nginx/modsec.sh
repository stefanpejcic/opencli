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
VIEW_LOGS=0
DOMAIN_NAME=""
FILE_NAME=""
DOMAIN_OPTION=0
ENABLE=0
DISABLE=0

# Process flags
while [[ "$#" -gt 0 ]]; do
    case $1 in
        --json) OUTPUT_JSON=1 ;;
        --rules) SEARCH_RULES=1 ;;
        --update) UPDATE_RULES=1 ;;
        --logs) VIEW_LOGS=1; LOG_FILTER="${2:-}"; if [[ "$LOG_FILTER" != "--"* ]]; then shift; fi ;;
        --domain) DOMAIN_OPTION=1; DOMAIN_NAME="$2"; shift ;;
        --enable) ENABLE=1; FILE_NAME="$2"; shift ;;
        --disable) DISABLE=1; FILE_NAME="$2"; shift ;;
        *) echo "Unknown parameter passed: $1"; exit 1 ;;
    esac
    shift
done


# enable conf file
if [ "$ENABLE" -eq 1 ]; then
    if [ -n "$FILE_NAME" ]; then
        # Check if FILE_NAME ends with .conf.disabled
        if [[ "$FILE_NAME" == *.conf.disabled ]]; then
            # Check if FILE_NAME exists
            if [ ! -e "$FILE_NAME" ]; then
                
                if [ ! -e "${FILE_NAME%.disabled}" ]; then
    
                    if [ "$OUTPUT_JSON" -eq 1 ]; then
                        JSON_MESSAGE="{ \"message\": \"SUCCESS: File is already enabled.\" }"
                        echo "$JSON_MESSAGE"
                    else
                         echo "SUCCESS: File is already enabled."
                        exit 0
                    fi
                

                else
                    echo "ERROR: File '$FILE_NAME' does not exist."

                    if [ "$OUTPUT_JSON" -eq 1 ]; then
                        JSON_MESSAGE="{ \"message\": \"ERROR: File does not exist.\" }"
                        echo "$JSON_MESSAGE"
                    else
                         echo "ERROR: File '$FILE_NAME' does not exist."
                        exit 1
                    fi

                    
                fi
            else
                # Remove the ".disabled" suffix from FILE_NAME
                NEW_FILE_NAME="${FILE_NAME%.disabled}"
                # Rename the file
                mv "$FILE_NAME" "$NEW_FILE_NAME"
                
                if [ "$OUTPUT_JSON" -eq 1 ]; then
                    JSON_MESSAGE="{ \"message\": \"SUCCESS: Enabled conf file: $NEW_FILE_NAME\" }"
                    echo "$JSON_MESSAGE"
                else
                    echo "SUCCESS: Enabled conf file: $NEW_FILE_NAME"
                fi
            fi           
        else
                    if [ "$OUTPUT_JSON" -eq 1 ]; then
                        JSON_MESSAGE="{ \"message\": \"ERROR: File name is not valid.\" }"
                        echo "$JSON_MESSAGE"
                    else
                         echo "ERROR: File name is not valid!"
                        exit 1
                    fi
        fi
  exit 0  
  else
                    if [ "$OUTPUT_JSON" -eq 1 ]; then
                        JSON_MESSAGE="{ \"message\": \"ERROR: Please provide configuration file path.\" }"
                        echo "$JSON_MESSAGE"
                    else
                         echo "ERROR: Please provide configuration file path."
                        exit 1
                    fi
    
   fi
fi


# disable conf file
if [ "$DISABLE" -eq 1 ]; then
    if [ -n "$FILE_NAME" ]; then
        # Check if FILE_NAME ends with .conf
        if [[ "$FILE_NAME" == *.conf ]]; then
            # Check if FILE_NAME exists
            if [ ! -e "$FILE_NAME" ]; then
                
                if [ ! -e "${FILE_NAME%.disabled}" ]; then
                    echo "SUCCESS: File is already disabled."
                    exit 0
                else
                    echo "ERROR: File '$FILE_NAME' does not exist."
                fi
            else
                # Remove the ".disabled" suffix from FILE_NAME
                DISABLED_FILE_NAME="$FILE_NAME.disabled"
                # Rename the file
                mv "$FILE_NAME" "$DISABLED_FILE_NAME"
                echo "SUCCESS: Disabled conf file: $DISABLED_FILE_NAME"
            fi

        elif [[ "$FILE_NAME" == *.conf.disabled ]]; then
                    if [ "$OUTPUT_JSON" -eq 1 ]; then
                        JSON_MESSAGE="{ \"message\": \"SUCCESS: File is already disabled.\" }"
                        echo "$JSON_MESSAGE"
                    else
                         echo "SUCCESS: File is already disabled."
                        exit 1
                    fi
        else
                    if [ "$OUTPUT_JSON" -eq 1 ]; then
                        JSON_MESSAGE="{ \"message\": \"ERROR: File name is not valid.\" }"
                        echo "$JSON_MESSAGE"
                    else
                         echo "ERROR: File name is not valid!"
                        exit 1
                    fi

        fi
  exit 0  
  else
    
                    if [ "$OUTPUT_JSON" -eq 1 ]; then
                        JSON_MESSAGE="{ \"message\": \"ERROR: Please provide configuration file path.\" }"
                        echo "$JSON_MESSAGE"
                    else
                         echo "ERROR: Please provide configuration file path."
                        exit 1
                    fi
    
   fi
fi











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



if [ "$VIEW_LOGS" -eq 1 ]; then
    if [ -n "$LOG_FILTER" ]; then
        # Use the filter if provided
        grep "ModSecurity: Access denied with code 403" /var/log/nginx/error.log | grep "$LOG_FILTER"
        zgrep "ModSecurity: Access denied with code 403" /var/log/nginx/error.log.*.gz | grep "$LOG_FILTER"
    else
        grep "ModSecurity: Access denied with code 403" /var/log/nginx/error.log
        zgrep "ModSecurity: Access denied with code 403" /var/log/nginx/error.log.*.gz
    fi
    exit 0
fi



# Domain-specific functionality
if [ "$DOMAIN_OPTION" -eq 1 ]; then
    if [ -n "$DOMAIN_NAME" ]; then
        # Check ModSecurity status in the domain's Nginx configuration
        if [ -f "/etc/nginx/sites-available/$DOMAIN_NAME.conf" ]; then
            MODSECURITY_STATUS=$(grep "modsecurity on;" "/etc/nginx/sites-available/$DOMAIN_NAME.conf" | wc -l)
            
            if [ "$MODSECURITY_STATUS" -gt 0 ]; then
                echo "ModSecurity status: ✔ Enabled"
            else
                echo "ModSecurity status: ✘ Disabled"
            fi
        else
            echo "ModSecurity status: ? Unknown"
            echo "Nginx configuration file for /etc/nginx/sites-available/$DOMAIN_NAME.conf not found."
        fi


    
        # Grep the domain name and show total count      
        total_count=$(grep "ModSecurity: Access denied with code 403" /var/log/nginx/error.log | grep -c "$DOMAIN_NAME")
        echo "Blocked requests: $total_count"
        # Obtain the username of the user owning the domain, extracting only the last word after ':'
        OWNER_INFO=$(opencli domains-whoowns "$DOMAIN_NAME")
        USERNAME=$(echo "$OWNER_INFO" | awk -F': ' '{print $NF}')

        # Display the domain-specific WAF configuration
        if [ -f "/usr/local/panel/core/users/$USERNAME/domains/$DOMAIN_NAME-waf.conf" ]; then
            disabled_rules_list=$(cat "/usr/local/panel/core/users/$USERNAME/domains/$DOMAIN_NAME-waf.conf")
            echo "Disabled rules: $disabled_rules_list"
        else
            echo "No disabled rules."
        fi
    else
        echo "Domain name not provided."
        exit 1
    fi
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


# TODO: GET RULE INFO FROM https://coreruleset.org/docs/rules/rules/
for dir in $SEARCH_DIR; do
    if [ -d "$dir" ]; then
        while IFS= read -r file; do
            # Extract the file name from the file path
            name=$(basename "$file")

            # Initialize variables to store version
            version=""

            # Read each line of the file
            while IFS= read -r line; do
                # Check for version
                if [[ "$line" == *"ver."* ]]; then
                    version="${line##*ver.}"
                    # Break to prevent unnecessary further checks for version
                    break
                fi
            done < "$file"

            # Output the collected information
            if [ "$OUTPUT_JSON" -eq 1 ]; then
                if [ "$json_started" != "true" ]; then
                    echo -n "["
                    json_started="true"
                else
                    echo -n ","
                fi
                jq -n --arg file "$file" --arg name "$name" --arg version "$version" '{file: $file, name: $name, version: $version}'
            else
                echo "File: $file"
                echo "Name: $name"
                echo "Version: $version"
                echo
            fi
        done < <(find "$dir" -type f \( -name "*.conf" -o -name "*.conf.disabled" \)) # .disabled
    fi
done

# Close the JSON array if JSON output is enabled
if [ "$OUTPUT_JSON" -eq 1 ]; then
    echo "]"
fi
exit 0




    



fi


# Output processing
if [ "$OUTPUT_JSON" -eq 1 ]; then
    printf '%s\n' "${OUTPUT_DATA[@]}" | jq -R . | jq -s .
else
    for item in "${OUTPUT_DATA[@]}"; do
        echo "$item"
    done
fi
