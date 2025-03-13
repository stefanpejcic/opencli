#!/bin/bash
################################################################################
# Script Name: domains/add.sh
# Description: Add a domain name for user.
# Usage: opencli domains-add <DOMAIN_NAME> <USERNAME> [--docroot DOCUMENT_ROOT] --debug
# Author: Stefan Pejcic
# Created: 20.08.2024
# Last Modified: 03.03.2025
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
REMOTE_SERVER=""
PANEL_CONFIG_FILE='/etc/openpanel/openpanel/conf/openpanel.config'

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


log() {
    if $debug_mode; then
        echo "$1"
    fi
}

if [[ -n "$docroot" && ! "$docroot" =~ ^/var/www/html/ ]]; then
    echo "FATAL ERROR: Invalid docroot. It must start with /var/www/html/"
    exit 1
fi

if [[ -n "$docroot" ]]; then
    log "Using document root: $docroot"
else
    docroot="/var/www/html/$domain_name"
    log "No document root specified, using /var/www/html/$domain_name"
fi

# added in 0.3.8 so user can not add the server hostname and take over server!
compare_with_force_domain() {
	read_config() {
	    config=$(awk -F '=' '/\[DEFAULT\]/{flag=1; next} /\[/{flag=0} flag{gsub(/^[ \t]+|[ \t]+$/, "", $1); gsub(/^[ \t]+|[ \t]+$/, "", $2); print $1 "=" $2}' $PANEL_CONFIG_FILE)
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



	json_file="/etc/openpanel/openpanel/core/users/$new_username/ip.json"
	
	if [ -e "$json_file" ]; then
	    dedicated_ip=$(jq -r '.ip' "$json_file")
	    log "User has reserved IP: $dedicated_ip."
	
	    # Check if dedicated_ip is present in the output of `hostname -I`
	    if hostname -I | grep -q "$dedicated_ip"; then
	        REMOTE_SERVER="no"
	 	current_ip=$dedicated_ip
	        log "User has a dedicated IP address $dedicated_ip"
	    else
	        REMOTE_SERVER="yes"
	        log "IP address is asigned to node server."
	    fi
	fi


}


clear_cache_for_user() {
	log "Purging cached list of domains for the account"
	rm /etc/openpanel/openpanel/core/users/${user}/data.json >/dev/null 2>&1
}



make_folder() {
	log "Creating document root directory $docroot"
        docker --context $context compose -f /home/$context/docker-compose.yml run --rm -v ${context}_html_data:/var/www/html/ busybox sh -c "mkdir -p $docroot && chown 0:33 $docroot && chmod -R g+w $docroot"
}



check_and_create_default_file() {
    # extra step needed for nginx
    log "Checking if default configuration file exists for Nginx"
    
    # Check if the file exists
    if [ ! -e "/home/$context/nginx.conf" ]; then
        log "Creating default vhost file for Nginx: /etc/nginx/nginx.conf"

        # Create the Nginx configuration file
        echo "user  nginx;
worker_processes  auto;

pid        /var/run/nginx.pid;

events {
    worker_connections  1024;
}

http {
    include       /etc/nginx/mime.types;
    default_type  application/octet-stream;

    sendfile        on;
    #tcp_nopush     on;

    keepalive_timeout  65;

    #gzip  on;

    include /etc/nginx/conf.d/*.conf;
}" > "/home/$context/nginx.conf"
    fi
}



get_webserver_for_user(){
	    log "Checking webserver configuration"
	    output=$(opencli webserver-get_webserver_for_user $user)
	    if [[ $output == *nginx* ]]; then
	        ws="nginx"
	 	check_and_create_default_file
	    elif [[ $output == *apache* ]]; then
	        ws="apache"
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
        log "Starting container for the default PHP version ${php_version}"
	#docker --context $context compose -f /home/$context/docker-compose.yml up -d php-fpm-${php_version} >/dev/null 2>&1
 	nohup sh -c "docker --context $context compose -f /home/$context/docker-compose.yml up -d php-fpm-${php_version}" </dev/null >nohup.out 2>nohup.err &
}




vhost_files_create() {
	
	if [[ $ws == *apache* ]]; then
#vhost_in_docker_file="/etc/$ws/sites-available/${domain_name}.conf"
vhost_in_docker_file="/home/$context/docker-data/volumes/${context}_webserver_data/_data/${domain_name}.conf"

vhost_docker_template="/etc/openpanel/nginx/vhosts/1.1/docker_apache_domain.conf"
	elif [[ $ws == *nginx* ]]; then
#vhost_in_docker_file="/etc/$ws/conf.d/${domain_name}.conf" 		
vhost_docker_template="/etc/openpanel/nginx/vhosts/1.1/docker_nginx_domain.conf"
vhost_in_docker_file="/home/$context/docker-data/volumes/${context}_webserver_data/_data/${domain_name}.conf"
	elif [[ $ws == *litespeed* ]]; then
		vhost_docker_template="/etc/openpanel/docker/templates/docker_litespeed_domain.conf"
 		vhost_in_docker_file="/usr/local/lsws/conf/vhosts/${domain_name}.conf"


        docker --context $context compose -f /home/$context/docker-compose.yml run --rm busybox echo 'virtualHost $domain_name{
    vhRoot                   ${docroot}/
    allowSymbolLink          1
    enableScript             1
    restrained               1
    maxKeepAliveReq
    smartKeepAlive
    setUIDMode               0
    chrootMode               0
    configFile               conf/vhosts/${domain_name}.conf
}' >> /usr/local/lsws/conf/httpd_config.conf
	fi
 
       log "Starting $ws container.."
       docker --context $context compose -f /home/$context/docker-compose.yml up -d $ws > /dev/null 2>&1       

       log "Creating ${domain_name}.conf" #$vhost_in_docker_file
       cp $vhost_docker_template $vhost_in_docker_file > /dev/null 2>&1
       php_version=$(opencli php-default $user | grep -oP '\d+\.\d+')

	sed -i \
	  -e "s|<DOMAIN_NAME>|$domain_name|g" \
	  -e "s|<USER>|$user|g" \
	  -e "s|<PHP>|$php_version|g" \
	  -e "s|<DOCUMENT_ROOT>|$docroot|g" \
	  $vhost_in_docker_file

       docker --context $context restart $ws > /dev/null 2>&1       
 
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
	local logs_dir="/var/log/caddy/domlogs/${domain_name}"
	mkdir -p $logs_dir && touch $logs_dir/access.log

	#docker_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $user) #from 025 ips are not used
 
	local env_file="/home/${context}/.env"
 	source $env_file

	    # Check if the file exists
	    if [[ ! -f "$env_file" ]]; then
	        echo "Error: .env file not found for user $username"
	        return 1
	    fi
	
	    non_ssl_port=$(echo "$HTTP_PORT" | cut -d':' -f2)
	    ssl_port=$(echo "$HTTPS_PORT" | cut -d':' -f2)


 # VARNISH
 	# added in 0.2.6
	if su "$user" -c "docker exec $container_name test -f /etc/default/varnish" > /dev/null 2>&1; then
	    log "Detected Varnish for user, setting Caddy to proxy requests to Varnish in user container."
	else
	    log "Setting Caddy to proxy requests to $ws user container."
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

if [ "$REMOTE_SERVER" == "yes" ]; then
	domain_conf=$(cat "$conf_template" | sed -e "s|<DOMAIN_NAME>|$domain_name|g" \
                                           -e "s|127.0.0.1:<SSL_PORT>|$current_ip:$ssl_port|g" \
                                           -e "s|127.0.0.1:<NON_SSL_PORT>|$current_ip:$non_ssl_port|g")
 
else
	domain_conf=$(cat "$conf_template" | sed -e "s|<DOMAIN_NAME>|$domain_name|g" \
                                           -e "s|<SSL_PORT>|$ssl_port|g" \
                                           -e "s|<NON_SSL_PORT>|$non_ssl_port|g")
fi
        echo "$domain_conf" > "$domains_file"

}



ENV_FILE="/root/.env"
if [ -f "$ENV_FILE" ]; then
    # Extract the value of CADDY_IMAGE from the .env file
    CADDY_IMAGE=$(grep -oP '^CADDY_IMAGE=\K.*' "$ENV_FILE" | sed 's/^"\(.*\)"$/\1/')

    if [[ "$CADDY_IMAGE" == "openpanel/caddy-coraza" ]]; then
        conf_template="/etc/openpanel/caddy/templates/domain.conf_with_modsec"
        log "Creating vhosts proxy file for Caddy with ModSecurity OWASP Coreruleset"
        sed_values_in_domain_conf
    elif [[ "$CADDY_IMAGE" == "caddy:latest" || "$CADDY_IMAGE" == "caddy" ]]; then
        conf_template="/etc/openpanel/caddy/templates/domain.conf"
        log "Creating Caddy configuration for the domain, without ModSecurity"
        sed_values_in_domain_conf
    else
        echo "ERROR: unable to detect any services. Contact support."
        exit 1
    fi
else
    echo "ERROR: unable to detect .env file. Contact support."
    exit 1
fi



check_and_add_to_enabled() {
    # Validate the Caddyfile
    if docker --context default exec caddy caddy validate --config /etc/caddy/Caddyfile >/dev/null 2>&1; then
        # Wait for validation to finish before proceeding
        docker --context default exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1
        return 0
    else
        return 1
    fi
}



 	# Check if the 'caddy' container is running
	if [ $(docker --context default ps -q -f name=caddy) ]; then
 	    log "Caddy is running, validating new domain configuration"

                ########check_and_add_to_enabled
		docker --context default restart caddy >/dev/null 2>&1
		if [ $? -eq 0 ]; then
		    log "Domain successfully added and Caddy reloaded."
		else
		    log "Failed to add domain configuration, changes reverted."
		fi
	else
	    log "Caddy is not running, starting in background.."
	    nohup sh -c "cd /root && docker --context default compose up -d caddy" </dev/null >nohup.out 2>nohup.err &
     	fi

}




get_slave_dns_option() {
	# Path to the named.conf.options file
	BIND_CONFIG_FILE="/etc/bind/named.conf.options"
	
# Extract the values of allow-transfer and allow-update until the first semicolon
ALLOW_TRANSFER=$(grep -oP 'allow-transfer\s+\{\s*\K[^\;]*' $BIND_CONFIG_FILE | tr -d '[:space:]')
ALLOW_UPDATE=$(grep -oP 'also-notify\s+\{\s*\K[^\;]*' $BIND_CONFIG_FILE | tr -d '[:space:]')


	# Check if both allow-transfer and allow-update are set to the same value
	if [[ "$ALLOW_TRANSFER" == "$ALLOW_UPDATE" && -n "$ALLOW_TRANSFER" && -n "$ALLOW_UPDATE" ]]; then
	    SLAVE_IP=$ALLOW_TRANSFER
     	    MASTER_IP=$current_ip
     	    notify_slave
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
    		log "Creating DNS zone file with A records: $ZONE_FILE_DIR$domain_name.zone"
	else
  		ZONE_TEMPLATE_PATH='/etc/openpanel/bind9/zone_template_ipv6.txt'
        	log "Creating DNS zone file with AAAA records: $ZONE_FILE_DIR$domain_name.zone"
	fi

   zone_template=$(<"$ZONE_TEMPLATE_PATH")
   
   # get nameservers
	get_config_value() {
	    local key="$1"
	    grep -E "^\s*${key}=" "$PANEL_CONFIG_FILE" | sed -E "s/^\s*${key}=//" | tr -d '[:space:]'
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
    if [ $(docker --context default ps -q -f name=openpanel_dns) ]; then
        log "DNS service is running, adding the zone"
	docker --context default exec openpanel_dns rndc reconfig >/dev/null 2>&1
    else
	log "DNS is enabled but the DNS service is not yet started, starting now.."
 	nohup sh -c "cd /root && docker --context default compose up -d bind9" </dev/null >nohup.out 2>nohup.err &
    fi
}



notify_slave(){

    echo "Notifying Slave DNS server ($SLAVE_IP): Adding new zone for domain $domain_name"

ssh root@$SLAVE_IP <<EOF
    if ! grep -q "$domain_name.zone" /etc/bind/named.conf.local; then
        echo "zone \"$domain_name\" { type slave; masters { $MASTER_IP; }; file \"/etc/bind/zones/$domain_name.zone\"; };" >> /etc/bind/named.conf.local
        touch /etc/bind/zones/$domain_name.zone
        echo "Zone $domain_name added to slave server and file touched."
    else
        echo "Zone $domain_name already exists on the slave server."
    fi
EOF


}



# add mountpoint and reload mailserver
# todo: need better solution!
create_mail_mountpoint(){
    key_value=$(grep "^key=" $PANEL_CONFIG_FILE | cut -d'=' -f2-)
    
    # Check if 'enterprise edition'
    if [ -n "$key_value" ]; then
	# do for enterprise!
 	DOMAIN_DIR="/home/$context/mail/$domain_name/"
        COMPOSE_FILE="/usr/local/mail/openmail/compose.yml"
        if [ -f "$COMPOSE_FILE" ]; then
	    log "Creating directory $DOMAIN_DIR for emails"
     	    mkdir -p $DOMAIN_DIR
	    log "Adding mountpoint to the mail-server in background"
            volume_to_add="  - $DOMAIN_DIR:/var/mail/$domain_name/"
	    
sed -i "/^  mailserver:/,/^  sogo:/ { /^    volumes:/a\\
    $volume_to_add
}" "$COMPOSE_FILE"

	     #cd /usr/local/mail/openmail/ && docker-compose up -d --force-recreate mailserver > /dev/null 2>&1 & disown  
             nohup sh -c "cd /usr/local/mail/openmail/ && docker-compose up -d --force-recreate mailserver " </dev/null >nohup.out 2>nohup.err &
	fi
    fi
}




# Function to create a zone file
dns_stuff() {

    enabled_modules_line=$(grep '^enabled_modules=' "$PANEL_CONFIG_FILE")
    if [[ $enabled_modules_line == *"dns"* ]]; then  
	    create_zone_file                             # create zone
	    get_slave_dns_option                         # create zone on slave before include on master
	    update_named_conf                            # include zone 
    else
        log "DNS module is disabled - skipping creating DNS records"
    fi
}


litespeed_extra() {
 	docker --context $context compose -f /home/$context/docker-compose.yml run --rm busybox sh -c "chown $nobody:33 $docroot"
}


get_php_version() {
	php_version=$(opencli php-default $user | grep -oP '\d+\.\d+')
}

# Add domain to the database
add_domain() {
    local user_id="$1"
    local domain_name="$2"
    log "Adding $domain_name to the domains database"
    local insert_query="INSERT INTO domains (user_id, docroot, php_version, domain_url) VALUES ('$user_id', '$docroot', '$php_version', '$domain_name');"
    mysql -e "$insert_query"

    # Verify if the domain was added successfully
    local verify_query="SELECT COUNT(*) FROM domains WHERE user_id = '$user_id' AND docroot = '$docroot' AND domain_url = '$domain_name';"
    local result=$(mysql -N -e "$verify_query")

    if [ "$result" -eq 1 ]; then
    
    	clear_cache_for_user                         # rm cached file for ui
php_ve    	make_folder                                  # create dirs on host server
    	get_webserver_for_user                       # nginx or apache
    	get_server_ipv4_or_ipv6                      # get outgoing ip     
	vhost_files_create                           # create file in container
	create_domain_file                           # create file on host
	dns_stuff
	start_default_php_fpm_service                # start phpX.Y-fpm service
	create_mail_mountpoint                       # add mountpoint to mailserver
 	#litespeed_extra # TODO!
    
 	######add_domain_to_clamav_list                    # added in 0.3.4    
        echo "Domain $domain_name added successfully"
        #echo "Domain $domain_name has been added for user $user."
    else
        log "Adding domain $domain_name failed! Contact administrator to check if the mysql database is running."
        echo "Failed to add domain $domain_name for user $user (id:$user_id)."
    fi
}


get_php_version
add_domain "$user_id" "$domain_name"
