#!/bin/bash
################################################################################
# Script Name: domains/add.sh
# Description: Add a domain name for user.
# Usage: opencli domains-add <DOMAIN_NAME> <USERNAME> [--docroot DOCUMENT_ROOT] --debug
# Author: Stefan Pejcic
# Created: 20.08.2024
# Last Modified: 03.02.2025
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
if [ "$#" -lt 2 ]; then
    echo "Usage: opencli domains-add <DOMAIN_NAME> <USERNAME> [--debug]"
    exit 1
fi

# Parameters
domain_name="$1"
user="$2"
container_name="$2"

if ! [[ "$domain_name" =~ ^(xn--[a-z0-9-]+\.[a-z0-9-]+|[a-zA-Z0-9.-]+\.[a-zA-Z]{2,})$ ]]; then
    echo "FATAL ERROR: Invalid domain name: $domain_name"
    exit 1
fi

debug_mode=false
docroot=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --debug)
            debug_mode=true
            shift
            ;;
        --docroot)
            if [[ -n "$2" ]]; then
                docroot="$2"
                shift 2
            else
                echo "FATAL ERROR: Missing value for --docroot"
                exit 1
            fi
            ;;
        *)
            shift
            ;;
    esac
done


# used for flask route to show progress..
log() {
    if $debug_mode; then
        echo "$1"
    fi
}

if [[ -n "$docroot" && ! "$docroot" =~ ^/home/$user/files/ ]]; then
    echo "FATAL ERROR: Invalid docroot. It must start with /home/$user/files/"
    exit 1
fi

if [[ -n "$docroot" ]]; then
    log "Using document root: $docroot"
else
    docroot="/home/$user/files/$domain_name"
    log "No document root specified, using /home/$user/files/$domain_name"
fi

# added in 0.3.8 so user can not add the server hostname and take over server!
compare_with_force_domain() {
	local CONFIG_FILE_PATH='/etc/openpanel/openpanel/conf/openpanel.config'
	read_config() {
	    config=$(awk -F '=' '/\[DEFAULT\]/{flag=1; next} /\[/{flag=0} flag{gsub(/^[ \t]+|[ \t]+$/, "", $1); gsub(/^[ \t]+|[ \t]+$/, "", $2); print $1 "=" $2}' $CONFIG_FILE_PATH)
	    echo "$config"
	}
	
	local config=$(read_config)
	local force_domain=$(echo "$config" | grep -i 'force_domain' | cut -d'=' -f2)

	if [ -n "$force_domain" ]; then
	    if [ "$force_domain" == "$domain_name" ]; then
		echo "ERROR: domain $domain_name can not be added as it is currently used as the server hostname."
  		exit 1
	    fi
	fi
}








