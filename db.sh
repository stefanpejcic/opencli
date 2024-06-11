#!/bin/bash

#########################################################################
############################### DB LOGIN ################################ 
#########################################################################
# MySQL database configuration
config_files=("/etc/my.cnf" "/etc/openpanel/mysql/db.cnf" "/usr/local/admin/db.cnf") # for compatibility with openpanel <0.2.0

# Function to check if a config file exists
check_config_file() {
    for config_file in "${config_files[@]}"; do
        if [ -f "$config_file" ]; then
            #echo "Using config file $config_file."
            return 0
        fi
    done
    return 1
}

# Check for the config file in the specified order
if ! check_config_file; then
    echo "No mysql config files found in the specified locations."
    exit 1
fi

# Define the MySQL database name
mysql_database="panel"

#########################################################################
