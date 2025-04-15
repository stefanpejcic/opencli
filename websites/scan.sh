#!/bin/bash
################################################################################
# Script Name: websites/scan.sh
# Description: Scan user files for WP sites and add them to SiteManager interface.
# Usage: opencli websites-scan $username
# Author: Stefan Pejcic
# Created: 23.10.2024
# Last Modified: 23.02.2025
# Company: openpanel.com
# Copyright (c) openpanel.com
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

# Source the database configuration file
if [[ -f /usr/local/opencli/db.sh ]]; then
    source /usr/local/opencli/db.sh
else
    echo "ERROR: Database configuration file not found"
    exit 1
fi

# Set up logging
LOG_DIR="/var/log/opencli/websites"
LOG_FILE="$LOG_DIR/scan_$(date +%Y%m%d%H%M%S).log"

# Create log directory if it doesn't exist
mkdir -p "$LOG_DIR"

# Log function to write to both console and log file
log() {
    local level="$1"
    local message="$2"
    local timestamp=$(date +"%Y-%m-%d %H:%M:%S")
    echo -e "[$timestamp] [$level] $message" | tee -a "$LOG_FILE"
}

log_success() { log "SUCCESS" "$1"; }
log_info() { log "INFO" "$1"; }
log_warning() { log "WARNING" "$1"; }
log_error() { log "ERROR" "$1"; }

# Function to get domain ID using a prepared statement approach
get_domain_id() {
    local domain_name="$1"
    # Use a file to pass the SQL to avoid command injection
    local sql_file=$(mktemp)
    echo "SELECT domain_id FROM domains WHERE domain_url = '$domain_name';" > "$sql_file"
    local result=$(mysql -s -N < "$sql_file")
    rm "$sql_file"
    echo "$result"
}

get_context_for_user() {
    local username="$1"
    # Use a file to pass the SQL to avoid command injection
    local sql_file=$(mktemp)
    echo "SELECT server FROM users WHERE username = '$username';" > "$sql_file"
    local context=$(mysql -D "$mysql_database" -s -N < "$sql_file")
    rm "$sql_file"

    if [ -z "$context" ]; then
        context="$username"
    fi
    echo "$context"
}

# Function to run WordPress CLI commands with improved error handling
run_wp_cli() {
    local username="$1"
    local path="$2"
    local command="$3"
    local context="$4"

    if [ -z "$context" ]; then
        log_error "Docker context not provided for WP CLI command"
        return 1
    fi

    # Run the command and capture both stdout and stderr
    local temp_output=$(mktemp)
    if ! docker --context "$context" exec "$username" bash -c "wp --allow-root --path=${path} ${command}" > "$temp_output" 2>&1; then
        local error=$(cat "$temp_output")
        log_error "WP CLI command failed: $error"
        rm "$temp_output"
        return 1
    fi

    local output=$(cat "$temp_output")
    rm "$temp_output"
    echo "$output"
    return 0
}

# Function to check if site already exists in database
check_site_already_exists_in_db() {
    local site_name="$1"
    local sql_file=$(mktemp)

    # Escape single quotes to prevent SQL injection
    site_name=$(echo "$site_name" | sed "s/'/\\\\'/g")

    echo "SELECT EXISTS(SELECT 1 FROM sites WHERE site_name = '$site_name');" > "$sql_file"
    local result=$(mysql -s -N < "$sql_file")
    rm "$sql_file"

    if [[ "$result" -eq 1 ]]; then
        return 0  # exists
    else
        return 1  # not exist
    fi
}

# Function to insert site into database safely
insert_site_into_db() {
    local site_name="$1"
    local domain_id="$2"
    local admin_email="$3"
    local version="$4"
    local site_type="$5"

    local sql_file=$(mktemp)

    # Escape single quotes to prevent SQL injection
    site_name=$(echo "$site_name" | sed "s/'/\\\\'/g")
    admin_email=$(echo "$admin_email" | sed "s/'/\\\\'/g")
    version=$(echo "$version" | sed "s/'/\\\\'/g")

    echo "INSERT INTO sites (site_name, domain_id, admin_email, version, type)
          VALUES ('$site_name', '$domain_id', '$admin_email', '$version', '$site_type');" > "$sql_file"

    if ! mysql < "$sql_file" 2>/dev/null; then
        local error=$?
        rm "$sql_file"
        return $error
    fi

    rm "$sql_file"
    return 0
}

