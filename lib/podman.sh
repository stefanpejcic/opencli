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

# run a podman command against <username>'s rootless podman instance
# usage: podman_user <username> <podman-args...>
podman_user() {
    local user="$1"; shift
    local sock
    sock="$(podman_user_socket "$user")" || return 1
    CONTAINER_HOST="$sock" podman --remote "$@"
}

# run podman-compose against <username>'s rootless podman instance
# usage: podman_compose_user <username> <podman-compose-args...>
# NOTE: don't try to force this via `--podman-args="--remote"` - podman-compose
# inserts extra podman-args AFTER the subcommand (e.g. `podman ps --remote`),
# but `--remote` is only valid as a GLOBAL flag BEFORE the subcommand
# (`podman --remote ps`), so that combination fails with "unknown flag:
# --remote". CONTAINER_HOST alone is enough - podman auto-detects remote mode
# when it points somewhere other than the default local socket.
podman_compose_user() {
    local user="$1"; shift
    local sock
    sock="$(podman_user_socket "$user")" || return 1
    CONTAINER_HOST="$sock" podman-compose "$@"
}

# many scripts carry a "context" value pulled from the users table's `server`
# column (a holdover from when it could be a remote node/ssh host - now it's
# always either a username or "default"/"root"/"" for root's own system stack)
# usage: podman_ctx <context> <podman-args...>
podman_ctx() {
    local context="$1"; shift
    case "$context" in
        ""|default|root) podman "$@" ;;
        *)                podman_user "$context" "$@" ;;
    esac
}

# same as podman_ctx but for podman-compose
# usage: podman_compose_ctx <context> <podman-compose-args...>
podman_compose_ctx() {
    local context="$1"; shift
    case "$context" in
        ""|default|root) podman-compose "$@" ;;
        *)                podman_compose_user "$context" "$@" ;;
    esac
}

# Tells whether <container> is actually running, by state rather than by
# presence in `podman ps` — a container wedged in a transitional state
# (created/paused/restarting/removing, or a stuck "starting"/"exiting") is
# absent from `podman ps` too, so a plain `podman ps -q -f name=X` check
# can't tell "not running" apart from "stuck", and callers that assume it
# means "not running" end up recompose-up'ing on top of a wedged container
# instead of recovering it.
# usage: podman_is_running <container>
podman_is_running() {
    [[ "$(podman inspect "$1" --format '{{.State.Status}}' 2>/dev/null)" == "running" ]]
}

# Ensures <container> is actually running, recovering it via podman-compose
# if it's exited/absent, or by force-removing it first if it's wedged in a
# transitional state that a plain restart/compose-up won't clear. Returns 0
# once the container is confirmed running, 1 otherwise.
# usage: podman_ensure_running <container> <compose_dir> <compose_service> [timeout_secs]
podman_ensure_running() {
    local container="$1" compose_dir="$2" service="$3" timeout_secs="${4:-30}"
    local state
    state=$(podman inspect "$container" --format '{{.State.Status}}' 2>/dev/null)

    if [[ "$state" == "running" ]]; then
        return 0
    fi

    if [[ -n "$state" && "$state" != "exited" && "$state" != "stopped" ]]; then
        # wedged (created/paused/restarting/removing/stuck starting-exiting) — a
        # plain restart or compose-up won't clear this, force it out first
        podman kill "$container" &>/dev/null
        podman rm -f "$container" &>/dev/null
        podman rm -f --storage "$container" &>/dev/null
    fi

    (cd "$compose_dir" && timeout "$timeout_secs" podman-compose up -d "$service") &>/dev/null
    podman_is_running "$container"
}

# echoes the path to <context>'s docker-compose.yml (root's own stack for
# ""/default/root, otherwise the user's per-account compose file)
# usage: podman_compose_file <context>
podman_compose_file() {
    local context="$1"
    case "$context" in
        ""|default|root) echo "/root/docker-compose.yml" ;;
        *)                echo "/home/$context/docker-compose.yml" ;;
    esac
}

# derives a user-<uid>.slice TasksMax (cgroup pids.max) ceiling from the plan's
# RAM allotment (GB). Unlike CPU/RAM/disk, a process/thread cap isn't a plan
# tier customers shop for - it's a fork-bomb/runaway-process safety net - so
# it's derived from ram rather than stored as its own plan column, and stays
# proportional automatically whenever a plan's ram value changes. A user's own
# /home/<user>/TasksMax file (single integer) overrides the derived value.
# usage: derive_tasks_max <ram_gb> [username]
readonly TASKS_PER_RAM_GB=150
readonly TASKS_MAX_FLOOR=150

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
