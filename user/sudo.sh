#!/bin/bash

if [ $# -lt 2 ] || [ $# -gt 3 ]; then
    echo "Usage: opencli user-sudo <username> <enable/disable/status>"
    exit 1
fi

username="$1"
action="$2"
entrypoint_path="/etc/entrypoint.sh"

# Check if the container exists
container_id=$(docker ps -q -f name="$username")
if [ -z "$container_id" ]; then
    echo "ERROR: Docker container for username '$username' not found."
    exit 1
fi


if [ "$action" == "enable" ]; then
    docker exec "$container_id" sed -i "s/SUDO=\"[^\"]*\"/SUDO=\"YES\"/" "$entrypoint_path"
    docker exec "$container_id" usermod -aG sudo -u "$username"
    echo "SUDO enabled for user $username."
elif [ "$action" == "disable" ]; then
    docker exec "$container_id" sed -i "s/SUDO=\"[^\"]*\"/SUDO=\"NO\"/" "$entrypoint_path"
    docker exec "$container_id" sed -i "/^sudo:.*$username/d" /etc/group
    echo "SUDO disabled for user $username."
elif [ "$action" == "status" ]; then
    status=$(docker exec "$container_id" grep -o 'SUDO="[^"]*"' "$entrypoint_path" | cut -d'"' -f2)
    if [ "$status" == "YES" ]; then
        echo "SUDO is enabled."
    elif [ "$status" == "NO" ]; then
        echo "SUDO is disabled."
    else
        echo "Unknown status."
        exit 1
    fi
else
    echo "Invalid action. Please choose 'enable', 'disable', or 'status'."
    exit 1
fi
