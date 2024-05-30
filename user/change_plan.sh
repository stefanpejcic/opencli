#!/bin/bash
################################################################################
# Script Name: user/change_plan.sh
# Description: Change plan for a user and apply new plan limits.
# Usage: opencli user-change_plan <USERNAME> <NEW_PLAN_NAME>
# Author: Petar Ćurić
# Created: 17.11.2023
# Last Modified: 30.05.2024
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
if [ "$#" -ne 2 ] && [ "$#" -ne 3 ]; then
    echo "Usage: opencli user-change-plan <username> <new_plan_name>"
    exit 1
fi

container_name=$1
new_plan_name=$2

debug=false
for arg in "$@"
do
    # Enable debug mode if --debug flag is provided 
    if [ "$arg" == "--debug" ]; then
        debug=true
        break
    fi
done

# DB
source /usr/local/admin/scripts/db.sh

# Function to fetch the current plan ID for the container
get_current_plan_id() {
    local container="$1"
    local query="SELECT plan_id FROM users WHERE username = '$container'"
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$query"
}

# Function to fetch plan limits for a given plan ID smece format
get_plan_limits() {
    local plan_id="$1"
    local query="SELECT cpu, ram, docker_image, storage_file, inodes_limit, bandwidth FROM plans WHERE id = '$plan_id'"
    mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$query"
}


