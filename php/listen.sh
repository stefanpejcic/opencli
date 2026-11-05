#!/bin/bash
################################################################################
# Script Name: php/listen.sh
# Description: View or change how a user's websites reach php-fpm: TCP port 9000, a unix socket, or Caddy talking to the socket directly without a webserver container.
# Usage: opencli php-listen <USERNAME> [port|socket|caddy] [--webserver nginx|apache|openresty]
# Author: Stefan Pejcic
# Created: 01.11.2026
# Last Modified: 01.11.2026
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
# shellcheck disable=SC1091
. /usr/local/opencli/lib/tenantfs.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/php_listen.sh

usage() {
    echo "Usage: opencli php-listen <USERNAME> [port|socket|caddy] [--webserver nginx|apache|openresty]"
    echo
    echo "  port     webserver reaches php-fpm-X.Y:9000 (default)"
    echo "  socket   webserver reaches php-fpm over /run/php/php-fpm-X.Y/www.sock"
    echo "  caddy    no webserver container, Caddy serves files and talks to the php-fpm socket itself"
    echo
    echo "  --webserver  webserver to start again when leaving caddy mode (default: the one used before caddy, or nginx)"
    exit 1
}

username="" target="" back_ws=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --webserver) back_ws="$2"; shift 2 ;;
        port|socket|caddy) target="$1"; shift ;;
        -h|--help) usage ;;
        -*) echo "Unknown option: $1"; usage ;;
        *) [[ -z "$username" ]] && username="$1" || usage; shift ;;
    esac
done
[[ -z "$username" ]] && usage
[[ -n "$back_ws" && ! "$back_ws" =~ ^(nginx|apache|openresty)$ ]] && { echo "ERROR: --webserver must be nginx, apache or openresty."; exit 1; }

# shellcheck source=/usr/local/opencli/db.sh
source /usr/local/opencli/db.sh
user_id=$(mariadb -D "$mysql_database" -sN -e "SELECT id FROM users WHERE username = '$(mysql_escape "$username")'")
[[ -z "$user_id" ]] && { echo "ERROR: user $username does not exist."; exit 1; }
context=$(mariadb -D "$mysql_database" -sN -e "SELECT server FROM users WHERE id = '$user_id'")
[[ -z "$context" || "$context" == "default" ]] && context="$username"

env_file="/home/$context/.env"
compose_file="/home/$context/docker-compose.yml"
[[ -f "$env_file" && -f "$compose_file" ]] || { echo "ERROR: /home/$context/.env or docker-compose.yml is missing."; exit 1; }

web_server=$(php_listen_env_value "$context" WEB_SERVER)
current=$(php_listen_mode "$context")

if [[ -z "$target" ]]; then
    echo "User '$username' reaches php-fpm over: $current (webserver: $web_server)"
    exit 0
fi

if [[ "$current" == "$target" ]]; then
    echo "User '$username' already uses: $target"
    exit 0
fi

if [[ "$web_server" == *litespeed* ]]; then
    echo "ERROR: $web_server runs LSPHP itself, it doesn't use php-fpm."
    exit 1
fi
if [[ "$target" == "caddy" ]] && grep -qE "^PROXY_HTTP_PORT=" "$env_file"; then
    echo "ERROR: Varnish is enabled for $username, disable it first, caddy mode has no webserver for Varnish to sit in front of."
    exit 1
fi

set_env() {
    if grep -qE "^$1=" "$env_file"; then
        sed -i "s|^$1=.*|$1=\"$2\"|" "$env_file"
    else
        echo "$1=\"$2\"" >> "$env_file"
    fi
}

uid=$(stat -c '%u' "/home/$context")
vhost_dir="/home/$context/docker-data/volumes/${context}_webserver_data/_data"
webserver_volume=$(tenant_volume "$context" webserver_data)
running_php=$(podman_ctx "$context" ps --format '{{.Names}}' 2>/dev/null | grep -E '^php-fpm-[0-9.]+$' | xargs)
default_php=$(php_listen_env_value "$context" DEFAULT_PHP_VERSION)
[[ -z "$running_php" && -n "$default_php" ]] && running_php="php-fpm-$default_php"

# domain <tab> docroot <tab> php version
domains=$(mariadb -D "$mysql_database" -sN -e "SELECT domain_url, docroot, php_version FROM domains WHERE user_id = '$user_id'")

echo "Switching $username from $current to $target"

result=$(php_listen_compose_patch "$compose_file")
echo "- docker-compose.yml: $result"
mkdir -p "/home/$context/sockets/php"
chown "$uid:$uid" "/home/$context/sockets" "/home/$context/sockets/php"
chmod 755 "/home/$context/sockets/php"

listen_value="socket"; [[ "$target" == "port" ]] && listen_value="port"
set_env PHP_FPM_LISTEN "$listen_value"

