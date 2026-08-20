#!/bin/bash
################################################################################
# Script Name: backup.sh
# Description: Backs up (and restores) OpenPanel/OpenAdmin SYSTEM AND USER
#              CONFIGURATION -- panel settings, per-domain webserver/DNS
#              config, the root docker-compose.yml/.env stacks (main +
#              mailserver's compose.yml/.env/mailserver.env only -- NOT its
#              DKIM keys or mail accounts, see docker-data/dms/config),
#              PHP/MySQL/FTP/SSH config, etc. Deliberately excludes real
#              user data: website files, databases, mailbox contents, mail
#              accounts/DKIM keys, and large downloadable software assets
#              (CMS installer archives, the GeoIP database, the vendored
#              Caddy WAF ruleset, ionCube/composer caches) are never
#              touched. Destination and retention are read from
#              /etc/openpanel/openadmin/config/backups.ini -- OpenAdmin's
#              System Backups page writes that file.
# Usage: opencli backup
#        opencli backup --restore <archive_filename>
#        opencli backup --quiet
# Docs: https://docs.openpanel.com
# Author: Stefan Pejcic
# Created: 20.08.2026
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

set -o pipefail

readonly CONFIG_FILE="/etc/openpanel/openadmin/config/backups.ini"
readonly LOG_FILE="/var/log/openpanel/admin/system-backup.log"
readonly RUNS_FILE="/var/log/openpanel/admin/system-backup-runs.jsonl"
readonly ARCHIVE_PREFIX="system-backup_"

QUIET=0
RESTORE_FILE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --restore) RESTORE_FILE="$2"; shift 2 ;;
        --quiet)   QUIET=1; shift ;;
        *) echo "[ERROR] Unknown option: $1" >&2; exit 1 ;;
    esac
done

mkdir -p "$(dirname "$LOG_FILE")"

log() {
    local msg="$1" ts; ts=$(date +'%Y-%m-%d %H:%M:%S')
    if [[ $QUIET -eq 1 ]]; then
        echo "[$ts] $msg" >> "$LOG_FILE"
    else
        echo "[$ts] $msg" | tee -a "$LOG_FILE"
    fi
}

die() { log "[✘] $1"; exit "${2:-1}"; }

# record_run appends one JSON-line summary of this invocation to RUNS_FILE,
# which is what OpenAdmin's "Runs" tab reads -- the human-readable LOG_FILE
# above is for full detail, this is the compact, easily-parsed summary.
record_run() {
    local action="$1" status="$2" archive="$3" size_bytes="$4" duration="$5" detail="$6"
    printf '{"timestamp":"%s","action":"%s","status":"%s","archive":"%s","size_bytes":%s,"duration_seconds":%s,"detail":"%s"}\n' \
        "$(date +'%Y-%m-%dT%H:%M:%S%z')" "$action" "$status" "$archive" "${size_bytes:-0}" "${duration:-0}" \
        "$(echo "$detail" | sed 's/"/\\"/g')" >> "$RUNS_FILE"
}

# Curated list of system/user CONFIGURATION paths (relative to /, since the
# archive is built with `tar -C /` so it can be restored with a plain
# `tar -xzf archive -C /`). This is deliberately hand-picked, not "the whole
# of /etc/openpanel" -- see the file header for what's excluded and why. Add
# more paths here as needed; a path that doesn't exist on this install (e.g.
# an app that was never used) is silently skipped, not an error.
BACKUP_PATHS=(
    "root/docker-compose.yml"
    "root/.env"
    "root/openpanel_run_after_update"
    "usr/local/mail/openmail/compose.yml"
    "usr/local/mail/openmail/.env"
    "usr/local/mail/openmail/mailserver.env"
    "etc/cron.d/openpanel"
    # /etc/bind holds the LIVE named config (named.conf.local,
    # rndc.key) and every domain's actual DNS zone file --
    # etc/openpanel/bind9 below is only install-time templates/defaults,
    # not the same thing.
    "etc/bind"
    "etc/openpanel/openadmin/config"
    "etc/openpanel/openadmin/cluster"
    "etc/openpanel/openadmin/service"
    "etc/openpanel/openpanel/conf"
    "etc/openpanel/openpanel/features"
    "etc/openpanel/openpanel/custom_code"
    "etc/openpanel/openpanel/service"
    "etc/openpanel/csf"
    "etc/openpanel/docker/compose"
    "etc/openpanel/docker/daemon"
    "etc/openpanel/docker/templates"
    "etc/openpanel/mysql"
    "etc/openpanel/nginx/vhosts"
    "etc/openpanel/nginx/templates"
    "etc/openpanel/nginx/modsecurity"
    "etc/openpanel/nginx/error_pages"
    "etc/openpanel/nginx/certs"
    "etc/openpanel/caddy/Caddyfile"
    "etc/openpanel/caddy/domains"
    "etc/openpanel/caddy/templates"
    "etc/openpanel/caddy/deny"
    "etc/openpanel/caddy/suspended_domains"
    "etc/openpanel/caddy/ssl"
    "etc/openpanel/openlitespeed"
    "etc/openpanel/apache"
    "etc/openpanel/openresty"
    "etc/openpanel/php/ini"
    "etc/openpanel/ftp"
    "etc/openpanel/ssh"
    "etc/openpanel/ofelia"
    "etc/openpanel/skeleton"
    "etc/openpanel/varnish"
    "etc/openpanel/postgres"
    "etc/openpanel/services"
    "etc/openpanel/modules"
    "etc/openpanel/bind9"
    "etc/openpanel/backups"
    "etc/openpanel/clamav"
    "etc/openpanel/email"
)

