#!/bin/bash
################################################################################
# Script Name: user/memcached.sh
# Description: Check and enable/disable Memcached for user.
# Usage: opencli user-varnish <USERNAME> [install|start|restart|stop|uninstall] 
# Docs: https://docs.openpanel.co/docs/admin/scripts/users#varnish
# Author: Stefan Pejcic
# Created: 21.08.2024
# Last Modified: 21.08.2024
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


usage() {
    echo "Usage: opencli user-varnish <username> {install|start|restart|stop|uninstall}"
    echo
    echo "Commands:"
    echo "  install    - Installs the Varnish server and its dependencies."
    echo "  start      - Starts the Varnish server."
    echo "  restart    - Restarts the Varnish server."
    echo "  stop       - Stops the Varnish server."
    echo "  uninstall  - Uninstalls the Varnish server and reverts to Nginx/Apache."
    echo
    echo "Examples:"
    echo "  opencli user-varnish <username> install    # Install the varnish server"
    echo "  opencli user-varnish <username> start      # Start the varnish server"
    echo "  opencli user-varnish <username> restart    # Restart the varnish server"
    echo "  opencli user-varnish <username> stop       # Stop the varnish server"
    echo "  opencli user-varnish <username> uninstall  # Uninstall the varnish server"
    exit 1
}


if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
    usage
fi

DEBUG=false  # Default value for DEBUG

while [[ "$#" -gt 2 ]]; do
    case $3 in
        --debug) DEBUG=true ;;
    esac
    shift
done




# INSTALL
install_varnish_for_user(){
   if [ "$DEBUG" = true ]; then
        echo ""
        echo "----------------- INSTALLING VARNISH ------------------"
        echo ""
        docker exec bash -c $container_name "apt-get install varnish -y"
        docker cp /etc/openpanel/varnish/default $container_name:/etc/default/varnish
  else
       docker exec bash -c $container_name "apt-get install varnish -y" >/dev/null 2>&1
       docker cp /etc/openpanel/varnish/default $container_name:/etc/default/varnish >/dev/null 2>&1
  fi
}

# TODO: open tcp out!!!!!


# UNINSTALL
uninstall_varnish_for_user(){
   if [ "$DEBUG" = true ]; then
        echo ""
        echo "----------------- UNINSTALLING VARNISH ------------------"
        echo ""
        docker exec bash -c $container_name "apt-get remove varnish -y"
  else
       docker exec bash -c $container_name "apt-get remove varnish -y" >/dev/null 2>&1
  fi
}


# START
start_varnish_for_user(){
   if [ "$DEBUG" = true ]; then
        echo ""
        echo "----------------- STARTING VARNISH ------------------"
        echo ""
        docker exec bash -c $container_name "pkill varnish; service varnish start"
  else
        docker exec bash -c $container_name "pkill varnish; service varnish start" >/dev/null 2>&1
  fi
}

# STOP
stop_varnish_for_user(){
   if [ "$DEBUG" = true ]; then
        echo ""
        echo "----------------- STOPPING VARNISH ------------------"
        echo ""
        docker exec bash -c $container_name "pkill varnish; service varnish stop"
  else
        docker exec bash -c $container_name "pkill varnish; service varnish stop" >/dev/null 2>&1
  fi
}


# PURGE CACHE
purge_varnish_cache_for_user(){
   if [ "$DEBUG" = true ]; then
        echo ""
        echo "----------------- PURGE VARNISH CACHE ------------------"
        echo ""
        docker exec bash -c $container_name "varnishadm 'ban req.url ~ /'"
  else
        docker exec bash -c $container_name "varnishadm 'ban req.url ~ /'" >/dev/null 2>&1
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
                sed -i -e 's|proxy_pass http://\(\$container_name\);|proxy_pass http://\1:6081;|g' -e 's|proxy_pass https://\(\$container_name\);|proxy_pass https://\1:6081;|g' "$DOMAIN_CONF"
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
                sed -i -e 's|proxy_pass http://\(\$container_name\):6081;|proxy_pass http://\1;|g' -e 's|proxy_pass https://\(\$container_name\):6081;|proxy_pass https://\1;|g' "$DOMAIN_CONF"
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
container_name="$1"

case "$2" in
    install)
        echo "Installing the Varnish cache server for user $container_name"
        install_varnish_for_user                          # install service 
        start_varnish_for_user                            # start service 
        #########      todo: check port 6081 for user localhost      #########
        process_all_domains_nginx_conf                    # include in nginx conf
        restart_nginx_service                             # serve with varnish
        ;;
    start)
        echo "Starting varnish for user $container_name"
        start_varnish_for_user                            # start service 
        process_all_domains_nginx_conf                    # include in nginx conf
        restart_nginx_service                             # serve with varnish
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
        process_all_domains_nginx_conf                    # include in nginx conf
        restart_nginx_service                             # serve with varnish
        ;;
    stop)
        echo "Stopping varnish server for user $container_name"
        remove_from_all_domains_nginx_conf                # remove from nginx conf
        restart_nginx_service                             # serve with nginx
        stop_varnish_for_user                             # stop service
        ;;
    uninstall)
        echo "Uninstalling varnish server for user $container_name"
        remove_from_all_domains_nginx_conf                # remove from nginx conf
        restart_nginx_service                             # serve with nginx
        stop_varnish_for_user                             # stop service
        uninstall_varnish_for_user                        # remove
        ;;
    *)
        usage                                             # show help
        ;;
esac



