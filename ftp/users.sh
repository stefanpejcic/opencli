#!/bin/bash

USER_FILES=$(cat /etc/openpanel/ftp/users/*/users.list)

USERS=""
while read -r line; do
  USERS="$USERS $line"
done <<< "$USER_FILES"

echo $USERS

USERS=$(echo $USERS | xargs)

echo "USERS=\"$USERS\"" > /etc/openpanel/ftp/all.users

