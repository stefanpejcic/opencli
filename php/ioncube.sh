#!/bin/bash


# Check if the correct number of arguments are provided
if [ "$#" -ne 1 ]; then
  echo "Usage: $0 <username>"
  exit 1
fi

container_name="$1"

# Check if the Docker container with the given name exists
if ! docker ps -a --format '{{.Names}}' | grep -q "^$container_name$"; then
  echo "Error: Docker container with the name '$container_name' does not exist."
  exit 1
fi

echo "Checking if installed PHP versions have IonCube Loader extension.."
echo ""
# Download latest ioncube files
docker exec -it "$container_name" bash -c "cd /tmp && wget https://downloads.ioncube.com/loader_downloads/ioncube_loaders_lin_x86-64.tar.gz > /dev/null 2>&1"
docker exec -it "$container_name" bash -c "cp -r /tmp/ioncube/ioncube_loader_lin_*.so /usr/lib/php/20*/"

# List php versions
php_versions=$(docker exec -it "$container_name" update-alternatives --list php | awk -F'/' '{print $NF}' | grep -v 'default' | tr -d '\r')


# Process each PHP version
for php_version in $php_versions; do

  # Strip the 'php' part to get the version number
  php_version_number=$(echo "$php_version" | sed 's/php//')


  # Check if already enabled
  if docker exec -it "$container_name" bash -c "$php_version -m | grep -qi ioncube"; then
      echo "ionCube Loader is already enabled for PHP version $php_version_number"
      continue
  fi

  # Check if the ionCube loader file exists for this PHP version
  ioncube_file="/tmp/ioncube/ioncube_loader_lin_${php_version_number}.so"
  if docker exec -it "$container_name" test -f "$ioncube_file"; then
    echo "IonCube Loader extension is available for PHP version: $php_version_number - enabling.."
    
    cli_php_ini="/etc/php/$php_version_number/cli/php.ini"
    fpm_php_ini="/etc/php/$php_version_number/fpm/php.ini"
  
    docker exec -it "$container_name" sh -c "
      sed -i 's/^;zend_extension=.*/zend_extension=ioncube_loader_lin_$php_version_number.so/' $cli_php_ini
      sed -i 's/^;zend_extension=.*/zend_extension=ioncube_loader_lin_$php_version_number.so/' $fpm_php_ini
    "

    #fallback if already was enabled..
    docker exec -it "$container_name" sh -c "
      sed -i 's/^zend_extension=.*/zend_extension=ioncube_loader_lin_$php_version_number.so/' $cli_php_ini
      sed -i 's/^zend_extension=.*/zend_extension=ioncube_loader_lin_$php_version_number.so/' $fpm_php_ini
    "
  
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
