#!/bin/bash
################################################################################
# Script Name: domains/ssl.sh
# Description: Check SSL for domain, add custom certificate, view files.
# Usage: opencli domains-ssl <DOMAIN_NAME> [status|info|auto|custom] [path/to/fullchain.pem path/to/key.pem]
# Author: Stefan Pejcic
# Created: 22.03.2025
# Last Modified: 22.03.2025
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




# Ensure a domain name is provided
if [ -z "$1" ]; then
    echo "Usage: opencli domains-ssl <domain> [status|info]auto|custom] [cert_path key_path]"
    exit 1
fi

DOMAIN="$1"
CONFIG_FILE="/etc/openpanel/caddy/domains/$DOMAIN.conf"

# Ensure the file exists
if [ ! -f "$CONFIG_FILE" ]; then
    echo "Domain does not exist."
    exit 1
fi


hostfs_domain_tls_dir="/hostfs/etc/openpanel/caddy/ssl/$DOMAIN"
domain_tls_dir="/hostfs/etc/openpanel/caddy/ssl/$DOMAIN"


check_and_use_tls() {

	if openssl x509 -noout -in "$3" >/dev/null 2>&1; then
		    
	    mkdir -p $domain_tls_dir
	    
	    cp /hostfs{$3} $hostfs_domain_tls_dir/fullchain.pem
	    cp /hostfs{$4} $hostfs_domain_tls_dir/key.pem
	    
		if grep -qE "tls\s+/.*?/fullchain\.pem\s+/.*?/key\.pem" "$CONFIG_FILE"; then
		    echo "Custom SSL already configured for $DOMAIN. Updating certificate and key.."
		    
		    sed -i -E "s|tls\s+/.*?/fullchain\.pem\s+/.*?/key\.pem|tls $domain_tls_dir/fullchain.pem $domain_tls_dir/key.pem|g" "$CONFIG_FILE"
		else
		    echo "Adding custom certificate.."
		    sed -i -E "s|tls\s*{\s*on_demand\s*}|tls $domain_tls_dir/fullchain.pem $domain_tls_dir/key.pem|g" "$CONFIG_FILE"
		fi	    
	    
	    
	    sed -i -E "s|tls\s*{\s*on_demand\s*}|tls $domain_tls_dir/fullchain.pem $domain_tls_dir/key.pem|g" "$CONFIG_FILE"
	    docker --context default caddy caddy reload >/dev/null
	else
	    echo "Error: $3 is not a valid SSL certificate file."
	    exit 1
	fi
}


cat_certificate_files() {
    	if grep -qE "tls\s+/.*?/fullchain\.pem\s+/.*?/key\.pem" "$CONFIG_FILE"; then
    		cat $hostfs_domain_tls_dir/fullchain.pem
    		cat $hostfs_domain_tls_dir/key.pem
    	else
    		cat /hostfs/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/$DOMAIN/$DOMAIN.crt
	    	cat /hostfs/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/$DOMAIN/$DOMAIN.key
    	fi
}


show_examples() {
	echo "Usage:"
	echo ""
	echo "Check current SSL status for domain (AutoSSL, CustomSSL or No SSL):"
	echo ""
	echo "opencli domains-ssl $DOMAIN status"
	echo ""
	echo "Display fullchain and key files for the domain:"
	echo ""
	echo "opencli domains-ssl $DOMAIN info"
	echo ""
	echo "Set free AutoSSL for the domain (default):"
	echo ""
	echo "opencli domains-ssl $DOMAIN auto"
	echo ""
	echo "Add custom certificate files for the domain:"
	echo ""
	echo "opencli domains-ssl $DOMAIN custom path/to/fullchain.pem path/to/key.pem"
	echo ""
}


if [ -n "$2" ]; then
    if [ "$2" == "info" ]; then
	cat_certificate_files
	exit 0
    elif [ "$2" == "status" ]; then
    	check_custom_ssl_or_auto
    	exit 0
    elif [ "$2" == "auto" ]; then
        sed -i -E "s|tls\s+/.*?/fullchain\.pem\s+/.*?/key\.pem|  tls {\n    on_demand\n  }|g" "$CONFIG_FILE"
        echo "Updated $DOMAIN to use AutoSSL"
        exit 0
    elif [ "$2" == "custom" ] && [ -n "$3" ] && [ -n "$4" ]; then        
        check_and_use_tls
        echo "Updated $DOMAIN to use custom SSL with cert: $3 and key: $4"
        exit 0
    else
        echo "Invalid arguments. Usage: opencli domains-ssl <domain> [auto|custom] [cert_path key_path]"
        exit 1
    fi
else
	show_examples
	exit 0

fi


exit 0
