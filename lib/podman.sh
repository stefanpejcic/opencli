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

# podman_compose_user with retries, two runs at once both create a missing network or volume and the loser exits 125 without starting anything, e.g. podman_compose_user_retry <username> -f <compose_file> up -d <service...>
podman_compose_user_retry() {
    local try
    for try in 1 2 3; do
        podman_compose_user "$@" && return 0
        (( try < 3 )) && sleep $(( try * 2 ))
    done
    return 1
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

# same as podman_is_running but against <context>'s podman, e.g. podman_ctx_is_running <context> <container>
podman_ctx_is_running() {
    [[ "$(podman_ctx "$1" inspect -f '{{.State.Running}}' "$2" 2>/dev/null)" == true ]]
}

# same as podman_ctx_is_running but also wants a listening tcp socket inside, a php-fpm container is running a few seconds before fpm accepts connections, e.g. podman_ctx_is_listening <context> <container>
podman_ctx_is_listening() {
    podman_ctx_is_running "$1" "$2" \
        && podman_ctx "$1" exec "$2" sh -c "awk '\$4 == \"0A\"' /proc/net/tcp /proc/net/tcp6 2>/dev/null | grep -q ." >/dev/null 2>&1
}

# reloads <context>'s webserver without dropping connections: config test, then a graceful signal to its pid 1, or lswsctrl restart on litespeed whose pid 1 is start.sh -- extra containers (e.g. the php-fpm a new vhost points to) are waited on for up to 60s until they listen, since nginx refuses a config whose upstream host doesn't resolve yet and 502s until fpm accepts; a stopped webserver gets started and a failed signal falls back to a restart -- e.g. podman_reload_webserver <context> <webserver> [container...]
podman_reload_webserver() {
    local context="$1" ws="$2"; shift 2
    local c ready tries=30 signal out
    local -a check

    if (( $# > 0 )); then
        while :; do
            ready=1
            podman_ctx_is_running "$context" "$ws" || ready=0
            for c in "$@"; do
                (( ready )) && { podman_ctx_is_listening "$context" "$c" || ready=0; }
            done
            (( ready || --tries <= 0 )) && break
            sleep 2
        done
    fi

    if ! podman_ctx_is_running "$context" "$ws"; then
        podman_ctx "$context" restart "$ws" >/dev/null 2>&1
        return
    fi

    case "$ws" in
        nginx|openresty) check=(nginx -t); signal=HUP ;;
        apache)          check=(apachectl configtest); signal=USR1 ;;
        *litespeed)
            podman_ctx "$context" exec "$ws" /usr/local/lsws/bin/lswsctrl restart >/dev/null 2>&1 \
                || podman_ctx "$context" restart "$ws" >/dev/null 2>&1
            return ;;
        *)
            podman_ctx "$context" restart "$ws" >/dev/null 2>&1
            return ;;
    esac

    # a reload with a broken config is ignored silently, a restart would take every site down
    # retried since aardvark-dns briefly fails lookups right after a container is created or removed
    tries=5
    until out=$(podman_ctx "$context" exec "$ws" "${check[@]}" 2>&1) || (( --tries <= 0 )); do
        sleep 1
    done
    if (( tries <= 0 )); then
        echo "$ws config test failed, still running the previous config:" >&2
        echo "$out" >&2
        return 1
    fi
    podman_ctx "$context" kill --signal "$signal" "$ws" >/dev/null 2>&1 \
        || podman_ctx "$context" restart "$ws" >/dev/null 2>&1
}

# sed for a file another container sees through a single-file bind mount: sed -i swaps the inode and the container keeps reading the old file until it restarts, e.g. sed_in_place <file> <sed-args...>
sed_in_place() {
    local file="$1" tmp; shift
    tmp=$(mktemp) || return 1
    if sed "$@" "$file" > "$tmp"; then
        cat "$tmp" > "$file"
    else
        rm -f "$tmp"
        return 1
    fi
    rm -f "$tmp"
}

# applies caddy config changes without dropping connections, a restart takes every site on the server down and its stop can hang; an invalid config is reported and caddy keeps the running one, a stopped caddy gets started -- e.g. caddy_reload
caddy_reload() {
    if ! podman_is_running caddy; then
        (cd /root && podman-compose up -d caddy) >/dev/null 2>&1
        return
    fi
    local out
    if ! out=$(podman exec caddy caddy validate --config /etc/caddy/Caddyfile 2>&1); then
        echo "Caddy config is invalid, kept the running one: $(grep -m1 -i error <<< "$out")" >&2
        return 1
    fi
    podman exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1 || podman restart caddy >/dev/null 2>&1
}

# echoes the shared image store root and users read through additionalimagestores in root's storage.conf, servers installed with 2.0.0-beta have it at /var/lib/openpanel/shared-containers/storage
shared_image_store() {
    local store
    store=$(awk '/^[[:space:]]*additionalimagestores/ { f = 1 } f && match($0, /"[^"]+"/) { print substr($0, RSTART + 1, RLENGTH - 2); exit } f && /\]/ { exit }' /etc/containers/storage.conf 2>/dev/null)
    echo "${store:-/var/lib/containers/shared-storage}"
}

# a write to the shared store leaves images.json and new layers readable by root only, users' podman then fails to start and its socket gives up (trigger-limit-hit)
fix_shared_perms() {
    local store="${SHARED_STORE:-$(shared_image_store)}"
    chmod -R o+rX "$store" 2>/dev/null || true
    find "$store" -name '*.lock' -exec chmod o+rw {} + 2>/dev/null || true
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
