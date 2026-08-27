#!/bin/bash
################################################################################
# Script Name: websites/scan.sh
# Description: Scan user files for CMS installations (WordPress, Drupal,
#              Joomla, OpenCart, Nextcloud, PrestaShop, Matomo, Moodle,
#              MediaWiki, Flarum, SofaWiki) and add them to SiteManager.
# Usage: opencli websites-scan $username
# Author: Stefan Pejcic
# Created: 23.10.2024
# Last Modified: 27.08.2026
# Company: openpanel.com
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
    echo  $result
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
	default_php_version=$(opencli php-default $current_username | grep -oP '\d+\.\d+')
}

#Function to run WordPress CLI commands
run_wp_cli() {
    local username="$1"
    local path="$2"
    local command="$3"
	podman_ctx "$context" exec "php-fpm-$default_php_version" sh -c "php -d memory_limit=-1 -d open_basedir=none -d disable_functions= -d display_errors=0 -d error_log=/dev/null /usr/local/bin/wp --allow-root --path=${path} ${command}"
}

check_site_already_exists_in_db() {
    local site_name="$1"

    local result=$(mariadb -sse "SELECT EXISTS(SELECT 1 FROM sites WHERE site_name = '$(mysql_escape "$site_name")');")
    
    if [[ "$result" -eq 1 ]]; then
        return 0  # exists
    else
        return 1  # not exist
    fi
}


get_mariadb_or_mysql_for_user() {
    mysql_type=$(grep '^MYSQL_TYPE=' /home/$current_username/.env | cut -d '=' -f2 | tr -d '"')

    if [[ "$mysql_type" != "mariadb" && "$mysql_type" != "mysql" ]]; then
        mysql_type="localhost"
    fi

}

# ------------------------------------------------------------------------
# Shared helpers for the non-WordPress CMS scanners below. WordPress keeps
# its own dedicated wp-cli-driven logic above (siteurl/admin_email/version
# all come live from the DB via wp-cli) since none of these other CMS types
# have an equivalent CLI tool readily available for every install; instead
# we resolve the domain from the domains table (matching the found file's
# path against each of the user's domain docroots, longest match wins - the
# same docroot+optional-subdirectory model every install.go in the Go app
# already uses) and read the version straight from a file on disk.
# ------------------------------------------------------------------------

