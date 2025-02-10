#!/bin/bash
################################################################################
# Script Name: php/install_php_version.sh
# Description: Install a specific PHP version (and extensions) for a user.
# Usage: opencli php-install_version <username> <php_version>
# Author: Stefan Pejcic
# Created: 07.10.2023
# Last Modified: 07.10.2024
# Company: openpanel.com
# Copyright (c) openpanel.com
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

# Define the default extensions
default_extensions=(
  fpm
  imagick
  mysql
  curl
  gd
  mbstring
  xml
  xmlrpc
  soap
  intl
  zip
  bcmath
  calendar
  exif
  ftp
  ldap
  sockets
  sysvmsg
  sysvsem
  sysvshm
  tidy
  uuid
  opcache
  redis
  memcached
  mysqli
)


get_context_for_user() {
     source /usr/local/admin/scripts/db.sh
        username_query="SELECT server FROM users WHERE username = '$container_name'"
        context=$(mysql -D "$mysql_database" -e "$username_query" -sN)
        if [ -z "$context" ]; then
            context=$container_name
        fi
}



make_sure_container_exists() {
  if ! docker --context $context ps -a --format '{{.Names}}' | grep -q "^$container_name$"; then
    echo "Error: Docker container with the name '$container_name' does not exist."
    exit 1
  fi
}


extensions_file="/etc/openpanel/php/extensions.txt"

if [[ -f "$extensions_file" ]]; then
    mapfile -t extensions < "$extensions_file"
else
    extensions=("${default_extensions[@]}")
fi

extensions_to_install=()

# Loop through each extension and add the prefix
for ext in "${extensions[@]}"; do
    extensions_to_install+=("php$php_version-$ext")
done

echo "## Started installation for PHP version $php_version"

get_context_for_user
make_sure_container_exists





# Check if php version already installed
if docker --context $context exec "$container_name" bash -c "dpkg -l | grep -q \"ii  php${php_version}-fpm\""; then
  echo "## ERROR: PHP $php_version is already installed."
  if [[ -f "$extensions_file" ]]; then
    echo "## Setting php extensions specified from the $extensions_file file.."
  else
    echo "## Setting recommended extensions.."
  fi
