#!/bin/bash

# Define the directory where the JSON files are stored
stats_dir="/usr/local/panel/core/stats"

# Get the resource_usage_retention value from panel.config
panel_config="/usr/local/panel/conf/panel.config"
resource_usage_retention=$(grep -Eo "resource_usage_retention=[0-9]+" "$panel_config" | cut -d'=' -f2)

if [[ -z $resource_usage_retention ]]; then
  echo "Error: Could not determine resource_usage_retention value in panel.config"
  exit 1
fi

# Loop through the directories
for user_dir in "$stats_dir"/*; do
  if [ -d "$user_dir" ]; then
    user=$(basename "$user_dir")
    file_count=$(find "$user_dir" -name "*.json" | wc -l)
    
    # Calculate the number of files to delete
    files_to_delete=$((file_count - resource_usage_retention))

    # Delete the oldest files if necessary
    if [ "$files_to_delete" -gt 0 ]; then
      cd "$user_dir" || exit 1
      find . -name "*.json" -type f -printf '%T@ %p\n' | sort -n | head -n "$files_to_delete" | cut -d' ' -f2 | xargs rm
      echo "Deleted $files_to_delete files in $user_dir"
    fi
  fi
done

exit 0
