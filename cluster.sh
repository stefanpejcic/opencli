#!/bin/bash
################################################################################
# Script Name: cluster.sh
# Description: Manage Cluster.
# Usage: opencli cluster
# Author: Stefan Pejcic
# Created: 05.09.2024
# Last Modified: 29.11.2024
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

# Enable debug mode based on --debug flag
DEBUG=0
if [[ "$1" == "--debug" ]]; then
    DEBUG=1
    shift
fi

debug_echo() {
    if [[ $DEBUG -eq 1 ]]; then
        echo "$@"
    else
        > /dev/null
    fi
}


# Function to log messages
log_message() {
    local LOG_FILE="/var/log/openpanel/admin/cluster.log"
    echo "$(date): $1" | tee -a $LOG_FILE
}

# ======================================================================
# LIST ALL SERVERS IN CLUSTER
list_slaves() {
    debug_echo "Debug: Listing all slave users and IPs..."
    # Get the list of Docker contexts
    printf "%-20s %-10s\n" "Server" "Users"
    printf "%-20s %-10s\n" "--------------------" "----------"

    # Get the list of Docker contexts
    contexts=$(docker context ls -q)

    for context in $contexts; do
        docker context use "$context" >/dev/null 2>&1
        user_count=$(mysql -e "SELECT COUNT(*) FROM users WHERE server = '$context';" -B --skip-column-names)
        printf "%-20s %-10s\n" "$context" "$user_count"
    done
}



LOG_FILE="/var/log/openpanel/admin/cluster.log"

