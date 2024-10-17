#!/bin/bash

#########################################################################
############################### SEND MAIL ################################ 
#########################################################################




                CONFIG_FILE_PATH='/etc/openpanel/openpanel/conf/openpanel.config'

                
                # Send an email alert
                generate_random_token_one_time_only() {
                    TOKEN_ONE_TIME="$(tr -dc 'a-zA-Z0-9' < /dev/urandom | head -c 64)"
                    local new_value="mail_security_token=$TOKEN_ONE_TIME"
                    sed -i "s|^mail_security_token=.*$|$new_value|" "${CONFIG_FILE_PATH}"
                }

                
                email_notification() {
                  local title="$1"
                  local message="$2"
                  generate_random_token_one_time_only
                  TRANSIENT=$(awk -F'=' '/^mail_security_token/ {print $2}' "${CONFIG_FILE_PATH}")
                                
                  SSL=$(awk -F'=' '/^ssl/ {print $2}' "${CONFIG_FILE_PATH}")
                
                # Determine protocol based on SSL configuration
                if [ "$SSL" = "yes" ]; then
                  PROTOCOL="https"
                else
                  PROTOCOL="http"
                fi
                
                # Send email using appropriate protocol
                curl -k -X POST "$PROTOCOL://127.0.0.1:2087/send_email" -F "transient=$TRANSIENT" -F "recipient=$email" -F "subject=$title" -F "body=$message"
                
                }
                



                read_config() {
                    config=$(awk -F '=' '/\[DEFAULT\]/{flag=1; next} /\[/{flag=0} flag{gsub(/^[ \t]+|[ \t]+$/, "", $1); gsub(/^[ \t]+|[ \t]+$/, "", $2); print $1 "=" $2}' $CONFIG_FILE_PATH)
                    echo "$config"
                }
                
                get_ssl_status() {
                    config=$(read_config)
                    ssl_status=$(echo "$config" | grep -i 'ssl' | cut -d'=' -f2)
                    [[ "$ssl_status" == "yes" ]] && echo true || echo false
                }
                
                get_force_domain() {
                    config=$(read_config)
                    force_domain=$(echo "$config" | grep -i 'force_domain' | cut -d'=' -f2)
                
                    if [ -z "$force_domain" ]; then
                        ip=$(get_public_ip)
                        force_domain="$ip"
                    fi
                    echo "$force_domain"
                }
                
                get_public_ip() {
                    # IP SERVERS
                    SCRIPT_PATH="/usr/local/admin/core/scripts/ip_servers.sh"
                    if [ -f "$SCRIPT_PATH" ]; then
                        source "$SCRIPT_PATH"
                    else
                        IP_SERVER_1=IP_SERVER_2=IP_SERVER_3="https://ip.openpanel.com"
                    fi
                
                    ip=$(curl --silent --max-time 2 -4 $IP_SERVER_1 || wget --timeout=2 -qO- $IP_SERVER_2 || curl --silent --max-time 2 -4 $IP_SERVER_3)
                        
                    # Check if IP is empty or not a valid IPv4
                    if [ -z "$ip" ] || ! [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
                        ip=$(hostname -I | awk '{print $1}')
                    fi
                    echo "$ip"
                }


                # Get port from panel.config or fallback to 2083
                local port=$(grep -Eo 'port=[0-9]+' "$CONFIG_FILE_PATH" | cut -d '=' -f 2)
                port="${port:-2083}"
                
                if [ "$(get_ssl_status)" == true ]; then
                    hostname=$(get_force_domain)
                    login_url="https://${hostname}:$port/"
                else
                    ip=$(get_public_ip)
                    login_url="http://${ip}:$port/"
                fi
