#!/bin/bash
################################################################################
# Script Name: ftp/add.sh
# Description: Create frp sub-user for openpanel user.
# Usage: opencli ftp-add <NEW_USERNAME> <NEW_PASSWORD> <FOLDER> <OPENPANEL_USERNAME>
# Docs: https://docs.openpanel.co/docs/admin/scripts/ftp#add
# Author: Stefan Pejcic
# Created: 22.05.2024
# Last Modified: 22.05.2024
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

if [ "$#" -lt 4 ] || [ "$#" -gt 5 ]; then
    script_name=$(realpath --relative-to=/usr/local/admin/scripts/ "$0")
    script_name="${script_name//\//-}"  # Replace / with -
    script_name="${script_name%.sh}"     # Remove the .sh extension
    echo "Usage: opencli $script_name <new_username> <new_password> '<directory>' <openpanel_username> [--debug]"
    exit 1
fi

username="${1,,}"
password="$2"
directory="$3"
openpanel_username="$4"
DEBUG=false  # Default value for DEBUG



# Parse optional flags to enable debug mode when needed!
for arg in "$@"; do
    case $arg in
        --debug)
            DEBUG=true
            ;;
        *)
            ;;
    esac
done


dummy_func_tobe_removed(){

if [ "$DEBUG" = true ]; then

else

fi

}


# Function to read users from users.list files and create them
create_user() {
    command='echo -e "$PASS\n$PASS" | adduser -h $FOLDER -s /sbin/nologin $UID_OPT $GROUP_OPT $NAME'
    docker exec -it openadmin_ftp sh -c "$command"
    mkdir -p $FOLDER
    chown 1000:33 $FOLDER
}


# Function to delete a user - WILL BE SEPARATED IN ANOTHER FILE!
delete_user() {
    docker exec -it openadmin_ftp sh -c "deluser $username && rm -rf $folder"
    echo "Success: FTP user '$username' deleted successfully."
}



# user@domain or user@openpanel_username
if [[ ! $username == *"@"* ]]; then
    echo "Error: FTP username must include '@' symbol in the format 'user@domain.com' or 'user@openpanel'."
    exit 1
fi

# check if ftp user exists
user_exists() {
    local user="$1"
    grep -q "^$user\|" /etc/openpanel/ftp/users/${openpanel_username}/users.list
}

mkdir -p /etc/openpanel/ftp/users/
touch /etc/openpanel/ftp/users/${openpanel_username}/users.list

# Check if user already exists
if user_exists "$username"; then
    echo "Error: FTP User '$username' already exists."
    exit 1
fi

# check folder path is under the openpanel_username home folder
if [[ $directory != /home/$openpanel_username* ]]; then
    echo "ERROR: Invalid folder '$directory' - folder must start with '/home/$openpanel_username/'."
    exit 1
else

# If user does not exist, add them to the file
echo "$username|$password|$directory" >> /etc/openpanel/ftp/users/${username}/users.list

# and in the ftp container:
create_user

# Output success message
echo "Success: FTP user '$username' created successfully."



: '
EXAMPLES

user1|password1|/home/user1
user2|password2|/home/user2




# to be supported in future:

user1|password1|/home/user1|1001|1001
user2|password2|/home/user2|1002|1002

user1|password1|/home/user1||1001|users
user2|password2|/home/user2||1002|admins

'