# added in 0.3.8 so admin can disable some domains!
compare_with_dorbidden_domains_list() {
    local CONFIG_FILE_PATH='/etc/openpanel/openpanel/conf/domain_restriction.txt'
    local CADDYFILE='/etc/openpanel/caddy/Caddyfile'
    local domain_name="$1"
    local forbidden_domains=()

    # Check forbidden domains list
    if [ -f "forbidden_domains.txt" ]; then
        log "Checking domain against forbidden_domains list"
        mapfile -t forbidden_domains < forbidden_domains.txt
        if [[ " ${forbidden_domains[@]} " =~ " ${domain_name} " ]]; then
            echo "ERROR: $domain_name is a forbidden domain."
            exit 1
        fi    
    fi

    # Check domains in Caddyfile
    if [ -f "$CADDYFILE" ]; then
        log "Checking domain against system domains"
        while IFS= read -r line; do
            if [[ "$line" =~ ^[a-zA-Z0-9.-]+\s+\{ ]]; then
                caddy_domain=$(echo "$line" | awk '{print $1}')
                if [[ "$domain_name" == "$caddy_domain" ]]; then
                    echo "ERROR: $domain_name is already configured."
                    exit 1
                fi
            fi
        done < "$CADDYFILE"
    fi
}



# Check if domain already exists
log "Checking if domain already exists on the server"
if opencli domains-whoowns "$domain_name" | grep -q "not found in the database."; then
    :
    compare_with_force_domain                      # dont allow hostname takeover
    compare_with_dorbidden_domains_list            # dont allow admin-defined domains
else
    echo "ERROR: Domain $domain_name already exists."
    exit 1
fi


# get user ID from the database
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


result=$(get_user_info "$user")
user_id=$(echo "$result" | cut -d',' -f1)
context=$(echo "$result" | cut -d',' -f2)

#echo "User ID: $user_id"
#echo "Context: $context"



if [ -z "$user_id" ]; then
    echo "FATAL ERROR: user $user does not exist."
    exit 1
fi






get_server_ipv4_or_ipv6() {
	# IP SERVERS
	SCRIPT_PATH="/usr/local/admin/core/scripts/ip_servers.sh"
 	log "Checking IPv4 address for the account"
	if [ -f "$SCRIPT_PATH" ]; then
	    source "$SCRIPT_PATH"
	else
	    IP_SERVER_1=IP_SERVER_2=IP_SERVER_3="https://ip.openpanel.com"
	fi
 
	        log "Trying to fetch IP address..."

	get_ip() {
	    local ip_version=$1
	    local server1=$2
	    local server2=$3
	    local server3=$4
	
	    if [ "$ip_version" == "-4" ]; then
		    curl --silent --max-time 2 $ip_version $server1 || \
		    wget --timeout=2 -qO- $server2 || \
		    curl --silent --max-time 2 $ip_version $server3
	    else
		    curl --silent --max-time 2 $ip_version $server1 || \
		    curl --silent --max-time 2 $ip_version $server3
	    fi

	}

	# use public IPv4
	current_ip=$(get_ip "-4" "$IP_SERVER_1" "$IP_SERVER_2" "$IP_SERVER_3")

	# fallback from the server
	if [ -z "$current_ip" ]; then
	    log "Fetching IPv4 from local hostname..."
	    current_ip=$(ip addr | grep 'inet ' | grep global | head -n1 | awk '{print $2}' | cut -f1 -d/)
	fi
 
 	IPV4="yes"
  
	# public IPv6
	if [ -z "$current_ip" ]; then
 	    IPV4="no"
	    log "No IPv4 found. Checking IPv6 address..."
	    current_ip=$(get_ip "-6" "$IP_SERVER_1" "$IP_SERVER_2" "$IP_SERVER_3")
	    # Fallback to hostname IPv6 if no IPv6 from servers
	    if [ -z "$current_ip" ]; then
	        log "Fetching IPv6 from local hostname..."
	        current_ip=$(ip addr | grep 'inet6 ' | grep global | head -n1 | awk '{print $2}' | cut -f1 -d/)
	    fi
	fi
	
	# no :(
	if [ -z "$current_ip" ]; then
	    echo "Error: Unable to determine IP address (IPv4 or IPv6)."
	    exit 1
	fi
}


clear_cache_for_user() {
	log "Purging cached list of domains for the account"
	rm /etc/openpanel/openpanel/core/users/${user}/data.json >/dev/null 2>&1
}






get_user_uid() {
	local username="$1"
	UID=$(awk -F: -v user="$username" '$1 == user {print $3}' /hostfs/etc/passwd)
	
	# Check if UID was found
	if [ -n "$UID" ]; then
	    echo "$UID"
	else
	    echo "User '$username' not found on teh server!"
	    exit 2
	fi
}



make_folder() {
	uid=$(get_user_uid)
	log "Creating document root directory $docroot"
	docker --context $context exec $container_name bash -c "chown $uid /home/$user/files/"
	docker --context $context exec -u $context $container_name bash -c "mkdir -p $docroot"
	docker --context $context exec $container_name bash -c "chown $uid:33 $docroot"
	docker --context $context exec $container_name bash -c "chmod -R g+w $docroot" #maybe back
}


check_and_create_default_file() {
#extra step needed for nginx
log "Checking if default vhosts file exists for Nginx"
file_exists=$(docker --context $context exec  -u $context $container_name bash -c "test -e /etc/nginx/sites-enabled/default && echo yes || echo no")

if [ "$file_exists" == "no" ]; then
    		log "Creating default vhost file for Nginx: /etc/nginx/sites-enabled/default"

tempfile=$(mktemp) 

# Create the Nginx configuration file
echo "server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;
    deny all;
    return 444;
}" > "$tempfile"

# Copy the file to the container
docker --context "$context" cp "$tempfile" "$container_name:/etc/nginx/sites-enabled/default" > /dev/null 2>&1
rm "$tempfile" > /dev/null 2>&1
  
fi
}




