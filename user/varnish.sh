#!/bin/bash
################################################################################
# Script Name: user/varnish.sh
# Description: Configure and manage Varnish Cache for user.
# Usage: opencli user-varnish <USERNAME> <install|start|test|purge|restart|stop|uninstall> [--debug]
# Docs: https://docs.openpanel.co/docs/admin/scripts/users#varnish
# Author: Stefan Pejcic
# Created: 21.08.2024
# Last Modified: 25.08.2024
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


usage() {
    echo "Usage: opencli user-varnish <username> {install|start|restart|test|purge|stop|uninstall}"
    echo
    echo "Commands:"
    echo "  install    - Installs the Varnish server and its dependencies."
    echo "  start      - Starts the Varnish server."
    echo "  test       - Check response from Varnish server."
    echo "  purge      - Removes all cache from Varnish server."
    echo "  restart    - Restarts the Varnish server."
    echo "  stop       - Stops the Varnish server."
    echo "  uninstall  - Uninstalls the Varnish server and reverts to Nginx/Apache."
    echo
    echo "Examples:"
    echo "  opencli user-varnish <username> install    # Install the varnish server"
    echo "  opencli user-varnish <username> start      # Start the varnish server"
    echo "  opencli user-varnish <username> test       # Test if cache used in response"
    echo "  opencli user-varnish <username> purge      # Purge all cache"
    echo "  opencli user-varnish <username> restart    # Restart the varnish server"
    echo "  opencli user-varnish <username> stop       # Stop the varnish server"
    echo "  opencli user-varnish <username> uninstall  # Uninstall the varnish server"
    exit 1
}


if [ "$#" -lt 2 ] || [ "$#" -gt 5 ]; then
    usage
fi

DEBUG=false  # Default value for DEBUG

while [[ "$#" -gt 0 ]]; do
    case "$1" in
        --debug)
            DEBUG=true
            ;;
        install|start|restart|test|purge|stop|uninstall)
            action="$1"
            ;;
        *)
            container_name="$1"
            ;;
    esac
    shift
done


if [ -z "$action" ] || [ -z "$container_name" ]; then
    usage
fi








# FIREWALL
setup_firewall(){

    if command -v csf >/dev/null 2>&1; then
        echo "Checking ConfigServer Firewall configuration.."
        port="6081"
        local csf_conf="/etc/csf/csf.conf"
        port_opened=$(grep "TCP_OUT = .*${port}" "$csf_conf")
        if [ -z "$port_opened" ]; then
            sed -i "s/TCP_OUT = \"\(.*\)\"/TCP_OUT = \"\1,${port}\"/" "$csf_conf"
            ports_opened=1
        fi
    elif command -v ufw >/dev/null 2>&1; then
        echo ""
        echo "Checking UFW configuration.."
        # TODO
        # ufw allow from 192.168.1.0/24 to any port 6081
        # ufw deny 6081
        # ufw reload
    else
        echo "Error: Neither CSF nor UFW are installed, make sure outgoing port 6081 is opened on external firewall."
    fi

}


# INSIDE
get_webserver_for_user(){
	    echo "Checking webserver configuration"
	    output=$(opencli webserver-get_webserver_for_user $container_name)
	    if [[ $output == *nginx* ]]; then
	        ws="nginx"
	    elif [[ $output == *apache* ]]; then
	        ws="apache2"
	    else
	        ws="unknown"
	    fi
}

