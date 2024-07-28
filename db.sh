#!/bin/bash

#########################################################################
############################### DB LOGIN ################################ 
#########################################################################

config_files=("/etc/my.cnf" "/etc/openpanel/mysql/db.cnf" "/usr/local/admin/db.cnf") # for compatibility with openpanel <0.2.0

check_config_file() {
    for config_file in "${config_files[@]}"; do
        if [ -f "$config_file" ]; then
            return 0
        fi
    done
    return 1
}

if ! check_config_file; then
    echo "No mysql config files found in the specified locations."
    exit 1
fi

mysql_database="panel"

#########################################################################