get_webserver_for_user(){
	    log "Checking webserver configuration"
	    output=$(opencli webserver-get_webserver_for_user $user)
	    if [[ $output == *nginx* ]]; then
	        ws="nginx"
	 	check_and_create_default_file
	    elif [[ $output == *apache* ]]; then
	        ws="apache2"
	    elif [[ $output == *litespeed* ]]; then
	        ws="litespeed"
	    else
	        ws="unknown"
	    fi
}




add_domain_to_clamav_list(){	
	local domains_list="/etc/openpanel/clamav/domains.list"
	# from 0.3.4 we have optional script to run clamav scan for all files in domains dirs, this adds new domains to list of directories to monitor
 	if [ -f $domains_list ]; then
      		log "ClamAV Upload Scanner is enabled - Adding $docroot for monitoring"
		echo "$docroot" >> "$domains_list"
		# not needed since we also watch the domains list file for changes! 
  		#service clamav_monitor restart > /dev/null 2>&1
 	fi
}



start_default_php_fpm_service() {
        log "Starting service for the default PHP version ${php_version}"
	docker --context $context exec $container_name bash -c "service php${php_version}-fpm start >/dev/null 2>&1"
        log "Checking and setting PHP service to automatically start on reboot"
	docker --context $context exec $container_name bash -c "sed -i 's/PHP${php_version//./}FPM_STATUS=\"off\"/PHP${php_version//./}FPM_STATUS=\"on\"/' /etc/entrypoint.sh >/dev/null 2>&1"
}



auto_start_webserver_for_user_in_future(){
        log "Checking and setting $ws service to automatically start on reboot"
	if [[ $ws == *apache2* ]]; then
 		######docker --context $context exec $container_name bash -c  
		su "$user" -c "docker exec $container_name sed -i 's/APACHE_STATUS=\"off\"/APACHE_STATUS=\"on\"/' /etc/entrypoint.sh"
	elif [[ $ws == *nginx* ]]; then
		su "$user" -c "docker exec $container_name sed -i 's/NGINX_STATUS=\"off\"/NGINX_STATUS=\"on\"/' /etc/entrypoint.sh"
	elif [[ $ws == *litespeed* ]]; then
		su "$user" -c "docker exec $container_name sed -i 's/LITESPEED_STATUS=\"off\"/LITESPEED_STATUS=\"on\"/' /etc/entrypoint.sh"
	fi
}


