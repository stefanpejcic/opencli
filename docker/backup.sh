#!/bin/bash
################################################################################
# Script Name: docker/backup.sh
# Description: Backs up every account whose backups the hosting provider manages, or one with --account.
# Usage: opencli docker-backup [--parallel=N | --account=USERNAME]   (skip a user: touch /home/<context>/backup.off)
# Author: Stefan Pejcic
# Created: 22.07.2025
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

readonly LOG_FILE="/var/log/openpanel/admin/docker-backup.log"

# shellcheck disable=SC1091
. /usr/local/opencli/lib/podman.sh

# number of users backed up at the same time (each user has its own podman context)
# default: one backup per 4 CPU cores (gzip + upload per backup), at least 1, at most the number of users
PARALLEL=""
ACCOUNT=""
for arg in "$@"; do
    case "$arg" in
        --parallel=*) PARALLEL="${arg#*=}" ;;
        --account=*)
            ACCOUNT="${arg#*=}"
            [[ -n "$ACCOUNT" ]] || { echo "--account needs a username"; exit 1; }
            ;;
        *) echo "Usage: opencli docker-backup [--parallel=N | --account=USERNAME]"; exit 1 ;;
    esac
done
[[ -z "$PARALLEL" || "$PARALLEL" =~ ^[1-9][0-9]*$ ]] || { echo "--parallel must be a positive number"; exit 1; }
[[ -n "$ACCOUNT" && -n "$PARALLEL" ]] && { echo "--parallel and --account can't be used together"; exit 1; }

# a backup that finds its account locked by another run exits with this
readonly LOCKED=75
readonly LOCK_DIR="/run/lock"
readonly CORE_USERS_DIR="/etc/openpanel/openpanel/core/users"

# the hosting provider manages an account's backups when OpenAdmin left the admin.backups marker, everyone else runs their own
is_provider_managed() {
    [[ -e "$CORE_USERS_DIR/$1/admin.backups" ]]
}

# parallel workers write to their own file (USER_LOG_FILE), appended to LOG_FILE when the user is done
log() {
    local message="$1"
    echo "$(date '+%Y-%m-%d %H:%M:%S') : $message" | tee -a "${USER_LOG_FILE:-$LOG_FILE}"
}

# sentinel only logs/sends it if docker_backup is enabled in notifications.ini
notify_failure() {
    local message="$1" details
    details=$(jq -nc --arg l "$LOG_FILE" '{log_file: $l}')
    nohup opencli sentinel --action=docker_backup --severity=critical --title="Backup failed" \
        --message="$message Detailed report was saved in: $LOG_FILE" --details="$details" >/dev/null 2>&1 &
    disown
}

# --no-deps since we start docker-proxy ourselves, otherwise podman-compose run can tear down the whole stack (mariadb included)
COMPOSE_RUN_FLAGS=(--rm --no-deps --entrypoint backup)

# https://github.com/stefanpejcic/OpenPanel/discussions/1146#discussioncomment-18528896
DOCKER_PROXY_WAIT_SECS=15
DOCKER_PROXY_SETTLE_SECS=2


is_proxy_running() {
    local context="$1"
    [[ "$(podman_ctx "$context" inspect -f '{{.State.Running}}' docker-proxy 2>/dev/null)" == "true" ]]
}

is_proxy_ready() {
    local context="$1"
    podman_ctx "$context" exec docker-proxy sh -c 'nc -z 127.0.0.1 2375' >/dev/null 2>&1
}

wait_for_proxy_ready() {
    local context="$1"
    local waited=0
    while (( waited < DOCKER_PROXY_WAIT_SECS )); do
        if is_proxy_running "$context" && is_proxy_ready "$context"; then
            log "docker-proxy is ready for context: $context (after ${waited}s)"
            sleep "$DOCKER_PROXY_SETTLE_SECS"
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done

    log "ERROR: docker-proxy not ready for context: $context within ${DOCKER_PROXY_WAIT_SECS}s"
    podman_ctx "$context" logs --tail 20 docker-proxy 2>&1 | tee -a "${USER_LOG_FILE:-$LOG_FILE}"
    return 1
}

