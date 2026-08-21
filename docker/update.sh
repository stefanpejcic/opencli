#!/bin/bash
################################################################################
# Script Name: update.sh
# Description: Check for image updates across root's stack and every
#              non-suspended user's stack, print what's available, and
#              optionally pull + recreate (down/up) the affected services.
#              Every image is pulled at most once, into the shared image
#              store that root and every user's rootless podman already
#              reads from (additionalimagestores) - see user/add.sh and
#              PODMAN_INSTALL.sh - so N stacks referencing the same image
#              never trigger N pulls.
# Usage: opencli docker-update [-y|--yes] [--dry-run]
# Author: Stefan Pejcic
# Created: 20.08.2026
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

readonly LOG_DIR="/var/log/openpanel/admin/docker"
readonly LOG_FILE="${LOG_DIR}/$(date '+%Y-%m-%d').log"
readonly SHARED_STORE="/var/lib/containers/shared-storage"
mkdir -p "$LOG_DIR"

# shellcheck disable=SC1091
. /usr/local/opencli/lib/podman.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/requirement.sh
require_command skopeo
require_command jq

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') : $1" | tee -a "$LOG_FILE"
}

usage() {
    echo "Usage: opencli docker-update [options]"
    echo ""
    echo "Options:"
    echo "  -y, --yes       Apply updates without prompting for confirmation."
    echo "  --dry-run       Only check and print available updates, never prompt or apply."
    echo ""
    echo "With no options, available updates are printed and confirmation is asked"
    echo "for 10 seconds - if there's no answer in that time the script exits"
    echo "without changing anything."
    exit 1
}

AUTO_YES=false
DRY_RUN=false
for arg in "$@"; do
    case "$arg" in
        -y|--yes) AUTO_YES=true ;;
        --dry-run) DRY_RUN=true ;;
        -h|--help) usage ;;
        *) echo "Unknown option: $arg"; usage ;;
    esac
done

