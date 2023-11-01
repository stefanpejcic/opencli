#!/bin/bash

# Stop Docker
systemctl stop docker
systemctl stop docker.socket
systemctl stop containerd


# Korisnici
DOCKER_USERS=$(docker ps -a --format '{{.Names}}')

# Start mount skriptu 
bash /usr/local/admin/scripts/webserver/mount_folders_for_all_users.sh

# Loop kroz Docker usere i pokreni skript
for USERNAME in $DOCKER_USERS; do
    # Run the user-specific script
    bash /usr/local/admin/scripts/domains/change_ip_in_vhosts_files.sh $USERNAME

done

# Start Docker
systemctl start docker

# Fix ports
bash /usr/local/admin/scripts/webserver/fix_ufw_ports.sh
