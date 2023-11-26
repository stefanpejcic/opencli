#!/bin/bash

# Function to print usage
print_usage() {
    echo "Usage: $0 <check|enable|disable> <container_name>"
    exit 1
}

# Check if arguments are provided
if [ $# -ne 2 ]; then
    print_usage
fi

# Parse command-line options
action=$1
container_name=$2

# Check if the action is valid
if [[ "$action" != "check" && "$action" != "enable" && "$action" != "disable" ]]; then
    print_usage
fi

# Run the action inside the Docker container
case $action in
    check)
        docker exec "$container_name" service ssh status
        # Check if checking status was successful
        if [ $? -eq 0 ]; then
            echo "SSH service is running in container $container_name."
        else
            echo "SSH service is not running in container $container_name."
        fi
        ;;
    enable)
        docker exec "$container_name" service ssh start
        # Check if enabling was successful
        if [ $? -eq 0 ]; then
            echo "SSH service enabled successfully in container $container_name."
        else
            echo "Failed to enable SSH service in container $container_name."
        fi
        ;;
    disable)
        docker exec "$container_name" service ssh stop
        # Check if disabling was successful
        if [ $? -eq 0 ]; then
            echo "SSH service disabled successfully in container $container_name."
        else
            echo "Failed to disable SSH service in container $container_name."
        fi
        ;;
    *)
        print_usage
        ;;
esac