run_for_single_user() {
    local current_username="$1"
    log_info "Starting scan for user: $current_username"

    # Get Docker context for the user
    local context=$(get_context_for_user "$current_username")
    log_info "Using Docker context: $context"

    # Base directory to scan for wp-config.php files
    local base_directory="/home/${current_username}"

    local found_installations=()
    local existing_installations=()
    local skipped_installations=()
    local error_installations=()

    local found_count=0
    local existing_count=0
    local skipped_count=0
    local error_count=0
    local total_files=0

    # Count total files to process for progress reporting
    if command -v find &>/dev/null; then
        total_files=$(find "$base_directory" -name 'wp-config.php' | wc -l)
        log_info "Found $total_files WordPress configuration files to process"
    fi

    local current_file=0

    # Iterate through user files
    while IFS= read -r -d '' config_file_path; do
        ((current_file++))
        local progress=$((current_file * 100 / total_files))
        log_info "[$progress%] Processing file ($current_file/$total_files): $config_file_path"

        # Get sitename for manager
        # Remove /wp-config.php suffix
        local site_name=${config_file_path%/wp-config.php}

        # Remove /home/$current_username/ prefix
        site_name=${site_name/#\/home\/$current_username\//}

        # Get domain name (first part of the path)
        local domain_name="${site_name%%/*}"

        # Check if website exists in sites table
        if check_site_already_exists_in_db "$site_name"; then
            log_info "Site $site_name already exists in the SiteManager - Skipping"
            existing_installations+=("- $site_name - domain: $domain_name, config: ${config_file_path%/wp-config.php}")
            ((existing_count++))
            continue
        fi

        # Get admin email from wp-config.php
        local admin_email=$(run_wp_cli "$current_username" "$(dirname "$config_file_path")" "option get admin_email 2>/dev/null" "$context")
        if [[ ! "$admin_email" =~ "@" ]]; then
            log_warning "Invalid admin email: $admin_email for site $site_name"
            admin_email=""
        fi

        # Get WordPress version
        local version=$(run_wp_cli "$current_username" "$(dirname "$config_file_path")" "core version 2>/dev/null" "$context")
        if [ -z "$version" ]; then
            log_warning "Could not determine WordPress version for site $site_name"
            version="unknown"
        else
            log_info "WordPress version for $site_name: $version"
        fi

        # Get domain ID
        local domain_id=$(get_domain_id "$domain_name")
        if ! [[ "$domain_id" =~ ^[0-9]+$ ]]; then
            log_warning "ID not detected for domain $domain_name - make sure that domain is added for user - Skipping this site"
            skipped_installations+=("- $site_name - domain: $domain_name, reason: No domain ID")
            ((skipped_count++))
            continue
        fi

        log_info "Adding website $site_name to Site Manager"
        if insert_site_into_db "$site_name" "$domain_id" "$admin_email" "$version" "wordpress"; then
            log_success "Site $site_name added to database"

            log_info "Enabling auto-login to wp-admin from Site Manager interface for $site_name"
            local wp_login_result=$(run_wp_cli "$current_username" "$(dirname "$config_file_path")" "package install aaemnnosttv/wp-cli-login-command" "$context")

            if [ $? -ne 0 ]; then
                log_warning "Failed to install WP-CLI login command for $site_name"
            fi

            found_installations+=("- $site_name, domain: $domain_name, email: $admin_email, version: $version")
            ((found_count++))
        else
            log_error "Failed to add site $site_name to database"
            error_installations+=("- $site_name - domain: $domain_name, reason: Database insertion error")
            ((error_count++))
        fi
    done < <(find "$base_directory" -name 'wp-config.php' -print0)

    # Summary messages
    log_info "Scan completed for user $current_username"
    log_info "Summary: $found_count new sites, $existing_count existing, $skipped_count skipped, $error_count errors"

    if [ ${#found_installations[@]} -gt 0 ]; then
        log_success "Detected $found_count new WordPress installations:"
        for installation in "${found_installations[@]}"; do
            log_info "$installation"
        done
    fi

    if [ ${#existing_installations[@]} -gt 0 ]; then
        log_info "Found $existing_count existing WordPress installations:"
        for installation in "${existing_installations[@]}"; do
            log_info "$installation"
        done
    fi

    if [ ${#skipped_installations[@]} -gt 0 ]; then
        log_warning "Skipped $skipped_count WordPress installations:"
        for installation in "${skipped_installations[@]}"; do
            log_warning "$installation"
        done
    fi

    if [ ${#error_installations[@]} -gt 0 ]; then
        log_error "Encountered $error_count errors:"
        for installation in "${error_installations[@]}"; do
            log_error "$installation"
        done
    fi

    if [ $found_count -eq 0 ] && [ $existing_count -eq 0 ] && [ $skipped_count -eq 0 ]; then
        log_warning "No WordPress installations detected for user $current_username"
    fi
}

# Main execution starts here
main() {
    log_info "Starting WordPress site scanner"
    log_info "Log file: $LOG_FILE"

    if [ $# -eq 0 ]; then
        log_error "Usage: opencli websites-scan <USERNAME> OR opencli websites-scan -all"
        return 1
    elif [[ "$1" == "-all" ]]; then
        # ALL USERS
        log_info "Scanning all users for WordPress installations"

        # Use a more secure way to get users
        local users=$(opencli user-list --json | grep -v 'SUSPENDED' | awk -F'"' '/username/ {print $4}')

        if [[ -z "$users" || "$users" == "No users." ]]; then
            log_error "No users found in the database"
            return 1
        fi

        local total_users=$(echo "$users" | wc -w)
        log_info "Processing $total_users users"

        local current_user_index=1

        for user in $users; do
            log_info "Processing user: $user ($current_user_index/$total_users)"
            run_for_single_user "$user"
            log_info "Completed user: $user"
            log_info "------------------------------"
            ((current_user_index++))
        done
        log_success "All users processed successfully"

    # SINGLE USER
    elif [ $# -eq 1 ]; then
        run_for_single_user "$1"
        log_success "User $1 processed successfully"
    else
        log_error "Usage: opencli websites-scan <USERNAME> OR opencli websites-scan -all"
        return 1
    fi

    log_info "Scan completed. Log saved to: $LOG_FILE"
    return 0
}

# Execute main function with all arguments
main "$@"
exit $?
