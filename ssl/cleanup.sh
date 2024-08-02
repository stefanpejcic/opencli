#!/bin/bash
################################################################################
# Script Name: ssl/cleanup.sh
# Description: Delete all unused certificates.
# Usage: opencli ssl-cleanup [-y]
# Author: Stefan Pejcic
# Created: 02.08.2024
# Last Modified: 02.08.2024
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

nginx_sites_enabled_path="/etc/nginx/sites-enabled"
server_fqdn=$(hostname --fqdn)

nginx_domains=()
for conf in "$nginx_sites_enabled_path"/*; do
    domain=$(basename "$conf" .conf)
    if [ -n "$domain" ]; then
        nginx_domains+=("$domain")
    fi
done

certbot_domains=$(certbot certificates 2>/dev/null | grep 'Domains:' | awk '{print $2}' | tr ',' '\n')

delete_certbot_certificate() {
    domain=$1
    echo "[✖] Deleting SSL certificate for domain: $domain"
    certbot delete --cert-name "$domain" --non-interactive
}

delete_flag=false
if [ "$1" == "-y" ]; then
    delete_flag=true
fi

for cert_domain in $certbot_domains; do
    if [[ "$cert_domain" == "$server_fqdn" ]]; then
        echo "[!] Skipping server FQDN: $cert_domain"
        continue
    fi

    if ! [[ " ${nginx_domains[*]} " == *" $cert_domain "* ]]; then
        if $delete_flag; then
            delete_certbot_certificate "$cert_domain"
        else
            echo "[✖] DRY-RUN: would delete SSL certificate for domain: $cert_domain"
        fi
    else
        echo "[✔] Nginx configuration found for Certbot domain: $cert_domain"
    fi
done

echo "Finished cleaning certificates."
