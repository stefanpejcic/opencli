#!/bin/bash
################################################################################
# Script Name: email/server.sh
# Description: Manage mailserver
# Usage: opencli email-server <install|start|restart|stop|uninstall> [--debug]
# Docs: https://docs.openpanel.co/docs/admin/scripts/emails#server
# Author: Stefan Pejcic
# Created: 18.08.2024
# Last Modified: 18.08.2024
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
    echo "Usage: opencli email-server {install|start|restart|stop|uninstall}"
    echo
    echo "Commands:"
    echo "  install    - Installs the email server and its dependencies."
    echo "  start      - Starts the email server service."
    echo "  restart    - Restarts the email server service."
    echo "  stop       - Stops the email server service."
    echo "  uninstall  - Uninstalls the email server and removes all data."
    echo
    echo "Examples:"
    echo "  opencli email-server install    # Install the email server"
    echo "  opencli email-server start      # Start the email server"
    echo "  opencli email-server restart    # Restart the email server"
    echo "  opencli email-server stop       # Stop the email server"
    echo "  opencli email-server uninstall  # Uninstall the email server"
    exit 1
}



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

# CONFIG
MAIL_CONTAINER_DIR="/usr/local/mail/openmail/"
GITHUB_REPO="https://github.com/stefanpejcic/openmail"
# INSTALL
install_mailserver(){
  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- INSTALLING MAILSERVER ------------------"
      echo ""
      echo "Downloading from $GITHUB_REPO"
      echo ""
      mkdir -p /usr/local/mail/
      cd /usr/local/mail/ && git clone $GITHUB_REPO
      cd openmail && bash setup.sh
      mkdir -p /etc/openpanel/email/snappymail
      cp snappymail.ini /etc/openpanel/email/snappymail/config.ini
      docker compose up -d mailserver
  else
      mkdir -p /usr/local/mail/  >/dev/null 2>&1
      cd /usr/local/mail/ && git clone $GITHUB_REPO >/dev/null 2>&1
      cd openmail && bash setup.sh >/dev/null 2>&1
      mkdir -p /etc/openpanel/email/snappymail >/dev/null 2>&1
      cp snappymail.ini /etc/openpanel/email/snappymail/config.ini >/dev/null 2>&1
      docker compose up -d mailserver >/dev/null 2>&1
  fi


  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- CONFIGURING FIREWALL ------------------"
      echo ""
  fi
  
  
  function open_port_csf() {
      local port=$1
      local csf_conf="/etc/csf/csf.conf"
      
      # Check if port is already open
      port_opened=$(grep "TCP_IN = .*${port}" "$csf_conf")
      if [ -z "$port_opened" ]; then
          # Open port
          sed -i "s/TCP_IN = \"\(.*\)\"/TCP_IN = \"\1,${port}\"/" "$csf_conf"
          echo "Port ${port} opened in CSF."
          ports_opened=1
      else
          echo "Port ${port} is already open in CSF."
      fi
  }
  
  
  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- CONFIGURING FIREWALL ------------------"
      echo ""
   # CSF
    if command -v csf >/dev/null 2>&1; then
        open_port_csf 25
        open_port_csf 143
        open_port_csf 465
        open_port_csf 587
        open_port_csf 993
    # UFW
    elif command -v ufw >/dev/null 2>&1; then
        ufw allow 25
        #ufw allow 8080 && \ #uncomment to expose webmail
        ufw allow 143
        ufw allow 465
        ufw allow 587
        ufw allow 993
    else
        echo "Error: Neither CSF nor UFW are installed. make sure ports 25 243 465 587 and 993 are opened on external firewall, or email will not work."
    fi
  else

    # CSF
    if command -v csf >/dev/null 2>&1; then
        open_port_csf 25 >/dev/null 2>&1
        open_port_csf 143 >/dev/null 2>&1
        open_port_csf 465 >/dev/null 2>&1
        open_port_csf 587 >/dev/null 2>&1
        open_port_csf 993 >/dev/null 2>&1
        
    # UFW
    elif command -v ufw >/dev/null 2>&1; then
        ufw allow 25 >/dev/null 2>&1
        #ufw allow 8080 && \ #uncomment to expose webmail
        ufw allow 143 >/dev/null 2>&1
        ufw allow 465 >/dev/null 2>&1
        ufw allow 587 >/dev/null 2>&1
        ufw allow 993 >/dev/null 2>&1
    else
        echo "Error: Neither CSF nor UFW are installed. make sure ports 25 243 465 587 and 993 are opened on external firewall, or email will not work."
    fi

fi

#########

  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- ENABLE MAIL FOR EXISTING USERS ------------------"
      echo ""
  fi
  
  user_list=$(opencli user-list --json)
  
  ensure_jq_installed() {
      # Check if jq is installed
      if ! command -v jq &> /dev/null; then
          # Install jq using apt
          sudo apt-get update > /dev/null 2>&1
          sudo apt-get install -y -qq jq > /dev/null 2>&1
          # Check if installation was successful
          if ! command -v jq &> /dev/null; then
              echo "Error: jq installation failed. Please install jq manually and try again."
              exit 1
          fi
      fi
  }
  
  ensure_jq_installed
    
  # Loop through each user
  echo "$user_list" | jq -c '.[]' | while read -r user; do
      username=$(echo "$user" | jq -r '.username')
      if [[ "$username" != *"_"* ]]; then
          echo "Enabling emails for: $username"
          docker network connect openmail_network "$username"
      else
          echo "Skipping suspended user $username"
      fi
  done

# at end lets add all domains
process_all_domains_and_start
  
}

