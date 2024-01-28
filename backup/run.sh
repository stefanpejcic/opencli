#!/bin/bash

# Check if the correct number of command line arguments is provided
if [ "$#" -ne 1 ] && [ "$#" -ne 2 ]; then
    echo "Usage: $0 <NUMBER> [--force-run]"
    exit 1
fi

NUMBER=$1
FORCE_RUN=false

# Check if the --force-run flag is provided
if [ "$#" -eq 2 ] && [ "$2" == "--force-run" ]; then
    FORCE_RUN=true
fi

JSON_FILE="/usr/local/admin/backups/jobs/$NUMBER.json"

# Check if the JSON file exists
if [ ! -f "$JSON_FILE" ]; then
    echo "Error: File $JSON_FILE does not exist."
    exit 1
fi

# Read and parse the JSON file
read_json_file() {
    local json_file="$1"
    jq -r '.status, .destination, .directory, .type[]?, .schedule, .retention, .filters[]?' "$json_file"
}

# Extract data from the JSON file
data=$(read_json_file "$JSON_FILE")

# Assign variables to extracted values
status=$(echo "$data" | awk 'NR==1')
destination=$(echo "$data" | awk 'NR==2')
directory=$(echo "$data" | awk 'NR==3')
types=($(echo "$data" | awk 'NR>=4 && NR<=6'))
schedule=$(echo "$data" | awk 'NR==7')
retention=$(echo "$data" | awk 'NR==8')
filters=($(echo "$data" | awk 'NR>=9'))

# Check if the status is "off" and --force-run flag is not provided
if [ "$status" == "off" ] && [ "$FORCE_RUN" == false ]; then
    echo "Backup job is disabled. Use --force-run to run the backup job anyway."
    exit 0
fi

# Display the extracted values
echo "Status: $status"
echo "Destination: $destination"
echo "Directory: $directory"
echo "Types: ${types[@]}"
echo "Schedule: $schedule"
echo "Retention: $retention"
echo "Filters: ${filters[@]}"

# Add your logic here based on the extracted values
# For example, you can add commands to perform backup operations.
# ...
