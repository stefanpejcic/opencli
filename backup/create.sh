#!/bin/bash

RED="\e[31m"
GREEN="${GREEN}"
ENDCOLOR="${END}"


# Get the container name from the first argument
container_name="$1"

# Check if a container name is provided
if [ -z "$container_name" ]; then
  echo "Usage: $0 <username>"
  exit 1
fi

volume_name="mysql-$container_name"
timestamp=$(date +"%Y%m%d%H%M%S")
backup_dir="/backup/$container_name/$timestamp"
backup_file="/backup/$container_name/$timestamp/docker_${container_name}_${timestamp}.tar"

#########################################################################
############################### DB LOGIN ################################ 
#########################################################################
    # MySQL database configuration
    config_file="/usr/local/admin/db.cnf"

    # Check if the config file exists
    if [ ! -f "$config_file" ]; then
        echo "Config file $config_file not found."
        exit 1
    fi

    mysql_database="panel"

#########################################################################



# Create the backup directory
mkdir -p "$backup_dir"

echo "Creating a backup of user container.."

# Export the Docker container to a tar file
docker export "$container_name" > "$backup_file"

# Check if the export was successful
if [ $? -eq 0 ]; then
  echo "${GREEN}[ ✓ ]${END} Exported $container_name to $backup_file"
else
  echo "${RED}ERROR${ENDCOLOR}: exporting $container_name"
fi

backup_mysql_data() {
  # Get the volume name associated with the container
  local volume_name=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/var/lib/mysql"}}{{.Source}}{{end}}{{end}}' "$container_name")

  # Check if a volume is found
  if [ -z "$volume_name" ]; then
    echo "No volume found for container $container_name"
    return 1
  fi

  # Create the backup directory
  mkdir -p "$backup_dir/mysql-volume"
  
  # Copy data from the volume to the backup directory
  rsync -avR $volume_name $backup_dir/mysql-volume/
  
  # Check if the copy operation was successful
  if [ $? -eq 0 ]; then
    echo "${GREEN}[ ✓ ]${END} Copied data from volume $volume_name to $backup_dir/mysql-volume/"
  else
    echo "${RED}ERROR${ENDCOLOR}: copying data from volume $volume_name"
    return 1
  fi
}



export_user_data_from_database() {
    user_id=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT id FROM users WHERE username='$container_name';" -N)

    if [ -z "$user_id" ]; then
        echo "${RED}ERROR${ENDCOLOR}: export_user_data_to_sql: User '$container_name' not found in the database."
        exit 1
    fi

    # Create a single SQL dump file
    backup_file="$backup_dir/user_data_dump.sql"
    
    # Use mysqldump to export data from the 'sites', 'domains', and 'users' tables
    mysqldump --defaults-extra-file="$config_file" --no-create-info --no-tablespaces --skip-extended-insert "$mysql_database" users -w "id='$user_id'" >> "$backup_file"
    mysqldump --defaults-extra-file="$config_file" --no-create-info --no-tablespaces --skip-extended-insert --single-transaction "$mysql_database" domains -w "user_id='$user_id'" >> "$backup_file"
    mysqldump --defaults-extra-file="$config_file" --no-create-info --no-tablespaces --skip-extended-insert --single-transaction "$mysql_database" sites -w "domain_id IN (SELECT domain_id FROM domains WHERE user_id='$user_id')" >> "$backup_file"

    echo "${GREEN}[ ✓ ]${END}User '$container_name' data exported to $backup_file successfully."
}


# Function to backup Apache .conf files and SSL certificates for domain names associated with a user
backup_apache_conf_and_ssl() {

    # Step 1: Get the user_id from the 'users' table
    user_id=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT id FROM users WHERE username='$container_name';" -N)
    
    if [ -z "$user_id" ]; then
        echo "${RED}ERROR${ENDCOLOR}: backup_apache_conf_and_ssl: User '$container_name' not found in the database."
        exit 1
    fi
    
    # Get domain names associated with the user_id from the 'domains' table
    local domain_names=$(mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e "SELECT domain_name FROM domains WHERE user_id='$user_id';" -N)
    echo "Getting Apache configuration for user's domains.."
    # Loop through domain names
    for domain_name in $domain_names; do
        local apache_conf_dir="/etc/apache2/sites-available"
        
        local apache_conf_file="$domain_name.conf"
        
        local backup_apache_conf_dir="$backup_dir/apache_conf"
        
        local certbot_ssl_dir="/etc/letsencrypt/live/$domain_name"
        
        local backup_certbot_ssl_dir="$backup_dir/ssl/$domain_name"

        # Check if the Apache .conf file exists and copy it
        if [ -f "$apache_conf_dir/$apache_conf_file" ]; then
            mkdir -p "$backup_apache_conf_dir"
            cp "$apache_conf_dir/$apache_conf_file" "$backup_apache_conf_dir/$apache_conf_file"
            echo "${GREEN}[ ✓ ]${END} Backed up Apache .conf file for domain '$domain_name' to $backup_apache_conf_dir"
        else
            echo "Apache .conf file for domain '$domain_name' not found."
        fi

        # Check if Certbot SSL certificates exist and copy them
        if [ -d "$certbot_ssl_dir" ]; then
            mkdir -p "$backup_certbot_ssl_dir"
            cp -r "$certbot_ssl_dir"/* "$backup_certbot_ssl_dir/"
            echo "${GREEN}[ ✓ ]${END} Backed up Certbot SSL certificates for domain '$domain_name' to $backup_certbot_ssl_dir"
        else
            echo "Certbot SSL certificates for domain '$domain_name' not found."
        fi
    done
}



backup_mysql_data
export_user_data_from_database
backup_apache_conf_and_ssl