vhost_files_create() {
	
	if [[ $ws == *apache2* ]]; then
vhost_in_docker_file="/etc/$ws/sites-available/${domain_name}.conf"
restart_in_container_command="ln -s $vhost_in_docker_file /etc/$ws/sites-enabled/ && service $ws restart"
 		if [[ $VARNISH == true ]]; then
   			vhost_docker_template="/etc/openpanel/nginx/vhosts/docker_varnish_apache_domain.conf"
   		else
			vhost_docker_template="/etc/openpanel/nginx/vhosts/1.0/docker_apache_domain.conf"
  		fi      
	elif [[ $ws == *nginx* ]]; then
vhost_in_docker_file="/etc/$ws/sites-available/${domain_name}.conf"
restart_in_container_command="ln -s $vhost_in_docker_file /etc/$ws/sites-enabled/ && service $ws restart"
 		
 		if [[ $VARNISH == true ]]; then
   			vhost_docker_template="/etc/openpanel/nginx/vhosts/docker_varnish_nginx_domain.conf"
   		else
			vhost_docker_template="/etc/openpanel/nginx/vhosts/1.0/docker_nginx_domain.conf"
  		fi
	elif [[ $ws == *litespeed* ]]; then
 restart_in_container_command="/usr/local/lsws/bin/lswsctrl restart"
 		vhost_in_docker_file="/usr/local/lsws/conf/vhosts/${domain_name}.conf"

docker --context $context exec $container_name bash -c "echo '
virtualHost $domain_name{
    vhRoot                   ${docroot}/
    allowSymbolLink          1
    enableScript             1
    restrained               1
    maxKeepAliveReq
    smartKeepAlive
    setUIDMode               0
    chrootMode               0
    configFile               conf/vhosts/${domain_name}.conf
}' >> /usr/local/lsws/conf/httpd_config.conf"
	fi

	log "Creating $vhost_in_docker_file"
	logs_dir="/var/log/$ws/domlogs"
	
	docker --context $user exec $container_name bash -c "mkdir -p $logs_dir && touch $logs_dir/${domain_name}.log" > /dev/null 2>&1
	docker --context $user cp $vhost_docker_template $user:$vhost_in_docker_file > /dev/null 2>&1

  	# gateway is always 172.17.0.0/16
	php_version=$(opencli php-default_version $user | grep -oP '\d+\.\d+')


 	docker --context $user exec $container_name touch $vhost_in_docker_file > /dev/null 2>&1 # to be removed!

	docker --context "$user" exec "$container_name" bash -c "
	  sed -i \
	    -e 's|<DOMAIN_NAME>|$domain_name|g' \
	    -e 's|<USER>|$user|g' \
	    -e 's|<PHP>|php${php_version}|g' \
	    -e 's|<DOCUMENT_ROOT>|$docroot|g' \
	    $vhost_in_docker_file
	"


       docker --context $user exec $container_name bash -c "$restart_in_container_command" > /dev/null 2>&1
 
}


check_if_varnish_installed_for_user() {
# todo: edit to check if running!
	# VARNISH
 	# added in 0.3.7
	if su "$user" -c "docker exec $container_name test -f /etc/default/varnish"; then
 	    log "Varnish is installed"
      		VARNISH=true
	else
 		VARNISH=false
	fi
}



