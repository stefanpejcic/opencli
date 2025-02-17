#!/bin/bash


DB_CONFIG_FILE="/usr/local/opencli/db.sh"
archive_path="$1"
DEBUG=false             # Default value for DEBUG

if [ "$2" = "--debug" ] || [ "$3" = "--debug" ]; then
    DEBUG=true
fi

filename=$(basename "$archive_path")
username=$(echo "$filename" | sed -E 's/^backup_([a-zA-Z0-9]+)_.*\.tar\.gz$/\1/')
#username=$(echo "$archive_path" | sed -E 's/.*backup_([a-zA-Z0-9_]+)_.*/\1/')

log() {
    if $DEBUG; then
        echo "$1"
    fi
}

. "$DB_CONFIG_FILE"



mkdirs()  {

  echo "Settings paths for user '$username' and docker context '$context'"
  
  server_apparmor_dir="/etc/apparmor.d/"
  server_openpanel_core="/etc/openpanel/openpanel/core/users/$context"
  server_caddy_vhosts="/etc/openpanel/caddy/domains/"
  server_dns_zones="/etc/bind/zones//"  
  server_caddy_suspended_vhosts="/etc/openpanel/caddy/suspended_domains/"

  mkdir -p $apparmor_dir $server_openpanel_core $server_caddy_vhosts $server_dns_zones $server_caddy_suspended_vhosts 

}


import_user_data_in_database() {

    echo "Importing user data to OpenPanel database.."


    check_success() {
      if [ $? -eq 0 ]; then
        echo "- Importing $1 to database successful"
      else
        echo "ERROR: Importing $1 to database failed"
      fi
    }

    # mysql -e "SET foreign_key_checks = 0;"


    # Import User Data
    mysql -e "SOURCE $openpanel_database/users.sql"
    check_success "User data"
    
    # Import User's Plan Data
    mysql -e "SOURCE $openpanel_database/plans.sql"
    check_success "Plan data"
    
    # Import Domains Data for User
    mysql -e "SOURCE $openpanel_database/domains.sql"
    check_success "Domains data"
    
    # Import Sites Data for User
    mysql -e "SOURCE $openpanel_database/sites.sql"
    check_success "Sites data"

    # mysql -e "SET foreign_key_checks = 1;"

    echo ""
    echo "User '$username' data imported to $openpanel_database successfully."
}



create_context() {
  echo "Creating Docker context '$context'"

	docker context create $context \
	  --docker "host=unix:///hostfs/run/user/$user_id/docker.sock" \
   	  --description "$context"
     
}











advanced() {

  sudo apt install dbus-x11 -y -qq > /dev/null 2>&1
  machinectl shell $context@ /bin/bash -c "
  dbus-launch --sh-syntax --exit-with-session > /dev/null 2>&1
  export XDG_RUNTIME_DIR=/run/user/$(id -u)
  export $(dbus-launch)
  echo $XDG_RUNTIME_DIR
  echo $DBUS_SESSION_BUS_ADDRESS
  docker context rm rootless -f
  docker context create rootless --docker \"host=unix:///run/user/\$(id -u)/docker.sock\"
  docker context use rootless 
  "
}






apparmor_start() {
  echo "Configuring AppArmor.."
  cp -r $apparmor_dir /etc/apparmor.d/
  sudo systemctl restart apparmor.service   >/dev/null 2>&1
  loginctl enable-linger $context   >/dev/null 2>&1


  advanced

  
  machinectl shell $context@ /bin/bash -c "
  systemctl --user daemon-reload > /dev/null 2>&1
  systemctl --user enable docker > /dev/null 2>&1
  systemctl --user start docker > /dev/null 2>&1
  "
  
}








create_user_and_set_quota() {
  echo "Creating user $username"
		    useradd -m -d /home/$context $context
      		    user_id=$(id -u $context)	
        			if [ $? -ne 0 ]; then
        			    echo "Failed creating linux user $context"
        			    exit 1
        			fi
           
    log "Configuring disk and inodes limits for the user"
    # TODO

    
  
    SUDOERS_FILE="/etc/sudoers"
    
    echo "$username ALL=(ALL) NOPASSWD:ALL" >> "$SUDOERS_FILE"
    if grep -q "$username ALL=(ALL) NOPASSWD:ALL" "$SUDOERS_FILE"; then
        :
    else
        echo "Failed to update the sudoers file. Please check the syntax."
    fi

}



change_mysql_perms() {
  docker --context $context exec $username bash -c "chown -R mysql:mysql /var/lib/mysql /var/lib/mysql-files /var/lib/mysql-keyring"
}

file_permissions() {
	echo "Setting user ownership and permisisons.."
	chmod 700 /home/$context/.docker/run #  >/dev/null 2>&1
	chmod 755 -R /home/$context/  # >/dev/null 2>&1
	chown -R $context:$context /home/$context/  # >/dev/null 2>&1
}

