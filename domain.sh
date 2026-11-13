#!/bin/bash
################################################################################
# Script Name: domain.sh
# Description: View and set domain/ip for accessing panels.
# Usage: opencli domain [set <domain_name> | ip] [--debug] [--no-restart]
# Author: Stefan Pejcic
# Created: 09.02.2025
# Last Modified: 13.11.2026
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
# Configuration
# shellcheck disable=SC1091
. /usr/local/opencli/lib/podman.sh
readonly CADDY_FILE="/etc/openpanel/caddy/Caddyfile"
readonly DOMAINS_DIR="/etc/openpanel/caddy/domains/"
readonly MAILSERVER_ENV="/usr/local/mail/openmail/mailserver.env"
readonly MAILSERVER_COMPOSE_FILE="/usr/local/mail/openmail/compose.yml"
readonly REDIRECTS_CONF="/etc/openpanel/caddy/redirects.conf"
readonly DEFAULT_DOMAIN="example.net"

# ======================================================================
# Variables
# shellcheck disable=SC1091
source /usr/local/opencli/lib/redis.sh
DEBUG=false
current_domain=""
new_hostname=""
server_ip=""

# ======================================================================
# Helpers

log_debug() {
    if [[ "$DEBUG" == true ]]; then
        echo "$1"
    fi
}

log_error() {
    echo "Error: $1" >&2
}

log_info() {
    echo "$1"
}

