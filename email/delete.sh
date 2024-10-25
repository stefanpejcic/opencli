#!/bin/bash
################################################################################
# Script Name: email/delete.sh
# Description: Deletes one or more email accounts and updates related configuration files.
# Usage: opencli email-delete <email_address1> <email_address2> ...
# Docs: https://docs.openpanel.co/docs/admin/scripts/emails#delete
# Author: Radovan Jecmenica
# Created: 25.10.2024
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

# Check if at least one email argument is provided
if [ "$#" -lt 1 ]; then
    echo "Usage: opencli email-delete <email_address1> <email_address2> ..."
    exit 1
fi

# Enterprise check - load config
ENTERPRISE="/usr/local/admin/core/scripts/enterprise.sh"
PANEL_CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
key_value=$(grep "^key=" $PANEL_CONFIG_FILE | cut -d'=' -f2-)

# Check if 'enterprise edition'
if [ -z "$key_value" ]; then
    echo "Error: OpenPanel Community edition does not support email deletion."
    source $ENTERPRISE
    echo "$ENTERPRISE_LINK"
    exit 1
fi

# Validate email
is_valid_email() {
    local email="$1"
    local email_regex='^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$'
    [[ $email =~ $email_regex ]]
}

# Delete emails and refresh users' email lists
delete_email_accounts() {
    local emails=("$@")
    local valid_emails=()
    local domains_to_refresh=()

    # Validate emails and prepare list of valid emails
    for email in "${emails[@]}"; do
        if is_valid_email "$email"; then
            valid_emails+=("$email")
            
            # Determine the domain and owner for each email
            local domain="${email#*@}"
            local whoowns_output=$(opencli domains-whoowns "$domain")
            local owner=$(echo "$whoowns_output" | awk -F "Owner of '$domain': " '{print $2}')
            
            if [ -n "$owner" ]; then
                # Queue domain for refresh if owner found
                domains_to_refresh+=("$owner:$domain")
            else
                echo "Warning: Domain $domain not found or not owned by any user, skipping refresh."
            fi
        else
            echo "Error: Invalid email address format: $email"
        fi
    done

    # Proceed to delete valid emails
    if [ "${#valid_emails[@]}" -gt 0 ]; then
        docker exec openadmin_mailserver setup email del "${valid_emails[@]}"
        echo "Deleted email accounts: ${valid_emails[*]}"
        
        # Refresh email lists for each owner
        for entry in "${domains_to_refresh[@]}"; do
            IFS=":" read -r owner domain <<< "$entry"
            local file_to_refresh="/etc/openpanel/openpanel/core/users/$owner/emails.yml"
            ALL_DOMAINS_OWNED_BY_USER=$(opencli domains-user "$owner")
            ALL_EMAILS_ON_SERVER=$(opencli email-setup email list)
            
            > "$file_to_refresh"
            for domain in $ALL_DOMAINS_OWNED_BY_USER; do
                echo "$ALL_EMAILS_ON_SERVER" | grep "@${domain}" >> "$file_to_refresh"
            done
            echo "Updated email list for user $owner."
        done
    else
        echo "No valid email addresses provided for deletion."
        exit 1
    fi
}

# MAIN
delete_email_accounts "$@"