# awk that turns a `podman-compose config` (or raw compose) stream into
# "service image" pairs. Tracks 2-space service headers, turns off inside
# top-level networks/volumes/configs/secrets blocks.
read -r -d '' PARSE_COMPOSE <<'AWK'
BEGIN { s=1 }
/^services:[[:space:]]*$/ { s=1; next }
/^(networks|volumes|configs|secrets):[[:space:]]*$/ { s=0; next }
s && /^  [A-Za-z0-9._-]+:[[:space:]]*$/ { svc=$1; sub(/:$/,"",svc); next }
s && /image:[[:space:]]/ {
    line=$0
    sub(/.*image:[[:space:]]*/,"",line)
    gsub(/["'\'']/,"",line)
    sub(/[[:space:]].*$/,"",line)
    if (svc!="") print svc, line
}
AWK

# echoes "service image" pairs actually deployed for <context>, resolving
# ${VAR} placeholders via podman-compose config, falling back to a raw parse
# (which may leave compose-default tags) if that produces nothing.
get_context_images() {
    local context="$1" compose_file="$2"
    local pairs
    pairs=$(podman_compose_ctx "$context" -f "$compose_file" config 2>/dev/null | awk "$PARSE_COMPOSE")
    if [[ -z "$pairs" ]]; then
        pairs=$(sed -E 's/\$\{[A-Za-z0-9_]+:-([^}]*)\}/\1/g' "$compose_file" | awk "$PARSE_COMPOSE")
    fi
    echo "$pairs"
}

# echoes the manifest digest (sha256:...) of <image> as currently seen in
# the shared store, or nothing if it isn't pulled at all. Root's and every
# user's podman read this store via additionalimagestores, so checking it
# once here is equivalent to checking every context individually.
local_digest() {
    local image="$1"
    podman --root "$SHARED_STORE" image inspect --format '{{.Digest}}' "$image" 2>/dev/null
}

# echoes the manifest digest (sha256:...) that <image> currently resolves to
# in its registry, without pulling it.
remote_digest() {
    local image="$1"
    skopeo inspect --format '{{.Digest}}' "docker://${image}" 2>/dev/null
}

# --- A: enumerate every image in use, root first then non-suspended users --

# ordered list of contexts ("root", then each username/context)
declare -a CONTEXTS=("root")
declare -A CONTEXT_LABEL=([root]="root")

mapfile -t USER_CONTEXTS < <(opencli user-list --json 2>/dev/null | jq -r '.data[] | select(.username | startswith("SUSPENDED_") | not) | .context')
for context in "${USER_CONTEXTS[@]}"; do
    [[ -z "$context" ]] && continue
    CONTEXTS+=("$context")
    CONTEXT_LABEL["$context"]="user: $context"
done

# images used by <context>, space-separated (deduped)
declare -A CONTEXT_IMAGES=()
# every unique image across all contexts -> 1
declare -A ALL_IMAGES=()

for context in "${CONTEXTS[@]}"; do
    compose_file=$(podman_compose_file "$context")
    compose_dir=$(dirname "$compose_file")
    [[ -f "$compose_file" ]] || continue

    pairs=$(cd "$compose_dir" && get_context_images "$context" "$compose_file")
    [[ -z "$pairs" ]] && continue

    declare -A seen=()
    while IFS= read -r pair; do
        [[ -z "$pair" ]] && continue
        img="${pair#* }"
        [[ -z "$img" || "$img" == *'${'* ]] && continue
        seen["$img"]=1
        ALL_IMAGES["$img"]=1
    done <<< "$pairs"
    CONTEXT_IMAGES["$context"]="${!seen[*]}"
    unset seen
done

if [[ ${#ALL_IMAGES[@]} -eq 0 ]]; then
    echo "No compose-managed images found."
    exit 0
fi

# --- B: check each unique image once for an available update --------------

# images with an update available (missing locally, or digest mismatch) -> 1
declare -A UPDATE_IMAGES=()

echo "Checking ${#ALL_IMAGES[@]} unique image(s)..."
for img in "${!ALL_IMAGES[@]}"; do
    local_d=$(local_digest "$img")
    remote_d=$(remote_digest "$img")

    if [[ -z "$remote_d" ]]; then
        echo "  ?  $img - could not reach registry, skipping"
        continue
    fi

    if [[ -z "$local_d" ]]; then
        echo "  +  $img - not pulled yet"
        UPDATE_IMAGES["$img"]=1
    elif [[ "$local_d" != "$remote_d" ]]; then
        echo "  ^  $img - update available"
        UPDATE_IMAGES["$img"]=1
    fi
done
echo

if [[ ${#UPDATE_IMAGES[@]} -eq 0 ]]; then
    echo "All images are up to date."
    exit 0
fi

# contexts that actually reference at least one outdated image
declare -a CONTEXTS_TO_UPDATE=()
for context in "${CONTEXTS[@]}"; do
    for img in ${CONTEXT_IMAGES[$context]:-}; do
        if [[ -n "${UPDATE_IMAGES[$img]:-}" ]]; then
            CONTEXTS_TO_UPDATE+=("$context")
            break
        fi
    done
done

echo "${#UPDATE_IMAGES[@]} image(s) have an update available, affecting ${#CONTEXTS_TO_UPDATE[@]} context(s):"
for context in "${CONTEXTS_TO_UPDATE[@]}"; do
    echo "  - ${CONTEXT_LABEL[$context]}"
done
echo

if [[ "$DRY_RUN" == true ]]; then
    echo "Dry run - not pulling or restarting anything."
    exit 0
fi

# --- C: apply, if approved -------------------------------------------------

if [[ "$AUTO_YES" == false ]]; then
    read -r -t 10 -p "Apply these updates now? [y/N] (auto-cancel in 10s): " answer
    ret=$?
    echo
    if [[ $ret -ne 0 ]]; then
        echo "No response within 10 seconds - exiting without applying updates."
        exit 0
    fi
    if [[ ! "$answer" =~ ^[Yy]$ ]]; then
        echo "Aborted - not applying updates."
        exit 0
    fi
fi

log "=== Update run started ==="

# images that failed to pull -> 1
declare -A FAILED_IMAGES=()
# contexts that had at least one failure (pull or recreate) -> 1
declare -A FAILED_CONTEXTS=()

# pull each outdated image exactly once, into the shared store
for img in "${!UPDATE_IMAGES[@]}"; do
    log "pull $img"
    if ! podman --root "$SHARED_STORE" pull "$img" >/dev/null 2>&1; then
        log "FAILED to pull $img"
        FAILED_IMAGES["$img"]=1
    fi
done
chmod -R o+rX "$SHARED_STORE" 2>/dev/null || true

# recreate (down/up) only the stacks that reference an updated image
for context in "${CONTEXTS_TO_UPDATE[@]}"; do
    label="${CONTEXT_LABEL[$context]}"
    compose_file=$(podman_compose_file "$context")
    compose_dir=$(dirname "$compose_file")

    context_images=(${CONTEXT_IMAGES[$context]:-})
    needed=()
    all_failed=true
    for img in "${context_images[@]}"; do
        [[ -n "${UPDATE_IMAGES[$img]:-}" ]] || continue
        needed+=("$img")
        [[ -n "${FAILED_IMAGES[$img]:-}" ]] || all_failed=false
    done

    if [[ "$all_failed" == true ]]; then
        log "skipping recreate for $label - every image it needed failed to pull"
        FAILED_CONTEXTS["$context"]=1
        continue
    fi

    log "recreating stack for $label (down/up)"
    if ! (cd "$compose_dir" && podman_compose_ctx "$context" down) >/dev/null 2>&1; then
        log "  WARNING: 'down' reported an error for $label (continuing to 'up')"
    fi
    if (cd "$compose_dir" && podman_compose_ctx "$context" up -d) >/dev/null 2>&1; then
        log "  $label is back up"
        # the stack is up, but any service still on a failed-pull image
        # didn't actually get updated - still worth flagging
        for img in "${needed[@]}"; do
            if [[ -n "${FAILED_IMAGES[$img]:-}" ]]; then
                log "  WARNING: $label - $img failed to pull, service left on old image"
                FAILED_CONTEXTS["$context"]=1
            fi
        done
    else
        log "  ERROR: failed to bring $label back up - check manually"
        FAILED_CONTEXTS["$context"]=1
    fi
done

log "=== Update run finished ==="

if [[ ${#FAILED_IMAGES[@]} -gt 0 || ${#FAILED_CONTEXTS[@]} -gt 0 ]]; then
    failed_users=${#FAILED_CONTEXTS[@]}
    failed_containers=${#FAILED_IMAGES[@]}
    log "FAILED: $failed_containers image(s) failed to pull, $failed_users context(s) affected - see $LOG_FILE"

    nohup opencli sentinel --action=docker_update \
        --title="Docker Image Update failed" \
        --message="Update failed for ${failed_users} users and ${failed_containers} containers. Detailed report was saved in: ${LOG_FILE}" \
        >/dev/null 2>&1 &
    disown
fi