compose_up() {
    # shellcheck disable=SC2086
    (cd "/home/$context" && podman_compose_user_retry "$context" -f "$compose_file" up -d --no-deps "$@" >/dev/null 2>&1)
}

if [[ "$target" == "caddy" ]]; then
    if ! podman inspect caddy --format '{{range .Mounts}}{{.Destination}} {{end}}' 2>/dev/null | tr ' ' '\n' | grep -qx /home; then
        echo "- Caddy doesn't see /home yet, adding a read-only mount and recreating it (every site on the server reconnects)"
        if ! grep -q "/home:/home:ro" /root/docker-compose.yml; then
            sed -i '/^  caddy:/,/^  [a-z]/ s|^\(      - /var/log/caddy/:/var/log/caddy/\)$|\1\n      - /home:/home:ro|' /root/docker-compose.yml
        fi
        (cd /root && podman-compose up -d caddy >/dev/null 2>&1)
        podman inspect caddy --format '{{range .Mounts}}{{.Destination}} {{end}}' 2>/dev/null | tr ' ' '\n' | grep -qx /home \
            || { echo "ERROR: caddy still doesn't mount /home, check /root/docker-compose.yml"; exit 1; }
    fi

    echo "- Recreating php-fpm on the socket: $running_php"
    # shellcheck disable=SC2086
    compose_up $running_php
    for c in $running_php; do
        for _ in $(seq 30); do podman_ctx_is_listening "$context" "$c" && break; sleep 2; done
    done

    while IFS=$'\t' read -r domain docroot php; do
        [[ -z "$domain" ]] && continue
        conf="/etc/openpanel/caddy/domains/$domain.conf"
        [[ -f "$conf" ]] || { echo "  $domain: no caddy conf, skipped"; continue; }
        # the vhost knows the version the domain really runs, the db column can lag behind
        v=$(grep -o "php-fpm-[0-9.]\+" "$vhost_dir/$domain.conf" 2>/dev/null | grep -o "[0-9.]\+" | head -n1)
        php_caddy_conf_direct "$conf" "$context" "$docroot" "${v:-$php}"
        echo "  $domain: caddy -> php-fpm-${v:-$php} socket, docroot $docroot"
    done <<< "$domains"
    podman exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1 || echo "WARNING: caddy reload failed, check: podman exec caddy caddy validate --config /etc/caddy/Caddyfile"

    if [[ -n "$web_server" && "$web_server" != "caddy" ]]; then
        set_env PHP_CADDY_PREV_WEB_SERVER "$web_server"
        (cd "/home/$context" && podman_compose_ctx "$context" stop "$web_server" >/dev/null 2>&1)
        echo "- Stopped $web_server, its vhosts are kept for switching back"
    fi
    set_env WEB_SERVER caddy
    echo "Done, Caddy now talks to php-fpm directly for $username"
    exit 0
fi

# port or socket, possibly coming back from caddy mode
if [[ "$current" == "caddy" ]]; then
    [[ -z "$back_ws" ]] && back_ws=$(php_listen_env_value "$context" PHP_CADDY_PREV_WEB_SERVER)
    [[ -z "$back_ws" ]] && back_ws=nginx
    web_server="$back_ws"
    set_env WEB_SERVER "$web_server"
    while IFS=$'\t' read -r domain docroot php; do
        [[ -z "$domain" ]] && continue
        conf="/etc/openpanel/caddy/domains/$domain.conf"
        [[ -f "$conf" ]] && php_caddy_conf_proxy "$conf"
        if [[ ! -f "$vhost_dir/$domain.conf" ]]; then
            sed -e "s|<DOMAIN_NAME>|$domain|g" -e "s|<USER>|$username|g" -e "s|<PHP>|$php|g" -e "s|<DOCUMENT_ROOT>|$docroot|g" \
                "/etc/openpanel/nginx/vhosts/1.1/docker_${web_server}_domain.conf" \
                | tenant_write "$webserver_volume" "$vhost_dir/$domain.conf" 644
            echo "  $domain: created $web_server vhost"
        fi
    done <<< "$domains"
fi

for f in "$vhost_dir"/*.conf; do
    [[ -f "$f" ]] || continue
    tenant_cat "$webserver_volume" "$f" | php_vhost_transport "$target" | tenant_write "$webserver_volume" "$f" 644
    echo "  $(basename "$f" .conf): $web_server -> php-fpm over $target"
done

echo "- Recreating $running_php and $web_server"
# shellcheck disable=SC2086
compose_up $running_php "$web_server"
# shellcheck disable=SC2086
podman_reload_webserver "$context" "$web_server" $running_php
[[ "$current" == "caddy" ]] && podman exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1
echo "Done, $web_server now reaches php-fpm over $target for $username"
