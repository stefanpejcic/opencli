#!/bin/bash
################################################################################
# Script Name: email/webmail.sh
# Description: Display webmail domain or choose webmail software
# Usage: opencli email-webmail [roundcube|snappymail|sogo] [--debug]
# Docs: https://docs.openpanel.com
# Author: 27.08.2024
# Created: 18.08.2024
# Last Modified: 23.02.2025
# Company: openpanel.com
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


usage() {
    echo "Usage: opencli email-webmail {roundcube|snappymail|sogo}"
    echo
    echo "Examples:"
    echo "  opencli email-webmail               # Display webmail domain."
    echo "  opencli email-webmail roundcube     # Set RoundCube as webmail."
    echo "  opencli email-webmail snappymail    # Set SnappyMail as webmail."
    echo "  opencli email-webmail sogo          # Set SoGo as webmail."
    echo ""
    exit 1
}



if [ "$#" -gt 2 ]; then
    usage
fi

DEBUG=false  # Default value for DEBUG
SNAPPYMAIL=false
ROUNDCUBE=false
SOGO=false
WEBMAIL_PORT=""  # Port is now empty as we use domain proxy only



# ENTERPRISE
ENTERPRISE="/usr/local/admin/core/scripts/enterprise.sh"
PANEL_CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
PROXY_FILE="/etc/openpanel/caddy/redirects.conf"
key_value=$(grep "^key=" $PANEL_CONFIG_FILE | cut -d'=' -f2-)

# Check if 'enterprise edition'
if [ -n "$key_value" ]; then
    :
else
    echo "Error: OpenPanel Community edition does not support emails. Please consider purchasing the Enterprise version that allows unlimited number of email addresses."
    source $ENTERPRISE
    echo "$ENTERPRISE_LINK"
    exit 1
fi



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
            echo "Setting SoGo as Webmail software"
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



get_domain_for_webmail() {
    # Check if the file exists
    if [[ ! -f "$PROXY_FILE" ]]; then
        echo "Configuration file not found at $PROXY_FILE"
        exit 1
    fi

    # Use grep and awk to extract the domain from the /webmail block
    domain=$(grep "^redir @webmail" "$PROXY_FILE" | awk '{print $3}' | sed -e 's|https://||' -e 's|http://||' -e 's|:.*||')

    if [[ -n "$domain" ]]; then
        echo "$domain"
    else
        echo "No domain configured for /webmail"
        exit 0
    fi
}




cd /usr/local/mail/openmail

if [ "$SNAPPYMAIL" = true ]; then
  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- STOPPING EXISTING WEBMAIL SOFTWARE ------------------"
      echo ""
      echo "Stopping RoundCube:"
    docker --context default compose rm -s -v roundcube
      echo "Stopping SoGO:"
    docker --context default compose rm -s -v sogo
      echo ""
      echo "----------------- STARTING SNAPPYMAIL ------------------"
      echo ""
    docker --context default compose up -d snappymail
  else
    docker --context default compose rm -s -v roundcube >/dev/null 2>&1
    docker --context default compose rm -s -v sogo >/dev/null 2>&1
    docker --context default compose up -d snappymail >/dev/null 2>&1
  fi
elif [ "$ROUNDCUBE" = true ]; then
    docker --context default compose rm -s -v snappymail >/dev/null 2>&1
    docker --context default compose rm -s -v sogo >/dev/null 2>&1
    docker --context default compose up -d roundcube
elif [ "$SOGO" = true ]; then
    docker --context default compose rm -s -v roundcube >/dev/null 2>&1
    docker --context default compose rm -s -v snappymail >/dev/null 2>&1
    docker --context default compose up -d sogo
else
    get_domain_for_webmail    # display domain only
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


configure_webmail_access() {
    # Check if we're using domain proxy or direct access
    if [ -n "$WEBMAIL_PORT" ]; then
        echo "[WARNING] Direct port access to webmail is enabled. This is not recommended for security!"
        echo "          Consider switching to domain proxy only by running: opencli config update webmail_port \"\""
    else
        # Using domain proxy only (more secure)
        echo "Webmail is configured to use domain proxy only (secure configuration)"

        # Ensure nginx proxy configuration exists
        if [ -d "/etc/nginx/conf.d/" ]; then
            # Create or update webmail proxy configuration
            cat > /etc/nginx/conf.d/webmail-proxy.conf << EOF
# Webmail proxy configuration - secure domain proxy only
server {
    listen 80;
    server_name webmail.$HOSTNAME mail.$HOSTNAME;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        # Security headers
        proxy_set_header X-Content-Type-Options nosniff;
        proxy_set_header X-XSS-Protection "1; mode=block";
        proxy_set_header X-Frame-Options "SAMEORIGIN";
        proxy_set_header Referrer-Policy "strict-origin-when-cross-origin";
    }

    # Block access to sensitive files
    location ~ /\.(ht|git) {
        deny all;
    }
}
EOF
            # Test and reload nginx
            nginx -t && systemctl reload nginx
        fi
    fi
}

configure_webmail_access

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
