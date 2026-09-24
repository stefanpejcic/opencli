#!/bin/bash
################################################################################
# Script Name: ftp/add.sh
# Description: Display all active FTP connections, or only those for domains owned by an OpenPanel user.
# Usage: opencli ftp-connections [OPENPANEL_USERNAME]
# Docs: https://docs.openpanel.com
# Author: Stefan Pejcic
# Created: 11.09.2024
# Last Modified: 21.08.2026
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

if [ "$#" -gt 1 ]; then
    echo "Usage: opencli ftp-connections [openpanel_username]"
    exit 1
fi

# ======================================================================
# Main
if [ -n "$1" ]; then
    # FTP sub-users are named user@domain, so only show connections for domains this user owns
    domains=$(opencli domains-user "$1" 2>/dev/null | grep -E '^[A-Za-z0-9.-]+\.[A-Za-z0-9-]+$')
    if [ -z "$domains" ]; then
        echo "ERROR: No domains found for user '$1'. Aborting!"
        exit 1
    fi
    pattern="@($(sed 's/\./\\./g' <<< "$domains" | paste -sd'|')):"
    podman exec openadmin_ftp sh -c 'ps | grep "vsftpd:" | grep -w -v grep' | grep -E -- "$pattern"
else
    podman exec openadmin_ftp sh -c 'ps | grep "vsftpd:" | grep -w -v grep'
fi
