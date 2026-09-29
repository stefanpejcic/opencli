#!/bin/bash
################################################################################
# Script Name: user/transfer.sh
# Description: Transfers a single user account from this server to another.
# Usage: opencli user-transfer --account <OPENPANEL_USER> --host <DESTINATION_IP> --username <DESTINATION_SSH_USERNAME> --password <DESTINATION_SSH_PASSWORD> [--port <SSH_PORT>] [--force] [--live-transfer]
# Author: Stefan Pejcic
# Created: 28.06.2025
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

# shellcheck disable=SC1091
. /usr/local/opencli/lib/podman.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/requirement.sh

pid=$$
start_time=$(date +%s) #used to calculate elapsed time at the end

: '
Usage: opencli user-transfer --account <OPENPANEL_USER> --host <DESTINATION_IP> --username <OPENPANEL_USERNAME> --password <DESTINATION_SSH_PASSWORD> [--live-transfer]
'

USERNAME=""
REMOTE_HOST=""
REMOTE_USER="root"
REMOTE_PORT="22"
REMOTE_PASS=""
FORCE=0
LIVE_TRANSFER=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--host)
            REMOTE_HOST="$2"
            shift 2
            ;;
        -u|--username)
            REMOTE_USER="$2"
            shift 2
            ;;
        --account)
            USERNAME="$2"
            shift 2
            ;;
        --password)
            REMOTE_PASS="$2"
            shift 2
            ;;
        --port)
            REMOTE_PORT="$2"
            shift 2
            ;;
        --force)
            FORCE=1
            shift
            ;;
        --live-transfer)
            LIVE_TRANSFER=true
            shift
            ;;
        *)
            echo "[ERROR] Unknown option: $1"
            exit 1
            ;;
    esac
done


if [[ -z "$REMOTE_HOST" || -z "$USERNAME" ]]; then
    echo "Usage: opencli user-transfer --account <OPENPANEL_USER> --host <DESTINATION_IP> --username <OPENPANEL_USERNAME> --password <DESTINATION_SSH_PASSWORD> [--live-transfer]"
    exit 1
fi

# numeric ids + hardlinks/acls/xattrs so rootless podman storage (subuid-owned files) survives the copy
RSYNC_OPTS="-azHAX --numeric-ids" #--progress

export SSHPASS="$REMOTE_PASS"

timestamp="$(date +'%Y-%m-%d_%H-%M-%S')" #used by log file name
base_name="$(basename "$USERNAME")"
log_dir="/var/log/openpanel/admin/transfers"
mkdir -p $log_dir
log_file="$log_dir/${base_name}_${REMOTE_HOST}_${timestamp}.log"

echo "Import started, log file: $log_file"

log() {
    local message="$1"
    local timestamp; timestamp=$(date +'%Y-%m-%d %H:%M:%S')
    echo "[$timestamp] $message" | tee -a "$log_file"
}


# remote output goes to the log too, the openadmin transfer runs in the background so stdout is lost
logpipe() {
    while IFS= read -r line; do log "  $line"; done
}

log_paths_are() {
    log "Log file: $log_file"
    log "PID: $pid"
}

# what got moved, shown on the destination under the user_transfer notification
notify_destination() {
    local domain_list domains sites mails ftps containers elapsed title message
    domain_list=$(opencli domains-user "$USERNAME" 2>/dev/null | grep -v "No domains found" | awk 'NF {print $1}')
    domains=$(grep -c . <<< "$domain_list")
    sites=$(mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s -e "SELECT COUNT(*) FROM sites s JOIN domains d ON s.domain_id=d.domain_id JOIN users u ON d.user_id=u.id WHERE u.username='$USERNAME';" 2>/dev/null)
    mails=0
    if [[ -n "$domain_list" && -f /usr/local/mail/openmail/docker-data/dms/config/postfix-accounts.cf ]]; then
        mails=$(grep -cE "@($(sed 's/\./\\./g' <<< "$domain_list" | paste -sd'|'))\|" /usr/local/mail/openmail/docker-data/dms/config/postfix-accounts.cf)
    fi
    ftps=$(grep -c '|' "/etc/openpanel/ftp/users/$CONTEXT/users.list" 2>/dev/null)
    containers=$(cut -d: -f2 /tmp/docker_containers_names_${USERNAME}.txt 2>/dev/null | xargs | wc -w)
    elapsed=$(( $(date +%s) - start_time ))
    title="User $USERNAME transferred from ${current_ip:-another server}"
    message="Account $USERNAME was transferred from ${current_ip:-another server} in $((elapsed / 60))m $((elapsed % 60))s. Domains: ${domains:-0}. Websites: ${sites:-0}. Email accounts: ${mails:-0}. FTP accounts: ${ftps:-0}. Containers started: ${containers:-0}."
    [[ "$LIVE_TRANSFER" == true ]] && message+=" Live transfer: the account was suspended on the source server and DNS points here."
    "${SSH_CMD[@]}" "nohup opencli sentinel --action=user_transfer --title=$(printf '%q' "$title") --message=$(printf '%q' "$message") >/dev/null 2>&1 &" < /dev/null
}

success_message() {
    end_time=$(date +%s)
    elapsed=$(( end_time - start_time ))
    hours=$(( elapsed / 3600 ))
    minutes=$(( (elapsed % 3600) / 60 ))
    seconds=$(( elapsed % 60 ))

    log "Elapsed time: ${hours}h ${minutes}m ${seconds}s"
    log "SUCCESS: Transfer process for user $USERNAME completed."
}

whitelist_remote_srv() {
	csf -ta "$REMOTE_HOST" &>/dev/null
}

