#!/bin/bash

# Check if the correct number of arguments are provided
if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <domain_name> <user>"
    exit 1
fi

# Parameters
domain_name="$1"
user="$2"

# Validate domain name (basic validation)
if ! [[ "$domain_name" =~ ^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$ ]]; then
    echo "FATAL ERROR: Invalid domain name: $domain_name"
    exit 1
fi

# Check if domain already exists
if opencli domains-whoowns "$domain_name" | grep -q "not found in the database."; then
    :
else
    echo "WARNING: Domain $domain_name already exists."
    exit 1
fi

# get user ID from the database
get_user_id() {
    local user="$1"
    local query="SELECT id FROM users WHERE username = '${user}';"
    
    user_id=$(mysql -se "$query")
    echo "$user_id"
}

user_id=$(get_user_id "$user")

if [ -z "$user_id" ]; then
    echo "FATAL ERROR: user $user does not exist."
    exit 1
fi



get_server_ipv4() {

	# Get server ipv4 from ip.openpanel.co
	current_ip=$(curl --silent --max-time 2 -4 https://ip.openpanel.co || wget --timeout=2 -qO- https://ip.openpanel.co || curl --silent --max-time 2 -4 https://ifconfig.me)
	
	# If site is not available, get the ipv4 from the hostname -I
	if [ -z "$current_ip" ]; then
	   # current_ip=$(hostname -I | awk '{print $1}')
	    # ip addr command is more reliable then hostname - to avoid getting private ip
	    current_ip=$(ip addr|grep 'inet '|grep global|head -n1|awk '{print $2}'|cut -f1 -d/)
	fi

}




clear_cache_for_user() {
	rm /etc/openpanel/openpanel/core/users/${user}/data.json >/dev/null 2>&1
}

make_folder() {
	mkdir -p /home/$user/$domain_name
	docker exec $user bash -c "chown $user:www-data /home/$user/$domain_name"
	chmod -R g+w /home/$user/$domain_name
}


check_and_create_default_file() {
#extra step needed for nginx

file_exists=$(docker exec "$user" test -e "/etc/nginx/sites-enabled/default" && echo "yes" || echo "no")

if [ "$file_exists" == "no" ]; then
    echo "Default nginx vhost file does not exist, creating.."
    
    # Create the file with the specified content
    docker exec "$user" sh -c "echo 'server {
        listen 80 default_server;
        listen [::]:80 default_server;
        server_name _;
        deny all;
        return 444;
        }' > /etc/nginx/sites-enabled/default"
fi
}

get_webserver_for_user(){
	    output=$(opencli webserver-get_webserver_for_user $user)
	    if [[ $output == *nginx* ]]; then
	        ws="nginx"
	 	check_and_create_default_file



  
	    elif [[ $output == *apache* ]]; then
	        ws="apache2"
	    else
	        ws="unknown"
	    fi
}


vhost_files_create() {

vhost_docker_template="/etc/openpanel/nginx/vhosts/docker_${ws}_domain.conf"
vhost_in_docker_file="/etc/$ws/sites-available/${domain_name}.conf"
logs_dir="/var/log/$ws/domlogs"

docker exec $user bash -c "mkdir -p $logs_dir && touch $logs_dir/${domain_name}.log"  >/dev/null 2>&1

docker cp  $vhost_docker_template $user:$vhost_in_docker_file  >/dev/null 2>&1

user_gateway=$(docker exec $user bash -c "ip route | grep default | cut -d' ' -f3")

# Execute the sed command inside the Docker container
docker exec -it $user /bin/bash -c "
  sed -i \
    -e 's|<DOMAIN_NAME>|$domain_name|g' \
    -e 's|<USER>|$user|g' \
    -e 's|<PHP>|$php_version|g' \
    -e 's|172.17.0.1|$user_gateway|g' \
    -e 's|<DOCUMENT_ROOT>|/home/$user/$domain_name|g' \
    $vhost_in_docker_file
"

docker exec $user bash -c "mkdir -p /etc/$ws/sites-enabled/ && ln -s $vhost_in_docker_file /etc/$ws/sites-enabled/ && service $ws restart  >/dev/null 2>&1"

}