get_new_plan_id() {
    local plan_name="$1"
    local query="SELECT id FROM plans WHERE name = '$plan_name'"
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
new_plan_id=$(get_new_plan_id "$new_plan_name")

# Check if the container exists
if [ -z "$current_plan_id" ]; then
    echo "Error: Container '$container_name' not found in the database."
    exit 1
fi

# Fetch limits for the current plan
current_plan_limits=$(get_plan_limits "$current_plan_id")

##echo "Current plan limits:('$current_plan_limits')."

# Check if the current plan limits were retrieved
if [ -z "$current_plan_limits" ]; then
    echo "Error: Unable to fetch limits for the current plan ('$current_plan_id')."
    exit 1
fi

# Fetch limits for the new plan
new_plan_limits=$(get_plan_limits "$new_plan_id")
##echo "New plan limits:('$new_plan_limits')."

# Check if the new plan limits were retrieved
if [ -z "$new_plan_limits" ]; then
    echo "Error: Unable to fetch limits for the new plan ('$new_plan_id')."
    exit 1
fi



#Limiti stari i novi cpu, ram, docker_image, storage_file, inodes_limit, bandwidth
##echo "New plan ID:$new_plan_id"
Ncpu=$(get_plan_limit "$new_plan_id" "cpu")
Ocpu=$(get_plan_limit "$current_plan_id" "cpu")
Nram=$(get_plan_limit "$new_plan_id" "ram")
Oram=$(get_plan_limit "$current_plan_id" "ram")
Ndocker_image=$(get_plan_limit "$new_plan_id" "docker_image")
Odocker_image=$(get_plan_limit "$current_plan_id" "docker_image")
Ndisk_limit=$(get_plan_limit "$new_plan_id" "storage_file")
Odisk_limit=$(get_plan_limit "$current_plan_id" "storage_file")
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


if (($numNdisk>0)); then
    nMounted=true
else
    nMounted=false
fi

oMounted=true
if df -BG | grep -q "/home/$container_name"; then
    # Directory is mounted
    curSize=$(df -BG | grep "/home/$container_name" | awk 'NR==1 {print $3}' | sed 's/G//')
        if $debug; then
            echo "storage file IS currently mounted, current size: ${curSize}G"
        fi
else
    # Directory is not mounted
    curSize=$(du -sBG "/home/$container_name" | awk '{print $1}' | sed 's/G//')
        if $debug; then
            echo "storage file IS NOT currently mounted, current size: ${curSize}G" 
        fi
    oMounted=false
fi

addSize=0
addInodes=0
if [ "$oMounted" = "true" ] && [ "$nMounted" = "true" ]; then    
    if $debug; then 
        echo "BOTH MOUNTED"
    fi
    addSize=$((numNdisk - numOdisk))
    addInodes=$((Ninodes_limit - Oinodes_limit))
fi

if $debug; then 
    echo "New plan Disk limit - Old plan Disk limit = $addSize (set to 0 if new or old plan is unlimited)" 
fi

curInode=$(find /home/$container_name/. | wc -l)

if (( $numNram > $maxRAM )); then
    echo "Error: New RAM value exceeds the server limit, not enough physical memory - $numNram > $maxRam."
    exit 1
fi

if (( $Ncpu > $maxCPU )); then
    echo "Error: New CPU value exceeds the server limit, not enough CPU cores - $Ncpu > $maxCPU."
    exit 1
fi

noDiskSpace=false
if (( $numNdisk > $free_space )); then
    echo "Error: Insufficient space on disk, no changes to disk limit were made, Available: ${free_space}GB - Required: $Ndisk_limit."
    noDiskSpace=true
fi

if [[ "$Ndocker_image" != "$Odocker_image" ]]; then
    echo "Error: Can't change docker image."
    exit 1
fi

if (( $curSize > $numNdisk && $numNdisk!=0 )); then
    echo "Error: Current size on disk exceeds the limits of the new plan - $curSize > $numNdisk."
    exit 1
fi

if (( $curInode > $Ninodes_limit )); then
    echo "Error: Current inode usage exceeds the limits of the new plan - $curInode > $Ninodes_limit."
    exit 1
fi

#if (( $addInodes < 0 && $numNdisk!=0 )); then
#    echo "Error: Storage downgrades are not possible."
#    exit 1
#fi

if $debug; then
    echo "          cpu, ram, docker_image, disk_limit, inodes_limit, bandwidth"
    echo "Old plan: $Ocpu , $Oram, $Odocker_image, $Odisk_limit, $Oinodes_limit, $Obandwith"
    echo "New plan: $Ncpu , $Nram, $Ndocker_image, $Ndisk_limit, $Ninodes_limit, $Nbandwith"

    echo "Difference in cpu: $Ocpu to $Ncpu"
fi

docker update --cpus="$Ncpu" "$container_name" > /dev/null

if $debug; then
    echo "Difference in ram: $Oram to $Nram"
fi

docker update --memory="$Nram" --memory-swap="$Nram" "$container_name" > /dev/null

if $debug; then
    echo "Current docker image $Odocker_image"
    echo "New docker image $Ndocker_image (must be the same for the plan change to work!)"
    #ako je drugi image nema izmene plana

    echo "Difference in disk_limit: $Odisk_limit to $Ndisk_limit"
    echo "Difference in inodes_limit: $Oinodes_limit to $Ninodes_limit"
    echo "New limits must be larger than or equal to old limits."
fi

if [ "$oMounted" = "true" ] && [ "$nMounted" = "true" ]; then


    if (( $addSize > 0 || $addInodes > 0 )) && [ "$noDiskSpace" = false ]; then
        docker stop $container_name
        if mount | grep "/home/$container_name" > /dev/null; then
            umount /home/$container_name
        fi

        #echo "falokejt parametar ${numNdisk}g"
        fallocate -l ${numNdisk}g /home/storage_file_$container_name

        #fix+resize FSystem
        e2fsck -f -y /home/storage_file_$container_name  > /dev/null
        resize2fs /home/storage_file_$container_name
        mount -o loop /home/storage_file_$container_name /home/$container_name
        docker start $container_name
        if $debug; then
            echo "Disk limit changed from ${numOdisk}G to ${numNdisk}G"
        fi

        if (( $addInodes > 0 )); then
            #mkfs.ext4 -N $Ninodes_limit /home/storage_file_$container_name
            echo "Warning: Increasing Inode limit is not possible, old plan limit remains"
        fi

    elif (($addSize < 0)); then
        echo "Warning: No change was made to the disk limit, new plan limit is more restrictive which is not allowed"
    else
        if $debug; then
            echo "No change was made to the disk limit, new plan limit is the same as old limit."
        fi
    fi

elif [ "$oMounted" = "true" ] && [ "$nMounted" != "true" ]; then

    echo "Warning: disk size limit can't be set to unlimited after filesystem creation, original plan limit remains."
        #umount /home/$container_name
        #rm /home/storage_file_$container_name

elif [ "$oMounted" != "true" ] && [ "$nMounted" = "true" ]; then
    echo "Warning: disk usage cannot be limited after user was created on an unlimited plan, disk usage remains unlimited."
    #if [ "$free_space" -le "$numNdisk" ]; then
    #    echo "Error: Not enough free space on disk for storage file creation, no limit enforced on container, switch to a smaller plan or free up disk space."
    #else
        #docker stop $container_name
        #fallocate -l ${numNdisk}g /home/storage_file_$container_name 
        #mkfs.ext4 -N $Ninodes_limit /home/storage_file_$container_name 
        #mount -o loop /home/storage_file_$container_name /home/$container_name
        #docker start $container_name
        #if $debug; then
        #    echo "Container disk usage now limited to ${numNdisk}G"
        #fi
    #fi
else
    if $debug; then
        echo "No change in disk, both new and original plan are unlimited."
    fi
fi
##echo "Difference in bandwidth: $Obandwidth to $Nbandwidth"


# Remove the current Docker network from the container
#docker network disconnect "$current_plan_name" "$container_name"

#novi catch all networks
NETWORKS=$(docker inspect --format='{{range .NetworkSettings.Networks}}{{.NetworkID}} {{end}}' "$container_name")

# Loop through each network and disconnect the container
for network in $NETWORKS; do
  docker network disconnect "$network" "$container_name"
done

if $debug; then
    echo "old plan name: ('$current_plan_name')"
fi
# Connect the container to the new Docker network
docker network connect "$new_plan_name" "$container_name"
if $debug; then
    echo "new plan name:('$new_plan_name')"
fi
#Menja ID
query="UPDATE users SET plan_id = $new_plan_id WHERE username = '$container_name';"
mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$query"


#skripta za rewrite nginx vhosts za tog usera!
if $debug; then
    opencli nginx-update_vhosts $container_name --nginx-reload
else
    opencli nginx-update_vhosts $container_name --nginx-reload > /dev/null
fi
# Compare limits and list the differences
#diff_output=$(diff -u <(echo "$current_plan_limits") <(echo "$new_plan_limits"))
