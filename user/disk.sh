#!/bin/bash
username="$1"
action="$2"
json="$3"


print_usage() {
    echo "Usage: opencli user-disk <summary|detail> <user>"
    exit 1
}


# Check if the action is valid
if [[ "$action" != "path" && "$action" != "detail" && "$action" != "summary" ]]; then
    print_usage
fi


# Function to parse `df` output
parse_df_output() {
    local output="$1"
    local last_line
    local penultimate_line

    # Extract last two lines
    last_line=$(echo "$output" | tail -n 1)
    penultimate_line=$(echo "$output" | tail -n 2 | head -n 1)

    # Split lines into columns
    IFS=' ' read -r -a columns_last <<< "$last_line"
    IFS=' ' read -r -a columns_penultimate <<< "$penultimate_line"

    if [ "${#columns_last[@]}" -eq 5 ] && [ "${#columns_penultimate[@]}" -eq 5 ]; then




      # 
      case $action in
          path)

display_paths
          
              ;;
          detail)
check_details_and_show


     
              ;;
          summary)

        check_and_show_summary
          
              ;;
          *)
              print_usage
              ;;
      esac
      






      
    else
        echo "Error: Unexpected output format from 'df'"
        return 1
    fi
}






########## HELPERS




# DETAILS
check_details_and_show(){

              if [ "${columns_last[4]}" == "/" ]; then
                  # no storage file
                  disk_limit=false
              elif [ "${columns_last[4]}" == "/home/$username" ]; then
                  disk_limit=true
              fi
      



          if [ "$json" == "--json" ]; then

    if [ "${columns_last[4]}" == "/" ]; then
        # No storage file
        home_used="${columns_last[3]}"
        home_total="${columns_last[2]}"
        inodes_used="${columns_last[1]}"
        inodes_total="${columns_last[0]}"
        home_path="/"
    elif [ "${columns_last[4]}" == "/home/$username" ]; then
        # Specific home directory
        home_used="${columns_last[3]}"
        home_total="${columns_last[2]}"
        inodes_used="${columns_last[1]}"
        inodes_total="${columns_last[0]}"
        home_path="/home/$username"
    fi


                  container_path="${columns_penultimate[4]}"
                  container_used="${columns_penultimate[3]}"
                  container_total="${columns_penultimate[2]}"
                  
                  inodes_docker_used="${columns_penultimate[1]}"
                  inodes_docker_total="${columns_penultimate[0]}"







       
            # Output in JSON format
# Build the JSON output
json_output=$(cat <<EOF
{
  "home": {
    "path": "$home_path",
    "bytes_used": "$home_used",
    "bytes_total": "$home_total",
    "bytes_limit": $disk_limit,
    "inodes_used": "$inodes_used",
    "inodes_total": "$inodes_total"
  },
  "container": {
    "path": "$container_path",
    "bytes_used": "$container_used",
    "bytes_total": "$container_total",
    "inodes_used": "$inodes_docker_used",
    "inodes_total": "$inodes_docker_total"
  },
  "storage_driver": "$storage_driver"
}
EOF
)

# Output the JSON
echo "$json_output"






    
          else

          
              #echo "/home/$username"
              
              echo "storage driver:        $storage_driver"
              
              echo "-------------------"
      
      
              # Check if huser has no storage file - and uses just '/'
              if [ "${columns_last[4]}" == "/" ]; then
                  # no storage file
                  echo "DISK USAGE FOR ${columns_last[4]}"
                  echo "- home_used=${columns_last[3]}"
                  echo "- home_total=${columns_last[2]}"
              elif [ "${columns_last[4]}" == "/home/$username" ]; then
                  echo "DISK USAGE FOR ${columns_last[4]}"
                  echo "- home_used=${columns_last[3]}"
                  echo "- home_total=${columns_last[2]}"
              fi
                 echo  "- home_limit=$disk_limit"


      
              echo "-------------------"
      
              
              # Check if huser has no storage file - and uses just '/'
              if [ "${columns_last[4]}" == "/" ]; then
                  # no storage file
                  echo "INODES USAGE FOR ${columns_last[4]}"
                  echo "- inodes_used=${columns_last[1]}"
                  echo "- inodes_total=${columns_last[0]}"
              elif [ "${columns_last[4]}" == "/home/$username" ]; then
                  echo "INODES USAGE FOR ${columns_last[4]}"
                  echo "- inodes_used=${columns_last[1]}"
                  echo "- inodes_total=${columns_last[0]}"
              fi
      
      
              echo "-------------------"
      
                  echo "DISK USAGE FOR ${columns_penultimate[4]}"
                  echo "- container_used=${columns_penultimate[3]}"
                  echo "- container_total=${columns_penultimate[2]}"
                  
                  echo "INODES USAGE FOR ${columns_penultimate[4]}"
                  echo "- inodes_used=${columns_penultimate[1]}"
                  echo "- inodes_total=${columns_penultimate[0]}"
              

          fi
}

