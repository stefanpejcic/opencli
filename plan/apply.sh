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
# DB
source /usr/local/admin/scripts/db.sh

# Check if the correct number of parameters is provided
if [ "$#" -lt 2 ]; then
    script_name=$(realpath --relative-to=/usr/local/admin/scripts/ "$0")
    script_name="${script_name//\//-}"  # Replace / with -
    script_name="${script_name%.sh}"     # Remove the .sh extension
    echo "Usage: opencli $script_name <plan_id> <username1> <username2>..."
    exit 1
fi

new_plan_id=$1
shift
usernames=()
#partial kao samo cpu ili samo ram...
partial=false
debug=false
bulk=false

docpu=false
doram=false
dodsk=false
donet=false
for arg in "$@"
do
    # Enable debug mode if --debug flag is provided 
    if [ "$arg" == "--debug" ]; then
        debug=true
        continue
    elif [ "$arg" == "--all" ]; then
        bulk=true
        continue
    elif [ "$arg" == "--cpu" ]; then
        partial=true
        docpu=true
        continue
    elif [ "$arg" == "--ram" ]; then
        partial=true
        doram=true
        continue
    elif [ "$arg" == "--dsk" ]; then
        partial=true    
        dodsk=true
        continue
    elif [ "$arg" == "--net" ]; then
        partial=true    
        donet=true
        continue
    fi
done

if [ "$bulk" = "true" ]; then
    usernames_raw=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT users.username FROM users WHERE users.plan_id = '$new_plan_id';" | tail -n +2)

    while IFS= read -r line; do
        usernames+=("$line")
    done <<< "$usernames_raw"

    if $debug; then
        echo "Applying plan changes to users:"
        echo "${usernames[@]}"
    fi
else
    for arg in "$@"
    do
        if [[ "${arg:0:2}" != "--" ]]; then
            usernames+=("$arg")  # Add the argument to the usernames array
        fi
    done
fi


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

## Function to create a Docker network with bandwidth limiting
create_docker_network() {

local name="$1"
local bandwidth="$2"
  for ((i = 18; i < 255; i++)); do
    subnet="172.$i.0.0/16"
    gateway="172.$i.0.1"

    # Check if the subnet is already in use
    used_subnets=$(docker network ls --format "{{.Name}}" | while read -r network_name; do
      docker network inspect --format "{{range .IPAM.Config}}{{.Subnet}}{{end}}" "$network_name"
    done)

    if [[ $used_subnets =~ $subnet ]]; then
      continue  # Skip if the subnet is already in use
    fi
    # Create the Docker network
    docker network create --driver bridge --subnet "$subnet" --gateway "$gateway" "$name"

    # Extract the network interface name for the gateway IP
    gateway_interface=$(ip route | grep "$gateway" | awk '{print $3}')

    # Limit the gateway bandwidth
    sudo tc qdisc add dev "$gateway_interface" root tbf rate "$bandwidth"mbit burst "$bandwidth"mbit latency 3ms

    found_subnet=1  # Set the flag to indicate success
    break
  done
  if [ $found_subnet -eq 0 ]; then
    echo "No available subnet found. Exiting."
    exit 1  # Exit with an error code
  fi
}


totalc="${#usernames[@]}"
counter=0


if [ "$debug" = true ]; then
    echo "DEBUG: Usernames: ${usernames[@]}"
fi



for container_name in "${usernames[@]}"
do
    # Debug echo
    ((counter++))
    echo "+=============================================================================+"
    echo "Processing user: $container_name (${counter}/${totalc})"
    echo ""
    # Fetch current plan ID for the container
    current_plan_id=$(get_current_plan_id "$container_name")
    current_plan_name=$(get_plan_name "$current_plan_id")
    new_plan_name=$(get_plan_name "$new_plan_id")

    # Check if the container exists in db
    if [ -z "$current_plan_id" ]; then
        echo "Error: Docker container for user '$container_name' exited."
        continue
    fi


    if docker inspect "$container_name" >/dev/null 2>&1; then
        if $debug; then
            echo "DEBUG: Container $container_name exists!"
        fi
    else
        echo "Error: Docker container for user '$container_name' is not running! (Is account suspended?)"
        continue
    fi

    # Fetch limits for the current plan
#    current_plan_limits=$(get_plan_limits "$current_plan_id")

    ##echo "Current plan limits:('$current_plan_limits')."

    # Check if the current plan limits were retrieved
#    if [ -z "$current_plan_limits" ]; then
#        echo "Warning: Unable to fetch old plan limits for plan with ID ('$current_plan_id')."
#    fi

    # Fetch limits for the new plan
#    new_plan_limits=$(get_plan_limits "$new_plan_id")
    ##echo "New plan limits:('$new_plan_limits')."

    # Check if the new plan limits were retrieved
