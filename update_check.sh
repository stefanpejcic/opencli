#!/bin/bash

# Define the route to check for updates
update_check() {
    # Read the local version from /usr/local/panel/version
    if [ -f "/usr/local/panel/version" ]; then
        local_version=$(cat "/usr/local/panel/version")
    else
        echo '{"error": "Local version file not found"}' >&2
        exit 1
    fi

    # Fetch the remote version from https://update.openpanel.co/
    remote_version=$(curl -s "https://update.openpanel.co/")

    if [ -z "$remote_version" ]; then
        echo '{"error": "Error fetching remote version"}' >&2
        exit 1
    fi

    # Compare the local and remote versions
    if [ "$local_version" == "$remote_version" ]; then
        echo '{"status": "Up to date", "installed_version": "'"$local_version"'"}'
    elif [ "$local_version" \> "$remote_version" ]; then
        echo '{"status": "Local version is greater", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
    else
        echo '{"status": "Update available", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
    fi
}

# Call the function and print the result
update_check