# START
process_all_domains_and_start(){
  CONFIG_DIR="/etc/nginx/sites-available"
  COMPOSE_FILE="/usr/local/mail/openmail/compose.yml"
  new_volumes="    volumes:\n      - ./docker-data/dms/mail-state/:/var/mail-state/\n      - ./var/log/mail/:/var/log/mail/\n      - ./docker-data/dms/config/:/tmp/docker-mailserver/\n      - /etc/localtime:/etc/localtime:ro\n"

  cp "$COMPOSE_FILE" "$COMPOSE_FILE.bak"
  
  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- DEBUG INFORMATION ------------------"
      echo ""
      echo "Re-mounting mail directories for all doamins:"
      echo ""
      echo "- DOMAINS DIRECTORY: $CONFIG_DIR" 
      echo "- MAIL SETTINGS FILE: $COMPOSE_FILE"
      printf "%b" "- DEFAULT VOLUMES:\n$new_volumes"
  fi
    
  total_domains=0
  
  for file in "$CONFIG_DIR"/*.conf; do
      if [ ! -L "$file" ]; then
          while IFS= read -r line; do
              if [[ $line =~ include[[:space:]]/etc/openpanel/openpanel/core/users/([^/]+)/domains/.*-block_ips\.conf ]]; then
                  USERNAME="${BASH_REMATCH[1]}"
                  DOMAIN=$(basename "$file" .conf)
                  DOMAIN_DIR="/home/$USERNAME/mail/$DOMAIN/"
                  new_volumes+="      - $DOMAIN_DIR:/var/mail/$DOMAIN/\n"
  
                  ((total_domains++))
  
              fi
          done < "$file"
      fi
  done
  
  
  if [ "$DEBUG" = true ]; then
      echo "- TOTAL DOMAINS: $total_domains"
      echo ""
      echo "----------------- EMAIL DIRECTORIES ------------------"
      echo ""
      printf "%b" "- DEFAULT VOLUMES + VOLUMES PER DOMAIN:\n$new_volumes"
      echo ""
      echo "----------------- UPDATE COMPOSE ------------------"
      echo ""
  else
  	echo "Processing $total_domains domains"
  fi
  
  
  
  awk -v new_volumes="$new_volumes" '
  BEGIN { in_mailserver=0; }
  /^  mailserver:/ { in_mailserver=1; print; next; }
  /^  [a-z]/ { in_mailserver=0; }  # End of mailserver section if a new service starts
  {
      if (in_mailserver) {
          if ($1 == "volumes:") {
              print new_volumes
              while (getline > 0) {
                  if (/^[ ]{6}-[ ]+\/home/) {
                      continue
                  }
                  if (!/^[ ]{6}-/) {
                      print $0
                      break
                  }
              }
              in_mailserver=0
          } else {
              print
          }
      } else {
          print
      }
  }
  ' "$COMPOSE_FILE.bak" > "$COMPOSE_FILE"
  
  
  if [ "$DEBUG" = true ]; then
  	echo "compose.yml has been updated with the new volumes."
      echo ""
      echo "----------------- RESTART MAILSERVER ------------------"
      echo ""
  	cd $MAIL_CONTAINER_DIR && docker compose up -d mailserver
      echo ""
  else
  	cd $MAIL_CONTAINER_DIR && docker compose up -d mailserver >/dev/null 2>&1
  	echo "MailServer started successfully."
  fi
  

}

# STOP
stop_mailserver_if_running(){

  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- STOP MAILSERVER ------------------"
      echo ""
  	  cd $MAIL_CONTAINER_DIR && docker compose down mailserver
      echo ""
  else
  	cd $MAIL_CONTAINER_DIR && docker compose down mailserver >/dev/null 2>&1
  	echo "MailServer stopped succesfully."
  fi
  
}

# UNINSTALL
remove_mailserver_and_all_config(){
  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- UNINSTALL MAILSERVER ------------------"
      echo ""
  fi

  echo "Are you sure you want to uninstall the MailServer and remove all its configuration? (yes/no)"
  read -t 10 -n 1 user_input

  if [ $? -ne 0 ]; then
    echo ""
    echo "No response received. Aborting uninstallation."
    return
  fi

  if [[ "$user_input" != "y" && "$user_input" != "Y" && "$user_input" != "yes" ]]; then
    echo ""
    echo "Uninstallation aborted."
    return
  fi

  if [ "$DEBUG" = true ]; then
      cd $MAIL_CONTAINER_DIR && docker compose down
      rm -rf $MAIL_CONTAINER_DIR
      echo ""
  else
      cd $MAIL_CONTAINER_DIR && docker compose down >/dev/null 2>&1
      rm -rf $MAIL_CONTAINER_DIR >/dev/null 2>&1
      echo "MailServer uninstalled successfully."
  fi
}



# Parse the command line argument
case "$1" in
    install)
        echo "Installing the mailserver..."
        install_mailserver
        ;;
    start)
        echo "Starting mailserver..."
        process_all_domains_and_start
        ;;
    restart)
        echo "Restarting the mailserver..."
        stop_mailserver_if_running
        process_all_domains_and_start
        ;;
    stop)
        echo "Stopping the mailserver..."
        stop_mailserver_if_running
        ;;
    uninstall)
        echo "Uninstalling the mailsserver..."
        remove_mailserver_and_all_config
        ;;
    *)
        usage
        ;;
esac




