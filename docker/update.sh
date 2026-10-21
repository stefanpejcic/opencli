#!/bin/bash
################################################################################
# Script Name: update.sh
# Description: Check for image updates across root's stack and every
#              non-suspended user's stack, print what's available, and
#              optionally pull and recreate the running containers on an
#              outdated image, nothing else in a stack is stopped or started.
#              Every image is pulled at most once, into the shared image
#              store that root and every user's rootless podman already
#              reads from (additionalimagestores) - see user/add.sh and
#              PODMAN_INSTALL.sh - so N stacks referencing the same image
#              never trigger N pulls. Root's own stack uses root's storage.
#              Given an image, only that image is pulled and only the running
#              containers that use it are recreated, php-fpm ones also get
#              their user's webserver reloaded.
# Usage: opencli docker-update [IMAGE] [-y|--yes] [--dry-run]
# Author: Stefan Pejcic
# Created: 20.08.2026
# Last Modified: 21.10.2026
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
    echo "Usage: opencli docker-update [IMAGE] [options]"
    echo ""
    echo "Options:"
    echo "  -y, --yes       Apply updates without prompting for confirmation."
    echo "  --dry-run       Only check and print available updates, never prompt or apply."
    echo ""
    echo "With no options, available updates are printed and confirmation is asked"
    echo "for 10 seconds - if there's no answer in that time the script exits"
    echo "without changing anything."
    echo ""
    echo "Only running containers on an outdated image are recreated, stacks are never"
    echo "taken down. With IMAGE (e.g. shinsenter/php:7.4-fpm) only that image is checked."
    echo "After a php-fpm container is recreated the user's webserver is reloaded so it"
    echo "connects to the new container."
    exit 1
}

AUTO_YES=false
DRY_RUN=false
TARGET_IMAGE=""
for arg in "$@"; do
    case "$arg" in
        -y|--yes) AUTO_YES=true ;;
        --dry-run) DRY_RUN=true ;;
        -h|--help) usage ;;
        -*) echo "Unknown option: $arg"; usage ;;
        *)
            [[ -n "$TARGET_IMAGE" ]] && { echo "Only one image can be given."; usage; }
            TARGET_IMAGE="$arg"
            ;;
    esac
done

