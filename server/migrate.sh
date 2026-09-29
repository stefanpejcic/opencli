#!/bin/bash
################################################################################
# Script Name: server/migrate.sh
# Description: Migrates all data from this server to another.
# Usage: opencli server-migrate -h <DESTINATION_IP> --user root --password <DESTINATION_PASSWORD> [--force] [--exclude-* options]
# Author: Stefan Pejcic
# Created: 26.06.2025
# Last Modified: 21.08.2026
# Company: OpenPanel, LLC.
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
Usage: opencli server-migrate -h <remote_host> -u <remote_user> [--password <password>] [--force] [--exclude-home] [--exclude-logs] [--exclude-csf] [--exclude-mail] [--exclude-bind] [--exclude-openpanel] [--exclude-mysql] [--exclude-stack] [--exclude-postupdate] [--exclude-users] [--exclude-contexts]
'

# shellcheck disable=SC1091
. /usr/local/opencli/lib/podman.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/requirement.sh

REMOTE_HOST=""
REMOTE_USER=""
REMOTE_PASS=""
EXCLUDE_HOME=0
EXCLUDE_LOGS=0
EXCLUDE_MAIL=0
EXCLUDE_BIND=0
EXCLUDE_CSF=0
EXCLUDE_OPENPANEL=0
EXCLUDE_MYSQL=0
EXCLUDE_STACK=0
EXCLUDE_POSTUPDATE=0
EXCLUDE_USERS=0
EXCLUDE_CONTEXTS=0
FORCE=0
COMPOSE_START_MAIL=0

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
	--exclude-csf)
            EXCLUDE_CSF=1
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
        --force)
            FORCE=1
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

# numeric ids + hardlinks/acls/xattrs so rootless podman volumes (subuid-owned files) survive the copy
RSYNC_OPTS="-azHAX --numeric-ids" #--progress

export SSHPASS="$REMOTE_PASS"

# persistent log, openadmin's /tmp/server_migrate.log gets overwritten on every run
start_time=$(date +%s)
log_dir="/var/log/openpanel/admin/migrations"
mkdir -p "$log_dir"
log_file="$log_dir/${REMOTE_HOST}_$(date +'%Y-%m-%d_%H-%M-%S').log"
exec > >(tee -a "$log_file") 2>&1
echo "Migration started, log file: $log_file"
# openadmin reads the pid from line 2 and SUCCESS:/FATAL ERROR: from the last line for the log list status
echo "PID: $$"
MIGRATE_ERRORS=()

check_install_sshpass() {
	# If a password is provided, use sshpass for rsync/scp
	if [[ -n "$REMOTE_PASS" ]]; then
		require_command sshpass
  		RSYNC_CMD=(sshpass -e rsync $RSYNC_OPTS -e "ssh -o StrictHostKeyChecking=no")
	else
	    	RSYNC_CMD=(rsync $RSYNC_OPTS)
	fi
}



check_disk_used_on_source() {
    HOME_DIR="/home"
    USED_HOME_ON_SOURCE=$(df --output=used "$HOME_DIR" | tail -n 1)
    USED_HOME_ON_SOURCE_BYTES=$((USED_HOME_ON_SOURCE * 1024)) #1K blocks
}

check_if_dest_has_space(){
    echo "Checking available disk on destination server ..."

    AVAILABLE_HOME_ON_DEST=$(sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
        "df --output=avail $HOME_DIR | tail -n 1")

    # empty means ssh itself failed, don't report that as a full disk
    if [[ ! "$AVAILABLE_HOME_ON_DEST" =~ ^[0-9]+$ ]]; then
        echo "FATAL ERROR: Could not connect to ${REMOTE_USER}@${REMOTE_HOST}. Check the SSH user, password and that SSH is reachable."
        exit 1
    fi

    AVAILABLE_HOME_ON_DEST_BYTES=$((AVAILABLE_HOME_ON_DEST * 1024)) #1K blocks

    if [[ $AVAILABLE_HOME_ON_DEST_BYTES -ge $USED_HOME_ON_SOURCE_BYTES ]]; then
        echo "There is enough disk space on destination server."
    else
        echo "Available: $AVAILABLE_HOME_ON_DEST_BYTES bytes - Needed: $USED_HOME_ON_SOURCE_BYTES bytes"
        echo "FATAL ERROR: Not enough disk space on destination."
        exit 1
    fi
}



