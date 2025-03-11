#!/bin/bash

docker --context default compose up -d openpanel_mysql

sleep 15s

cd /root

# Load environment variables from .env file
export $(grep -v '^#' .env | xargs)

# Check if the MySQL root password is correct
if mysql -u root -p"$MYSQL_ROOT_PASSWORD" -e "exit" 2>/dev/null; then
    echo "MySQL root password is correct. Proceeding with configuration."
    
    # Create symbolic link if not exists
    ln -sf /etc/openpanel/mysql/host_my.cnf /etc/my.cnf
    
    # Update MySQL configuration files with the root password
    sed -i 's/password = .*/password = '"${MYSQL_ROOT_PASSWORD}"'/g' /etc/openpanel/mysql/host_my.cnf
    sed -i 's/password = .*/password = '"${MYSQL_ROOT_PASSWORD}"'/g' /etc/openpanelmysql/container_my.cnf
    
    echo "Configuration updated successfully!"
else
    echo "ERROR: MySQL root password $MYSQL_ROOT_PASSWORD is no longer working."
    exit 1
fi
