#!/bin/bash

USERS=""

for dir in /etc/openpanel/ftp/users/*; do
    file="$dir/users.list"
    user=$(basename "$dir")
    if [[ -f "$file" ]]; then
        while IFS= read -r line; do
            modified_line="${line//\/var\/www\/html\//\/home\/${user}\/docker-data\/volumes\/${user}_html_data\/_data\/}"
            USERS+="$modified_line"
        done < "$file"
    fi
done

echo "$USERS" 

echo "USERS=\"$USERS\"" > /etc/openpanel/ftp/all.users
