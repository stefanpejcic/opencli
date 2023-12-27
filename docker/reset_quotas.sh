#!/bin/bash

# Run the command and store the output in a variable
output=$(df -BG | awk 'NR>1 && $6 ~ /^\/var\/lib\/docker\/devicemapper\/mnt\//' | jq -R -s '
  [
    split("\n")
    | .[]
    | select(length > 0)
    | gsub(" +"; " ")
    | split(" ")
    | {filesystem: .[0], spacetotal: .[1], spaceavail: .[2], mount: .[5]}
  ]'
)

# Check if the command was successful
if [ $? -eq 0 ]; then
    updated_output='['

    # Iterate over each entry in the JSON array and add the username and original size
    for entry in $(echo "$output" | jq -c '.[]'); do
        mount_path=$(echo "$entry" | jq -r '.mount')
        username=$(grep 'x:1000:1000' "$mount_path/rootfs/etc/passwd" | awk -F: '{print $1}')

        # Run docker inspect command and get original size
        original_size=$(docker inspect --format='{{.HostConfig.StorageOpt.size}}' "$username")

        # Add username and original size to the JSON entry
        entry_with_username_and_size=$(echo "$entry" | jq --arg username "$username" --arg original "$original_size" '. + {username: $username, original: $original}')
        updated_output="$updated_output$entry_with_username_and_size,"
    done

    # Remove the trailing comma and close the array
    updated_output="${updated_output%,}]"

    # Save the updated output to a JSON file
    echo "$updated_output" | jq '.' > df.json
    echo "Command successful. JSON output with usernames and original sizes saved to df.json."
else
    # Print an error message if the command failed
    echo "Error running the command."
fi
