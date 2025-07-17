#!/bin/bash
################################################################################
# Script Name: email/server.sh
# Description: Manage mailserver
# Usage: opencli email-server <install|start|restart|stop|uninstall> [--debug]
# Docs: https://docs.openpanel.com
# Author: Stefan Pejcic
# Created: 18.08.2024
# Last Modified: 17.07.2025
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
#set -ueo pipefail

APP="opencli email-server"
REPO="https://github.com/stefanpejcic/openmail"
DIR="/usr/local/mail/openmail"
CONTAINER=openadmin_mailserver
TIMEOUT=3600
DOCKER_COMPOSE="docker compose"
CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
ENTERPRISE_SCRIPT="/usr/local/admin/core/scripts/enterprise.sh"

# ─── FLAGS ────────────────────────────────────────────────────────────────────
DEBUG=false
for arg in "$@"; do
    case "$arg" in
        --debug) DEBUG=true; echo "--debug enabled";;
        -x) DEBUG=true; set -x; echo "-x (trace) enabled";;
    esac
done

# ─── UTILITY FUNCTIONS ────────────────────────────────────────────────────────
log() { [ "$DEBUG" = true ] && echo "$@"; }

check_bins() {
    local cmd
    for cmd in "$@"; do
        command -v "$cmd" &>/dev/null || { echo "Missing: $cmd"; exit 1; }
    done
    $DOCKER_COMPOSE version &>/dev/null || { echo "$DOCKER_COMPOSE not found"; exit 1; }
}

ensure_jq() {
    command -v jq &>/dev/null && return
    for pm in apt-get yum dnf; do
        if command -v "$pm" &>/dev/null; then
            sudo "$pm" install -y -q jq &>/dev/null && return
        fi
    done
    echo "Please install jq manually."; exit 1
}

_container() {
    [ "$1" = "-it" ] && shift && docker exec -it "$CONTAINER" "$@" || docker exec "$CONTAINER" "$@"
}

_status() {
    local indent=14
    local spaces status
    spaces=$(printf "%${indent}s")
    status=$(echo "$2" | fold -s -w $(($(tput cols) - 16)) | sed "s/^/$spaces/")
    printf "%-${indent}s%s\n" "$1:" "${status:$indent}"
}

_ports() {
    docker port "$CONTAINER"
}

_getDMSVersion() {
    _container bash -c 'cat /VERSION 2>/dev/null || echo "$DMS_RELEASE"'
}

check_enterprise() {
    if ! grep -q "^key=" "$CONFIG_FILE"; then
        echo "Community Edition does not support emails."
        source "$ENTERPRISE_SCRIPT"
        echo "$ENTERPRISE_LINK"
        exit 1
    fi
}

enable_email_module() {
    local enabled_modules
    enabled_modules=$(grep '^enabled_modules=' "$CONFIG_FILE" | cut -d'=' -f2)
    if ! grep -q 'emails' <<<"$enabled_modules"; then
        log "Enabling 'emails' module..."
        sed -i "s/^enabled_modules=.*/enabled_modules=${enabled_modules},emails/" "$CONFIG_FILE"
        docker ps -q -f name=openpanel &>/dev/null && docker restart openpanel || (cd /root && docker compose up -d openpanel)
    fi
}

open_port_csf() {
    local port=$1
    local conf="/etc/csf/csf.conf"
    grep -q "TCP_IN = .*${port}" "$conf" || sed -i "s/TCP_IN = \"/TCP_IN = \",${port}/" "$conf"
}

configure_csf_ports() {
    if command -v csf &>/dev/null; then
        for p in 25 143 465 587 993; do open_port_csf "$p"; done
    else
        echo "Warning: CSF not installed. Ensure email ports are open externally."
    fi
}

generate_reports() {
    git clone https://github.com/stefanpejcic/PFLogSumm-HTML-GUI.git /tmp/PFLogSumm-HTML-GUI &>/dev/null
    docker cp /tmp/PFLogSumm-HTML-GUI/pflogsummUIReport.sh "$CONTAINER":/opt/
    docker exec "$CONTAINER" bash /opt/pflogsummUIReport.sh
    mkdir -p /usr/local/admin/static/reports /usr/local/admin/templates/emails
    docker cp "$CONTAINER":/usr/local/admin/static/reports/reports.html /usr/local/admin/templates/emails/reports.html
    docker cp "$CONTAINER":/usr/local/admin/static/reports/data /usr/local/admin/templates/emails/
    rm -rf /tmp/PFLogSumm-HTML-GUI
}