#    if [ -z "$new_plan_limits" ]; then
#        echo "Error: Unable to fetch limits for the new plan with ID('$new_plan_id')."
#        continue
#    fi



    #Limiti stari i novi cpu, ram, docker_image, storage_file, inodes_limit, bandwidth
    ##echo "New plan ID:$new_plan_id"
    Ncpu=$(get_plan_limit "$new_plan_id" "cpu")
    Ocpu=$(get_plan_limit "$current_plan_id" "cpu")
    Nram=$(get_plan_limit "$new_plan_id" "ram")
    Oram=$(get_plan_limit "$current_plan_id" "ram")
    
    Ndocker_image=$(get_plan_limit "$new_plan_id" "docker_image")
    Odocker_image=$(docker inspect $container_name | grep '"Image":' | grep -v 'sha' | awk -F '"' '{print $4}')
    Ndisk_limit=$(get_plan_limit "$new_plan_id" "storage_file")
    Odisk_limit=$(get_plan_limit "$current_plan_id" "storage_file")
    Ninodes_limit=$(get_plan_limit "$new_plan_id" "inodes_limit")
    Oinodes_limit=$(get_plan_limit "$current_plan_id" "inodes_limit")
    #ne zanima me band
    Nbandwidth=$(get_plan_limit "$new_plan_id" "bandwidth")
    Obandwidth=$(get_plan_limit "$current_plan_id" "bandwidth")

    #Serverski limiti za provere
    free_space=$(df -BG / | awk 'NR==2 {print $4}' | sed 's/G//')
    maxCPU=$(nproc)
    maxRAM=$(free -g | awk '/^Mem/ {print $2}')
    numOram=$(echo "$Oram" | tr -d 'g')
    numNram=$(echo "$Nram" | tr -d 'g')
    numOdisk=$(echo "$Odisk_limit" | awk '{print $1}')
    numNdisk=$(echo "$Ndisk_limit" | awk '{print $1}')


        #if $debug; then
        #echo "          cpu, ram, docker_image, disk_limit, inodes_limit, bandwidth"
        #echo "Old plan: $Ocpu , $Oram, $Odocker_image, $Odisk_limit, $Oinodes_limit, $Obandwith"
        #echo "New plan: $Ncpu , $Nram, $Ndocker_image, $Ndisk_limit, $Ninodes_limit, $Nbandwith"
        #fi

        if $debug; then
            if [[ "$Ndocker_image" != "$Odocker_image" ]]; then
                echo "Warning: Can't change docker image, container image: $Odocker_image != plan image:$Ndocker_image"
            fi
        fi


    ########################################################################################################################################################################################
    #######CPU I RAM##############CPU I RAM##############CPU I RAM##############CPU I RAM##############CPU I RAM##############CPU I RAM##############CPU I RAM##############CPU I RAM#######
    ########################################################################################################################################################################################
    if [ "$partial" != "true" ] || [ "$doram" = "true" ]; then
        if (( $numNram > $maxRAM )); then
            echo "Error: New RAM value exceeds the server limit, not enough physical memory - $numNram > $maxRam."
        else
            docker update --memory="$Nram" --memory-swap="$Nram" "$container_name" > /dev/null
            echo "RAM limit set to ${numNram}GB."
            echo ""
        fi
    fi

    if [ "$partial" != "true" ] || [ "$docpu" = "true" ]; then
        if (( $Ncpu > $maxCPU )); then
            echo "Error: New CPU value exceeds the server limit, not enough CPU cores - $Ncpu > $maxCPU."
        else
            docker update --cpus="$Ncpu" "$container_name" > /dev/null
            echo "CPU limit set to $Ncpu cores."
            echo ""
        fi
    fi

    ####################################################################################################################################################################################
    ####### DISK ############## DISK ############## DISK ############## DISK ############## DISK ############## DISK ############## DISK ############## DISK ############## DISK #######
    ####################################################################################################################################################################################
    if [ "$partial" != "true" ] || [ "$dodsk" = "true" ]; then

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

        #if $debug; then 
        #    echo "New plan Disk limit - Old plan Disk limit = $addSize (set to 0 if new or old plan is unlimited)" 
        #fi

        #curInode=$(find /home/$container_name/. | wc -l)

        noDiskSpace=false
        if (( $numNdisk > $free_space )); then
            echo "Error: Insufficient space on disk for storage file, no changes to disk limit were made, Available: ${free_space}GB - Required: $Ndisk_limit."
            noDiskSpace=true
        fi

    #   if (( $curSize > $numNdisk && $numNdisk!=0 )); then
    #        echo "Error: Current size on disk exceeds the limits of the new plan - $curSize > $numNdisk."
    #   fi

    #   if (( $curInode > $Ninodes_limit )); then
    #        echo "Error: Current inode usage exceeds the limits of the new plan - $curInode > $Ninodes_limit."
    #   fi

        #if (( $addInodes < 0 && $numNdisk!=0 )); then
        #    echo "Error: Storage downgrades are not possible."
        #fi

        if $debug; then
            echo "Difference in disk_limit: $Odisk_limit to $Ndisk_limit"
            #echo "Difference in inodes_limit: $Oinodes_limit to $Ninodes_limit"
            echo "New limits must be larger than or equal to old limits."
        fi

                        ###DEO KOJI ZAPRAVO RADI NESTO###
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

                echo "Disk limit changed from ${numOdisk}GB to ${numNdisk}GB"


                if (( $addInodes > 0 )); then
                    #mkfs.ext4 -N $Ninodes_limit /home/storage_file_$container_name
                    echo "Warning: Increasing Inode limit is not possible, old plan limit remains"
                fi

            elif (($addSize < 0)); then
                echo "Warning: No change was made to the disk limit, new plan limit is more restrictive which is not allowed"
            else

                echo "No change was made to the disk limit, new plan limit is the same as old limit."

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
            echo "No change in disk, both new and original plan are unlimited."
        fi
    fi
    ##echo "Difference in bandwidth: $Obandwidth to $Nbandwidth"

    ################################################################################################################################################################################
    # NETOWRK AKO NE POSTOJI PRAVIM NOVI            NETOWRK AKO NE POSTOJI PRAVIM NOVI            NETOWRK AKO NE POSTOJI PRAVIM NOVI            NETOWRK AKO NE POSTOJI PRAVIM NOVI #            
    ################################################################################################################################################################################

    # Remove the current Docker network from the container
    #docker network disconnect "$current_plan_name" "$container_name"
    #novi catch all networks
    if [ "$partial" != "true" ] || [ "$donet" = "true" ]; then
            NETWORKS=$(docker inspect --format='{{range .NetworkSettings.Networks}}{{.NetworkID}} {{end}}' "$container_name")
            conngood=false
            # Loop through each network and disconnect the container
            for network in $NETWORKS; do
                if [ "$network" != "$new_plan_name" ]; then
                    #ovaj check ne radi jer NETWORKS nisu plain text pa ce ga uvek iskljuciti sa mreze iako je vec na pravoj
                    docker network disconnect "$network" "$container_name"
                    echo "container disconnected from network: ('$network')"
                else
                    conngood=true
                fi
            done

        if [ "$conngood" = "true" ]; then
            #ovo ne radi jer NETWORKS nisu plain text uvek ide u else
            echo "container already connected to network: ('$new_plan_name')"
        else
            # Check if DEBUG is true and the Docker network exists
            if docker network inspect "$new_plan_name" >/dev/null 2>&1; then
            
                if $debug; then
                    echo "DEBUG: Docker network '$new_plan_name' already exists, attempting to connect container..."
                fi
                    docker network connect "$new_plan_name" "$container_name"
                if $debug; then
                    echo "DEBUG: Container $container_name successfully connected to network '$new_plan_name'."
                    #skripta za rewrite nginx vhosts za tog usera!
                    opencli nginx-update_vhosts $container_name --nginx-reload
                else
                    opencli nginx-update_vhosts $container_name --nginx-reload > /dev/null
                fi
   
            else
                # Docker network does not exist, we need to create it..
                echo "Docker network '"$new_plan_name"' does not exist. Creating..."
                create_docker_network ""$new_plan_name"" "$Nbandwidth"
                echo "connecting container to network '"$new_plan_name"'..."
                docker network connect "$new_plan_name" "$container_name"
                opencli nginx-update_vhosts $container_name --nginx-reload
            fi
        fi



            # Compare limits and list the differences
            #diff_output=$(diff -u <(echo "$current_plan_limits") <(echo "$new_plan_limits"))
    echo ""
    fi
    #Menja ID
    #query="UPDATE users SET plan_id = $new_plan_id WHERE username = '$container_name';"
    #mysql --defaults-extra-file=$config_file -D "$mysql_database" -N -B -e "$query"
    #echo "Finished applying new values for container $container_name ($counter/$totalc)"
done
echo ""
echo "+=============================================================================+"
echo ""
echo "COMPLETED!"

if [ "$debug" = true ]; then
    echo "DEBUG: Deleting unused docker networks"
    docker network prune -f
else
    docker network prune -f >/dev/null 2>&1
fi

#cleanup
find /tmp -name 'opencli_plan_apply_*' -type f -mtime +1 -exec rm {} \; > /dev/null
