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



if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
    usage
fi

DEBUG=false  # Default value for DEBUG
SNAPPYMAIL=false
ROUNDCUBE=false
SOGO=false
WEBMAIL_PORT="8080" # TODO: 8080 should be disabled and instead allow domain proxy only!

while [[ "$#" -gt 0 ]]; do
    case $1 in
        --debug)
            echo ""
            echo "----------------- DISPLAY DEBUG INFORMATION ------------------"
            echo ""
            DEBUG=true
            ;;
        roundcube)
            echo "Setting RoundCube as Webmail software:"
            SNAPPYMAIL=false
            ROUNDCUBE=true
            SOGO=false
            ;;
        snappymail)
            echo "Setting SnappyMail as Webmail software:"
            SNAPPYMAIL=true
            ROUNDCUBE=false
            SOGO=false
            ;;
        sogo)
            echo "Setting SoGo as Webmail software""
            SNAPPYMAIL=false
            ROUNDCUBE=false
            SOGO=true
            ;;
        *)
            echo "Invalid option: $1"
            usage
            exit 1
            ;;
    esac
    shift
done




cd /usr/local/mail/openmail

if [ "$SNAPPYMAIL" = true ]; then
  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- STOPPING EXISTING WEBMAIL SOFTWARE ------------------"
      echo ""
      echo "Stopping RoundCube:"
    docker compose rm -s -v roundcube
      echo "Stopping SoGO:"
    docker compose rm -s -v sogo
      echo ""
      echo "----------------- STARTING SNAPPYMAIL ------------------"
      echo ""
    docker compose up -d snappymail
  else
    docker compose rm -s -v roundcube >/dev/null 2>&1
    docker compose rm -s -v sogo >/dev/null 2>&1
    docker compose up -d snappymail >/dev/null 2>&1
  fi
elif [ "$ROUNDCUBE" = true ]; then
    docker compose rm -s -v snappymail >/dev/null 2>&1
    docker compose rm -s -v sogo >/dev/null 2>&1
    docker compose up -d roundcube
elif [ "$SOGO" = true ]; then
    docker compose rm -s -v roundcube >/dev/null 2>&1
    docker compose rm -s -v snappymail >/dev/null 2>&1
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
      if [ "$DEBUG" = true ]; then
          echo ""
          echo "Opening port on ConfigServer Firewall"
          echo ""
          sed -i "s/TCP_IN = \"\(.*\)\"/TCP_IN = \"\1,${port}\"/" "$csf_conf"
          echo ""
      else
          sed -i "s/TCP_IN = \"\(.*\)\"/TCP_IN = \"\1,${port}\"/" "$csf_conf" >/dev/null 2>&1
      fi
      ports_opened=1
    else
      if [ "$DEBUG" = true ]; then
          echo "Port ${port} is already open in CSF."
      else
          echo "Port ${port} is already open in CSF." >/dev/null 2>&1
      fi
    fi
}


if [ "$DEBUG" = true ]; then
    echo ""
    echo "----------------- OPENING PORT 8080 ON FIREWALL ------------------"
fi
# CSF
if command -v csf >/dev/null 2>&1; then
    open_port_csf $WEBMAIL_PORT    
# UFW
elif command -v ufw >/dev/null 2>&1; then
      if [ "$DEBUG" = true ]; then
          echo "Opening port on UncomplicatedFirewall"
          echo ""
          ufw allow $WEBMAIL_PORT
          echo ""
      else
          ufw allow $WEBMAIL_PORT >/dev/null 2>&1
      fi

else
      if [ "$DEBUG" = true ]; then
          echo "Warning: Neither CSF nor UFW are installed. In order for Webmail to work, make sure port 8080 is opened on external firewall.."
      else
          :
      fi
fi




