#!/bin/bash
################################################################################
# Script Name: websites/scan.sh
# Description: Scan user files for WP sites and add them to SiteManager interface.
# Usage: opencli websites-scan <USERNAME|-all>
# Author: Stefan Pejcic
# Created: 23.10.2024
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
. /usr/local/opencli/lib/podman.sh

# Function to get domain ID from the database
get_domain_id() {
    local domain_name="$1"
    result=$(mariadb -sse "SELECT domain_id FROM domains WHERE domain_url = '$(mysql_escape "$domain_name")';")
    echo  "$result"
}

get_context_for_user() {
     source /usr/local/opencli/db.sh
        username_query="SELECT server FROM users WHERE username = '$(mysql_escape "$current_username")'"
        context=$(mariadb -D "$mysql_database" -e "$username_query" -sN)
        if [ -z "$context" ]; then
            context=$current_username
        fi
}

get_php_default_for_user() {
	default_php_version=$(opencli php-default "$current_username" | grep -oP '\d+\.\d+')
}

#Function to run WordPress CLI commands
run_wp_cli() {
    local path="$2"
    local command="$3"
	podman_ctx "$context" exec "php-fpm-$default_php_version" sh -c "php -d memory_limit=-1 -d open_basedir=none -d disable_functions= -d display_errors=0 -d error_log=/dev/null /usr/local/bin/wp --allow-root --path=${path} ${command}"
}

check_site_already_exists_in_db() {
    local site_name="$1"

    local result
    result=$(mariadb -sse "SELECT EXISTS(SELECT 1 FROM sites WHERE site_name = '$(mysql_escape "$site_name")');")
    
    if [[ "$result" -eq 1 ]]; then
        return 0  # exists
    else
        return 1  # not exist
    fi
}


get_mariadb_or_mysql_for_user() {
    mysql_type=$(grep '^MYSQL_TYPE=' /home/"$current_username"/.env | cut -d '=' -f2 | tr -d '"')

    if [[ "$mysql_type" != "mariadb" && "$mysql_type" != "mysql" ]]; then
        mysql_type="localhost"
    fi

}


insert_scanned_site() {
    local site_name="$1" domain_id="$2" admin_email="$3" version="$4" cms_type="$5"

    echo "Adding website $site_name to Site Manager"
    echo "INSERT INTO sites (site_name, domain_id, admin_email, version, type) VALUES ('$(mysql_escape "$site_name")', '$(mysql_escape "$domain_id")', '$(mysql_escape "$admin_email")', '$(mysql_escape "$version")', '$(mysql_escape "$cms_type")');" | mysql

    local inside_container_path
    inside_container_path="/var/www/html/${site_name}"
    echo "Fixing permissions and ownership for the directory $inside_container_path"
    nohup timeout 600 opencli files-fix_permissions "$current_username" "$inside_container_path" >/dev/null 2>&1 &
    disown
}

