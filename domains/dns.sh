#!/bin/bash
################################################################################
# Script Name: domains/dns.sh
# Description: Parse nginx access logs for users domains and generate static html
# Usage: opencli domains-dns <DOMAIN>
# Author: Stefan Pejcic
# Created: 31.08.2024
# Last Modified: 31.08.2024
# Company: openpanel.co
# Copyright (c) openpanel.co
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

# COLORS
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
RESET='\033[0m'

usage() {
  command="opencli domains-dns"
  echo "Usage:"
  echo -e " ${GREEN}$command reconfig${RESET}               - Load new DNS zones into bind server."
  echo -e " ${GREEN}$command check <DOMAIN>${RESET}         - Check and validate dns zone for a domain."
  echo -e " ${GREEN}$command reload <DOMAIN>${RESET}        - Reload DNS zone for a single domain."
  echo -e " ${GREEN}$command config${RESET}                 - Check main bind configuration file for syntax errros."
  echo -e " ${GREEN}$command start${RESET}                  - Start the DNS server."
  echo -e " ${GREEN}$command restart${RESET}                - Soft restart of bind9 docker container."
  echo -e " ${GREEN}$command hard-restart${RESET}           - Hard restart: terminates container and start again."
  echo -e " ${GREEN}$command stop${RESET}                   - Stop the DNS server."
  exit 1
}

# Check if at least one argument is provided
if [ "$#" -lt 1 ]; then
    usage
fi











######## START MAIN FUNCTION #######



reconfig_command(){
  echo "Loading new DNS zones.."
  docker exec openpanel_dns rndc reconfig
}



check_named_main_conf(){
  echo "Checking /etc/bind/named.conf configuration:"
  docker exec openpanel_dns named-checkconf  /etc/bind/named.conf
}



reload_single_dns_zone(){
  DOMAIN=$1
  if [[ -n "$DOMAIN" ]]; then
    echo "Reloading DNS zone for domain: $DOMAIN"
    docker exec openpanel_dns rndc reload $DOMAIN
  else
    echo "Reloading all DNS zones.."
    docker exec openpanel_dns rndc reload
  fi
  exit 0
}


check_single_dns_zone(){
  DOMAIN=$1
  if [[ -n "$DOMAIN" ]]; then
    echo "Checking DNS zone for domain: $DOMAIN"
  fi
  docker exec openpanel_dns named-checkzone  $DOMAIN /etc/bind/zones/$DOMAIN.zone
  exit 0
}

start_dns_server(){
  echo "Starting DNS service.."
  cd /root && docker compose up -d bind9
  exit 0
}

stop_dns_server(){
  echo "Stopping DNS service.."
  docker stop openpanel_dns && docker rm openpanel_dns
  exit 0
}



soft_reset(){
  echo "Restarting DNS service.."
  docker restart openpanel_dns
  exit 0
}


hard_reset(){
  stop_dns_server
  start_dns_server
  exit 0
}



######## END MAIN FUNCTIONS ########













while [[ $# -gt 0 ]]; do
  case $1 in
    reconfig)
      reconfig_command
      shift
      ;;
    check)
      check_single_dns_zone "$2"
      shift 2
      ;;
    reload)
      reload_single_dns_zone "$2"
      shift 2
      ;; 
    restart)
      soft_reset
      shift
      ;;
    config)
      check_named_main_conf
      shift
      ;;
    start)
      start_dns_server
      shift
      ;;
    stop)
      stop_dns_server
      shift
      ;;
    hard-restart)
      hard_reset
      shift
      ;;
    *)
      if [[ -z "$DOMAIN" ]]; then
        DOMAIN=$1
      else
        echo "Unknown option: $1"
        usage
      fi
      shift
      ;;
  esac
done
