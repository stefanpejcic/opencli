#!/bin/bash
################################################################################
# Script Name: php/domain.sh
# Description: View or change the PHP version used for a single domain name.
# Usage: opencli php-domain <DOMAIN_NAME> [--update <PHP_VERSION>]
#        opencli php-domain <domain_name> --update <new_php_version>
# Author: Stefan Pejcic
# Created: 07.10.2023
# Last Modified: 19.11.2026
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
. /usr/local/opencli/lib/tenantfs.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/podman.sh
# ======================================================================
# Variables
domain="$1"
update_flag=false
new_php_version=""


# ======================================================================
# Validators

if [ $# -lt 1 ]; then
    echo "Usage: opencli php-domain <domain> [--update <new_php_version>]"
    exit 1
fi

if [ "$2" == "--update" ]; then
    if [ -z "$3" ]; then
        echo "Error: --update flag requires a new PHP version in the format number.number."
        exit 1
    fi
    if [[ ! "$3" =~ ^[0-9]\.[0-9]$ ]]; then
        echo "Invalid PHP version format. Please use the format 'number.number' (e.g., 8.1 or 5.6)."
        exit 1
    fi
    update_flag=true
    new_php_version="$3"
fi


# ======================================================================
# Helpers
get_context_for_user() {
	# shellcheck source=/usr/local/opencli/db.sh
    source /usr/local/opencli/db.sh
	username_query="SELECT server FROM users WHERE username = '$(mysql_escape "$owner")'"
	context=$(mariadb -D "$mysql_database" -e "$username_query" -sN)
	if [ -z "$context" ]; then
		context=$owner
	fi
}

get_webserver_for_user(){
		web_server=$(grep "^WEB_SERVER=" "/home/$context/.env" | awk -F '=' '{print $2}' | tr -d '[:space:]' | sed 's/^"\(.*\)"$/\1/')
		WEB_SERVER=$(echo "$web_server" | grep -Eo 'nginx|openresty|apache|openlitespeed|litespeed|caddy' | head -n1)
        if [[ "$WEB_SERVER" == "openlitespeed" || "$WEB_SERVER" == "litespeed" ]]; then
            echo "ERROR: PHP version can not be changed on $WEB_SERVER webserver. Instead you need to change the docker image tag for the user."
            echo "Available tags: https://hub.docker.com/r/litespeedtech/${WEB_SERVER}/tags"
            exit 0
        fi
}

# ======================================================================
# Main
whoowns_output=$(opencli domains-whoowns "$domain")
owner=$(echo "$whoowns_output" | awk -F "Owner of '$domain': " '{print $2}')
if [ -z "$owner" ]; then
    echo "Failed to determine the owner of the domain '$domain'." >&2
    exit 1
fi


get_context_for_user
get_webserver_for_user
domain_path_in_volume="/home/$context/docker-data/volumes/${context}_webserver_data/_data/$domain.conf"
# caddy mode keeps the php socket in the caddy conf, there's no vhost
[[ "$WEB_SERVER" == "caddy" ]] && domain_path_in_volume="/etc/openpanel/caddy/domains/$domain.conf"
php_version=$(grep -o "php-fpm-[0-9.]\+" "$domain_path_in_volume" | grep -o "[0-9.]\+" | head -n 1)

if [ -z "$php_version" ]; then
	echo "Failed to determine the PHP version for the domain '$domain' (owned by user $owner)." >&2
	exit 1
fi

# show
if [ "$update_flag" == false ]; then
	echo "Domain '$domain' (owned by user: $owner) uses PHP version: $php_version"
	exit 0
fi

# update
# 1. replace in domain vhost file
if [[ "$WEB_SERVER" == "caddy" ]]; then
    sed -i "s/php-fpm-[0-9.]\+/php-fpm-$new_php_version/g" "$domain_path_in_volume"
else
    tenant_sed_i "$(tenant_volume "$context" webserver_data)" "$domain_path_in_volume" "s/php-fpm-[0-9.]\+/php-fpm-$new_php_version/g"
fi

# 2. start new php version container if not running
# sh -c doesn't inherit lib/podman.sh's bash functions, so the socket is inlined
owner_sock="unix:///hostfs/run/user/$(stat -c '%u' "/home/$owner")/podman/podman.sock"
# up -d would recreate it if another domain already runs this version
if ! podman_ctx_is_running "$owner" "php-fpm-${new_php_version}"; then
    nohup sh -c "cd /home/$owner && CONTAINER_HOST=$owner_sock podman-compose up -d php-fpm-${new_php_version}" </dev/null >/dev/null 2>&1 &
fi

# 3. previous version gets stopped if not used by other domains AND not set as default
old_php_version="$php_version"
default_php_version=$(awk -F '=' '/DEFAULT_PHP_VERSION/ {print $2}' "/home/$context/.env" | tr -d '[:space:]')
stop_old=""
if [ -n "$default_php_version" ] && [ "$old_php_version" != "$default_php_version" ]; then
	user_vhost_dir="/home/$context/docker-data/volumes/${context}_webserver_data/_data/"
    if [[ "$WEB_SERVER" == "caddy" ]]; then
        grep -rqF "/home/$context/sockets/php/php-fpm-$old_php_version/" /etc/openpanel/caddy/domains/ --include="*.conf" || stop_old="php-fpm-$old_php_version"
    else
        grep -rq "php-fpm-$old_php_version" "$user_vhost_dir" --include="*.conf" || stop_old="php-fpm-$old_php_version"
    fi
fi

# 4. reload webserver once the new php-fpm listens, and only then stop the old one so the site never points at a stopped container
# shellcheck disable=SC2016
nohup bash -c '. /usr/local/opencli/lib/podman.sh
    podman_reload_webserver "$1" "$2" "$3" || exit 1
    [ -n "$4" ] && cd "/home/$5" && podman_compose_ctx "$5" down "$4"' _ "$context" "$WEB_SERVER" "php-fpm-$new_php_version" "$stop_old" "$owner" </dev/null >/dev/null 2>&1 &
disown

# 5. save in database
update_query="UPDATE domains SET php_version='$(mysql_escape "$new_php_version")' WHERE domain_url='$(mysql_escape "$domain")';"
mariadb -e "$update_query"

# 6. jobs of this domain that ran in the old php-fpm move to the new one, same as the PHP version switch in the user panel
docroot=$(mariadb -sN -e "SELECT docroot FROM domains WHERE domain_url='$(mysql_escape "$domain")'" 2>/dev/null)
crons_file="/home/$context/crons.ini"
if [[ -f "$crons_file" ]]; then
    # first pass marks the blocks (separated by blank lines) of this domain in the old container, the second rewrites their container line
    # plain awk string matching since the opencli in the openpanel container has no python, a longer docroot like a.com2 doesn't count
    tmp_crons=$(mktemp)
    awk -v dom="$domain" -v root="${docroot%/}" -v old="php-fpm-$old_php_version" -v new="php-fpm-$new_php_version" '
        function trim(s) { gsub(/^[ \t]+|[ \t]+$/, "", s); return s }
        # true if needle is in s with an allowed char before and after it
        function has(s, needle, before, after, hostonly,    i, p, b, a, pre) {
            if (needle == "") return 0
            p = 0
            while ((i = index(substr(s, p + 1), needle)) > 0) {
                p += i
                b = (p > 1) ? substr(s, p - 1, 1) : ""
                a = substr(s, p + length(needle), 1)
                if ((b == "" || index(before, b)) && (a == "" || index(after, a))) {
                    if (!hostonly) return 1
                    # the domain counts after "host:" or "://" only
                    pre = tolower(substr(s, 1, p - 1)); sub(/[ \t]+$/, "", pre)
                    if (pre ~ /host:$/ || pre ~ /:\/\/$/) return 1
                }
            }
            return 0
        }
        BEGIN { blk = 0 }
        NR == FNR {
            if ($0 ~ /^[ \t]*$/) { blk++; next }
            line = $0; sub(/^#[ \t]*/, "", line)
            if (line ~ /^container[ \t]*=/) { v = line; sub(/^container[ \t]*=/, "", v); cont[blk] = trim(v) }
            if (line ~ /^command[ \t]*=/) { v = line; sub(/^command[ \t]*=/, "", v); cmd[blk] = v }
            next
        }
        FNR == 1 { b = 0; for (k in cont) if (cont[k] == old && (has(cmd[k], dom, ":/ \t", "/: \t\047\"", 1) || has(cmd[k], root, " \t\047\"=:", "/ \t\047\"", 0))) move[k] = 1 }
        {
            if ($0 ~ /^[ \t]*$/) b++
            else if (move[b] && $0 ~ /^#?[ \t]*container[ \t]*=/) { sub(old "[ \t]*$", new); moved++ }
            print
        }
        END { print moved + 0 > "/dev/stderr" }
    ' "$crons_file" "$crons_file" > "$tmp_crons" 2> "$tmp_crons.n"
    moved=$(cat "$tmp_crons.n" 2>/dev/null)
    [[ "${moved:-0}" -gt 0 ]] && cat "$tmp_crons" > "$crons_file"
    rm -f "$tmp_crons" "$tmp_crons.n"
    if [[ "${moved:-0}" -gt 0 ]]; then
        podman_ctx_is_running "$owner" cron && podman_ctx "$owner" restart cron >/dev/null 2>&1
        echo "Moved $moved cron job(s) of $domain from php-fpm-$old_php_version to php-fpm-$new_php_version"
    fi
fi

# 7. notify
# todo: trigger user and admin notifications
echo "Updated PHP version in the configuration file to $new_php_version"