format_commands() {
	# If a password is provided, use sshpass for rsync/scp
	if [[ -n "$REMOTE_PASS" ]]; then
	    require_command sshpass
	    RSYNC_CMD=(sshpass -e rsync $RSYNC_OPTS -e "ssh -p $REMOTE_PORT -o StrictHostKeyChecking=no")
	    SSH_CMD=(sshpass -e ssh -p "$REMOTE_PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR "${REMOTE_USER}@${REMOTE_HOST}")
	else
	    RSYNC_CMD=(rsync $RSYNC_OPTS -e "ssh -p $REMOTE_PORT -o StrictHostKeyChecking=no")
	    SSH_CMD=(ssh -p "$REMOTE_PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR "${REMOTE_USER}@${REMOTE_HOST}") # for ssh keys!
	fi

 	# csf
  	whitelist_remote_srv

	# test
	log "Testing SSH connection to $REMOTE_USER@$REMOTE_HOST..."
	if "${SSH_CMD[@]}" "echo 'SSH connection established, starting transfer process..'" >/dev/null 2>&1; then
	    log "SSH connection established, starting transfer process.."
	else
	    log "[✘] SSH connection to $REMOTE_HOST failed. Please check credentials or SSH keys."
	    log "Command attempted:"
	    log "${SSH_CMD[*]} echo 'SSH connection established, starting transfer process..'"
	    exit 1
	fi
 
}



get_server_ipv4(){
	current_ip=$(curl --silent --max-time 1 -4 "https://ip.openpanel.com" || curl --silent --max-time 1 -4 "https://ifconfig.me")

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
	    log "Invalid or private IPv4 address: $current_ip. OpenPanel requires a public IPv4 address to bind Nginx configuration files."
	fi
}


PANEL_CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
key_value=$(grep "^key=" $PANEL_CONFIG_FILE | cut -d'=' -f2-)

get_users_count_on_destination() {
	user_count_query="SELECT COUNT(*) FROM users"
    # shellcheck disable=SC2154 # config_file/mysql_database are set by sourced db.sh (see DB_CONFIG_FILE below)
    if ! user_count=$("${SSH_CMD[@]}" "mariadb --defaults-extra-file=$config_file -D $mysql_database -e \"$user_count_query\" -sN"); then
        log "[✘] ERROR: Unable to check users from remote server. Is OpenPanel installed?"
        exit 1
    fi

    if [ -n "$key_value" ]; then
    :
    else
        if [ "$user_count" -gt 2 ]; then
            log "[✘] ERROR: OpenPanel Community edition has a limit of 3 user accounts - which should be enough for private use."
	          log "If you require more than 3 accounts, please consider purchasing the Enterprise version that allows unlimited number of users and domains/websites."
            exit 1
        fi
    fi
}


# "context" is the users.server column (system user / home dir / docker context / ftp context), not necessarily the same as $USERNAME
resolve_context() {
    CONTEXT=$(mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s \
        -e "SELECT server FROM users WHERE username = '$USERNAME';")
    if [[ -z "$CONTEXT" ]]; then
        log "[✘] ERROR: Could not resolve context (server) for user '$USERNAME'. Aborting."
        exit 1
    fi
    log "Resolved context for $USERNAME: $CONTEXT"
}

check_username_exists() {
    username_exists_query="SELECT COUNT(*) FROM users WHERE username = '$USERNAME'"
    if ! user_count=$("${SSH_CMD[@]}" "mariadb --defaults-extra-file=$config_file -D $mysql_database -e \"$username_exists_query\" -sN"); then
        log "[✘] Error: Unable to check username existence in the database. Is mariadb running?"
        exit 1
    fi

    echo "$user_count"
}



copy_user_account() {
    local CONTEXT="$1"
    TMPDIR=$(mktemp -d)
    log "Creating system user ($CONTEXT) on remote server ..."

    # Prepare passwd, group and shadow for the system user (context)
    awk -F: -v user="$CONTEXT" '$1 == user {print}' /etc/passwd > "$TMPDIR/passwd.user"
    awk -F: -v user="$CONTEXT" 'BEGIN{gid=""}
        $1 == user {gid=$4}
        $3 == gid {print}
        $1 == user {print}' /etc/group > "$TMPDIR/group.user"
    grep "^$CONTEXT:" /etc/shadow > "$TMPDIR/shadow.user"
    grep "^$CONTEXT:" /etc/subuid > "$TMPDIR/subuid.user" 2>/dev/null
    grep "^$CONTEXT:" /etc/subgid > "$TMPDIR/subgid.user" 2>/dev/null

    # Send files to remote
    # per-account dir on the destination, bulk transfers run two of these at once
    "${RSYNC_CMD[@]}" "$TMPDIR/passwd.user" "$TMPDIR/group.user" "$TMPDIR/shadow.user" "$TMPDIR/subuid.user" "$TMPDIR/subgid.user" "${REMOTE_USER}@${REMOTE_HOST}:/root/transfer_${CONTEXT}/"
    rm -rf "$TMPDIR" >/dev/null

    # Remote command (heredoc WITHOUT quotes so we interpolate CONTEXT)
    "${SSH_CMD[@]}" <<EOF
export CONTEXT="$CONTEXT"

USER_PASSWD="/root/transfer_$CONTEXT/passwd.user"
USER_GROUP="/root/transfer_$CONTEXT/group.user"
USER_SHADOW="/root/transfer_$CONTEXT/shadow.user"
UID_MAP_FILE="/root/\${CONTEXT}_uid_map.txt"

user_exists() {
    id "\$1" &>/dev/null
}

get_used_uids() {
    getent passwd | cut -d: -f3
}

# Find a free UID >= 1002 and not in use
find_free_uid() {
    used_uids=\$(get_used_uids)
    uid=\$1
    while echo "\$used_uids" | grep -qw "\$uid"; do
        uid=\$((uid + 1))
    done
    echo "\$uid"
}

# add groups
cut -d: -f1,3 "\$USER_GROUP" | while IFS=: read -r group gid; do
    if ! getent group "\$group" > /dev/null; then
        groupadd -g "\$gid" "\$group"
    fi
done

# create the UID map file
> "\$UID_MAP_FILE"

# create the user and record the mapping if needed
while IFS=: read -r user uid gid comment home shell; do
    free_uid=\$(find_free_uid "\$uid")
    CHOWN_HOMEDIR=false

    if user_exists "\$user"; then
        echo "System user \$user already exists, skipping creation."
    else
        if [[ "\$free_uid" != "\$uid" ]]; then
            echo "UID for \$user changed from \$uid to \$free_uid"
            CHOWN_HOMEDIR=true
        fi

        useradd -u "\$free_uid" -g "\$gid" -c "\$comment" -d "\$home" -s "\$shell" "\$user"
        echo "\$user:\$uid:\$free_uid:\$gid:\$home" >> "\$UID_MAP_FILE"

        if [[ "\$CHOWN_HOMEDIR" == "true" && -d "\$home" ]]; then
            echo "Changing ownership of home directory \$home to \$user"
            chown -R "\$user:\$gid" "\$home"
        fi
    fi
done < <(cut -d: -f1,3,4,5,6,7 "\$USER_PASSWD")

# set the password
cut -d: -f1,2 "\$USER_SHADOW" | while IFS=: read -r user hash; do
    if [[ -n "\$hash" ]]; then
        usermod -p "\$hash" "\$user"
    fi
done

# keep the source subuid/subgid range so files in the rootless podman storage keep the right owners
for f in subuid subgid; do
    src_line=\$(head -n1 "/root/transfer_$CONTEXT/\$f.user" 2>/dev/null)
    [[ -z "\$src_line" ]] && continue
    IFS=: read -r _ start count <<< "\$src_line"
    end=\$((start + count - 1))
    overlap=\$(awk -F: -v u="\$CONTEXT" -v s="\$start" -v e="\$end" '\$1!=u && \$2<=e && (\$2+\$3-1)>=s {print \$1}' /etc/\$f)
    if [[ -n "\$overlap" ]]; then
        echo "[!] \$f range \$start:\$count for \$CONTEXT is taken by \$overlap on this server, keeping the one useradd assigned"
    else
        sed -i "/^\$CONTEXT:/d" /etc/\$f
        echo "\$CONTEXT:\$start:\$count" >> /etc/\$f
    fi
done

# remove temp files
rm -rf "/root/transfer_$CONTEXT"
EOF

    # Fetch the UID map file locally and remove it from the remote side
    "${SSH_CMD[@]}" "cat /root/${CONTEXT}_uid_map.txt" > "/tmp/${CONTEXT}_uid_map.txt"
    "${SSH_CMD[@]}" "rm -f /root/${CONTEXT}_uid_map.txt"
}



store_running_containers_for_user() {
output_file="/tmp/docker_containers_names_${USERNAME}.txt"
: > "$output_file"  # clear the file

compose_file="$(podman_compose_file "$CONTEXT")"
if [ -f "$compose_file" ]; then
    log "Checking podman context ...."
    containers=$(podman_user "$CONTEXT" ps --format "{{.Names}}" 2>/dev/null)
    if [ -n "$containers" ]; then
        containers_single_line=$(echo "$containers" | tr '\n' ' ' | sed 's/ $//')
        echo "$CONTEXT: $containers_single_line" >> "$output_file"
    else
        echo "$CONTEXT: no containers" >> "$output_file"
    fi
fi

"${RSYNC_CMD[@]}" $output_file "${REMOTE_USER}"@"${REMOTE_HOST}":$output_file
}

copy_feature_set() {
    local PLAN_FEATURE_SET
    PLAN_FEATURE_SET=$(mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s -e "SELECT feature_set FROM plans WHERE id = $PLAN_ID;")
    
    local FEATURE_FILE="${PLAN_FEATURE_SET}.txt"
    local FEATURES_DIR="/etc/openpanel/openpanel/features"

    log "Listing features on remote server ..."
    local REMOTE_FILES
    REMOTE_FILES=$("${SSH_CMD[@]}" "ls -1 \"$FEATURES_DIR\" 2>/dev/null" || true)

    if echo "$REMOTE_FILES" | grep -Fxq "$FEATURE_FILE"; then
        log "Feature set '$FEATURE_FILE' already exists on remote server"
    else
        log "Copying feature set '$FEATURE_FILE' to remote server"
        "${RSYNC_CMD[@]}" "$FEATURES_DIR/$FEATURE_FILE" "${REMOTE_USER}@${REMOTE_HOST}:${FEATURES_DIR}/"
    fi
}


restore_running_containers_for_user() {
output_file="/tmp/docker_containers_names_${USERNAME}.txt"

# Open the file on FD 3 to avoid stdin conflicts
exec 3<"$output_file"

while IFS=: read -r ctx containers <&3; do
    ctx=$(echo "$ctx" | xargs)

    if [[ -z "$ctx" ]] || [[ "$containers" =~ no\ containers ]]; then
        log "No containers running for user"
        continue
    fi

    log "Starting containers inside podman context on remote server ..."
    "${SSH_CMD[@]}" "remote_uid=\$(stat -c '%u' /home/$ctx); CONTAINER_HOST=unix:///hostfs/run/user/\${remote_uid}/podman/podman.sock podman-compose -f /home/$ctx/docker-compose.yml down >/dev/null 2>&1; CONTAINER_HOST=unix:///hostfs/run/user/\${remote_uid}/podman/podman.sock podman-compose -f /home/$ctx/docker-compose.yml up -d $containers >/dev/null 2>&1"
done

# Close FD 3
exec 3<&-
}

import_mysql() {
  "${SSH_CMD[@]}" bash -s <<EOF
set -e

export mysql_database="$mysql_database"
export USERNAME="$USERNAME"
CONFIG_FILE="/etc/my.cnf"

if [[ -z "\$mysql_database" ]]; then
  echo "[ERROR] mysql_database is not set"
  exit 1
fi
if [[ -z "\$USERNAME" ]]; then
  echo "[ERROR] USERNAME is not set"
  exit 1
fi

cd "/tmp/user_import_${USERNAME}/" || { echo "[ERROR] Directory /tmp/user_import_${USERNAME}/ not found"; exit 1; }

# Fix trailing commas in SQL
for f in plan_\${USERNAME}_autoinc.sql user_\${USERNAME}_autoinc.sql domains_\${USERNAME}_autoinc.sql sites_\${USERNAME}_autoinc.sql; do
  [[ -f "\$f" ]] && sed -i -E ':a;N;\$!ba;s/,\s*;\s*/;/g' "\$f"
done

PLAN_NAME=\$(awk -F"'" '/INSERT INTO plans/ {getline; print \$2; exit}' "plan_\${USERNAME}_autoinc.sql")

EXISTING_PLAN_ID=\$(mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$mysql_database" -N -s \
  -e "SELECT id FROM plans WHERE name = '\$PLAN_NAME' LIMIT 1;")

if [[ -n "\$EXISTING_PLAN_ID" ]]; then
  echo "Plan already exists (ID: \$EXISTING_PLAN_ID)"
  SRC_FEATURE_SET=\$(grep -oP "'[^']*'(?=, '[^']*', [0-9]+\)?[,;]?\s*$)" "plan_\${USERNAME}_autoinc.sql" | tail -1 | tr -d "'")
  DST_FEATURE_SET=\$(mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$mysql_database" -N -s -e "SELECT feature_set FROM plans WHERE id = \$EXISTING_PLAN_ID;")
  [[ -n "\$SRC_FEATURE_SET" && "\$SRC_FEATURE_SET" != "\$DST_FEATURE_SET" ]] && echo "[!] Warning: plan '\$PLAN_NAME' on this server uses feature set '\$DST_FEATURE_SET', on the source it was '\$SRC_FEATURE_SET'"
else
  echo "Importing new plan..."
  (echo "USE \\\`\$mysql_database\\\`;" && cat "plan_\${USERNAME}_autoinc.sql") | mariadb --defaults-extra-file="\$CONFIG_FILE"
  EXISTING_PLAN_ID=\$(mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$mysql_database" -N -s \
    -e "SELECT id FROM plans WHERE name = '\$PLAN_NAME' LIMIT 1;")
fi

# --force: drop the existing account rows first, otherwise the insert hits the unique username
if [[ "$FORCE" -eq 1 ]]; then
  OLD_ID=\$(mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$mysql_database" -N -s -e "SELECT id FROM users WHERE username = '\$USERNAME';")
  if [[ -n "\$OLD_ID" ]]; then
    echo "Removing existing rows for \$USERNAME (--force)"
    for stmt in "DELETE FROM sites WHERE domain_id IN (SELECT domain_id FROM domains WHERE user_id=\$OLD_ID)" "DELETE FROM domains WHERE user_id=\$OLD_ID" "DELETE FROM mcp_tokens WHERE user_id=\$OLD_ID" "DELETE FROM user_passkeys WHERE user_id=\$OLD_ID" "DELETE FROM users WHERE id=\$OLD_ID"; do
      mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$mysql_database" -e "\$stmt" 2>/dev/null
    done
  fi
fi

sed -E "s/,[[:space:]]*[0-9]+\);$/,\$EXISTING_PLAN_ID);/" "user_\${USERNAME}_autoinc.sql" > tmp_user.sql
sed -i "s/'NULL'/NULL/g" tmp_user.sql

echo "Importing user into database..."
(echo "USE \\\`\$mysql_database\\\`;" && cat tmp_user.sql) | mariadb --defaults-extra-file="\$CONFIG_FILE"
rm -f tmp_user.sql

USER_ID=\$(mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$mysql_database" -N -s \
  -e "SELECT id FROM users WHERE username = '\$USERNAME';")

if [[ -z "\$USER_ID" ]]; then
  echo "[ERROR] Failed to import user!"
  exit 1
fi

for t in mcp_tokens user_passkeys; do
  [[ -s "\${t}.ddl" ]] || continue
  mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$mysql_database" < "\${t}.ddl"
  [[ -s "\${t}.rows.sql" ]] || continue
  mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$mysql_database" -e "DROP TABLE IF EXISTS _import_\${t}; CREATE TABLE _import_\${t} LIKE \${t};"
  sed "s/INSERT INTO \\\`\${t}\\\`/INSERT INTO \\\`_import_\${t}\\\`/" "\${t}.rows.sql" | mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$mysql_database"
  COLS=\$(mariadb --defaults-extra-file="\$CONFIG_FILE" -N -s -e "SELECT GROUP_CONCAT(column_name ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='\$mysql_database' AND table_name='\$t' AND column_name NOT IN ('id','user_id');")
  mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$mysql_database" -e "INSERT IGNORE INTO \${t} (user_id,\$COLS) SELECT \$USER_ID,\$COLS FROM _import_\${t}; DROP TABLE _import_\${t};" && echo "Imported \$(grep -c '^INSERT' "\${t}.rows.sql") row(s) into \$t"
done


EOF
}


export_mysql() {

TMP_DIR="/tmp/export_${USERNAME}_$RANDOM"
mkdir -p "$TMP_DIR"

# Get user ID
USER_ID=$(mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s \
  -e "SELECT id FROM users WHERE username = '$USERNAME';")

if [[ -z "$USER_ID" ]]; then
  log "[ERROR] No user found with username '$USERNAME'"
  exit 1
fi

# Get plan ID
PLAN_ID=$(mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s \
  -e "SELECT plan_id FROM users WHERE id = $USER_ID;")

### EXPORT PLAN (no ID)
mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s -e "
  SELECT name, description, domains_limit, websites_limit, email_limit, ftp_limit,
         disk_limit, inodes_limit, db_limit, cpu, ram, bandwidth, feature_set,
         max_email_quota, max_hourly_email
  FROM plans WHERE id = $PLAN_ID;" > "$TMP_DIR/plan.tsv"



awk -v table="plans" '
BEGIN {
  FS="\t";
  print "INSERT INTO plans (name, description, domains_limit, websites_limit, email_limit, ftp_limit, disk_limit, inodes_limit, db_limit, cpu, ram, bandwidth, feature_set, max_email_quota, max_hourly_email) VALUES"
}
{
  printf "('\''%s'\'', '\''%s'\'', %s, %s, %s, %s, '\''%s'\'', %s, %s, '\''%s'\'', '\''%s'\'', %s, '\''%s'\'', '\''%s'\'', %s),\n",
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
}
END { print ";" }
' "$TMP_DIR/plan.tsv" > "$TMP_DIR/plan_${USERNAME}_autoinc.sql"


### EXPORT USER (no ID)
mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s -e "
  SELECT username, password, email, owner, user_domains, twofa_enabled, otp_secret,
         plan, registered_date, server, plan_id
  FROM users WHERE id = $USER_ID;" > "$TMP_DIR/user.tsv"

awk '
BEGIN {
  FS="\t";
  print "INSERT INTO users (username, password, email, owner, user_domains, twofa_enabled, otp_secret, plan, registered_date, server, plan_id) VALUES"
}
{
  printf "('\''%s'\'', '\''%s'\'', '\''%s'\'', '\''%s'\'', '\''%s'\'', %s, '\''%s'\'', '\''%s'\'', '\''%s'\'', '\''%s'\'', %s),\n",
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
}
END { print ";" }
' "$TMP_DIR/user.tsv" > "$TMP_DIR/user_${USERNAME}_autoinc.sql"

### EXPORT SITES as tsv, domain_url included so the destination can resolve its own domain_id
: > "$TMP_DIR/sites_${USERNAME}.tsv"
mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s -e "
  SELECT s.site_name, d.domain_url, s.admin_email, s.version, s.type, s.ports, s.path, s.container
  FROM sites s JOIN domains d ON s.domain_id = d.domain_id
  WHERE d.user_id = $USER_ID;" > "$TMP_DIR/sites_${USERNAME}.tsv"

# per-user tables the panel only creates on first use, ship the ddl too so a fresh destination has them
for t in mcp_tokens user_passkeys; do
  if mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s -e "SHOW TABLES LIKE '$t'" | grep -qx "$t"; then
    mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s -e "SHOW CREATE TABLE $t" | cut -f2- | sed 's/^CREATE TABLE/CREATE TABLE IF NOT EXISTS/; s/\\n/\n/g' > "$TMP_DIR/${t}.ddl"
    echo ";" >> "$TMP_DIR/${t}.ddl"
    # plain sql instead of mysqldump, the host client config has options mysqldump rejects
    cols=$(mariadb --defaults-extra-file="$config_file" -N -s -e "SELECT GROUP_CONCAT(CONCAT('\`',column_name,'\`') ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='$mysql_database' AND table_name='$t' AND column_name<>'id';")
    qcols=$(mariadb --defaults-extra-file="$config_file" -N -s -e "SELECT GROUP_CONCAT(CONCAT('QUOTE(\`',column_name,'\`)') ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='$mysql_database' AND table_name='$t' AND column_name<>'id';")
    mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s -r -e "SELECT CONCAT('INSERT INTO \`$t\` ($cols) VALUES (', CONCAT_WS(',', $qcols), ');') FROM $t WHERE user_id=$USER_ID;" > "$TMP_DIR/${t}.rows.sql"
  fi
done

"${SSH_CMD[@]}" "mkdir -p /tmp/user_import_${USERNAME}/"
"${RSYNC_CMD[@]}" "$TMP_DIR"/*.ddl "$TMP_DIR"/*.rows.sql "${REMOTE_USER}"@"${REMOTE_HOST}":/tmp/user_import_${USERNAME}/ 2>/dev/null
"${RSYNC_CMD[@]}" "$TMP_DIR"/plan_"${USERNAME}"_autoinc.sql "${REMOTE_USER}"@"${REMOTE_HOST}":/tmp/user_import_${USERNAME}/
"${RSYNC_CMD[@]}" "$TMP_DIR"/user_"${USERNAME}"_autoinc.sql "${REMOTE_USER}"@"${REMOTE_HOST}":/tmp/user_import_${USERNAME}/
[[ -s "$TMP_DIR/sites_${USERNAME}.tsv" ]] && "${RSYNC_CMD[@]}" "$TMP_DIR/sites_${USERNAME}.tsv" "${REMOTE_USER}@${REMOTE_HOST}:/tmp/user_import_${USERNAME}/"
rm -rf "$TMP_DIR"
}

# runs after domains are added on the destination, since sites point at the new domain_id
import_sites() {
  "${SSH_CMD[@]}" bash -s <<EOF
CONFIG_FILE="/etc/my.cnf"
DB="$mysql_database"
USERNAME="$USERNAME"
SITES="/tmp/user_import_\${USERNAME}/sites_\${USERNAME}.tsv"
q() { mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$DB" -N -s -e "\$1"; }
esc() { printf '%s' "\$1" | sed "s/'/''/g"; }
val() { [[ "\$1" == "NULL" || -z "\$1" ]] && echo NULL || echo "'\$(esc "\$1")'"; }

if [[ ! -s "\$SITES" ]]; then
  echo "No sites found to import."
else
  USER_ID=\$(q "SELECT id FROM users WHERE username = '\$USERNAME';")
  while IFS=\$'\t' read -r site_name domain_url admin_email version type ports site_path container; do
    [[ -z "\$site_name" ]] && continue
    DOMAIN_ID=\$(q "SELECT domain_id FROM domains WHERE domain_url = '\$(esc "\$domain_url")' AND user_id = \$USER_ID LIMIT 1;")
    if [[ -z "\$DOMAIN_ID" ]]; then
      echo "[ERROR] Domain not found for site: \$domain_url"
      continue
    fi
    [[ "\$ports" =~ ^[0-9]+\$ ]] || ports=NULL
    q "INSERT INTO sites (site_name, domain_id, admin_email, version, type, ports, path, container) VALUES (\$(val "\$site_name"), \$DOMAIN_ID, \$(val "\$admin_email"), \$(val "\$version"), \$(val "\$type"), \$ports, \$(val "\$site_path"), \$(val "\$container"));" && echo "Site imported: \$site_name"
  done < "\$SITES"
fi
rm -rf "/tmp/user_import_\${USERNAME}/"
EOF
}

sync_local_dns_zone() {
    local domain="$1"
    local zone_file="/etc/bind/zones/$domain.zone"

    if [[ -f "$zone_file" ]]; then
        echo "[LIVE] Updating DNS zone for $domain locally"
        sed -i "s/$current_ip/$REMOTE_HOST/g" "$zone_file"
    else
        echo "[WARNING] Local DNS zone file not found for $domain"
    fi
}

NAMESERVERS=()

get_remote_nameservers() {

if [[ "$LIVE_TRANSFER" == true ]]; then
    REMOTE_CONFIG="/etc/openpanel/openpanel/conf/openpanel.config"
    
    echo "Checking NS configuration on remote server..."

    "${SSH_CMD[@]}" "[ -f '$REMOTE_CONFIG' ]" || {
        echo "[ERROR] Configuration file not found on remote: $REMOTE_CONFIG"
        exit 1
    }

    NS1=$("${SSH_CMD[@]}" "grep '^ns1=' '$REMOTE_CONFIG' | cut -d'=' -f2")
    NS2=$("${SSH_CMD[@]}" "grep '^ns2=' '$REMOTE_CONFIG' | cut -d'=' -f2")
    NS3=$("${SSH_CMD[@]}" "grep '^ns3=' '$REMOTE_CONFIG' | cut -d'=' -f2")
    NS4=$("${SSH_CMD[@]}" "grep '^ns4=' '$REMOTE_CONFIG' | cut -d'=' -f2")

    if [[ -z "$NS1" || -z "$NS2" ]]; then
        echo "[ERROR] ns1 and ns2 are not set on remote serverm - Live transfer will not forward DNS!"
    else
	    NAMESERVERS=("$NS1" "$NS2")
	    [[ -n "$NS3" ]] && NAMESERVERS+=("$NS3")
	    [[ -n "$NS4" ]] && NAMESERVERS+=("$NS4")
	
	    echo "[INFO] Remote NS: ${NAMESERVERS[*]}"
    fi
fi

}


update_zone_file() {
    local zone_file="$1"
    local tmp_file
    tmp_file=$(mktemp)

    if [[ ! -f "$zone_file" ]]; then
        echo "[WARNING] Zone file not found: $zone_file"
        return
    fi

    echo "[INFO] Updating NS records in: $zone_file"

    grep -v '^[^;].*IN[[:space:]]*NS[[:space:]]*' "$zone_file" > "$tmp_file"

    for ns in "${NAMESERVERS[@]}"; do
        echo "@ IN NS $ns." >> "$tmp_file"
    done

    cat "$tmp_file" > "$zone_file"
    rm "$tmp_file"
}


rsync_files_for_user() {
    log "Syncing files for user $USERNAME (context: $CONTEXT) ..."
    RSYNC_OUTPUT=$("${RSYNC_CMD[@]}" --include="/$CONTEXT/docker-data/volumes/" --exclude="/$CONTEXT/docker-data/*" --exclude="/$CONTEXT/sockets/*/*.sock" --exclude="/$CONTEXT/sockets/*/*.pid" /home/"$CONTEXT" "${REMOTE_USER}@${REMOTE_HOST}:/home/" 2>&1)
    RSYNC_EXIT=$?
    log "$RSYNC_OUTPUT"
    if [[ $RSYNC_EXIT -eq 0 ]]; then
        MAPPING_FILE="/tmp/${CONTEXT}_uid_map.txt"

        if [[ -f "$MAPPING_FILE" ]]; then
            MAPPING_LINE=$(grep "^$CONTEXT:" "$MAPPING_FILE")
            if [[ -n "$MAPPING_LINE" ]]; then
                IFS=':' read -r _ old_uid new_uid gid _ <<< "$MAPPING_LINE"
                if [[ "$old_uid" != "$new_uid" ]]; then
                    log "UID changed for $CONTEXT (from $old_uid to $new_uid), performing chown on remote host ..."
                    "${SSH_CMD[@]}" "chown -R $new_uid:$gid /home/$CONTEXT"
		fi
            fi
            rm -f "$MAPPING_FILE"
        else
            log "[WARNING] UID mapping file not found for $CONTEXT"
        fi
    else
        log "[ERROR] Rsync failed! Output:"
        log "$RSYNC_OUTPUT"
        exit 1
    fi

    CADDY_STATS="/var/log/caddy/stats/$USERNAME"
    if [ -d "$CADDY_STATS" ]; then
		"${RSYNC_CMD[@]}" "$CADDY_STATS" "${REMOTE_USER}"@"${REMOTE_HOST}":/var/log/caddy/stats/
    fi

	BLOCKED_IPS="/etc/openpanel/caddy/deny/${CONTEXT}.ips"
	if [[ -f "$BLOCKED_IPS" ]]; then
	    "${SSH_CMD[@]}" "mkdir -p /etc/openpanel/caddy/deny/"
	    "${RSYNC_CMD[@]}" "$BLOCKED_IPS" "${REMOTE_USER}@${REMOTE_HOST}:/etc/openpanel/caddy/deny/"
	fi

    ALL_DOMAINS=$(opencli domains-user "$USERNAME" --docroot --php_version)
        
if [[ "$ALL_DOMAINS" == *"No domains found for user '$USERNAME'"* ]]; then
        log "No domains found for user $USERNAME. Skipping."
else	
    while IFS=$'\t ' read -r -u 3 domain docroot php_version; do
    whoowns_output=$("${SSH_CMD[@]}" "opencli domains-whoowns $domain" < /dev/null)
    owner=$(echo "$whoowns_output" | awk -F "Owner of '$domain': " '{print $2}')
    
    if [ -z "$owner" ]; then
	    # add domain on remote
	    if ! "${SSH_CMD[@]}" "opencli domains-add $domain $USERNAME --docroot $docroot --php_version $php_version --skip_caddy --skip_vhost --skip_containers --skip_dns" < /dev/null >> "$log_file" 2>&1; then
	       log "[✘] ERROR: Failed to import domain $domain"
		   exit 1
	    fi
    
	    DOMAIN_CADDY_CONF="/etc/openpanel/caddy/domains/$domain.conf"
	    if [ -f "$DOMAIN_CADDY_CONF" ]; then
			"${RSYNC_CMD[@]}" "$DOMAIN_CADDY_CONF" "${REMOTE_USER}"@"${REMOTE_HOST}":/etc/openpanel/caddy/domains/
			if [[ "$LIVE_TRANSFER" == true ]]; then
			   # https://github.com/stefanpejcic/OpenPanel/issues/897
		       sed -E -i 's|reverse_proxy (https?://)[^ ]+|reverse_proxy \1'"$REMOTE_HOST"'|g' "$DOMAIN_CADDY_CONF"
			fi
		fi
	
	    DOMAIN_CADDY_LOG="/var/log/caddy/domlogs/$domain"
	    if [ -f "$DOMAIN_CADDY_LOG" ]; then
			"${RSYNC_CMD[@]}" "$DOMAIN_CADDY_LOG" "${REMOTE_USER}"@"${REMOTE_HOST}":/var/log/caddy/domlogs/
		fi
	
	    DOMAIN_CADDY_WAF="/var/log/caddy/coraza_waf/$domain.log"
	    if [ -f "$DOMAIN_CADDY_WAF" ]; then
			"${RSYNC_CMD[@]}" "$DOMAIN_CADDY_WAF" "${REMOTE_USER}"@"${REMOTE_HOST}":/var/log/caddy/coraza_waf/
		fi

		DOMAIN_CADDY_SUSPENDED="/etc/openpanel/caddy/suspended_domains/$domain.conf"
		if [[ -f "$DOMAIN_CADDY_SUSPENDED" ]]; then
		    "${SSH_CMD[@]}" "mkdir -p /etc/openpanel/caddy/suspended_domains/"
		    "${RSYNC_CMD[@]}" "$DOMAIN_CADDY_SUSPENDED" "${REMOTE_USER}@${REMOTE_HOST}:/etc/openpanel/caddy/suspended_domains/"
		fi

		DOMAIN_ZONE_FILE="/etc/bind/zones/$domain.zone"
		if [ -f "$DOMAIN_ZONE_FILE" ]; then
		    "${RSYNC_CMD[@]}" "$DOMAIN_ZONE_FILE" "${REMOTE_USER}"@"${REMOTE_HOST}":/etc/bind/zones/
      
		    "${SSH_CMD[@]}" "sed -i 's/$current_ip/$REMOTE_HOST/g' /etc/bind/zones/$domain.zone"
      
		    "${SSH_CMD[@]}" <<EOF > /dev/null 2>&1
grep -qF "zone \"$domain\"" /etc/bind/named.conf.local || \
echo 'zone "$domain" IN { type master; file "/etc/bind/zones/$domain.zone"; };' >> /etc/bind/named.conf.local
EOF

            if [[ "$LIVE_TRANSFER" == true ]]; then
                sync_local_dns_zone "$domain"
                update_zone_file "/etc/bind/zones/$domain.zone"
            fi
		fi

		DOMAIN_CADDY_SSL="/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/$domain"
		if [ -d "$DOMAIN_CADDY_SSL" ]; then
		"${RSYNC_CMD[@]}" "$DOMAIN_CADDY_SSL" "${REMOTE_USER}"@"${REMOTE_HOST}":/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/
		fi

		DOMAIN_CADDY_CUSTOM_SSL="/etc/openpanel/caddy/ssl/custom/$domain"
		if [ -d "$DOMAIN_CADDY_CUSTOM_SSL" ]; then
			"${RSYNC_CMD[@]}" "$DOMAIN_CADDY_CUSTOM_SSL" "${REMOTE_USER}"@"${REMOTE_HOST}":/etc/openpanel/caddy/ssl/custom/
		fi
 	fi

	DKIM_DIR="/usr/local/mail/openmail/docker-data/dms/config/opendkim/keys/$domain"
	if [[ -d "$DKIM_DIR" ]]; then
	    "${SSH_CMD[@]}" "mkdir -p /usr/local/mail/openmail/docker-data/dms/config/opendkim/keys/"
	    "${RSYNC_CMD[@]}" "$DKIM_DIR" "${REMOTE_USER}@${REMOTE_HOST}:/usr/local/mail/openmail/docker-data/dms/config/opendkim/keys/"
	    # keys alone aren't used until the domain is listed in the opendkim tables
	    for table in KeyTable SigningTable TrustedHosts; do
	        local_table="/usr/local/mail/openmail/docker-data/dms/config/opendkim/$table"
	        [[ -f "$local_table" ]] || continue
	        awk -v d="$domain" '{k=$1} k=="*@"d || k==d || k=="*."d || (length(k)>length(d) && substr(k, length(k)-length(d)-11)=="._domainkey."d)' "$local_table" | while IFS= read -r line; do
	            "${SSH_CMD[@]}" "touch /usr/local/mail/openmail/docker-data/dms/config/opendkim/$table; grep -qxF '$line' /usr/local/mail/openmail/docker-data/dms/config/opendkim/$table || echo '$line' >> /usr/local/mail/openmail/docker-data/dms/config/opendkim/$table" < /dev/null
	        done
	    done
	fi

 done 3<<< "$ALL_DOMAINS"

 "${SSH_CMD[@]}" "cd /root && podman-compose up -d bind9 >/dev/null 2>&1; podman exec openpanel_dns rndc reconfig >/dev/null 2>&1"

 if [[ "$LIVE_TRANSFER" == true ]]; then
   podman exec caddy caddy reload >/dev/null 2>&1
 fi

fi
}



setup_remote_podman() {
    # context resolution is dynamic per user uid under podman, nothing to register and no per-user AppArmor (that was rootlesskit, podman doesn't use it) -- ~/.config/containers/*.conf already rides along via rsync_files_for_user and stays valid as-is on the destination
    SRC="/home/$CONTEXT/.config/containers"
    if [[ -d "$SRC" ]]; then
        REMOTE_UID=$("${SSH_CMD[@]}" "stat -c '%u' /home/$CONTEXT" 2>/dev/null)

        if [[ -z "$REMOTE_UID" ]]; then
            log "FATAL ERROR: Failed to get UID for user $CONTEXT on remote server"
            exit 1
        fi

        log "Enabling rootless podman for $CONTEXT on destination ..."

        "${SSH_CMD[@]}" "loginctl enable-linger $CONTEXT" \
            >/dev/null 2>&1 || log "Failed to enable linger for $CONTEXT"

        # wait for user@uid before systemctl --user, enable-linger returns before it's up
        "${SSH_CMD[@]}" "for i in \$(seq 30); do systemctl is-active user@${REMOTE_UID}.service >/dev/null 2>&1 && break; sleep 1; done; systemctl --user -M ${CONTEXT}@ daemon-reload; systemctl --user -M ${CONTEXT}@ reset-failed podman.socket; systemctl --user -M ${CONTEXT}@ enable --now podman.socket" \
            >/dev/null 2>&1
        if ! "${SSH_CMD[@]}" "systemctl --user -M ${CONTEXT}@ is-active podman.socket" >/dev/null 2>&1; then
            log "[!] Warning: podman.socket is not active for $CONTEXT on destination, containers may not start"
        fi
    else
        log "No .config/containers directory for $CONTEXT on source!"
        exit 1
    fi
}

restart_services_on_target() {
        log "Reloading services on ${REMOTE_HOST} server ..."
	"${SSH_CMD[@]}" "cd /root && podman-compose up -d openpanel bind9 caddy >/dev/null 2>&1; podman exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1; systemctl restart admin >/dev/null 2>&1"

	if [[ $COMPOSE_START_MAIL -eq 1 ]]; then
            log "Reloading mailserver and webmail on ${REMOTE_HOST} server ..."
            "${SSH_CMD[@]}" "cd /usr/local/mail/openmail && podman-compose up -d mailserver roundcube >/dev/null 2>&1"
	fi

	# todo: clamav
}

refresh_quotas() {
            log "Recalculating disk and inodes usage for all users on ${REMOTE_HOST} ..."
            "${SSH_CMD[@]}" "opencli user-quota >/dev/null 2>&1"
}



restore_ftp_for_user() {
    # FTP accounts live under the context (users.server), already resolved as $CONTEXT.
    local LOCAL_FTP_DIR="/etc/openpanel/ftp/users/${CONTEXT}"
    local USERS_LIST="${LOCAL_FTP_DIR}/users.list"

    if [[ ! -f "$USERS_LIST" ]]; then
        log "No FTP accounts found for context $CONTEXT, skipping FTP restore."
        return 0
    fi

    log "Restoring FTP accounts for $USERNAME (context: $CONTEXT) ..."

    # 1. Sync the users.list (and any per-context config) to the remote
    "${SSH_CMD[@]}" "mkdir -p '$LOCAL_FTP_DIR'"
    "${RSYNC_CMD[@]}" "$LOCAL_FTP_DIR/" "${REMOTE_USER}@${REMOTE_HOST}:${LOCAL_FTP_DIR}/"

    # 2. Replay each entry into the remote openadmin_ftp container.
    #    Passwords are already SHA-512 hashed in users.list, so no re-hashing.
    #    GID is re-derived from /home/$CONTEXT on the remote in case the UID was
    #    remapped during copy_user_account / rsync_files_for_user.
    local FTP_OUT; FTP_OUT=$(mktemp)
    if "${SSH_CMD[@]}" bash -s > "$FTP_OUT" 2>&1 <<EOF
set -e
context="$CONTEXT"

# starts the FTP server if not actually running, checked by real state (not just podman ps, which misses a container wedged mid-transition) so a stuck one gets force-removed and recreated instead of no-op'd over by compose-up
FTP_STATE=\$(podman inspect openadmin_ftp --format '{{.State.Status}}' 2>/dev/null)
if [ "\$FTP_STATE" != "running" ]; then
    if [ -n "\$FTP_STATE" ] && [ "\$FTP_STATE" != "exited" ] && [ "\$FTP_STATE" != "stopped" ]; then
        podman kill openadmin_ftp >/dev/null 2>&1
        podman rm -f openadmin_ftp >/dev/null 2>&1
        podman rm -f --storage openadmin_ftp >/dev/null 2>&1
    fi
    cd /root && timeout 30 podman-compose up -d openadmin_ftp >/dev/null 2>&1
    sleep 2
fi

# GID from the home directory owner on the remote (post-UID-remap)
GID=\$(stat -c '%u' "/home/\$context")
if [[ ! "\$GID" =~ ^[0-9]+\$ ]]; then
    echo "[FTP] ERROR: could not determine GID for \$context on remote"
    exit 1
fi

# Ensure the shared group exists inside the container
EXISTING_GROUP=\$(podman exec openadmin_ftp sh -c "getent group '\$GID' | cut -d: -f1")
if [[ -z "\$EXISTING_GROUP" ]]; then
    podman exec openadmin_ftp addgroup -g "\$GID" "\$context" 2>/dev/null || true
fi

USERS_LIST="/etc/openpanel/ftp/users/\${context}/users.list"
[[ -f "\$USERS_LIST" ]] || { echo "[FTP] no users.list on remote"; exit 0; }

while IFS='|' read -r username hashed_pass directory uid gid; do
    [[ -z "\$username" ]] && continue

    real_path="/home/\${context}/docker-data/volumes/\${context}_html_data/_data/"
    relative_path="\${directory##/var/www/html/}"
    new_directory="\${real_path}\${relative_path}"

    # Skip if the user already exists in the container
    if podman exec openadmin_ftp id "\$username" >/dev/null 2>&1; then
        echo "[FTP] \$username already exists in container, skipping."
        continue
    fi

    # Recreate the host directory and permissions
    mkdir -p "\$new_directory"
    chmod +rx "/home/\$context" \\
              "/home/\$context/docker-data" \\
              "/home/\$context/docker-data/volumes" \\
              "/home/\$context/docker-data/volumes/\${context}_html_data" \\
              "/home/\$context/docker-data/volumes/\${context}_html_data/_data" 2>/dev/null || true
    chown -R "\$GID:\$GID" "\$new_directory"
    chmod -R 2775 "\$new_directory"

    # Recreate the container user with the SAME hashed password (no re-hashing)
    podman exec openadmin_ftp useradd -d "\$new_directory" -s /sbin/nologin \\
        -g "\$context" -M "\$username" --badname 2>/dev/null || true
    podman exec openadmin_ftp sh -c "usermod -p '\$hashed_pass' '\$username'"

    echo "[FTP] restored \$username -> \$directory"
done < "\$USERS_LIST"
exit 0
EOF
    then
        logpipe < "$FTP_OUT"
        log "FTP accounts restored for context $CONTEXT."
    else
        local ftp_rc=$?
        logpipe < "$FTP_OUT"
        log "[!] Warning: FTP restore reported an error for context $CONTEXT (exit $ftp_rc)"
    fi
    rm -f "$FTP_OUT"
}


# MAIN
DB_CONFIG_FILE="/usr/local/opencli/db.sh"
# shellcheck disable=SC1090
. "$DB_CONFIG_FILE"

ssh-keygen -f '/root/.ssh/known_hosts' -R "$REMOTE_HOST" > /dev/null
format_commands # creates rsync and sshpass commands, installs sshpass if missing

log_paths_are                                                              # where will we store the progress

get_server_ipv4 
get_users_count_on_destination

username_exists_count=$(check_username_exists)
if [ "$username_exists_count" -gt 0 ]; then\
    if [[ $FORCE -eq 0 ]]; then
      log "[✘] Error: Username '$USERNAME' is already taken on destination server."
      exit 1
    else
      log "[!] Warning: Username '$USERNAME' is already taken on destination server but will be overwritten due to the --force flag."
    fi
fi


resolve_context   # sets $CONTEXT from users.server

export_mysql
import_mysql 2>&1 | logpipe
if [[ "$(check_username_exists)" -lt 1 ]]; then
    log "[✘] ERROR: User $USERNAME was not created in the destination panel database, aborting."
    exit 1
fi
copy_feature_set
copy_user_account "$CONTEXT"
get_remote_nameservers
rsync_files_for_user
log "Importing sites ..."
import_sites 2>&1 | logpipe
setup_remote_podman # enable rootless podman.socket on dest
restore_ftp_for_user # recreate ftp sub-accounts in remote container
"${SSH_CMD[@]}" "systemctl daemon-reload" 
"${SSH_CMD[@]}" "opencli user-quota --update $USERNAME >/dev/null 2>&1" # set quotas
"${SSH_CMD[@]}" "mkdir -p /var/log/caddy/stats/ /var/log/caddy/domlogs/ /var/log/caddy/coraza_waf/ /etc/openpanel/caddy/domains/ /etc/bind/zones/ /etc/openpanel/caddy/ssl/certs/ /etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/ /etc/openpanel/caddy/ssl/custom/ /etc/openpanel/openpanel/core/users/"

# https://github.com/stefanpejcic/opencli/issues/159
if [ -n "$key_value" ]; then
    log "Syncing mail data ..."

    DMS_CONFIG="/usr/local/mail/openmail/docker-data/dms/config"
    POSTFWD_SRC="/usr/local/mail/openmail/postfwd/postfwd.cf"

    # Resolve domain list for grep patterns
    ALL_DOMAINS_FOR_MAIL=$(opencli domains-user "$USERNAME" --docroot --php_version 2>/dev/null)
    DOMAIN_LIST_STR=""
    if [[ "$ALL_DOMAINS_FOR_MAIL" != *"No domains found"* && -n "$ALL_DOMAINS_FOR_MAIL" ]]; then
        while IFS=$'\t ' read -r domain _; do
            [[ -n "$domain" ]] && DOMAIN_LIST_STR+="$domain "
        done <<< "$ALL_DOMAINS_FOR_MAIL"
        DOMAIN_LIST_STR="${DOMAIN_LIST_STR% }"
    fi

    if [[ -n "$DOMAIN_LIST_STR" ]]; then
        # one alternative per domain, anchored so example.com doesn't also grab blog.example.com
        read -ra MAIL_DOMAINS <<< "$DOMAIN_LIST_STR"
        DOMAIN_PATTERN="@($(printf '%s|' "${MAIL_DOMAINS[@]//./\\.}" | sed 's/|$//'))([|: ]|$)"
        REGEX_PATTERN="@($(printf '%s|' "${MAIL_DOMAINS[@]//./\\.}" | sed 's/|$//'))/"

        TMP_MAIL_DIR=$(mktemp -d)

        for cf in postfix-accounts.cf postfix-virtual.cf dovecot-quotas.cf postfix-receive-access.cf postfix-send-access.cf; do
            : > "$TMP_MAIL_DIR/$cf"
            [[ -f "$DMS_CONFIG/$cf" ]] && grep -E "$DOMAIN_PATTERN" "$DMS_CONFIG/$cf" > "$TMP_MAIL_DIR/$cf" || true
        done

        : > "$TMP_MAIL_DIR/postfix-regex.cf"
        [[ -f "$DMS_CONFIG/postfix-regex.cf" ]] && grep -E "$REGEX_PATTERN" "$DMS_CONFIG/postfix-regex.cf" > "$TMP_MAIL_DIR/postfix-regex.cf" || true

        : > "$TMP_MAIL_DIR/postfwd.cf"
        if [[ -f "$POSTFWD_SRC" ]]; then
            while IFS= read -r line; do
                matched=0
                if [[ "$line" == id=* ]]; then
                    for domain in $DOMAIN_LIST_STR; do
                        if [[ "$line" == *"@${domain}"* ]]; then
                            matched=1; break
                        fi
                    done
                fi
                if [[ $matched -eq 1 ]]; then
                    echo "$line"
                    IFS= read -r next_line && echo "$next_line"
                fi
            done < "$POSTFWD_SRC" > "$TMP_MAIL_DIR/postfwd.cf"
        fi

        # merge into the destination files instead of overwriting them, other accounts already live there
        "${SSH_CMD[@]}" "mkdir -p $DMS_CONFIG /usr/local/mail/openmail/postfwd/ /tmp/mail_import_${USERNAME}/"
        "${RSYNC_CMD[@]}" "$TMP_MAIL_DIR/" "${REMOTE_USER}@${REMOTE_HOST}:/tmp/mail_import_${USERNAME}/"
        "${SSH_CMD[@]}" bash -s <<EOF
cd /tmp/mail_import_${USERNAME} || exit 0
for cf in postfix-accounts.cf postfix-virtual.cf dovecot-quotas.cf postfix-receive-access.cf postfix-send-access.cf postfix-regex.cf; do
    [[ -s "\$cf" ]] || continue
    dst="$DMS_CONFIG/\$cf"
    touch "\$dst"
    added=0
    while IFS= read -r line; do
        [[ -z "\$line" ]] && continue
        key="\${line%%[|: ]*}"
        if ! awk -v k="\$key" 'index(\$0,k)==1 && substr(\$0,length(k)+1,1) ~ /[|: ]/ {f=1} END{exit !f}' "\$dst"; then echo "\$line" >> "\$dst"; added=\$((added+1)); fi
    done < "\$cf"
    echo "Merged \$cf: \$added line(s) added"
done
if [[ -s postfwd.cf ]]; then
    touch /usr/local/mail/openmail/postfwd/postfwd.cf
    while IFS= read -r id_line; do
        IFS= read -r action_line
        grep -qxF "\$id_line" /usr/local/mail/openmail/postfwd/postfwd.cf || printf '%s\n%s\n' "\$id_line" "\$action_line" >> /usr/local/mail/openmail/postfwd/postfwd.cf
    done < postfwd.cf
    echo "Merged postfwd.cf"
fi
rm -rf /tmp/mail_import_${USERNAME}
EOF
        rm -rf "$TMP_MAIL_DIR"
        COMPOSE_START_MAIL=1
    fi

    # Physical maildir sync
    STORE_EMAILS_IN=$(grep -E '^email_storage_location=' /etc/openpanel/openadmin/config/admin.ini 2>/dev/null | cut -d'=' -f2- | xargs)
    REMOTE_STORE_EMAILS_IN=$("${SSH_CMD[@]}" "grep -E '^email_storage_location=' /etc/openpanel/openadmin/config/admin.ini 2>/dev/null | cut -d'=' -f2- | xargs" 2>/dev/null)

    if [[ "$STORE_EMAILS_IN" == /* && -d "$STORE_EMAILS_IN" ]]; then
        # shared store keeps one dir per domain, only copy this user's domains
        [[ "$REMOTE_STORE_EMAILS_IN" == /* ]] || REMOTE_STORE_EMAILS_IN="$STORE_EMAILS_IN"
        for domain in $DOMAIN_LIST_STR; do
            if [[ -d "${STORE_EMAILS_IN%/}/$domain" ]]; then
                log "Syncing maildir ${STORE_EMAILS_IN%/}/$domain → ${REMOTE_STORE_EMAILS_IN%/}/$domain ..."
                "${SSH_CMD[@]}" "mkdir -p ${REMOTE_STORE_EMAILS_IN%/}"
                "${RSYNC_CMD[@]}" "${STORE_EMAILS_IN%/}/$domain" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_STORE_EMAILS_IN%/}/"
                COMPOSE_START_MAIL=1
            fi
        done
    elif [[ -d "/home/$CONTEXT/mail/" ]]; then
        log "Maildir is inside /home/$CONTEXT/mail/, already synced with the home directory."
    else
        log "[!] No maildir found for $USERNAME, skipping."
    fi
fi

# logs and stuff
"${RSYNC_CMD[@]}" /etc/openpanel/openpanel/core/users/"$USERNAME"/ "${REMOTE_USER}"@"${REMOTE_HOST}":/etc/openpanel/openpanel/core/users/"$USERNAME"

store_running_containers_for_user         # export running contianers on source and copy to dest

if [[ "$LIVE_TRANSFER" == true ]]; then
	opencli user-suspend "$USERNAME" > /dev/null 2>&1 &
fi
restore_running_containers_for_user       # start containers on dest
restart_services_on_target                # restart openpanel, webserver and admin on dest
refresh_quotas                            # recalculate user usage on dest
notify_destination                        # summary in the destination's notifications
success_message
exit 0
