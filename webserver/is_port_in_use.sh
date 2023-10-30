#!/bin/bash

# Check if the script is run with root/sudo privileges
if [ "$EUID" -ne 0 ]; then
  echo "This script requires superuser privileges to access Docker."
  exit 1
fi

# Check if the correct number of arguments is provided
if [ "$#" -ne 2 ]; then
  echo "Usage: $0 <username> <port>"
  exit 1
fi

# Assign provided arguments to variables
USERNAME="$1"
PORT="$2"

# Use `docker exec` to run the lsof command inside the existing container
#docker exec "$USERNAME" apt-get install lsof -qq -y
docker exec "$USERNAME" lsof -i :"$PORT"

# Check the exit code to determine if the port is in use or not
if [ "$?" -eq 0 ]; then
  echo "Port $PORT is in use in the container $USERNAME."
else
  echo "Port $PORT is not in use in the container $USERNAME."
fi
