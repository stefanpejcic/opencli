#!/bin/bash


DB_CONFIG_FILE="/usr/local/opencli/db.sh"

username="$1"
DEBUG=false             # Default value for DEBUG

if [ "$2" = "--debug" ] || [ "$3" = "--debug" ]; then
    DEBUG=true
fi


log() {
    if $DEBUG; then
        echo "$1"
    fi
}



. "$DB_CONFIG_FILE"



copy_domain_zones() {
    local caddy_dir="/etc/openpanel/caddy/domains/"
    local caddy_suspended_dir="/etc/openpanel/caddy/suspended_domains/"
    local zones_dir="/etc/bind/zones/"
    local domain_names=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT domain_url FROM domains WHERE user_id='$user_id';" -N)

    
    for domain_name in $domain_names; do
      cp ${caddy_dir}${domain_name}.conf ${caddy_vhosts}${domain_name}.conf > /dev/null 2>&1 
      cp ${caddy_suspended_dir}${domain_name}.conf ${caddy_suspended_vhosts}${domain_name}.conf > /dev/null 2>&1 
      cp ${zones_dir}${domain_name}.zone ${dns_zones}${domain_name}.zone > /dev/null 2>&1 
    done        

}



export_user_data_from_database() {

    echo "Exporting user data from OpenPanel database.."
    user_id=$(mysql -e "SELECT id FROM users WHERE username='$username';" -N)

    if [ -z "$user_id" ]; then
        echo "ERROR: export_user_data_to_sql: User '$username' not found in the database."
        exit 1
    fi

    


    check_success() {
      if [ $? -eq 0 ]; then
        echo "- Exporting $1 from database successful"
      else
        echo "ERROR: Exporting $1 from database failed"
      fi
    }

# Export User Data with INSERT INTO
mysql --defaults-extra-file=$config_file -N -e "
    SELECT CONCAT('INSERT INTO panel.users (id, username, password, email, services, user_domains, twofa_enabled, otp_secret, plan, registered_date, server, plan_id) VALUES (',
        id, ',', QUOTE(username), ',', QUOTE(password), ',', QUOTE(email), ',', QUOTE(services), ',', QUOTE(user_domains), ',', twofa_enabled, ',', QUOTE(otp_secret), ',', QUOTE(plan), ',', IFNULL(QUOTE(registered_date), 'NULL'), ',', QUOTE(server), ',', plan_id, ');')
    FROM panel.users WHERE id = $user_id
" > $openpanel_database/users.sql
check_success "User data export"


# Export User's Plan Data with INSERT INTO
mysql --defaults-extra-file=$config_file -N -e "
    SELECT CONCAT('INSERT INTO panel.plans (id, name, description, domains_limit, websites_limit, email_limit, ftp_limit, disk_limit, inodes_limit, db_limit, cpu, ram, docker_image, bandwidth) VALUES (',
        p.id, ',', QUOTE(p.name), ',', QUOTE(p.description), ',', p.domains_limit, ',', p.websites_limit, ',', p.email_limit, ',', p.ftp_limit, ',', QUOTE(p.disk_limit), ',', p.inodes_limit, ',', p.db_limit, ',', QUOTE(p.cpu), ',', QUOTE(p.ram), ',', QUOTE(p.docker_image), ',', p.bandwidth, ');')
    FROM panel.plans p
    JOIN panel.users u ON u.plan_id = p.id
    WHERE u.id = $user_id
" > $openpanel_database/plans.sql
check_success "Plan data export"


# Export Domains Data for User with INSERT INTO
mysql --defaults-extra-file=$config_file -N -e "
    SELECT CONCAT('INSERT INTO panel.domains (domain_id, user_id, domain_url, docroot, php_version) VALUES (',
        domain_id, ',', user_id, ',', QUOTE(domain_url), ',', QUOTE(docroot), ',', QUOTE(php_version), ');')
    FROM panel.domains WHERE user_id = $user_id
" > $openpanel_database/domains.sql
check_success "Domains data export"


# Export Sites Data for User with INSERT INTO
mysql --defaults-extra-file=$config_file -N -e "
    SELECT CONCAT('INSERT INTO panel.sites (id, domain_id, site_name, admin_email, version, created_date, type, ports, path) VALUES (',
        s.id, ',', s.domain_id, ',', QUOTE(s.site_name), ',', QUOTE(s.admin_email), ',', QUOTE(s.version), ',', QUOTE(s.created_date), ',', QUOTE(s.type), ',', s.ports, ',', QUOTE(s.path), ');')
    FROM panel.sites s
    JOIN panel.domains d ON s.domain_id = d.domain_id
    WHERE d.user_id = $user_id
" > $openpanel_database/sites.sql
check_success "Sites data export"


    # no need for sessions!

    echo ""
    echo "User '$username' data exported to $openpanel_database successfully."
}


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








# MAIN


result=$(get_user_info "$username")
user_id=$(echo "$result" | cut -d',' -f1)
context=$(echo "$result" | cut -d',' -f2)


if [ -z "$user_id" ]; then
    echo "FATAL ERROR: user $username does not exist."
    exit 1
fi


mkdirs() {

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


tar_everything() {
  echo "Creating archive for all user files.."
  # home files
  tar czpf "${backups_dir}/backup_${username}_$(date +%Y%m%d_%H%M%S).tar.gz" -C /home/"$context" --exclude='*/.sock' .
}


copy_files_temporary_to_user_home() {

  # database
  export_user_data_from_database
  
  # apparmor profile
  echo "Collectiong AppArmor profile.."
  cp /etc/apparmor.d/home.$context.bin.rootlesskit $apparmor_dir
  # https://media2.giphy.com/media/v1.Y2lkPTc5MGI3NjExYWx1MjY4YXB0YTRla3dlazMxYmhkM3k2MWV0eDVsNDUxcHQ1aW9jNyZlcD12MV9pbnRlcm5hbF9naWZfYnlfaWQmY3Q9Zw/uNE1fngZuYhIQ/giphy.gif
  #cp /etc/apparmor.d/$(echo /home/pejcic/bin/rootlesskit | sed -e s@^/@@ -e s@/@.@g) $apparmor_dir

  # core panel data
  echo "Collectiong core OpenPanel files.."
  cp -r /etc/openpanel/openpanel/core/users/$context/  $openpanel_core

  # caddy and bind9
  echo "Collectiong DNS zones and Caddy files.."
  copy_domain_zones
  
  echo "Collectiong Docker context information.."
  echo "$context" > /home/$context/context

}


clean_tmp_files() {
    echo "Cleaning up temporary files.."
    rm -rf $apparmor_dir $openpanel_core ${caddy_vhosts} ${caddy_suspended_vhosts} ${dns_zones} #> /dev/null 2>&1 

}

mkdirs
copy_files_temporary_to_user_home
tar_everything
clean_tmp_files


