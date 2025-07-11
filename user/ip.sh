#!/bin/bash
################################################################################
# Script Name: user/ip.sh
# Description: Assing or remove dedicated IP to a user.
# Usage: opencli user-ip <USERNAME> <IP | DELETE> [-y] [--debug]
# Author: Radovan Jecmenica
# Created: 23.11.2023
# Last Modified: 11.07.2025
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

USERNAME=$1
ACTION=$2
CONFIRM_FLAG=$3
DEBUG=false

JSON_FILE_BASE="/etc/openpanel/openpanel/core/users"
CADDY_CONF_PATH="/etc/openpanel/caddy/domains"
ZONE_FILE="/etc/bind/zones"
SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
ALLOWED_IP_ADDRESSES=$(hostname -I | tr ' ' '\n' | grep -v '^172\.' | tr '\n' ' ')

# Parse flags
for arg in "$@"; do
    case $arg in
        --debug) DEBUG=true ;;
    esac
done

if [ -z "$USERNAME" ]; then
    echo "Usage: opencli user-ip <USERNAME> <ACTION> [ -y ] [--debug]"
    echo ""
    echo "Assign Dedicated IP to a user: opencli user-ip <USERNAME> <IP_ADDRESS> [ -y ] [--debug]"
    echo "Remove Dedicated IP from user: opencli user-ip <USERNAME> delete [ -y ] [--debug]"
    exit 1
fi

ensure_jq_installed() {
    if ! command -v jq &> /dev/null; then
        if command -v apt-get &> /dev/null; then
            sudo apt-get update -qq && sudo apt-get install -y -qq jq
        elif command -v yum &> /dev/null; then
            sudo yum install -y -q jq
        elif command -v dnf &> /dev/null; then
            sudo dnf install -y -q jq
        else
            echo "Error: No compatible package manager found. Please install jq manually."
            exit 1
        fi
        command -v jq &> /dev/null || { echo "jq installation failed."; exit 1; }
    fi
}

check_ip_validity() {
    local ip=$1
    if ! echo "$ALLOWED_IP_ADDRESSES" | grep -qw "$ip"; then
        echo "Error: The provided IP address is not allowed. Must be one of: $ALLOWED_IP_ADDRESSES"
        exit 1
    fi
}

check_ip_usage() {
    local ip=$1
    local all_users
    all_users=$(ls "$JSON_FILE_BASE")
    for user in $all_users; do
        [ "$user" = "$USERNAME" ] && continue
        local user_json="$JSON_FILE_BASE/$user/ip.json"
        if [ -f "$user_json" ]; then
            local user_ip
            user_ip=$(jq -r '.ip' "$user_json")
            if [ "$user_ip" = "$ip" ]; then
                if [ "$CONFIRM_FLAG" != "-y" ]; then
                    echo "Error: IP $ip already assigned to user $user."
                    read -p "Are you sure you want to continue? (y/n): " answer
                    [[ "$answer" != "y" ]] && echo "Script aborted." && exit 1
                fi
            fi
        fi
    done
}

delete_ip_config() {
    local json_file="$JSON_FILE_BASE/$USERNAME/ip.json"
    if [ -f "$json_file" ]; then
        rm -f "$json_file"
        echo "IP configuration deleted for user $USERNAME."
    fi
}

get_current_ip() {
    local json_file="$JSON_FILE_BASE/$USERNAME/ip.json"
    if [ -f "$json_file" ]; then
        jq -r '.ip' "$json_file"
    else
        echo "$SERVER_IP"
    fi
}


escape_sed_regex() {
  local str=$1
  # escape \ / ^ $ . | ? * + ( ) [ ] { }
  printf '%s' "$str" | sed -e 's/[\/&^$.*[]/\\&/g'
}

update_bind_in_block() {
  local conf=$1
  local block_header=$2
  local ip=$3

  local esc_block_header
  esc_block_header=$(escape_sed_regex "$block_header")

  # Check if bind exists immediately after block_header
  if sed -n "/^$esc_block_header/{n;/^[[:space:]]*bind /p}" "$conf" | grep -q "bind "; then
    # Replace bind line
    sed -i "/^$esc_block_header/{n;s/^[[:space:]]*bind .*/    bind $ip/}" "$conf"
  else
    # Insert bind line after block_header
    sed -i "/^$esc_block_header/a\    bind $ip" "$conf"
  fi
}


update_caddy_conf() {
    local ip=$1
    local domains
    domains=$(opencli domains-user "$USERNAME")

    for domain in $domains; do
        local conf="$CADDY_CONF_PATH/$domain.conf"
        if [ -f "$conf" ]; then
            update_bind_in_block "$conf" "http://$domain, http://*.$domain {" "$ip"
            update_bind_in_block "$conf" "https://$domain, https://*.$domain {" "$ip"

            $DEBUG && echo "Updated Caddy bind for $domain to $ip"
        fi
    done

    docker --context=default exec caddy bash -c "caddy validate && caddy reload" >/dev/null 2>&1
}


update_dns_zone_file() {
    local ip=$1
    local domains
    domains=$(opencli domains-user "$USERNAME")
    local current_ip
    current_ip=$(get_current_ip)

    for domain in $domains; do
        local zone_conf="$ZONE_FILE/$domain.zone"
        if [ -f "$zone_conf" ]; then
            sed -i "s/$current_ip/$ip/g" "$zone_conf"
            $DEBUG && echo "Updated DNS zone file $zone_conf with IP $ip"
        fi
    done
}

create_ip_file() {
    local ip=$1
    local json_file="$JSON_FILE_BASE/$USERNAME/ip.json"
    echo "{ \"ip\": \"$ip\" }" > "$json_file"
    $DEBUG && echo "Created IP file $json_file with IP $ip"
}

update_firewall_rules() {
    # Placeholder for firewall rules logic
    if command -v csf &> /dev/null; then
        :
        # TODO: implement firewall updates for dedicated IP
    else
        echo "Warning: CSF is not installed; user ports may be exposed without protection."
    fi
}

ensure_jq_installed

if [ "$ACTION" = "delete" ]; then
    delete_ip_config
    ip_to_use="$SERVER_IP"
else
    IP=$2
    if [ -z "$IP" ]; then
        current_ip=$(get_current_ip)
        echo $current_ip
        exit 0
    fi

    check_ip_validity "$IP"
    check_ip_usage "$IP"
    create_ip_file "$IP"
    ip_to_use="$IP"
fi

update_caddy_conf "$ip_to_use"
update_firewall_rules
update_dns_zone_file "$ip_to_use"

if [ $? -eq 0 ]; then
    echo "IP successfully changed for user $USERNAME to: $ip_to_use"
else
    echo "Error: Data insertion failed."
    exit 1
fi
