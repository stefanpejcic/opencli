#!/bin/bash

# Check if the correct number of arguments are provided
if [ "$#" -ne 1 ]; then
  echo "Usage: $0 <username>"
  exit 1
fi

container_name="$1"

# Check if the Docker container with the given name exists
if ! docker ps -a --format '{{.Names}}' | grep -q "^$container_name$"; then
  echo "Error: Docker container with the name '$container_name' does not exist."
  exit 1
fi

# Run the command to list installed PHP versions inside the Docker container,
# then filter out the "default" version
docker exec -it "$container_name" update-alternatives --list php | awk -F'/' '{print $NF}' | grep -v 'default'
