#!/bin/bash
################################################################################
# Script Name: user/delete.sh
# Description: Delete user account and permanently remove all their data.
# Usage: opencli user-delete <username> [-y]
# Author: Stefan Pejcic
# Created: 01.10.2023
# Last Modified: 26.10.2026
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
. /usr/local/opencli/lib/redis.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/requirement.sh
require_command jq

# ======================================================================
# Variables
skip_confirmation=false



# ======================================================================
# Validations
if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
    echo "Usage: opencli user-delete <username> [-y]"
    exit 1
fi

confirm_action() {
    if [ "$skip_confirmation" = true ]; then
        return 0
    fi

    read -r -p "This will permanently delete user '$USERNAME' and all associated data. Confirm? [Y/n]: " response
    response=${response,,} # to lowercase
    if [[ ! $response =~ ^(yes|y| ) ]]; then
        echo "Operation canceled for user '$USERNAME'."
        exit 0
    fi
}



source /usr/local/opencli/db.sh


# ======================================================================
# Functions

get_user_info() {
	# 1. get context and ID
    local escaped_username
    escaped_username=$(mysql_escape "$USERNAME")
    # exact name or SUSPENDED_<14 digit timestamp>_<name>, exact name first
    read -r user_id context <<< "$(mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -e "
        SELECT id, server FROM users
        WHERE username='$escaped_username'
           OR (username REGEXP '^SUSPENDED_[0-9]{14}_' AND SUBSTRING(username, 26) = '$escaped_username')
        ORDER BY username='$escaped_username' DESC
        LIMIT 1;
    ")"

	[ -n "$user_id" ] || { echo "ERROR: User '$USERNAME' not found in the database."; exit 1; }
	[ -n "$context" ]  || { echo "ERROR: Could not determine podman context for '$USERNAME'."; exit 1; }
}

