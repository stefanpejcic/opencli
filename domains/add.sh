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
    echo "Invalid domain name: $domain_name"
    exit 1
fi

# Check if domain already exists
if opencli domains-whoowns "$domain_name" | grep -q "not found in the database."; then
    :
else
    echo "Domain $domain_name already exists."
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
    echo "User $user does not exist."
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


get_webserver_for_user(){
	    output=$(opencli webserver-get_webserver_for_user $user)
	    if [[ $output == *nginx* ]]; then
	        ws="nginx"
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

docker exec $user bash -c "mkdir -p $logs_dir && touch $logs_dir/${domain_name}.log"

docker cp  $vhost_docker_template $user:$vhost_in_docker_file

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

docker exec $user bash -c "mkdir -p /etc/$ws/sites-enabled/ && ln -s $vhost_in_docker_file /etc/$ws/sites-enabled/ && service $ws restart"

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

mkdir -p /etc/openpanel/openpanel/core/users/${user}/domains/ && touch /etc/openpanel/openpanel/core/users/${user}/domains/{domain_name}-block_ips.conf

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
    	clear_cache_for_user
    	make_folder
    	get_webserver_for_user
    	get_server_ipv4
	vhost_files_create
	create_domain_file
    	
        echo "Domain $domain_name has been added for user $user."
    else
        echo "Failed to add domain $domain_name for user $user (id:$user_id)."
    fi
}


echo "Addin domain $domain_name for user ID: $user_id"
add_domain "$user_id" "$domain_name"
