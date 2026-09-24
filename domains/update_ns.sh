#!/bin/bash
################################################################################
# Script Name: domains/update_ns.sh
# Description: Change nameservers for a single or all dns zones.
# Usage: opencli domains-update_ns <DOMAIN_NAME | --all> [-y]
#        opencli domains-update_ns --all
# Author: Stefan Pejcic
# Created: 20.08.2023
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


CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
ZONE_DIR="/etc/bind/zones"
BACKUP_DIR="$ZONE_DIR/backups"

print_usage() {
  echo "Usage: $0 [--all | --all -y | domain_name]"
  echo "Options:"
  echo "  --all       Update all zone files."
  echo "  --all -y    Update all zone files without confirmation."
  echo "  domain_name Update the zone file for the specified domain."
  exit 1
}

backup_zone_file() {
  local zone_file="$1"
  mkdir -p "$BACKUP_DIR"
  cp "$zone_file" "$BACKUP_DIR/$(basename "$zone_file").bak"
}

update_zone_file() {
  local zone_file="$1"
  local tmp_file; tmp_file=$(mktemp)

  backup_zone_file "$zone_file"

  # strip out existing NS records
  grep -v '^[^;].*IN[[:space:]]*NS[[:space:]]*' "$zone_file" > "$tmp_file"

  # append the new ones
  for ns in "${NAMESERVERS[@]}"; do
    echo "@ IN NS $ns." >> "$tmp_file"
  done

  cat "$tmp_file" > "$zone_file"
  rm "$tmp_file"
}

confirm_all() {
  echo "You are about to update all zone files. This action cannot be undone."
  echo "Proceed? (y/n)"
  read -t 10 -r confirm
  if [[ $confirm != "y" ]]; then
    echo "Operation aborted."
    exit 1
  fi
}

if [ ! -f "$CONFIG_FILE" ]; then
  echo "Configuration file not found: $CONFIG_FILE"
  exit 1
fi

# pull nameserver values from the config
NS1=$(grep '^ns1=' "$CONFIG_FILE" | cut -d'=' -f2)
NS2=$(grep '^ns2=' "$CONFIG_FILE" | cut -d'=' -f2)
NS3=$(grep '^ns3=' "$CONFIG_FILE" | cut -d'=' -f2)
NS4=$(grep '^ns4=' "$CONFIG_FILE" | cut -d'=' -f2)

if [ -z "$NS1" ] || [ -z "$NS2" ]; then
  echo "ns1 and ns2 must be set in the configuration file."
  exit 1
fi

NAMESERVERS=("$NS1" "$NS2")
[ -n "$NS3" ] && NAMESERVERS+=("$NS3")
[ -n "$NS4" ] && NAMESERVERS+=("$NS4")

case "$1" in
  --all)
    if [ "$2" != "-y" ]; then
      confirm_all
    fi
    for zone_file in "$ZONE_DIR"/*.zone; do
      if [ -f "$zone_file" ]; then
        update_zone_file "$zone_file"
      fi
    done
    ;;
    
  "")
    print_usage
    ;;

  *)
    domain="$1"
    zone_file="$ZONE_DIR/$domain.zone"
    if [ -f "$zone_file" ]; then
      update_zone_file "$zone_file"
    else
      echo "Zone file for $domain not found: $zone_file"
      exit 1
    fi
    ;;
esac


podman exec openpanel_dns rndc reconfig >/dev/null 2>&1
cd /root && podman-compose up -d bind9  >/dev/null 2>&1

echo "Nameservers have been updated and BIND9 zones reloaded."
exit 0
