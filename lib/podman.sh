#!/bin/bash
################################################################################
# Script Name: lib/podman.sh
# Description: Shared helpers for talking to a user's rootless Podman instance.
#              Replaces the old `docker --context=<user> ...` pattern now that
#              opencli talks to podman directly instead of through a docker
#              compatibility shim.
#              NOTE: `timeout` execs a binary directly, so it can't wrap these
#              bash functions (`timeout 5 podman_user ...` will fail with "not
#              found"). Either skip timeout, or inline CONTAINER_HOST yourself
#              and call `timeout N podman --remote ...` / `podman-compose ...`
#              directly, or use `timeout N bash -c 'source .../lib/podman.sh; podman_user "$@"' _ ...`.
#              Also not usable from a `nohup bash -c "..."` detached subshell
#              unless you `export -f podman_user podman_compose_user` first —
#              simplest is usually to inline CONTAINER_HOST there too.
# Docs: https://docs.openpanel.com
# Author: Stefan Pejcic
# Created: 10.07.2026
# Company: OpenPanel, LLC.
################################################################################

# echoes the CONTAINER_HOST URL for <username>'s rootless podman socket
podman_user_socket() {
    local user="$1" uid
    uid="$(stat -c '%u' "/home/$user" 2>/dev/null)" || { echo "podman_user_socket: no such user '$user'" >&2; return 1; }
    echo "unix:///hostfs/run/user/${uid}/podman/podman.sock"
}

# runs a podman command against <username>'s rootless instance, e.g. podman_user <username> <podman-args...>
podman_user() {
    local user="$1"; shift
    local sock
    sock="$(podman_user_socket "$user")" || return 1
    CONTAINER_HOST="$sock" podman --remote "$@"
}

# runs podman-compose against <username>'s rootless instance, e.g. podman_compose_user <username> <podman-compose-args...>
# don't try to force this via --podman-args="--remote" -- podman-compose puts it after the subcommand where --remote isn't valid; CONTAINER_HOST alone is enough, podman auto-detects remote mode from it
podman_compose_user() {
    local user="$1"; shift
    local sock
    sock="$(podman_user_socket "$user")" || return 1
    CONTAINER_HOST="$sock" podman-compose "$@"
}

# "context" comes from the users table's server column (used to be a remote node/ssh host, now it's just a username or "default"/"root"/"" for root's own stack), e.g. podman_ctx <context> <podman-args...>
podman_ctx() {
    local context="$1"; shift
    case "$context" in
        ""|default|root) podman "$@" ;;
        *)                podman_user "$context" "$@" ;;
    esac
}

# same as podman_ctx but for podman-compose, e.g. podman_compose_ctx <context> <podman-compose-args...>
podman_compose_ctx() {
    local context="$1"; shift
    case "$context" in
        ""|default|root) podman-compose "$@" ;;
        *)                podman_compose_user "$context" "$@" ;;
    esac
}

# checks by state rather than presence in `podman ps` -- a wedged container (paused/restarting/stuck starting-exiting) is absent from `podman ps` too, so that check can't tell "not running" from "stuck", e.g. podman_is_running <container>
podman_is_running() {
    [[ "$(podman inspect "$1" --format '{{.State.Status}}' 2>/dev/null)" == "running" ]]
}

# recovers <container> via podman-compose if exited/absent, or force-removes it first if wedged in a state a plain restart won't clear; returns 0 once running, 1 otherwise -- e.g. podman_ensure_running <container> <compose_dir> <compose_service> [timeout_secs]
podman_ensure_running() {
    local container="$1" compose_dir="$2" service="$3" timeout_secs="${4:-30}"
    local state
    state=$(podman inspect "$container" --format '{{.State.Status}}' 2>/dev/null)

    if [[ "$state" == "running" ]]; then
        return 0
    fi

    if [[ -n "$state" && "$state" != "exited" && "$state" != "stopped" ]]; then
        # wedged (created/paused/restarting/removing/stuck starting-exiting) -- a plain restart or compose-up won't clear this, force it out first
        podman kill "$container" &>/dev/null
        podman rm -f "$container" &>/dev/null
        podman rm -f --storage "$container" &>/dev/null
    fi

    # --no-deps: without it podman-compose 1.0.6 can remove other services of the stack and not start them again (seen with clamav and phpmyadmin when only openpanel was started)
    (cd "$compose_dir" && timeout "$timeout_secs" podman-compose up -d --no-deps "$service") &>/dev/null
    podman_is_running "$container"
}