# resolve_site_name_for_path: given a container-path install directory
# (e.g. /var/www/html/example.com/blog), finds which of the current user's
# domains it belongs to and returns "domain.tld" or "domain.tld/blog" for a
# subdirectory install. Empty output means no owning domain was found (the
# domain wasn't added in Site Manager, or docroot mismatch) - callers must
# skip the site in that case, same as the WP scanner already does when
# get_domain_id can't resolve an ID.
resolve_site_name_for_path() {
    local install_dir="$1"
    local best_domain="" best_docroot="" best_len=0
    while IFS=$'\t' read -r durl droot; do
        [ -z "$droot" ] && continue
        if [[ "$install_dir" == "$droot" || "$install_dir" == "$droot"/* ]]; then
            if [ ${#droot} -gt $best_len ]; then
                best_domain="$durl"
                best_docroot="$droot"
                best_len=${#droot}
            fi
        fi
    done < <(mariadb -sse "SELECT domain_url, docroot FROM domains WHERE user_id = (SELECT id FROM users WHERE username = '$(mysql_escape "$current_username")');")

    if [ -z "$best_domain" ]; then
        echo ""
        return
    fi
    local subfolder="${install_dir#$best_docroot}"
    subfolder="${subfolder#/}"
    if [ -n "$subfolder" ]; then
        echo "$best_domain/$subfolder"
    else
        echo "$best_domain"
    fi
}

# extract_composer_lock_version: reads a package's resolved version out of
# a composer.lock file (Drupal/Flarum are Composer-based installs). Scans
# a few lines after the matching "name" key rather than trying to parse
# JSON properly, since composer's own lockfile formatting is consistent
# (one key per line) and this avoids depending on jq/python3 being present
# on the host.
extract_composer_lock_version() {
    local lockfile="$1" pkgname="$2"
    [ -f "$lockfile" ] || return
    grep -A5 "\"name\": \"$pkgname\"" "$lockfile" 2>/dev/null | grep -m1 '"version":' | sed -E 's/.*"version": *"([^"]+)".*/\1/'
}

# insert_scanned_site: shared existing-check + INSERT + permissions-fix +
# summary bookkeeping for every non-WP CMS scanner, mirroring the WP
# scanner's own inline equivalent so all 11 supported types report through
# the same found/skipped counters and end-of-scan summary.
insert_scanned_site() {
    local site_name="$1" admin_email="$2" version="$3" cms_type="$4" inside_container_path="$5" container="$6"

    if check_site_already_exists_in_db "$site_name"; then
        echo "  Site $site_name already exists in the SiteManager - Skipping"
        existing_installations+=("- $site_name ($cms_type)")
        ((existing_count++))
        return
    fi

    local domain_name="${site_name%%/*}"
    local domain_id
    domain_id=$(get_domain_id "$domain_name")
    if ! [[ "$domain_id" =~ ^[0-9]+$ ]]; then
        echo "  WARNING: ID not detected for domain $domain_name - make sure that domain is added for user - Skipping"
        return
    fi

    echo "Adding website $site_name ($cms_type) to Site Manager"
    # container is only meaningful for the docker-compose-service app types
    # (ruby/nodejs/python) - appinstall/install.go's own INSERT sets it to
    # the uppercase service name so getPM2ForApplication() can find that
    # service's <SERVICE>_<TYPE>_CPU/_RAM/etc env vars in .env; every CMS
    # type passes this empty, matching how drupal/flarum/etc's own installers
    # never set this column either (NULL is the correct value for those).
    if [ -n "$container" ]; then
        echo "INSERT INTO sites (site_name, domain_id, admin_email, version, type, container) VALUES ('$(mysql_escape "$site_name")', '$(mysql_escape "$domain_id")', '$(mysql_escape "$admin_email")', '$(mysql_escape "$version")', '$(mysql_escape "$cms_type")', '$(mysql_escape "$container")');" | mysql
    else
        echo "INSERT INTO sites (site_name, domain_id, admin_email, version, type) VALUES ('$(mysql_escape "$site_name")', '$(mysql_escape "$domain_id")', '$(mysql_escape "$admin_email")', '$(mysql_escape "$version")', '$(mysql_escape "$cms_type")');" | mysql
    fi

    echo "Fixing permissions and ownership for the directory $inside_container_path"
    nohup timeout 600 opencli files-fix_permissions "$current_username" "$inside_container_path" >/dev/null 2>&1 &
    disown

    found_installations+=("- $site_name ($cms_type), version: $version")
    ((found_count++))
}

# run_drupal_scan: settings.php only exists (with a real $databases array)
# once drush site:install has actually run - a bare composer create-project
# leaves default.settings.php instead, so this can't false-positive on a
# half-finished install. install.go symlinks web/'s contents up into the
# docroot, so settings.php is found under <docroot>/web/sites/default/ and
# composer.lock lives directly at <docroot>/composer.lock (project root ==
# docroot for this module, same as flarum's).
run_drupal_scan() {
    while IFS= read -r -d '' config_file_path; do
        inside_container_path=$(echo "$config_file_path" | sed -E 's~^.*/_data/~/var/www/html/~')
        grep -q "\$databases\['default'\]\['default'\]" "$config_file_path" 2>/dev/null || continue

        install_dir="${inside_container_path%/web/sites/default/settings.php}"
        site_name=$(resolve_site_name_for_path "$install_dir")
        [ -z "$site_name" ] && continue

        lock_file="${config_file_path%/web/sites/default/settings.php}/composer.lock"
        version=$(extract_composer_lock_version "$lock_file" "drupal/core-recommended")
        [ -z "$version" ] && version=$(extract_composer_lock_version "$lock_file" "drupal/core")
        [ -z "$version" ] && version="Unknown"

        domain_name="${site_name%%/*}"
        insert_scanned_site "$site_name" "admin@$domain_name" "$version" "drupal" "$install_dir"
    done < <(find "$base_directory" -path '*/web/sites/default/settings.php' -print0 2>/dev/null)
}

# run_flarum_scan: config.php lives directly at the docroot's base (Paths
# writes it to $paths->base) unlike Drupal's nested settings.php - install.go
# writes a real config.php only after `flarum install` succeeds, so its
# presence with a 'database' array is a reliable "actually installed" marker.
run_flarum_scan() {
    while IFS= read -r -d '' config_file_path; do
        inside_container_path=$(echo "$config_file_path" | sed -E 's~^.*/_data/~/var/www/html/~')
        grep -q "'database'" "$config_file_path" 2>/dev/null || continue
        # skip Drupal/other configs that might coincidentally have a
        # 'database' key one directory up from a web/ split
        [ -f "$(dirname "$config_file_path")/flarum" ] || continue

        install_dir=$(dirname "$inside_container_path")
        site_name=$(resolve_site_name_for_path "$install_dir")
        [ -z "$site_name" ] && continue

        lock_file="$(dirname "$config_file_path")/composer.lock"
        version=$(extract_composer_lock_version "$lock_file" "flarum/core")
        [ -z "$version" ] && version="Unknown"

        domain_name="${site_name%%/*}"
        insert_scanned_site "$site_name" "admin@$domain_name" "$version" "flarum" "$install_dir"
    done < <(find "$base_directory" -maxdepth 6 -iname 'config.php' -print0 2>/dev/null)
}

# run_joomla_scan: configuration.php is the class Joomla's installer writes
# once setup completes (a bare extracted archive has no such file at all).
run_joomla_scan() {
    while IFS= read -r -d '' config_file_path; do
        inside_container_path=$(echo "$config_file_path" | sed -E 's~^.*/_data/~/var/www/html/~')
        grep -q "class JConfig" "$config_file_path" 2>/dev/null || continue
        [ -f "$(dirname "$config_file_path")/libraries/src/Version.php" ] || continue

        install_dir=$(dirname "$inside_container_path")
        site_name=$(resolve_site_name_for_path "$install_dir")
        [ -z "$site_name" ] && continue

        version_file="$(dirname "$config_file_path")/libraries/src/Version.php"
        major=$(grep -oP "MAJOR_VERSION\s*=\s*\K\d+" "$version_file" 2>/dev/null)
        minor=$(grep -oP "MINOR_VERSION\s*=\s*\K\d+" "$version_file" 2>/dev/null)
        patch=$(grep -oP "PATCH_VERSION\s*=\s*\K\d+" "$version_file" 2>/dev/null)
        version="Unknown"
        [ -n "$major" ] && [ -n "$minor" ] && [ -n "$patch" ] && version="$major.$minor.$patch"

        domain_name="${site_name%%/*}"
        insert_scanned_site "$site_name" "admin@$domain_name" "$version" "joomla" "$install_dir"
    done < <(find "$base_directory" -maxdepth 6 -iname 'configuration.php' -print0 2>/dev/null)
}

# run_opencart_scan: config.php with DB_DATABASE defined only exists after
# OpenCart's installer runs. OpenCart also writes admin/config.php, but the
# top-level one is unique to a completed install (the bare download has
# config-dist.php instead), so it's the reliable marker.
run_opencart_scan() {
    while IFS= read -r -d '' config_file_path; do
        inside_container_path=$(echo "$config_file_path" | sed -E 's~^.*/_data/~/var/www/html/~')
        grep -q "define('DB_DATABASE'" "$config_file_path" 2>/dev/null || continue
        [ -f "$(dirname "$config_file_path")/index.php" ] || continue
        [ -f "$(dirname "$config_file_path")/admin/config.php" ] || continue

        install_dir=$(dirname "$inside_container_path")
        site_name=$(resolve_site_name_for_path "$install_dir")
        [ -z "$site_name" ] && continue

        version=$(grep -oP "VERSION'\s*,\s*'\K[^']+" "$(dirname "$config_file_path")/index.php" 2>/dev/null | head -1)
        [ -z "$version" ] && version="Unknown"

        domain_name="${site_name%%/*}"
        insert_scanned_site "$site_name" "admin@$domain_name" "$version" "opencart" "$install_dir"
    done < <(find "$base_directory" -maxdepth 6 -iname 'config.php' -print0 2>/dev/null)
}

# run_nextcloud_scan: config/config.php only has 'installed' => true and a
# 'dbname' entry once the setup wizard finishes - the bundled config.sample.php
# has neither, so a bare extraction can't false-positive here.
run_nextcloud_scan() {
    while IFS= read -r -d '' config_file_path; do
        inside_container_path=$(echo "$config_file_path" | sed -E 's~^.*/_data/~/var/www/html/~')
        grep -q "'dbname'" "$config_file_path" 2>/dev/null || continue
        grep -q "'installed' => true" "$config_file_path" 2>/dev/null || continue

        install_dir=$(dirname "$(dirname "$inside_container_path")")
        site_name=$(resolve_site_name_for_path "$install_dir")
        [ -z "$site_name" ] && continue

        version_file="$(dirname "$(dirname "$config_file_path")")/version.php"
        version=$(grep -oP "OC_VersionString\s*=\s*'\K[^']+" "$version_file" 2>/dev/null)
        [ -z "$version" ] && version="Unknown"

        domain_name="${site_name%%/*}"
        insert_scanned_site "$site_name" "admin@$domain_name" "$version" "nextcloud" "$install_dir"
    done < <(find "$base_directory" -path '*/config/config.php' -print0 2>/dev/null)
}

# run_prestashop_scan: app/config/parameters.php is generated by the
# installer with real 'database_name' etc values - a fresh extraction has
# no such file (parameters.php.dist instead).
run_prestashop_scan() {
    while IFS= read -r -d '' config_file_path; do
        inside_container_path=$(echo "$config_file_path" | sed -E 's~^.*/_data/~/var/www/html/~')
        grep -q "'database_name'" "$config_file_path" 2>/dev/null || continue

        install_dir=$(dirname "$(dirname "$(dirname "$inside_container_path")")")
        site_name=$(resolve_site_name_for_path "$install_dir")
        [ -z "$site_name" ] && continue

        version_file="$(dirname "$(dirname "$(dirname "$config_file_path")")")/src/Core/Version.php"
        version=$(grep -oP "const VERSION\s*=\s*'\K[^']+" "$version_file" 2>/dev/null)
        [ -z "$version" ] && version="Unknown"

        domain_name="${site_name%%/*}"
        insert_scanned_site "$site_name" "admin@$domain_name" "$version" "prestashop" "$install_dir"
    done < <(find "$base_directory" -path '*/app/config/parameters.php' -print0 2>/dev/null)
}

# run_matomo_scan: config/config.ini.php is an INI file (not a PHP array
# like the others) written by Matomo's installer with a real [database]
# section - the bundled example only ships as config.ini.php.dist.
run_matomo_scan() {
    while IFS= read -r -d '' config_file_path; do
        inside_container_path=$(echo "$config_file_path" | sed -E 's~^.*/_data/~/var/www/html/~')
        grep -qE '^dbname *= *"' "$config_file_path" 2>/dev/null || continue

        install_dir=$(dirname "$(dirname "$inside_container_path")")
        site_name=$(resolve_site_name_for_path "$install_dir")
        [ -z "$site_name" ] && continue

        version_file="$(dirname "$(dirname "$config_file_path")")/core/Version.php"
        version=$(grep -oP "VERSION\s*=\s*'\K[^']+" "$version_file" 2>/dev/null)
        [ -z "$version" ] && version="Unknown"

        domain_name="${site_name%%/*}"
        insert_scanned_site "$site_name" "admin@$domain_name" "$version" "matomo" "$install_dir"
    done < <(find "$base_directory" -path '*/config/config.ini.php' -print0 2>/dev/null)
}

# run_mediawiki_scan: LocalSettings.php is written by the web installer -
# a bare extraction has no such file at all (only LocalSettings.php isn't
# shipped; DefaultSettings.php is, which this glob doesn't match).
run_mediawiki_scan() {
    while IFS= read -r -d '' config_file_path; do
        inside_container_path=$(echo "$config_file_path" | sed -E 's~^.*/_data/~/var/www/html/~')
        grep -q '\$wgDBname' "$config_file_path" 2>/dev/null || continue
        [ -f "$(dirname "$config_file_path")/includes/Defines.php" ] || continue

        install_dir=$(dirname "$inside_container_path")
        site_name=$(resolve_site_name_for_path "$install_dir")
        [ -z "$site_name" ] && continue

        version_file="$(dirname "$config_file_path")/includes/Defines.php"
        version=$(grep -oP "define\(\s*'MW_VERSION',\s*'\K[^']+" "$version_file" 2>/dev/null)
        [ -z "$version" ] && version="Unknown"

        domain_name="${site_name%%/*}"
        insert_scanned_site "$site_name" "admin@$domain_name" "$version" "mediawiki" "$install_dir"
    done < <(find "$base_directory" -maxdepth 6 -iname 'LocalSettings.php' -print0 2>/dev/null)
}

# run_moodle_scan: moodle's docroot is a symlink to <slug>_moodleapp/public
# (install.go writes the actual code to that sibling directory, not the
# docroot itself - see moodle/install.go's docroot-symlink comment) so this
# scans for the *_moodleapp directories directly, then matches each one back
# to an owning domain by finding which domain's docroot (or a direct
# subdirectory of it) is a symlink pointing at "<approot>/public" - the
# same container-path string install.go itself writes as the symlink
# target, so this is comparing strings, not resolving anything on disk.
run_moodle_scan() {
    while IFS= read -r -d '' approot_config; do
        approot_dir=$(dirname "$approot_config")
        version_file="$approot_dir/version.php"
        [ -f "$version_file" ] || continue
        grep -q '\$CFG->dbname' "$approot_config" 2>/dev/null || continue

        approot_container_path=$(echo "$approot_dir" | sed -E 's~^.*/_data/~/var/www/html/~')
        expected_target="$approot_container_path/public"

        install_dir=""
        while IFS=$'\t' read -r durl droot; do
            [ -z "$droot" ] && continue
            droot_host="/home/$current_username/docker-data/volumes/${current_username}_html_data/_data${droot#/var/www/html}"
            for candidate in "$droot_host" "$droot_host"/*; do
                [ -L "$candidate" ] || continue
                target=$(readlink "$candidate")
                if [ "$target" = "$expected_target" ]; then
                    install_dir=$(echo "$candidate" | sed -E 's~^.*/_data~/var/www/html~')
                    break 2
                fi
            done
        done < <(mariadb -sse "SELECT domain_url, docroot FROM domains WHERE user_id = (SELECT id FROM users WHERE username = '$(mysql_escape "$current_username")');")

        [ -z "$install_dir" ] && continue
        site_name=$(resolve_site_name_for_path "$install_dir")
        [ -z "$site_name" ] && continue

        version=$(grep -oP "\\\$release\s*=\s*'\K[^']+" "$version_file" 2>/dev/null | awk '{print $1}')
        [ -z "$version" ] && version="Unknown"

        domain_name="${site_name%%/*}"
        insert_scanned_site "$site_name" "admin@$domain_name" "$version" "moodle" "$install_dir"
    done < <(find "$base_directory" -maxdepth 2 -iname '*_moodleapp' -type d -exec find {} -maxdepth 1 -iname 'config.php' \; 2>/dev/null | tr '\n' '\0')
}

# run_sofawiki_scan: SofaWiki has no database and no CLI, so install.go's
# "download and extract" step alone can't be told apart from an installed
# site - but its own install wizard (inc/special/install.php) only ever
# writes site/configuration.php once the 4-step wizard actually completes,
# so that file's existence is the real "installed, not just extracted"
# marker. Version has no clean per-release number upstream (no tags, no
# composer.lock - see sofawiki.go's package doc comment), so this reads the
# @version line off index.php, same fallback SofaWiki's own admin footer uses.
run_sofawiki_scan() {
    while IFS= read -r -d '' config_file_path; do
        install_root_container=$(echo "$config_file_path" | sed -E 's~^.*/_data/~/var/www/html/~' | sed -E 's~/site/configuration\.php$~~')
        install_dir_fs=$(dirname "$(dirname "$config_file_path")")
        [ -f "$install_dir_fs/inc/configuration-install.php" ] || continue
        [ -f "$install_dir_fs/index.php" ] || continue

        site_name=$(resolve_site_name_for_path "$install_root_container")
        [ -z "$site_name" ] && continue

        version=$(grep -oP '@version\s+\K[0-9.]+' "$install_dir_fs/index.php" 2>/dev/null | head -1)
        [ -z "$version" ] && version="master"

        domain_name="${site_name%%/*}"
        insert_scanned_site "$site_name" "admin@$domain_name" "$version" "sofawiki" "$install_root_container"
    done < <(find "$base_directory" -path '*/site/configuration.php' -print0 2>/dev/null)
}

# run_ruby_scan: unlike the CMS types above, a Ruby app has no config file
# to find on disk - it's a generic docker-compose service (same shape as
# the nodejs/python app installers, which this scan intentionally skips
# entirely since they have no config-file marker either). What CAN be
# detected here is a compose service using the official ruby image that
# hasn't been recorded in the sites table yet (e.g. a service someone added
# by hand, or a site row that got deleted without removing the compose
# service). Each service block looks like:
#   svcname:
#     image: ruby:${SVCNAME_RUBY_TAG:-X.Y}
# so this greps for a 2-space-indented top-level key followed (before the
# next top-level key) by an "image: ruby:" line, then reads that service's
# workdir/tag out of .env (SVCNAME_RUBY_WORKDIR/SVCNAME_RUBY_TAG, written by
# the ruby install flow - see appinstall.buildAppRunCommand's env-var infix
# convention) - confirmed live against a real installed service's exact
# compose/env output.
run_ruby_scan() {
    local compose_file="$base_directory/docker-compose.yml"
    local env_file="$base_directory/.env"
    [ -f "$compose_file" ] || return

    while IFS= read -r svc; do
        [ -z "$svc" ] && continue
        local upper
        upper=$(echo "$svc" | tr '[:lower:]' '[:upper:]')
        local workdir
        workdir=$(grep "^${upper}_RUBY_WORKDIR=" "$env_file" 2>/dev/null | cut -d'=' -f2- | tr -d '"')
        [ -z "$workdir" ] && continue
        local version
        version=$(grep "^${upper}_RUBY_TAG=" "$env_file" 2>/dev/null | cut -d'=' -f2- | tr -d '"')
        [ -z "$version" ] && version="Unknown"

        site_name=$(resolve_site_name_for_path "$workdir")
        [ -z "$site_name" ] && continue

        domain_name="${site_name%%/*}"
        insert_scanned_site "$site_name" "admin@$domain_name" "$version" "ruby" "$workdir" "$upper"
    done < <(awk '
        /^[[:space:]]{2}[a-zA-Z0-9_-]+:[[:space:]]*$/ { svc=$1; sub(/:$/,"",svc); pending=svc; next }
        pending && /image:[[:space:]]*ruby:/ { print pending; pending="" }
        /^[[:space:]]{2}[a-zA-Z0-9_-]+:/ { pending="" }
    ' "$compose_file")
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
    nohup timeout 600 opencli files-fix_permissions $current_username $inside_container_path >/dev/null 2>&1 &
    disown

    found_installations+=("- $site_name, domain: $domain_name, email: $admin_email, version: $version")
    ((found_count++))
done < <(find "$base_directory" -name 'wp-config.php' -print0)

# Non-WordPress CMS types - each function finds+validates+inserts its own
# type, sharing found_installations/existing_installations/found_count/
# existing_count with the WP loop above for one combined summary below.
run_drupal_scan
run_flarum_scan
run_joomla_scan
run_opencart_scan
run_nextcloud_scan
run_prestashop_scan
run_matomo_scan
run_mediawiki_scan
run_moodle_scan
run_sofawiki_scan
run_ruby_scan


# Summary messages
if [ ${#found_installations[@]} -gt 0 ]; then
    echo "Scan completed. Detected $found_count new CMS installations:"
    for installation in "${found_installations[@]}"; do
        echo "$installation"
    done
elif [ ${#existing_installations[@]} -gt 0 ]; then
    echo "Scan completed. No new CMS installations detected, but the following $existing_count existing installations are present:"

    for installation in "${existing_installations[@]}"; do
        echo "$installation"
    done
else
    echo "Scan completed. No CMS installations detected."
fi


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
