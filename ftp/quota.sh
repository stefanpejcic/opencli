#!/bin/bash
################################################################################
# Script Name: ftp/quota.sh
# Description: Show or set the disk quota for an FTP sub-user.
# Usage: opencli ftp-quota <username> [<quota>] <openpanel_username> [--debug]
# Docs: https://docs.openpanel.com/
# Author: Stefan Pejcic
# Created: 14.10.2026
# Last Modified: 14.10.2026
# Company: OpenPanel, LLC.
# Copyright (c) openpanel.com
# 
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation (the "Software"), to deal
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

if [ "$#" -lt 2 ] || [ "$#" -gt 4 ]; then
    echo "Usage: opencli ftp-quota <username> [<quota>] <openpanel_username> [--debug]"
    echo "       <quota> is in MB (500), or with a unit (500M, 2G), 0 or 'unlimited' removes the quota."
    exit 1
fi

DEBUG=false
ARGS=()
for arg in "$@"; do
    case $arg in
        --debug) DEBUG=true ;;
        *) ARGS+=("$arg") ;;
    esac
done

username="${ARGS[0],,}"
if [ "${#ARGS[@]}" -eq 3 ]; then
    quota="${ARGS[1]}"
    openpanel_username="${ARGS[2]}"
elif [ "${#ARGS[@]}" -eq 2 ]; then
    quota=""
    openpanel_username="${ARGS[1]}"
else
    echo "Usage: opencli ftp-quota <username> [<quota>] <openpanel_username> [--debug]"
    exit 1
fi

at_count="${username//[^@]/}"
if [[ -z "$username" ]] || [[ "${#at_count}" -ne 1 ]] || [[ "$username" == @* || "$username" == *@ ]] || ! [[ "$username" =~ ^[A-Za-z0-9._@-]+$ ]]; then
    echo "ERROR: Invalid FTP username '$username'."
    exit 1
fi

if [[ ! "$openpanel_username" =~ ^[a-z0-9_-]+$ ]]; then
    echo "ERROR: Invalid OpenPanel username."
    exit 1
fi

get_docker_context_for_user(){
	context=$(mariadb -N -e "
	SELECT u.server
	FROM users u
	WHERE u.username='$openpanel_username'
	AND EXISTS (
	    SELECT 1
	    FROM domains d
	    WHERE d.domain_url = SUBSTRING_INDEX('$username','@',-1)
	      AND d.user_id = u.id
	);
	")

    if [ -z "$context" ]; then
        echo "ERROR: No context found for user '$openpanel_username' - or does not own the domain name. Aborting!"
        exit 1
    fi
}

# 500, 500M and 2G all end up as MB, 0 means unlimited
parse_quota() {
    local q="${1^^}"
    case "$q" in
        UNLIMITED) quota_mb=0 ;;
        *G) [[ "${q%G}" =~ ^[0-9]+$ ]] && quota_mb=$(( 10#${q%G} * 1024 )) ;;
        *M) [[ "${q%M}" =~ ^[0-9]+$ ]] && quota_mb=$(( 10#${q%M} )) ;;
        *)  [[ "$q" =~ ^[0-9]+$ ]] && quota_mb=$(( 10#$q )) ;;
    esac
    if [ -z "$quota_mb" ]; then
        echo "ERROR: Invalid quota '$1'. Use a number of MB (500), a value with a unit (500M, 2G), or 'unlimited'."
        exit 1
    fi
}

human_mb() {
    if [ "$1" -ge 1024 ] && [ $(( $1 % 1024 )) -eq 0 ]; then
        echo "$(( $1 / 1024 ))G"
    else
        echo "${1}M"
    fi
}

show_quota() {
    local current used_kb
    current=$(awk -F'|' -v user="$username" '$1 == user { print $6 }' "$users_list")
    used_kb=$(awk -F'|' -v user="$username" '$1 == user { print $2 }' "$usage_file" 2>/dev/null)
    if [[ ! "$current" =~ ^[0-9]+$ ]] || [ "$current" -eq 0 ]; then
        echo "FTP user '$username' has no quota (unlimited)."
    else
        echo "FTP user '$username' quota: $(human_mb "$current"), used: $(( ${used_kb:-0} / 1024 ))M"
    fi
}

set_quota() {
    local tmp="/tmp/${context}.ftp_users.list.tmp"
    if ! awk -F'|' -v OFS='|' -v user="$username" -v q="$quota_mb" '
        $1 == user { if (q == 0) { NF = 5 } else { for (i = NF + 1; i <= 5; i++) $i = ""; $6 = q } }
        { print }
    ' "$users_list" > "$tmp"; then
        echo "ERROR: Failed to update $users_list"
        rm -f "$tmp"
        exit 1
    fi
    cat "$tmp" > "$users_list"
    rm -f "$tmp"

    opencli ftp-users >/dev/null 2>&1

    # applies the block or unblock right away instead of on the next check
    if ! podman exec openadmin_ftp /bin/start_vsftpd.sh --check-quotas >/tmp/ftp-quota-check.log 2>&1; then
        if [ "$DEBUG" = true ]; then
            cat /tmp/ftp-quota-check.log
        fi
        echo "WARNING: Quota saved, but the FTP service could not check it now, it will apply on the next check."
    fi

    if [ "$quota_mb" -eq 0 ]; then
        echo "Success: Quota removed for FTP user '$username'."
    else
        echo "Success: FTP quota for user '$username' set to $(human_mb "$quota_mb")."
    fi
}

get_docker_context_for_user

users_list="/etc/openpanel/ftp/users/${context}/users.list"
usage_file="/etc/openpanel/ftp/users/${context}/quota.usage"

if ! grep -Fq "$username|" "$users_list" 2>/dev/null; then
    echo "ERROR: FTP User '$username' does not exist."
    exit 1
fi

if [ -z "$quota" ]; then
    show_quota
    exit 0
fi

parse_quota "$quota"
set_quota
