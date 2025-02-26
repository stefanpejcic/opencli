#!/bin/bash
################################################################################
# Script Name: resources.sh
# Description: View services limits for user.
# Usage: opencli user-resources <CONTEXT> [--test=<SERVICE_NAME>] [--json]
# Author: Stefan Pejcic
# Created: 26.02.2025
# Last Modified: 26.02.2025
# Company: openpanel.com
# Copyright (c) openpanel.com
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


# Load .env variables, excluding the readonly UID variable
if [ -f .env ]; then
    export $(grep -v '^#' .env | grep -v '^UID=' | xargs)
else
    echo "Error: .env file not found!"
    exit 1
fi

# Ensure TOTAL_CPU and TOTAL_RAM are set
if [ -z "$TOTAL_CPU" ] || [ -z "$TOTAL_RAM" ]; then
    echo "Error: TOTAL_CPU or TOTAL_RAM not set in .env!"
    exit 1
fi


TOTAL_RAM=$(echo "$TOTAL_RAM" | sed 's/[gG]//g')



TOTAL_RAM=$(echo "$TOTAL_RAM" | awk '{print int($1)}')
TOTAL_CPU=$(echo "$TOTAL_CPU" | awk '{print int($1)}')


if ! [[ "$TOTAL_CPU" =~ ^[0-9]+$ ]]; then
    echo "Error: TOTAL_CPU is not an integer."
    exit 1
fi


if ! [[ "$TOTAL_RAM" =~ ^[0-9]+$ ]]; then
    echo "Error: TOTAL_RAM is not an integer."
    exit 1
fi



# Process the OS service (first argument)
context=$1
if [ -z "$context" ]; then
    echo "Error: docker context name must be provided as the first argument!"
    exit 1
fi

TOTAL_USED_CPU=0
TOTAL_USED_RAM=0

os_service_name=$(echo "$context" | sed 's/[.-]/_/g')

os_cpu_value="$OS_CPU"
os_ram_value="$OS_RAM"

RUNNING_SERVICES=$(docker --context $context ps --format "{{.Names}}")
if [ $? -ne 0 ]; then
    echo "Failed to retrieve the list of running services. Please ensure Docker is installed and the context '$context' is valid."
    exit 1
fi

if [ -z "$RUNNING_SERVICES" ]; then
    echo "No services are currently running in context '$context'."
fi

# Flag for JSON output
json_output=false
new_service=""

# Parse flags and arguments
for arg in "$@"; do
    if [[ "$arg" == "--json" ]]; then
        json_output=true
    elif [[ "$arg" == --test=* ]]; then
        new_service="${arg#--test=}"
    fi
done

# Prepare the base structure for JSON output
json_data="{\"context\": \"$context\", \"services\": [], \"limits\": {\"cpu\": {\"used\": $TOTAL_USED_CPU, \"total\": $TOTAL_CPU}, \"ram\": {\"used\": $TOTAL_USED_RAM, \"total\": $TOTAL_RAM}}}"

message=""