replace_80_to_8080() {
	if [[ $ws == *apache2* ]]; then
		echo "NOT YET FOR APACHE!"
	elif [[ $ws == *nginx* ]]; then
        for domain_conf_file in /home/$container_name/etc/nginx/sites-available/*.conf; do
          sed -i 's/listen 80;/listen 8080;/g' "$domain_conf_file"
        done  
       docker exec $container_name bash -c "service nginx restart" >/dev/null 2>&1
	fi

}


replace_8080_to_80() {
	if [[ $ws == *apache2* ]]; then
		echo "NOT YET FOR APACHE!"
	elif [[ $ws == *nginx* ]]; then
        for domain_conf_file in /home/$container_name/etc/nginx/sites-available/*.conf; do
          sed -i 's/listen 8080;/listen 80;/g' "$domain_conf_file"
        done  
       docker exec $container_name bash -c "service nginx restart" >/dev/null 2>&1
	fi

}



# INSTALL
install_varnish_for_user(){
   if [ "$DEBUG" = true ]; then
        echo ""
        echo "----------------- INSTALLING VARNISH ------------------"
        echo ""
        docker exec $container_name bash -c "apt-get update && apt-get install varnish -y"
        # must be after install or prompt
        docker cp /etc/openpanel/services/varnish.service $container_name:/lib/systemd/system/varnish.service
        docker cp /etc/openpanel/varnish/default.vcl $container_name:/etc/varnish/default.vcl
  else
       docker exec $container_name bash -c "apt-get update && apt-get install varnish -y" >/dev/null 2>&1
       docker cp /etc/openpanel/services/varnish.service $container_name:/lib/systemd/system/varnish.service >/dev/null 2>&1
       docker cp /etc/openpanel/varnish/default.vcl $container_name:/etc/varnish/default.vcl >/dev/null 2>&1
  fi
}

# TODO: open tcp out!!!!!


# UNINSTALL
uninstall_varnish_for_user(){
   if [ "$DEBUG" = true ]; then
        echo ""
        echo "----------------- UNINSTALLING VARNISH ------------------"
        echo ""
        docker exec $container_name bash -c "apt-get remove varnish -y"
  else
       docker exec $container_name bash -c "apt-get remove varnish -y" >/dev/null 2>&1
  fi
}


# START
start_varnish_for_user(){
   if [ "$DEBUG" = true ]; then
        echo ""
        echo "----------------- STARTING VARNISH ------------------"
        echo ""
        docker exec $container_name bash -c "pkill varnish; service varnish start; /etc/init.d/varnish start"
  else
        docker exec $container_name bash -c "pkill varnish; service varnish start; /etc/init.d/varnish start" >/dev/null 2>&1
  fi
}

# STOP
stop_varnish_for_user(){
   if [ "$DEBUG" = true ]; then
        echo ""
        echo "----------------- STOPPING VARNISH ------------------"
        echo ""
        docker exec $container_name bash -c "pkill varnish; service varnish stop"
  else
        docker exec $container_name bash -c "pkill varnish; service varnish stop" >/dev/null 2>&1
  fi
}


# PURGE CACHE
purge_varnish_cache_for_user(){
   if [ "$DEBUG" = true ]; then
        echo ""
        echo "----------------- PURGE VARNISH CACHE ------------------"
        echo ""
        docker exec $container_name bash -c "/etc/init.d/varnish start ; varnishadm 'ban req.url ~ /'"
  else
        docker exec $container_name bash -c "/etc/init.d/varnish start ; varnishadm 'ban req.url ~ /'" >/dev/null 2>&1
  fi
}


# TEST VARNISH CACHE
test_cache_for_user(){
    URL="http://localhost:6081"
    response=$(docker exec $container_name bash -c "curl -ILs $URL")

   if [ "$DEBUG" = true ]; then
        echo ""
        echo "----------------- TESTING VARNISH CACHE ------------------"
        echo ""
  fi


    if echo "$response" | grep -q "Varnish"; then
        echo "Varnish is currently used for user domains."
    else
        echo "Varnish not currently detected for user domains."
    fi
    
    if [ "$DEBUG" = true ]; then
        docker exec $container_name bash -c "curl -ILs $URL"
    fi
    
}


# UPDATE NGINX
process_all_domains_nginx_conf(){

ALL_DOMAINS=$(opencli domains-user $container_name)
NGINX_CONF_PATH="/etc/nginx/sites-available/"
  
  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- ENABLING VARNISH CACHE FOR ALL DOMAINS OWNED BY USER ------------------"
      echo ""
  fi

        for domain in $ALL_DOMAINS; do
            DOMAIN_CONF="$NGINX_CONF_PATH/$domain.conf"
            if [ -f "$DOMAIN_CONF" ]; then
                sed -i -e '/if (\$scheme = "https") {/,/proxy_pass https:\/\//s|proxy_pass https://|proxy_pass http://|' "$DOMAIN_CONF"            
                echo "Varnish enabled for domain $domain"
            fi
        done

    # Restart Nginx to apply changes
    docker exec nginx bash -c "nginx -t && nginx -s reload" >/dev/null 2>&1 

}

# REMOVE NGINX
remove_from_all_domains_nginx_conf(){

ALL_DOMAINS=$(opencli domains-user $container_name)
NGINX_CONF_PATH="/etc/nginx/sites-available/"
  
  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- DISABLING VARNISH CACHE FOR ALL DOMAINS OWNED BY USER ------------------"
      echo ""
  fi

        for domain in $ALL_DOMAINS; do
            DOMAIN_CONF="$NGINX_CONF_PATH/$domain.conf"
            if [ -f "$DOMAIN_CONF" ]; then
                sed -i -e '/if (\$scheme = "https") {/,/proxy_pass http:\/\//s|proxy_pass http://|proxy_pass https://|' "$DOMAIN_CONF"
                echo "Varnish disabled for domain $domain"
            fi
        done

}

# RELOAD NGINX
restart_nginx_service(){
  if [ "$DEBUG" = true ]; then
      echo ""
      echo "----------------- RELOADING NGINX CONFIGURATION ------------------"
      echo ""
      docker exec nginx bash -c "nginx -t && nginx -s reload"
  else
      docker exec nginx bash -c "nginx -t && nginx -s reload" >/dev/null 2>&1 
  fi
}




# MAIN

if $DEBUG; then
      echo "----------------- DEBUG MODE IS ENABLED ------------------"
fi


case "$action" in
    install)
        echo "Installing the Varnish cache server for user $container_name"
        install_varnish_for_user                          # install service
        setup_firewall                                    # out_tcp 6081
        get_webserver_for_user                            # nginx only now
        replace_80_to_8080                                # sed
        start_varnish_for_user                            # start service 
        #########      todo: check port 6081 for user localhost      #########
        process_all_domains_nginx_conf                    # include in nginx conf
        restart_nginx_service                             # serve with varnish
        ;;
    start)
        echo "Starting varnish for user $container_name"
        get_webserver_for_user                            # nginx only now
        replace_80_to_8080                                # sed
        start_varnish_for_user                            # start service 
        test_cache_for_user                               # test before adding to nginx
        process_all_domains_nginx_conf                    # include in nginx conf
        restart_nginx_service                             # serve with varnish
        ;;
    test)
        echo "Testing varnish cache for user $container_name"
        test_cache_for_user                               # test cache
        ;;
    purge)
        echo "Purge varnish cache for all domains owned by user $container_name"
        purge_varnish_cache_for_user                      # purge cache 
        ;;
    restart)
        echo "Restarting varnish server for user $container_name"
        stop_varnish_for_user                             # stop service
        remove_from_all_domains_nginx_conf                # remove from nginx conf
        restart_nginx_service                             # serve with nginx
        start_varnish_for_user                            # start service 
        replace_80_to_8080
        process_all_domains_nginx_conf                    # include in nginx conf
        restart_nginx_service                             # serve with varnish
        ;;
    stop)
        echo "Stopping varnish server for user $container_name"
        replace_8080_to_80
        remove_from_all_domains_nginx_conf                # remove from nginx conf
        restart_nginx_service                             # serve with nginx
        stop_varnish_for_user                             # stop service
        ;;
    uninstall)
        echo "Uninstalling varnish server for user $container_name"
        replace_8080_to_80
        remove_from_all_domains_nginx_conf                # remove from nginx conf
        restart_nginx_service                             # serve with nginx
        stop_varnish_for_user                             # stop service
        uninstall_varnish_for_user                        # remove
        ;;
    *)
        usage                                             # show help
        ;;
esac



