#!/bin/bash
################################################################################
# Script Name: server/migrate.sh
# Description: Migrates all data from this server to another.
# Usage: opencli server-migrate -h <DESTINATION_IP> --user root --password <DESTINATION_PASSWORD>
# Author: Stefan Pejcic
# Created: 26.06.2025
# Last Modified: 26.06.2025
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

: '
Usage: opencli server-migrate -h <remote_host> -u <remote_user> [--password <password>] [--exclude-home] [--exclude-logs] [--exclude-mail] [--exclude-bind] [--exclude-openpanel] [--exclude-mysql] [--exclude-stack] [--exclude-postupdate] [--exclude-users]
'

REMOTE_HOST=""
REMOTE_USER=""
REMOTE_PASS=""
EXCLUDE_HOME=0
EXCLUDE_LOGS=0
EXCLUDE_MAIL=0
EXCLUDE_BIND=0
EXCLUDE_OPENPANEL=0
EXCLUDE_MYSQL=0
EXCLUDE_STACK=0
EXCLUDE_POSTUPDATE=0
EXCLUDE_USERS=0
EXCLUDE_CONTEXTS=0

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--host)
            REMOTE_HOST="$2"
            shift 2
            ;;
        -u|--user)
            REMOTE_USER="$2"
            shift 2
            ;;
        --password)
            REMOTE_PASS="$2"
            shift 2
            ;;
        --exclude-home)
            EXCLUDE_HOME=1
            shift
            ;;
        --exclude-logs)
            EXCLUDE_LOGS=1
            shift
            ;;
        --exclude-mail)
            EXCLUDE_MAIL=1
            shift
            ;;
        --exclude-bind)
            EXCLUDE_BIND=1
            shift
            ;;
        --exclude-openpanel)
            EXCLUDE_OPENPANEL=1
            shift
            ;;
        --exclude-mysql)
            EXCLUDE_MYSQL=1
            shift
            ;;
        --exclude-stack)
            EXCLUDE_STACK=1
            shift
            ;;
        --exclude-postupdate)
            EXCLUDE_POSTUPDATE=1
            shift
            ;;
        --exclude-users)
            EXCLUDE_USERS=1
            shift
            ;;
        --exclude-contexts)
            EXCLUDE_CONTEXTS=1
            shift
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

if [[ -z "$REMOTE_HOST" || -z "$REMOTE_USER" ]]; then
    echo "Usage: opencli server-migrate -h <remote_host> -u <remote_user> [--password <password>] [--exclude-* options]"
    exit 1
fi

RSYNC_OPTS="-avz --progress"

ssh-keygen -f '/root/.ssh/known_hosts' -R $REMOTE_HOST

# If a password is provided, use sshpass for rsync/scp
if [[ -n "$REMOTE_PASS" ]]; then
    if ! command -v sshpass &>/dev/null; then
        echo "sshpass not found. Attempting to install..."
        if [[ -x "$(command -v apt-get)" ]]; then
            sudo apt-get update && sudo apt-get install -y sshpass
        elif [[ -x "$(command -v dnf)" ]]; then
            sudo dnf install -y sshpass
        elif [[ -x "$(command -v yum)" ]]; then
            sudo yum install -y epel-release && sudo yum install -y sshpass
        elif [[ -x "$(command -v pacman)" ]]; then
            sudo pacman -Sy sshpass
        else
            echo "Package manager not supported. Please install sshpass manually."
            exit 2
        fi
    fi
    RSYNC_CMD="sshpass -p '$REMOTE_PASS' rsync $RSYNC_OPTS -e 'ssh -o StrictHostKeyChecking=no'"
else
    RSYNC_CMD="rsync $RSYNC_OPTS"
fi


DB_CONFIG_FILE="/usr/local/opencli/db.sh"
. "$DB_CONFIG_FILE"

