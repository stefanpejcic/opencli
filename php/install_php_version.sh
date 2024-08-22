#!/bin/bash
################################################################################
# Script Name: php/install_php_version.sh
# Description: Install a specific PHP version (and extensions) for a user.
# Usage: opencli php-install_php_version <username> <php_version>
# Author: Stefan Pejcic
# Created: 07.10.2023
# Last Modified: 18.07.2024
# Company: openpanel.co
# Copyright (c) openpanel.co
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
if [ "$#" -ne 2 ]; then
  echo "Usage: $0 <username> <php_version>"
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



echo "## Started installation for PHP version $php_version"

# Check if each extension is already installed
if docker exec "$container_name" bash -c 'dpkg -l | grep -q "ii  php${php_version}-fpm"'; then
  echo "## ERROR: PHP $php_version is already installed."
  echo "## Setting recommended extensions.."
else
  echo "## PHP $php_version is not installed, starting installation.."  
  install_php() {
    docker exec "$container_name" bash -c "
      apt-get update && 
      apt --fix-broken install && 
      dpkg --configure -a && 
      apt-get install -y php$php_version-fpm
    "
  }

  # Retry mechanism
  retries=5
  count=0
  while [ $count -lt $retries ]; do
    # Check if `apt-get` is currently running and wait if necessary
    if ! docker exec "$container_name" bash -c "fuser /var/lib/apt/lists/lock >/dev/null 2>&1"; then
      install_php
      if [ $? -eq 0 ]; then
        break
      else
        echo "## Installation failed, retrying..."
        count=$((count + 1))
        sleep 10  # Wait before retrying
      fi
    else
      echo "## Waiting for apt-get to release the lock..."
      sleep 5  # Wait before checking again
    fi
  done
  


  if [ $count -eq $retries ]; then
    echo "## ERROR: PHP $php_version installation failed after multiple attempts."
    exit 1
  fi

  echo "## Installed, checking if configured properly.."


  # Check if actually installed
  if docker exec "$container_name" bash -c "dpkg -l | grep -q \"ii  php$php_version\""; then
    # Proceed to extensions..
    echo "## PHP version $php_version is now installed, setting default PHP extensions.."
  else
    echo "## ERROR: PHP $php_version installation failed."
    exit 1
  fi
fi


# uodate just once, then start extensions
docker exec "$container_name" bash -c "apt-get update"

# Install php extensions in parallel using xargs
printf "%s\n" "${extensions_to_install[@]}" | xargs -n 1 -P 8 -I {} bash -c '
  if docker exec "$container_name" dpkg -l | grep -q "ii  $extension"; then
    echo "## {} is already installed."
  else
    # Install the extension
    docker exec "$container_name" bash -c "apt-get install -y {}"
    wait $!
    echo "## PHP extension $extension is now successfully installed."
  fi
'


### Settings limits for FPM service
echo "## Setting recommended limits for PHP-FPM service"

docker exec "$container_name" bash -c "sed -i 's/^upload_max_filesize = .*/upload_max_filesize = 1024M/' /etc/php/$php_version/fpm/php.ini"
 wait $!
docker exec "$container_name" bash -c "sed -i 's/^opcache.enable=.*/opcache.enable=1/' /etc/php/$php_version/fpm/php.ini"
 wait $!
echo "upload_max_filesize = 1024M"
docker exec "$container_name" bash -c "sed -i 's/^max_input_time = .*/max_input_time = 600/' /etc/php/$php_version/fpm/php.ini"
 wait $!
echo "max_input_time = -1"
docker exec "$container_name" bash -c "sed -i 's/^memory_limit = .*/memory_limit = -1/' /etc/php/$php_version/fpm/php.ini"
 wait $!
echo "post_max_size = 1024M"
docker exec "$container_name" bash -c "sed -i 's/^post_max_size = .*/post_max_size = 1024M/' /etc/php/$php_version/fpm/php.ini"
 wait $!
 echo "sendmail_path = '/usr/bin/msmtp -t'"
docker exec "$container_name" bash -c "sed -i 's|^;sendmail_path = .*|sendmail_path = "/usr/bin/msmtp -t"|' /etc/php/$php_version/fpm/php.ini"
 wait $!
 echo "max_execution_time = 600"
docker exec "$container_name" bash -c "sed -i 's/^max_execution_time = .*/max_execution_time = 600/' /etc/php/$php_version/fpm/php.ini"


### Settings limits for CLI version
echo "## Setting recommended limits for PHP-CLI"

docker exec "$container_name" bash -c "sed -i 's/^upload_max_filesize = .*/upload_max_filesize = 1024M/' /etc/php/$php_version/fpm/cli.ini"
 wait $!
docker exec "$container_name" bash -c "sed -i 's/^opcache.enable=.*/opcache.enable=1/' /etc/php/$php_version/fpm/cli.ini"
 wait $!
echo "upload_max_filesize = 1024M"
docker exec "$container_name" bash -c "sed -i 's/^max_input_time = .*/max_input_time = 600/' /etc/php/$php_version/cli/php.ini"
 wait $!
echo "max_input_time = -1"
docker exec "$container_name" bash -c "sed -i 's/^memory_limit = .*/memory_limit = -1/' /etc/php/$php_version/cli/php.ini"
 wait $!
echo "post_max_size = 1024M"
docker exec "$container_name" bash -c "sed -i 's/^post_max_size = .*/post_max_size = 1024M/' /etc/php/$php_version/cli/php.ini"
 wait $!
 echo "sendmail_path = '/usr/bin/msmtp -t'"
docker exec "$container_name" bash -c "sed -i 's|^;sendmail_path = .*|sendmail_path = "/usr/bin/msmtp -t"|' /etc/php/$php_version/cli/php.ini"
 wait $!
echo "max_execution_time = 600"
docker exec "$container_name" bash -c "sed -i 's/^max_execution_time = .*/max_execution_time = 600/' /etc/php/$php_version/cli/php.ini"



echo "## Setting service for PHP $php_version"
docker exec $container_name find /etc/php/ -type f -name "www.conf" -exec sed -i 's/user = .*/user = '"$container_name"'/' {} \;
wait $!
echo "## Restarting all installed PHP versions.."
docker exec $container_name bash -c 'for phpv in $(ls /etc/php/); do if [[ -d "/etc/php/$phpv/fpm" ]]; then service php${phpv}-fpm restart; fi done'

echo "## PHP version $php_version is successfully installed."
