#!/bin/bash
################################################################################
# Script Name: lib/notifications.sh
# Description: Shared helpers for reading and writing OpenAdmin notifications.
# Usage: . /usr/local/opencli/lib/notifications.sh
# Author: Stefan Pejcic
# Created: 24.09.2026
# Last Modified: 24.09.2026
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

# One JSON object per line:
# {"id","time","last_seen","count","status":"unread|read","severity":"critical|warning|info",
#  "category","source","title","message","details":{...},"resolved_at"}
# Lines that aren't JSON (written before 2.0.12) are left untouched.

# shellcheck disable=SC1091
. /usr/local/opencli/lib/requirement.sh

NOTIFICATIONS_LOG="/var/log/openpanel/admin/notifications.log"
NOTIFICATIONS_LOCK="${NOTIFICATIONS_LOG}.lock"

require_command jq

# jq prelude that turns each raw line into an object, or null for old-format lines
_NOTIF_PARSE='. as $raw | (try fromjson catch null) as $o'

_notifications_init() {
  mkdir -p "$(dirname "$NOTIFICATIONS_LOG")"
  [[ -f "$NOTIFICATIONS_LOG" ]] || : > "$NOTIFICATIONS_LOG"
}

# prints how many entries match a jq condition, extra args go to jq (e.g. --arg t "$title")
_notifications_count() {
  local cond="$1"; shift
  jq -nR "$@" "[inputs | $_NOTIF_PARSE | select((\$o|type) == \"object\") | \$o | select($cond)] | length" "$NOTIFICATIONS_LOG" 2>/dev/null || echo 0
}

# applies a jq update to entries matching a condition, old-format lines pass through; caller holds the lock
_notifications_update() {
  local cond="$1" update="$2"; shift 2
  local tmp; tmp=$(mktemp /tmp/notifications.XXXXXX) || return 1
  # cat instead of mv so the log keeps its inode and permissions
  if jq -rR "$@" "$_NOTIF_PARSE | if (\$o|type) == \"object\" then (\$o | if ($cond) then ($update) else . end | tojson) else \$raw end" "$NOTIFICATIONS_LOG" > "$tmp"; then
    cat "$tmp" > "$NOTIFICATIONS_LOG"
  fi
  rm -f "$tmp"
}

notification_is_unread() {
  _notifications_init
  (( $(_notifications_count '.status == "unread" and .title == $t' --arg t "$1") > 0 ))
}

# adds an entry, or with dedup=yes bumps count/last_seen of an unread entry with the same title
# returns 0 when a new entry was added, 1 when an existing one was bumped
# usage: notification_add <dedup yes|no> <status> <severity> <category> <source> <title> <message> [details json]
notification_add() {
  local dedup="$1" status="$2" severity="$3" category="$4" source="$5" title="$6" message="$7" details="${8:-null}"
  local now; now=$(date '+%Y-%m-%d %H:%M:%S')
  local id; id="$(date +%s)$(od -An -N3 -tx1 /dev/urandom | tr -d ' \n')"
  jq -e . >/dev/null 2>&1 <<< "$details" || details=null
  _notifications_init
  local added=0
  {
    flock -x 200
    if [[ "$dedup" == "yes" ]] && (( $(_notifications_count '.status == "unread" and .title == $t' --arg t "$title") > 0 )); then
      _notifications_update '.status == "unread" and .title == $t' '.count = ((.count // 1) + 1) | .last_seen = $now' --arg t "$title" --arg now "$now"
    else
      jq -nc --arg id "$id" --arg now "$now" --arg status "$status" --arg severity "$severity" --arg category "$category" \
        --arg source "$source" --arg title "$title" --arg message "$message" --argjson details "$details" \
        '{id: $id, time: $now, last_seen: $now, count: 1, status: $status, severity: $severity, category: $category,
          source: $source, title: $title, message: $message} + (if $details == null then {} else {details: $details} end)' >> "$NOTIFICATIONS_LOG"
      added=1
    fi
  } 200>"$NOTIFICATIONS_LOCK"
  (( added ))
}

# marks unread entries with this title as read and resolved, --prefix matches titles with dynamic parts
# prints the time the oldest of them was first seen, returns 1 if nothing matched
notification_resolve() {
  local title="$1" cond='.status == "unread" and .severity != "info" and .title == $t'
  [[ "$2" == "--prefix" ]] && cond='.status == "unread" and .severity != "info" and (.title | startswith($t))'
  _notifications_init
  grep -qF '"unread"' "$NOTIFICATIONS_LOG" || return 1
  local now; now=$(date '+%Y-%m-%d %H:%M:%S')
  local first=""
  {
    flock -x 200
    if (( $(_notifications_count "$cond" --arg t "$title") > 0 )); then
      first=$(jq -nrR --arg t "$title" "[inputs | $_NOTIF_PARSE | select((\$o|type) == \"object\") | \$o | select($cond) | .time] | min // \"\"" "$NOTIFICATIONS_LOG")
      _notifications_update "$cond" '.status = "read" | .resolved_at = $now' --arg t "$title" --arg now "$now"
    fi
  } 200>"$NOTIFICATIONS_LOCK"
  [[ -n "$first" ]] || return 1
  echo "$first"
}

notification_delete_title() {
  _notifications_init
  local tmp; tmp=$(mktemp /tmp/notifications.XXXXXX) || return 1
  {
    flock -x 200
    if jq -rR --arg t "$1" "$_NOTIF_PARSE | if (\$o|type) == \"object\" and \$o.title == \$t then empty else \$raw end" "$NOTIFICATIONS_LOG" > "$tmp"; then
      cat "$tmp" > "$NOTIFICATIONS_LOG"
    fi
  } 200>"$NOTIFICATIONS_LOCK"
  rm -f "$tmp"
}