get_users_count_on_destination() {

	user_count_query="SELECT COUNT(*) FROM users"

    user_count=$(sshpass -p "$REMOTE_PASS" ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
    "mysql --defaults-extra-file=$config_file -D $mysql_database -e \"$user_count_query\" -sN")
 
        if [ $? -ne 0 ]; then
            echo "[✘] ERROR: Unable to check users from remote server. Is OpenPanel installed?"
            exit 1
        fi
    
        if [ "$user_count" -gt 0 ]; then
            echo "[✘] ERROR: Migration is possible only to a freshly installed OpenPanel with no existing users."
            exit 1
        fi
}


get_users_count_on_destination

copy_user_accounts() {
    TMPDIR=$(mktemp -d)
    # Copy user passwd entries (UID >= 1000)
    awk -F: '$3 >= 1000 {print}' /etc/passwd > "$TMPDIR/passwd.users"
    awk -F: '$3 >= 1000 {print}' /etc/group > "$TMPDIR/group.users"
    # Copy user shadow entries
    grep -F -f <(cut -d: -f1 "$TMPDIR/passwd.users") /etc/shadow > "$TMPDIR/shadow.users"
    # Rsync these files to remote /root/
    eval $RSYNC_CMD "$TMPDIR/passwd.users" "$TMPDIR/group.users" "$TMPDIR/shadow.users" ${REMOTE_USER}@${REMOTE_HOST}:/root/
    echo "User account files for UID >= 1000 copied to /root/ on remote server."
    rm -rf "$TMPDIR"

    # ere now we need to add the suers on remote server!
sshpass -p "$REMOTE_PASS" ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" <<'EOF'
USER_PASSWD="/root/passwd.users"
USER_GROUP="/root/group.users"
USER_SHADOW="/root/shadow.users"

# Add groups
cut -d: -f1,3 "$USER_GROUP" | while IFS=: read -r group gid; do
    if ! getent group "$group" > /dev/null; then
        groupadd -g "$gid" "$group"
    fi
done

# Add users
cut -d: -f1,3,4,5,6,7 "$USER_PASSWD" | while IFS=: read -r user uid gid comment home shell; do
    if ! id "$user" &>/dev/null; then
        useradd -u "$uid" -g "$gid" -c "$comment" -d "$home" -s "$shell" "$user"
    fi
done

# Set passwords from shadow file
cut -d: -f1,2 "$USER_SHADOW" | while IFS=: read -r user hash; do
    if [ -n "$hash" ]; then
        usermod -p "$hash" "$user"
    fi
done

rm -rf $USER_PASSWD $USER_GROUP $USER_SHADOW
echo "Users have been created on the remote server."

EOF
    
}

copy_docker_contexts() {
    awk -F: '$3 >= 1000 {print $1 ":" $3}' /etc/passwd | while IFS=: read USERNAME USER_ID; do
        SRC="/home/$USERNAME/.docker"
        if [[ -d "$SRC" ]]; then
            echo "Creating Docker context: $USERNAME ..."
            sshpass -p "$REMOTE_PASS" ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
                "docker context create $USERNAME --docker 'host=unix:///hostfs/run/user/${USER_ID}/docker.sock' --description '$USERNAME'"

            echo "Starting containers for: $USERNAME"
            sshpass -p "$REMOTE_PASS" ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
                "machinectl shell ${USERNAME}@ /bin/bash -c 'systemctl --user daemon-reload >/dev/null 2>&1; systemctl --user restart docker >/dev/null 2>&1'"

            echo "Fetching plan limits for user from the ..."
            
            query="SELECT p.disk_limit, p.inodes_limit 
                   FROM plans p
                   JOIN users u ON u.plan_id = p.id
                   WHERE u.username = '$username'"
            cpu_ram_info=$(mysql --defaults-extra-file=$config_file -D "$mysql_database" -e "$query" -sN)
            
            if [ -z "$cpu_ram_info" ]; then
                disk_limit="0"
                inodes="0"
            else
                disk_limit=$(echo "$cpu_ram_info" | awk '{print $1}' | sed 's/ //;s/B//')
                inodes=$(echo "$cpu_ram_info" | awk '{print $3}')
            fi

            if [ "$disk_limit" -ne 0 ]; then
            	storage_in_blocks=$((disk_limit * 1024000))
                echo "Setting storage size of ${disk_limit}GB and $inodes inodes for the user"
              	sshpass -p "$REMOTE_PASS" ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
                   "setquota -u $USERNAME $storage_in_blocks $storage_in_blocks $inodes $inodes /"
            else
            	echo "Setting unlimited storage and inodes for the user"
              	sshpass -p "$REMOTE_PASS" ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
              	    "setquota -u $USERNAME 0 0 0 0 /"
            fi

        fi
    done
}