# awk turns a podman-compose config stream into "service image container" lines, tracks 2-space service headers, off inside top-level networks/volumes/configs/secrets blocks
read -r -d '' PARSE_COMPOSE <<'AWK'
function flush() { if (svc != "" && img != "") print svc, img, (cn != "" ? cn : svc); svc = ""; img = ""; cn = "" }
function value(line) { sub(/^[^:]*:[[:space:]]*/, "", line); gsub(/["'\'']/, "", line); sub(/[[:space:]].*$/, "", line); return line }
BEGIN { s=1 }
/^services:[[:space:]]*$/ { flush(); s=1; next }
/^(networks|volumes|configs|secrets):[[:space:]]*$/ { flush(); s=0; next }
s && /^  [A-Za-z0-9._-]+:[[:space:]]*$/ { flush(); svc=$1; sub(/:$/,"",svc); next }
s && /^    image:[[:space:]]/ { img=value($0) }
s && /^    container_name:[[:space:]]/ { cn=value($0) }
END { flush() }
AWK

# echoes "service image container" lines deployed for <context>, resolving ${VAR} via podman-compose config, falling back to a raw parse if that's empty
get_context_images() {
    local context="$1" compose_file="$2"
    local pairs
    pairs=$(podman_compose_ctx "$context" -f "$compose_file" config 2>/dev/null | awk "$PARSE_COMPOSE")
    if [[ -z "$pairs" ]]; then
        pairs=$(sed -E 's/\$\{[A-Za-z0-9_]+:-([^}]*)\}/\1/g' "$compose_file" | awk "$PARSE_COMPOSE")
    fi
    echo "$pairs"
}

# root's own stack uses root's storage, its copy there wins over the shared one; user stacks read the shared store
store_of() {
    [[ "$1" == root ]] && echo root || echo shared
}

store_podman() {
    local store="$1"; shift
    if [[ "$store" == shared ]]; then
        podman --root "$SHARED_STORE" "$@"
    else
        podman "$@"
    fi
}

# id of <image> in <store>, empty if not pulled
image_id() {
    store_podman "$1" image inspect --format '{{.Id}}' "$2" 2>/dev/null
}

# true if <store> already has <digest> for <image>, .Digest is only one of the digests it was pulled under (index or platform manifest) so all RepoDigests are checked
has_digest() {
    local store="$1" image="$2" digest="$3"
    [[ -n "$digest" ]] || return 1
    store_podman "$store" image inspect --format '{{.Digest}} {{range .RepoDigests}}{{.}} {{end}}' "$image" 2>/dev/null | tr ' ' '\n' | grep -q -- "${digest}$"
}

# echoes what digest <image> currently resolves to in its registry, without pulling it
remote_digest() {
    local image="$1"
    skopeo inspect --format '{{.Digest}}' "docker://${image}" 2>/dev/null
}

# shinsenter/php:7.4-fpm and docker.io/shinsenter/php:7.4-fpm are the same image
normalize_image() {
    local image="${1#docker.io/}"
    image="${image#library/}"
    [[ "${image##*/}" == *[:@]* ]] || image="$image:latest"
    echo "$image"
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

# normalized image -> the name the compose files use
declare -A ALL_IMAGES=()
# normalized image -> "context service container" lines that use it
declare -A IMAGE_USERS=()
# "context/container" -> state and image id, one podman ps per context
declare -A CONTAINER_STATE=() CONTAINER_IMAGE=()

for context in "${CONTEXTS[@]}"; do
    compose_file=$(podman_compose_file "$context")
    compose_dir=$(dirname "$compose_file")
    [[ -f "$compose_file" ]] || continue

    pairs=$(cd "$compose_dir" && get_context_images "$context" "$compose_file")
    [[ -z "$pairs" ]] && continue
    while read -r cname state image_id; do
        CONTAINER_STATE["$context/$cname"]="$state"
        CONTAINER_IMAGE["$context/$cname"]="$image_id"
    done < <(podman_ctx "$context" ps -a --no-trunc --format '{{.Names}} {{.State}} {{.ImageID}}' 2>/dev/null)
    while read -r svc img cname; do
        [[ -z "$img" || "$img" == *'${'* ]] && continue
        key=$(normalize_image "$img")
        ALL_IMAGES["$key"]="$img"
        IMAGE_USERS["$key"]+="$context $svc $cname"$'\n'
    done <<< "$pairs"
done

if [[ -n "$TARGET_IMAGE" ]]; then
    want=$(normalize_image "$TARGET_IMAGE")
    if [[ -z "${ALL_IMAGES[$want]:-}" ]]; then
        echo "No stack uses $TARGET_IMAGE."
        exit 0
    fi
    declare -A TARGETS=(["$want"]="${ALL_IMAGES[$want]}")
elif [[ ${#ALL_IMAGES[@]} -eq 0 ]]; then
    echo "No compose-managed images found."
    exit 0
else
    declare -A TARGETS=()
    for key in "${!ALL_IMAGES[@]}"; do TARGETS["$key"]="${ALL_IMAGES[$key]}"; done
fi

# --- B: plan, per image and store -----------------------------------------

# "store image" to pull
declare -a PLAN_PULL=()
# "context service container store image" to recreate
declare -a PLAN_RECREATE=()

# plans one image: which stores get it pulled and which running containers get recreated, only containers that were already running are touched
plan_image() {
    local want="$1" image="$2"
    local -a running=() current_ok=() stopped=() todo=()
    local -A stores=() pull_store=()
    local context svc cname store id current remote_d entry note

    while read -r context svc cname; do
        [[ -z "$svc" ]] && continue
        store=$(store_of "$context")
        stores["$store"]=1
        if [[ "${CONTAINER_STATE[$context/$cname]:-}" == "running" ]]; then
            running+=("$context $svc $cname $store")
        else
            stopped+=("$context $svc")
        fi
    done <<< "${IMAGE_USERS[$want]}"

    remote_d=$(remote_digest "$image")
    for store in "${!stores[@]}"; do
        id=$(image_id "$store" "$image")
        if [[ -z "$remote_d" ]]; then
            continue
        elif [[ -z "$id" ]]; then
            # never pulled and nothing runs it, it's pulled whenever a service using it first starts
            for entry in "${running[@]}"; do
                [[ "$entry" == *" $store" ]] && { pull_store["$store"]=1; break; }
            done
        elif ! has_digest "$store" "$image" "$remote_d"; then
            pull_store["$store"]=1
        fi
    done

    for entry in "${running[@]}"; do
        read -r context svc cname store <<< "$entry"
        current="${CONTAINER_IMAGE[$context/$cname]:-}"
        if [[ -n "${pull_store[$store]:-}" || "$current" != "$(image_id "$store" "$image")" ]]; then
            todo+=("$entry")
        else
            current_ok+=("$context $svc")
        fi
    done

    # nothing to pull or recreate, only worth a line in single image mode
    if [[ ${#pull_store[@]} -eq 0 && ${#todo[@]} -eq 0 && -z "$TARGET_IMAGE" ]]; then
        [[ -z "$remote_d" ]] && echo "  ?  $image - could not reach registry, skipping"
        return 0
    fi

    echo "$image"
    echo "  registry: ${remote_d:-could not reach registry}"
    for store in "${!stores[@]}"; do
        if [[ -n "${pull_store[$store]:-}" ]]; then
            note="newer image available, will be pulled"
            [[ -z "$(image_id "$store" "$image")" ]] && note="not pulled yet, will be pulled"
        elif [[ -z "$(image_id "$store" "$image")" ]]; then
            note="not pulled, nothing runs it"
        else
            note="up to date"
        fi
        echo "  ${store} store: $note"
        [[ -n "${pull_store[$store]:-}" ]] && PLAN_PULL+=("$store $image")
    done
    for entry in "${todo[@]}"; do
        read -r context svc cname store <<< "$entry"
        PLAN_RECREATE+=("$context $svc $cname $store $image")
        if [[ "$svc" == php-fpm-* && "$context" != root ]]; then
            echo "  recreate: ${CONTEXT_LABEL[$context]}: $svc, then reload its webserver"
        else
            echo "  recreate: ${CONTEXT_LABEL[$context]}: $svc"
        fi
    done
    for entry in "${current_ok[@]}"; do
        read -r context svc <<< "$entry"
        echo "  already current: ${CONTEXT_LABEL[$context]}: $svc"
    done
    for entry in "${stopped[@]}"; do
        read -r context svc <<< "$entry"
        echo "  not running, left alone: ${CONTEXT_LABEL[$context]}: $svc"
    done
    echo
}

if [[ -z "$TARGET_IMAGE" ]]; then
    echo "Checking ${#TARGETS[@]} unique image(s)..."
    echo
fi
for key in "${!TARGETS[@]}"; do
    plan_image "$key" "${TARGETS[$key]}"
done

if [[ ${#PLAN_PULL[@]} -eq 0 && ${#PLAN_RECREATE[@]} -eq 0 ]]; then
    echo "Nothing to do, every running container already uses the current image."
    exit 0
fi
echo "${#PLAN_PULL[@]} pull(s), ${#PLAN_RECREATE[@]} container(s) to recreate."

if [[ "$DRY_RUN" == true ]]; then
    echo "Dry run - not pulling or recreating anything."
    exit 0
fi

# --- C: apply, if approved -------------------------------------------------

if [[ "$AUTO_YES" == false ]]; then
    read -r -t 10 -p "Apply now? [y/N] (auto-cancel in 10s): " answer
    ret=$?
    echo
    if [[ $ret -ne 0 ]]; then
        echo "No response within 10 seconds - exiting without changing anything."
        exit 0
    fi
    if [[ ! "$answer" =~ ^[Yy]$ ]]; then
        echo "Aborted - nothing changed."
        exit 0
    fi
fi

# waits until php-fpm in the recreated container listens on 9000, a webserver reloaded before that gets connection refused
wait_fpm_ready() {
    local context="$1" container="$2" tries=60
    while (( tries-- > 0 )); do
        if podman_ctx "$context" exec "$container" sh -c "awk '\$2 ~ /:2328\$/ && \$4 == \"0A\"' /proc/net/tcp /proc/net/tcp6 2>/dev/null | grep -q ." >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    return 1
}

# reloads <context>'s webserver so it resolves the recreated php-fpm container's new address, a signal to its pid 1 instead of a restart so open connections finish
reload_webserver() {
    local context="$1" ws signal
    ws=$(grep "^WEB_SERVER=" "/home/$context/.env" 2>/dev/null | cut -d= -f2- | tr -d '"[:space:]' | grep -Eo 'nginx|openresty|apache|litespeed' | head -n1)
    case "$ws" in
        nginx|openresty) signal=HUP ;;
        apache) signal=USR1 ;; # graceful
        *) return 0 ;; # litespeed runs lsphp itself
    esac
    if [[ "$(podman_ctx "$context" inspect -f '{{.State.Status}}' "$ws" 2>/dev/null)" != "running" ]]; then
        log "  $ws is not running for ${CONTEXT_LABEL[$context]}, nothing to reload"
        return 0
    fi
    if podman_ctx "$context" kill --signal "$signal" "$ws" >/dev/null 2>&1; then
        log "  reloaded $ws ($signal) for ${CONTEXT_LABEL[$context]}"
    else
        log "  ERROR: could not reload $ws for ${CONTEXT_LABEL[$context]}"
        return 1
    fi
}

log "=== Update run started ==="
declare -A FAILED_PULL=()
failed=0

for entry in "${PLAN_PULL[@]}"; do
    read -r store image <<< "$entry"
    log "pull $image into the $store store"
    if ! store_podman "$store" pull "$image" >/dev/null 2>&1; then
        log "FAILED to pull $image into the $store store"
        FAILED_PULL["$store $image"]=1
        failed=$((failed + 1))
    fi
done
chmod -R o+rX "$SHARED_STORE" 2>/dev/null || true

for entry in "${PLAN_RECREATE[@]}"; do
    read -r context svc cname store image <<< "$entry"
    label="${CONTEXT_LABEL[$context]}"
    if [[ -n "${FAILED_PULL[$store $image]:-}" ]]; then
        log "skipping $svc for $label, $image failed to pull"
        continue
    fi
    compose_dir=$(dirname "$(podman_compose_file "$context")")
    log "recreating $svc for $label"
    if ! (cd "$compose_dir" && podman_compose_ctx "$context" up -d --no-deps --force-recreate "$svc") >/dev/null 2>&1; then
        log "  ERROR: failed to recreate $svc for $label - check manually"
        failed=$((failed + 1))
        continue
    fi
    expected=$(image_id "$store" "$image")
    current=$(podman_ctx "$context" inspect -f '{{.Image}}' "$cname" 2>/dev/null)
    # a copy of the image in the user's own storage wins over the shared store
    if [[ -n "$expected" && "$current" != "$expected" ]]; then
        log "  WARNING: $svc for $label still runs ${current:0:12}, not ${expected:0:12} - it has its own copy of $image"
        failed=$((failed + 1))
    fi
    if [[ "$svc" == php-fpm-* && "$context" != root ]]; then
        if ! wait_fpm_ready "$context" "$cname"; then
            log "  WARNING: $svc for $label is not listening on 9000 after 2 minutes, reloading the webserver anyway"
            failed=$((failed + 1))
        fi
        reload_webserver "$context" || failed=$((failed + 1))
    fi
done

log "=== Update run finished ==="

if [[ $failed -gt 0 ]]; then
    log "FAILED: $failed problem(s) during the update - see $LOG_FILE"
    nohup opencli sentinel --action=docker_update \
        --title="Docker Image Update failed" \
        --message="Docker image update had ${failed} problem(s). Detailed report was saved in: ${LOG_FILE}" \
        >/dev/null 2>&1 &
    disown
    exit 1
fi