create_domain_file() {

	mkdir -p $logs_dir && touch $logs_dir/${domain_name}.log

	#docker_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $user) #from 025 ips are not used

	get_port_mapping() {
	    local username="$1"
            local inside_port="$2"
	    local env_file="/etc/openpanel/docker/compose/${username}/.env"
	    regex="port_\d+=\"\K[0-9]+(?=:$inside_port\")"
	
	    # Check if the file exists
	    if [[ ! -f "$env_file" ]]; then
	        echo "Error: .env file not found for user $username"
	        return 1
	    fi
	
	    # Extract the port mapping for :80
	    local nonssl_port_mapping
	    port_mapping=$(grep -oP "$regex" "$env_file")
	    if [[ -n "$port_mapping" ]]; then
	        echo "$port_mapping"
	    else
	        echo "Error: No port mapping found for :$inside_port"
	        return 1
	    fi
	}

	non_ssl_port=$(get_port_mapping "$user" "80")
        ssl_port=$(get_port_mapping "$user" "443")
 	localhost_and_port="127.0.0.1:$port"

 # VARNISH
 	# added in 0.2.6
	if su "$user" -c "docker exec $container_name test -f /etc/default/varnish" > /dev/null 2>&1; then
	    log "Detected Varnish for user, setting Nginx to proxy requests to Varnish in user container."
	else
	    log "Setting Nginx to proxy requests to $ws user container."
	fi




	if [ "$IPV4" == "yes" ]; then
 		ip_format_for_nginx="$current_ip"
   	else
		ip_format_for_nginx="[$current_ip]"
    	fi

     # todo: include only if dedi ip in caddy file!

mkdir -p /etc/openpanel/caddy/domains/

domains_file="/etc/openpanel/caddy/domains/$domain_name.conf"
touch $domains_file


sed_values_in_domain_conf() {
	domain_conf=$(cat "$conf_template" | sed -e "s|<DOMAIN_NAME>|$domain_name|g" \
                                           -e "s|<SSL_PORT>|$ssl_port|g" \
                                           -e "s|<NON_SSL_PORT>|$non_ssl_port|g")
 
    echo "$domain_conf" > "$domains_file"
}



DOCKER_COMPOSE_FILE="/root/docker-compose.yml"
if [ -f "$DOCKER_COMPOSE_FILE" ]; then
    CADDY_IMAGE=$(grep -oP '(?<=image:\s).*' "$DOCKER_COMPOSE_FILE" | grep 'caddy')

    if [[ "$CADDY_IMAGE" == *"openpanel/caddy-coraza"* ]]; then
	conf_template="/etc/openpanel/caddy/templates/domain.conf_with_modsec"
     	log "Creating vhosts proxy file for Caddy with ModSecurity OWASP Coreruleset"
        sed_values_in_domain_conf
    elif [[ "$CADDY_IMAGE" == *"caddy:latest"* || "$CADDY_IMAGE" == *"caddy"* ]]; then
	conf_template="/etc/openpanel/caddy/templates/domain.conf"
     	log "Creating Caddy configuration for the domain, without ModSecurity"
      	sed_values_in_domain_conf
    else
        echo "ERROR: unable to detect any services. Contact support."
	exit 1
    fi
else
        echo "ERROR: unable to detect any services. Contact support."
	exit 1
fi





# Function to validate and reload Caddy service
check_and_add_to_enabled() {
    # Validate the Caddyfile
    output=$(docker exec caddy caddy validate --config /etc/caddy/Caddyfile >/dev/null 2>&1)

    if echo "$output" | grep -q "Valid configuration"; then
        docker exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1
        return 0
    else
        #echo "Validation failed, reverting changes."
        #cp "$backup_file" "$domains_file"
        return 1
    fi
}


 	# Check if the 'caddy' container is running
	if [ $(docker ps -q -f name=caddy) ]; then
 	    log "Caddy is running, validating new domain configuration"
	else
	    log "Caddy is not running, starting now"
            cd /root && docker compose up -d caddy  >/dev/null 2>&1
     	fi
	    	check_and_add_to_enabled
		if [ $? -eq 0 ]; then
		    log "Domain successfully added and Caddy reloaded."
		else
		    log "Failed to add domain configuration, changes reverted."
		fi

    
}






update_named_conf() {
    ZONE_FILE_DIR='/etc/bind/zones/'
    NAMED_CONF_LOCAL='/etc/bind/named.conf.local'
    log "Adding the newly created zone file to the DNS server"
    local config_line="zone \"$domain_name\" IN { type master; file \"$ZONE_FILE_DIR$domain_name.zone\"; };"

    # Check if the domain already exists in named.conf.local
    # fix for: https://github.com/stefanpejcic/OpenPanel/issues/95
    if grep -q "zone \"$domain_name\"" "$NAMED_CONF_LOCAL"; then
        log "Domain '$domain_name' already exists in $NAMED_CONF_LOCAL"
        return
    fi

    # Append the new zone configuration to named.conf.local
    echo "$config_line" >> "$NAMED_CONF_LOCAL"
}





