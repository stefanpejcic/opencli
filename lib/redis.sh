#!/bin/bash
# ======================================================================
# Shared Redis cache-invalidation helpers, sourced by scripts that mutate
# data the OpenPanel UI (Go, internal/core/cache) has memoized in Redis.
#
# NEVER FLUSHALL or wildcard-DEL 'openpanel_cache_*' from a script. That
# nukes every memoized function for every user in one shot, and FLUSHALL
# additionally wipes login sessions and rate-limiter state that live in
# the same Redis instance. Always target the specific key(s) whose
# underlying data actually changed.
#
# Keys are openpanel_cache_<key>, where <key> is the string passed to
# cache.Memoize in the panel, usually "<func>:<username|id|context>",
# e.g. openpanel_cache_get_user_details_with_plan:12
#
# This file only defines functions - it has no top-level logic/exit, so
# it is safe to `source` from any script.
# ======================================================================

REDIS_CONTAINER="openpanel_redis"

# Runs a redis-cli command inside the redis container.
redis_cli() {
    podman exec "$REDIS_CONTAINER" redis-cli "$@"
}

# Deletes one or more exact keys. No-op if called with no args.
redis_drop_key() {
    [ "$#" -eq 0 ] && return 0
    redis_cli DEL "$@" >/dev/null 2>&1 || true
}

# deletes the go panel's cache keys matching each pattern, e.g. redis_drop_pattern "openpanel_cache_load_user_features:john:*" - keep patterns targeted, never openpanel_cache_*
redis_drop_pattern() {
    local pattern key
    for pattern in "$@"; do
        redis_cli --scan --pattern "$pattern" 2>/dev/null | while IFS= read -r key; do
            [ -n "$key" ] && redis_cli UNLINK "$key" >/dev/null 2>&1
        done || true
    done
}

# drops the go panel's long-lived per-user cache (plan, email, 2fa, feature set, context) - pass the db user id and every username it was cached under
redis_drop_user_cache() {
    local user_id="$1" u keys=()
    shift
    if [ -n "$user_id" ]; then
        keys+=( "openpanel_cache_get_user_details_with_plan:${user_id}" "openpanel_cache_get_2fa_status_for_user:${user_id}" )
    fi
    for u in "$@"; do
        [ -z "$u" ] && continue
        keys+=( "openpanel_cache_get_feature_set_on_plan:${u}" "openpanel_cache_query_context_by_username:${u}" "openpanel_cache_get_uid:${u}" )
        redis_drop_pattern "openpanel_cache_load_user_features:${u}:*"
    done
    redis_drop_key "${keys[@]}"
}

# Terminates all active sessions for a given user id (forces re-login),
# used when a user's password/IP/account is changed or removed.
redis_drop_user_sessions() {
    local user_id="$1"
    [ -z "$user_id" ] && return 0

    local session_keys
    session_keys=$(redis_cli --scan --pattern "session:${user_id}:*")
    [ -z "$session_keys" ] && return 0

    local key
    while IFS= read -r key; do
        [ -n "$key" ] && redis_cli UNLINK "$key" >/dev/null 2>&1
    done <<< "$session_keys"
}