# echoes the shared image store root and users read through additionalimagestores in root's storage.conf, servers installed with 2.0.0-beta have it at /var/lib/openpanel/shared-containers/storage
shared_image_store() {
    local store
    store=$(awk '/^[[:space:]]*additionalimagestores/ { f = 1 } f && match($0, /"[^"]+"/) { print substr($0, RSTART + 1, RLENGTH - 2); exit } f && /\]/ { exit }' /etc/containers/storage.conf 2>/dev/null)
    echo "${store:-/var/lib/containers/shared-storage}"
}

# echoes the path to <context>'s docker-compose.yml (root's own stack for ""/default/root, otherwise the user's per-account file), e.g. podman_compose_file <context>
podman_compose_file() {
    local context="$1"
    case "$context" in
        ""|default|root) echo "/root/docker-compose.yml" ;;
        *)                echo "/home/$context/docker-compose.yml" ;;
    esac
}

# derives a user-<uid>.slice TasksMax (cgroup pids.max) from the plan's RAM, since a process cap is a fork-bomb safety net rather than something customers shop for, so it scales with ram instead of being its own plan column; a /home/<user>/TasksMax file overrides it -- e.g. derive_tasks_max <ram_gb> [username]
[[ -v TASKS_PER_RAM_GB ]] || readonly TASKS_PER_RAM_GB=1000
[[ -v TASKS_MAX_FLOOR ]] || readonly TASKS_MAX_FLOOR=1000

derive_tasks_max() {
    local ram_gb="$1" username="${2:-}"
    local tasks_max=$(( ram_gb * TASKS_PER_RAM_GB ))
    (( tasks_max < TASKS_MAX_FLOOR )) && tasks_max=$TASKS_MAX_FLOOR

    local override_file="/home/${username}/TasksMax"
    if [[ -n "$username" && -f "$override_file" ]]; then
        local override_value
        override_value="$(tr -dc '0-9' < "$override_file")"
        [[ -n "$override_value" ]] && tasks_max="$override_value"
    fi

    echo "$tasks_max"
}

# safety ceiling for a user-<uid>.slice whose plan has unlimited cpu/ram, so one account can never take the whole server: all cores but 2 (at least half of them), MemoryHigh at 80% and MemoryMax at 90% of host RAM -- e.g. read -r cpu_pct mem_high_mb mem_max_mb < <(unlimited_slice_ceiling)
unlimited_slice_ceiling() {
    local cores ram_mb cpu_pct
    cores=$(nproc --all)
    ram_mb=$(awk '/^MemTotal:/{print int($2/1024)}' /proc/meminfo)
    cpu_pct=$(( (cores - 2) * 100 ))
    (( cpu_pct < cores * 50 )) && cpu_pct=$(( cores * 50 ))
    echo "$cpu_pct $(( ram_mb * 80 / 100 )) $(( ram_mb * 90 / 100 ))"
}

# derives a user-<uid>.slice IOWeight from the plan's CPU cores (cores x 100, 50..1000, unlimited = 100), so bigger plans get a bigger share of a busy disk and nobody is throttled on an idle one -- e.g. derive_io_weight <cpu>
derive_io_weight() {
    awk -v c="${1%[Gg]}" 'BEGIN { w = (c + 0 == 0) ? 100 : int(c * 100); if (w < 50) w = 50; if (w > 1000) w = 1000; print w }'
}
