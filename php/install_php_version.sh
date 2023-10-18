#!/bin/bash

# Check if the correct number of arguments are provided
if [ "$#" -ne 2 ]; then
  echo "Usage: $0 <container_name> <php_version>"
  exit 1
fi

container_name="$1"
php_version="$2"

# Check if the Docker container with the given name exists
if ! docker ps -a --format '{{.Names}}' | grep -q "^$container_name$"; then
  echo "Error: Docker container with the name '$container_name' does not exist."
  exit 1
fi

# Define the list of extensions to install
extensions_to_install=(
  php$php_version-fpm
  php$php_version-imagick
  php$php_version-mysql
  php$php_version-curl
  php$php_version-gd
  php$php_version-mbstring
  php$php_version-xml
  php$php_version-xmlrpc
  php$php_version-soap
  php$php_version-intl
  php$php_version-zip
  php$php_version-bcmath
  php$php_version-calendar
  php$php_version-exif
  php$php_version-ftp
  php$php_version-ldap
  php$php_version-sockets
  php$php_version-sysvmsg
  php$php_version-sysvsem
  php$php_version-sysvshm
  php$php_version-tidy
  php$php_version-uuid
  php$php_version-opcache
  php$php_version-redis
  php$php_version-memcached
)

# Check if each extension is already installed
for extension in "${extensions_to_install[@]}"; do
  if docker exec "$container_name" dpkg -l | grep -q "ii  $extension"; then
    echo "$extension is already installed in the Docker container '$container_name'."
  else
    # Install the extension
    docker exec -it "$container_name" bash -c "apt-get update && apt-get install -y $extension"
    echo "$extension has been installed in the Docker container '$container_name'."
  fi
done
