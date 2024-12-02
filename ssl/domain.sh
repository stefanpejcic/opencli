#!/bin/bash
################################################################################
# Script Name: ssl/domain.sh
# Description: Generate or Delete SSL for a domain.
# Usage: opencli ssl-domain [-d] <domain_url> [-k path -p path]
# Author: Radovan Ječmenica
# Created: 27.11.2023
# Last Modified: 12.09.2024
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

# Initialize variables
DRY_RUN=0
delete_flag=false
SSL_PRIVATE_KEY_PATH=""
SSL_PUBLIC_KEY_PATH=""
DEBUG=0

print_usage() {
    echo "Usage: opencli ssl-domain [-d] <domain_url> [-k <key_path> -p <cert_path>]"
    echo ""
    echo " opencli ssl-domain <domain_url>                                 Generate and use SSL for the specified domain"
    echo " opencli ssl-domain <domain_url> --dry-run                       Use 'dry-run' flag for certbot and simulate generating ssl."
    echo " opencli ssl-domain <domain_url> -k <key_path> -p <cert_path>    Add custom SSL certificate and enable https"
    echo " opencli ssl-domain -d <domain_url>                              Delete SSL and disable https for domain"
    echo ""
    
}

get_server_ip() {
    domain_url=$1
    result=$(opencli domains-whoowns $domain_url)

    if [[ $result == *"Owner of"* ]]; then
        username=$(echo $result | awk '{print $NF}')
    else
        echo "Domain does not exist on the server or MySQL service is down: $result"
        exit 1
    fi

    current_username=$username
    dedicated_ip_file_path="/etc/openpanel/openpanel/core/users/{current_username}/ip.json"

    if [ -e "$dedicated_ip_file_path" ]; then
        # If the file exists, read the IP from it
        server_ip=$(jq -r '.ip' "$dedicated_ip_file_path" 2>/dev/null)
    else
        # Try to get the server's IP using the hostname -I command
        server_ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    fi
    #echo $server_ip
}



ensure_jq_installed() {
    # Check if jq is installed
    if ! command -v jq &> /dev/null; then
        # Detect the package manager and install jq
        if command -v apt-get &> /dev/null; then
            sudo apt-get update > /dev/null 2>&1
            sudo apt-get install -y -qq jq > /dev/null 2>&1
        elif command -v yum &> /dev/null; then
            sudo yum install -y -q jq > /dev/null 2>&1
        elif command -v dnf &> /dev/null; then
            sudo dnf install -y -q jq > /dev/null 2>&1
        else
            echo "Error: No compatible package manager found. Please install jq manually and try again."
            exit 1
        fi

        # Check if installation was successful
        if ! command -v jq &> /dev/null; then
            echo "Error: jq installation failed. Please install jq manually and try again."
            exit 1
        fi
    fi
}


import_ssl(){

    # Verify SSL certificate
    echo "Verifying SSL certificate for domain: $DOMAIN_NAME"
    if ! openssl x509 -noout -modulus -in "$SSL_PUBLIC_KEY_PATH" | openssl md5 > /dev/null; then
        echo "ERROR: Invalid SSL public certificate."
        exit 1
    fi
    
    if ! openssl rsa -noout -modulus -in "$SSL_PRIVATE_KEY_PATH" | openssl md5 > /dev/null; then
        echo "ERROR: Invalid SSL private key."
        exit 1
    fi
    
    CERT_MODULUS=$(openssl x509 -noout -modulus -in "$SSL_PUBLIC_KEY_PATH" | openssl md5)
    KEY_MODULUS=$(openssl rsa -noout -modulus -in "$SSL_PRIVATE_KEY_PATH" | openssl md5)
    
    if [ "$CERT_MODULUS" != "$KEY_MODULUS" ]; then
        echo "ERROR: The SSL certificate and key do not match."
        exit 1
    fi
    
    # Copy the SSL files to the directory
    SSL_DIR="/etc/nginx/ssl/$DOMAIN_NAME"
    mkdir -p "$SSL_DIR"
    cp "$SSL_PRIVATE_KEY_PATH" "$SSL_DIR/privkey.pem"
    cp "$SSL_PUBLIC_KEY_PATH" "$SSL_DIR/fullchain.pem"
    
    
    
    # check if domain already has ssl in nginx conf file
    marker_for_letsencrypt="ssl_certificate_key /etc/letsencrypt/live/$DOMAIN_NAME/privkey.pem;"
    marker_for_custom_ssl="ssl_certificate_key /etc/nginx/ssl/$DOMAIN_NAME/privkey.pem;"
    nginx_conf_path="/etc/nginx/sites-available/$DOMAIN_NAME.conf"
    
    
    if grep -qF "$marker_for_custom_ssl" "$nginx_conf_path"; then
        echo "Custom SSL certificate already in use by the domain. Removing and re-adding configuration again..."
    elif grep -qF "$marker_for_letsencrypt" "$nginx_conf_path"; then
        echo "Let's Encrypt SSL certificate already in use by the domain. Editing the configuration to use custom SSL instead..."
    else
        echo "No existing SSL configuration found for the domain. Editing the configuration to use custom SSL..."
    fi
    
    revert_nginx_conf "$DOMAIN_NAME" # remove existing ssl 

    # after deleting ssl from conf, we down do modify_nginx_conf to add the ssl!

}