restart_services_on_target() {
            echo "Restarting services on  ${REMOTE_HOST} server ..."
            sshpass -p "$REMOTE_PASS" ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
                "cd /root && docker compose up -d openpanel bind9 caddy && systemctl restart admin"
}

refresh_quotas() {
            echo "Recalculating disk and inodes usage for all users on ${REMOTE_HOST} ..."
            sshpass -p "$REMOTE_PASS" ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
                "quotacheck -avm >/dev/null 2>&1 && repquota -u / > /etc/openpanel/openpanel/core/users/repquota"
}
  
   





if [[ $EXCLUDE_USERS -eq 0 ]]; then
    echo "Extracting and copying user accounts with UID >= 1000 ..."
    copy_user_accounts
fi

if [[ $EXCLUDE_HOME -eq 0 ]]; then
    echo "Syncing /home ..."
    eval $RSYNC_CMD /home/ ${REMOTE_USER}@${REMOTE_HOST}:/home/
fi

if [[ $EXCLUDE_CONTEXTS -eq 0 ]]; then
    echo "Syncing docker contexts ..."
    copy_docker_contexts # create docker context, start docker, set quotas
fi

if [[ $EXCLUDE_LOGS -eq 0 ]]; then
    echo "Syncing /var/log/openpanel ..."
    eval $RSYNC_CMD /var/log/openpanel/ ${REMOTE_USER}@${REMOTE_HOST}:/var/log/openpanel/

    echo "Syncing /var/log/caddy/ ..."
    eval $RSYNC_CMD /var/log/caddy/ ${REMOTE_USER}@${REMOTE_HOST}:/var/log/caddy/
fi

if [[ $EXCLUDE_MAIL -eq 0 ]]; then
    echo "Syncing /var/mail ..."
    eval $RSYNC_CMD /var/mail/ ${REMOTE_USER}@${REMOTE_HOST}:/var/mail/
fi

if [[ $EXCLUDE_BIND -eq 0 ]]; then
    echo "Syncing /etc/bind ..."
    eval $RSYNC_CMD /etc/bind/ ${REMOTE_USER}@${REMOTE_HOST}:/etc/bind/
fi

if [[ $EXCLUDE_OPENPANEL -eq 0 ]]; then
    echo "Syncing /etc/openpanel ..."
    eval $RSYNC_CMD /etc/openpanel/ ${REMOTE_USER}@${REMOTE_HOST}:/etc/openpanel/
fi

if [[ $EXCLUDE_MYSQL -eq 0 ]]; then
    echo "Syncing root_mysql Docker volume ..."
    if [[ -d "/var/lib/docker/volumes/root_mysql/_data" ]]; then
        eval $RSYNC_CMD /var/lib/docker/volumes/root_mysql/_data/ ${REMOTE_USER}@${REMOTE_HOST}:/var/lib/docker/volumes/root_mysql/_data/
    else
        echo "/var/lib/docker/volumes/root_mysql/_data does not exist! Skipping."
    fi
fi

if [[ $EXCLUDE_STACK -eq 0 ]]; then
    echo "Syncing /root/docker-compose.yml and /root/.env ..."
    eval $RSYNC_CMD /root/docker-compose.yml ${REMOTE_USER}@${REMOTE_HOST}:/root/
    eval $RSYNC_CMD /root/.env ${REMOTE_USER}@${REMOTE_HOST}:/root/
fi

if [[ $EXCLUDE_POSTUPDATE -eq 0 ]]; then
    echo "Syncing /root/openpanel_run_after_update ..."
    eval $RSYNC_CMD /root/openpanel_run_after_update ${REMOTE_USER}@${REMOTE_HOST}:/root/
fi

restart_services_on_target
refresh_quotas

echo "Sync complete."