#SUMMARY
check_and_show_summary(){
            home_actual_size_for_user=$(du -sh /home/${username})
            docker_contianer_acutal_size=$(du -sh ${columns_penultimate[4]})


          if [ "$json" == "--json" ]; then
            #use blocks anc cut path
            home_actual_size_for_user=$(du -s /home/${username})
            docker_container_actual_size=$(du -s ${columns_penultimate[4]})
            
            home_usage=$(echo "$home_actual_size_for_user" | cut -f1)
            home_path=$(echo "$home_actual_size_for_user" | cut -f2-)
            
            docker_usage=$(echo "$docker_container_actual_size" | cut -f1)
            docker_path=$(echo "$docker_container_actual_size" | cut -f2-)

          
            # Output in JSON format
            echo "{\"home_directory_usage\": \"$home_usage\", \"docker_container_usage\": \"$docker_usage\", \"home_path\": \"$home_path\", \"docker_path\": \"$docker_path\"}"
          else

            # use human readable
            home_actual_size_for_user=$(du -sh /home/${username})
            docker_contianer_acutal_size=$(du -sh ${columns_penultimate[4]})
            echo "DISK USAGE:"
            echo "- ${home_actual_size_for_user}" # 
            echo "- ${docker_contianer_acutal_size}"
          fi
}



#PATHS
display_paths(){

          if [ "$json" == "--json" ]; then
            # Output in JSON format
            echo '{"home_directory": "/home/'"$username"'","docker_container_path": "'"${columns_penultimate[4]}"'"}'
          else
            echo "PATHS:"
            echo "- home_directory=/home/$username"
            echo "- docker_container_path=${columns_penultimate[4]}"
            echo "-------------------"
          fi
}


########### END HELPERS
















#MAIN

# Determine Docker storage driver
storage_driver=$(docker info --format '{{.Driver}}')

# Construct the path based on the storage driver
case "$storage_driver" in
    overlay2)
        full_path="/var/lib/docker/overlay2/"
        ;;
    devicemapper)
        device_name=$(docker inspect --format '{{ .GraphDriver.Data.DeviceName }}' "$username" 2>/dev/null)
        if [ $? -ne 0 ]; then
            echo "Error getting path: $?"
            exit 1
        fi
        path=$(echo "$device_name" | awk -F'-' '{print $NF}')
        full_path="/var/lib/docker/devicemapper/mnt/$path"
        ;;
    *)
        echo "Unsupported storage driver"
        exit 1
        ;;
esac

# Define the home directory path
home_path="/home/$username"

# Run df command
combined_command="df -B1 --output=itotal,iused,size,used,target $full_path $home_path | tail -n 2"
combined_output=$(eval "$combined_command")

# Parse the output
df_result=$(parse_df_output "$combined_output")

echo -e "$df_result"



