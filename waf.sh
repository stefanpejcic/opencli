#!/bin/bash
################################################################################
# Script Name: waf.sh
# Description: Manage CorazaWAF
# Usage: opencli waf <setting> 
# Author: Stefan Pejcic
# Created: 22.05.2025
# Last Modified: 22.05.2025
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


# Display usage information
usage() {
    echo "Usage: opencli waf <command> [options]"
    echo ""
    echo "Commands:"
    echo "  status                                        Check if CorazaWAF is enabled for new domains and users."
    echo "  domain                                        Check if CorazaWAF is enabled for a domain."
    echo "  domain DOMAIN_NAME enable                     Enable CorazaWAF for a domain."
    echo "  domain DOMAIN_NAME disable                    Disable CorazaWAF for a domain."
    echo ""
    echo "Examples:"
    echo "  opencli waf status"
    echo "  opencli waf domain pcx3.com"
    echo "  opencli waf domain pcx3.com enable"
    echo "  opencli waf domain pcx3.com disable"
    exit 1
}



check_domain() {
    local domain="$1"
    local file="/hostfs/etc/openpanel/caddy/domains/${domain}.conf"
    
    if [[ ! -f "$file" ]]; then
        echo "Domain not found!"
        exit 1
    fi

    if grep -iq '^[[:space:]]*SecRuleEngine[[:space:]]\+On' "$file"; then
        echo "SecRuleEngine is set to On for domain $domain"
    elif grep -iq '^[[:space:]]*SecRuleEngine[[:space:]]\+Off' "$file"; then
        echo "SecRuleEngine is set to Off for domain $domain"
    else
        echo "SecRuleEngine is not set for domain $domain"
    fi
}

check_coraza_status() {
  local env_file="/hostfs/root/.env"
  local custom_image='CADDY_IMAGE="openpanel/caddy-coraza"'
  
  if grep -q "^$custom_image" "$env_file"; then
      echo "CorazaWAF is ENABLED"
  else
       echo "CorazaWAF is DISABLED"
  fi
}

reload_caddy_now() {
    docker --context=default exec caddy caddy reload --config /etc/caddy/Caddyfile > /dev/null 2>&1
}

enable_coraza_waf_for_domain() {
    local domain="$1"
    local file="/hostfs/etc/openpanel/caddy/domains/${domain}.conf"
    
    if [[ ! -f "$file" ]]; then
        echo "Domain not found!"
        exit 1
    fi

    sed -i 's/SecRuleEngine Off/SecRuleEngine On/g' "$file"
    
    if [[ $? -eq 0 ]]; then
        echo "SecRuleEngine On is now set for domain $domain"
        reload_caddy_now
    else
        echo "Failed setting SecRuleEngine On - please contact Administrator."
        exit 1
    fi
}

disable_coraza_waf_for_domain() {
    local domain="$1"
    local file="/hostfs/etc/openpanel/caddy/domains/${domain}.conf"
    
    if [[ ! -f "$file" ]]; then
        echo "Domain not found!"
        exit 1
    fi

    sed -i 's/SecRuleEngine On/SecRuleEngine Off/g' "$file"
    
    if [[ $? -eq 0 ]]; then
        echo "SecRuleEngine Off is now set for domain $domain"
        reload_caddy_now
    else
        echo "Failed setting SecRuleEngine Off - please contact Administrator."
        exit 1
    fi
}

# MAIN
case "$1" in
    "status")
        check_coraza_status
        ;;
    "domain")
        if [[ -z "$2" ]]; then
            echo "Domain name is required."
            usage
            exit 1
        fi
        case "$3" in
            "enable")
                enable_coraza_waf_for_domain "$2"
                ;;
            "disable")
                disable_coraza_waf_for_domain "$2"
                ;;
            "")
                check_domain "$2"
                ;;
            *)
                echo "Invalid action for domain: $3"
                usage
                exit 1
                ;;
        esac
        ;;
    "enable")
        enable_coraza_waf
        ;;
    "disable")
        disable_coraza_waf
        ;;
    "help")
        usage
        ;;
    *)
        echo "Invalid option: $1"
        usage
        exit 1
        ;;
esac

exit 0
