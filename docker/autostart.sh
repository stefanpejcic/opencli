#!/bin/bash
################################################################################
# Script Name: autostart.sh
# Description: Set services to auto-start for user on acocunt creation.
# Usage: opencli docker-autostart
# Author: Stefan Pejcic
# Created: 14.05.2026
# Last Modified: 09.07.2026
# Company: openpanel.com
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

COMPOSE_DIR="/etc/openpanel/docker/compose/1.0"
COMPOSE_FILE="${COMPOSE_DIR}/docker-compose.yml"
AUTOSTART_FILE="${COMPOSE_DIR}/autostart.services"
SHARED_STORE="/var/lib/openpanel/shared-containers/storage"
FORCE=false
[[ "$1" == "-f" || "$1" == "--force" ]] && FORCE=true
# disk check (free space on /), skippable with -f/--force
disk_mb=$(( $(df / --output=avail | tail -1) / 1024 ))
if [[ "$FORCE" == true ]]; then
    echo "Force flag set — skipping disk check (free: $(( disk_mb / 1024 ))GB)."
elif (( disk_mb <= 81920 )); then
    echo "Root disk free is $(( disk_mb / 1024 ))GB (<=80GB) — skipping prefetch."
    echo "Re-run with -f or --force to prefetch anyway."
    exit 0
else
    echo "Root disk free is $(( disk_mb / 1024 ))GB — proceeding with prefetch."
fi
[[ -f "$COMPOSE_FILE" ]]   || { echo "compose file not found: $COMPOSE_FILE"; exit 1; }
[[ -f "$AUTOSTART_FILE" ]] || { echo "autostart file not found: $AUTOSTART_FILE"; exit 1; }
mkdir -p "$SHARED_STORE"
cd "$COMPOSE_DIR" || exit 1
# awk that emits "service image" pairs. Tracks 2-space service headers,
# turns off inside top-level networks/volumes/configs/secrets blocks.
read -r -d '' PARSE <<'AWK'
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
# primary: podman-compose config (resolves ${VAR} from adjacent .env)
mapfile -t pairs < <(podman-compose -f "$COMPOSE_FILE" config 2>/dev/null | awk "$PARSE")
# fallback: raw file with ${VAR:-default} -> default (may give compose-default tags)
if (( ${#pairs[@]} == 0 )); then
    echo "compose config produced nothing — falling back to raw parse (tags may be compose defaults)."
    mapfile -t pairs < <(sed -E 's/\$\{[A-Za-z0-9_]+:-([^}]*)\}/\1/g' "$COMPOSE_FILE" | awk "$PARSE")
fi
declare -A SVC_IMAGE
for p in "${pairs[@]}"; do
    svc="${p%% *}"; img="${p#* }"
    [[ -z "$svc" || -z "$img" || "$img" == *'${'* ]] && continue
    SVC_IMAGE["$svc"]="$img"
done
# autostart service set (strip comments/blanks/whitespace)
declare -A AUTO
while IFS= read -r line; do
    line="$(echo "$line" | sed 's/#.*//; s/[[:space:]]//g')"
    [[ -n "$line" ]] && AUTO["$line"]=1
done < "$AUTOSTART_FILE"
echo
echo "Autostart services: ${!AUTO[*]}"
echo "Compose services with images: ${#SVC_IMAGE[@]}"
echo
# iterate compose services in sorted order; pull only if in autostart
mapfile -t sorted < <(printf '%s\n' "${!SVC_IMAGE[@]}" | sort)
for svc in "${sorted[@]}"; do
    img="${SVC_IMAGE[$svc]}"
    if [[ -z "${AUTO[$svc]}" ]]; then
        echo "skip   $svc (not in autostart)"
        continue
    fi
    echo -n "pull   $svc -> $img ... "
    if podman --root "$SHARED_STORE" pull --policy always "$img" >/dev/null 2>&1; then
        echo "OK"
    else
        echo "FAIL"
    fi
done
# autostart entries that have no image in compose (typos / imageless services)
for svc in "${!AUTO[@]}"; do
    [[ -z "${SVC_IMAGE[$svc]}" ]] && echo "note   '$svc' in autostart but no image found in compose"
done
echo
echo "Fixing permissions on $SHARED_STORE ..."
chmod -R o+rX "$SHARED_STORE"
find "$SHARED_STORE" -name '*.lock' -exec chmod o+rw {} \; 2>/dev/null || true
echo "Done."
echo
echo "Store contents:"
podman --root "$SHARED_STORE" images
echo
size_h="$(du -sh "$SHARED_STORE" 2>/dev/null | cut -f1)"
size_gb="$(du -sm "$SHARED_STORE" 2>/dev/null | cut -f1 | awk '{printf "%.2f", $1/1024}')"
echo "Shared store disk usage: ${size_h} (${size_gb} GB) at ${SHARED_STORE}"