compose_up() {
      echo "Starting the container.."

    machinectl shell $context@ /bin/bash -c "cd /home/$context/ && docker compose up -d"
  
  compose_running=$(docker --context $context compose ls)
  
  if echo "$compose_running" | grep -q "/home/$context/docker-compose.yml"; then
      :
  else
      echo "docker-compose.yml for context $context of user: $username is not found or the container did not start!"
  	#docker rm -f "$username" > /dev/null 2>&1
  	#docker context rm "$username" > /dev/null 2>&1
          #killall -u $username > /dev/null 2>&1
          #deluser --remove-home $username > /dev/null 2>&1
    	exit 1
  fi
}


untar_now() {
    echo "Extracting data from the archive.."
  tar xzpf $backups_dir/backup_${username}_*.tar.gz -C /home/$context
}



dirs_to_user_for_mv() {
    echo "Creating directories.."

  apparmor_dir="/home/"$context"/apparmor/"
  openpanel_core="/home/"$context"/op_core/"
  openpanel_database="/home/"$context"/op_db/"
  caddy_vhosts="/home/"$context"/caddy/"
  dns_zones="/home/"$context"/dns/"  
  caddy_suspended_vhosts="/home/"$context"/caddy_suspended/"

  # backup dir!
  backups_dir="/backups"
  
  mkdir -p $apparmor_dir $openpanel_core $openpanel_database $backups_dir $caddy_vhosts $dns_zones $caddy_suspended_vhosts

}


restart_caddy_and_dns() {
    echo "Restarting Caddy and BIND9 services.."

	  if [ $(docker ps -q -f name=caddy) ]; then
 	    log "Caddy is running, validating new domain configuration"
	  else
	    log "Caddy is not running, starting now"
      cd /root && docker compose up -d caddy  >/dev/null 2>&1
     fi

    if [ $(docker ps -q -f name=openpanel_dns) ]; then
      log "DNS service is running, adding the zone"
  	  docker exec openpanel_dns rndc reconfig >/dev/null 2>&1
    else
  	  log "DNS service is not started, starting now"
      cd /root && docker compose up -d bind9  >/dev/null 2>&1
    fi     
    
}

copy_domain_zones() {
    local caddy_dir="/etc/openpanel/caddy/domains/"
    local caddy_suspended_dir="/etc/openpanel/caddy/suspended_domains/"
    local zones_dir="/etc/bind/zones/"

    echo "Copying Caddy VHosts files.."
    cp -r ${caddy_vhosts} ${caddy_dir} > /dev/null 2>&1 
    cp -r ${caddy_suspended_vhosts} ${caddy_suspended_dir} > /dev/null 2>&1 

    echo "Copying DNS zone files for domains.."
    cp -r ${dns_zones} ${zones_dir} > /dev/null 2>&1 
}


op_core_files() {
  echo "Copying core openpanel files for user.."
  cp -r $openpanel_core/ $server_openpanel_core

}


get_just_context() {
  echo "Extracting docker context information from the backup.."
  rm -rf /tmp/$username/
  mkdir -p /tmp/$username/
  tar xzpf $archive_path -C /tmp/$username './context'
  context=$(cat /tmp/$username/context)
  rm /tmp/$username/
}


reload_user_quotas() {
  echo "Reloading user quotas.."
    quotacheck -avm > /dev/null
    repquota -u / > /etc/openpanel/openpanel/core/users/repquota
}

start_panel_service() {
	log "Checking if OpenPanel service is already running, or starting it.."
	cd /root && docker compose up -d openpanel > /dev/null 2>&1
}



get_user_info() {
    local user="$1"
    local query="SELECT id, server FROM users WHERE username = '${username}';"
    
    # Retrieve both id and context
    user_info=$(mysql -se "$query")
    
    # Extract user_id and context from the result
    user_id=$(echo "$user_info" | awk '{print $1}')
    context=$(echo "$user_info" | awk '{print $2}')
    
    echo "$user_id,$context"
}



validate_user() {
  result=$(get_user_info "$username")
  user_id=$(echo "$result" | cut -d',' -f1)
  db_context=$(echo "$result" | cut -d',' -f2)
  
  # todo: compare $context with $db_context

  if [ -z "$user_id" ]; then
      echo "FATAL ERROR: Failed to create user $username"
      exit 1
  else
      echo "Successfully restored user $username"
  fi
}

collect_stats() {
	opencli docker-collect_stats $username  > /dev/null 2>&1
}


get_just_context
mkdirs
dirs_to_user_for_mv
untar_now
create_user_and_set_quota
file_permissions
op_core_files
apparmor_start
create_context
copy_domain_zones
compose_up
change_mysql_perms
import_user_data_in_database
restart_caddy_and_dns
reload_user_quotas
start_panel_service
validate_user
collect_stats
exit 0
