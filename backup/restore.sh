#!/bin/bash

RED="\e[31m"
GREEN="\e[32m"
ENDCOLOR="\e[0m"

# Get the backup directory path from the first argument
backup_dir="$1"

# Check if a backup directory path is provided
if [ -z "$backup_dir" ]; then
  echo "Usage: $0 <backup_directory>"
  exit 1
fi

# Extract the container_name from the backup directory path
container_name=$(basename "$(dirname "$backup_dir")")
volume_name="mysql-$container_name"
backup_file="$backup_dir/docker_${container_name}_$(basename "$backup_dir").tar"
sql_dump_file="$backup_dir/user_data_dump.sql"

#echo $container_name
#echo $volume_name
#echo $backup_file
#echo $sql_dump_file

# Check if the backup files exist
if [ ! -f "$backup_file" ] || [ ! -f "$sql_dump_file" ]; then
  echo "${RED}ERROR${ENDCOLOR}: Backup files not found in the specified directory."
  exit 1
fi

echo "Restoring Docker container $container_name..."
if [ ! "$(docker ps -a -q -f name=${container_name})" ] && [ ! "$(docker volume ls -q -f name=${container_name})" ]; then
    docker import "$backup_file" "$container_name-image" || { echo "${RED}ERROR${ENDCOLOR}: Failed to restore Docker container $container_name"; exit 1; }

    docker volume create --name "$volume_name"

    # Copy data to the volume
    rsync -avR $backup_dir/mysql-volume/ /var/lib/docker/volumes/$volume_name/_data/
    
    # run your container
    $$$$$$$$docker run -d --name <name> my-docker-image
fi






RUN KOMANDA

copy vhosts
copy ssl
a2ensite



insert in db user
domains
sites
