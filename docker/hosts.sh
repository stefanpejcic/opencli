#!/bin/bash
################################################################################
# Script Name: docker/hosts.sh
# Description: Write the hosts file that users' new containers start with, so the panel and mail hostnames point at the server.
# Usage: opencli docker-hosts [--show | --apply [USERNAME]]
# Author: Stefan Pejcic
# Created: 12.11.2026
# Last Modified: 12.11.2026
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

. /usr/local/opencli/lib/podman.sh

# podman builds a new container's /etc/hosts from this file when the user's containers.conf has base_hosts_file set (user-add does that)
HOSTS_FILE="/etc/openpanel/docker/container_hosts"
# lines the admin wants in every container, appended as they are
CUSTOM_FILE="/etc/openpanel/docker/container_hosts.custom"
CADDY_FILE="/etc/openpanel/caddy/Caddyfile"
MAILSERVER_ENV="/usr/local/mail/openmail/mailserver.env"
# pasta maps this address to the host, the public ip isn't reachable from rootless containers
HOST_ADDR="169.254.1.2"

usage() {
    echo "Usage: opencli docker-hosts [--show | --apply [USERNAME]]"
    echo ""
    echo "  (no args)           Write $HOSTS_FILE from the panel domain and the mail hostname"
    echo "  --show              Print the file without writing it"
    echo "  --apply [USERNAME]  Write the file, set it for all existing users (or one) and restart their running php-fpm and webserver containers"
}

panel_domain() {
    [[ -f "$CADDY_FILE" ]] || return 0
    local d
    for block in "USERPANEL DOMAIN" "HOSTNAME DOMAIN"; do
        d=$(sed -n "/# START $block #/,/# END $block #/p" "$CADDY_FILE" | sed -n 's/^\([A-Za-z0-9.-]*\)[[:space:]]*{.*/\1/p' | head -n1)
        if [[ -n "$d" ]]; then
            echo "$d"
            return 0
        fi
    done
}

mail_hostname() {
    [[ -f "$MAILSERVER_ENV" ]] || return 0
    grep -m1 '^OVERRIDE_HOSTNAME=' "$MAILSERVER_ENV" | cut -d= -f2- | tr -d '"'"'"' '
}

is_hostname() {
    [[ "$1" =~ ^([A-Za-z0-9-]+\.)+[A-Za-z]{2,}$ ]]
}

build() {
    local names=() n
    for n in "$(panel_domain)" "$(mail_hostname)"; do
        is_hostname "$n" || continue
        [[ " ${names[*]} " == *" $n "* ]] || names+=("$n")
    done

    echo "# written by opencli docker-hosts, put your own lines in $CUSTOM_FILE"
    echo "127.0.0.1	localhost"
    echo "::1	localhost ip6-localhost ip6-loopback"
    [[ ${#names[@]} -gt 0 ]] && echo "$HOST_ADDR	${names[*]}"
    if [[ -f "$CUSTOM_FILE" ]]; then
        echo ""
        cat "$CUSTOM_FILE"
    fi
}

# sets base_hosts_file in the user's containers.conf, returns 1 when the user has no podman config
set_user_base_hosts() {
    local conf="/home/$1/.config/containers/containers.conf"
    [[ -f "$conf" ]] || return 1
    grep -q "^base_hosts_file *= *\"$HOSTS_FILE\"" "$conf" && return 0
    sed -i '/^base_hosts_file *=/d' "$conf"
    if grep -q '^\[containers\]' "$conf"; then
        sed -i "/^\[containers\]/a base_hosts_file = \"$HOSTS_FILE\"" "$conf"
    else
        printf '\n[containers]\nbase_hosts_file = "%s"\n' "$HOSTS_FILE" >> "$conf"
    fi
}

# podman builds /etc/hosts on every start, so a stop and start is enough, php first and the webserver last
restart_user_containers() {
    local user="$1" ws c running
    running=$(podman_user "$user" ps --format '{{.Names}}' 2>/dev/null)
    ws=$(grep -m1 '^WEB_SERVER=' "/home/$user/.env" 2>/dev/null | cut -d= -f2- | tr -d '"'"'"' ')
    for c in $(grep '^php-fpm-' <<< "$running") $(grep -x -- "$ws" <<< "$running"); do
        if podman_user "$user" stop "$c" >/dev/null 2>&1 && podman_user "$user" start "$c" >/dev/null 2>&1; then
            echo "  restarted $c"
        else
            echo "  failed to restart $c, check it with: opencli docker-logs $user"
        fi
    done
}

apply() {
    local only="$1" home user users=()
    if [[ -n "$only" ]]; then
        [[ -f "/home/$only/.env" ]] || { echo "User $only not found."; exit 1; }
        users=("$only")
    else
        for home in /home/*/; do
            user=$(basename "$home")
            [[ -f "/home/$user/.env" && -f "/home/$user/.config/containers/containers.conf" ]] && users+=("$user")
        done
    fi
    for user in "${users[@]}"; do
        echo "$user:"
        if ! set_user_base_hosts "$user"; then
            echo "  no containers.conf, skipped"
            continue
        fi
        # the podman service may still have the old config loaded, it only restarts if it's running
        systemctl --user -M "${user}@" try-restart podman.service >/dev/null 2>&1
        restart_user_containers "$user"
    done
}

case "$1" in
    --show)
        build
        exit 0
        ;;
    --apply)
        APPLY=1
        APPLY_USER="$2"
        ;;
    -h|--help)
        usage
        exit 0
        ;;
    "")
        ;;
    *)
        usage
        exit 1
        ;;
esac

mkdir -p "$(dirname "$HOSTS_FILE")"
tmp=$(mktemp "${HOSTS_FILE}.XXXXXX") || exit 1
if ! build > "$tmp"; then
    rm -f "$tmp"
    echo "Failed to write $HOSTS_FILE"
    exit 1
fi
# users' podman reads it, so it has to stay world readable
chmod 644 "$tmp"
mv -f "$tmp" "$HOSTS_FILE"
echo "Updated $HOSTS_FILE, new containers will use it."

# an if, not &&, so the script doesn't exit 1 when --apply wasn't passed
if [[ -n "$APPLY" ]]; then
    apply "$APPLY_USER"
fi
exit 0
