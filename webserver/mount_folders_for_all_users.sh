#!/bin/bash

# Step 1: List all container names
container_names=$(docker ps -a --format '{{.Names}}')

# Step 2: Loop through container names and check for storage files
for container_name in $container_names; do
    storage_file="/home/storage_file_$container_name"
    
    # Step 3: Check if the storage file exists
    if [ -e "$storage_file" ]; then
        # Step 4: Mount the storage file for the user
        mount -o loop "$storage_file" "/home/$container_name"
        echo "Mounted storage file for user: $container_name"
    else
        echo "Storage file does not exist for user: $container_name"
    fi
done
