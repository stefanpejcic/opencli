#!/bin/bash

# Check if at least one argument is provided
if [ "$#" -lt 1 ]; then
    echo "Usage: opencli email-setup <command> [<args>...]"
    exit 1
fi

# Extract the command and arguments
command="$@"

# Execute the command inside the Docker container
docker exec openadmin_mailserver setup $command
