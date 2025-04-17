#!/bin/bash
################################################################################
# Script Name: update.sh
# Description: Check if update is available, install updates.
# Usage: opencli update [--check | --force]
# Author: Stefan Pejcic
# Created: 10.10.2023
# Last Modified: 23.02.2025
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


usage() {
  echo "Usage: opencli update [OPTION]"
  echo ""
  echo "Options:"
  echo "  --check             Check if update is available"
  echo "  --force             Force update even when autopatch/autoupdate is disabled"
  echo "  (no argument)       Run update if autopatch/autoupdate is enabled"
  echo "  -h, --help          Show this help message"
  exit 1
}


image_name="openpanel/openpanel-ui"
LOG_FILE="/var/log/openpanel/admin/notifications.log"


# Define the route to check for updates
update_check() {

    get_last_message_content() {
      tail -n 1 "$LOG_FILE" 2>/dev/null
    }
    
    is_unread_message_present() {
      local unread_message_content="$1"
      grep -q "UNREAD.*$unread_message_content" "$LOG_FILE" && return 0 || return 1
    }
    
    write_notification_for_update_check() {
      local title="$1"
      local message="$2"
      local current_message="$(date '+%Y-%m-%d %H:%M:%S') UNREAD $title MESSAGE: $message"
      local last_message_content=$(get_last_message_content)
    
      # Check if the current message content is the same as the last one and has "UNREAD" status
      if [ "$message" != "$last_message_content" ] && ! is_unread_message_present "$title"; then
        echo "$current_message" >> "$LOG_FILE"
      fi
    }




    local_version=$(opencli version)



    #OLD! remote_version=$(curl -s "https://raw.githubusercontent.com/stefanpejcic/OpenPanel/refs/heads/main/website/docusaurus.config.js" | grep -oP '(?<=label: ")[0-9]+\.[0-9]+\.[0-9]+')
    tags=$(curl -s "https://hub.docker.com/v2/repositories/${image_name}/tags" | jq -r '.results[].name')
    
    remote_version=$(echo "$tags" | grep -v '^latest$' | sort -V | tail -n 1)

    # If github unreachable
    if [ -z "$remote_version" ]; then
        echo '{"error": "Error fetching remote version"}' >&2
        write_notification_for_update_check "Update check failed" "Failed connecting to GitHub"
        exit 1
    fi

    # Compare the local and remote versions
    if [ "$local_version" == "$remote_version" ]; then
        echo '{"status": "Up to date", "installed_version": "'"$local_version"'"}'
    elif [ "$local_version" \> "$remote_version" ]; then
        #write_notification_for_update_check "New OpenPanel update is available" "Installed version: $local_version | Available version: $remote_version"
        echo '{"status": "Local version is greater", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
    else
        # Check if skip_versions file exists and if remote version matches
        if [ -f "/etc/openpanel/upgrade/skip_versions" ]; then
            if grep -q "$remote_version" "/etc/openpanel/upgrade/skip_versions"; then
                echo '{"status": "Skipped version", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
                exit 0
            fi
        fi
        write_notification_for_update_check "New OpenPanel update is available" "Installed version: $local_version | Available version: $remote_version"
        echo '{"status": "Update available", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
    fi
}


# Function to check if an update is needed
check_update() {
    
    write_notification() {
      local title="$1"
      local message="$2"
      local current_message="$(date '+%Y-%m-%d %H:%M:%S') UNREAD $title MESSAGE: $message"
      echo "$current_message" >> "$LOG_FILE"
    }

    remove_last_notification() {
        sed -i '/OpenPanel update started MESSAGE/d' "$LOG_FILE"
    }

    remove_update_notifications() {
        sed -i "/New OpenPanel update is available/d" "$LOG_FILE"
    }

    ensure_jq_installed() {
        # Check if jq is installed
        if ! command -v jq &> /dev/null; then
            # Detect the package manager and install jq
            if command -v apt-get &> /dev/null; then
                sudo apt-get update > /dev/null 2>&1
                sudo apt-get install -y -qq jq > /dev/null 2>&1
            elif command -v yum &> /dev/null; then
                sudo yum install -y -q jq > /dev/null 2>&1
            elif command -v dnf &> /dev/null; then
                sudo dnf install -y -q jq > /dev/null 2>&1
            else
                echo "Error: No compatible package manager found. Please install jq manually and try again."
                exit 1
            fi
    
            # Check if installation was successful
            if ! command -v jq &> /dev/null; then
                echo "Error: jq installation failed. Please install jq manually and try again."
                exit 1
            fi
        fi
    }





    ensure_jq_installed
    
    local force_update=false

    # Check if the '--force' flag is provided
    if [[ "$1" == "--force" ]]; then
        force_update=true
        echo "Forcing updates, ignoring autopatch and autoupdate settings."
    fi

    local autopatch
    local autoupdate

    if [ "$force_update" = true ]; then
        # When the '--force' flag is provided, set autopatch and autoupdate to "on"
        autopatch="on"
        autoupdate="on"
    else
        autopatch=$(awk -F= '/^autopatch=/{print $2}' /etc/openpanel/openpanel/conf/openpanel.config)
        autoupdate=$(awk -F= '/^autoupdate=/{print $2}' /etc/openpanel/openpanel/conf/openpanel.config)
    fi

    # Only proceed if autopatch or autoupdate is set to "on"
    if [ "$autopatch" = "on" ] || [ "$autoupdate" = "on" ] || [ "$force_update" = true ]; then

        # Extract the local and remote version from the update status
        local local_version=$(opencli version)
        local remote_version=$(opencli update --check | jq -r '.latest_version')

        if [ "$remote_version" == "null" ]; then
          echo "No update available."
          exit 1
        fi
        # Check if autoupdate is "no" and not forcing the update
        if [ "$autoupdate" = "off" ] && [ "$local_version" \< "$remote_version" ] && [ "$force_update" = false ]; then
            echo "Update is available, autopatch will be installed."
                # Check if skip_versions file exists and if remote version matches
                if [ -f "/etc/openpanel/upgrade/skip_versions" ]; then
                    if grep -q "$remote_version" "/etc/openpanel/upgrade/skip_versions"; then
                        echo "Version $remote_version is skipped due to /etc/openpanel/upgrade/skip_versions file."
                    else
                        run_update_immediately "$remote_version"
                    fi
                else
                    run_update_immediately "$remote_version"
                fi
        else
            # If autoupdate is "on" or force_update is true, check if local_version is less than remote_version
            if [ "$local_version" \< "$remote_version" ] || [ "$force_update" = true ]; then
                echo "Update is available and will be automatically installed."
                    # Check if skip_versions file exists and if remote version matches
                    if [ -f "/etc/openpanel/upgrade/skip_versions" ]; then
                        if grep -q "$remote_version" "/etc/openpanel/upgrade/skip_versions"; then
                            echo "Version $remote_version is skipped due to /etc/openpanel/upgrade/skip_versions file."
                        else
                            run_update_immediately "$remote_version"
                        fi
                    else
                        run_update_immediately "$remote_version"
                    fi
            else
                echo "No update available."
            fi
        fi
    else
        echo "Autopatch and Autoupdate are both set to 'off'. No updates will be installed automatically."
    fi
}

