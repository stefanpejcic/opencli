#!/bin/bash
################################################################################
# Script Name: files/fix_permissions.sh
# Description: Fix permissions for users /home directory files inside the container.
# Usage: opencli files-fix_permissions <USERNAME|--all> [PATH] [--debug]
# Author: Stefan Pejcic
# Created: 15.11.2023
# Last Modified: 21.08.2026
# Company: OpenPanel, LLC.
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

# shellcheck disable=SC1091
. /usr/local/opencli/lib/requirement.sh

verbose=""

check_and_fix_FTP_permissions() {
    local user="$1"
    local uid="$2"
    local base_path="/home/${user}/docker-data/volumes/${user}_html_data/_data"
    local found=0
    while read -r directory; do

            [ -z "$directory" ] && continue
            found=1

            relative_path="${directory#/var/www/html/}"
            relative_path="${relative_path#/var/www/html}"

            [ -z "$relative_path" ] && continue

            new_directory="${base_path}/${relative_path}"

            if [ -d "$new_directory" ]; then
                echo "[*] Fixing ownership for FTP path: $new_directory"
                chown -R "$uid:$uid" "$new_directory"
            fi
        done < <(opencli ftp-list "$user" | tail -n +2 | cut -d'|' -f2 | sed 's/^ *//;s/ *$//' | grep -v "^/var/www/html$")

    if [ "$found" -eq 1 ]; then
        chmod +rx "/home/$user" "/home/$user/docker-data" "/home/$user/docker-data/volumes" "/home/$user/docker-data/volumes/${user}_html_data" "/home/$user/docker-data/volumes/${user}_html_data/_data"
    fi
}

apply_permissions_in_container() {
  local username="$1"
  local path="$2"

        get_user_info() {
            local user="$1"
            local query="SELECT id, server FROM users WHERE username = '${user}';"
            user_info=$(mariadb -se "$query")
            
            user_id=$(echo "$user_info" | awk '{print $1}')
            context=$(echo "$user_info" | awk '{print $2}')
            
            echo "$user_id,$context"
        }
        
        # openlitespeed's lsphp always runs as container uid/gid 65534 (nobody:nogroup) since the image can't be configured to run as another user - https://github.com/litespeedtech/ols-dockerfiles/issues/13#issuecomment-4275956701     
        mapped_gid_for_container_nogroup() {
            local container="$1" uid sock gid_map
            uid=$(stat -c '%u' "/home/$context" 2>/dev/null) || return 1
            sock="unix:///hostfs/run/user/${uid}/podman/podman.sock"

            # https://github.com/stefanpejcic/openpanel/issues/1116
            gid_map=$(CONTAINER_HOST="$sock" podman --remote exec "$container" cat /proc/self/gid_map 2>/dev/null)
            [ -z "$gid_map" ] && return 1

            awk -v want=65534 '
                { inside=$1; hostid=$2; len=$3
                  if (want >= inside && want < inside+len) { print hostid + (want-inside); found=1; exit } }
                END { exit !found }
            ' <<< "$gid_map"
        }

        apply_workaround_for_litespeed() {
            local raw_ws webserver mapped_gid
            raw_ws=$(grep "^WEB_SERVER=" "/home/$context/.env" | head -n1 | awk -F '=' '{print $2}' | tr -d '[:space:]' | sed 's/^"\(.*\)"$/\1/')
            webserver=$(echo "$raw_ws" | grep -Eo 'nginx|openresty|apache|openlitespeed|litespeed' | head -n1)
            [[ "$webserver" == "openlitespeed" || "$webserver" == "litespeed" ]] || return

            if mapped_gid=$(mapped_gid_for_container_nogroup "$webserver"); then
                gid="$mapped_gid"
            else
                echo "WARNING: could not determine ${context}'s real nogroup gid (is the '${webserver}' container running?) - falling back to 65534, which is likely wrong. Run 'opencli files-fix_permissions ${context}' again once the container is up." >&2
                gid="65534" # nogroup
            fi
        }

        result=$(get_user_info "$username")
        context=$(echo "$result" | cut -d',' -f2)

        if [ -z "$context" ]; then
            echo "FATAL ERROR: user $username does not have a valid docker context."
            exit 1
        fi

        if [ -n "$path" ]; then       
            if [[ $path == /var/www/html/* ]]; then
                path="${path#/var/www/html/}"
            fi

            directory="/home/${context}/docker-data/volumes/${context}_html_data/_data/$path"
            fake_directory="/var/www/html/$path"
        else   
            directory="/home/${context}/docker-data/volumes/${context}_html_data/_data/"     
            fake_directory="/var/www/html/"
        fi

        # get uid first!
        uid=$(stat -c '%u' "/home/$context" 2>/dev/null)
        gid="$uid"

        # https://github.com/litespeedtech/ols-dockerfiles/issues/13#issuecomment-4275956701
        apply_workaround_for_litespeed

        # owner
        #chown -R $verbose $uid:$uid $directory
        find "$directory" -print0 | xargs -0 chown $verbose "$uid":"$gid" > /dev/null 2>&1
        owner_result=$?

        # files
        find "$directory" -type f -print0 | xargs -0 chmod $verbose 664 > /dev/null 2>&1
        files_result=$?

        # folders
        find "$directory" -type d -print0 | xargs -0 chmod $verbose 775
        folders_result=$?

        check_and_fix_FTP_permissions "$username" "$uid"
        ftp_result=$?

        # CHECK ALL 4
        if [ $owner_result -eq 0 ] && [ $files_result -eq 0 ] && [ $folders_result -eq 0 ] && [ $ftp_result -eq 0 ]; then
            echo "Permissions applied successfully to $fake_directory"
        else
            echo "Error applying permissions to $fake_directory"
        fi
}



args=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --debug)
      verbose="-v"
      shift
      ;;
    *)
      args+=("$1")
      shift
      ;;
  esac
done

if [ "${args[0]}" == "--all" ]; then
  require_command jq
  for username in $(opencli user-list --json | jq -r '.[].username'); do
    apply_permissions_in_container "$username"
  done
else
  username="${args[0]}"
  path="${args[1]:-}"
  [ -z "$username" ] && { echo "Username required"; exit 1; }
  apply_permissions_in_container "$username" "$path"
fi
