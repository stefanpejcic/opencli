#!/bin/bash

# Korisnici
DOCKER_USERS=$(docker ps -a --format '{{.Names}}')

# Start mount skriptu
bash /usr/local/admin/scripts/webserver/mount_folders_for_all_users.sh

# Stop servisa
systemctl stop docker
systemctl stop docker.socket
systemctl stop containerd
systemctl stop panel

#Start Docker
systemctl start docker

# Loop kroz Docker usere i pokreni skript
for USERNAME in $DOCKER_USERS; do
    # Run the user-specific script
    bash /usr/local/admin/scripts/webserver/change_ip_in_vhosts_files.sh $USERNAME

done

#reset servisa
service nginx reload
service panel restart

# Fix ports
bash /usr/local/admin/scripts/webserver/fix_ufw_ports.sh