ensure_docker_proxy_running() {
    local context="$1"

    if ! is_proxy_running "$context"; then
        log "docker-proxy not running for context: $context — starting it"

        local start_output start_exit
        start_output=$(podman_compose_ctx "$context" up -d docker-proxy 2>&1)
        start_exit=$?
        echo "$start_output" | tee -a "${USER_LOG_FILE:-$LOG_FILE}"

        if [ $start_exit -ne 0 ]; then
            log "ERROR: failed to start docker-proxy for context: $context | exit code: $start_exit"
            return 1
        fi
    fi

    wait_for_proxy_ready "$context"
}

# db containers started before backup_meta was in the compose file need one recreate, otherwise the manifest misses their dumps and the panel has to list the whole archive
refresh_db_containers() {
    local context="$1" service mounts output
    grep -q "backup_meta:/tmp/backup_meta" "$(podman_compose_file "$context")" 2>/dev/null || return 0
    for service in mysql mariadb postgres; do
        [[ "$(podman_ctx "$context" inspect -f '{{.State.Running}}' "$service" 2>/dev/null)" == "true" ]] || continue
        mounts=$(podman_ctx "$context" inspect -f '{{range .Mounts}}{{.Destination}} {{end}}' "$service" 2>/dev/null)
        [[ "$mounts" == *"/tmp/backup_meta"* ]] && continue
        log "Recreating $service for context: $context to add the backup_meta volume"
        if ! output=$(podman_compose_ctx "$context" up -d --no-deps --force-recreate "$service" 2>&1); then
            log "WARNING: failed to recreate $service for context: $context"
            echo "$output" | tee -a "${USER_LOG_FILE:-$LOG_FILE}"
        fi
    done
}

# podman context (users.server) of a user, defaults to the username
get_context() {
    local username="$1" ctx
    # shellcheck disable=SC1091
    source /usr/local/opencli/db.sh
    username_query="SELECT server FROM users WHERE username = '$(mysql_escape "$username")'"
    # shellcheck disable=SC2154 # mysql_database is set by the sourced db.sh
    ctx=$(mariadb -D "$mysql_database" -e "$username_query" -sN)
    echo "${ctx:-$username}"
}

# active, suspended or missing, a suspended account is renamed to SUSPENDED_<time>_<username>
get_account_state() {
    local name
    [[ "$1" == SUSPENDED_* ]] && { echo suspended; return; }
    # shellcheck disable=SC1091
    source /usr/local/opencli/db.sh
    name=$(mysql_escape "$1")
    # shellcheck disable=SC2154 # mysql_database is set by the sourced db.sh
    mariadb -D "$mysql_database" -sN -e "SELECT CASE
        WHEN EXISTS (SELECT 1 FROM users WHERE username = '$name') THEN 'active'
        WHEN EXISTS (SELECT 1 FROM users WHERE username LIKE 'SUSPENDED\_%' AND SUBSTRING_INDEX(username, '_', -1) = '$name') THEN 'suspended'
        ELSE 'missing' END"
}

# runs the rest of the arguments while holding the account's lock, so a scheduled run and --account never back up the same account at once
with_account_lock() {
    local context="$1" fd status
    shift
    exec {fd}>"$LOCK_DIR/openpanel-docker-backup-$context.lock" || return 1
    if ! flock -n "$fd"; then
        exec {fd}>&-
        return "$LOCKED"
    fi
    "$@"
    status=$?
    exec {fd}>&-
    return "$status"
}

