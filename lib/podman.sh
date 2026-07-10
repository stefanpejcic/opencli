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
# Company: openpanel.com
################################################################################

# echoes the CONTAINER_HOST URL for <username>'s rootless podman socket
podman_user_socket() {
    local user="$1" uid
    uid="$(id -u "$user" 2>/dev/null)" || { echo "podman_user_socket: no such user '$user'" >&2; return 1; }
    echo "unix:///run/user/${uid}/podman/podman.sock"
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