# ======================================================================
# ADD NEW SLAVE TO CLUSTER
add_slave() {
    local SLAVE_USER="$1"
    local SLAVE_IP="$2"
    local AUTH_VALUE="${3}"
    local PUBLIC_KEY_PATH="${HOME}/.ssh/id_rsa.pub"
 
    
   
    
    # Function to execute commands over SSH using password authentication
    ssh_with_password() {
        sshpass -p "$1" ssh -o StrictHostKeyChecking=no "$2@$3" "$4"
    }
    
    # Function to execute commands over SSH using key authentication
    ssh_with_key() {
        ssh -i "$1" -o StrictHostKeyChecking=no "$2@$3" "$4"
    }
    
    
    
    # STEP 1. get ipv4 of master server
    current_ip=$(curl --silent --max-time 2 -4 https://ip.openpanel.com || wget --timeout=2 -qO- https://ip.openpanel.com || curl --silent --max-time 2 -4 https://ifconfig.me)
    if [ -z "$current_ip" ]; then
       current_ip=$(ip addr|grep 'inet '|grep global|head -n1|awk '{print $2}'|cut -f1 -d/)
    fi
    
    
    # STEP 2. check if key or pass is provided
    if [ -n "$AUTH_VALUE" ]; then
        if [ -f "$AUTH_VALUE" ]; then
            AUTH_TYPE="key"
            SSH_KEY_PATH="${AUTH_VALUE}"           
        else
            AUTH_TYPE="password"
            SSH_KEY_PATH="${HOME}/.ssh/id_rsa"
        fi
    else
        echo "Error: No authentication value provided. Please provide either a password or SSH key path."
        exit 1
    fi
    
    # STEP 3. ensure sshpass is installed on master
    if [ "$AUTH_TYPE" == "password" ] && ! command -v sshpass >/dev/null 2>&1; then
        log_message "sshpass is not installed but required for password authentication. It will be automatically installed."
        apt-get install sshpass -y
    fi
    
    # STEP 4. csf and ufw
    log_message "Checking if slave server: $SLAVE_IP is allowed on master firewall."
    
    is_ip_whitelisted() {
        if command -v csf >/dev/null 2>&1; then
            csf -l | grep -q "$SLAVE_IP"
        elif command -v ufw >/dev/null 2>&1; then
            ufw status | grep -q "$SLAVE_IP"
        fi
    }
    
    if is_ip_whitelisted; then
        log_message "IP $SLAVE_IP is whitelisted on firewall."
    else
        if command -v csf >/dev/null 2>&1; then
            csf -a "$SLAVE_IP"
            if [ $? -eq 0 ]; then
                log_message "Successfully whitelisted $SLAVE_IP using CSF."
            else
                log_message "Error whitelisting $SLAVE_IP with CSF."
                exit 1
            fi
        elif command -v ufw >/dev/null 2>&1; then
            ufw allow from "$SLAVE_IP"
            if [ $? -eq 0 ]; then
                log_message "Successfully whitelisted $SLAVE_IP using UFW."
            else
                log_message "Error whitelisting $SLAVE_IP with UFW."
                exit 1
            fi
        else
            log_message "Neither CSF nor UFW are installed. Cannot whitelist the slave IP."
        fi    
    
    fi
    
    # STEP 5. test ssh
    log_message "Testing SSH connection to $SLAVE_USER@$SLAVE_IP..."
    
    if [ "$AUTH_TYPE" == "password" ]; then
        ssh_with_password "$AUTH_VALUE" "$SLAVE_USER" "$SLAVE_IP" "echo 'SSH connection successful!'" > /dev/null 2>&1
    elif [ "$AUTH_TYPE" == "key" ]; then
        if [ ! -f "$AUTH_VALUE" ]; then
            log_message "Error: SSH key file not found at $AUTH_VALUE."
            exit 1
        fi
        ssh_with_key "$AUTH_VALUE" "$SLAVE_USER" "$SLAVE_IP" "echo 'SSH connection successful!'" > /dev/null 2>&1
    fi
    
    if [ $? -ne 0 ]; then
        log_message "Error: SSH connection to $SLAVE_USER@$SLAVE_IP failed."
        exit 1
    fi
    
    log_message "SSH connection to $SLAVE_USER@$SLAVE_IP established."
    
    # STEP 6. test on slave ssh to master, whitelist it and then setup all needed on slave!
    #TODOOOOOO
    
    # todo:
    
    : '
    csf/ufw - zaseban konf za slave, samo da daje sa master sve
    docker - zaseban konf
    bind -  moze mount samo
    nginx - moraju da imaju zaseban konf!
    openpanel -default master, ali da moze i overwrite sve - whne adding doamin check if cluster, if so, if slave then use master ns but slave ipv4
    certbot - nema promena, svaki radi posebno
    mail server - na master da lista i sa slave servera
    webmail - nema promena -domen da moze po slave da se definise
    ftp - nema promena, posebno su
    clamav - nema promena - na svakom srv će da radi samo za domene i home dir koji je tamo
    '
       
    
    # STEP 7. setup ssh between servers
    if [ ! -f "$SSH_KEY_PATH" ]; then
        log_message "Generating SSH key on master..."
        ssh-keygen -t rsa -b 4096 -f "$SSH_KEY_PATH" -N "" > /dev/null 2>&1
        if [ $? -ne 0 ]; then
            log_message "Error: Failed to generate SSH key."
            exit 1
        else
            log_message "SSH key generated successfully."
        fi
    else
        log_message "Using SSH key $SSH_KEY_PATH provided by the user"
    fi
    
    
    if [ "$AUTH_TYPE" == "password" ]; then
        log_message "Copying SSH public key to slave for passwordless SSH..."
        sshpass -p "$AUTH_VALUE" ssh-copy-id -i "$SSH_KEY_PATH.pub" "$SLAVE_USER@$SLAVE_IP"
        sshpass -p "$AUTH_VALUE" ssh-copy-id -i "$SSH_KEY_PATH" "$SLAVE_USER@$SLAVE_IP"
        if [ $? -ne 0 ]; then
            log_message "Error: Failed to copy SSH public key to the slave."
            exit 1
        else
            log_message "SSH public key copied successfully. Future SSH connections will use key-based authentication."
        fi
    fi

    log_message "Copying SSH public key to slave for master-to-slave SSH access..."
    
    ssh_with_key "$SSH_KEY_PATH" "$SLAVE_USER" "$SLAVE_IP" \
        "mkdir -p ~/.ssh && cat >> ~/.ssh/authorized_keys" < "$PUBLIC_KEY_PATH"
    
    if [ $? -ne 0 ]; then
        log_message "Error: Failed to copy SSH public key to slave server."
        exit 1
    fi
    
    # ADD ON MASTER
    mkdir -p ~/.ssh && cat >> ~/.ssh/authorized_keys < "$PUBLIC_KEY_PATH"
    if [ $? -ne 0 ]; then
        log_message "Error: Failed to copy SSH public key to master server."
    fi

    ssh_with_key "$SSH_KEY_PATH" "$SLAVE_USER" "$SLAVE_IP" \
        "touch $SSH_KEY_PATH && chmod 0600 $SSH_KEY_PATH && cat >> $SSH_KEY_PATH" < "$SSH_KEY_PATH"


    scp -i $SSH_KEY_PATH $SSH_KEY_PATH "$SLAVE_USER@$SLAVE_IP:/etc/openpanel/openadmin/cluster/" > /dev/null 2>&1

    ssh -i "$SSH_KEY_PATH" -o StrictHostKeyChecking=no "$SLAVE_USER@$SLAVE_IP" "
        echo 'SSH connection to slave successful';
        ssh -o StrictHostKeyChecking=no -i '$SSH_KEY_PATH' 'root@$current_ip' 'echo SSH connection to MASTER successful'
    "   
    if [ $? -ne 0 ]; then
        log_message "Error: SSH connection test from slave $SLAVE_IP to master $current_ip failed."
        exit 1
    fi
    
    
    
    # STEP 8. create docker context
    SLAVE_HOSTNAME=$(ssh -i "$SSH_KEY_PATH" "$SLAVE_USER@$SLAVE_IP" "hostname")
    
    if docker context ls --format '{{.Name}}' | grep -q "^$SLAVE_HOSTNAME$"; then
        log_message "Docker context '$SLAVE_HOSTNAME' already exists. Updating it..."
        
        docker context rm "$SLAVE_HOSTNAME" > /dev/null 2>&1
        if [ $? -ne 0 ]; then
            log_message "Error: Failed to remove existing Docker context."
            exit 1
        fi
    else
        log_message "Docker context '$SLAVE_HOSTNAME' does not exist. Creating it..."
    fi
    
    docker context create "$SLAVE_HOSTNAME" --description="OpenPanel slave server" --docker "host=ssh://$SLAVE_USER@$SLAVE_IP" > /dev/null 2>&1
    
    if [ $? -ne 0 ]; then
        log_message "Error: Failed to create or update Docker context."
        exit 1
    fi
    
    log_message "Docker context '$SLAVE_HOSTNAME' updated successfully."
    
    
    # STEP 9. TEST SSH 
    log_message "Testing SSH connection to $SLAVE_USER@$SLAVE_IP..."
    
    ssh -i "$SSH_KEY_PATH" -o StrictHostKeyChecking=no -o BatchMode=yes "$SLAVE_USER@$SLAVE_IP" "echo 'SSH connection successful.'" > /dev/null 2>&1
    
    if [ $? -ne 0 ]; then
        log_message "Error: SSH connection test failed."
        exit 1
    fi
    
    log_message "SSH connection test successful. You can now connect to $SLAVE_USER@$SLAVE_IP using key-based authentication."
    
    
    # STEP 10. initial sync of data
    log_message "Performing initial data sync from master to slave..."
    
    mkdir -p /etc/openpanel/openadmin/cluster/
    
    if ! command -v scp >/dev/null 2>&1; then
        log_message "scp is not installed but is required for data synchronization. It will be automatically installed."
        sudo apt-get install scp -y
    fi
    
    # cluster does not exist yet on slave!
    scp -i $SSH_KEY_PATH -r /etc/openpanel/openadmin/cluster/ "$SLAVE_USER@$SLAVE_IP:/etc/openpanel/openadmin/" > /dev/null 2>&1
    
    
    if [ $? -ne 0 ]; then
        log_message "Error: Initial sync failed."
        exit 1
    fi
    
    log_message "Initial sync completed successfully."
    
    # STEP 11. create docker context on slave for master
    context_exists=$(ssh -i "$SSH_KEY_PATH" "$SLAVE_USER@$SLAVE_IP" "docker context ls --format '{{.Name}}' | grep -w 'master'" 2>/dev/null)
    
    if [ -z "$context_exists" ]; then
        log_message "Creating Docker context for the master on the slave with name 'master'..."
        ssh -i "$SSH_KEY_PATH" "$SLAVE_USER@$SLAVE_IP" "docker context create master --description='OpenPanel master server' --docker 'host=ssh://root@$current_ip'" > /dev/null 2>&1
    
        if [ $? -ne 0 ]; then
            log_message "Error: Failed to create Docker context for the master on the slave."
            exit 1
        fi
    
        log_message "Docker context 'master' created successfully on the slave."
    else
        # TODO: Context exists, update it
        log_message "Docker context 'master' already exists on the slave."
    fi
    
    log_message "Docker containers running on the slave server $SLAVE_HOSTNAME that are visible from the master:"
    docker --context $SLAVE_HOSTNAME ps
    log_message "Switching to Docker context 'master' on the slave and reading docker info:"
   
    docker_summary=$(ssh -i "$SSH_KEY_PATH" -o StrictHostKeyChecking=no -o BatchMode=yes "$SLAVE_USER@$SLAVE_IP" \
        "'docker --context master ps'")
    
    if [ $? -ne 0 ]; then
        log_message "Error: Failed to switch Docker context to 'master' and retrieve Docker summary from the slave."
        exit 1
    fi
    
    #docker_summary=$(ssh -i "$SSH_KEY_PATH" -o StrictHostKeyChecking=no -o BatchMode=yes "$SLAVE_USER@$SLAVE_IP" "docker --context master ps")
    log_message "Docker containers running on the master server $(hostname) that are visible from the slave server:"
    echo "$docker_summary"
    
    
    # STEP 12. additional ssh config
    log_message "Configuring SSH to reuse SSH connection for multiple invocations of the docker CLI"
    echo "
    ControlMaster     auto
    ControlPath       ~/.ssh/control-%C
    ControlPersist    yes" >> ~/.ssh/config
    
    
    # STEP 13. mount files
    
     sudo apt-get install sshfs    # Debian/Ubuntu
    #sudo yum install fuse-sshfs   # CentOS/RHEL
    #sudo dnf install sshfs        # Fedora
    
    # TODO: on slave also!
    
    sshfs -o IdentityFile="$SSH_KEY_PATH" "$SLAVE_USER@$SLAVE_IP:/home" /home
    if [ $? -ne 0 ]; then
        log_message "Error: Failed to mount /home from slave server to the master."
        exit 1
    fi
    
    echo "$SLAVE_USER@$SLAVE_IP:/etc/openpanel/ /etc/nginx fuse IdentityFile=$SSH_KEY_PATH,_netdev,users,allow_other 0 0" >> /etc/fstab
    
    
    sshfs -o IdentityFile="$SSH_KEY_PATH" "$SLAVE_USER@$SLAVE_IP:/etc/bind" /etc/bind
    if [ $? -ne 0 ]; then
        log_message "Error: Failed to mount /etc/bind from slave server to the master."
        exit 1
    fi
    
    echo "$SLAVE_USER@$SLAVE_IP:/etc/bind /etc/bind fuse IdentityFile=$SSH_KEY_PATH,_netdev,users,allow_other 0 0" >> /etc/fstab
    
    
    
    log_message "Setup completed successfully."
  
  



}




# ======================================================================
# REMOVE SLAVE SERVER
remove_slave() {
    local slave_ip="$1"
    debug_echo "Debug: Removing slave with IP=$slave_ip"

}

# ======================================================================
# VALIDATE CONNECTION TO SLAVE
check_slave() {
    local slave_ip="$1"
    debug_echo "Debug: Checking slave with IP=$slave_ip"

}

# ======================================================================
# HELP
usage() {
    echo "Usage:"
    echo "  opencli cluster [--debug] list"
    echo "  opencli cluster [--debug] add <slave_user> <slave_ip> <key_path|password>"
    echo "  opencli cluster [--debug] remove <slave_ip>"
    echo "  opencli cluster [--debug] check <slave_ip>"
    exit 1
}

if [[ $# -lt 1 ]]; then
    usage
fi

command="$1"
shift

case "$command" in
    list)
        list_slaves
        ;;
    add)
        if [[ $# -lt 2 ]]; then
            echo "Error: 'add' requires at least <slave_user> and <slave_ip>"
            usage
        fi
        add_slave "$@"
        ;;
    remove)
        if [[ $# -ne 1 ]]; then
            echo "Error: 'remove' requires <slave_ip>"
            usage
        fi
        remove_slave "$1"
        ;;
    check)
        if [[ $# -ne 1 ]]; then
            echo "Error: 'check' requires <slave_ip>"
            usage
        fi
        check_slave "$1"
        ;;
    *)
        echo "Error: Unknown command '$command'"
        usage
        ;;
esac