run_for_user() {
    local username="$1"
    context=$(get_context "$username")

    cd /home/"$context"/ || { log "ERROR: Cannot cd into /home/$context/"; return 1; }

    if ! ensure_docker_proxy_running "$context"; then
        log "Backup FAILED for user: $username (context: $context) — docker-proxy unavailable"
        return 1
    fi
    refresh_db_containers "$context"
    # a leftover backup container has the same archive-pre labels, files-list.sh would run in both and race on backup_meta
    if podman_ctx "$context" container exists backup 2>/dev/null; then
        log "Removing leftover backup container for context: $context"
        podman_compose_ctx "$context" down backup >/dev/null 2>&1
    fi
    start_user_time=$(date +%s)
    # todo: switch to podman run so backups can use admin-set cpu/ram instead of the user's own
    local backup_output backup_exit
    backup_output=$(podman_compose_ctx "$context" run "${COMPOSE_RUN_FLAGS[@]}" backup 2>&1)
    backup_exit=$?
    end_user_time=$(date +%s)
    duration=$((end_user_time - start_user_time))

    log "--- backup container output for user: $username ---"
    echo "$backup_output" | tee -a "${USER_LOG_FILE:-$LOG_FILE}"
    log "--- end of backup container output for user: $username ---"

    local down_output down_exit
    down_output=$(podman_compose_ctx "$context" down backup 2>&1)
    down_exit=$?
    if [ $down_exit -ne 0 ]; then
        log "WARNING: failed to bring down backup service for user: $username (context: $context) | exit code: $down_exit"
        echo "$down_output" | tee -a "${USER_LOG_FILE:-$LOG_FILE}"
    fi

    if [ $backup_exit -ne 0 ]; then
        log "ERROR: Backup FAILED for user: $username (context: $context) | exit code: $backup_exit | Time taken: ${duration}s"
        return 1
    fi

    log "Backup completed for user: $username (context: $context) | Time taken: ${duration}s"
    reindex_user "$username" "$context"
}

# rebuild the user's /backups/list index so the new backup shows up without a manual reindex
reindex_user() {
    local username="$1" context="$2" reindex_output
    if ! podman container exists openpanel || [[ "$(podman inspect -f '{{.State.Running}}' openpanel 2>/dev/null)" != "true" ]]; then
        log "WARNING: openpanel container is not running, skipped backups reindex for user: $username"
        return 0
    fi
    if reindex_output=$(podman exec openpanel /openpanel backups-reindex "$context" 2>&1); then
        log "Backups reindexed for user: $username"
    else
        log "WARNING: backups reindex failed for user: $username | $reindex_output"
    fi
}

process_user() {
    local username="$1" status
    log "Processing user: $username"

    with_account_lock "$(get_context "$username")" run_for_user "$username"
    status=$?
    (( status == LOCKED )) && return "$LOCKED"
    if (( status != 0 )); then
        log "Failed processing user: $username"
        return 1
    fi

    return 0
}

# --account: back up one account now, with the same checks the scheduled run applies
process_one_account() {
    local username="$1" ctx status
    log "=== Backup of user $username started at $(date '+%Y-%m-%d %H:%M:%S') ==="

    case "$(get_account_state "$username")" in
        active) ;;
        suspended) log "ERROR: user $username is suspended, suspended accounts are not backed up"; return 1 ;;
        *) log "ERROR: user $username does not exist"; return 1 ;;
    esac
    ctx=$(get_context "$username")
    if [[ -e "/home/$ctx/backup.off" ]]; then
        log "ERROR: user $username is excluded from backups (/home/$ctx/backup.off exists)"
        return 1
    fi
    if ! is_provider_managed "$ctx"; then
        log "ERROR: user $username manages its own backups ($CORE_USERS_DIR/$ctx/admin.backups doesn't exist)"
        return 1
    fi

    process_user "$username"
    status=$?
    if (( status == LOCKED )); then
        log "ERROR: a backup of user $username is already running"
        return "$LOCKED"
    fi
    if (( status != 0 )); then
        notify_failure "Backup failed for user $username."
        return 1
    fi

    log "=== Backup of user $username finished at $(date '+%Y-%m-%d %H:%M:%S') ==="
    return 0
}

get_active_users() {
    local users
    users=$(opencli user-list --json 2>/dev/null | grep -v 'SUSPENDED' | awk -F'"' '/username/ {print $4}')
    
    if [[ -z "$users" ]] || [[ "$users" == "No users." ]]; then
        log "No active users found in the database"
        return 1
    fi
    
    echo "$users"
    return 0
}

