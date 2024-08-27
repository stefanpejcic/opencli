#!/bin/bash
################################################################################
# Script Name: email/webmail.sh
# Description: Choose Webmail software
# Usage: opencli email-webmail <roundcube|snappymail|sogo> [--debug]
# Docs: https://docs.openpanel.co/docs/admin/scripts/emails#webmail
# Author: 27.08.2024
# Created: 18.08.2024
# Last Modified: 27.08.2024
# Company: openpanel.co
# Copyright (c) openpanel.co
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


if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
    usage
fi

DEBUG=false  # Default value for DEBUG


# Parse optional flags to enable debug mode when needed
while [[ "$#" -gt 1 ]]; do
    case $2 in
        --debug) DEBUG=true ;;
    esac
    shift
done


usage() {
    echo "Usage: opencli email-webmail {roundcube|snappymail|sogo}"
    echo
    echo "Examples:"
    echo "  opencli email-webmail roundcube     # Set RoundCube as webmail."
    echo "  opencli email-webmail snappymail    # Set SnappyMail as webmail."
    echo "  opencli email-webmail sogo          # Set SoGo as webmail."
    echo ""
    exit 1
}


SNAPPYMAIL=false
ROUNDCUBE=false
SOGO=false


while [[ "$#" -gt 0 ]]; do
    case $1 in
        roundcube)
            echo "Setting RoundCube as Webmail software."
            SNAPPYMAIL=false
            ROUNDCUBE=true
            SOGO=false
            ;;
        snappymail)
            echo "Setting SnappyMail as Webmail software."
            SNAPPYMAIL=true
            ROUNDCUBE=false
            SOGO=false
            ;;
        sogo)
            echo "Setting SoGo as Webmail software."
            SNAPPYMAIL=false
            ROUNDCUBE=false
            SOGO=true
            ;;
        *)
            echo "Invalid option: $1"
            usage
            ;;
    esac
    shift
done



cd /usr/local/mail/openmail

if [ "$SNAPPYMAIL" = true ]; then
    docker compose rm -s -v roundcube
    docker compose rm -s -v sogo
    docker compose up -d snappymail
elif [ "$DOVECOT" = true ]; then
    docker compose rm -s -v snappymail
    docker compose rm -s -v sogo
    docker compose up -d roundcube
elif [ "$SOGO" = true ]; then
    docker compose rm -s -v roundcube
    docker compose rm -s -v snappymail
    docker compose up -d sogo
else
    usage
fi








function open_port_csf() {
    local port=$1
    local csf_conf="/etc/csf/csf.conf"
    
    # Check if port is already open
    port_opened=$(grep "TCP_IN = .*${port}" "$csf_conf")
    if [ -z "$port_opened" ]; then
        # Open port
        sed -i "s/TCP_IN = \"\(.*\)\"/TCP_IN = \"\1,${port}\"/" "$csf_conf"
        echo "Port ${port} is now opened in CSF."
        ports_opened=1
    else
        echo "Port ${port} is already open in CSF."
    fi
}

# TODO: 8080 should be disabled and instead allow doamin proxy only!

# CSF
if command -v csf >/dev/null 2>&1; then
    open_port_csf 8080    
# UFW
elif command -v ufw >/dev/null 2>&1; then
    ufw allow 8080
else
    echo "Warning: Neither CSF nor UFW are installed. In order for Webmail to work, make sure port 8080 is opened on external firewall.."
fi




