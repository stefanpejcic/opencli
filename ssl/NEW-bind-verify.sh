#!/bin/bash

# Define the path to the BIND9 zone files
ZONE_FILE_DIR="/etc/bind/zones"

# Read the input arguments
ACTION=$1
CREATE_DOMAIN=$2
CERTBOT_VALIDATION=$3

# Extract the main domain from the challenge domain
DOMAIN=$(echo $CREATE_DOMAIN | sed 's/^_acme-challenge\.//; s/\.$//')

# Extract the zone file name from the domain (assuming it's the same as the domain name)
ZONE_FILE="$ZONE_FILE_DIR/$DOMAIN.zone"

if [ "$ACTION" = "present" ]; then
    # Create a temporary file for the new zone contents
    TEMP_ZONE_FILE=$(mktemp)

    # Check if the TXT record already exists and replace it if it does
    if grep -q "$CREATE_DOMAIN" "$ZONE_FILE"; then
        echo "Record exists, replacing it..."
        sed "s|$CREATE_DOMAIN.*|$CREATE_DOMAIN\t14400\tIN\tTXT\t\"$CERTBOT_VALIDATION\"|" "$ZONE_FILE" > "$TEMP_ZONE_FILE"
    else
        echo "Record does not exist, adding it..."
        cp "$ZONE_FILE" "$TEMP_ZONE_FILE"
        echo -e "$CREATE_DOMAIN\t14400\tIN\tTXT\t\"$CERTBOT_VALIDATION\"" >> "$TEMP_ZONE_FILE"
    fi

    # Replace the old zone file with the new one
    mv "$TEMP_ZONE_FILE" "$ZONE_FILE"

    # Reload BIND9 to apply the changes
    echo "Reloading BIND9 to apply changes"
    rndc reload $DOMAIN

elif [ "$ACTION" = "cleanup" ]; then
    # Create a temporary file for the new zone contents
    TEMP_ZONE_FILE=$(mktemp)

    # Remove the TXT record from the zone file
    echo "Removing the TXT record from the zone file..."
    grep -v "$CREATE_DOMAIN" "$ZONE_FILE" > "$TEMP_ZONE_FILE"

    # Replace the old zone file with the new one
    mv "$TEMP_ZONE_FILE" "$ZONE_FILE"

    # Reload BIND9 to apply the changes
    echo "Reloading BIND9 to apply changes"
    rndc reload $DOMAIN

else
    echo "Invalid action specified. Use 'present' to add or update a record, or 'cleanup' to remove a record."
    exit 1
fi
