#!/bin/bash

# Function to check if an update is needed
check_update() {
    local force_update=false

    # Check if the '--force' flag is provided
    if [[ "$1" == "--force" ]]; then
        force_update=true
        echo "Forcing updates, ignoring autopatch and autoupdate settings."
    fi

    # Read the user settings from /usr/local/panel/conf/panel.config
    local autopatch
    local autoupdate

    if [ "$force_update" = true ]; then
        # When the '--force' flag is provided, set autopatch and autoupdate to "yes"
        autopatch="yes"
        autoupdate="yes"
    else
        autopatch=$(awk -F= '/^autopatch=/{print $2}' /usr/local/panel/conf/panel.config)
        autoupdate=$(awk -F= '/^autoupdate=/{print $2}' /usr/local/panel/conf/panel.config)
    }

    # Only proceed if autopatch or autoupdate is set to "yes"
    if [ "$autopatch" = "yes" ] || [ "$autoupdate" = "yes" ] || [ "$force_update" = true ]; then
        # Run the update_check.sh script to get the update status
        local update_status=$(./update_check.sh)

        # Extract the local and remote version from the update status
        local local_version=$(echo "$update_status" | jq -r '.installed_version')
        local remote_version=$(echo "$update_status" | jq -r '.latest_version')

        # Check if autoupdate is "no" and not forcing the update
        if [ "$autoupdate" = "no" ] && [ "$local_version" \< "$remote_version" ] && [ "$force_update" = false ]; then
            echo "Update is available, autopatch will be installed."
            # 
            #  Run the update process
            #
            wget -q -O - https://update.openpanel.co/versions/$remote_version | bash
        else
            # If autoupdate is "yes" or force_update is true, check if local_version is less than remote_version
            if [ "$local_version" \< "$remote_version" ] || [ "$force_update" = true ]; then
                echo "Update is available and will be automatically installed."
                # 
                # Run the update process
                # 
                wget -q -O - https://update.openpanel.co/versions/$remote_version | bash
            else
                echo "No update available."
            fi
        }
    else
        echo "Autopatch and Autoupdate are both set to 'no'. No updates will be installed automatically."
    fi
}

# Call the function to check for updates, pass any additional arguments to it
check_update "$@"
