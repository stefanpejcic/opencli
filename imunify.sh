#!/bin/bash
################################################################################
# Script Name: imunify.sh
# Description: Install and manage ImunifyAV service.
# Usage: opencli imunify [status|start|stop|install|uninstall]
# Docs: https://docs.openpanel.com
# Author: Stefan Pejcic
# Created: 04.08.2025
# Last Modified: 04.08.2025
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

readonly SERVICE_NAME="ImunifyAV"



update_version() {
  local PANEL_INFO_JSON="/etc/sysconfig/imunify360/get-panel-info.json"
opencli_version=$(opencli version)
  cat <<EOF > "$PANEL_INFO_JSON"
{
  "data": {
    "name": "OpenPanel",
    "version": "$opencli_version"
  },
  "metadata": {
    "result": "ok"
  }
}
EOF
}

status_av() {
  if pgrep -u _imunify -f "php -S 0.0.0.0:9000" >/dev/null; then
    echo "Imunify GUI is running."
    ps -u _imunify -f | grep "php -S 0.0.0.0:9000"
  else
    echo "Imunify GUI is not running."
  fi
}

install_av() {

echo "Creating directories..."
mkdir -p /etc/sysconfig/imunify360/

PAM_DENY_FILE="/etc/pam.d/imunify360-deny"
if [ ! -f "$PAM_DENY_FILE" ]; then
  echo "Creating pam_deny.so file..."
  cat <<EOF > "$PAM_DENY_FILE"
auth required pam_deny.so
account required pam_deny.so
EOF
else
  echo "$PAM_DENY_FILE already exists, skipping..."
fi

INTEGRATION_CONF="/etc/sysconfig/imunify360/integration.conf"
if [ ! -f "$INTEGRATION_CONF" ]; then
  echo "Creating integration.conf file..."
  cat <<EOF > "$INTEGRATION_CONF"
[paths]
ui_path = /etc/sysconfig/imunify360/imav
ui_path_owner = _imunify:_imunify 

[pam]
service_name = imunify360-deny

[integration_scripts]
panel_info = /etc/sysconfig/imunify360/get-panel-info.sh

[malware]
basedir = /home
pattern_to_watch = ^/home/[^/]+/docker-data/volumes/[^/]+_html_data/_data(/.*)?$
EOF
else
  echo "$INTEGRATION_CONF already exists, skipping..."
fi

PANEL_INFO_JSON="/etc/sysconfig/imunify360/get-panel-info.json"
if [ ! -f "$PANEL_INFO_JSON" ]; then
  update_version
else
  echo "$PANEL_INFO_JSON already exists, skipping..."
fi

DEPLOY_SCRIPT="imav-deploy.sh"
if [ ! -f "$DEPLOY_SCRIPT" ]; then
  echo "Downloading deploy script..."
  wget https://repo.imunify360.cloudlinux.com/defence360/imav-deploy.sh -O "$DEPLOY_SCRIPT"
else
  echo "$DEPLOY_SCRIPT already downloaded, skipping..."
fi

if ! grep -q "# Deployed by imav-deploy" "$DEPLOY_SCRIPT"; then
  echo "Running deploy script..."
  bash "$DEPLOY_SCRIPT"
else
  echo "Deploy script already executed or invalid, skipping..."
fi

echo "Installing PHP if not present..."
if ! command -v php >/dev/null 2>&1; then
  apt-get update
  apt-get install -y php
else
  echo "PHP already installed."
fi

echo "Install completed!"
}



uninstall_av() {
  echo "Removing files and directories..."
  rm -rf /etc/sysconfig/imunify360/
  rm -f /etc/pam.d/imunify360-deny
  rm -f imav-deploy.sh
  rm -f /var/log/imunify-php-server.log

  echo "Checking for _imunify user..."
  if id "_imunify" >/dev/null 2>&1; then
    echo "User '_imunify' exists. Not removing automatically to avoid breaking dependencies."
    echo "→ If you're sure, run: userdel _imunify"
  fi

  echo "Uninstall complete."
}


start_av() {
  echo "Starting webserver..."
  pkill -u _imunify -f "php -S 0.0.0.0:9000" 2>/dev/null || true

  chown -R _imunify /etc/sysconfig/imunify360/
  if ! pgrep -f "php -S 0.0.0.0:9000" >/dev/null; then
    nohup sudo -u _imunify php -S 0.0.0.0:9000 -t /etc/sysconfig/imunify360/ > /var/log/imunify-php-server.log 2>&1 &
    echo "Webserver started."
  else
    echo "Webserver already running."
  fi
}

stop_av() {
  echo "Stopping webserver..."
  pkill -f "php -S 0.0.0.0:9000"
  if ! pgrep -f "php -S 0.0.0.0:9000" >/dev/null; then
    echo "Webserver stopped."
  else
    echo "Failed to stop webserver."
  fi
}





# MAIN
case "$1" in
    status)
        echo "Checking status for $SERVICE_NAME GUI..."
        status_av
        exit 0
        ;;
    install)
        echo "Installing $SERVICE_NAME..."
        install_av
        exit 0
        ;;
    uninstall)
        echo "Uninstalling $SERVICE_NAME..."
        stop_av
        uninstall_av
        exit 0
        ;;
    start)
        echo "Starting $SERVICE_NAME..."
        update_version
        start_av
        exit 0
        ;;
    stop)
        echo "Stopping $SERVICE_NAME..."
        stop_av
        exit 0
        ;;
    *)
        echo "Usage: opencli imunify {install|uninstall|start|stop}"
        exit 1
        ;;
esac
exit 0
