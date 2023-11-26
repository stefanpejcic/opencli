#!/bin/bash
################################################################################
# Script Name: user/change_plan.sh
# Description: Change plan for a user and apply new plan limits.
# Usage: opencli user-change_plan <USERNAME> <NEW_PLAN_ID>
# Author: Petar Ćurić
# Created: 17.11.2023
# Last Modified: 17.11.2023
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

# Check if the correct number of parameters is provided
if [ "$#" -ne 2 ]; then
    script_name=$(realpath --relative-to=/usr/local/admin/scripts/ "$0")
    script_name="${script_name//\//-}"  # Replace / with -
    script_name="${script_name%.sh}"     # Remove the .sh extension
    echo "Usage: opencli $script_name <username> <new_plan_id>"
    exit 1
fi

container_name=$1
new_plan_id=$2

# MySQL database configuration
config_file="/usr/local/admin/db.cnf"

# Check if the config file exists
if [ ! -f "$config_file" ]; then
    echo "Config file $config_file not found."
    exit 1
fi

mysql_database="panel"

# Function to fetch the current plan ID for the container
get_current_plan_id() {
    local container="$1"
    local query="SELECT plan_id FROM users WHERE username = '$container'"
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$query"
}

# Function to fetch plan limits for a given plan ID
get_plan_limits() {
    local plan_id="$1"
    local query="SELECT cpu, ram, docker_image, disk_limit, inodes_limit, bandwidth FROM plans WHERE id = '$plan_id'"
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$query"
}

# Function to fetch single plan limit for a given plan ID and resource type
get_plan_limit() {
    local plan_id="$1"
    local resource="$2"
    local query="SELECT $resource FROM plans WHERE id = '$plan_id'"
    #echo "$query"
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$query"
}


# Function to fetch the name of a plan for a given plan ID
get_plan_name() {
    local plan_id="$1"
    local query="SELECT name FROM plans WHERE id = '$plan_id'"
    mysql --defaults-extra-file="$config_file" -D "$mysql_database" -N -B -e "$query"
}

# Fetch current plan ID for the container
current_plan_id=$(get_current_plan_id "$container_name")

current_plan_name=$(get_plan_name "$current_plan_id")
new_plan_name=$(get_plan_name "$new_plan_id")

# Check if the container exists
if [ -z "$current_plan_id" ]; then
    echo "Error: Container '$container_name' not found in the database."
    exit 1
fi

# Fetch limits for the current plan
current_plan_limits=$(get_plan_limits "$current_plan_id")

echo "Current plan limits:('$current_plan_limits')."

# Check if the current plan limits were retrieved
if [ -z "$current_plan_limits" ]; then
    echo "Error: Unable to fetch limits for the current plan ('$current_plan_id')."
    exit 1
fi

# Fetch limits for the new plan
new_plan_limits=$(get_plan_limits "$new_plan_id")
echo "New plan limits:('$new_plan_limits')."

# Check if the new plan limits were retrieved
if [ -z "$new_plan_limits" ]; then
    echo "Error: Unable to fetch limits for the new plan ('$new_plan_id')."
    exit 1
fi



#Limiti stari i novi cpu, ram, docker_image, disk_limit, inodes_limit, bandwidth
echo "New plan ID:$new_plan_id"
Ncpu=$(get_plan_limit "$new_plan_id" "cpu")
Ocpu=$(get_plan_limit "$current_plan_id" "cpu")
Nram=$(get_plan_limit "$new_plan_id" "ram")
Oram=$(get_plan_limit "$current_plan_id" "ram")
Ndocker_image=$(get_plan_limit "$new_plan_id" "docker_image")
Odocker_image=$(get_plan_limit "$current_plan_id" "docker_image")
Ndisk_limit=$(get_plan_limit "$new_plan_id" "disk_limit")
Odisk_limit=$(get_plan_limit "$current_plan_id" "disk_limit")
Ninodes_limit=$(get_plan_limit "$new_plan_id" "inodes_limit")
Oinodes_limit=$(get_plan_limit "$current_plan_id" "inodes_limit")
Nbandwidth=$(get_plan_limit "$new_plan_id" "bandwidth")
Obandwidth=$(get_plan_limit "$current_plan_id" "bandwidth")

