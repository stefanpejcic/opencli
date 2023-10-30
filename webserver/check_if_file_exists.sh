#!/bin/bash

# Check if the script is run with root/sudo privileges
if [ "$EUID" -ne 0 ]; then
  echo "This script requires superuser privileges to access Docker."
  exit 1
fi

# Check if the correct number of arguments is provided
if [ "$#" -ne 2 ]; then
  echo "Usage: $0 <username> <file_path>"
  exit 1
fi

# Assign provided arguments to variables
USERNAME="$1"
FILE_PATH="$2"

# Construct the full path to the file inside the container
FULL_PATH="/home/$USERNAME/$FILE_PATH"

# Use `docker exec` to check if the file exists inside the container
docker exec "$USERNAME" test -e "$FULL_PATH"

# Check the exit code to determine if the file exists or not
if [ "$?" -eq 0 ]; then
  echo "$FULL_PATH exists in the container $USERNAME."
else
  echo "$FULL_PATH does not exist in the container $USERNAME."
fi
