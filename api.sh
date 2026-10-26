#!/bin/bash
################################################################################
# Script Name: api.sh
# Description: Check status, enable or disable OpenAdmin API access, and list available API endpoints.
# Usage: opencli api <status|on|off|list>
# Author: Stefan Pejcic
# Created: 04.09.2024
# Last Modified: 26.10.2026
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

# ======================================================================
# Helpers

show_usage() {
    cat <<EOF

Usage: opencli api {status|on|off|list}
Docs:  https://openpanel.com/docs/articles/dev-experience/openadmin-api

Examples:
  opencli api status              Display current OpenAdmin API status.
  opencli api on                  Enable OpenAdmin API access.
  opencli api off                 Disable OpenAdmin API access.
  opencli api list                List available API endpoints with curl examples.

EOF
}

# the list is built into the openadmin binary, from the same file as the swagger docs on Settings > API
list_endpoints() {
    local bin url
    bin=$(grep -oP '^ExecStart=\K\S+' /etc/openpanel/openadmin/service/openadmin.service 2>/dev/null)
    # old installs still have gunicorn there
    [[ "$bin" == /usr/local/admin/openadmin-* && -x "$bin" ]] || bin=$(ls /usr/local/admin/openadmin-* 2>/dev/null | head -n1)
    [[ -x "$bin" ]] || { echo "ERROR: OpenAdmin binary not found in /usr/local/admin/"; exit 1; }
    url=$(opencli admin 2>/dev/null | grep -oE 'https?://[^ ]+' | head -n1)
    url="${url:-https://localhost:2087/}"
    "$bin" api-endpoints "${url%/}" || { echo "ERROR: This OpenAdmin version can't list endpoints, update it with: opencli update --admin"; exit 1; }
}

# ======================================================================
# Validators

if [[ -z "$1" ]]; then
  show_usage
  exit 1
fi

license_key=$(grep "^key=" "/etc/openpanel/openpanel/conf/openpanel.config" | cut -d'=' -f2-)
[ -n "$license_key" ] || {
    echo "Error: OpenPanel Community edition does not support API access. Please consider purchasing the Enterprise version that has remote API access and integrations with billing softwares such as WHMCS and FOSSBilling."
    source "/usr/local/opencli/lib/enterprise.sh"
    echo "$ENTERPRISE_LINK"
    exit 1
}


# ======================================================================
# Main
case "$1" in
  status) opencli config get api ;;
  on|off)
    opencli config update api "$1"
    nohup opencli sentinel --action=admin_api --title="OpenAdmin API is $1" --message="API access for the administrator-level panel is now: '$1'." >/dev/null 2>&1 &
    disown
    if systemctl is-active --quiet admin; then
        systemctl restart admin 2>&1
    fi
    ;;
  list) list_endpoints ;;
  *)    echo "Invalid option: $1"; show_usage; exit 1 ;;
esac