get_server_ipv4(){
	current_ip=$(curl --silent --max-time 1 -4 "https://ip.openpanel.com" || curl --silent --max-time 1 -4 "https://ifconfig.me/ip")

	if [ -z "$current_ip" ]; then
	    current_ip=$(ip addr|grep 'inet '|grep global|head -n1|awk '{print $2}'|cut -f1 -d/)
	fi

	    is_valid_ipv4() {
	        local ip=$1
	        # is it ip
	        [[ $ip =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] && \
	        # is it private
	        ! [[ $ip =~ ^10\. ]] && \
	        ! [[ $ip =~ ^172\.(1[6-9]|2[0-9]|3[0-1])\. ]] && \
	        ! [[ $ip =~ ^192\.168\. ]]
	    }

	if ! is_valid_ipv4 "$current_ip"; then
	        echo "Invalid or private IPv4 address: $current_ip. OpenPanel requires a public IPv4 address to bind Nginx configuration files."
	fi

}


get_remote_ipv4() {
    REMOTE_IP=$(sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
        "curl --silent --max-time 3 -4 https://ip.openpanel.com || curl --silent --max-time 3 -4 https://ifconfig.me/ip || hostname -I | awk '{print \$1}'" < /dev/null)
    if [[ ! "$REMOTE_IP" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
        echo "[!] Could not detect the public IPv4 of ${REMOTE_HOST}, using ${REMOTE_HOST} as is."
        REMOTE_IP="$REMOTE_HOST"
    fi
}

get_users_count_on_destination() {

	user_count_query="SELECT COUNT(*) FROM users"

    # shellcheck disable=SC2154 # config_file and mysql_database are set by the sourced $DB_CONFIG_FILE
    if ! user_count=$(sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
    "mariadb --defaults-extra-file=$config_file -D $mysql_database -e \"$user_count_query\" -sN"); then
            echo "FATAL ERROR: Unable to check users from remote server. Is OpenPanel installed?"
            exit 1
        fi
    
        if [ "$user_count" -gt 0 ]; then
            echo "FATAL ERROR: Migration is possible only to a freshly installed OpenPanel with no existing users."
            exit 1
        fi
}




copy_user_accounts() {
    TMPDIR=$(mktemp -d)
    awk -F: '$3 >= 1000 && $3 < 65534 {print}' /etc/passwd > "$TMPDIR/passwd.users"
    awk -F: '$3 >= 1000 && $3 < 65534 {print}' /etc/group > "$TMPDIR/group.users"
    awk -F: 'NR==FNR {u[$1]; next} $1 in u' "$TMPDIR/passwd.users" /etc/shadow > "$TMPDIR/shadow.users"
    # useradd hands out subuid ranges in creation order, keep the source ones so volume files keep their owners
    awk -F: 'NR==FNR {u[$1]; next} $1 in u' "$TMPDIR/passwd.users" /etc/subuid > "$TMPDIR/subuid.users"
    awk -F: 'NR==FNR {u[$1]; next} $1 in u' "$TMPDIR/passwd.users" /etc/subgid > "$TMPDIR/subgid.users"
    "${RSYNC_CMD[@]}" "$TMPDIR/passwd.users" "$TMPDIR/group.users" "$TMPDIR/shadow.users" "$TMPDIR/subuid.users" "$TMPDIR/subgid.users" "${REMOTE_USER}"@"${REMOTE_HOST}":/root/
    rm -rf "$TMPDIR" >/dev/null

sshpass -e ssh -q -o LogLevel=ERROR -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" <<'EOF' >/dev/null 2>&1
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

for f in subuid subgid; do
    [ -s "/root/$f.users" ] || continue
    cut -d: -f1 "/root/$f.users" | while read -r user; do sed -i "/^$user:/d" "/etc/$f"; done
    cat "/root/$f.users" >> "/etc/$f"
done

rm -rf "$USER_PASSWD" "$USER_GROUP" "$USER_SHADOW" /root/subuid.users /root/subgid.users

EOF
    
}

store_running_containers_for_users() {
output_file="/tmp/docker_containers_names.txt"
: > "$output_file"  # clear the file

for userdir in /home/*; do
    if [ -d "$userdir" ]; then
        username=$(basename "$userdir")
        compose_file="$userdir/docker-compose.yml"

        if [ -f "$compose_file" ]; then
            echo "Checking podman context for user: $username"
            containers=$(podman_user "$username" ps --format "{{.Names}}" 2>/dev/null)

            if [ -n "$containers" ]; then
                containers_single_line=$(echo "$containers" | tr '\n' ' ' | sed 's/ $//')
                echo "$username: $containers_single_line" >> "$output_file"
            else
                echo "$username: no containers" >> "$output_file"
            fi
        fi
    fi
done

"${RSYNC_CMD[@]}" $output_file "${REMOTE_USER}"@"${REMOTE_HOST}":$output_file
}

restore_running_containers_for_all_users() {
output_file="/tmp/docker_containers_names.txt"

# Count total lines for progress display
TOTALCOUNT=$(wc -l < "$output_file")
CURRENT=0

# Open the file on FD 3 to avoid stdin conflicts
exec 3<"$output_file"

while IFS=: read -r username containers <&3; do
    CURRENT=$((CURRENT+1))
    username=$(echo "$username" | xargs)

    if [[ -z "$username" ]] || [[ "$containers" =~ no\ containers ]]; then
        #echo "Skipping user $username (no containers or empty line)"
        continue
    fi

    echo "Starting containers for context: $username ($CURRENT/$TOTALCOUNT)..."
    sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
        "remote_uid=\$(stat -c '%u' /home/$username); CONTAINER_HOST=unix:///hostfs/run/user/\${remote_uid}/podman/podman.sock podman-compose -f /home/$username/docker-compose.yml down >/dev/null 2>&1; CONTAINER_HOST=unix:///hostfs/run/user/\${remote_uid}/podman/podman.sock podman-compose -f /home/$username/docker-compose.yml up -d $containers"
done

# Close FD 3
exec 3<&-
}

setup_remote_podman_for_all_users() {
    # context resolution is dynamic per user uid under podman -- nothing to register, no per-user AppArmor (that was rootlesskit, podman doesn't use it), no /run/user/* to rsync (ephemeral, recreated by systemd), and ~/.config/containers/*.conf already rides along with the home dir rsync elsewhere in this script
	awk -F: '$3 >= 1000 && $3 < 65534 {print $1 ":" $3}' /etc/passwd > /tmp/userlist.txt
	TOTALCOUNT=$(wc -l < /tmp/userlist.txt)
	CURRENT=0

	# Open the file on FD 3
	exec 3</tmp/userlist.txt

	# shellcheck disable=SC2034 # USER_ID is a required positional placeholder to parse USERNAME out of the "user:uid" line
	while IFS=: read -r USERNAME USER_ID <&3; do
	    CURRENT=$((CURRENT+1))
	    SRC="/home/$USERNAME/.config/containers"
	    if [[ -d "$SRC" ]]; then
	        echo "Setting linger for: $USERNAME"
		sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
		    "loginctl enable-linger $USERNAME" \
		    >/dev/null 2>&1 < /dev/null || echo "Failed to enable linger for $USERNAME"

	        echo "Enabling rootless podman for: $USERNAME ($CURRENT/$TOTALCOUNT) ..."

		# wait for user@uid before systemctl --user, enable-linger returns before it's up
		sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
		    "uid=\$(id -u $USERNAME); for i in \$(seq 30); do systemctl is-active user@\$uid.service >/dev/null 2>&1 && break; sleep 1; done; systemctl --user -M $USERNAME@ daemon-reload; systemctl --user -M $USERNAME@ reset-failed podman.socket; systemctl --user -M $USERNAME@ enable --now podman.socket; systemctl --user -M $USERNAME@ is-active podman.socket" \
		    >/dev/null 2>&1 < /dev/null || echo "Failed to enable podman.socket for $USERNAME"
	    else
	        echo "No .config/containers directory for $USERNAME, skipping."
	    fi
            echo "[OK] Context $USERNAME processed"
	    echo ""
	done

	# Close FD 3
	exec 3<&-
}

restart_services_on_target() {
            echo "Restarting services on ${REMOTE_HOST} server ..."
            # same root services as on this server
            ROOT_SERVICES=$(podman ps --filter label=io.podman.compose.project=root --format '{{ index .Labels "com.docker.compose.service" }}' 2>/dev/null | sort -u | tr '\n' ' ')
            [[ -z "$ROOT_SERVICES" ]] && ROOT_SERVICES="openpanel_mysql openpanel_redis openpanel bind9 caddy openadmin_ftp"
            # the synced compose file changes the config hash, podman-compose then downs the whole stack itself and
            # openpanel's removal can leave a storage-only container behind that blocks the name, so down + clean up first
            local restart_out
            restart_out=$(sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" bash -s 2>&1 <<EOF
cd /root && podman-compose down >/dev/null 2>&1
for d in /var/lib/containers/storage/overlay/*/merged; do
    mountpoint -q "\$d" || [ -z "\$(ls -A "\$d" 2>/dev/null)" ] || rm -rf "\$d"
done
podman ps -a --external --format '{{.ID}} {{.Status}}' | awk '\$2=="Storage" {print \$1}' | xargs -r -n1 podman rm -f --storage >/dev/null 2>&1
podman-compose up -d $ROOT_SERVICES >/dev/null 2>&1
sleep 5
podman exec openpanel_dns rndc reconfig >/dev/null 2>&1
podman exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1
systemctl restart admin >/dev/null 2>&1
for c in $ROOT_SERVICES; do
    n=\$(podman ps -a --filter label=com.docker.compose.service=\$c --filter label=io.podman.compose.project=root --format '{{.Names}} {{.State}}' | head -1)
    [[ "\$n" == *running* ]] || echo "[!] Service \$c is not running on destination: \${n:-missing}"
done
EOF
)
            [[ -n "$restart_out" ]] && echo "$restart_out"
            while IFS= read -r line; do
                [[ "$line" == "[!] "* ]] && MIGRATE_ERRORS+=("${line#\[!\] }")
            done <<< "$restart_out"

	if [[ $COMPOSE_START_MAIL -eq 1 ]]; then
            echo "Starting mailserver and webmail on ${REMOTE_HOST} server ..."
            sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
                "cd /usr/local/mail/openmail && podman-compose up -d mailserver roundcube >/dev/null 2>&1"
	fi

	#todo: ftp, clamav 
  
}

refresh_quotas() {
            echo "Recalculating disk and inodes usage for all users on ${REMOTE_HOST} ..."
            sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
                "opencli user-quota >/dev/null 2>&1"
}

  
replace_ip_in_zones() {
	sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" bash -c "'
	    zones_dir=\"/etc/bind/zones\"
	
	    for ZONE_CONF in \"\$zones_dir\"/*.zone; do
	        if [ -f \"\$ZONE_CONF\" ]; then
	            domain=\$(basename \"\$ZONE_CONF\" .zone)
	            sed -i \"s/$current_ip/$REMOTE_IP/g\" \"\$ZONE_CONF\"
	            echo \"Updated DNS zone for domain \$domain - \$ZONE_CONF\"
	        fi
	    done
	'"
}


# synced configs still bind to the source ip, sentinel then fails to recreate the stack on the new server
replace_ip_in_configs() {
    [[ -n "$current_ip" && "$current_ip" != "$REMOTE_IP" ]] || return 0
    echo "Replacing $current_ip with $REMOTE_IP in stack and Caddy configuration ..."
    sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
        "for f in /root/docker-compose.yml /root/.env /etc/openpanel/caddy/Caddyfile /etc/openpanel/caddy/redirects.conf /etc/openpanel/openpanel/conf/openpanel.config; do [ -f \$f ] && sed -i 's/${current_ip//./\\.}/$REMOTE_IP/g' \$f; done" < /dev/null
}

# MAIN
DB_CONFIG_FILE="/usr/local/opencli/db.sh"
# shellcheck disable=SC1090,SC1091
. "$DB_CONFIG_FILE"

ssh-keygen -f '/root/.ssh/known_hosts' -R "$REMOTE_HOST" >/dev/null 2>&1
check_install_sshpass
get_server_ipv4
get_remote_ipv4
check_disk_used_on_source
check_if_dest_has_space


if [[ $FORCE -eq 0 ]]; then
	get_users_count_on_destination
fi

if [[ $EXCLUDE_USERS -eq 0 ]]; then
    echo "Creating system users on remote server ..."
    copy_user_accounts
fi

if [[ $EXCLUDE_HOME -eq 0 ]]; then
    echo "Syncing files (/home directory) ..."
    # podman image/container state points at the source's shared image store layers and won't start on a new server, containers get recreated from volumes
    RSYNC_OUTPUT=$("${RSYNC_CMD[@]}" --include='/*/docker-data/volumes/' --exclude='/*/docker-data/*' --exclude='/*/sockets/*/*.sock' --exclude='/*/sockets/*/*.pid' /home/ "${REMOTE_USER}@${REMOTE_HOST}:/home/" 2>&1)
    RSYNC_EXIT=$?
    echo "$RSYNC_OUTPUT"
    if [[ $RSYNC_EXIT -eq 0 ]]; then
        echo "[OK] Files have been copied to the remote server."
	echo ""
    else
        MIGRATE_ERRORS+=("home directory rsync failed")
        echo "[ERROR] Rsync failed! Output:"
        echo "$RSYNC_OUTPUT"
        echo "FATAL ERROR: Syncing /home to ${REMOTE_HOST} failed."
        exit 1
    fi
fi

if [[ $EXCLUDE_CONTEXTS -eq 0 ]]; then
    echo "Enabling rootless podman for all users ..."
    setup_remote_podman_for_all_users
fi


sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
"systemctl daemon-reload" 





if [[ $EXCLUDE_LOGS -eq 0 ]]; then
    echo "Syncing /var/log/openpanel ..."
    "${RSYNC_CMD[@]}" /var/log/openpanel/ "${REMOTE_USER}"@"${REMOTE_HOST}":/var/log/openpanel/

    echo "Syncing /var/log/caddy/ ..."
    "${RSYNC_CMD[@]}" /var/log/caddy/ "${REMOTE_USER}"@"${REMOTE_HOST}":/var/log/caddy/
fi

if [[ $EXCLUDE_MAIL -eq 0 ]]; then
    PANEL_CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
    key_value=$(grep "^key=" $PANEL_CONFIG_FILE | cut -d'=' -f2-)

	if [ -n "$key_value" ]; then
	    if [ -d /usr/local/mail/openmail ]; then
	        echo "Syncing /usr/local/mail/openmail ..."
	        "${RSYNC_CMD[@]}" /usr/local/mail/openmail/ "${REMOTE_USER}"@"${REMOTE_HOST}":/usr/local/mail/openmail/
	        COMPOSE_START_MAIL=1
	    fi

	    STORE_EMAILS_IN=$(grep -E '^email_storage_location=' /etc/openpanel/openadmin/config/admin.ini 2>/dev/null | cut -d'=' -f2- | xargs)
	    if [[ "$STORE_EMAILS_IN" == /* && -d "$STORE_EMAILS_IN" ]]; then
	        echo "Syncing mailboxes from ${STORE_EMAILS_IN%/}/ ..."
	        sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" "mkdir -p ${STORE_EMAILS_IN%/}"
	        "${RSYNC_CMD[@]}" "${STORE_EMAILS_IN%/}/" "${REMOTE_USER}"@"${REMOTE_HOST}":"${STORE_EMAILS_IN%/}/"
	        COMPOSE_START_MAIL=1
	    fi
	fi


fi

if [[ $EXCLUDE_CSF -eq 0 ]]; then
    echo "Syncing /etc/csf/ ..."
    "${RSYNC_CMD[@]}" /etc/csf/ "${REMOTE_USER}"@"${REMOTE_HOST}":/etc/csf/     
    sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
	"csf -a $current_ip > /dev/null && csf -r >/dev/null && systemctl restart lfd"    
fi


if [[ $EXCLUDE_BIND -eq 0 ]]; then
    echo "Syncing /etc/bind ..."
    "${RSYNC_CMD[@]}" /etc/bind/ "${REMOTE_USER}"@"${REMOTE_HOST}":/etc/bind/
    replace_ip_in_zones   
fi

if [[ $EXCLUDE_OPENPANEL -eq 0 ]]; then
    echo "Syncing /etc/openpanel ..."
    "${RSYNC_CMD[@]}" /etc/openpanel/ "${REMOTE_USER}"@"${REMOTE_HOST}":/etc/openpanel/
    replace_ip_in_configs

    echo "Syncing system cronjobs..."
    "${RSYNC_CMD[@]}" /etc/cron.d/openpanel "${REMOTE_USER}"@"${REMOTE_HOST}":/etc/cron.d/
fi

if [[ $EXCLUDE_MYSQL -eq 0 ]]; then
    # dump + import instead of rsyncing the live volume, copying innodb files under a running server gives a corrupt copy
    echo "Exporting panel databases ..."
    DUMP_FILE="/root/openpanel_migrate_dump.sql"
    if podman exec openpanel_mysql sh -c 'mariadb-dump -uroot -p"$MYSQL_ROOT_PASSWORD" --all-databases --single-transaction --routines --events --triggers --flush-privileges' > "$DUMP_FILE" 2>/tmp/migrate_dump.err; then
        "${RSYNC_CMD[@]}" "$DUMP_FILE" "${REMOTE_USER}"@"${REMOTE_HOST}":"$DUMP_FILE"
        echo "Importing panel databases on ${REMOTE_HOST} ..."
        if sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
            "podman exec -i openpanel_mysql sh -c 'mariadb -uroot -p\"\$MYSQL_ROOT_PASSWORD\"' < $DUMP_FILE; rc=\$?; rm -f $DUMP_FILE; exit \$rc"; then
            echo "[OK] Panel databases imported."
        else
            MIGRATE_ERRORS+=("panel database import failed")
            echo "[ERROR] Importing panel databases on destination failed."
        fi
    else
        MIGRATE_ERRORS+=("panel database export failed")
        echo "[ERROR] Could not dump panel databases: $(cat /tmp/migrate_dump.err)"
    fi
    rm -f "$DUMP_FILE" /tmp/migrate_dump.err
fi

if [[ $EXCLUDE_STACK -eq 0 ]]; then
    echo "Syncing /root/docker-compose.yml and /root/.env ..."
    "${RSYNC_CMD[@]}" /root/docker-compose.yml "${REMOTE_USER}"@"${REMOTE_HOST}":/root/
    "${RSYNC_CMD[@]}" /root/.env "${REMOTE_USER}"@"${REMOTE_HOST}":/root/
fi

replace_ip_in_configs

if [[ $EXCLUDE_POSTUPDATE -eq 0 ]]; then
    if [[ -e /root/openpanel_run_after_update ]]; then
        echo "Syncing /root/openpanel_run_after_update ..."
        "${RSYNC_CMD[@]}" /root/openpanel_run_after_update "${REMOTE_USER}"@"${REMOTE_HOST}":/root/
    fi
fi


# quotas come from the plans in the panel db, so only after it's imported
echo "Restoring user quotas ..."
sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
	"opencli user-quota --update --all"


store_running_containers_for_users        # export running contianers on source and copy to dest
restore_running_containers_for_all_users  # start containers per context on dest
restart_services_on_target                # restart openpanel, webserver and admin on dest
refresh_quotas                            # recalculate users usage on dest

# what got moved, for the log and the destination's notifications
send_migration_summary() {
    local q users user_count domain_count site_count mail_count ftp_count home_size elapsed skipped status title message
    q() { mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s -e "$1" 2>/dev/null; }
    users=$(q "SELECT GROUP_CONCAT(username ORDER BY username SEPARATOR ', ') FROM users;")
    user_count=$(q "SELECT COUNT(*) FROM users;")
    domain_count=$(q "SELECT COUNT(*) FROM domains;")
    site_count=$(q "SELECT COUNT(*) FROM sites;")
    mail_count=$(grep -c '|' /usr/local/mail/openmail/docker-data/dms/config/postfix-accounts.cf 2>/dev/null || echo 0)
    ftp_count=$(cat /etc/openpanel/ftp/users/*/users.list 2>/dev/null | grep -c '|')
    home_size=$(du -sh /home 2>/dev/null | cut -f1)
    elapsed=$(( $(date +%s) - start_time ))

    skipped=()
    [[ $EXCLUDE_USERS -eq 1 ]] && skipped+=("system users")
    [[ $EXCLUDE_HOME -eq 1 ]] && skipped+=("home directories")
    [[ $EXCLUDE_CONTEXTS -eq 1 ]] && skipped+=("podman contexts")
    [[ $EXCLUDE_LOGS -eq 1 ]] && skipped+=("logs")
    [[ $EXCLUDE_MAIL -eq 1 ]] && skipped+=("mail")
    [[ $EXCLUDE_CSF -eq 1 ]] && skipped+=("csf")
    [[ $EXCLUDE_BIND -eq 1 ]] && skipped+=("dns zones")
    [[ $EXCLUDE_OPENPANEL -eq 1 ]] && skipped+=("/etc/openpanel")
    [[ $EXCLUDE_MYSQL -eq 1 ]] && skipped+=("panel database")
    [[ $EXCLUDE_STACK -eq 1 ]] && skipped+=("docker stack")
    [[ $EXCLUDE_POSTUPDATE -eq 1 ]] && skipped+=("post-update script")

    status="completed"
    [[ ${#MIGRATE_ERRORS[@]} -gt 0 ]] && status="completed with ${#MIGRATE_ERRORS[@]} error(s)"
    title="Server migrated from $current_ip"
    message="Migration from $current_ip $status in $((elapsed / 60))m $((elapsed % 60))s. Users ($user_count): ${users:-none}. Domains: ${domain_count:-0}. Websites: ${site_count:-0}. Email accounts: $mail_count. FTP accounts: $ftp_count. Home directories: $home_size."
    [[ ${#skipped[@]} -gt 0 ]] && message+=" Skipped: $(IFS=,; echo "${skipped[*]}" | sed 's/,/, /g')."
    [[ ${#MIGRATE_ERRORS[@]} -gt 0 ]] && message+=" Errors: $(printf '%s; ' "${MIGRATE_ERRORS[@]}" | sed 's/; $//')."

    echo ""
    echo "$message"
    echo "Log file: $log_file"

    # quoted for the remote shell, the message has spaces and parentheses
    sshpass -e ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
        "nohup opencli sentinel --action=user_transfer --title=$(printf '%q' "$title") --message=$(printf '%q' "$message") >/dev/null 2>&1 &" < /dev/null
}

send_migration_summary
echo "[OK] Sync complete"
echo "SUCCESS: Migration to ${REMOTE_HOST} completed."