generate_ssl_with_http01() {
    domain_url=$1

    mkdir -p /home/${username}/${domain_url}/.well-known/acme-challenge
    chown -R 1000:33 /home/${username}/${domain_url}/.well-known 
    chmod 755 -R /home/${username}/${domain_url}/.well-known/
    
            if [ -e "/etc/letsencrypt/archive/$domain_url.pem" ]; then
                echo "Skipping file-based verification because certificate exists."
            else
                echo "Retrying SSL generation for ${domain_url} using file-based verification:"
                
                # FILE VALIDATION
                if [[ $DRY_RUN -eq 1 ]]; then
                certbot_command=(
                    "docker" "run" "--rm" "--network" "host"
                    "-v" "/etc/letsencrypt:/etc/letsencrypt"
                    "-v" "/var/lib/letsencrypt:/var/lib/letsencrypt"
                    "-v" "/etc/nginx/sites-available:/etc/nginx/sites-available"
                    "-v" "/etc/nginx/sites-enabled:/etc/nginx/sites-enabled"
                    "-v" "/home/${username}/${domain_url}/:/home/${username}/${domain_url}/"
                    "certbot/certbot" "certonly" "--preferred-challenges=http"
                    "--webroot" "--webroot-path=/home/${username}/${domain_url}/"
                    "--non-interactive" "--agree-tos"
                    "-m" "webmaster@${domain_url}" "-d" "${domain_url}" "--dry-run" "-v"
                )
                else
                
                certbot_command=(
                    "docker" "run" "--rm" "--network" "host"
                    "-v" "/etc/letsencrypt:/etc/letsencrypt"
                    "-v" "/var/lib/letsencrypt:/var/lib/letsencrypt"
                    "-v" "/etc/nginx/sites-available:/etc/nginx/sites-available"
                    "-v" "/etc/nginx/sites-enabled:/etc/nginx/sites-enabled"
                    "-v" "/home/${username}/${domain_url}/:/home/${username}/${domain_url}/"
                    "certbot/certbot" "certonly" "--preferred-challenges=http"
                    "--webroot" "--webroot-path=/home/${username}/${domain_url}/"
                    "--non-interactive" "--agree-tos"
                    "-m" "webmaster@${domain_url}" "-d" "${domain_url}"
                )
                fi

                # Run Certbot command
                "${certbot_command[@]}"
                status=$?
        
                #rm dir eitherway
                rm -rf /home/${username}/${domain_url}/.well-known/


                if [ $status -eq 0 ]; then
                    echo "SSL generation for ${domain_url} completed successfully using file verification!"


                    # Attempt to request certificate for both www and non-www domains
                    docker run --rm --network host \
                        -v /etc/letsencrypt:/etc/letsencrypt \
                        -v /var/lib/letsencrypt:/var/lib/letsencrypt \
                        -v /etc/nginx/sites-available:/etc/nginx/sites-available \
                        -v /etc/nginx/sites-enabled:/etc/nginx/sites-enabled \
                        -v /home/${username}/${domain_url}/:/home/${username}/${domain_url}/ \
                        certbot/certbot certonly --webroot \
                        --webroot-path=/home/${username}/${domain_url}/ \
                        --non-interactive --agree-tos \
                        -m webmaster@${domain_url} --expand -d "${domain_url},www.${domain_url}"
                        
                    if [ $? -eq 0 ]; then
                        echo "Certificate request using file verification for both ${domain_url} and www.${domain_url} successful."
                    else
                        echo "Combined certificate request failed. Non-www certificate is still valid."
                    fi
                else
                    echo "SSL generation for ${domain_url} using file verification failed with exit status $status"
                    exit 1
                fi
            fi
}            