# read_ini_value reads a flat "key=value" line from CONFIG_FILE, ignoring
# any [section] header (backups.ini only has one section's worth of keys,
# so which section it's under doesn't matter for lookup purposes).
read_ini_value() {
    local key="$1"
    grep -E "^${key}=" "$CONFIG_FILE" 2>/dev/null | tail -1 | cut -d'=' -f2- | xargs
}

do_backup() {
    [[ -f "$CONFIG_FILE" ]] || die "Config file not found: $CONFIG_FILE. Set a destination on the System Backups page first."

    local destination retention_days
    destination=$(read_ini_value "destination")
    retention_days=$(read_ini_value "retention_days")
    retention_days="${retention_days:--1}"

    [[ -n "$destination" ]] || die "No backup destination configured in $CONFIG_FILE."
    mkdir -p "$destination" || die "Could not create destination directory: $destination"

    local start_time; start_time=$(date +%s)
    log "=== System backup started ==="

    local existing_paths=()
    for p in "${BACKUP_PATHS[@]}"; do
        [[ -e "/$p" ]] && existing_paths+=("$p")
    done
    log "Including ${#existing_paths[@]}/${#BACKUP_PATHS[@]} configured paths that exist on this server."

    local timestamp archive_name archive_path
    timestamp=$(date +'%Y-%m-%d_%H-%M-%S')
    archive_name="${ARCHIVE_PREFIX}${timestamp}.tar.gz"
    archive_path="${destination%/}/${archive_name}"

    # --exclude guards against the (common) case of the destination sitting
    # inside one of BACKUP_PATHS (e.g. destination under
    # /etc/openpanel/backups/, which is itself backed up) -- without it tar
    # tries to read the archive it's still writing and fails with "file
    # changed as we read it".
    if ! tar -czf "$archive_path" -C / --exclude="${destination#/}/*" "${existing_paths[@]}" 2>>"$LOG_FILE"; then
        record_run "backup" "failed" "$archive_name" "0" "$(( $(date +%s) - start_time ))" "tar command failed, see $LOG_FILE"
        die "Backup failed -- see $LOG_FILE for details."
    fi

    local size_bytes; size_bytes=$(stat -c '%s' "$archive_path" 2>/dev/null || echo 0)
    local duration=$(( $(date +%s) - start_time ))
    log "Backup archive created: $archive_path ($(du -h "$archive_path" | cut -f1), ${duration}s)"

    # Retention: prune older backups beyond retention_days, if set (>0).
    local pruned=0
    if [[ "$retention_days" =~ ^[0-9]+$ && "$retention_days" -gt 0 ]]; then
        while IFS= read -r old_file; do
            rm -f "$old_file" && { log "Pruned old backup: $old_file"; ((pruned++)); }
        done < <(find "$destination" -maxdepth 1 -name "${ARCHIVE_PREFIX}*.tar.gz" -mtime "+${retention_days}" 2>/dev/null)
    fi

    log "=== System backup finished (${duration}s, pruned ${pruned} old backup(s)) ==="
    record_run "backup" "success" "$archive_name" "$size_bytes" "$duration" "${#existing_paths[@]} paths, pruned ${pruned}"
}

do_restore() {
    local filename="$1"
    [[ -f "$CONFIG_FILE" ]] || die "Config file not found: $CONFIG_FILE."

    local destination; destination=$(read_ini_value "destination")
    [[ -n "$destination" ]] || die "No backup destination configured in $CONFIG_FILE."

    # basename only -- never let a caller-supplied filename escape the
    # configured destination directory.
    filename="$(basename -- "$filename")"
    local archive_path="${destination%/}/${filename}"
    [[ -f "$archive_path" ]] || die "Backup archive not found: $archive_path"
    [[ "$filename" == ${ARCHIVE_PREFIX}*.tar.gz ]] || die "Refusing to restore a file that isn't a system-backup archive: $filename"

    local start_time; start_time=$(date +%s)
    log "=== Restoring from $filename ==="

    if ! tar -xzf "$archive_path" -C / 2>>"$LOG_FILE"; then
        record_run "restore" "failed" "$filename" "0" "$(( $(date +%s) - start_time ))" "tar extract failed, see $LOG_FILE"
        die "Restore failed -- see $LOG_FILE for details."
    fi

    local duration=$(( $(date +%s) - start_time ))
    log "=== Restore finished (${duration}s) -- affected services may need a restart to pick up restored config ==="
    record_run "restore" "success" "$filename" "$(stat -c '%s' "$archive_path" 2>/dev/null || echo 0)" "$duration" "restored"
}

if [[ -n "$RESTORE_FILE" ]]; then
    do_restore "$RESTORE_FILE"
else
    do_backup
fi
