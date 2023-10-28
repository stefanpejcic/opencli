#!/bin/bash

# Function to apply permissions and ownership changes within a Docker container
apply_permissions_in_container() {
  local container_name="$1"
  
  # Check if the container exists
  if docker inspect -f '{{.State.Running}}' "$container_name" &>/dev/null; then
    docker exec -u 0 -it "$container_name" bash -c "find /home/$container_name -type f -exec chown $container_name:$container_name {} \; && find /home/$container_name -type f \( -name '*.php' -o -name '*.cgi' -o -name '*.pl' \) -exec chmod 755 {} \; && find /home/$container_name -type f -name '*.log' -exec chmod 640 {} \; && find /home/$container_name -type d -exec chown $container_name:$container_name {} \; && find /home/$container_name -type d -exec chmod 755 {} \;"
  else
    echo "Container $container_name not found or is not running."
  fi
}

# Check if the --all flag is provided
if [ "$1" == "--all" ]; then
  # Apply changes to all running Docker containers
  for container in $(docker ps --format '{{.Names}}'); do
    apply_permissions_in_container "$container"
  done
elif [ $# -eq 1 ]; then
  # Check if a username is provided as an argument
  username="$1"
  
  # Apply changes to a specific user's Docker container
  apply_permissions_in_container "$username"
else
  echo "Usage: $0 <username> OR $0 --all"
  exit 1
fi