# Function to compare two semantic versions
compare_versions() {
    local version1=$1
    local version2=$2
    local IFS='.'

    local array1=($version1)
    local array2=($version2)

    for ((i = 0; i < ${#array1[@]}; i++)); do
        if ((array1[i] > array2[i])); then
            echo 1  # version1 > version2
            return
        elif ((array1[i] < array2[i])); then
            echo -1  # version1 < version2
            return
        fi
    done

    echo 0  # version1 == version2
}


run_update_immediately(){
    version="$1"
    log_dir="/var/log/openpanel/updates"
    mkdir -p $log_dir
    log_file="$log_dir/$version.log"
    # if not first try then set timestamp in filename
    if [ -f "$log_file" ]; then
        timestamp=$(date +"%Y%m%d_%H%M%S")
        log_file="${log_dir}/${version}_${timestamp}.log"
    fi

    touch $log_file

    # log in file and show on terminal!
    log() {
        echo "" | tee -a "$log_file" > /dev/null # only in log file
        echo "$1" | tee -a "$log_file"
    }

    run_custom_postupdate_script() {
        if [ -f "/root/openpanel_run_after_update" ]; then
            log " "
            log "Running post update script: '/root/openpanel_run_after_update'"
            log "https://dev.openpanel.com/customize.html#After-update"
            bash /root/openpanel_run_after_update | tee -a "$log_file"
        fi
    }


    
    write_notification "OpenPanel update started" "Started update to version $version - Log file: $log_file"
   
    log "Updating to version $version"

    log "Updating OpenPanel.."
    docker image pull ${image_name}:${version} | tee -a "$log_file"
    
    log "Setting version in /root/.env"
    sed -i "s/^VERSION=.*$/VERSION=\"$version\"/" /root/.env | tee -a "$log_file"


    log "Updating configuration files.."
    cd /etc/openpanel && git pull 2>&1 | tee -a "$log_file"

    log "Updating OpenCLI.."
    rm /usr/local/opencli/aliases.txt > /dev/null 2>&1
    cd /usr/local/opencli && git reset --hard origin/1.1 && git pull 2>&1 | tee -a "$log_file"
 
    log "Updating OpenAdmin.."
    cd /usr/local/admin && git pull 2>&1 | tee -a "$log_file"
    chmod +x /usr/local/admin/modules/security/csf.pl  > /dev/null 2>&1 # for csf!
    ln -s /etc/csf/ui/images/ /usr/local/admin/static/configservercsf  > /dev/null 2>&1 # for csf!

    log "Restarting OpenPanel service to use the newest image.."
    cd /root && docker --context default compose down openpanel && docker --context default compose up -d openpanel | tee -a "$log_file"
    
    log "Adding OpenCLI commands to path.."
    opencli commands | tee -a "$log_file"

    log "Checking for custom scripts.."
    url="https://raw.githubusercontent.com/stefanpejcic/OpenPanel/refs/heads/main/version/$version/UPDATE.sh"
    wget --spider -q "$url"
    if [ $? -ne 0 ]; then
        :
        log "No custom scripts provided.."
    else
        log "Downloading and executing custom post-update script for this version: $url"
        timeout 300 bash -c "wget -q -O - '$url' | bash" &>> "$log_file"
        if [ $? -eq 124 ]; then
            log "Error: Running custom post-update script for version $version timed out after 5 minutes."
        fi
    fi
    
    log "Checking for POST-upgrade scripts.."
    run_custom_postupdate_script

    remove_last_notification # remove update started msg
    remove_update_notifications # remove update available msg
    write_notification "OpenPanel updated successfully!" "OpenPanel updated to version $version - Log file: $log_file"
    
    log "DONE!"

    log "Restarting OpenAdmin service.."
    service admin restart 2>&1 | tee -a "$log_file"
   
}

case "$1" in
  --check)
    # only check for updates!
    update_check
    ;;
  "")
    # try update
    check_update
    ;;
  --force)
    # force update
    check_update "$@"
    ;;
  -h|--help)
    usage
    ;;
  *)
    echo "Unknown argument: $1"
    usage
    ;;
esac