delete_user_from_database() {
    openpanel_username="$1"

    # 1. Get all domain IDs and URLs
	read -r domain_ids domain_urls < <(mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -e "
	SELECT 
	    GROUP_CONCAT(domain_id) AS ids,
	    GROUP_CONCAT(domain_url) AS urls
	FROM domains
	WHERE user_id='$user_id';
	")

	# 2. Prepare SQL queries and execute all at once
    sql=""
	[ -n "$domain_ids" ] && sql+="DELETE FROM sites WHERE domain_id IN ($domain_ids); "
    sql+="DELETE FROM domains WHERE user_id='$user_id'; "
	sql+="DELETE FROM users WHERE id='$user_id';"
	[ -n "$sql" ] && mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -e "$sql"

	# 3. terminate redis sessions
	redis_drop_user_sessions "$user_id"
	redis_drop_user_cache "$user_id" "$openpanel_username"
	# the account is gone, so drop every cached key under its name, otherwise a new user with the same name sees the old data
	redis_drop_pattern "openpanel_cache_*:${openpanel_username}" "openpanel_cache_*:${openpanel_username}:*"

	# 4. delete domain files, emails and reload Caddy
	ionice -c3 rm -rf "/var/log/caddy/stats/$openpanel_username"            # goaccess reports
    if [ -n "$domain_urls" ]; then
		local email_storage_path; email_storage_path=$(grep -E '^email_storage_location=' /etc/openpanel/openadmin/config/admin.ini | cut -d'=' -f2- | xargs)
	    IFS=',' read -ra domains_array <<< "$domain_urls"
        paths_to_delete=()
        for domain in "${domains_array[@]}"; do
		    if [[ "$email_storage_path" == /* && "$email_storage_path" != "user_dir" ]]; then
		        paths_to_delete+=("$email_storage_path/$domain")
		    fi
           paths_to_delete+=(
           "/etc/openpanel/caddy/domains/$domain.conf"
           "/etc/openpanel/caddy/suspended_domains/$domain.conf"
           "/var/log/caddy/domlogs/$domain"
           "/var/log/caddy/coraza_waf/$domain.log"
           "/etc/openpanel/caddy/ssl/custom/$domain"
		   "/etc/bind/zones/$domain.zone"
           )

	        if grep -q "zone \"$domain\"" /etc/bind/named.conf.local; then
	            sed -i "/zone \"$domain\" IN {/,/};/d" /etc/bind/named.conf.local
	        fi
        done
		[[ ${#paths_to_delete[@]} -gt 0 ]] && ionice -c3 rm -rf "${paths_to_delete[@]}"
		# reload webserver
        nohup podman exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1 &
        disown
		# reload dns
        nohup podman exec openpanel_dns rndc reconfig >/dev/null 2>&1 &
        disown
    fi
}

postfwd_setup(){
    # Delete hourly email limits for the user
	opencli email-ratelimit --delete-user="$1" >/dev/null 2>&1		
}

delete_emails() {
    openpanel_username="$1"
    local user_domains=()

    # email accounts
    local email_file="/etc/openpanel/openpanel/core/users/$openpanel_username/emails.yml"
    emails=()
    if [ -f "$email_file" ]; then
        while read -r _ email _; do
            if [ -n "$email" ]; then
                emails+=("$email")
                domain="${email#*@}"
                [[ -n "$domain" ]] && user_domains+=("$domain")
            fi
        done < "$email_file"
    fi

    # emails.yml is only rewritten when /emails is opened, so also catch live mailboxes on the user's domains
    local db_domains
    db_domains=$(mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -e "SELECT domain_url FROM domains WHERE user_id='$user_id';" 2>/dev/null)
    if [ -n "$db_domains" ]; then
        while read -r mailbox; do
            [[ " ${emails[*]} " == *" $mailbox "* ]] || emails+=("$mailbox")
        done < <(opencli email-setup email list 2>/dev/null | awk -v doms="$db_domains" '
            BEGIN { n = split(tolower(doms), d, "\n"); for (i = 1; i <= n; i++) own[d[i]] = 1 }
            $1 == "*" { split(tolower($2), p, "@"); if (p[2] in own) print $2 }')
        while read -r domain; do
            [[ -n "$domain" ]] && user_domains+=("$domain")
        done <<< "$db_domains"
    fi

    # not backgrounded, a detached del got lost and left the mailboxes behind
    if [ "${#emails[@]}" -gt 0 ]; then
        opencli email-setup email del -y "${emails[@]}" >/dev/null 2>&1
    fi

    mapfile -t user_domains < <(printf '%s\n' "${user_domains[@]}" | sort -u)

    # aliases
    aliases_file="/etc/openpanel/openpanel/core/users/$openpanel_username/aliases.yml"
    if [ -f "$aliases_file" ]; then
        aliases=()
        while read -r _ email target; do
            if [ -n "$email" ]; then
                aliases+=("$email")
                domain="${email#*@}"
                [[ -n "$domain" ]] && user_domains+=("$domain")
            fi
        done < "$aliases_file"

        if [ "${#aliases[@]}" -gt 0 ]; then
            nohup opencli email-setup alias del "${aliases[@]}" >/dev/null 2>&1 &
            disown
        fi
    fi

    mapfile -t user_domains < <(printf '%s\n' "${user_domains[@]}" | sort -u)

    # regex aliases (default email address / catch-all)
    regex_aliases_file="/usr/local/mail/openmail/docker-data/dms/config/postfix-regex.cf"
    if [ -f "$regex_aliases_file" ] && [ "${#user_domains[@]}" -gt 0 ]; then
        tmp_file="$(mktemp)"
        changed=0

        while read -r pattern target; do
            [ -z "$pattern" ] && continue
            matched=0
            for domain in "${user_domains[@]}"; do
                if [[ "$pattern" =~ @${domain//./\\.}\$ ]]; then
                    matched=1
                    break
                fi
            done
            if [ "$matched" -eq 1 ]; then
                changed=1
            else
                echo "$pattern $target" >> "$tmp_file"
            fi
        done < "$regex_aliases_file"

        if [ "$changed" -eq 1 ]; then
            mv "$tmp_file" "$regex_aliases_file"
        else
            rm -f "$tmp_file"
        fi
    fi
}

delete_ftp_users() {
    local context="$1"
    local users_dir="/etc/openpanel/ftp/users"
    local ftp_accounts_file="${users_dir}/${context}/users.list"

    if [[ -d "${users_dir}/${context}" ]]; then
        if [[ -f "$ftp_accounts_file" ]]; then
            cut -d'|' -f1 "$ftp_accounts_file" | xargs -I{} podman exec openadmin_ftp deluser {}
        fi
        ionice -c3 rm -rf "${users_dir:?}/${context:?}"
    fi

    if podman exec openadmin_ftp getent group "$context" >/dev/null 2>&1; then
        podman exec openadmin_ftp delgroup "$context" >/dev/null 2>&1
    fi
}

delete_all_user_files() {
	local context="$1"
	# 1. delete files
    [ -d /home/"$context" ] && rm -rf "/home/${context:?}"
    [ -d /etc/openpanel/openpanel/core/users/"$context" ] && rm -rf "/etc/openpanel/openpanel/core/users/$context"
    # differs from the context after user-rename
    [ -n "$openpanel_username" ] && [ -d /etc/openpanel/openpanel/core/users/"$openpanel_username" ] && rm -rf "/etc/openpanel/openpanel/core/users/${openpanel_username:?}"
}


delete_system_user() {
    local context="$1" uid
    uid=$(id -u "$context" 2>/dev/null)

    # stop the user's systemd and podman first, with linger on they'd keep /run/user/<uid> alive
    loginctl disable-linger "$context" 2>/dev/null || true
    [[ "$uid" =~ ^[0-9]+$ ]] && systemctl stop "user@${uid}.service" 2>/dev/null || true

    # kill by both real and effective/saved uid, then loop until gone
    pkill -9 -u "$context" 2>/dev/null || true
    for _ in $(seq 1 10); do
        pgrep -u "$context" >/dev/null 2>&1 || break
        pkill -9 -u "$context" 2>/dev/null || true
        sleep 0.25
    done

    # last resort: still processes? force with loginctl / kill by name lookup
    if pgrep -u "$context" >/dev/null 2>&1; then
        loginctl terminate-user "$context" 2>/dev/null || true
        sleep 1
        pkill -9 -u "$context" 2>/dev/null || true
    fi

    if command -v userdel >/dev/null 2>&1; then
        userdel -f -r "$context"          # -f forces even if logged in / process running
    elif command -v deluser >/dev/null 2>&1; then
        deluser --remove-home "$context"
    fi

    # a new account that gets this uid would inherit the old podman network state from here and its containers wouldn't start
    [[ "$uid" =~ ^[0-9]+$ && "$uid" -ge 1000 ]] && rm -rf "/run/user/${uid:?}"

    # if the stop timed out or the pkill above hit the user manager, user@<uid> stays failed and the server shows degraded
    [[ "$uid" =~ ^[0-9]+$ ]] && systemctl reset-failed "user@${uid}.service" "user-runtime-dir@${uid}.service" 2>/dev/null || true
}

refresh_resellers_data() {
	local reseller_files="/etc/openpanel/openadmin/resellers"
	# todo: optimize, this checks all resellers instead of just the account owner
	if [ -d "$reseller_files" ]; then
		for json_file in "$reseller_files"/*.json; do
			if [ -f "$json_file" ]; then
				reseller=$(basename "$json_file" .json)
				query_for_owner="SELECT COUNT(*) FROM users WHERE owner='$(mysql_escape "$reseller")';"
				current_accounts=$(mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -e "$query_for_owner")
				mysql_exit=$?
				if [ $mysql_exit -eq 0 ]; then				
					jq ".current_accounts = $current_accounts" "$json_file" > /tmp/"${reseller}"_config.json && mv /tmp/"${reseller}"_config.json "$json_file"
				fi
			fi
		done
	fi
}

USERNAME="$1"

if [ "$2" = "-y" ]; then
	skip_confirmation=true
fi

# ======================================================================
# Main

if [ -z "$USERNAME" ]; then
	echo "ERROR: Username is required."
	exit 1
fi

USERNAME="${USERNAME##*_}"

# 1. -y
confirm_action "$USERNAME"

# 2. get docker context and user ID from database
get_user_info

# 3. in parallel: delete emails, delete ftp accounts, user/sites/domains from database, homedir, postfwd limits
delete_emails "$USERNAME"
delete_ftp_users "$context" &
delete_user_from_database "$USERNAME" &
delete_all_user_files "$context" &
delete_system_user "$context" #& NOT DETACHED TO TEST FOR https://github.com/stefanpejcic/OpenPanel/issues/1023
postfwd_setup "$USERNAME" &

# 4. wait for all of the above functions to finish
wait

# 5. notify
echo "User $USERNAME deleted successfully."
nohup opencli sentinel --action=user_delete --title="User account deleted" --message="User account '$USERNAME' has been deleted." >/dev/null 2>&1 &
disown

# 6. refresh resellers usage
refresh_resellers_data &
nohup opencli user-quota >/dev/null 2>&1 &
disown
