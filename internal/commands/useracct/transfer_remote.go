// ##############################################################################
// Script Name: internal/commands/useracct/transfer_remote.go
// Description: Remote shell scripts user-transfer runs on the destination over ssh.
// Usage: imported by user-transfer
// Author: Stefan Pejcic
// Created: 28.06.2025
// Last Modified: 25.11.2026
// Company: openpanel.com
// Copyright (c) openpanel.com
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.
// ##############################################################################

package useracct

// heredoc bodies from the old script, kept as is since they run on the destination, ´ stands for a backtick
const trCopyUser = `
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
`

const trImportMySQL = `
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
  (echo "USE \\\´\$mysql_database\\\´;" && cat "plan_\${USERNAME}_autoinc.sql") | mariadb --defaults-extra-file="\$CONFIG_FILE"
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
(echo "USE \\\´\$mysql_database\\\´;" && cat tmp_user.sql) | mariadb --defaults-extra-file="\$CONFIG_FILE"
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
  sed "s/INSERT INTO \\\´\${t}\\\´/INSERT INTO \\\´_import_\${t}\\\´/" "\${t}.rows.sql" | mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$mysql_database"
  COLS=\$(mariadb --defaults-extra-file="\$CONFIG_FILE" -N -s -e "SELECT GROUP_CONCAT(column_name ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='\$mysql_database' AND table_name='\$t' AND column_name NOT IN ('id','user_id');")
  mariadb --defaults-extra-file="\$CONFIG_FILE" -D "\$mysql_database" -e "INSERT IGNORE INTO \${t} (user_id,\$COLS) SELECT \$USER_ID,\$COLS FROM _import_\${t}; DROP TABLE _import_\${t};" && echo "Imported \$(grep -c '^INSERT' "\${t}.rows.sql") row(s) into \$t"
done


`

const trImportSites = `
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
`

const trNamedZone = `
grep -qF "zone \"$domain\"" /etc/bind/named.conf.local || \
echo 'zone "$domain" IN { type master; file "/etc/bind/zones/$domain.zone"; };' >> /etc/bind/named.conf.local
`

const trFTP = `
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

    # rsync already brought the folder with its modes, only a missing one gets the FTP defaults
    if [[ ! -e "\$new_directory" ]]; then
        mkdir -p "\$new_directory"
        chown "\$GID:\$GID" "\$new_directory"
        chmod 2775 "\$new_directory"
    fi
    chmod +rx "/home/\$context" \\
              "/home/\$context/docker-data" \\
              "/home/\$context/docker-data/volumes" \\
              "/home/\$context/docker-data/volumes/\${context}_html_data" \\
              "/home/\$context/docker-data/volumes/\${context}_html_data/_data" 2>/dev/null || true

    # Recreate the container user with the SAME hashed password (no re-hashing)
    podman exec openadmin_ftp useradd -d "\$new_directory" -s /sbin/nologin \\
        -g "\$context" -M "\$username" --badname 2>/dev/null || true
    podman exec openadmin_ftp sh -c "usermod -p '\$hashed_pass' '\$username'"

    echo "[FTP] restored \$username -> \$directory"
done < "\$USERS_LIST"
exit 0
`

const trMail = `
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
opencli email-default_sync
if [[ -s postfwd.cf ]]; then
    touch /usr/local/mail/openmail/postfwd/postfwd.cf
    while IFS= read -r id_line; do
        IFS= read -r action_line
        grep -qxF "\$id_line" /usr/local/mail/openmail/postfwd/postfwd.cf || printf '%s\n%s\n' "\$id_line" "\$action_line" >> /usr/local/mail/openmail/postfwd/postfwd.cf
    done < postfwd.cf
    echo "Merged postfwd.cf"
fi
rm -rf /tmp/mail_import_${USERNAME}
`
