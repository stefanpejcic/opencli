#!/bin/bash
################################################################################
# Script Name: domains/suspend.sh
# Description: Suspend a domain name
# Usage: opencli domains-suspend <DOMAIN-NAME> [--comment="<COMMENT>"] [--by=admin|user] [--template=<NAME>]
# Author: Stefan Pejcic
# Created: 04.11.2024
# Last Modified: 15.10.2026
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

if [ $# -lt 1 ] || [ $# -gt 4 ]; then
    echo "Usage: opencli domains-suspend <domain_name> [--comment='reason'] [--by=admin|user] [--template=name]"
    exit 1
fi

domain_name="$1"
comment=""
by="user"
template=""
reason=""

shift
for arg in "$@"; do
    case "$arg" in
        --comment=*) comment="${arg#--comment=}" ;;
        --by=*) by="${arg#--by=}" ;;
        --template=*) template="${arg#--template=}" ;;
        *) echo "Unknown option: $arg"; exit 1 ;;
    esac
done

[[ "$by" == "admin" || "$by" == "user" ]] || { echo "ERROR: --by must be admin or user"; exit 1; }

# comment is written into the caddy file, so newlines would let callers inject directives
comment=$(printf '%s' "$comment" | tr -d '[:cntrl:]')

domain_vhost="/etc/openpanel/caddy/domains/$domain_name.conf"
suspended_dir="/etc/openpanel/caddy/suspended_domains/"
conf_template="/etc/openpanel/caddy/templates/suspended.conf"
page="/suspended_domain.html"

if [ -n "$template" ]; then
    [[ "$template" =~ ^[a-z0-9_-]+$ ]] || { echo "ERROR: invalid template name: $template"; exit 1; }
    [ -f "/etc/openpanel/caddy/templates/suspended/$template.html" ] || { echo "ERROR: suspension template '$template' does not exist"; exit 1; }
    page="/suspended/$template.html"
    # display name sits in the first line as <!-- name: ... -->, users see it as the reason
    reason=$(head -n1 "/etc/openpanel/caddy/templates/suspended/$template.html" | sed -n 's/^<!-- name: \(.*\) -->$/\1/p' | tr -d '[:cntrl:]')
    reason="${reason:-$template}"
fi

[ -f "$domain_vhost" ] || { echo "ERROR: vhost file for domain $domain_name does not exist"; exit 1; }
# copying the account-suspended vhost would overwrite the real one in suspended_dir
grep -q "suspended_user.html" "$domain_vhost" && { echo "ERROR: account owning $domain_name is suspended, unsuspend it first"; exit 1; }

# the live caddy file says whether it's suspended, a copy left in suspended_dir alone doesn't
if grep -qE '^# suspended_by:|suspended_domain\.html' "$domain_vhost" 2>/dev/null; then
    current_by=$(sed -n 's/^# suspended_by: //p' "$domain_vhost" | head -n1)
    # admin can take over or update a suspension, users can't touch an admin one
    if [ "$by" != "admin" ] || { [ "$current_by" = "admin" ] && [ -z "$template" ] && [ -z "$comment" ]; }; then
        echo "Domain is already suspended."
        exit 0
    fi
else
    # 1. cp vhost to suspended-dir
    mkdir -p $suspended_dir
    cp "$domain_vhost" $suspended_dir  > /dev/null 2>&1
fi

# 2. create file based on suspended domain template, comment stays on line 1 since the user panel reads it there
{
    [ -n "$comment" ] && printf '# comment: %s\n' "$comment"
    printf '# suspended_by: %s\n' "$by"
    [ -n "$reason" ] && printf '# reason: %s\n' "$reason"
    sed -e "s|<DOMAIN_NAME>|$domain_name|g" -e "s|/suspended_domain\.html|$page|g" "$conf_template"
} > "$domain_vhost"

# 3. reload caddy
nohup podman exec caddy sh -c "caddy validate && caddy reload" > /dev/null 2>&1 &
disown

# 4. notify
message="Domain name $domain_name has been suspended by $by.${reason:+ Reason: $reason.}${comment:+ Comment: $comment}"
nohup opencli sentinel --action=domains_status --title="Domain name $domain_name suspended" --message="$message" >/dev/null 2>&1 &
disown

echo "Domain suspended successfully."
