#!/bin/bash

# Initialize a flag to determine whether to show file content
show_content=false

# Check for optional flag "-show"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --show)
            show_content=true
            shift
            ;;
        *)
            username="$1"
            shift
            ;;
    esac
done

# Check if a username is provided as an argument
if [ -z "$username" ]; then
    echo "Usage: $0 [-show] <username>"
    exit 1
fi

# Define the directory and file path
directory="/home/$username/etc/.panel/php"
file_path="$directory/php_available_versions.json"

# Ensure the directory exists
if ! mkdir -p "$directory"; then
    echo "Error: Unable to create directory $directory"
    exit 1
fi

# Run the command to fetch PHP versions in the background
if (docker exec "$username" apt-get update > /dev/null 2>&1 && \
    docker exec "$username" apt-cache search php-fpm | grep -v '^php-fpm' | awk '{print $1}' > "$file_path") & then
    # Display dots while the process is running
    while true; do
        echo -n "."
        sleep 1
        if ! ps -p $! > /dev/null; then
            break
        fi
    done
    echo  # Print a newline after the dots

    # Check if the background process was completed successfully
    if wait $!; then
        if [ "$show_content" = true ]; then
            echo "Available PHP versions for user $username:"
            cat "$file_path"
        else
            echo "PHP versions for user $username have been updated and stored in $file_path."
        fi
    else
        echo "Error: Failed to update PHP versions."
        exit 1
    fi
else
    echo "Error: Failed to run the update command in a Docker container for user $username."
    exit 1
fi
