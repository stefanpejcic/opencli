#!/bin/bash
################################################################################
# Script Name: domains/ssl.sh
# Description: Check SSL for domain, add custom certificate, view files.
# Usage: opencli domains-ssl <DOMAIN_NAME> [status|info|logs|auto|custom] [path/to/fullchain.pem path/to/key.pem] | opencli domains-ssl --notify
# Author: Stefan Pejcic
# Created: 22.03.2025
# Last Modified: 25.09.2026
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

# ======================================================================
# Constants
# shellcheck disable=SC1091
source /usr/local/opencli/lib/requirement.sh
require_command jq

GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
RESET='\033[0m'


usage() {
    echo "Usage:"
    echo -e "  opencli domains-ssl <DOMAIN>                 - Display command examples for this domain."
    echo -e "  opencli domains-ssl <DOMAIN> ${GREEN}status${RESET}          - Display current status for the domain."
    echo -e "  opencli domains-ssl <DOMAIN> ${GREEN}info${RESET}            - Display certificate files."
    echo -e "  opencli domains-ssl <DOMAIN> ${GREEN}logs${RESET} [${YELLOW}1000${RESET}|${YELLOW}-f${RESET}]  - View caddy SSL-related logs for the domain."
    echo -e "  opencli domains-ssl <DOMAIN> ${GREEN}custom${RESET} ${YELLOW}<cert_path>${RESET} ${YELLOW}<key_path>${RESET} - Switch to custom SSL for the domain."
    echo -e "  opencli domains-ssl <DOMAIN> ${GREEN}auto${RESET}            - Switch back to AutoSSL for the domain."
    echo -e "  opencli domains-ssl ${GREEN}--notify${RESET}                - Email users about SSL certificates that expire soon or fail to renew."
}