# Function to generate SSL
generate_ssl_with_dns01() {
    domain_url=$1

    echo "Generating SSL for domain: $domain_url"

    echo "Downloading acme-dns-auth.py"
    rm -rf /etc/letsencrypt/acme-dns-auth.py > /dev/null 2>&1 # if downlaod failed and docker -v created it as a directory..
    wget -q -O /etc/letsencrypt/acme-dns-auth.py https://raw.githubusercontent.com/stefanpejcic/acme-dns-certbot-openpanel/master/acme-dns-auth.py
    chmod +x /etc/letsencrypt/acme-dns-auth.py
        
    # DNS VALIDATION
                if [[ $DRY_RUN -eq 1 ]]; then
        certbot_command=(
            "docker" "run" "--rm" "--network" "host"
            "-v" "/etc/letsencrypt:/etc/letsencrypt"
            "-v" "/var/lib/letsencrypt:/var/lib/letsencrypt"
            "-v" "/etc/bind/zones/${domain_url}.zone:/etc/bind/zones/${domain_url}.zone"
            "-v" "/etc/letsencrypt/acme-dns-auth.py:/etc/letsencrypt/acme-dns-auth.py"
            "certbot/certbot" "certonly" "--manual"
            "--manual-auth-hook" "/etc/letsencrypt/acme-dns-auth.py"
            "--preferred-challenges" "dns" "--debug-challenges"
            "--non-interactive" "--agree-tos"
            "-m" "webmaster@${domain_url}" "-d" "${domain_url}" "--dry-run" "-v"
        )
        else
                    dry_flag=''
                fi
        

        certbot_command=(
            "docker" "run" "--rm" "--network" "host"
            "-v" "/etc/letsencrypt:/etc/letsencrypt"
            "-v" "/var/lib/letsencrypt:/var/lib/letsencrypt"
            "-v" "/etc/bind/zones/${domain_url}.zone:/etc/bind/zones/${domain_url}.zone"
            "-v" "/etc/letsencrypt/acme-dns-auth.py:/etc/letsencrypt/acme-dns-auth.py"
            "certbot/certbot" "certonly" "--manual"
            "--manual-auth-hook" "/etc/letsencrypt/acme-dns-auth.py"
            "--preferred-challenges" "dns" "--debug-challenges"
            "--non-interactive" "--agree-tos"
            "-m" "webmaster@${domain_url}" "-d" "${domain_url}"
        )        

    # Run Certbot for domain and wildcard subdomain
    "${certbot_command[@]}"
    status=$?

    # Check if the Certbot command was successful
    if [ $status -eq 0 ]; then
        echo "SSL generation for ${domain_url} completed successfully using DNS verification! Trying to generate SSL for wildcard subdomain *.${domain_url}"
                if [[ $DRY_RUN -eq 1 ]]; then
                    certbot_command=(
                        "docker" "run" "--rm" "--network" "host"
                        "-v" "/etc/letsencrypt:/etc/letsencrypt"
                        "-v" "/var/lib/letsencrypt:/var/lib/letsencrypt"
                        "-v" "/etc/bind/zones/${domain_url}.zone:/etc/bind/zones/${domain_url}.zone"
                        "-v" "/var/run/docker.sock:/var/run/docker.sock"
                        "-v" "/etc/letsencrypt/acme-dns-auth.py:/etc/letsencrypt/acme-dns-auth.py"
                        "certbot/certbot" "certonly" "--manual"
                        "--manual-auth-hook" "/etc/letsencrypt/acme-dns-auth.py"
                        "--preferred-challenges" "dns" "--debug-challenges"
                        "--non-interactive" "--agree-tos"
                        "-m" "webmaster@${domain_url}" "-d" "*.${domain_url}" "--dry-run" "-v"
                    )
            else
                certbot_command=(
                    "docker" "run" "--rm" "--network" "host"
                    "-v" "/etc/letsencrypt:/etc/letsencrypt"
                    "-v" "/var/lib/letsencrypt:/var/lib/letsencrypt"
                    "-v" "/etc/bind/zones/${domain_url}.zone:/etc/bind/zones/${domain_url}.zone"
                    "-v" "/var/run/docker.sock:/var/run/docker.sock"
                    "-v" "/etc/letsencrypt/acme-dns-auth.py:/etc/letsencrypt/acme-dns-auth.py"
                    "certbot/certbot" "certonly" "--manual"
                    "--manual-auth-hook" "/etc/letsencrypt/acme-dns-auth.py"
                    "--preferred-challenges" "dns" "--debug-challenges"
                    "--non-interactive" "--agree-tos"
                    "-m" "webmaster@${domain_url}" "-d" "*.${domain_url}"
                )
            fi
                
        # Run Certbot for domain and wildcard subdomain
        "${certbot_command[@]}"
        status=$?
        # Check if the Certbot command was successful
        if [ $status -eq 0 ]; then
            echo "SSL generation for wildcard subdomain *.${domain_url} completed successfully using DNS verification!"
        else
            echo "SSL generation for wildcard subdomain *.${domain_url} using DNS verification failed with exit status $status"
            generate_ssl_with_http01 "${domain_url}"
        fi
    else
        generate_ssl_with_http01 "${domain_url}"
    fi
}










