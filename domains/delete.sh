#!/bin/bash
################################################################################
# Script Name: domains/delete.sh
# Description: Delete a domain name.
# Usage: opencli domains-delete <DOMAIN_NAME> --debug
# Author: Stefan Pejcic
# Created: 07.11.2024
# Last Modified: 17.03.2025
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



# Check if the correct number of arguments are provided
if [ "$#" -lt 1 ]; then
    echo "Usage: opencli domains-delete <DOMAIN_NAME> [--debug]"
    exit 1
fi

# Parameters
domain_name="$1"

debug_mode=false
if [[ "$2" == "--debug" ]]; then
    debug_mode=true
fi


# used for flask route to show progress..
log() {
    if $debug_mode; then
        echo "$1"
    fi
}


log "Checking owner for domain $domain_name"
whoowns_output=$(opencli domains-whoowns "$domain_name")
owner=$(echo "$whoowns_output" | awk -F "Owner of '$domain_name': " '{print $2}')

if [ -n "$owner" ]; then
    user="$owner"
else
    echo "No username received from command 'opencli domains-whoowns $domain_name' - make sure that domain is assigned to user and mysql service is running."
    exit 1
fi


domain_id=$(mysql -se "SELECT domain_id FROM domains WHERE domain_url = '$domain_name';")
if [[ -z "$domain_id" ]]; then
    echo "Domain ID not found in the database for domain $domain_name' - make sure that domain exists on the server and mysql service is running."
  exit 1
fi




clear_cache_for_user() {
	log "Purging cached list of domains for the account"
	rm /etc/openpanel/openpanel/core/users/${user}/data.json >/dev/null 2>&1
}



get_webserver_for_user(){
	    log "Checking webserver configuration"
	    output=$(opencli webserver-get_webserver_for_user $user)
	    if [[ $output == *nginx* ]]; then
	        ws="nginx"
	    elif [[ $output == *apache* ]]; then
	        ws="apache2"
	    else
	        ws="unknown"
	    fi
}




rm_domain_to_clamav_list(){	
	local domains_list="/etc/openpanel/clamav/domains.list"
 	local domain_path="/home/$user/$domain_name"
	# from 0.3.4 we have optional script to run clamav scan for all files in domains dirs, this adds new domains to list of directories to monitor
 	if [ -f $domains_list ]; then
      	log "ClamAV Upload Scanner is enabled - Removing $domain_path for monitoring"
        sed -i '/$domain_path/d' $domains_list
 	fi
}




get_user_info() {
    local user="$1"
    local query="SELECT id, server FROM users WHERE username = '${user}';"
    
    # Retrieve both id and context
    user_info=$(mysql -se "$query")
    
    # Extract user_id and context from the result
    user_id=$(echo "$user_info" | awk '{print $1}')
    context=$(echo "$user_info" | awk '{print $2}')
    
    echo "$user_id,$context"
}




vhost_files_delete() {
	
	vhost_in_docker_file="/etc/$ws/sites-available/${domain_name}.conf"
 	vhost_ln_in_docker_file="/etc/$ws/sites-enabled/${domain_name}.conf"

result=$(get_user_info "$user")
user_id=$(echo "$result" | cut -d',' -f1)
context=$(echo "$result" | cut -d',' -f2)

if [ -z "$user_id" ]; then
    echo "FATAL ERROR: user $user does not exist."
    exit 1
fi



	log "Deleting $vhost_in_docker_file"
	docker --context $context exec $user bash -c 'rm $vhost_in_docker_file' >/dev/null 2>&1
	docker --context $context exec $user bash -c 'rm $vhost_ln_in_docker_file' >/dev/null 2>&1
	
	  
 	log "Restarting $ws inside container to apply changes"
	docker --context $context exec $user bash -c 'service $ws restart' >/dev/null 2>&1

	logs_dir="/var/log/$ws/domlogs"
	log "Deleting access logs for the domain"
	rm $logs_dir/${domain_name}.log  >/dev/null 2>&1
}



# Function to validate and reload Caddy service
check_and_add_to_enabled() {
    # Validate the Caddyfile
    docker compose exec caddy caddy validate --config /etc/caddy/Caddyfile >/dev/null 2>&1
    if [ $? -eq 0 ]; then
        # If validation is successful, reload the Caddy service
        docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1
        return 0
    else
        # If validation fails, revert the domains file and return an error
        echo "Validation failed, reverting changes."
        return 1
    fi
}



delete_domain_file() {
  log "Removing domain from the proxy"
 
	mkdir -p /etc/openpanel/openpanel/core/users/$user/
	domains_file="/etc/openpanel/caddy/domains/$domain_name.conf" 
 	rm -rf $domains_file
	if [ $(docker ps -q -f name=caddy) ]; then
 	    log "Caddy is running, reloading configuration"
	    check_and_add_to_enabled
	fi   
	 #stats and logs
 	rm -rf /var/log/caddy/domlogs/$domain_name/
  	rm -rf /var/log/caddy/stats/$user/$domain_name.html
}


