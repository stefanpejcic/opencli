#!/bin/bash

# Function to check if an update is needed
check_update() {
    # Read the user settings from /usr/local/panel/conf/panel.config
    local autopatch=$(awk -F= '/^autopatch=/{print $2}' /usr/local/panel/conf/panel.config)
    local autoupdate=$(awk -F= '/^autoupdate=/{print $2}' /usr/local/panel/conf/panel.config)
    #echo "Autoupdate: $autoupdate"
    #echo "Autopatch: $autopatch"
    # Only proceed if autopatch or autoupdate is set to "yes"
    if [ "$autopatch" = "yes" ] || [ "$autoupdate" = "yes" ]; then
        # Run the update_check.sh script to get the update status
        local update_status=$(./update_check.sh)

        # Extract the local and remote version from the update status
        local local_version=$(echo "$update_status" | jq -r '.installed_version')
        local remote_version=$(echo "$update_status" | jq -r '.latest_version')
        #echo "Installed version: $local_version"
        #echo "Latest OpenPanel version: $remote_version"

        # Check if autoupdate is "no"
        if [ "$autoupdate" = "no" ] && [ "$local_version" \< "$remote_version" ]; then
            # Extract the last two numbers of local and remote versions
            local_version_minor=$(echo "$local_version" | awk -F. '{print $(NF-1) "." $NF}')
            remote_version_last=$(echo "$remote_version" | awk -F. '{print $(NF-1) "." $NF}')
        	#echo "local_version_minor: $local_version_last"
        	#echo "remote_version_minor: $remote_version_minor"
            # Compare the last two numbers of local and remote versions
            if [ "$local_version_minor" != "$remote_version_minor" ]; then
                echo "Update is available, autopatch will be installed."
                # 
                #  Run the update process
                #
                wget -q -O - https://update.openpanel.co/versions/$remote_version | bash
            else
                echo "No update required: $local_version -> $remote_version"
            fi
        else
            # If autoupdate is "yes", check if local_version is less than remote_version
            if [ "$local_version" \< "$remote_version" ]; then
                echo "Update is available and will be automatically installed."
                # 
                # Run the update process
                # 
                wget -q -O - https://update.openpanel.co/versions/$remote_version | bash
            else
                echo "No update available."
            fi
        fi
    else
        echo "Autopatch and Autoupdate are both set to 'no'. No updates will be installed automatically."
    fi
}

# Call the function to check for updates
check_update