install_mailserver() {
    log "Installing mailserver from $REPO"
    mkdir -p /usr/local/mail /etc/openpanel/email/snappymail
    cd /usr/local/mail && git clone "$REPO"
    set_ssl_for_mailserver
    cd "$DIR" && docker compose up -d mailserver roundcube
    enable_email_module
    configure_csf_ports
    process_domains_and_start
}



set_ssl_for_mailserver() {

	readonly MAILSERVER_ENV="/usr/local/mail/openmail/mailserver.env"	
	current_hostname=$(opencli domain)
	
	if [[ $current_hostname =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	    # an IP
	    log "Configuring mailserver to use IP address for IMAP/SMTP ..."
	    sed -i '/^SSL_TYPE=/c\SSL_TYPE=' "$MAILSERVER_ENV"
	    sed -i '/^SSL_CERT_PATH=/d' "$MAILSERVER_ENV"
	    sed -i '/^SSL_KEY_PATH=/d' "$MAILSERVER_ENV"
	else
	    # a domain
		cert_path="/etc/letsencrypt/live/${current_hostname}/${current_hostname}.crt"
		key_path="/etc/letsencrypt/live/${current_hostname}/${current_hostname}.key"
		
		if [[ -f "$cert_path" && -f "$key_path" ]]; then
		    log "Configuring mailserver to use domain $current_hostname for IMAP/SMTP ..."

		    sed -i '/^SSL_TYPE=/c\SSL_TYPE=manual' "$MAILSERVER_ENV"

		    grep -q '^SSL_CERT_PATH=' "$MAILSERVER_ENV" \
		        && sed -i "s|^SSL_CERT_PATH=.*|SSL_CERT_PATH=$cert_path|" "$MAILSERVER_ENV" \
		        || echo "SSL_CERT_PATH=$cert_path" >> "$MAILSERVER_ENV"

		    grep -q '^SSL_KEY_PATH=' "$MAILSERVER_ENV" \
		        && sed -i "s|^SSL_KEY_PATH=.*|SSL_KEY_PATH=$key_path|" "$MAILSERVER_ENV" \
		        || echo "SSL_KEY_PATH=$key_path" >> "$MAILSERVER_ENV"
		else
		    log "Warning: Domain $current_hostname is configured for panel access but has no SSL, it will not be used for mailserver IMAP/SMTP ..."
		    [[ ! -f "$cert_path" ]] && log "- Missing: $cert_path"
		    [[ ! -f "$key_path" ]] && log "- Missing: $key_path"
		fi
	fi
 }


process_domains_and_start() {
    local CONFIG_DIR="/etc/openpanel/caddy/domains"
    local COMPOSE_FILE="$DIR/compose.yml"
    local new_volumes=""
    cp "$COMPOSE_FILE" "$COMPOSE_FILE.bak"

  
  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- MOUNT USERS HOME DIRECTORIES ------------------"
      echo ""
      echo "Re-mounting mail directories for all domains:"
      echo ""
      echo "- DOMAINS DIRECTORY: $CONFIG_DIR" 
      echo "- MAIL SETTINGS FILE: $COMPOSE_FILE"
      printf "%b" "- DEFAULT VOLUMES:\n$new_volumes"
  fi
    
 
echo "Processing domains in directory: $CONFIG_DIR"
for file in "$CONFIG_DIR"/*.conf; do
    echo "Processing file: $file"
    if [ ! -L "$file" ]; then
        # Extract the username and domain from the file name
        BASENAME=$(basename "$file" .conf)
	whoowns_output=$(opencli domains-whoowns "$BASENAME")
	owner=$(echo "$whoowns_output" | awk -F "Owner of '$BASENAME': " '{print $2}')
	if [ -n "$owner" ]; then
	        echo "Domain $BASENAME skipped. No user."
   	else
	        USERNAME=$owner
	        DOMAIN=$BASENAME
	 
	        DOMAIN_DIR="/home/$USERNAME/mail/$DOMAIN/"
	        new_volumes+="      - $DOMAIN_DIR:/var/mail/$DOMAIN/\n"	
	        echo "Mount point added: - $DOMAIN_DIR:/var/mail/$DOMAIN/"
    	fi

    fi
    
done

if [ $? -ne 0 ]; then
    echo "Error encountered while processing $file"
fi

  
  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- EMAIL DIRECTORIES ------------------"
      echo ""
      printf "%b" "- DEFAULT VOLUMES + VOLUMES PER DOMAIN:\n$new_volumes"
      echo ""
      echo "----------------- UPDATE COMPOSE ------------------"
      echo ""
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
  	cd $DIR && docker --context default compose  up -d mailserver
      echo ""
  else
  	cd $DIR && docker --context default compose up -d mailserver >/dev/null 2>&1
  	echo "MailServer started successfully."
  fi
}

stop_mailserver() {
    cd "$DIR" && docker compose down mailserver
    echo "MailServer stopped."
}

uninstall_mailserver() {
    read -t 10 -p "Uninstall MailServer and all config? (y/N): " confirm
    [[ "$confirm" =~ ^[Yy](es)?$ ]] || { echo "Aborted."; return; }
    cd "$DIR" && docker compose down
    rm -rf "$DIR"
    echo "MailServer uninstalled."
}

# ─── MAIN ─────────────────────────────────────────────────────────────────────
ensure_jq
check_bins jq docker cat cut sed tail fold tput tr

check_enterprise

case "${1:-}" in
    install) install_mailserver; opencli email-webmail roundcube ;;
    pflogsumm) generate_reports ;;
    status)
        if docker ps -q -f "name=^$CONTAINER$" &>/dev/null; then
            _status "Container" "$(docker ps -f name=$CONTAINER --format "{{.Status}}")"
            _status "Version" "$(_getDMSVersion)"
            _status "Fail2ban" "$(_container fail2ban)"
            _status "Packages" "$(_container bash -c 'apt -q update | grep "All packages" || echo "Updates available"')"
            _status "Ports" "$(_ports)"
            POSTFIX=$(_container postqueue -p | tail -1 | awk '{print $5}')
            _status "Postfix" "${POSTFIX:-Mail queue is empty}"
            _status "Supervisor" "$(_container supervisorctl status | sort -b -k2,2)"
        else
            echo "Container: down"
        fi ;;
    config) _container cat /etc/dms-settings ;;
    start) process_domains_and_start ;;
    stop) stop_mailserver ;;
    restart) stop_mailserver; process_domains_and_start ;;
    uninstall) uninstall_mailserver ;;
    queue) _container postqueue -p ;;
    flush) _container postqueue -f; echo "Queue flushed." ;;
    unhold|delete)
        [ -z "${2:-}" ] && { echo "Queue ID missing"; exit 1; }
        shift
        local cmd args=()
        [[ "$1" == "unhold" ]] && cmd="-H" || cmd="-d"
        for i in "$@"; do args+=("$cmd" "$i"); done
        _container postsuper "${args[@]}" ;;
    view) _container postcat -q "$2" ;;
    fail*) shift; _container fail2ban "$@" ;;
    ports) _ports ;;
    postc*) shift; _container postconf "$@" ;;
    logs) [[ "$2" == "-f" ]] && docker logs -f "$CONTAINER" || docker logs "$CONTAINER" ;;
    login) _container -it bash ;;
    super*) shift; _container -it supervisorctl "$@" ;;
    update-c*) _container -it bash -c 'apt update && apt list --upgradable' ;;
    update-p*) _container -it bash -c 'apt update && apt upgrade' ;;
    version*) 
        printf "%-15s%s\n" "Mailserver:" "$(_getDMSVersion)"
        for pkg in amavisd-new clamav dovecot-core fail2ban fetchmail getmail6 rspamd opendkim opendmarc postfix spamassassin supervisor; do
            printf "%-15s" "$pkg:"; _container bash -c "dpkg -s $pkg 2>/dev/null | grep ^Version | cut -d' ' -f2 || echo 'Not installed'"
        done ;;
    *) cat <<EOF
Usage: $APP <command>

Available Commands:
  install        Install the mail server
  start          Start the mail server
  stop           Stop the mail server
  restart        Restart the mail server
  uninstall      Remove mail server and configuration
  status         Show container and mailserver status
  config         Show current configuration
  queue          Show mail queue
  flush          Flush mail queue
  view <id>      View mail by queue ID
  unhold <id>    Release held mail
  delete <id>    Delete queued mail
  pflogsumm      Generate summary report
  fail2ban       Interact with fail2ban
  ports          Show container ports
  postconf       Show postfix config
  login          Enter container shell
  logs [-f]      Show logs (use -f to follow)
  update-check   Check for package updates
  update-packages Upgrade all packages
  versions       Show mail server versions
EOF
        ;;
esac
