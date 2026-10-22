#!/bin/bash
################################################################################
# Script Name: domains/unsuspend.sh
# Description: Unsuspend a domain name
# Usage: opencli domains-unsuspend <DOMAIN-NAME> [--by=admin|user]
# Author: Stefan Pejcic
# Created: 04.11.2024
# Last Modified: 22.10.2026
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

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
    echo "Usage: opencli domains-unsuspend <domain_name> [--by=admin|user]"
    exit 1
fi

domain_name="$1"
# goes straight into the caddy file path
[[ "$domain_name" =~ ^[A-Za-z0-9.-]+$ && "$domain_name" != *".."* ]] || { echo "ERROR: Invalid domain name: $domain_name"; exit 1; }
by="user"

if [ -n "$2" ]; then
    case "$2" in
        --by=admin|--by=user) by="${2#--by=}" ;;
        *) echo "Unknown option: $2"; exit 1 ;;
    esac
fi

domain_vhost="/etc/openpanel/caddy/domains/$domain_name.conf"
suspended_vhost="/etc/openpanel/caddy/suspended_domains/$domain_name.conf"

[ -f "$suspended_vhost" ] || { echo "Domain $domain_name is not suspended."; exit 0; }
# while the account is suspended that copy is the only real vhost, don't touch it
grep -q "suspended_user.html" "$domain_vhost" 2>/dev/null && { echo "ERROR: account owning $domain_name is suspended, unsuspend it first"; exit 1; }
# a copy left behind by an older unsuspend must not overwrite a live, unsuspended vhost
grep -qE '^# suspended_by:|suspended_domain\.html' "$domain_vhost" 2>/dev/null || { rm -f "$suspended_vhost"; echo "Domain $domain_name is not suspended."; exit 0; }

if [ "$by" != "admin" ] && grep -q '^# suspended_by: admin' "$domain_vhost"; then
    echo "ERROR: Domain $domain_name was suspended by the administrator and can only be unsuspended by them."
    exit 1
fi

# 1. restore vhost, moved so the next suspend doesn't think it's still suspended
mv "$suspended_vhost" "$domain_vhost"  > /dev/null 2>&1

# 2. reload caddy
nohup podman exec caddy sh -c "caddy validate && caddy reload" > /dev/null 2>&1 &
disown

# 3. notify
nohup opencli sentinel --action=domains_status --title="Domain name $domain_name unsuspended" --message="Domain name $domain_name has been unsuspended." >/dev/null 2>&1 &
disown

echo "Domain unsuspended successfully."