# Function to modify Nginx configuration
modify_nginx_conf() {
    domain_url=$1
    type="$2"
    
    # Nginx configuration path
    nginx_conf_path="/etc/nginx/sites-available/$domain_url.conf"


    
if [[ $DRY_RUN -eq 1 ]]; then
    echo "Skip editing nginx conf because of --dry-run" flag.
else
    echo "Modifying Nginx configuration for domain: $domain_url"
    if [ "$type" == "le" ]; then
    
        # Nginx configuration content to be added
        nginx_config_content="
        if (\$scheme != \"https\"){
            #return 301 https://\$host\$request_uri;
        } #forceHTTPS

        # Advertise HTTP/3 QUIC support (required)
        add_header X-protocol $server_protocol always;
        add_header Alt-Svc 'h3=":$server_port"; ma=86400';
        quic_retry on;

        listen $server_ip:443 ssl; #HTTP/2
        listen $server_ip:443 quic; #HTTP/3
        http2 on;
        ssl_certificate /etc/letsencrypt/live/$domain_url/fullchain.pem;
        ssl_certificate_key /etc/letsencrypt/live/$domain_url/privkey.pem;
        include /etc/letsencrypt/options-ssl-nginx.conf;
        ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;
        "
    
        marker="ssl_certificate_key /etc/letsencrypt/live/$domain_url/privkey.pem;"
    
    elif [ "$type" == "custom" ]; then
    
        marker="ssl_certificate_key /etc/nginx/ssl/$domain_url/privkey.pem;"
        
        nginx_config_content="
        if (\$scheme != \"https\"){
            #return 301 https://\$host\$request_uri;
        } #forceHTTPS

        # Advertise HTTP/3 QUIC support (required)
        add_header X-protocol $server_protocol always;
        add_header Alt-Svc 'h3=":$server_port"; ma=86400';
        quic_retry on;
    
        listen $server_ip:443 ssl; #HTTP/2
        listen $server_ip:443 quic; #HTTP/3
        http2 on;
        ssl_certificate /etc/nginx/ssl/$domain_url/fullchain.pem;
        ssl_certificate_key /etc/nginx/ssl/$domain_url/privkey.pem;
        "
    else
        echo "ERROR: Invalid certificate type."
        exit 1
    fi
    
    if grep -qF "$marker" "$nginx_conf_path"; then
        :
        #echo "Configuration already exists. No changes made."
    else 
        # Find the position of the last closing brace
        last_brace_position=$(awk '/\}/{y=x; x=NR} END{print y}' "$nginx_conf_path")
    
        # Insert the Nginx configuration content before the last closing brace
        awk -v content="$nginx_config_content" -v pos="$last_brace_position" 'NR == pos {print $0 ORS content; next} {print}' "$nginx_conf_path" > temp_file
        mv temp_file "$nginx_conf_path"
    
        echo "Nginx configuration editedd successfully, reloading.."
    
        docker exec nginx sh -c "nginx -t > /dev/null 2>&1 && nginx -s reload > /dev/null 2>&1"
    
    fi
fi
}



# Function to check if SSL is valid
check_ssl_validity() {
    domain_url=$1

    echo "Checking SSL validity for domain: $domain_url"

    # Certbot command to check SSL validity
    certbot_check_command=("docker" "exec" "certbot" "certbot" "certificates" "--non-interactive" "--cert-name" "$domain_url")

    # Run Certbot command to check SSL validity
    if "${certbot_check_command[@]}" | grep -q "Expiry Date:.*VALID"; then
        echo "SSL is valid. Checking if in use by Nginx.."

        nginx_conf_path="/etc/nginx/sites-available/$domain_url.conf"
        
        if grep -q "ssl_certificate /etc/letsencrypt/live/$domain_url/fullchain.pem;" "$nginx_conf_path"; then
            echo "SSL is configured properly for the domain."
            check_other_domains_by_user_and_reload_ssl_cache
            exit 0
        else
            echo "SSL is valid but not in use. Checking if custom SSL is present.."
            
            if [[ -f "/etc/nginx/ssl/$domain_url/privkey.pem" && -f "/etc/nginx/ssl/$domain_url/fullchain.pem" ]]; then
                echo "Both privkey.pem and fullchain.pem exist for the domain - custom ssl is installed."
                # TODO: acutally check nginx conf for custom ssl - reuse marker_for_custom_ssl
            else
                echo "Custom SSL not detected for domain. Updating Nginx configuration to inlcude the Let's Encrypt certificate.."
                modify_nginx_conf "$domain_url" "le"
            fi
        fi
    else
        echo "SSL is not valid. Proceeding with SSL generation."
    fi
}