else
  echo "## PHP $php_version is not installed, starting installation.."  
  install_php() {
    docker --context $context exec "$container_name" bash -c "
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
    if ! docker --context $context exec "$container_name" bash -c "fuser /var/lib/apt/lists/lock >/dev/null 2>&1"; then
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
  if docker --context $context exec "$container_name" bash -c "dpkg -l | grep -q \"ii  php$php_version\""; then
    # Proceed to extensions..
    echo "## PHP version $php_version is now installed, setting default PHP extensions.."
  else
    echo "## ERROR: PHP $php_version installation failed."
    exit 1
  fi
fi


# uodate just once, then start extensions
docker --context $context exec "$container_name" bash -c "apt-get update"


# Output the resulting list (for debugging purposes)
echo "## Installing PHP extensions"
echo "extensions that will be installed: ${extensions_to_install[@]}"

# Install php extensions
for extension in "${extensions_to_install[@]}"; do
  if docker --context $context exec "$container_name" dpkg -l | grep -q "ii  $extension"; then
    echo "## $extension is already installed."
  else
    # Install the extension
    docker --context $context exec "$container_name" bash -c "apt-get install -y $extension"
    echo "## PHP extension $extension is now successfully installed."
  fi
done


### Settings limits for FPM service
echo "## Setting recommended limits for PHP-FPM service"



ini_file="/etc/openpanel/php/ini.txt"


# Check if the ini file exists
if [[ -f "$ini_file" ]]; then
    echo "Setting limits from the $ini_file file:"
    cat $ini_file
    # Read the ini file line by line
    while IFS='=' read -r key value; do
        # Trim whitespace
        key=$(echo "$key" | xargs)
        value=$(echo "$value" | xargs)

        # Skip empty lines or lines that don't have an '='
        if [[ -z "$key" ]] || [[ -z "$value" ]]; then
            continue
        fi

        # Determine the sed command based on the setting
        if [[ "$key" == "sendmail_path" ]]; then
            sed_command="sed -i 's|^;sendmail_path = .*|sendmail_path = \"$value\"|'"
        else
            sed_command="sed -i 's/^$key = .*/$key = $value/'"
        fi

        # Execute the sed command in the Docker container
        ###echo "$key = $value"
        docker --context $context exec "$container_name" bash -c "$sed_command /etc/php/$php_version/cli/php.ini"
        wait $!
        docker --context $context exec "$container_name" bash -c "$sed_command /etc/php/$php_version/fpm/php.ini"
        wait $!
    done < "$ini_file"
    echo "Finished setting limits."
else
    echo "Configuration file $ini_file not found. Setting default recommended settings in php.ini file:"
    echo "upload_max_filesize = 1024M"
    echo "max_input_time = -1"
    echo "post_max_size = 1024M"
    echo "sendmail_path = '/usr/bin/msmtp -t'"
    echo "max_execution_time = 600"
    
    docker --context $context exec "$container_name" bash -c "sed -i 's/^upload_max_filesize = .*/upload_max_filesize = 1024M/' /etc/php/$php_version/fpm/php.ini"
     wait $!
    docker --context $context exec "$container_name" bash -c "sed -i 's/^upload_max_filesize = .*/upload_max_filesize = 1024M/' /etc/php/$php_version/cli/php.ini"
     wait $!

     
    docker --context $context exec "$container_name" bash -c "sed -i 's/^opcache.enable=.*/opcache.enable=1/' /etc/php/$php_version/fpm/php.ini"
     wait $!
    docker --context $context exec "$container_name" bash -c "sed -i 's/^opcache.enable=.*/opcache.enable=1/' /etc/php/$php_version/cli/php.ini"
     wait $!

     
    docker --context $context exec "$container_name" bash -c "sed -i 's/^max_input_time = .*/max_input_time = 600/' /etc/php/$php_version/fpm/php.ini"
     wait $!
    docker --context $context exec "$container_name" bash -c "sed -i 's/^max_input_time = .*/max_input_time = 600/' /etc/php/$php_version/cli/php.ini"
     wait $!

     
    docker --context $context exec "$container_name" bash -c "sed -i 's/^memory_limit = .*/memory_limit = -1/' /etc/php/$php_version/fpm/php.ini"
     wait $!
    docker --context $context exec "$container_name" bash -c "sed -i 's/^memory_limit = .*/memory_limit = -1/' /etc/php/$php_version/cli/php.ini"
     wait $!

     
    docker --context $context exec "$container_name" bash -c "sed -i 's/^post_max_size = .*/post_max_size = 1024M/' /etc/php/$php_version/fpm/php.ini"
     wait $!
    docker --context $context exec "$container_name" bash -c "sed -i 's/^post_max_size = .*/post_max_size = 1024M/' /etc/php/$php_version/cli/php.ini"
     wait $!

     
     
    docker --context $context exec "$container_name" sh -c "sed -i 's|^;sendmail_path = .*|sendmail_path = /usr/bin/msmtp -t|' /etc/php/$php_version/fpm/php.ini"
     wait $!
    docker --context $context exec "$container_name" bash -c "sed -i 's|^;sendmail_path = .*|sendmail_path = "/usr/bin/msmtp -t"|' /etc/php/$php_version/cli/php.ini"
     wait $!
     
    docker --context $context exec "$container_name" bash -c "sed -i 's/^max_execution_time = .*/max_execution_time = 600/' /etc/php/$php_version/fpm/php.ini"
    docker --context $context exec "$container_name" bash -c "sed -i 's/^max_execution_time = .*/max_execution_time = 600/' /etc/php/$php_version/cli/php.ini"

    echo "Finished setting limits."

fi



echo "## Starting installed PHP versions.."
docker --context $context exec $container_name bash -c "service php${php_version}-fpm restart"

# STARTS ALL VERSIONS #docker --context $context exec $container_name bash -c 'for phpv in $(ls /etc/php/); do if [[ -d "/etc/php/$phpv/fpm" ]]; then service php${phpv}-fpm restart; fi done'

echo "## PHP version $php_version is successfully installed."
