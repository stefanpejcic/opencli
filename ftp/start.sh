#!/bin/bash

# stop first
docker stop openadmin_ftp && docker rm openadmin_ftp


# start
USER_FILES=$(cat /etc/openpanel/ftp/users/*/users.list)

USERS=""
while read -r line; do
  USERS="$USERS $line"
done <<< "$USER_FILES"

echo $USERS

USERS=$(echo $USERS | xargs)


cd /root && docker compose down openadmin_ftp
USERS="$USERS" docker compose up openadmin_ftp -d
