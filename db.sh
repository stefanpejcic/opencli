#!/bin/bash

#########################################################################
############################### DB LOGIN ################################ 
#########################################################################

: '
both are available on /etc/my.cnf
- for container: /usr/local/admin/container_my.cnf is mounted to /etc/my.cnf
- for host server: /usr/local/admin/host_my.cnf is symlinked to /etc/my.cnf
'

config_files=("/etc/my.cnf")

check_config_file() {
    for config_file in "${config_files[@]}"; do
        if [ -f "$config_file" ]; then
            return 0
        fi
    done
    return 1
}

if ! check_config_file; then
    echo "Mysql config file: $config_files is not available!"
    exit 1
fi

mysql_database="panel"

#########################################################################