# Function to create a zone file
create_zone_file() {
    ZONE_FILE_DIR='/etc/bind/zones/'
    CONFIG_FILE='/etc/openpanel/openpanel/conf/openpanel.config'

	if [ "$IPV4" == "yes" ]; then
 		ZONE_TEMPLATE_PATH='/etc/openpanel/bind9/zone_template.txt'
    		log "Creating DNS zone file with AAAA records: $ZONE_FILE_DIR$domain_name.zone"
	else
  		ZONE_TEMPLATE_PATH='/etc/openpanel/bind9/zone_template_ipv6.txt'
        	log "Creating DNS zone file with A records: $ZONE_FILE_DIR$domain_name.zone"
	fi

   zone_template=$(<"$ZONE_TEMPLATE_PATH")
   
   # get nameservers
	get_config_value() {
	    local key="$1"
	    grep -E "^\s*${key}=" "$CONFIG_FILE" | sed -E "s/^\s*${key}=//" | tr -d '[:space:]'
	}

    ns1=$(get_config_value 'ns1')
    ns2=$(get_config_value 'ns2')

    # Fallback
    if [ -z "$ns1" ]; then
        ns1='ns1.openpanel.org'
    fi

    if [ -z "$ns2" ]; then
        ns2='ns2.openpanel.org'
    fi

    # Create zone content
    timestamp=$(date +"%Y%m%d")
    
    # Replace placeholders in the template
	zone_content=$(echo "$zone_template" | sed -e "s|{domain}|$domain_name|g" \
                                           -e "s|{ns1}|$ns1|g" \
                                           -e "s|{ns2}|$ns2|g" \
                                           -e "s|{server_ip}|$current_ip|g" \
                                           -e "s|YYYYMMDD|$timestamp|g")
 
    mkdir -p "$ZONE_FILE_DIR"
    echo "$zone_content" > "$ZONE_FILE_DIR$domain_name.zone"

    # Reload BIND service
    if [ $(docker ps -q -f name=openpanel_dns) ]; then
        log "DNS service is running, adding the zone"
	docker exec openpanel_dns rndc reconfig >/dev/null 2>&1
    else
	log "DNS service is not started, starting now"
        cd /root && docker compose up -d bind9  >/dev/null 2>&1
    fi
    
}




# add mountpoint and reload mailserver
# todo: need better solution!
create_mail_mountpoint(){
    PANEL_CONFIG_FILE='/etc/openpanel/openpanel/conf/openpanel.config'
    key_value=$(grep "^key=" $PANEL_CONFIG_FILE | cut -d'=' -f2-)
    
    # Check if 'enterprise edition'
    if [ -n "$key_value" ]; then
	# do for enterprise!
 	DOMAIN_DIR="/home/$user/mail/$domain_name/"
        COMPOSE_FILE="/usr/local/mail/openmail/compose.yml"
        if [ -f "$COMPOSE_FILE" ]; then
	    log "Creating directory $DOMAIN_DIR for emails"
     	    mkdir -p $DOMAIN_DIR
	    log "Adding mountpoint to the mail-server in background"
            volume_to_add="  - $DOMAIN_DIR:/var/mail/$domain_name/"
	    
sed -i "/^  mailserver:/,/^  sogo:/ { /^    volumes:/a\\
    $volume_to_add
}" "$COMPOSE_FILE"

	     cd /usr/local/mail/openmail/ && docker-compose up -d --force-recreate mailserver > /dev/null 2>&1 & disown  
	fi
    fi
}




# Add domain to the database
add_domain() {
    local user_id="$1"
    local domain_name="$2"
    log "Adding $domain_name to the domains database"
    local insert_query="INSERT INTO domains (user_id, docroot, domain_url) VALUES ('$user_id', '$docroot', '$domain_name');"
    mysql -e "$insert_query"
    result=$(mysql -se "$query")

    # Verify if the domain was added successfully
    local verify_query="SELECT COUNT(*) FROM domains WHERE user_id = '$user_id' AND docroot = '$docroot' AND domain_url = '$domain_name';"
    local result=$(mysql -N -e "$verify_query")

    if [ "$result" -eq 1 ]; then
    
    	clear_cache_for_user                         # rm cached file for ui
    	make_folder                                  # create dirs on host server
     	check_if_varnish_installed_for_user	     # use varnish templates
    	get_webserver_for_user                       # nginx or apache
    	get_server_ipv4_or_ipv6                      # get outgoing ip     
	vhost_files_create                           # create file in container
	create_domain_file                           # create file on host
        create_zone_file                             # create zone
	update_named_conf                            # include zone 
 	auto_start_webserver_for_user_in_future      # edit entrypoint
       	start_default_php_fpm_service                # start phpX.Y-fpm service
	create_mail_mountpoint                       # add mountpoint to mailserver
 	add_domain_to_clamav_list                    # added in 0.3.4    
        echo "Domain $domain_name added successfully"
        #echo "Domain $domain_name has been added for user $user."
    else
        log "Adding domain $domain_name failed! Contact administrator to check if the mysql database is running."
        echo "Failed to add domain $domain_name for user $user (id:$user_id)."
    fi
}



add_domain "$user_id" "$domain_name"
