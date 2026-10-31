#!/bin/bash
################################################################################
# Script Name: user/litespeed.sh
# Description: Show or set the LiteSpeed Enterprise license of a user.
# Usage: opencli user-litespeed <USERNAME> [status|serial <SERIAL>|trial] [--json]
# Author: Stefan Pejcic
# Created: 31.10.2026
# Last Modified: 31.10.2026
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

# shellcheck disable=SC1091
. /usr/local/opencli/lib/podman.sh

usage() {
    echo "Usage: opencli user-litespeed <USERNAME> [status|serial <SERIAL>|trial] [--json]"
    echo
    echo "  status           Show the license LiteSpeed runs with (default)."
    echo "  serial <SERIAL>  Register a LiteSpeed serial number, it's used from then on."
    echo "  trial            Remove the serial and go back to a trial license."
    echo "  --json           Print the result as JSON."
    exit 1
}

JSON=false
ARGS=()
for a in "$@"; do
    case "$a" in
        --json) JSON=true ;;
        -h|--help) usage ;;
        *) ARGS+=("$a") ;;
    esac
done

USERNAME="${ARGS[0]}"
ACTION="${ARGS[1]:-status}"
SERIAL="${ARGS[2]}"
[ -z "$USERNAME" ] && usage

CONTEXT="$USERNAME"
ENV_FILE="/home/$CONTEXT/.env"
CONF=/usr/local/lsws/conf

die() {
    if $JSON; then
        jq -cn --arg e "$1" '{success: false, error: $e}'
    else
        echo "Error: $1" >&2
    fi
    exit 1
}

[ -f "$ENV_FILE" ] || die "User $USERNAME not found."
WEB_SERVER=$(grep "^WEB_SERVER=" "$ENV_FILE" | cut -d= -f2- | tr -d '"[:space:]')
[ "$WEB_SERVER" = "litespeed" ] || die "User $USERNAME doesn't use LiteSpeed (webserver: ${WEB_SERVER:-none})."

lsws() { podman_ctx "$CONTEXT" exec -i litespeed "$@"; }

ensure_running() {
    podman_ctx_is_running "$CONTEXT" litespeed && return
    (cd "/home/$CONTEXT" && podman_compose_user "$CONTEXT" up -d litespeed >/dev/null 2>&1)
    local i
    for i in $(seq 1 30); do
        podman_ctx_is_running "$CONTEXT" litespeed && return
        sleep 2
    done
    die "LiteSpeed container of $USERNAME is not running."
}

# prints the license type and what lshttpd says about it
status() {
    ensure_running
    local type message
    type=$(lsws sh -c "if [ -s $CONF/serial.no ]; then echo serial; elif [ -e $CONF/license.key ]; then echo license; elif [ -e $CONF/trial.key ]; then echo trial; else echo none; fi")
    message=$(lsws /usr/local/lsws/bin/lshttpd -V 2>&1 | grep -v 'Memory size' | grep -i 'license\|expire\|serial' | tail -1 | sed 's/^.*\] //')
    # a serial that never got registered leaves no license key behind
    [[ "$type" == serial && "$message" == *"Failed to find your license key"* ]] && message="The serial number isn't registered, LiteSpeed won't start until it is."
    local days
    days=$(grep -oE 'expire in [0-9]+ day' <<< "$message" | grep -oE '[0-9]+')
    if $JSON; then
        jq -cn --arg type "$type" --arg message "$message" --arg days "$days" \
            '{success: true, type: $type, message: $message, days_left: (if $days == "" then null else ($days | tonumber) end)}'
    else
        echo "License: $type"
        [ -n "$message" ] && echo "$message"
    fi
}

restart_lsws() { lsws /usr/local/lsws/bin/lswsctrl restart >/dev/null 2>&1; }

set_serial() {
    [[ "$SERIAL" =~ ^[A-Za-z0-9+/=-]{8,64}$ ]] || die "Invalid serial number."
    ensure_running
    local out
    # the old keys are kept as .old, same as litespeed's own serialctl.sh
    out=$(printf '%s\n' "$SERIAL" | lsws sh -c "cd $CONF && for f in serial.no license.key trial.key; do [ -e \$f ] && mv -f \$f \$f.old; done; cat > serial.no && chown lsadm:lsadm serial.no && /usr/local/lsws/bin/lshttpd -r 2>&1")
    if lsws test -e "$CONF/license.key"; then
        restart_lsws
        if $JSON; then
            jq -cn '{success: true, message: "Serial number registered, LiteSpeed restarted with the new license."}'
        else
            echo "Serial number registered, LiteSpeed restarted with the new license."
        fi
        return
    fi
    # the serial stays, the running server keeps its old license until the next restart
    local reason
    reason=$(grep -oE 'ERR [A-Z.]+: .*' <<< "$out" | tail -1)
    die "Serial number could not be registered: ${reason:-no license key was issued}. It is saved and LiteSpeed will use it from the next restart."
}

set_trial() {
    ensure_running
    lsws sh -c "cd $CONF && for f in serial.no license.key; do [ -e \$f ] && mv -f \$f \$f.old; done; rm -f trial.key && wget -q -P $CONF/ http://license.litespeedtech.com/reseller/trial.key && chown lsadm:lsadm trial.key" \
        || die "Could not download a trial license."
    restart_lsws
    if $JSON; then
        jq -cn '{success: true, message: "Switched to a trial license."}'
    else
        echo "Switched to a trial license."
    fi
}

case "$ACTION" in
    status) status ;;
    serial) [ -z "$SERIAL" ] && usage; set_serial ;;
    trial)  set_trial ;;
    *) usage ;;
esac
