#!/bin/bash
################################################################################
# Script Name: lib/backups.sh
# Description: Shared helpers for the read-only mounts of a user's backup service.
# Usage: . /usr/local/opencli/lib/backups.sh
# Author: Stefan Pejcic
# Created: 15.10.2026
# Last Modified: 15.10.2026
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
. /usr/local/opencli/lib/podman.sh

# real mail dir of a domain, mail_data only holds a symlink to it that the backup container can't follow, e.g. backup_mail_dir <context> <domain>
backup_mail_dir() {
    local context="$1" domain="$2" store_in
    store_in=$(grep -E '^email_storage_location=' /etc/openpanel/openadmin/config/admin.ini 2>/dev/null | cut -d'=' -f2- | xargs)
    if [[ "$store_in" == /* ]]; then
        echo "${store_in%/}/$domain"
    else
        echo "/home/$context/mail/$domain"
    fi
}

# adds a read-only mount to <context>'s backup service, right after the legacy mail_data one, e.g. backup_mount_add <context> <host_dir> <container_dir>
backup_mount_add() {
    local context="$1" src="$2" dest="$3" compose line
    compose="$(podman_compose_file "$context")"
    line="      - ${src%/}:${dest}:ro"
    [[ -f "$compose" ]] || return 1
    # domains-add runs the mail and dns steps in parallel, both edit this file
    (
        flock 9 || exit 1
        grep -qxF "$line" "$compose" && exit 0
        grep -qx "      - mail_data:/backup/mail:ro" "$compose" || exit 1
        awk -v line="$line" '{print} $0 == "      - mail_data:/backup/mail:ro" {print line}' "$compose" > "$compose.tmp" \
            && cat "$compose.tmp" > "$compose" && rm -f "$compose.tmp"
    ) 9>"/tmp/.backup-mounts-$context.lock"
}

# removes the mount for <container_dir> from <context>'s backup service, e.g. backup_mount_remove <context> <container_dir>
backup_mount_remove() {
    local context="$1" dest="$2" compose
    compose="$(podman_compose_file "$context")"
    [[ -f "$compose" ]] || return 0
    (
        flock 9 || exit 1
        grep -q ":${dest}:ro$" "$compose" || exit 0
        awk -v suffix=":${dest}:ro" '!(substr($0, 1, 8) == "      - " && substr($0, length($0) - length(suffix) + 1) == suffix)' "$compose" > "$compose.tmp" \
            && cat "$compose.tmp" > "$compose" && rm -f "$compose.tmp"
    ) 9>"/tmp/.backup-mounts-$context.lock"
}

# admin-configured accounts get a fresh backup container per opencli docker-backup run, user-configured ones keep it running with its own schedule, e.g. backup_service_reload <username> <context>
backup_service_reload() {
    local username="$1" context="$2" sock
    [[ -e "/etc/openpanel/openpanel/core/users/$username/admin.backups" ]] && return 0
    [[ "$(podman_user "$context" inspect -f '{{.State.Running}}' backup 2>/dev/null)" == "true" ]] || return 0
    sock="$(podman_user_socket "$context")" || return 1
    # recreated in the background so the caller doesn't wait on podman-compose
    nohup sh -c "cd /home/$context && CONTAINER_HOST=$sock podman-compose up -d --no-deps --force-recreate backup" </dev/null >/dev/null 2>&1 &
    disown
}