is_valid_ipv4() {
    local ip="$1"
    [[ $ip =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || return 1
    [[ $ip =~ ^10\. ]] && return 1
    [[ $ip =~ ^172\.(1[6-9]|2[0-9]|3[0-1])\. ]] && return 1
    [[ $ip =~ ^192\.168\. ]] && return 1
    return 0
}

# get server's public IPv4 address
get_server_ipv4() {
    local current_ip=""
	current_ip=$(curl --silent --max-time 1 -4 "https://ip.openpanel.com" 2>/dev/null || curl --silent --max-time 1 -4 "https://ifconfig.me/ip")

	if is_valid_ipv4 "$current_ip"; then
		echo "$current_ip"
		return 0
	fi
	
	current_ip=$(curl --silent --max-time 1 -4 "https://ifconfig.me" 2>/dev/null)
	if is_valid_ipv4 "$current_ip"; then
		echo "$current_ip"
		return 0
	fi
    
	if command -v ip >/dev/null 2>&1; then
	    current_ip=$(ip addr | grep 'inet ' | grep global | head -n1 | awk '{print $2}' | cut -f1 -d/)
	else
	    current_ip=$(hostname -I | awk '{print $1}')
	fi
	
    if is_valid_ipv4 "$current_ip"; then
        echo "$current_ip"
        return 0
    fi
    
    log_error "Invalid or private IPv4 address. Please contact support."
    return 1
}

is_valid_domain() {
    local input="$1"
    local domain_regex='^([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$'
    local ipv4_regex='^((25[0-5]|2[0-4][0-9]|1?[0-9]{1,2})\.){3}(25[0-5]|2[0-4][0-9]|1?[0-9]{1,2})$'
    [[ "$input" =~ $domain_regex || "$input" =~ $ipv4_regex ]]
}

# the hostname used to be pinned to 169.254.1.2 (the host as rootless containers see it) in the host's own /etc/hosts,
# which the host itself can't reach, so curl to the panel from the server hung, e.g. sentinel's emails. Containers reach
# the public IP fine, so drop that line and leave container-only names to opencli docker-hosts when it's installed.
update_container_hosts_entry() {
    local domain="$1"
    [[ -z "$domain" ]] && return 0
    sed -i -E "/^169\.254\.1\.2[[:space:]]+(.*[[:space:]])?${domain//./\\.}([[:space:]]|\$)/d" /etc/hosts
    if [[ -f /usr/local/opencli/docker/hosts.sh ]]; then
        opencli docker-hosts >/dev/null 2>&1 || true
    fi
}

# reads the current domain from the Caddyfile
get_current_domain() {
    if [[ ! -f "$CADDY_FILE" ]]; then
        log_error "Caddyfile not found at $CADDY_FILE"
        return 1
    fi

    extract_domain() {
        local start_marker="$1"
        local end_marker="$2"
        sed -n "/$start_marker/,/$end_marker/p" "$CADDY_FILE" | sed -n 's/^\([A-Za-z0-9.-]*\)[[:space:]]*{.*/\1/p' | head -n1
    }

    extract_ip() {
        local start_marker="$1"
        local end_marker="$2"
        sed -n "/$start_marker/,/$end_marker/p" "$CADDY_FILE" | sed -n 's/^\([0-9]\{1,3\}\(\.[0-9]\{1,3\}\)\{3\}\)[[:space:]]*{.*/\1/p' | head -n1
    }

    # used to branch on /.dockerenv here, but OpenAdmin always runs natively on the host now, so just check both markers unconditionally
    domain=$(extract_domain "# START USERPANEL DOMAIN #" "# END USERPANEL DOMAIN #")
    if [[ -z "$domain" ]]; then
        domain=$(extract_domain "# START HOSTNAME DOMAIN #" "# END HOSTNAME DOMAIN #")
    fi

    # Fallback: check IP block
    if [[ -z "$domain" ]]; then
        domain=$(extract_ip "# START HOSTNAME IP #" "# END HOSTNAME IP #")
    fi

    echo "$domain"
}

get_current_domain_or_ip() {
    local domain
    domain=$(get_current_domain)
    
    if [[ "$domain" == "$DEFAULT_DOMAIN" ]]; then
        get_server_ipv4
    else
        echo "$domain"
    fi
}

update_caddyfile() {
    log_debug "Updating Caddyfile with new domain/IP: $new_hostname"

    if [[ ! -f "$CADDY_FILE" ]]; then
        log_error "Caddyfile not found at $CADDY_FILE"
        return 1
    fi

    #IPv4
	if [[ "$new_hostname" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
        # was using IPv4
        if grep -q "# START HOSTNAME IP #" "$CADDY_FILE"; then
            sed_in_place "$CADDY_FILE" "/# START HOSTNAME IP #/,/# END HOSTNAME IP #/ s/[0-9]\{1,3\}\.[0-9]\{1,3\}\.[0-9]\{1,3\}\.[0-9]\{1,3\}/$new_hostname/"
        else
		# was not using IPv4
			local admin_port; admin_port=$(opencli admin port)
            local ip_block="# START HOSTNAME IP #\n${new_hostname} {\n  tls {\n    reuse_private_keys\n    issuer acme {\n      profile shortlived\n    }\n  }\n  reverse_proxy localhost:${admin_port}\n}\n# END HOSTNAME IP #\n"
            sed_in_place "$CADDY_FILE" "s|# START HOSTNAME DOMAIN #|${ip_block}# START HOSTNAME DOMAIN #|"
        fi
		# example.net
		sed_in_place "$CADDY_FILE" "s/$current_domain/$DEFAULT_DOMAIN/g"
    else
        # was using a domain
        sed_in_place "$CADDY_FILE" "s/$current_domain/$new_hostname/g"
    fi
}

# Create or move domain configuration file
create_mv_file() {
    log_debug "Managing domain configuration file"
    
    mkdir -p "$DOMAINS_DIR"
    
    if [[ "$new_hostname" == "$DEFAULT_DOMAIN" ]]; then
        # Remove existing domain file when switching to IP, nothing to do if already on IP
        rm -f "${DOMAINS_DIR}${current_domain}.conf"
    else
        # Move or create domain file
        if [[ -f "${DOMAINS_DIR}${current_domain}.conf" ]]; then
            mv "${DOMAINS_DIR}${current_domain}.conf" "${DOMAINS_DIR}${new_hostname}.conf" 2>/dev/null
        fi
        touch "${DOMAINS_DIR}${new_hostname}.conf"
    fi
}

update_redirects() {
    local redirect_url
    
    if [[ "$new_hostname" == "$DEFAULT_DOMAIN" ]]; then
        redirect_url="http://$server_ip"
        log_debug "Updating redirects to use IP address"
    else
        redirect_url="https://$new_hostname"
        log_debug "Updating redirects to use custom domain"
    fi
    
    if [[ ! -f "$REDIRECTS_CONF" ]]; then
        log_error "Redirects configuration not found at $REDIRECTS_CONF"
        return 1
    fi
    
    sed -i -E "s|(redir @openpanel )https?://[^:]+:|\1${redirect_url}:|; s|(redir @openadmin )https?://[^:]+:|\1${redirect_url}:|" "$REDIRECTS_CONF"
    update_webmail_redirect
    update_mcp_proxy
}

# /webmail follows the hostname only while it's on the default (ip:8080 or webmail.<old hostname>), a custom one from opencli email-webmail stays
update_webmail_redirect() {
    local cur new
    cur=$(grep -oP '^redir @webmail \Khttps?://[^ ]+' "$REDIRECTS_CONF" | head -n 1)
    [[ -z "$cur" ]] && return 0
    cur="${cur%/}"
    if [[ "$cur" != "http://${server_ip}:8080" && "$cur" != "https://webmail.${current_domain}" && "$cur" != "https://webmail.${DEFAULT_DOMAIN}" ]]; then
        log_debug "Webmail redirect $cur is custom, leaving it"
        return 0
    fi
    if [[ "$new_hostname" == "$DEFAULT_DOMAIN" ]]; then
        new="http://${server_ip}:8080/"
    elif grep -q "^webmail\.${new_hostname//./\\.} {" "$CADDY_FILE"; then
        new="https://webmail.${new_hostname}/"
    else
        return 0
    fi
    sed -i -E "s|^(redir @webmail )https?://[^ ]+|\1${new}|" "$REDIRECTS_CONF"
    redis_drop_pattern "openpanel_cache_get_webmail_domain:*"
    log_debug "Webmail redirect changed from $cur to $new"
}

# panel only serves tls when it has a cert for its domain, so the /mcp upstream scheme follows the domain
update_mcp_proxy() {
    local port tmp
    port=$(grep -oP 'reverse_proxy @mcp https?://127\.0\.0\.1:\K[0-9]+' "$REDIRECTS_CONF" | head -n 1)
    port=${port:-2083}

    if ! grep -q '^# START MCP #' "$REDIRECTS_CONF"; then
        printf '\n# MCP: every domain'"'"'s /mcp is proxied to the panel, opencli domain/port keep this block in sync\n# START MCP #\n# END MCP #\n' >> "$REDIRECTS_CONF"
    fi

    tmp=$(mktemp) || return 1
    awk -v host="$new_hostname" -v def="$DEFAULT_DOMAIN" -v port="$port" '
        /^# START MCP #/ {
            print
            print "@mcp path /mcp /mcp/*"
            if (host == def) {
                print "reverse_proxy @mcp http://127.0.0.1:" port
            } else {
                print "reverse_proxy @mcp https://127.0.0.1:" port " {"
                print "    transport http {"
                print "        tls_server_name " host
                print "    }"
                print "}"
            }
            skip = 1
            next
        }
        /^# END MCP #/ { skip = 0 }
        !skip
    ' "$REDIRECTS_CONF" > "$tmp" && cat "$tmp" > "$REDIRECTS_CONF"
    rm -f "$tmp"
}

# roundcube connects to ssl://<hostname>, older versions mapped the hostname to 169.254.1.2 in the host's /etc/hosts
# (the host as seen from rootless user containers), which roundcube on the rootful network can't reach: pin it to the public IP
set_roundcube_extra_host() {
    local host="$1"
    sed -i '/extra_hosts: # opencli domain$/,+1d' "$MAILSERVER_COMPOSE_FILE"
    [[ -z "$host" || -z "$server_ip" ]] && return 0
    sed -i "/container_name: openadmin_roundcube/a\\    extra_hosts: # opencli domain\\n      - \"${host}:${server_ip}\"" "$MAILSERVER_COMPOSE_FILE"
}

# caddy requests the cert right after the reload, it usually lands within seconds
wait_for_hostname_cert() {
    local crt="$1" key="$2" i
    log_debug "Waiting up to 60s for Caddy to issue SSL certificate for $new_hostname"
    curl -sk --max-time 5 --resolve "${new_hostname}:443:127.0.0.1" "https://${new_hostname}/" -o /dev/null 2>&1 || true
    for i in $(seq 30); do
        [[ -f "$crt" && -f "$key" ]] && return 0
        sleep 2
    done
    return 1
}

configure_mailserver() {
    [[ ! -f "$MAILSERVER_ENV" ]] && return 0
	
    if [[ "$new_hostname" == "$DEFAULT_DOMAIN" ]] || is_valid_ipv4 "$new_hostname"; then
        log_debug "Configuring mailserver for plain-text authentication with IP"

		# used by roundcube to detect if SSL should be used
		sed -i '/^OVERRIDE_HOSTNAME=/c\OVERRIDE_HOSTNAME=' "$MAILSERVER_ENV"

        sed -i '/^SSL_TYPE=/c\SSL_TYPE=' "$MAILSERVER_ENV"
        sed -i '/^SSL_CERT_PATH=/d' "$MAILSERVER_ENV"
        sed -i '/^SSL_KEY_PATH=/d' "$MAILSERVER_ENV"

        # SRS needs a hostname to send forwarded mail from
        sed -i '/^ENABLE_SRS=/c\ENABLE_SRS=0' "$MAILSERVER_ENV"
        sed -i '/^SRS_DOMAINNAME=/d' "$MAILSERVER_ENV"

        # podman-compose doesn't interpolate ${VAR:+alt} conditionals like docker compose does, so hardcode the resolved roundcube values in the compose file too
        if [[ -f "$MAILSERVER_COMPOSE_FILE" ]]; then
            sed -i "s|ROUNDCUBEMAIL_DEFAULT_HOST=.*|ROUNDCUBEMAIL_DEFAULT_HOST=mailserver|" "$MAILSERVER_COMPOSE_FILE"
            sed -i "s|ROUNDCUBEMAIL_DEFAULT_PORT=.*|ROUNDCUBEMAIL_DEFAULT_PORT=|" "$MAILSERVER_COMPOSE_FILE"
            sed -i "s|ROUNDCUBEMAIL_SMTP_SERVER=.*|ROUNDCUBEMAIL_SMTP_SERVER=mailserver|" "$MAILSERVER_COMPOSE_FILE"
            sed -i "s|ROUNDCUBEMAIL_SMTP_PORT=.*|ROUNDCUBEMAIL_SMTP_PORT=|" "$MAILSERVER_COMPOSE_FILE"
            set_roundcube_extra_host ""
        fi
    else
        log_debug "Configuring mailserver for TLS/SSL authentication with domain"

        local cert_path_on_hosts="/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/${new_hostname}/${new_hostname}.crt"
        local key_path_on_hosts="/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/${new_hostname}/${new_hostname}.key"

        # custom paths!
        local fallback_cert_path="/etc/openpanel/caddy/ssl/custom/${new_hostname}/${new_hostname}.crt"
        local fallback_key_path="/etc/openpanel/caddy/ssl/custom/${new_hostname}/${new_hostname}.key"
        
        if [[ -f "$cert_path_on_hosts" && -f "$key_path_on_hosts" ]]; then
            log_debug "Using Let's Encrypt certs for $new_hostname"
        elif [[ -f "$fallback_cert_path" && -f "$fallback_key_path" ]]; then
            log_debug "Using custom certs for $new_hostname"
            cert_path_on_hosts="$fallback_cert_path"
            key_path_on_hosts="$fallback_key_path"
        elif wait_for_hostname_cert "$cert_path_on_hosts" "$key_path_on_hosts"; then
            log_debug "Let's Encrypt certs for $new_hostname issued"
        else
            # caddy may still get it later (dns propagation, rate limits), so finish the mail setup once it does
            log_info "SSL certificate for $new_hostname is not issued yet, mailserver will switch to it once Caddy gets one."
            nohup bash -c "for i in \$(seq 60); do sleep 10; [ -f '$cert_path_on_hosts' ] && [ -f '$key_path_on_hosts' ] && exec opencli domain set '$new_hostname' --no-restart; done" </dev/null >/dev/null 2>&1 &
            return 0
        fi

		# used by roundcube to detect if SSL should be used
		sed -i "s|OVERRIDE_HOSTNAME=.*|OVERRIDE_HOSTNAME=$new_hostname|" "$MAILSERVER_ENV"

        sed -i "/^SSL_TYPE=/c\SSL_TYPE=manual" "$MAILSERVER_ENV"
        if grep -q '^SSL_CERT_PATH=' "$MAILSERVER_ENV"; then
            sed -i "s|^SSL_CERT_PATH=.*|SSL_CERT_PATH=$cert_path_on_hosts|" "$MAILSERVER_ENV"
        else
            echo "SSL_CERT_PATH=$cert_path_on_hosts" >> "$MAILSERVER_ENV"
        fi

        if grep -q '^SSL_KEY_PATH=' "$MAILSERVER_ENV"; then
            sed -i "s|^SSL_KEY_PATH=.*|SSL_KEY_PATH=$key_path_on_hosts|" "$MAILSERVER_ENV"
        else
            echo "SSL_KEY_PATH=$key_path_on_hosts" >> "$MAILSERVER_ENV"
        fi

        # SRS: forwarded mail goes out as SRS0=...@hostname, so it passes SPF at the receiver and bounces come back here.
        # The hostname resolves to this server (it has a certificate); docker-mailserver's default, its parent domain, may not.
        # Only with an accounts.sh that keeps the users' own mail out of SRS, an older one lets postsrsd rewrite all of it.
        if grep -q '_create_srs_exclude_map' /usr/local/mail/openmail/openpanel/accounts.sh 2>/dev/null; then
            sed -i '/^ENABLE_SRS=/c\ENABLE_SRS=1' "$MAILSERVER_ENV"
            if grep -q '^SRS_DOMAINNAME=' "$MAILSERVER_ENV"; then
                sed -i "s|^SRS_DOMAINNAME=.*|SRS_DOMAINNAME=$new_hostname|" "$MAILSERVER_ENV"
            else
                echo "SRS_DOMAINNAME=$new_hostname" >> "$MAILSERVER_ENV"
            fi
        fi

        # podman-compose doesn't interpolate ${VAR:+alt} conditionals like docker compose does, so hardcode the resolved roundcube values in the compose file too
        if [[ -f "$MAILSERVER_COMPOSE_FILE" ]]; then
            sed -i "s|ROUNDCUBEMAIL_DEFAULT_HOST=.*|ROUNDCUBEMAIL_DEFAULT_HOST=ssl://$new_hostname|" "$MAILSERVER_COMPOSE_FILE"
            sed -i "s|ROUNDCUBEMAIL_DEFAULT_PORT=.*|ROUNDCUBEMAIL_DEFAULT_PORT=993|" "$MAILSERVER_COMPOSE_FILE"
            sed -i "s|ROUNDCUBEMAIL_SMTP_SERVER=.*|ROUNDCUBEMAIL_SMTP_SERVER=ssl://$new_hostname|" "$MAILSERVER_COMPOSE_FILE"
            sed -i "s|ROUNDCUBEMAIL_SMTP_PORT=.*|ROUNDCUBEMAIL_SMTP_PORT=465|" "$MAILSERVER_COMPOSE_FILE"
            set_roundcube_extra_host "$new_hostname"
        fi

    fi
    
    log_debug "Restarting mailserver and webmail to apply new configuration"
    nohup sh -c "cd /usr/local/mail/openmail/ && podman-compose down && podman-compose up -d mailserver roundcube" </dev/null >/dev/null 2>&1 &
}


# OpenAdmin's SMTP follows the hostname only when it was pointing at the old one (or the IP), a custom relay or an empty
# setting (OpenAdmin then sends through the local mailserver itself) is left alone
update_admin_smtp_server() {
    local conf="/etc/openpanel/openpanel/conf/openpanel.config" new cur
    [[ -f "$conf" ]] || return 0
    new="$new_hostname"
    [[ "$new_hostname" == "$DEFAULT_DOMAIN" ]] && new="$server_ip"
    cur=$(awk -F= '/^\[SMTP\]/{f=1; next} /^\[/{f=0} f && $1 == "mail_server" {print $2; exit}' "$conf")
    [[ -z "$cur" || "$cur" == "$new" ]] && return 0
    if { [[ "$cur" == "$current_domain" && "$current_domain" != "$DEFAULT_DOMAIN" ]] || [[ -n "$server_ip" && "$cur" == "$server_ip" ]]; }; then
        sed -i "/^\[SMTP\]/,/^\[/ s|^mail_server=.*|mail_server=${new}|" "$conf"
        log_debug "OpenAdmin SMTP server changed from $cur to $new"
    fi
}

restart_services() {

	caddy_reload

    local no_restart=false
    
    for arg in "$@"; do
        if [[ "$arg" == "--no-restart" ]]; then
            no_restart=true
            break
        fi
    done
    
    if [[ "$no_restart" == true ]]; then
        log_debug "Skipping service restart due to --no-restart flag"
        return 0
    fi
    
    log_debug "Restarting OpenPanel and Caddy containers"
    
    cd /root || return 1
    
    podman restart openpanel >/dev/null 2>&1
}

show_success_message() {
    local display_hostname="$new_hostname"
    
    if [[ "$new_hostname" == "$DEFAULT_DOMAIN" ]]; then
        display_hostname="$server_ip"
    fi
    
    log_info "$display_hostname is now set for accessing the OpenPanel and OpenAdmin interfaces."
}

update_domain() {
    current_domain=$(get_current_domain) || return 1
    server_ip=$(get_server_ipv4) || return 1

    log_debug "Current domain: $current_domain"
    log_debug "Server IP: $server_ip"
    log_debug "New hostname: $new_hostname"

    update_caddyfile || return 1
    create_mv_file || return 1
	update_container_hosts_entry "$new_hostname" || return 1
    update_redirects || return 1
    redis_drop_key openpanel_cache_app.get_openpanel_domain
    restart_services "$@" || return 1
    configure_mailserver || return 1
    update_admin_smtp_server
    # users with base_hosts_file get the new domain from here, the rest still from /etc/hosts
    opencli docker-hosts >/dev/null 2>&1
    show_success_message
}

show_usage() {
    echo "Usage:"
    echo ""
    echo "opencli domain                        - displays current URL"
    echo "opencli domain set example.net        - set domain name for access"
    echo "opencli domain ip                     - set IP for access"
    echo ""
    echo "Options:"
    echo "  --debug                             - enable verbose output"
    echo "  --no-restart                        - skip service restart"
}

parse_arguments() {
    for arg in "$@"; do
        if [[ "$arg" == "--debug" ]]; then
            DEBUG=true
            log_debug "'--debug' flag provided: displaying verbose information."
            break
        fi
    done
}


check_domain_not_added_by_user(){

    compare_with_forbidden_domains_list() {
        local CONFIG_FILE_PATH='/etc/openpanel/openpanel/conf/domain_restriction.txt'
        local domain_name="$1"
        local forbidden_domains=()

        if [ -f "$CONFIG_FILE_PATH" ]; then
            log_debug "Checking domain against forbidden_domains list"
            mapfile -t forbidden_domains < "$CONFIG_FILE_PATH"
            local forbidden
            for forbidden in "${forbidden_domains[@]}"; do
                if [[ "$forbidden" == "$domain_name" ]]; then
                    echo "ERROR: $domain_name is a forbidden domain."
                    exit 1
                fi
            done
        fi
    }

if opencli domains-whoowns "$new_hostname" | grep -q "not found in the database."; then
    compare_with_forbidden_domains_list "$new_hostname"
    # todo: also check https://github.com/stefanpejcic/opencli/blob/9bf6ef2567160ac8086652e70aac73e8f7bc03e2/domains/add.sh#L255
else
    echo "ERROR: Domain $new_hostname is already in used by an OpenPanel account."
    exit 1
fi
    
}


# ======================================================================
main() {
    parse_arguments "$@"
    
    case "${1:-}" in
        "")
            get_current_domain_or_ip
            ;;
        "set")
            if [[ -z "$2" ]]; then
                log_error "Domain name required for 'set' command"
                show_usage
                exit 1
            fi
            
            if ! is_valid_domain "$2"; then
                log_error "Invalid domain format. Please provide a valid domain."
                show_usage
                exit 1
            fi
            
            new_hostname="$2"
            check_domain_not_added_by_user
            log_debug "Setting domain to: $new_hostname"
            update_domain "$@"
            ;;
        "ip")
            new_hostname="$DEFAULT_DOMAIN"
            log_debug "Setting IP address for accessing panels"
            update_domain "$@"
            ;;
        *)
            show_usage
            exit 1
            ;;
    esac
}

main "$@"
exit $?
