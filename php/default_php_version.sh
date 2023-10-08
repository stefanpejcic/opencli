#!/bin/bash

# Function to update PHP version in the configuration file
update_php_version() {
    local new_php_version="$1"
    local config_file="$2"

    # Use sed to update the PHP version in the configuration file
    sed -i "s/\(default_php_version:\s*\)php[0-9.]\+/\\1php$new_php_version/" "$config_file"
}

# Function to validate the PHP version format
validate_php_version() {
    local php_version="$1"
    if [[ ! "$php_version" =~ ^[0-9]\.[0-9]$ ]]; then
        echo "Invalid PHP version format. Please use the format 'number.number' (e.g., 8.1 or 5.6)."
        exit 1
    fi
}

# Check if username argument is provided
if [ $# -lt 1 ]; then
    echo "Usage: $0 <username> [--update <new_php_version>]"
    exit 1
fi

username="$1"
config_file="/usr/local/panel/core/users/$username/server_config.yml"

# Check if the configuration file exists
if [ ! -e "$config_file" ]; then
    echo "Configuration file for user '$username' not found."
    exit 1
fi

if [ "$2" == "--update" ]; then
    # Check if a new PHP version is provided
    if [ -z "$3" ]; then
        echo "Usage: $0 <username> --update <new_php_version>"
        exit 1
    fi

    new_php_version="$3"
    validate_php_version "$new_php_version"
    update_php_version "$new_php_version" "$config_file"
    echo "Default PHP version for user '$username' updated to: $new_php_version"
else
    # Use awk to extract the PHP version from the YAML file
    php_version=$(awk '/default_php_version/ {print $2}' "$config_file")

    if [ -n "$php_version" ]; then
        echo "Default PHP version for user '$username' is: $php_version"
    else
        echo "Default PHP version for user: '$username' not found in the configuration file."
        exit 1
    fi
fi
