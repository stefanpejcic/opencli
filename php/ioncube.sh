#!/bin/bash
################################################################################
# Script Name: php/ioncube.sh
# Description: Enable IonCube Loader for every installed PHP version
# Usage: opencli php-ioncube <username> [--reuse=/path/to/ioncube.so]
# Author: Stefan Pejcic
# Created: 26.07.2024
# Last Modified: 09.10.2024
# Company: openpanel.com
# Copyright (c) Stefan Pejcic
# 
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
# 
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
# 
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
# THE SOFTWARE.
################################################################################

# Check if the correct number of arguments are provided
if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "Usage: opencli php-ioncube <username> [--reuse=/path/to/ioncube.so]"
  exit 1
fi

container_name="$1"
reuse_path=""

# Parse optional --reuse flag
if [[ "$2" =~ ^--reuse= ]]; then
  reuse_path="${2#--reuse=}"
fi

# Check if the Docker container with the given name exists
if ! docker ps -a --format '{{.Names}}' | grep -q "^$container_name$"; then
  echo "Error: Docker container with the name '$container_name' does not exist."
  exit 1
fi

if [[ -n "$reuse_path" ]]; then
  # Check if the provided path exists and is a .so file
  if [ ! -f "$reuse_path" ]; then
    echo "Error: The file '$reuse_path' does not exist."
    exit 1
  elif [[ "$reuse_path" != *.so ]]; then
    echo "Error: The file '$reuse_path' is not a .so file."
    exit 1
  fi

  echo "Using existing ionCube loader from $reuse_path"
  # Copy the provided .so file into the container
  docker cp "$reuse_path" "$container_name:/tmp/ioncube_loader_custom.so"

else
  echo "Downloading latest ioncube loader extensions from https://www.ioncube.com/loaders.php"

  # Step 1: Download the ionCube loaders tarball
  docker exec -it "$container_name" bash -c "cd /tmp && wget https://downloads.ioncube.com/loader_downloads/ioncube_loaders_lin_x86-64.tar.gz > /dev/null 2>&1"

  # Step 2: Uncompress the downloaded tarball
  docker exec -it "$container_name" bash -c "cd /tmp && tar -xzf ioncube_loaders_lin_x86-64.tar.gz"

fi

echo "Listing installed PHP versions for user $container_name"
echo ""
# List PHP versions
php_versions=$(docker exec -it "$container_name" update-alternatives --list php | awk -F'/' '{print $NF}' | grep -v 'default' | tr -d '\r')

# Process each PHP version
for php_version in $php_versions; do
  # Strip the 'php' part to get the version number
  php_version_number=$(echo "$php_version" | sed 's/php//')

  echo "### CHECKING PHP VERSION $php_version_number"

  # Check if ionCube Loader is already enabled
  if docker exec -it "$container_name" bash -c "$php_version -m | grep -qi ioncube"; then
    echo "ionCube Loader is already enabled for $php_version"
    continue
  fi

  # Determine the ionCube loader file path
  if [[ -n "$reuse_path" ]]; then
    ioncube_file="/tmp/ioncube_loader_custom.so"
  else
    ioncube_file="/tmp/ioncube/ioncube_loader_lin_${php_version_number}.so"
  fi

  # Check if the ionCube loader file exists for this PHP version
  if docker exec -it "$container_name" test -f "$ioncube_file"; then
    echo "IonCube Loader extension is available for PHP version: $php_version_number - enabling.."

    # Function to add zend_extension line if it doesn't exist
    add_zend_extension_if_not_exists() {
      local ini_file="$1"
      local zend_extension_line="zend_extension=${ioncube_file}"
      
      # Check if zend_extension line exists, if not, append it
      if ! docker exec -it "$container_name" grep -q "^zend_extension=.*ioncube_loader_lin_${php_version_number}.so" "$ini_file"; then
        echo "$zend_extension_line" | docker exec -i "$container_name" tee -a "$ini_file" > /dev/null
      else
        docker exec -it "$container_name" sed -i "s|^zend_extension=.*ioncube_loader_lin_${php_version_number}.so|$zend_extension_line|" "$ini_file"
      fi
    }

    # Update CLI and FPM php.ini files
    add_zend_extension_if_not_exists "/etc/php/$php_version_number/cli/php.ini"
    add_zend_extension_if_not_exists "/etc/php/$php_version_number/fpm/php.ini"

    # Check if the PHP-FPM service is active
    service_status=$(docker exec -it "$container_name" sh -c "service php${php_version_number}-fpm status")
  
    if echo "$service_status" | grep -q "active (running)"; then
      echo "Restarting PHP-FPM service for PHP version $php_version_number"
      docker exec -it "$container_name" sh -c "service php${php_version_number}-fpm restart"
    fi

  else
    echo "ERROR: IonCube Loader extension ($ioncube_file) is not currently available for PHP version: $php_version_number"
    echo "       Please check manually on ioncube website if extension is available for your version: https://downloads.ioncube.com/loader_downloads/ioncube_loaders_lin_x86-64.tar.gz"
  fi
  
done

echo ""
echo "DONE"
