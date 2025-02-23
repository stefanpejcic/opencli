#!/bin/bash
################################################################################
# Script Name: plan/apply.sh
# Description: Change plan for a user and apply new plan limits.
# Usage: opencli plan-apply <USERNAME> <NEW_PLAN_ID>
# Author: Petar Ćurić
# Created: 17.11.2023
# Last Modified: 23.02.2025
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




# todo: remove storage



# DB
source /usr/local/opencli/db.sh

# Check if the correct number of parameters is provided
if [ "$#" -lt 2 ]; then
    echo "Usage: opencli plan-apply <plan_id> <username1> <username2>..."
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
    local query="SELECT plan_id, server FROM users WHERE username = '$container'"
    local result
    result=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -N -B -e "$query")

    # Extract plan_id and server from the result
    current_plan_id=$(echo "$result" | awk '{print $1}')
    server=$(echo "$result" | awk '{print $2}')
    if [[ -z "$server" || "$server" == "default" ]]; then
        server="$container"
    fi
}

# Function to fetch plan limits for a given plan ID smece format
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

## Function to create a Docker network with bandwidth limiting



totalc="${#usernames[@]}"
counter=0



for container_name in "${usernames[@]}"
do
    # Debug echo
    ((counter++))
    echo "+=============================================================================+"
    echo "Processing user: $container_name (${counter}/${totalc})"
    echo ""
    # Fetch current plan ID for the container
    get_current_plan_id "$container_name"
    current_plan_name=$(get_plan_name "$current_plan_id")
    new_plan_name=$(get_plan_name "$new_plan_id")

    # Check if the container exists in db
    if [ -z "$current_plan_id" ]; then
        echo "Error: Docker container for user '$container_name' exited."
        continue
    fi


    if docker --context $server inspect "$container_name" >/dev/null 2>&1; then
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
    Odocker_image=$(docker --context $server inspect $container_name | grep '"Image":' | grep -v 'sha' | awk -F '"' '{print $4}')
    Ndisk_limit=$(get_plan_limit "$new_plan_id" "disk_limit")
    Ninodes_limit=$(get_plan_limit "$new_plan_id" "inodes_limit")
    #ne zanima me band

    #Serverski limiti za provere
    maxCPU=$(nproc)
    maxRAM=$(free -g | awk '/^Mem/ {print $2}')
    numOram=$(echo "$Oram" | tr -d 'g')
    numNram=$(echo "$Nram" | tr -d 'g')
    numNdisk=$(echo "$Ndisk_limit" | awk '{print $1}')
    storage_in_blocks=$((numNdisk * 1024000))

    reload_user_quotas() {
    	quotacheck -avm > /dev/null
    	repquota -u / > /etc/openpanel/openpanel/core/users/repquota 
    }


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
            docker --context $server update --memory="$Nram" --memory-swap="$Nram" "$container_name" > /dev/null
            echo "RAM limit set to ${numNram}GB."
            echo ""
        fi
    fi

    if [ "$partial" != "true" ] || [ "$docpu" = "true" ]; then
        if (( $Ncpu > $maxCPU )); then
            echo "Error: New CPU value exceeds the server limit, not enough CPU cores - $Ncpu > $maxCPU."
        else
            docker --context $server update --cpus="$Ncpu" "$container_name" > /dev/null
            echo "CPU limit set to $Ncpu cores."
            echo ""
        fi
    fi

    ####################################################################################################################################################################################
    ####### DISK ############## DISK ############## DISK ############## DISK ############## DISK ############## DISK ############## DISK ############## DISK ############## DISK #######
    ####################################################################################################################################################################################
    if [ "$partial" != "true" ] || [ "$dodsk" = "true" ]; then
        setquota -u $username $storage_in_blocks $storage_in_blocks $Ninodes_limit $Ninodes_limit /
        reload_user_quotas
        

        
        
    fi

    ################################################################################################################################################################################
    # NETOWRK AKO NE POSTOJI PRAVIM NOVI            NETOWRK AKO NE POSTOJI PRAVIM NOVI            NETOWRK AKO NE POSTOJI PRAVIM NOVI            NETOWRK AKO NE POSTOJI PRAVIM NOVI #            
    ################################################################################################################################################################################

    if [ "$partial" != "true" ] || [ "$donet" = "true" ]; then
        #sudo tc qdisc add dev "$gateway_interface" root tbf rate "$bandwidth"mbit burst "$bandwidth"mbit latency 3ms
        :


    fi
done


#cleanup
find /tmp -name 'opencli_plan_apply_*' -type f -mtime +1 -exec rm {} \; > /dev/null
