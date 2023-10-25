#!/bin/bash

# Check version
version_check() {
    if [ -f "/usr/local/panel/version" ]; then
        local_version=$(cat "/usr/local/panel/version")
        echo $local_version
    else
        echo '{"error": "Local version file not found"}' >&2
        exit 1
    fi
}

version_check