#Serverski limiti za provere
free_space=$(df -BG / | awk 'NR==2 {print $4}' | sed 's/G//')
maxCPU=$(nproc)
maxRAM=$(free -g | awk '/^Mem/ {print $2}')
numNram=$(echo "$Nram" | tr -d 'g')
numOdisk=$(echo "$Odisk_limit" | awk '{print $1}')
numNdisk=$(echo "$Ndisk_limit" | awk '{print $1}')
addSize=$((numNdisk - numOdisk))
echo "addsize $addSize"
curSize=$(df -BG | grep /home/$container_name | awk 'NR==1 {print $3}' | sed 's/G//')
curInode=$(find /home/$container_name/. | wc -l)

if (( $numNram > $maxRAM )); then
    echo "Error: New RAM value exceeds the server limit, not enough physical memory - $numNram > $maxRam."
    exit 1
fi

if (( $Ncpu > $maxCPU )); then
    echo "Error: New CPU value exceeds the server limit, not enough CPU cores - $Ncpu > $maxCPU."
    exit 1
fi

if (( $addSize > $free_space )); then
    echo "Error: Insufficient disk space mounted, Available: $free_space - Required: $addSize."
    exit 1
fi

if [[ "$Ndocker_image" != "$Odocker_image" ]]; then
    echo "Error: Can't change docker image."
    exit 1
fi

if (( $curSize > $numNdisk )); then
    echo "Error: Current size on disk exceeds the limits of the new plan - $curSize > $numNdisk."
    exit 1
fi

if (( $curInode > $Ninodes_limit )); then
    echo "Error: Current inode usage exceeds the limits of the new plan - $curInode > $Ninodes_limit."
    exit 1
fi


echo "          cpu, ram, docker_image, disk_limit, inodes_limit, bandwidth"
echo "Old plan: $Ocpu , $Oram, $Odocker_image, $Odisk_limit, $Oinodes_limit, $Obandwith"
echo "New plan: $Ncpu , $Nram, $Ndocker_image, $Ndisk_limit, $Ninodes_limit, $Nbandwith"

echo "Difference in cpu: $Ocpu to $Ncpu"
docker update --cpus="$Ncpu" "$container_name"

echo "Difference in ram: $Oram to $Nram"
docker update --memory="$Nram" --memory-swap="$Nram" "$container_name"

echo "Difference in docker_image: $Odocker_image to $Ndocker_image"
#ako je drugi image nema izmene plana

echo "Difference in disk_limit: $Odisk_limit to $Ndisk_limit"
echo "Difference in inodes_limit: $Oinodes_limit to $Ninodes_limit"

if (( $addSize > 0 )); then
    if mount | grep "/home/$container_name" > /dev/null; then
        umount /home/$container_name
    fi

echo "falokejt parametar ${numNdisk}g"
fallocate -l ${numNdisk}g /home/storage_file_$container_name
mkfs.ext4 -F -N $Ninodes_limit /home/storage_file_$container_name
#fix+resize FSystem
e2fsck -f -y /home/storage_file_$container_name
resize2fs /home/storage_file_$container_name

mount -o loop /home/storage_file_$container_name /home/$container_name
elif (( $addSize < 0 )); then
    if mount | grep "/home/$container_name" > /dev/null; then
        umount /home/$container_name
    fi

truncate -s ${numNdisk}g /home/storage_file_$container_name
mkfs.ext4 -F -N $Ninodes_limit /home/storage_file_$container_name
#fix+resize FSystem
e2fsck -f -y /home/storage_file_$container_name
resize2fs /home/storage_file_$container_name

mount -o loop /home/storage_file_$container_name /home/$container_name
else
echo "No change in disk size."
fi


echo "Difference in bandwidth: $Obandwidth to $Nbandwidth"


# Remove the current Docker network from the container
#docker network disconnect "$current_plan_name" "$container_name"

#novi catch all networks
NETWORKS=$(docker inspect --format='{{range .NetworkSettings.Networks}}{{.NetworkID}} {{end}}' "$container_name")

# Loop through each network and disconnect the container
for network in $NETWORKS; do
  docker network disconnect "$network" "$container_name"
done
echo "current plan name: ('$current_plan_name')"

# Connect the container to the new Docker network
docker network connect "$new_plan_name" "$container_name"
echo "new plan name:('$new_plan_name')"

#Menja ID
query="UPDATE users SET plan_id = $new_plan_id WHERE username = '$container_name';"
mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$query"


#skripta za rewrite nginx vhosts za tog usera!
opencli nginx-update_vhosts $container_name -nginx-reload

# Compare limits and list the differences
#diff_output=$(diff -u <(echo "$current_plan_limits") <(echo "$new_plan_limits"))