if [ -n "$RUNNING_SERVICES" ]; then
    services_data=""

    if $json_output; then
        :
    else
        echo "Services:"
    fi

    for service in $RUNNING_SERVICES; do
        # Replace any dots or hyphens with underscores in the service name to match .env variables
        service_name=$(echo "$service" | sed 's/[.-]/_/g')

        if [[ "$service_name" == "$context" ]]; then
            service_name="OS"
        fi

        cpu_var="${service_name^^}_CPU"  # Convert service name to uppercase for matching .env variable
        ram_var="${service_name^^}_RAM"

        cpu_value=${!cpu_var:-0}
        ram_value=${!ram_var:-0}

        if [ -z "${!cpu_var}" ] || [ -z "${!ram_var}" ]; then
            # If either the CPU or RAM value is missing in the .env file, show a message
            message="Warning: Service $service_name does not have CPU or RAM limits defined in .env file!"
        fi


        # Strip "G" from RAM values
        ram_value=${ram_value//G/}

        TOTAL_USED_CPU=$(echo "$TOTAL_USED_CPU + $cpu_value" | bc)
        TOTAL_USED_RAM=$(echo "$TOTAL_USED_RAM + $ram_value" | bc)

        if [[ "$service_name" == "OS" ]]; then
            service_name=$context
        fi

        # Convert service name to display format (underscores to hyphens and numbers with dots)
        display_service_name=$(echo "$service_name" | sed 's/_/-/g' | sed -E 's/([0-9]+)-([0-9]+)/\1.\2/g')

        service_data="{\"name\": \"$display_service_name\", \"cpu\": $cpu_value, \"ram\": $ram_value}"
        
        if $json_output; then
            services_data="$services_data$service_data,"
        else
            echo "- $display_service_name - CPU: $cpu_value cores, RAM: $ram_value G"
        fi
    done

    # Remove last comma for valid JSON
    services_data=$(echo "$services_data" | sed 's/,$//')

    # Add services to the JSON structure
    if $json_output; then
        json_data="{\"context\": \"$context\", \"services\": [$services_data], \"limits\": {\"cpu\": {\"used\": $TOTAL_USED_CPU, \"total\": $TOTAL_CPU}, \"ram\": {\"used\": $TOTAL_USED_RAM, \"total\": $TOTAL_RAM}} , \"message\": \"$message\"}"
    else
        echo ""
        echo "Total usage:"
        echo "- CPU: $TOTAL_USED_CPU / $TOTAL_CPU"
        echo "- RAM: $TOTAL_USED_RAM / $TOTAL_RAM"
        echo ""
    fi

else
    message="No currently running services."
    echo "$message"
fi

# Handle new service addition if --test=<service_name> is provided
if [[ -n "$new_service" ]]; then
    # Replace dots and hyphens with underscores in the new service name
    new_service_name=$(echo "$new_service" | sed 's/[.-]/_/g')  

    new_cpu_var="${new_service_name^^}_CPU"
    new_ram_var="${new_service_name^^}_RAM"

    new_cpu_value=${!new_cpu_var:-0}
    new_ram_value=${!new_ram_var:-0}
    new_ram_value=${new_ram_value//G/}

    # Check if the CPU value is a valid float or integer
    if ! [[ "$new_cpu_value" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
        message="Error: Service $service_name does not have a valid CPU limit defined!"
    # Check if the CPU value is 0.0 or less (for floats)
    elif (( $(echo "$new_cpu_value > 0" | bc -l) == 0 )) || [ -z "$new_ram_value" ]; then
        message="Error: Service $service_name does not have CPU or RAM limits defined!"
    fi

        projected_cpu=$(echo "$TOTAL_USED_CPU + $new_cpu_value" | bc)
        if (( $(echo "$projected_cpu > $TOTAL_CPU" | bc -l) )); then
            if [ "$TOTAL_CPU" -eq 0 ]; then
                message="Warning: User has unlimited CPU limits: $projected_cpu / $TOTAL_CPU cpus"
            else
                message="Error: Adding $new_service will exceed CPU limits: $projected_cpu / $TOTAL_CPU cpus"
            fi
        fi


        projected_ram=$(echo "$TOTAL_USED_RAM + $new_ram_value" | bc)
        if (( $(echo "$projected_ram > $TOTAL_RAM" | bc -l) )); then
            if [ "$TOTAL_RAM" -eq 0 ]; then
                message="${message} \n Warning: User has unlimited RAM limits: $projected_ram G / $TOTAL_RAM G"
            else
                message="${message} \n Error: Adding $new_service will exceed RAM limits: $projected_ram G / $TOTAL_RAM G"
            fi

        fi




            if $json_output; then
                json_data="{\"context\": \"$context\", \"services\": [$services_data], \"limits\": {\"cpu\": {\"used\": $TOTAL_USED_CPU, \"total\": $TOTAL_CPU, \"after\": $projected_cpu}, \"ram\": {\"used\": $TOTAL_USED_RAM, \"total\": $TOTAL_RAM, \"after\": $projected_ram}} , \"message\": \"$message\"}"
                echo "$json_data" | jq .
            else
                echo "$message"
            fi




        if [ "$TOTAL_RAM" -eq 0 ] || [ "$TOTAL_CPU" -eq 0 ]; then
                exit 1

        fi

        if (( $(echo "$projected_cpu > $TOTAL_CPU" | bc -l) )) || (( $(echo "$projected_ram > $TOTAL_RAM" | bc -l) )); then
                exit 1
        fi



fi

if $json_output; then
    echo "$json_data" | jq .
fi
exit 0