# emails users about AutoSSL certificates with 7 days or less left (renewal is failing, Caddy renews ~30 days before) and any certificate with 1 day or less left, once per certificate and alert
notify_ssl_expiry() {
    # shellcheck disable=SC1091
    . /usr/local/opencli/lib/email.sh
    local now acme_dir="/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory"
    now=$(date +%s)

    # only domains that point to this server, a parked or externally hosted domain never gets AutoSSL here
    local server_ips
    server_ips="$(curl --silent --max-time 3 -4 "https://ip.openpanel.com" 2>/dev/null) $(hostname -I 2>/dev/null) $(cat /etc/openpanel/openpanel/core/users/*/ip.json 2>/dev/null | grep -oE '[0-9]+(\.[0-9]+){3}')"

    # latest TLS error per domain from the last day, so the email can say why renewal fails
    local tls_errors
    tls_errors=$(timeout 60 podman logs --since 24h caddy 2>&1 | jq -Rr 'fromjson? | select(.level == "error" and ((.logger // "") | test("tls|acme"))) | [(.identifier // .server_name // ""), (.error // .msg // "")] | @tsv' 2>/dev/null)

    local rows
    rows=$(mariadb --defaults-extra-file=/etc/my.cnf -D panel -N -B -e "SELECT u.username, d.domain_url FROM domains d JOIN users u ON u.id = d.user_id WHERE u.username NOT LIKE 'SUSPENDED%' ORDER BY u.username" 2>/dev/null)

    local username domain conf cert type end_date end_secs days level reason line
    declare -A body tips_auto tips_custom pending
    while IFS=$'\t' read -r username domain; do
        [[ -n "$domain" ]] || continue
        conf="/etc/openpanel/caddy/domains/${domain}.conf"
        [[ -f "$conf" ]] || continue
        if grep -q "fullchain.pem" "$conf"; then
            type="custom"; cert="/etc/openpanel/caddy/ssl/custom/${domain}/fullchain.pem"
        elif grep -q "on_demand" "$conf"; then
            type="auto"; cert="${acme_dir}/${domain}/${domain}.crt"
        else
            continue
        fi
        [[ -f "$cert" ]] || continue

        end_date=$(openssl x509 -enddate -noout -in "$cert" 2>/dev/null | cut -d= -f2)
        end_secs=$(date -d "$end_date" +%s 2>/dev/null) || continue
        days=$(( (end_secs - now) / 86400 ))

        level=""
        (( days <= 1 )) && level=1
        [[ -z "$level" && "$type" == "auto" ]] && (( days <= 7 )) && level=7
        [[ -n "$level" ]] || continue

        dig +short A "$domain" 2>/dev/null | grep -E '^[0-9.]+$' | grep -qxFf <(tr ' ' '\n' <<< "$server_ips" | grep .) || continue

        # same certificate and same or worse alert already sent
        local state="/etc/openpanel/openpanel/core/users/${username}/.ssl_notified" sent
        sent=$(awk -v d="$domain" -v e="$end_secs" '$1 == d && $2 == e { print $3 }' "$state" 2>/dev/null)
        [[ -n "$sent" ]] && (( sent <= level )) && continue

        if (( end_secs < now )); then
            line="- ${domain}: the $( [[ "$type" == auto ]] && echo "AutoSSL" || echo "custom") certificate expired on $(date -u -d "@$end_secs" '+%Y-%m-%d %H:%M UTC')."
        else
            line="- ${domain}: the $( [[ "$type" == auto ]] && echo "AutoSSL" || echo "custom") certificate expires on $(date -u -d "@$end_secs" '+%Y-%m-%d %H:%M UTC') ($( (( days < 1 )) && echo "less than a day" || echo "${days} day(s)") left)."
        fi
        if [[ "$type" == "auto" ]]; then
            line+=" It should have been renewed automatically, so renewal is failing."
            reason=$(awk -F'\t' -v d="$domain" '$1 == d { r = $2 } END { print r }' <<< "$tls_errors" | cut -c1-300)
            [[ -n "$reason" ]] && line+=" Last error: ${reason}"
            tips_auto[$username]=1
        else
            tips_custom[$username]=1
        fi
        body[$username]+="${line}"$'\n'
        # written to the state file only once the email is sent, so a failed send is retried next run
        pending[$username]+="${domain} ${end_secs} ${level}"$'\n'
    done <<< "$rows"

    for username in "${!body[@]}"; do
        local tips=""
        [[ -n "${tips_auto[$username]}" ]] && tips+="For AutoSSL, make sure the domain's A and AAAA records point to this server, no CAA record blocks Let's Encrypt, and no proxy or firewall blocks /.well-known/acme-challenge/. "
        [[ -n "${tips_custom[$username]}" ]] && tips+="For a custom certificate, upload a renewed one on the Domains > SSL Certificates page in OpenPanel, or switch the domain to AutoSSL to get free certificates that renew on their own."
        if send_user_email "$username" "SSL certificate problem on account $username" \
            "These SSL certificates on account $username need attention, without a valid certificate visitors see a security warning instead of the website:"$'\n\n'"${body[$username]}"$'\n'"Checked: $(date '+%Y-%m-%d %H:%M:%S %Z')" \
            notify_ssl_expiry "" "" "" "$tips"; then
            local state="/etc/openpanel/openpanel/core/users/${username}/.ssl_notified"
            # certificates that expired over 30 days ago are dropped, a replaced one never matches again
            { awk -v n="$now" 'NF == 3 && $2 > n - 2592000' "$state" 2>/dev/null; printf '%s' "${pending[$username]}"; } | awk '{ key = $1 " " $2; if (!(key in lvl) || $3 < lvl[key]) lvl[key] = $3 } END { for (k in lvl) print k, lvl[k] }' > "${state}.tmp" && mv "${state}.tmp" "$state"
            echo "$username: notified about $(grep -c . <<< "${body[$username]}") certificate(s)."
        else
            echo "$username: $(grep -c . <<< "${body[$username]}") certificate(s) need attention, no email sent (notifications off or no email address)."
        fi
    done
    [[ ${#body[@]} -eq 0 ]] && echo "No SSL certificates need attention."
}

if [ "$1" == "--notify" ]; then
    notify_ssl_expiry
    exit 0
fi

if [ -z "$1" ]; then
    echo "ERROR: Domain name is required!"
    usage
	exit 1
fi


DOMAIN="$1"
CONFIG_FILE="/etc/openpanel/caddy/domains/$DOMAIN.conf"

if [ ! -f "$CONFIG_FILE" ]; then
    echo -e "${RED}Domain ${DOMAIN} does not exist.$RESET"
    exit 1
fi






hostfs_domain_tls_dir="/etc/openpanel/caddy/ssl/custom/$DOMAIN"
domain_tls_dir="/data/caddy/certificates/custom/$DOMAIN"

get_user() {
  whoowns_output=$(opencli domains-whoowns "$DOMAIN")
  user=$(echo "$whoowns_output" | awk -F "Owner of '$DOMAIN': " '{print $2}')
  if [ -n "$user" ]; then
    :
  else
      echo "Failed to determine the owner of the domain '$DOMAIN'." >&2
      exit 1
  fi
  
}

get_user_info() {
    local user="$1"
    local query="SELECT id, server FROM users WHERE username = '${user}';"
    
    user_info=$(mariadb -se "$query")

    user_id=$(echo "$user_info" | awk '{print $1}')
    context=$(echo "$user_info" | awk '{print $2}')
    
    echo "$user_id,$context"
}




get_user_context() {

  result=$(get_user_info "$user")
  user_id=$(echo "$result" | cut -d',' -f1)
  context=$(echo "$result" | cut -d',' -f2)

  if [ -z "$user_id" ]; then
      echo "FATAL ERROR: user $user does not exist."
      exit 1
  fi
}


check_and_use_tls() {
	local full_cert="$1"
 	local full_key="$2"

	# 1. validate and format paths
	if [[ ! "$full_cert" =~ ^/var/www/html/ || ! "$full_key" =~ ^/var/www/html/ ]]; then
	  echo "ERROR: Paths must be inside /var/www/html/ directory."
	  exit 1
	fi

	local cert_path="${full_cert##/var/www/html/}"
 	local key_path="${full_key##/var/www/html/}"
 	local real_cert_path="/home/${context}/docker-data/volumes/${context}_html_data/_data/${cert_path}"
  	local real_key_path="/home/${context}/docker-data/volumes/${context}_html_data/_data/${key_path}"

  	# 2. check if cert is valid
	if ! openssl x509 -noout -checkend 0 -in "$real_cert_path" >/dev/null 2>&1; then
	    echo "Error: $cert_path is not valid or has expired!"
	    exit 1
	fi

	# 3. copy files from user homedir to /etc/openpanel/caddy/ssl/custom
	mkdir -p "$hostfs_domain_tls_dir"
	cp "${real_cert_path}" "$hostfs_domain_tls_dir"/fullchain.pem
	cp "${real_key_path}" "$hostfs_domain_tls_dir"/key.pem

	# 4. update caddyfile
	if grep -qE "tls\s+/.*?/fullchain\.pem\s+/.*?/key\.pem" "$CONFIG_FILE"; then
		echo "Custom SSL already configured for $DOMAIN. Updating certificate and key.."
	else
		echo "Adding custom certificate.."
			sed -i "/tls {/,/}/c\
tls $domain_tls_dir/fullchain.pem $domain_tls_dir/key.pem
" "$CONFIG_FILE"
	fi
	
	# 5. reload caddy
	nohup podman exec caddy sh -c "caddy validate && caddy reload" > /dev/null 2>&1 &
	disown

	# 6. notify
	nohup opencli sentinel --action=domains_ssl --title="Custom SSL set for domain" --message="Custom SSL is set for domain name: '$DOMAIN' owned by OpenPanel user '$user'." >/dev/null 2>&1 &
	disown

    echo "Updated $DOMAIN to use custom SSL."
}



cat_certificate_files() {
    	if grep -q "fullchain.pem" "$CONFIG_FILE" && grep -q "key.pem" "$CONFIG_FILE"; then
	 		# custom ssl
    		cat "$hostfs_domain_tls_dir"/fullchain.pem
    		cat "$hostfs_domain_tls_dir"/key.pem
    	else
	 		# letsencrypt
    		local cert="/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/$DOMAIN/$DOMAIN.crt"
          	local key="/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/$DOMAIN/$DOMAIN.key"
			[ -f "$cert" ] && cat "$cert"
			[ -f "$key" ] && cat "$key"
    	fi    
}


show_ssl_logs() {
    local lines=1000
    local follow=0

    # $3 is a number or '-f'
    if [[ "$3" =~ ^[0-9]+$ ]]; then
        lines="$3"
    elif [[ "$3" == "-f" ]]; then
        follow=1
    fi

    echo "Showing SSL-related log lines for $DOMAIN"	
    echo "-------------------------------------------------------"
	# podman logs --tail 1000 caddy 2>&1  | grep "$DOMAIN" | grep -Ei 'tls|acme|certificate|renew|obtain|challenge'

    # LogPath is only populated for json-file/k8s-file log drivers -- podman's default journald driver leaves this empty and hits the same error path below
    local log_path
    log_path=$(podman inspect --format='{{.LogPath}}' caddy 2>/dev/null)

    if [ -z "$log_path" ] || [ ! -f "$log_path" ]; then
        echo "ERROR: Could not determine Caddy log file path."
        exit 1
    fi

    if [ "$follow" -eq 1 ]; then
        tail -n "$lines" -f "$log_path"
    else
        tail -n "$lines" "$log_path"
    fi | jq -r '.log' | jq -R -c --arg domain "$DOMAIN" '
        fromjson? |
        select(
            .
            and
            (
                .request.host? == $domain or
                .request.tls.server_name? == $domain or
                .server_name? == $domain or
                (.identifiers?[]? == $domain)
            )
            and
            (
                (.logger? | test("tls|acme")) or
                (.msg? | test("certificate|renew|obtain|challenge"; "i"))
            )
        )
    '
}





show_examples() {
	echo -e "Usage examples for domain ${YELLOW}$DOMAIN${RESET}:"
	echo ""
	echo "- Check current SSL status for domain (AutoSSL, CustomSSL or No SSL):"
	echo -e "  opencli domains-ssl ${YELLOW}$DOMAIN${RESET} ${GREEN}status${RESET}"
	echo "- Display fullchain and key files for the domain:"
	echo -e "  opencli domains-ssl ${YELLOW}$DOMAIN${RESET} ${GREEN}info${RESET}"
	echo "- Set AutoSSL for the domain (default):"
	echo -e "  opencli domains-ssl ${YELLOW}$DOMAIN${RESET} ${GREEN}auto${RESET}"
	echo "- Add custom certificate for the domain:"
	echo -e "  opencli domains-ssl ${YELLOW}$DOMAIN${RESET} ${GREEN}custom${RESET} ${RED}/var/www/html/fullchain.pem /var/www/html/key.pem${RESET}"
	echo "- View SSL-related lines for the domain from Caddy logs:"
	echo -e "  opencli domains-ssl ${YELLOW}$DOMAIN${RESET} ${GREEN}logs${RESET}"
}


check_custom_ssl_or_auto() {   
	if grep -q "fullchain.pem" "$CONFIG_FILE"; then
	    echo "Custom SSL"
	elif grep -q "on_demand" "$CONFIG_FILE"; then
	    echo "AutoSSL"
	else
	    echo "Unknown"
	fi
}


if [ -n "$2" ]; then

  get_user
  get_user_context

    if [ "$2" == "info" ]; then
	cat_certificate_files
	exit 0
    elif [ "$2" == "status" ]; then
    	check_custom_ssl_or_auto
    	exit 0
    elif [ "$2" == "auto" ]; then

		# 1. replace custom ssl paths with on_demand
        sed -i -E "s|tls\s+/.*?/fullchain\.pem\s+/.*?/key\.pem|  tls {\n    on_demand\n  }|g" "$CONFIG_FILE"

		# 2. reload caddy
	    nohup podman exec caddy sh -c "caddy validate && caddy reload" > /dev/null 2>&1 &
	    disown

		# 3. notify
		nohup opencli sentinel --action=domains_ssl --title="AutoSSL set for domain" --message="AutoSSL is set for domain name: '$DOMAIN' owned by OpenPanel user '$user'." >/dev/null 2>&1 &
		disown

        echo "Updated $DOMAIN to use AutoSSL."
        exit 0
    elif [ "$2" == "custom" ] && [ -n "$3" ] && [ -n "$4" ]; then       
        check_and_use_tls "$3" "$4"
        exit 0
	elif [ "$2" == "logs" ]; then
	    show_ssl_logs "$@"
	    exit 0
    else
	    echo "ERROR: Invalid arguments provided for domain!"
	    usage	
        exit 1
    fi
else
	show_examples
	exit 0
fi