process_all_users() {
    local users user_count current_index=1
    local failed_users=() locked_users=()
    
    start_time=$(date +%s)
    log "=== Backup started at $(date '+%Y-%m-%d %H:%M:%S') ==="
    
    log "Fetching list of active users..."
    if ! users=$(get_active_users); then
        log "ERROR: Could not retrieve active users from the database."
        notify_failure "Backups did not run because the list of active users could not be retrieved from the database."
        return 1
    fi
    
    # users can be excluded with /home/<context>/backup.off, and users without the admin.backups marker run their own backups
    local user ctx kept=() skipped_users=() self_managed=()
    for user in $users; do
        ctx=$(get_context "$user")
        if [[ -e "/home/$ctx/backup.off" ]]; then
            skipped_users+=("$user")
            log "Skipping user: $user (/home/$ctx/backup.off exists)"
        elif ! is_provider_managed "$ctx"; then
            self_managed+=("$user")
            log "Skipping user: $user (manages its own backups)"
        else
            kept+=("$user")
        fi
    done
    users="${kept[*]}"
    (( ${#skipped_users[@]} )) && log "Skipped ${#skipped_users[@]} users with backup.off: ${skipped_users[*]}"
    (( ${#self_managed[@]} )) && log "Skipped ${#self_managed[@]} users that manage their own backups: ${self_managed[*]}"
    if (( ${#kept[@]} == 0 )); then
        log "No users to back up (all have backup.off or manage their own backups)"
        return 0
    fi

    user_count=$(echo "$users" | wc -w)
    log "Found $user_count active users to process"

    if [[ -z "$PARALLEL" ]]; then
        PARALLEL=$(( $(nproc) / 4 ))
        (( PARALLEL < 1 )) && PARALLEL=1
    fi
    (( PARALLEL > user_count )) && PARALLEL=$user_count
    
    if (( PARALLEL <= 1 )); then
        for user in $users; do
            log "Processing user: $user ($current_index/$user_count)"

            process_user "$user"
            case $? in
                0) ;;
                "$LOCKED")
                    log "Skipping user: $user (a backup of this account is already running)"
                    locked_users+=("$user")
                    ;;
                *) failed_users+=("$user") ;;
            esac

            log "------------------------------"
            ((current_index++))
        done
    else
        log "Running up to $PARALLEL user backups in parallel"
        local tmpdir
        tmpdir=$(mktemp -d)
        declare -A pid_user=()

        # wait for one worker, append its log in one piece, record failure
        reap_one() {
            local done_pid status
            wait -n -p done_pid "${!pid_user[@]}"
            status=$?
            local u="${pid_user[$done_pid]}"
            cat "$tmpdir/$u.log" >> "$LOG_FILE"
            if (( status == LOCKED )); then
                log "Skipping user: $u (a backup of this account is already running)"
                locked_users+=("$u")
            elif (( status != 0 )); then
                failed_users+=("$u")
            fi
            echo "$(date '+%Y-%m-%d %H:%M:%S') : ------------------------------" >> "$LOG_FILE"
            unset "pid_user[$done_pid]"
        }

        for user in $users; do
            while (( ${#pid_user[@]} >= PARALLEL )); do reap_one; done
            log "Processing user: $user ($current_index/$user_count)"
            ( USER_LOG_FILE="$tmpdir/$user.log" process_user "$user" ) >/dev/null 2>&1 &
            pid_user[$!]="$user"
            ((current_index++))
        done
        while (( ${#pid_user[@]} > 0 )); do reap_one; done
        rm -rf "$tmpdir"
    fi

    end_time=$(date +%s)
    total_duration=$((end_time - start_time))
    
    (( ${#locked_users[@]} )) && log "Skipped ${#locked_users[@]} users that were already being backed up: ${locked_users[*]}"
    if [[ ${#failed_users[@]} -eq 0 ]] && (( ${#locked_users[@]} )); then
        log "Successfully processed $((user_count - ${#locked_users[@]})) of $user_count users"
    elif [[ ${#failed_users[@]} -eq 0 ]]; then
        log "Successfully processed all $user_count users"
    else
        log "Failed to process ${#failed_users[@]} users: ${failed_users[*]}"
        notify_failure "Backup failed for ${#failed_users[@]} of $user_count users: ${failed_users[*]}."
        return 1
    fi

    log "=== Backup finished at $(date '+%Y-%m-%d %H:%M:%S') | Total time: ${total_duration}s ==="
    return 0
}

if [[ -n "$ACCOUNT" ]]; then
    process_one_account "$ACCOUNT"
    exit $?
fi
process_all_users
