#!/bin/bash
################################################################################
# Script Name: phpmyadmin.sh
# Description: Run phpmyadmin for OpenPanel users
# Usage: opencli phpmyadmin [ --enable | --disable | --status ]
# Author: Stefan Pejcic
# Created: 03.07.2024
# Last Modified: 03.07.2024
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

# todo: --enable should check if already running and kill continer first



# Define constants
CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
DOCKER_IMAGE="phpmyadmin/phpmyadmin"
CONTAINER_NAME="openpanel_phpmyadmin"
PORT_MAPPING="8080:80"
CONFIGURED_URL=""

# Function to get PMA_URL from the config file
get_pma_url() {
  if [ -f "$CONFIG_FILE" ]; then
    CONFIGURED_URL=$(grep -oP 'pma_url=\K\S+' "$CONFIG_FILE")
  fi
}

# Function to get list of users from opencli
get_users() {
  USERS_JSON=$(opencli user-list --json)
  USERNAMES=$(echo "$USERS_JSON" | jq -r '.[].username' | paste -sd "," -)
}

# Function to get list of networks from opencli
get_networks() {
  NETWORKS_JSON=$(opencli plan-list --json | sed '1d')  # Remove the first line "Plans:"
  NETWORK_NAMES=$(echo "$NETWORKS_JSON" | jq -r '.[].name')
}

# Function to create nginx vhost file
create_vhost() {
  DOMAIN=$(echo "$CONFIGURED_URL" | awk -F[/:] '{print $4}')
  VHOST_FILE="/etc/nginx/sites-available/$DOMAIN"

  cat <<EOL > "$VHOST_FILE"
server {
    listen 80;
    server_name $DOMAIN;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }


    # /username to ?server=username
    location ~ ^/(?<username>[^/]+) {
        proxy_pass http://127.0.0.1:8080/?server=\$username;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }

}
EOL

  ln -s "$VHOST_FILE" "/etc/nginx/sites-enabled/"
  systemctl reload nginx
}

# Function to delete nginx vhost file
delete_vhost() {
  DOMAIN=$(echo "$CONFIGURED_URL" | awk -F[/:] '{print $4}')
  VHOST_FILE="/etc/nginx/sites-available/$DOMAIN"
  if [ -f "$VHOST_FILE" ]; then
    rm "$VHOST_FILE"
    rm "/etc/nginx/sites-enabled/$DOMAIN"
    systemctl reload nginx
  fi
}

# Function to generate SSL certificate
generate_ssl() {
  DOMAIN=$(echo "$CONFIGURED_URL" | awk -F[/:] '{print $4}')
  python3 /usr/bin/certbot --nginx --non-interactive --agree-tos -m webmaster@$DOMAIN -d $DOMAIN
}

# Function to start the container
start_container() {
  get_pma_url
  get_users
  get_networks

  if [ -n "$CONFIGURED_URL" ]; then
    PMA_URL_ARG="-e PMA_ABSOLUTE_URI=\"$CONFIGURED_URL\""
  else
    PMA_URL_ARG=""
  fi

  docker run -d --name "$CONTAINER_NAME" -p "$PORT_MAPPING" -e PMA_HOSTS="$USERNAMES" $PMA_URL_ARG $DOCKER_IMAGE

  for NETWORK in $NETWORK_NAMES; do
    docker network connect "$NETWORK" "$CONTAINER_NAME"
  done

  if [ -n "$CONFIGURED_URL" ]; then
    create_vhost
    if [[ "$CONFIGURED_URL" == https://* ]]; then
      generate_ssl
    fi
  fi
}

# Function to check the status of the container
check_status() {

  if docker ps | grep -q "$CONTAINER_NAME"; then
    #echo "Container $CONTAINER_NAME is running."
    get_pma_url

    if [ -n "$CONFIGURED_URL" ]; then
      echo "phpMyAdmin is available on $CONFIGURED_URL" 
    else

      # Get server ipv4 from ip.openpanel.co
      current_ip=$(curl -s https://ip.openpanel.co || wget -qO- https://ip.openpanel.co)

      # If site is not available, get the ipv4 from the hostname -I
      if [ -z "$current_ip" ]; then
         # current_ip=$(hostname -I | awk '{print $1}')
          # ip addr command is more reliable then hostname - to avoid getting private ip
          current_ip=$(ip addr|grep 'inet '|grep global|head -n1|awk '{print $2}'|cut -f1 -d/)
      fi
      echo "phpMyAdmin is available on ${current_ip}:8080" 
    fi

  else
    echo "Container $CONTAINER_NAME is not running."
  fi
}

# Function to stop the container
stop_container() {
  get_pma_url

  docker stop "$CONTAINER_NAME"
  docker rm "$CONTAINER_NAME"

  if [ -n "$CONFIGURED_URL" ]; then
    delete_vhost
  fi
}

# Main script logic
case "$1" in
  --enable)
    start_container
    ;;
  --disable)
    stop_container
    ;;
  --status)
    check_status
    ;;
  *)
    echo "Usage: $0 {--enable|--disable|--status}"
    exit 1
    ;;
esac