create_domain_file() {

	if [ -f /etc/nginx/modsec/main.conf ]; then
	    conf_template="/etc/openpanel/nginx/vhosts/domain.conf_with_modsec"
	else
	    conf_template="/etc/openpanel/nginx/vhosts/domain.conf"
	fi

mkdir -p $logs_dir && touch $logs_dir/${domain_name}.log

cp $conf_template /etc/nginx/sites-available/${domain_name}.conf

docker_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $user)

mkdir -p /etc/openpanel/openpanel/core/users/${user}/domains/ && touch /etc/openpanel/openpanel/core/users/${user}/domains/${domain_name}-block_ips.conf

sed -i \
    -e "s|<DOMAIN_NAME>|$domain_name|g" \
    -e "s|<USERNAME>|$user|g" \
    -e "s|<IP>|$user_gateway|g" \
    -e "s|<LISTEN_IP>|$current_ip|g" \
    /etc/nginx/sites-available/${domain_name}.conf

    
    mkdir -p /etc/nginx/sites-enabled/
    ln -s /etc/nginx/sites-available/${domain_name}.conf /etc/nginx/sites-enabled/
    systemctl reload nginx
}


update_named_conf() {

ZONE_FILE_DIR='/etc/bind/zones/'
NAMED_CONF_LOCAL='/etc/bind/named.conf.local'

    local config_line="zone \"$domain_name\" IN { type master; file \"$ZONE_FILE_DIR$domain_name.zone\"; };"

    # Check if the domain already exists in named.conf.local
    if grep -q "zone \"$domain_name\"" "$NAMED_CONF_LOCAL"; then
        echo "Domain '$domain_name' already exists in $NAMED_CONF_LOCAL"
        return
    fi

    # Append the new zone configuration to named.conf.local
    echo "$config_line" >> "$NAMED_CONF_LOCAL"
}




# Function to create a zone file
create_zone_file() {
    ZONE_TEMPLATE_PATH='/etc/openpanel/bind9/zone_template.txt'
    ZONE_FILE_DIR='/etc/bind/zones/'
    CONFIG_FILE='/etc/openpanel/openpanel/conf/openpanel.config'

    zone_template=$(<"$ZONE_TEMPLATE_PATH")

	# Function to extract value from config file
	get_config_value() {
	    local key="$1"
	    grep -E "^\s*${key}=" "$CONFIG_FILE" | sed -E "s/^\s*${key}=//" | tr -d '[:space:]'
	}



    ns1=$(get_config_value 'ns1')
    ns2=$(get_config_value 'ns2')

    # Fallback
    if [ -z "$ns1" ]; then
        ns1='ns1.openpanel.co'
    fi

    if [ -z "$ns2" ]; then
        ns2='ns2.openpanel.co'
    fi

    # Create zone content
    zone_content=$(echo "$zone_template" | sed -e "s/{domain}/$domain_name/g" -e "s/{ns1}/$ns1/g" -e "s/{ns2}/$ns2/g" -e "s/{server_ip}/$current_ip/g")

    # Ensure the directory exists
    mkdir -p "$ZONE_FILE_DIR"

    # Write the zone content to the zone file
    echo "$zone_content" > "$ZONE_FILE_DIR$domain_name.zone"

    # Reload BIND service
    service bind9 reload
}






# Add domain to the database
add_domain() {
    local user_id="$1"
    local domain_name="$2"

    local insert_query="INSERT INTO domains (user_id, domain_name, domain_url) VALUES ('$user_id', '$domain_name', '$domain_name');"
    mysql -e "$insert_query"


    result=$(mysql -se "$query")



    # Verify if the domain was added successfully
    local verify_query="SELECT COUNT(*) FROM domains WHERE user_id = '$user_id' AND domain_name = '$domain_name' AND domain_url = '$domain_name';"
    local result=$(mysql -N -e "$verify_query")

    if [ "$result" -eq 1 ]; then
    
    	#TODO
    	clear_cache_for_user # rm cached file for ui
    	make_folder # create dirs on host server
    	get_webserver_for_user # nginx or apache
    	get_server_ipv4 # get outgoing ip
	vhost_files_create # create file in container
	create_domain_file # create file on host
        create_zone_file # create zone
	update_named_conf # include zone
	
        echo "Domain $domain_name has been added for user $user."
    else
        echo "Failed to add domain $domain_name for user $user (id:$user_id)."
    fi
}


#echo "Addin domain $domain_name for user ID: $user_id"
add_domain "$user_id" "$domain_name"
