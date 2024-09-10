#!/bin/sh

# Gather the contents of all users.list files into USER_FILES
USER_FILES=$(cat /etc/openpanel/ftp/users/*/users.list)

# Prepare the USERS variable
USERS=""
while IFS= read -r line; do
  USERS="$USERS $line"
done <<EOF
$USER_FILES
EOF

# Remove any leading/trailing spaces from USERS
USERS=$(echo "$USERS" | xargs)

# Write USERS to the all.users file
echo "USERS=\"$USERS\"" > /etc/openpanel/ftp/all.users
