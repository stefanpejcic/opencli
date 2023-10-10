#!/bin/bash

config_file="/usr/local/panel/conf/panel.config"

# Function to get the current configuration value for a parameter
get_config() {
    param_name="$1"
    param_value=$(grep "^$param_name=" "$config_file" | cut -d= -f2-)
    
    if [ -n "$param_value" ]; then
        echo "$param_value"
    elif grep -q "^$param_name=" "$config_file"; then
        echo "Parameter $param_name has no value."
    else
        echo "Parameter $param_name does not exist."
    fi
}

# Function to update a configuration value
update_config() {
    param_name="$1"
    new_value="$2"

    # Check if the parameter exists in the config file
    if grep -q "^$param_name=" "$config_file"; then
        # Update the parameter with the new value
        sed -i "s/^$param_name=.*/$param_name=$new_value/" "$config_file"
        echo "Updated $param_name to $new_value"
    else
        echo "Parameter $param_name not found in the configuration file."
    fi
}

# Main script logic
if [ "$#" -lt 2 ]; then
    echo "Usage: $0 [get|update] <parameter_name> [new_value]"
    exit 1
fi

command="$1"
param_name="$2"

case "$command" in
    get)
        get_config "$param_name"
        ;;
    update)
        if [ "$#" -ne 3 ]; then
            echo "Usage: $0 update <parameter_name> <new_value>"
            exit 1
        fi
        new_value="$3"
        update_config "$param_name" "$new_value"
        ;;
    *)
        echo "Invalid command. Usage: $0 [get|update] <parameter_name> [new_value]"
        exit 1
        ;;