# resolve_site_name_for_path takes an inside-container path (e.g.
# /var/www/html/example.com/sub/index.php) and, by matching it against the
# current user's domains.docroot values (longest match wins, so a
# subdirectory install resolves to the subdirectory rather than the parent
# domain), echoes back "<domain_id>\t<site_name>" - site_name being
# "domain.tld" for a root install or "domain.tld/sub" for a subdirectory
# one. Returns non-zero (nothing echoed) if no domain owns the path.
resolve_site_name_for_path() {
    local file_path="$1"
    local target_dir
    target_dir=$(dirname "$file_path")

    local domain_rows
    domain_rows=$(mariadb -sse "SELECT domains.domain_id, domains.domain_url, domains.docroot FROM domains JOIN users ON users.id = domains.user_id WHERE users.username = '$(mysql_escape "$current_username")' AND domains.docroot IS NOT NULL AND domains.docroot != '' ORDER BY LENGTH(domains.docroot) DESC;")

    local domain_id domain_url docroot
    while IFS=$'\t' read -r domain_id domain_url docroot; do
        [ -z "$docroot" ] && continue
        docroot="${docroot%/}"
        if [ "$target_dir" == "$docroot" ] || [[ "$target_dir" == "$docroot"/* ]]; then
            local subdir="${target_dir#"$docroot"}"
            subdir="${subdir#/}"
            if [ -n "$subdir" ]; then
                echo -e "${domain_id}\t${domain_url}/${subdir}"
            else
                echo -e "${domain_id}\t${domain_url}"
            fi
            return 0
        fi
    done <<< "$domain_rows"

    return 1
}

# run_tinyphotogallery_scan detects an EXISTING TinyPhotoGallery install
# not yet tracked in the sites table: every index.php under the user's
# html_data tree whose content mentions "tinyphotogallery" (case
# insensitive) AND that has a sibling photos/ directory is treated as a
# TinyPhotoGallery install (this is the entire upstream install - a single
# index.php plus an empty photos/ folder, see internal/modules/tinyphotogallery
# in the openpanel repo for the installer this mirrors).
run_tinyphotogallery_scan() {
    while IFS= read -r -d '' index_file; do
        grep -qi "tinyphotogallery" "$index_file" || continue

        local photos_dir
        photos_dir="$(dirname "$index_file")/photos"
        [ -d "$photos_dir" ] || continue

        local inside_container_path
        inside_container_path=$(echo "$index_file" | sed -E 's~^.*/_data/~/var/www/html/~')

        echo "- Found possible TinyPhotoGallery install: $inside_container_path"

        local resolved domain_id site_name
        resolved=$(resolve_site_name_for_path "$inside_container_path")
        if [ -z "$resolved" ]; then
            echo "  WARNING: unable to resolve domain for $inside_container_path - make sure a domain/subdirectory docroot covers this path - Skipping"
            continue
        fi
        domain_id="${resolved%%$'\t'*}"
        site_name="${resolved#*$'\t'}"

        if check_site_already_exists_in_db "$site_name"; then
            echo "  Site $site_name already exists in the SiteManager - Skipping"
            continue
        fi

        local domain_name admin_email
        domain_name="${site_name%%/*}"
        admin_email="admin@${domain_name}"

        insert_scanned_site "$site_name" "$domain_id" "$admin_email" "main" "tinyphotogallery"
    done < <(find "$base_directory" -name 'index.php' -print0)
}

# run_tinyfilemanager_scan detects an EXISTING TinyFileManager install not
# yet tracked in the sites table: unlike TinyPhotoGallery's generic
# index.php, TinyFileManager's own filename (tinyfilemanager.php) is
# distinctive enough on its own - no content grep needed, see
# internal/modules/tinyfilemanager in the openpanel repo for the installer
# this mirrors.
run_tinyfilemanager_scan() {
    while IFS= read -r -d '' tfm_file; do
        local inside_container_path
        inside_container_path=$(echo "$tfm_file" | sed -E 's~^.*/_data/~/var/www/html/~')

        echo "- Found possible TinyFileManager install: $inside_container_path"

        local resolved domain_id site_name
        resolved=$(resolve_site_name_for_path "$inside_container_path")
        if [ -z "$resolved" ]; then
            echo "  WARNING: unable to resolve domain for $inside_container_path - make sure a domain/subdirectory docroot covers this path - Skipping"
            continue
        fi
        domain_id="${resolved%%$'\t'*}"
        site_name="${resolved#*$'\t'}"

        if check_site_already_exists_in_db "$site_name"; then
            echo "  Site $site_name already exists in the SiteManager - Skipping"
            continue
        fi

        local domain_name admin_email
        domain_name="${site_name%%/*}"
        admin_email="admin@${domain_name}"

        insert_scanned_site "$site_name" "$domain_id" "$admin_email" "latest" "tinyfilemanager"
    done < <(find "$base_directory" -name 'tinyfilemanager.php' -print0)
}

# run_ojs_scan detects an existing OJS (Open Journal Systems) install. OJS
# installs itself into a sibling "<slug>_ojsapp" directory with the docroot
# left as a symlink pointing at it (see internal/modules/ojs's package doc
# comment), so a plain (non-symlink-following) find would only ever report
# the "_ojsapp" sibling path, which resolve_site_name_for_path can't map
# back to a domain since it isn't a real docroot. Using `find -L` instead
# makes find also descend through the docroot symlink and report
# config.inc.php a second time under the actual domain-facing docroot
# path - the "-not -path '*_ojsapp/config.inc.php'" filter keeps only that
# second, resolvable copy and drops the raw approot one.
run_ojs_scan() {
    while IFS= read -r -d '' ojs_config; do
        local inside_container_path
        inside_container_path=$(echo "$ojs_config" | sed -E 's~^.*/_data/~/var/www/html/~')
        inside_container_path="${inside_container_path%/config.inc.php}"

        echo "- Found possible OJS install: $inside_container_path"

        local resolved domain_id site_name
        resolved=$(resolve_site_name_for_path "$inside_container_path")
        if [ -z "$resolved" ]; then
            echo "  WARNING: unable to resolve domain for $inside_container_path - make sure a domain/subdirectory docroot covers this path - Skipping"
            continue
        fi
        domain_id="${resolved%%$'\t'*}"
        site_name="${resolved#*$'\t'}"

        if check_site_already_exists_in_db "$site_name"; then
            echo "  Site $site_name already exists in the SiteManager - Skipping"
            continue
        fi

        local domain_name admin_email
        domain_name="${site_name%%/*}"
        admin_email="admin@${domain_name}"

        # OJS tracks its installed version in the database, not in
        # config.inc.php, so an exact version can't be read off disk here -
        # matches tinyphotogallery/tinyfilemanager's "latest" fallback.
        insert_scanned_site "$site_name" "$domain_id" "$admin_email" "latest" "ojs"
    done < <(find -L "$base_directory" -name 'config.inc.php' -not -path '*_ojsapp/config.inc.php' -print0 2>/dev/null)
}

run_for_single_user() {

current_username=$1
get_context_for_user
# Base directory to scan for wp-config.php files
base_directory="/home/${current_username}"

found_installations=()
existing_installations=()

found_count=0
existing_count=0

get_php_default_for_user
get_mariadb_or_mysql_for_user



# Iterate through user files
while IFS= read -r -d '' config_file_path; do
    inside_container_path=$(echo "$config_file_path" | sed -E 's~^.*/_data/~\/var\/www\/html/~')

    echo "- Parsing file: $inside_container_path"
    sed -i -E \
    -e "s/(define\([[:space:]]*['\"]DB_HOST['\"],[[:space:]]*)['\"]localhost['\"]/\1'$mysql_type'/" \
    -e "s/(define\([[:space:]]*['\"]DB_HOST['\"],[[:space:]]*)['\"]localhost:3306['\"]/\1'$mysql_type'/" \
    -e "s/(define\([[:space:]]*['\"]DB_HOST['\"],[[:space:]]*)['\"]127\.0\.0\.1['\"]/\1'$mysql_type'/" \
    -e "s/(define\([[:space:]]*['\"]DB_HOST['\"],[[:space:]]*)['\"]127\.0\.0\.1:3306['\"]/\1'$mysql_type'/" \
    "$config_file_path"
    
    # get sitename and domain
	domain=$(run_wp_cli "$current_username" "$(dirname "$inside_container_path")" "option get siteurl 2>/dev/null")
	site_name=$(echo "$domain" | sed -E 's~https?://~~')
	
	# Validate that domain starts with http or https, else abort
	if [[ ! "$domain" =~ ^https?:// ]]; then
	    echo "Error: unable to get sitename from database."
	    continue
	fi

    domain_name=$(echo "$domain" | sed -E 's~https?://~~' | cut -d'/' -f1)

    # Check if website exists in sites table
    if check_site_already_exists_in_db "$site_name"; then
    	echo "  Site $site_name already exists in the SiteManager - Skipping"
        existing_installations+=("- $site_name - domain: $domain_name, config: ${inside_container_path%/wp-config.php}")
        ((existing_count++))   	
        continue
    fi

    # Get domain ID
    domain_id=$(get_domain_id "$domain_name")
    if ! [[ "$domain_id" =~ ^[0-9]+$ ]]; then
    	echo "  WARNING: ID not detected for domain $domain_name - make sure that domain is added for user - Skipping"
    	exit 1
    fi
    
    # Get admin email from wp-config.php
    admin_email=$(run_wp_cli "$current_username" "$(dirname "$inside_container_path")" "option get admin_email 2>/dev/null")
    if [[ ! "$admin_email" =~ "@" ]]; then
        echo "  WARNING: Invalid admin email: $admin_email"
    fi

    # Get WordPress version
    version=$(run_wp_cli "$current_username" "$(dirname "$inside_container_path")" "core version 2>/dev/null")
    
    echo "Adding website $site_name to Site Manager"
    echo "INSERT INTO sites (site_name, domain_id, admin_email, version, type) VALUES ('$(mysql_escape "$site_name")', '$(mysql_escape "$domain_id")', '$(mysql_escape "$admin_email")', '$(mysql_escape "$version")', 'wordpress');" | mysql

    echo "Fixing permissions and ownership for the directory $inside_container_path"
    nohup timeout 600 opencli files-fix_permissions "$current_username" "$inside_container_path" >/dev/null 2>&1 &
    disown

    found_installations+=("- $site_name, domain: $domain_name, email: $admin_email, version: $version")
    ((found_count++))
done < <(find "$base_directory" -name 'wp-config.php' -print0)





# Summary messages
if [ ${#found_installations[@]} -gt 0 ]; then
    echo "Scan completed. Detected $found_count new WordPress installations:"
    for installation in "${found_installations[@]}"; do
        echo "$installation"
    done
elif [ ${#existing_installations[@]} -gt 0 ]; then
    echo "Scan completed. No new WordPress installations detected, but the following $existing_count existing installations are present:"

    for installation in "${existing_installations[@]}"; do
        echo "$installation"
    done
else
    echo "Scan completed. No WordPress installations detected."
fi

run_tinyphotogallery_scan
run_tinyfilemanager_scan
run_ojs_scan

}






if [ $# -eq 0 ]; then
  echo "Usage: opencli websites-scan <USERNAME> OR opencli websites-scan -all"
  exit 1
elif [[ "$1" == "-all" ]]; then
# ALL USERS

  users=$(opencli user-list --json | grep -v 'SUSPENDED' | awk -F'"' '/username/ {print $4}')

  if [[ -z "$users" || "$users" == "No users." ]]; then
    echo "No users found in the database."
    exit 1
  fi
  
  total_users=$(echo "$users" | wc -w)
  current_user_index=1
  
  for user in $users; do
    echo "Processing user: $user ($current_user_index/$total_users)"
        run_for_single_user "$user"   
    echo "------------------------------"
    ((current_user_index++))
  done
  echo "DONE."

# SINGLE USER
elif [ $# -eq 1 ]; then
  run_for_single_user "$1"
else
  echo "Usage: opencli websites-scan <USERNAME> OR opencli websites-scan -all"
  exit 1
fi