# Function to delete SSL
delete_ssl() {
    domain_url=$1

    echo "Deleting SSL for domain: $domain_url"

    # Let's Encrypt SSL
    delete_command=("docker" "exec" "certbot" "certbot" "delete" "--cert-name" "$domain_url" "--non-interactive")
    "${delete_command[@]}"

    # Custom SSL
    rm "/etc/nginx/ssl/$domain_url/privkey.pem"  > /dev/null 2>&1
    rm "/etc/nginx/ssl/$domain_url/fullchain.pem"  > /dev/null 2>&1

    echo "SSL deletion completed successfully"
}



# Function to revert Nginx configuration
revert_nginx_conf() {
    domain_url=$1
    nginx_conf_path="/etc/nginx/sites-available/$domain_url.conf"

    echo "Editing Nginx configuration to not use SSL for domain: $domain_url"

    # Let's Encrypt SSL
    sed -i '/if (\$scheme != "https"){/,/ssl_dhparam \/etc\/letsencrypt\/ssl-dhparams.pem;/d' "$nginx_conf_path"

    # Custom SSL
    sed -i '/if (\$scheme != "https"){/,/ssl_certificate_key \/etc\/nginx\/ssl\/\$domain_url\/privkey.pem;/d' "$nginx_conf_path"

    echo "Nginx configuration reversion completed successfully"
}





check_other_domains_by_user_and_reload_ssl_cache() {
    #trigger file reload and recheck all other domains also!
    opencli ssl-user $current_username  > /dev/null 2>&1
}



# Main script

# Check the number of arguments
if [ "$#" -lt 1 ]; then
    print_usage
    exit 1
fi



# Use `getopt` to parse the command-line options
OPTS=$(getopt -o d:k:c --long dry-run -n "$0" -- "$@")
if [ $? != 0 ]; then
    echo "Failed to parse options."
    exit 1
fi

# Ensure the positional parameters are correctly set
eval set -- "$OPTS"

# Parse the options
while true; do
    case "$1" in
        --debug)
            DEBUG=1
            shift
            ;;
        --dry-run)
            DRY_RUN=1
            shift
            ;;
        -d)
            delete_flag=true
            shift
            ;;
        -k)
            SSL_PRIVATE_KEY_PATH="$2"
            shift 2
            ;;
        -c)
            SSL_PUBLIC_KEY_PATH="$2"
            shift 2
            ;;
        --)
            shift
            break
            ;;
        *)
            break
            ;;
    esac
done

# The next argument should be the domain URL
domain_url=$1

# Shift to remove the domain URL from the argument list
shift

# Check if domain URL is provided
if [ -z "$domain_url" ]; then
    echo "Error: Domain URL is missing"
    print_usage
    exit 1
fi


if [[ $DEBUG -eq 1 ]]; then
    echo "=========== DEBUG INFORMATION ==========="
    echo "DOMAIN NAME:           $domain_url"
    echo "DEBUG:                 $DEBUG"
    echo "DRY_RUN:               $DRY_RUN"
    echo "DELETE_SSL:            $delete_flag"
    echo "SSL_PRIVATE_KEY_PATH:  $SSL_PRIVATE_KEY_PATH"
    echo "SSL_PUBLIC_KEY_PATH:   $SSL_PUBLIC_KEY_PATH"
fi




# Perform actions based on options
if [ "$delete_flag" = true ]; then
    delete_ssl "$domain_url"
    revert_nginx_conf "$domain_url"
else
    
    if [ -n "$SSL_PRIVATE_KEY_PATH" ] || [ -n "$SSL_PUBLIC_KEY_PATH" ]; then
        import_ssl "$domain_url" || exit 1
        type="custom"
    else
        ensure_jq_installed
        get_server_ip "$domain_url"
        check_ssl_validity "$domain_url"
        generate_ssl_with_dns01 "$domain_url" || exit 1
        type="le"
    fi
    
    modify_nginx_conf "$domain_url" "$type"
fi


if [[ $DRY_RUN -eq 1 ]]; then
    echo "Skip checking SSL for other domains owned by user because of the '--dry-run' flag."
else
    check_other_domains_by_user_and_reload_ssl_cache
fi

# if we made it this far
exit 0
