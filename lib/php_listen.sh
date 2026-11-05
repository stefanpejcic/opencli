#!/bin/bash
################################################################################
# Script Name: lib/php_listen.sh
# Description: Helpers for how a user's webserver reaches php-fpm: TCP port 9000 (default), a unix socket, or caddy talking to the socket directly.
# Docs: https://openpanel.com/docs/articles/opencli/php/
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

# socket path inside the php-fpm and webserver containers, the per-version dir keeps "php-fpm-X.Y" followed by "/" so every php-fpm-[0-9.]+ parser still reads the version
PHP_SOCKET_DIR_IN_CONTAINER="/run/php"

php_listen_env_value() {
    grep -E "^${2}=" "/home/$1/.env" 2>/dev/null | tail -n1 | cut -d= -f2- | tr -d '"' | tr -d "'" | tr -d '[:space:]'
}

# port (default, also for accounts made before this existed), socket, or caddy when caddy talks to php-fpm itself, e.g. php_listen_mode <context>
php_listen_mode() {
    local ws mode
    ws=$(php_listen_env_value "$1" WEB_SERVER)
    [[ "$ws" == "caddy" ]] && { echo caddy; return; }
    mode=$(php_listen_env_value "$1" PHP_FPM_LISTEN)
    [[ "$mode" == "socket" ]] && echo socket || echo port
}

# host path of a php version's socket, what caddy dials in caddy mode, e.g. php_host_socket <context> 8.3
php_host_socket() {
    echo "/home/$1/sockets/php/php-fpm-$2/www.sock"
}

# rewrites the php-fpm upstreams of a vhost read on stdin to port or socket, apache SetHandler and nginx/openresty fastcgi_pass, e.g. php_vhost_transport socket < vhost.conf
php_vhost_transport() {
    if [[ "$1" == "socket" ]]; then
        sed -E \
            -e "s#proxy:fcgi://(php-fpm-[0-9.]+):9000#proxy:unix:${PHP_SOCKET_DIR_IN_CONTAINER}/\1/www.sock|fcgi://localhost#g" \
            -e "s#fastcgi_pass([[:space:]]+)(php-fpm-[0-9.]+):9000;#fastcgi_pass\1unix:${PHP_SOCKET_DIR_IN_CONTAINER}/\2/www.sock;#g"
    else
        sed -E \
            -e "s#proxy:unix:${PHP_SOCKET_DIR_IN_CONTAINER}/(php-fpm-[0-9.]+)/www\.sock\|fcgi://localhost#proxy:fcgi://\1:9000#g" \
            -e "s#fastcgi_pass([[:space:]]+)unix:${PHP_SOCKET_DIR_IN_CONTAINER}/(php-fpm-[0-9.]+)/www\.sock;#fastcgi_pass\1\2:9000;#g"
    fi
}

# caddy mode: comments out the domain's reverse_proxy blocks and puts php_fastcgi on the user's socket in their place, reversible with php_caddy_conf_proxy, e.g. php_caddy_conf_direct <conf> <context> <docroot> <php_version>
php_caddy_conf_direct() {
    local conf="$1" ctx="$2" docroot="$3" php="$4"
    python3 - "$conf" "$ctx" "$docroot" "$php" "$(php_host_socket "$ctx" "$php")" <<'PY'
import sys, re
conf, ctx, docroot, php, sock = sys.argv[1:6]
rel = docroot[len("/var/www/html"):] if docroot.startswith("/var/www/html") else "/" + docroot.lstrip("/")
host_root = f"/home/{ctx}/docker-data/volumes/{ctx}_html_data/_data{rel}".rstrip("/")
lines = open(conf).read().split("\n")
# undo a previous run first so a docroot or version change can just run this again
out, skip = [], False
for l in lines:
    if l.strip() == "#php-direct-begin": skip = True; continue
    if l.strip() == "#php-direct-end": skip = False; continue
    if not skip: out.append(l[len("#php-proxy# "):] if l.startswith("#php-proxy# ") else l)
lines, out, i = out, [], 0
while i < len(lines):
    l = lines[i]
    if re.match(r"^\s*reverse_proxy\s+https?://", l):
        ind = re.match(r"^(\s*)", l).group(1)
        out += [f"{ind}#php-direct-begin",
                f"{ind}root * {host_root}",
                f"{ind}php_fastcgi unix/{sock} {{",
                f"{ind}  root {docroot.rstrip('/') or '/var/www/html'}",
                f"{ind}}}",
                f"{ind}file_server",
                f"{ind}#php-direct-end"]
        depth = 0
        while i < len(lines):
            depth += lines[i].count("{") - lines[i].count("}")
            out.append("#php-proxy# " + lines[i])
            i += 1
            if depth <= 0: break
        continue
    out.append(l); i += 1
open(conf, "w").write("\n".join(out))
PY
}

# undoes php_caddy_conf_direct, the original reverse_proxy blocks come back as they were, e.g. php_caddy_conf_proxy <conf>
php_caddy_conf_proxy() {
    sed -i -e '/#php-direct-begin/,/#php-direct-end/d' -e 's/^#php-proxy# //' "$1"
}

# adds the socket mount (php-fpm and webservers) and PHPFPM_LISTEN env (php-fpm) to a compose file, comments and formatting stay as they were, e.g. php_listen_compose_patch /home/<context>/docker-compose.yml
php_listen_compose_patch() {
    python3 - "$1" <<'PY'
import sys, re
path = sys.argv[1]
lines = open(path).read().split("\n")
MOUNT = "./sockets/php:/run/php"
ENV = "PHPFPM_LISTEN=${PHP_FPM_LISTEN:-port}"
webservers = {"nginx", "apache", "openresty"}
out, svc, section, changed = [], None, None, False
def block_has(start, text):
    j = start + 1
    while j < len(lines) and not re.match(r"^  \S", lines[j]):
        if text in lines[j]: return True
        j += 1
    return False
svc_start = {}
for idx, l in enumerate(lines):
    m = re.match(r"^  ([A-Za-z0-9_.-]+):\s*$", l)
    if m: svc_start[m.group(1)] = idx
for idx, l in enumerate(lines):
    m = re.match(r"^  ([A-Za-z0-9_.-]+):\s*$", l)
    if m: svc = m.group(1)
    if re.match(r"^\S", l): svc = None
    out.append(l)
    if svc is None: continue
    is_php = svc.startswith("php-fpm-")
    if not (is_php or svc in webservers): continue
    if re.match(r"^    volumes:\s*$", l) and not block_has(svc_start[svc], MOUNT):
        out.append("      - " + MOUNT); changed = True
    if is_php and re.match(r"^    environment:\s*$", l) and not block_has(svc_start[svc], "PHPFPM_LISTEN="):
        out.append("      - " + ENV); changed = True
if changed:
    open(path, "w").write("\n".join(out))
print("patched" if changed else "unchanged")
PY
}