update_named_conf() {
    ZONE_FILE_DIR='/etc/bind/zones/'
    NAMED_CONF_LOCAL='/etc/bind/named.conf.local'
    local config_line="zone \"$domain_name\" IN { type master; file \"$ZONE_FILE_DIR$domain_name.zone\"; };"

    # Check if the domain exists in named.conf.local
    if grep -q "zone \"$domain_name\"" "$NAMED_CONF_LOCAL"; then
        log "Removing zone information from the server."
        sed -i "/zone \"$domain_name\"/d" "$NAMED_CONF_LOCAL"
    fi
}




# Function to create a zone file
delete_zone_file() {
    ZONE_FILE_DIR='/etc/bind/zones/'
    log "Removing DNS zone file: $ZONE_FILE_DIR$domain_name.zone"
    rm "$ZONE_FILE_DIR$domain_name.zone"

    # Reload BIND service
    if [ $(docker ps -q -f name=openpanel_dns) ]; then
        log "DNS service is running, reloading the zones"
      	docker exec openpanel_dns rndc reconfig >/dev/null 2>&1
    fi
}




# add mountpoint and reload mailserver
# todo: need better solution!
delete_mail_mountpoint(){
    PANEL_CONFIG_FILE='/etc/openpanel/openpanel/conf/openpanel.config'
    key_value=$(grep "^key=" $PANEL_CONFIG_FILE | cut -d'=' -f2-)
    
    # Check if 'enterprise edition'
    if [ -n "$key_value" ]; then
        DOMAIN_DIR="/home/$user/mail/$domain_name/"
        COMPOSE_FILE="/usr/local/mail/openmail/compose.yml"
        if [ -f "$COMPOSE_FILE" ]; then
            log "Removing directory $DOMAIN_DIR from mail server volumes"
            volume_to_remove="  - $DOMAIN_DIR:/var/mail/$domain_name/"
            sed -i "/^  mailserver:/,/^  sogo:/ { /^    volumes:/,/\$/ { /$volume_to_remove/d }}" "$COMPOSE_FILE"
            cd /usr/local/mail/openmail/ && docker-compose up -d --force-recreate mailserver > /dev/null 2>&1 & disown
        fi
    fi
}


delete_websites() {
    log "Removing any websites associated with domain (ID: $domain_id)"
    delete_sites_query="DELETE FROM sites WHERE domain_id = '$domain_id';"
    mysql -e "$delete_sites_query"
}


delete_emails() {
  local username="$1"
  local domain="$2"
  local email_file="/etc/openpanel/openpanel/core/users/$username/emails.yml"

  # Check if the email file exists
  if [ -f "$email_file" ]; then
    	  log "Deleting @$domain_name email accounts"
	  # Loop through each line in the email file
	  while IFS= read -r line; do
	    # Extract the email address from each line
	    email=$(echo "$line" | awk '{print $2}')
	    if [[ -n "$email" ]]; then
	      echo "- deleting: $email"
	      opencli email-setup email del "$email"
	    fi
	  done < "$email_file"
  fi
}




delete_domain_from_mysql(){
    local domain_name="$1"
    log "Removing $domain_name from the domains database"
    local delete_query="DELETE from domains where domain_url = '$domain_name';"
    mysql -e "$delete_query"
}

delete_domain() {
    local user="$1"
    local domain_name="$2"
    
    delete_websites $domain_name                     # delete sites associated with domain id
    # TODO: delete pm2 apps associated with it.
    delete_domain_from_mysql $domain_name            # delete

    # Verify if the domain was deleted successfully
    local verify_query="SELECT COUNT(*) FROM domains WHERE domain_url = '$domain_name';"
    local result=$(mysql -N -e "$verify_query")

    if [ "$result" -eq 0 ]; then
        clear_cache_for_user                         # rm cached file for ui
        get_webserver_for_user                       # nginx or apache
        vhost_files_delete                           # delete file in container
        delete_domain_file                           # create file on host
        delete_zone_file                             # create zone
        update_named_conf                            # include zone
        delete_mail_mountpoint                       # delete mountpoint to mailserver
        delete_emails  $user $domain_name            # delete mails for the domain
        rm_domain_to_clamav_list                     # added in 0.3.4    
        echo "Domain $domain_name deleted successfully"
    else
        log "Deleting domain $domain_name failed! Contact administrator to check if the mysql database is running."
        echo "Failed to delete domain $domain_name"
    fi
}



delete_domain "$user" "$domain_name"
