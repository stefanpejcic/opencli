#!/bin/bash
################################################################################
# Script Name: user/password.sh
# Description: Reset password for a user.
# Usage: opencli user-password <USERNAME> <NEW_PASSWORD | random>
# Docs: https://docs.openpanel.com
# Author: Stefan Pejcic
# Created: 30.11.2023
# Last Modified: 21.08.2026
# Company: OpenPanel, LLC.
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

# shellcheck disable=SC1091
. /usr/local/opencli/lib/redis.sh

# ======================================================================
# Variables
username=$1
new_password=$2
random_flag=false


# ======================================================================
# Validations
if [ $# -ne 2 ]; then
    echo "Usage: opencli user-password <USERNAME> <NEW_PASSWORD | random>"
    exit 1
fi


# ======================================================================
# Helpers

source /usr/local/opencli/lib/password_strength.sh
source /usr/local/opencli/lib/weakpass.sh

# guarantees at least one upper, lower, digit and punctuation char (plus 8 fully random chars) so this always scores top of the password_strength rubric, regardless of the admin-configured threshold
generate_random_password() {
    local pool='ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()-_=+'
    local pw
    pw="$(tr -dc "$pool" < /dev/urandom | head -c 8)"
    # shellcheck disable=SC2019 # ASCII-only charset is intentional for generated passwords, not locale-dependent [:upper:]
    pw="${pw}$(tr -dc 'A-Z' < /dev/urandom | head -c 1)"
    # shellcheck disable=SC2018 # ASCII-only charset is intentional for generated passwords, not locale-dependent [:lower:]
    pw="${pw}$(tr -dc 'a-z' < /dev/urandom | head -c 1)"
    pw="${pw}$(tr -dc '0-9' < /dev/urandom | head -c 1)"
    pw="${pw}$(tr -dc '!@#$%^&*()-_=+' < /dev/urandom | head -c 1)"
    echo "$pw" | fold -w1 | shuf | tr -d '\n'
}

generate_and_hash_password() {
    if [ "$new_password" == "random" ]; then
        new_password=$(generate_random_password)
        random_flag=true
    fi

    require_password_strength "$new_password"
    [ "$random_flag" = true ] || require_not_common_password "$new_password"
    hashed_password=$(openssl passwd -6 -salt "$(openssl rand -hex 8)" "$new_password")
}

# UPDATE on a missing user matches no rows and would still report success
check_user_exists() {
    if ! user_id=$(mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -N -s -e "SELECT id FROM users WHERE username='$(mysql_escape "$username")' LIMIT 1;"); then
        echo "Error: Could not look up user '$username' in the database."
        exit 1
    fi
    if ! [[ "$user_id" =~ ^[0-9]+$ ]]; then
        echo "Error: User '$username' does not exist."
        exit 1
    fi
}

save_to_database() {
    # 1. update pass
    local escaped_hash
    escaped_hash=$(mysql_escape "$hashed_password")
    mysql_query="UPDATE users SET password='$escaped_hash' WHERE username='$(mysql_escape "$username")';"
    if mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -e "$mysql_query"; then
        # 2. terminate all active sessions
        session_count=$(redis_cli --scan --pattern "session:$user_id:*" | wc -l)
        redis_drop_user_sessions "$user_id"

        # 3. send notification
        nohup opencli sentinel --action=user_password --title="User account password changed" --message="Password for user account '$username' has been changed. $session_count session(s) terminated." >/dev/null 2>&1 &
        disown
        echo "Successfully changed password for user $username$([ "$random_flag" = true ] && echo ", new random generated password is: $new_password")"
    else
        echo "Error: Data insertion failed."
        exit 1
    fi
}



# ======================================================================
# Main
source /usr/local/opencli/db.sh
check_user_exists
generate_and_hash_password
save_to_database
