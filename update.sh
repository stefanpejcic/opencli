#!/bin/bash
################################################################################
# Script Name: update.sh
# Description: Check if update is available, install updates.
# Usage: opencli update [--check | --force | --admin | --panel | --cli | --translations | --system | --modules | --compose | --env | --php]
# Author: Stefan Pejcic
# Created: 10.10.2023
# Last Modified: 24.09.2026
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
. /usr/local/opencli/lib/redis.sh
# shellcheck disable=SC1091
. /usr/local/opencli/lib/requirement.sh

# ---------------------- CONSTANTS ---------------------- #
readonly COMPOSE_FILE="/root/docker-compose.yml"
readonly LOG_FILE="/var/log/openpanel/admin/notifications.log"
readonly CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
readonly SKIP_VERSIONS_FILE="/etc/openpanel/upgrade/skip_versions"
readonly KEEP_KERNELS=2
readonly UPDATE_TIMEOUT=300

# ---------------------- COLOR CODES ---------------------- #
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly CYAN='\033[0;36m'
readonly NC='\033[0m' # no color/restart

# ---------------------- LOGGING FUNCTIONS ---------------------- #
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
}

log_debug() {
    echo -e "${BLUE}[DEBUG]${NC} $1"
}

print_header() {
    local title="$1"
    echo -e "${BLUE}===== ${title} =====${NC}"
}

# ---------------------- USAGE ---------------------- #
usage() {
    cat << EOF
Usage: opencli update [OPTION]...

Options:
    --check             Check if update is available
    --force             Force update even when autopatch/autoupdate is disabled
    (no argument)       Update if autopatch/autoupdate is enabled
    --admin             Update OpenAdmin UI only
    --panel             Update OpenPanel UI only
    --cli               Update OpenCLI only
    --translations      Update translation files and restart OpenPanel UI
    --system            Update system packages and kernel, purge older kernels, check if reboot required
    --modules           Update OpenAdmin modules/features list
    --compose           Update docker-compose.yml template in /etc/openpanel/docker/compose/1.0/
    --env               Update .env template in /etc/openpanel/docker/compose/1.0/
    --php               Update /etc/openpanel/php/ and add new PHP versions to the template and all users
    -h, --help          Show this help message

Examples:
    opencli update                 # Update if auto-update is enabled
    opencli update --check         # Check for available updates
    opencli update --force         # Force update regardless of settings
    opencli update --panel beta    # Update OpenPanel UI to the nightly-release
    opencli update --translations  # Update translation files and restart OpenPanel UI
    opencli update --system        # Update system packages and kernel
    opencli update --compose --env # Update both user templates
    opencli update --php           # Add new PHP versions to all users

Multiple options can be combined and run in the order given.
EOF
    exit 1
}

# ---------------------- HELPER ---------------------- #
# shellcheck disable=SC2329
command_exists() {
    command -v "$1" &> /dev/null
}


# ---------------------- DOCKER IMAGE ---------------------- #
# added in 1.7.42 to detect if custom image is used
IMAGE_NAME=$(podman-compose -f "$COMPOSE_FILE" config 2>/dev/null | awk '
  $1=="openpanel:" {f=1; next}
  f && $1=="image:" {
    split($2, arr, ":")
    print arr[1]
    exit
  }
')

if [ -z "$IMAGE_NAME" ]; then
    IMAGE_NAME="openpanel/openpanel"  # fallback
fi


# ---------------------- INSTALL PACKAGE ---------------------- #
# shellcheck disable=SC2329
install_package() {
    local package="$1"
    local quiet="${2:-false}"
    
    if [[ "$quiet" == "true" ]]; then
        local output_redirect="> /dev/null 2>&1"
    else
        local output_redirect=""
        log_info "Installing $package"
    fi
    
    if command_exists apt-get; then
        eval "apt-get update $output_redirect && apt-get install -y $package $output_redirect"
    elif command_exists dnf; then
        eval "dnf install -y $package $output_redirect"
    elif command_exists yum; then
        eval "yum install -y $package $output_redirect"
    else
        log_error "No supported package manager found (apt/dnf/yum)"
        return 1
    fi
}

# ---------------------- HELPERS ---------------------- #
get_last_message_content() {
    tail -n 1 "$LOG_FILE" 2>/dev/null || echo ""
}

is_unread_message_present() {
    local unread_message_content="$1"
    grep -q "UNREAD.*$unread_message_content" "$LOG_FILE" 2>/dev/null
}

write_notification() {
    local title="$1"
    local message="$2"
    local current_message
    current_message="$(date '+%Y-%m-%d %H:%M:%S') UNREAD $title MESSAGE: $message"
    mkdir -p "$(dirname "$LOG_FILE")"
    echo "$current_message" >> "$LOG_FILE"
}

write_notification_for_update_check() {
    local title="$1"
    local message="$2"
    local last_message_content
    last_message_content=$(get_last_message_content)
    if [[ "$message" != "$last_message_content" ]] && ! is_unread_message_present "$title"; then
        write_notification "$title" "$message"
    fi
}

remove_notifications_by_pattern() {
    local pattern="$1"
    if [[ -f "$LOG_FILE" ]]; then
        sed -i "/$pattern/d" "$LOG_FILE"
    fi
}

# ---------------------- GET CURRENT ---------------------- #
get_local_version() {
    opencli version 2>/dev/null || echo "0.0.0"
}

# ---------------------- GET AVAILABLE ---------------------- #
get_remote_version() {
    local beta="${1:-false}"
    local tags
    tags=$(curl -s "https://hub.docker.com/v2/repositories/${IMAGE_NAME}/tags" | jq -r '.results[].name' 2>/dev/null)

    if [[ -z "$tags" ]]; then
        return 1
    fi

    if [[ "$beta" == "true" ]]; then
        echo "$tags" | grep -- '-beta$' | sed 's/-beta$//' | sort -V | tail -n 1
    else
        echo "$tags" | grep -v '^latest$' | grep -v -- '-beta$' | sort -V | tail -n 1
    fi
}

# ---------------------- COMPARE, DUH! ---------------------- #
compare_versions() {
    local version1="$1"
    local version2="$2"
    if [[ "$version1" == "$version2" ]]; then
        echo 0
        return
    fi

    if [[ "$(printf '%s\n%s' "$version1" "$version2" | sort -V | head -n1)" == "$version1" ]]; then
        echo -1
    else
        echo 1
    fi
}

# ---------------------- USERS CAN SET VERSION TO BE SKIPPED ---------------------- #
is_version_skipped() {
    local version="$1"
    [[ -f "$SKIP_VERSIONS_FILE" ]] && grep -q "$version" "$SKIP_VERSIONS_FILE"
}

# ---------------------- READ AUTOPATCH AND AUTOUPDATE VALUES ---------------------- #
get_config_value() {
    local key="$1"
    local default_value="$2"
    
    if [[ -f "$CONFIG_FILE" ]]; then
        awk -F= "/^$key=/{print \$2}" "$CONFIG_FILE" | tr -d '"' | head -1
    else
        echo "$default_value"
    fi
}



# ---------------------- CHECK IF UPDATE IS AVAILABLE ---------------------- #
update_check() {
    local local_version
    local remote_version
    
    local_version=$(get_local_version)
    local_version="${local_version%-beta}" # strip '-beta'
    remote_version=$(get_remote_version)
    
    if [[ -z "$remote_version" ]]; then
        write_notification_for_update_check "Update check failed" "Failed connecting to Docker Hub"
        echo '{"error": "Error fetching remote version"}' >&2
        exit 1
    fi

    if [[ "$remote_version" == *-beta ]]; then
        exit 0
    fi

    local comparison
    comparison=$(compare_versions "$local_version" "$remote_version")
    
    case $comparison in
		0)
		    echo '{"status": "Up to date", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
		    ;;
        1)
            echo '{"status": "Local version is greater", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
            ;;
        -1)
            if is_version_skipped "$remote_version"; then
                echo '{"status": "Skipped version", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
            else
                write_notification_for_update_check "New OpenPanel update is available" "Installed version: $local_version | Available version: $remote_version"
                echo '{"status": "Update available", "installed_version": "'"$local_version"'", "latest_version": "'"$remote_version"'"}'
            fi
            ;;
    esac
}

# ---------------------- HELPERS USED IN MAJOR UPDATES ONLY ---------------------- #
# shellcheck disable=SC2329
detect_system() {
    if command_exists apt; then
        echo "debian"
    elif command_exists dnf; then
        echo "dnf"
    elif command_exists yum; then
        echo "yum"
    else
        log_error "[✘] Unsupported Linux distribution"
        exit 1
    fi
}

# ---------------------- RUNS ONLY ON MAJOR ---------------------- #
# shellcheck disable=SC2329
install_required_tools() {
    local distro
    distro=$(detect_system)
    log_info "Installing required system tools"
    case $distro in
        debian)
            if ! dpkg -s deborphan &>/dev/null; then
                install_package deborphan true
            fi
            ;;
        dnf|yum)
            if ! command_exists package-cleanup || ! command_exists needs-restarting; then
                install_package yum-utils true
            fi
            ;;
    esac
}

# ---------------------- RUNS ONLY ON MAJOR ---------------------- #
# shellcheck disable=SC2329
remove_old_kernels() {
    local distro
    distro=$(detect_system)
    case $distro in
        debian)
            log_info "Removing old kernels (Debian/Ubuntu)"
            local current_kernel
            current_kernel=$(uname -r)
            
            local kernels
            mapfile -t kernels < <(dpkg --list | grep linux-image | awk '{print $2}' | grep -v "$current_kernel" | sort -V)
            
            if (( ${#kernels[@]} > KEEP_KERNELS )); then
                local remove_count=$((${#kernels[@]} - KEEP_KERNELS))
                local remove_kernels=("${kernels[@]:0:$remove_count}")
                apt purge -y "${remove_kernels[@]}" >> "$log_file" 2>&1
            fi
            ;;
        dnf|yum)
            log_info "Removing old kernels (RHEL/CentOS)"
            if command_exists package-cleanup; then
                package-cleanup --oldkernels --count="$KEEP_KERNELS" -y >> "$log_file" 2>&1
            fi
            ;;
    esac
}

# ---------------------- RUNS ONLY ON MAJOR ---------------------- #
# shellcheck disable=SC2329
update_system_packages() {
    local distro
    distro=$(detect_system)
    log_info "Updating system packages"
    case $distro in
        debian)
            apt update >> "$log_file" 2>&1
            apt upgrade -y >> "$log_file" 2>&1
            apt full-upgrade -y >> "$log_file" 2>&1
            
            log_info "Cleaning up packages"
            apt autoremove -y >> "$log_file" 2>&1
            apt autoclean -y >> "$log_file" 2>&1
            apt clean >> "$log_file" 2>&1
            
            if command_exists deborphan; then
                deborphan | xargs -r apt purge -y >> "$log_file" 2>&1
            fi
            
            dpkg -l | awk '/^rc/ { print $2 }' | xargs -r apt purge -y >> "$log_file" 2>&1
            ;;
        dnf|yum)
            local pkg_mgr="$distro"
            $pkg_mgr -y update >> "$log_file" 2>&1
            
            log_info "Cleaning up packages"
            $pkg_mgr -y autoremove >> "$log_file" 2>&1
            $pkg_mgr clean all >> "$log_file" 2>&1
            
            if command_exists package-cleanup; then
                package-cleanup --leaves --all --quiet | xargs -r "$pkg_mgr" remove -y >> "$log_file" 2>&1
            fi
            ;;
    esac
}

# ---------------------- RUNS ONLY ON MAJOR ---------------------- #
# shellcheck disable=SC2329
check_reboot_required() {
    log_info "Checking if reboot is required"
    local distro
    distro=$(detect_system)
    case $distro in
        debian)
            if [[ -f /var/run/reboot-required ]]; then
                log_warn "[!]  Reboot required!"
                [[ -f /var/run/reboot-required.pkgs ]] && cat /var/run/reboot-required.pkgs >> "$log_file"
            fi
            ;;
        dnf|yum)
            if command_exists needs-restarting && ! needs-restarting -r &>/dev/null; then
                log_warn "[!] Reboot required!"
                needs-restarting >> "$log_file" 2>&1
            fi
            ;;
    esac
}

# ---------------------- HELPER: RUNS AFTER DOWNLOADING NEW IMAGE ---------------------- #
purge_previous_images() {
    log_info "Cleaning up old Podman images"
    local all_images
    all_images=$(podman images --format "{{.Repository}} {{.ID}}" | grep "^docker.io/$IMAGE_NAME" | awk '{print $2}')
    local used_images
    used_images=$(podman ps --format "{{.Image}}" | xargs -n1 podman inspect --format '{{.Id}}' 2>/dev/null | sort | uniq)
    for img in $all_images; do
        if echo "$used_images" | grep -q "$img"; then
            log_debug "Skipping in-use image: $img"
        else
            log_info "Removing unused image: $img"
            podman rmi "$img" 2>/dev/null || true
        fi
    done
}

# ---------------------- RUNS AFTER UPDATE ---------------------- #
run_custom_postupdate_script() {
    local script_path="/root/openpanel_run_after_update"
    
    if [[ -s "$script_path" ]]; then
        log_info "[!] Running custom post-update script: $script_path"
        bash "$script_path" 2>&1 | tee -a "$log_file"
    fi
}

# ---------------------- RUNS AFTER UPDATE ---------------------- #
update_modules() {
    local no_log="${1:-}"
    local msg="Updating OpenAdmin modules"
    # shellcheck disable=SC2015  # echo can't meaningfully fail here; used as if/else shorthand
    [[ "$no_log" == "--no-log" ]] && echo "$msg" || log "$msg"
    local url="https://raw.githubusercontent.com/stefanpejcic/openpanel-configuration/refs/heads/main/openadmin/config/features.json"

    if wget --spider -q "$url" 2>/dev/null; then
        if wget -q -O "/etc/openpanel/openadmin/config/features.json" "$url"; then
            (( updated++ ))
        else
            log_warn "Failed to download features from github"
            (( failed++ ))
        fi
    else
        log_warn "No features file found: $url (skipping)"
    fi
    
}


update_compose_template() {
    local file="$1"
    local dir="/etc/openpanel/docker/compose/1.0"
    local url="https://raw.githubusercontent.com/stefanpejcic/openpanel-configuration/refs/heads/main/docker/compose/1.0/$file"
    local tmp
    tmp=$(mktemp)

    log_info "Updating $dir/$file"
    if wget --timeout=10 --tries=1 -q -O "$tmp" "$url" && [[ -s "$tmp" ]]; then
        mkdir -p "$dir"
        # keep a copy in case the new template breaks something
        [[ -f "$dir/$file" ]] && cp -f "$dir/$file" "$dir/$file.bak" && log_info "Previous file saved as $dir/$file.bak"
        mv -f "$tmp" "$dir/$file"
        chmod 644 "$dir/$file"
        log_info "[✔] $file updated"
    else
        rm -f "$tmp"
        log_error "Failed to download $url"
        return 1
    fi
}

# ---------------------- PHP: SYNC FILES AND ADD NEW PHP VERSIONS ---------------------- #
# prints X.Y for every php-fpm-X.Y service in a compose file
php_versions_in_compose() {
    sed -n 's/^  php-fpm-\([0-9][0-9.]*\):[[:space:]]*$/\1/p' "$1" | sort -uV
}

# prints the whole service block, trailing blank lines stripped
extract_service_block() {
    awk -v svc="$2" '
        $0 ~ "^  " svc ":[ \t]*$" { f=1; print; next }
        f && /^  [^ #]/ { exit }
        f && /^[^ \t#]/ { exit }
        f { print }
    ' "$1" | tr -d '\r' | sed -e :a -e '/^[[:space:]]*$/{$d;N;ba' -e '}'
}

# line after which a new php-fpm block goes: end of the last php-fpm service, or end of services
php_insert_line() {
    awk '
        /^services:/ { s=1; next }
        s && /^[^ \t#]/ { s=0 }
        s && /^  [^ #]/ { php = ($0 ~ /^  php-fpm-/) }
        s && NF { last=NR; if (php) lastphp=NR }
        END { print (lastphp ? lastphp : last) }
    ' "$1"
}

# inserts the contents of $3 after line $2 of $1, optionally with a blank line in front
insert_after_line() {
    local file="$1" line="$2" block="$3" gap="${4:-true}"
    local tmp
    tmp=$(mktemp)
    awk -v n="$line" -v blk="$block" -v gap="$gap" '
        function dump() { if (gap=="true") print ""; while ((getline l < blk) > 0) print l; done=1 }
        { print } NR==n { dump() }
        END { if (!done) dump() }
    ' "$file" > "$tmp"
    # cat keeps owner and permissions of the original file
    cat "$tmp" > "$file"
    rm -f "$tmp"
}

# adds missing php-fpm services from $1 to $2, sets PHP_ADDED to what was added
merge_php_compose() {
    local src="$1" dst="$2" v block line
    PHP_ADDED=()
    local have
    have=$(php_versions_in_compose "$dst")
    block=$(mktemp)
    for v in $(php_versions_in_compose "$src"); do
        grep -qxF "$v" <<< "$have" && continue
        extract_service_block "$src" "php-fpm-$v" > "$block"
        [[ -s "$block" ]] || continue
        line=$(php_insert_line "$dst")
        insert_after_line "$dst" "$line" "$block"
        PHP_ADDED+=("php-fpm-$v")
    done
    rm -f "$block"
}

# adds missing PHP_FPM_X_Y_* vars from $1 to $2, sets PHP_ADDED to what was added
merge_php_env() {
    local src="$1" dst="$2" v key line block
    PHP_ADDED=()
    block=$(mktemp)
    for v in $3; do
        key="PHP_FPM_${v//./_}_"
        : > "$block"
        local missing=()
        while IFS= read -r l; do
            grep -q "^${l%%=*}=" "$dst" || missing+=("$l")
        done < <(grep "^${key}" "$src" | tr -d '\r')
        [[ ${#missing[@]} -eq 0 ]] && continue

        if grep -q "^${key}" "$dst"; then
            line=$(grep -n "^${key}" "$dst" | tail -n1 | cut -d: -f1)
            printf '%s\n' "${missing[@]}" > "$block"
            insert_after_line "$dst" "$line" "$block" false
        else
            line=$(grep -n "^PHP_FPM_" "$dst" | tail -n1 | cut -d: -f1)
            { echo "# PHP $v"; printf '%s\n' "${missing[@]}"; } > "$block"
            insert_after_line "$dst" "${line:-0}" "$block"
        fi
        PHP_ADDED+=("${missing[@]%%=*}")
    done
    rm -f "$block"
}

# prints "lineno<TAB>line" for each volume entry of a service
service_volumes() {
    awk -v svc="$2" '
        $0 ~ "^  " svc ":[ \t]*$" { f=1; next }
        f && (/^  [^ #]/ || /^[^ \t#]/) { exit }
        f && /^    volumes:/ { v=1; next }
        f && v && /^    [^ ]/ { v=0 }
        f && v && /^      - / { print NR "\t" $0 }
    ' "$1" | tr -d '\r'
}

# container side of a volume line, "- ./a:/b:ro" -> /b
volume_target() {
    local v="${1#*- }"
    v="${v//\"/}"
    v="${v//\'/}"
    cut -d: -f2 <<< "$v"
}

# adds volumes of service $3 from $1 that are missing in $2, only lines matching glob $4, sets PHP_ADDED
merge_service_mounts() {
    local src="$1" dst="$2" svc="$3" pattern="$4" line target block vols at source
    PHP_ADDED=()
    grep -q "^  $svc:[[:space:]]*$" "$dst" || return 0

    block=$(mktemp)
    while IFS= read -r line; do
        # shellcheck disable=SC2053  # $pattern is a glob on purpose
        [[ "$line" == $pattern ]] || continue
        vols=$(service_volumes "$dst" "$svc")
        [[ -n "$vols" ]] || break
        target=$(volume_target "$line")
        cut -f2- <<< "$vols" | while IFS= read -r l; do volume_target "$l"; done | grep -qxF "$target" && continue

        # put it next to mounts from the same dir, else at the end of volumes
        source=$(sed 's/^ *- *//; s/:.*//' <<< "$line")
        at=$(grep -F -- "- ${source%/*}/" <<< "$vols" | tail -n1 | cut -f1)
        [[ -n "$at" ]] || at=$(tail -n1 <<< "$vols" | cut -f1)

        echo "$line" > "$block"
        insert_after_line "$dst" "$at" "$block" false
        PHP_ADDED+=("$svc:$source")
    done < <(service_volumes "$src" "$svc" | cut -f2-)
    rm -f "$block"
}

# adds missing php mounts to openlitespeed and missing ionCube mounts to existing php-fpm services, sets PHP_ADDED
merge_php_mounts() {
    local src="$1" dst="$2" v added=()
    merge_service_mounts "$src" "$dst" openlitespeed '*php*'
    added+=("${PHP_ADDED[@]}")
    for v in $(php_versions_in_compose "$dst"); do
        merge_service_mounts "$src" "$dst" "php-fpm-$v" '*ioncube*'
        added+=("${PHP_ADDED[@]}")
    done
    PHP_ADDED=("${added[@]}")
}

compose_config_valid() {
    (cd "$1" && podman-compose config > /dev/null 2>&1)
}

update_php() {
    require_command podman-compose
    local repo_url="https://github.com/stefanpejcic/openpanel-configuration/archive/refs/heads/main.tar.gz"
    local template_dir="/etc/openpanel/docker/compose/1.0"
    local ts tmp_dir src versions
    ts=$(date +%Y%m%d_%H%M%S)
    tmp_dir=$(mktemp -d)

    print_header "Updating PHP"

    # 1. fresh copy of the configuration repo
    log_info "Downloading configuration files from github"
    if ! wget --timeout=30 --tries=2 -q -O "$tmp_dir/cfg.tar.gz" "$repo_url" || ! tar -xzf "$tmp_dir/cfg.tar.gz" -C "$tmp_dir"; then
        log_error "Failed to download $repo_url"
        rm -rf "$tmp_dir"
        return 1
    fi
    src=$(find "$tmp_dir" -mindepth 1 -maxdepth 1 -type d | head -n1)
    if [[ ! -d "$src/php" || ! -f "$src/docker/compose/1.0/docker-compose.yml" || ! -f "$src/docker/compose/1.0/.env" ]]; then
        log_error "Downloaded archive is missing php/ or docker/compose/1.0/ files"
        rm -rf "$tmp_dir"
        return 1
    fi
    local remote_compose="$src/docker/compose/1.0/docker-compose.yml"
    local remote_env="$src/docker/compose/1.0/.env"
    versions=$(php_versions_in_compose "$remote_compose")
    log_info "PHP versions on github: $(echo "$versions" | tr '\n' ' ')"

    # 2. overwrite everything in /etc/openpanel/php/
    log_info "Overwriting /etc/openpanel/php/"
    mkdir -p /etc/openpanel/php
    cp -rf "$src/php/." /etc/openpanel/php/
    log_info "[✔] /etc/openpanel/php/ updated"

    # 3. template files, then 4. every user, same steps for both
    local total=0 changed=0 unchanged=0 failed=0 skipped=0
    local summary=()

    # merge into $1 dir, $2 is a label for output, sets PHP_RESULT
    merge_php_dir() {
        local dir="$1" label="$2"
        local compose="$dir/docker-compose.yml" env="$dir/.env"
        local added=()

        if [[ ! -f "$compose" || ! -f "$env" ]]; then
            log_warn "[$label] docker-compose.yml or .env missing, skipping"
            PHP_RESULT="skipped"; return
        fi
        if ! compose_config_valid "$dir"; then
            log_warn "[$label] config is already invalid before changes, skipping"
            PHP_RESULT="skipped"; return
        fi

        cp -p "$compose" "$compose.php_$ts.bak"
        cp -p "$env" "$env.php_$ts.bak"

        merge_php_compose "$remote_compose" "$compose"
        added+=("${PHP_ADDED[@]}")
        merge_php_env "$remote_env" "$env" "$versions"
        added+=("${PHP_ADDED[@]}")
        merge_php_mounts "$remote_compose" "$compose"
        local mounts_added=("${PHP_ADDED[@]}")

        if [[ ${#added[@]} -eq 0 && ${#mounts_added[@]} -eq 0 ]]; then
            rm -f "$compose.php_$ts.bak" "$env.php_$ts.bak"
            log_info "[$label] up to date"
            PHP_RESULT="unchanged"; return
        fi

        if ! compose_config_valid "$dir"; then
            cp -p "$compose.php_$ts.bak" "$compose"
            cp -p "$env.php_$ts.bak" "$env"
            log_error "[$label] config invalid after changes, restored backup"
            PHP_RESULT="failed"; return
        fi

        # the new services mount ./php.ini/X.Y.ini so it has to exist
        if [[ -d "$dir/php.ini" ]]; then
            local v
            for v in $versions; do
                if [[ ! -f "$dir/php.ini/$v.ini" && -f "/etc/openpanel/php/ini/$v.ini" ]]; then
                    cp "/etc/openpanel/php/ini/$v.ini" "$dir/php.ini/$v.ini"
                    chown --reference="$dir/php.ini" "$dir/php.ini/$v.ini"
                    added+=("php.ini/$v.ini")
                fi
            done
        fi

        PHP_RESULT="changed:"
        if [[ ${#added[@]} -gt 0 ]]; then
            log_info "[$label] added: ${added[*]}"
            PHP_RESULT+=" ${added[*]}"
        fi
        if [[ ${#mounts_added[@]} -gt 0 ]]; then
            log_info "[$label] mounts added: ${mounts_added[*]}"
            PHP_RESULT+=" | mounts: ${mounts_added[*]}"
        fi
        log_info "[$label] backups: $compose.php_$ts.bak, $env.php_$ts.bak"
    }

    log_info "Checking template in $template_dir"
    merge_php_dir "$template_dir" "template"
    summary+=("template: $PHP_RESULT")

    local home user
    for home in /home/*/; do
        home="${home%/}"
        [[ -f "$home/docker-compose.yml" ]] || continue
        user=$(basename "$home")
        (( total++ ))
        log_info "Checking user $user ($total)"
        merge_php_dir "$home" "$user"
        case "$PHP_RESULT" in
            unchanged) (( unchanged++ )) ;;
            skipped)   (( skipped++ )); summary+=("$user: skipped") ;;
            failed)    (( failed++ ));  summary+=("$user: failed, restored") ;;
            *)         (( changed++ )); summary+=("$user: $PHP_RESULT") ;;
        esac
    done

    rm -rf "$tmp_dir"

    print_header "PHP update summary"
    local s
    for s in "${summary[@]}"; do
        echo "  $s"
    done
    echo "  Users checked: $total | updated: $changed | up to date: $unchanged | skipped: $skipped | failed: $failed"
    [[ $failed -eq 0 ]]
}

update_locales() {
    local no_log="${1:-}"
    local msg="Updating installed locales"
    # shellcheck disable=SC2015  # echo can't meaningfully fail here; used as if/else shorthand
    [[ "$no_log" == "--no-log" ]] && echo "$msg" || log "$msg"

    local babel_translations="/etc/openpanel/openpanel/translations"
    local github_repo="stefanpejcic/openpanel-translations"

    if [[ ! -d "$babel_translations" ]]; then
        # shellcheck disable=SC2015  # echo can't meaningfully fail here; used as if/else shorthand
        [[ "$no_log" == "--no-log" ]] && echo "[!] No translations directory found, skipping" || log "[!] No translations directory found, skipping"
        return 0
    fi

    local updated=0
    local failed=0

    for po_file in "$babel_translations"/*/LC_MESSAGES/messages.po; do
        [[ -f "$po_file" ]] || continue

        local two_letter
        two_letter=$(echo "$po_file" | awk -F'/' '{for(i=1;i<=NF;i++) if($i=="translations") print $(i+1)}')

        local formatted_locale="${two_letter}-${two_letter}"
        local url="https://raw.githubusercontent.com/${github_repo}/main/${formatted_locale}/messages.po"

        if wget --spider -q "$url" 2>/dev/null; then
            if wget -q -O "$po_file" "$url"; then
                (( updated++ ))
            else
                log_warn "Failed to download locale: $formatted_locale"
                (( failed++ ))
            fi
        else
            log_warn "No remote locale found for: $formatted_locale (skipping)"
        fi
    done

    if [[ $updated -gt 0 ]]; then
        local compile_msg="Compiling updated .mo files"
        # shellcheck disable=SC2015  # echo can't meaningfully fail here; used as if/else shorthand
        [[ "$no_log" == "--no-log" ]] && echo "$compile_msg" || log "$compile_msg"
        podman exec openpanel sh -c "pybabel compile -f -d $babel_translations &>/dev/null"
        redis_drop_key openpanel_cache_app.get_available_locales_memver &>/dev/null
    fi

    local summary="Locales updated: $updated, failed: $failed"
    # shellcheck disable=SC2015  # echo can't meaningfully fail here; used as if/else shorthand
    [[ "$no_log" == "--no-log" ]] && echo "[✔] $summary" || log "[✔] $summary"
}

# ---------------------- UPDATES TRANSLATIONS ONLY, THEN RESTARTS OPENPANEL ---------------------- #
update_translations() {
    update_locales --no-log
    log_info "Restarting OpenPanel service"
    podman restart openpanel &>/dev/null 2>&1
    log_info "[✔] Translations updated and OpenPanel restarted"
}

# ---------------------- CHECKS IF CUSTOM FILE EXISTS AND RUNS IT ---------------------- #

# https://github.com/stefanpejcic/OpenPanel/issues/984
run_version_specific_scripts_in_range() {
    local from_version="$1"
    local to_version="$2"

    local from_major from_minor from_patch
    local to_major to_minor to_patch

    IFS='.' read -r from_major from_minor from_patch <<< "$from_version"
    IFS='.' read -r to_major to_minor to_patch <<< "$to_version"

    log_info "Running version-specific scripts from $from_version to $to_version"

    local maj min pat
    for (( maj=from_major; maj<=to_major; maj++ )); do
        local min_start=0
        local min_end=999
        [[ $maj -eq from_major ]] && min_start=$from_minor
        [[ $maj -eq to_major ]]   && min_end=$to_minor

        for (( min=min_start; min<=min_end; min++ )); do
            local pat_start=0
            local pat_end=999
            [[ $maj -eq from_major && $min -eq from_minor ]] && pat_start=$(( from_patch + 1 ))
            [[ $maj -eq to_major   && $min -eq to_minor   ]] && pat_end=$to_patch

            for (( pat=pat_start; pat<=pat_end; pat++ )); do
                run_version_specific_script "${maj}.${min}.${pat}"
            done
        done
    done
}

run_version_specific_script() {
    local version="$1"
    local url="https://raw.githubusercontent.com/stefanpejcic/OpenPanel/refs/heads/main/UPDATES/$version/UPDATE.sh"
    log_info "Checking for version-specific update script"
    if wget --spider -q "$url" 2>/dev/null; then
        log_info "Downloading and executing version-specific script: $url"
        if timeout "$UPDATE_TIMEOUT" bash -c "wget --timeout=1 --tries=1 -q -O - '$url' | bash" &>> "$log_file"; then
            log_info "[✔] Version-specific script executed successfully"
        else
            local exit_code=$?
            if [[ $exit_code -eq 124 ]]; then
                log_error "[!] Version-specific script timed out after ${UPDATE_TIMEOUT} seconds"
            else
                log_error "[!] Version-specific script failed with exit code: $exit_code"
            fi
        fi
    else
        log_info "[✔] No version-specific script"
    fi
}


update_system() {
	: "${log_file:=/var/log/openpanel/updates/system_$(date +%Y%m%d_%H%M%S).log}"
	mkdir -p "$(dirname "$log_file")"
	log_info "Updating system packages"

	if [[ "$1" != "-y" ]]; then
		read -t 10 -p "System package update ready to proceed. A full server backup is recommended beforehand. Continue? [y/N] " confirm || { echo; log_info "No response, aborting update"; return 1; }
		[[ "$confirm" =~ ^[yY]([eE][sS])?$ ]] || { log_info "System upgrade cancelled"; return 1; }
	fi

	install_required_tools
	update_system_packages
	remove_old_kernels
	check_reboot_required
}

# ---------------------- MAIN UPDATE FUNCTION - THIS IS WHERE MAGIC HAPPENS ---------------------- #
run_update_immediately() {
    local version="$1"
    local log_dir="/var/log/openpanel/updates"
    
    # ---------------------- 1. PREPARATION
    mkdir -p "$log_dir"
    log_file="$log_dir/$version.log"
    if [[ -f "$log_file" ]]; then
        local timestamp
        timestamp=$(date +"%Y%m%d_%H%M%S")
        log_file="${log_dir}/${version}_${timestamp}.log"
    fi
    
    touch "$log_file"
    
    log() {
        echo "" >> "$log_file"
        echo -e "${CYAN}---- ${1} ----${NC}" | tee -a "$log_file"
    }

    # ---------------------- 2. START PROCESS, WRITE NOTIFICATION
    print_header "Starting update to version $version"
    log_info "Update log: $log_file"
    write_notification "OpenPanel update started" "Started update to version $version - Log file: $log_file"

    # ---------------------- 3. DOWNLOAD BASH SCRIPTS FROM GITHUB (must be befor openpanel image update)
    update_opencli
	
    # ---------------------- 4. DOWNLOAD NEW IMAGE FROM DOCKER HUB
    log "Updating OpenPanel container image"
    if ! timeout 60 podman image pull "${IMAGE_NAME}:${version}" 2>&1 | tee -a "$log_file"; then
        log_error "Failed to pull image or command timed out: podman image pull ${IMAGE_NAME}:${version}"
        remove_notifications_by_pattern "OpenPanel update started MESSAGE"
        write_notification "OpenPanel update failed!" "OpenPanel failed to update to version $version - Log file: $log_file"
        log "Update failed!"
        return 1
    else
        log "[✔] image ${IMAGE_NAME}:${version} downloaded successfully"

        log "Updating version in /root/.env"
        if [[ -f /root/.env ]]; then
            sed -i "s/^VERSION=.*$/VERSION=\"$version\"/" /root/.env
        fi

        log "Restarting OpenPanel service"
        if [[ -f /root/docker-compose.yml ]] || [[ -f /root/compose.yml ]]; then
            cd /root && podman-compose down openpanel && \
            podman-compose up -d openpanel 2>&1 | tee -a "$log_file"
        fi

        log "Cleaning up previous images"
        purge_previous_images
    fi

    # ---------------------- 5. UPDATE INSTALLED LOCALES
    update_locales  

    # ---------------------- 6. UPDATE MODULES/FEATURES
    update_modules 

    # ---------------------- 7. DOWNLOAD OPENADMIN FILES FROM GITHUB
    update_openadmin

    # ---------------------- 8. RUN VERSION-SPECIFIC FILES IF EXIST
    run_version_specific_scripts_in_range "$local_version" "$version"

    # ---------------------- 9. IF MAJOR VERSION, ALSO UPDATE SYSTEM, DOCKER AND KERNEL     
    current_major=$(echo "$local_version" | cut -d. -f1)
    new_major=$(echo "$version" | cut -d. -f1)
    if [[ "$current_major" -lt "$new_major" ]]; then
		update_system "-y"
    else
        log "[✔] Minor update - skipping system updates"
    fi

    # ---------------------- 10. RUN POST-UPDATE HOOK IF EXISTS        
    log "Checking for custom post-update scripts"
    run_custom_postupdate_script
    
    # ---------------------- 11. CLEANUP     
    remove_notifications_by_pattern "OpenPanel update started MESSAGE"
    remove_notifications_by_pattern "New OpenPanel update is available"
    write_notification "OpenPanel updated successfully!" "OpenPanel updated to version $version - Log file: $log_file"
    log "Update completed successfully!"

}



update_openpanel() {
    cd /root || return
    if [[ "$BETA" == "true" ]]; then
        require_command jq
        local base_version
        base_version=$(get_remote_version true)

        if [[ -z "$base_version" ]]; then
            log_error "Could not find a beta image on Docker Hub for ${IMAGE_NAME}"
            return 1
        fi

        log_info "Latest beta version found: ${base_version}-beta"
        if [[ -f /root/.env ]]; then
            sed -i "s/^VERSION=.*/VERSION=\"${base_version}-beta\"/" /root/.env
        fi
    fi
    podman-compose up -d openpanel --force-recreate --pull
}

update_openadmin() {
    # shellcheck disable=SC2015
    [[ "$1" == "--no-log" ]] && echo "Updating OpenAdmin" || log "Updating OpenAdmin"
    if [[ -d /usr/local/admin ]]; then
        cd /usr/local/admin || return

        [[ -f "/usr/local/admin/templates/emails/reports.html" ]] && cp /usr/local/admin/templates/emails/reports.html /tmp/report.html.backup

        local remote_version admin_binary url target_log
        case "$(uname -m)" in
            x86_64|amd64)  admin_binary="openadmin-amd64" ;;
            aarch64|arm64) admin_binary="openadmin-arm64" ;;
            *)             admin_binary="$(uname -m)" ;;
        esac

        remote_version=$(opencli update --check 2>/dev/null | jq -r '.latest_version' 2>/dev/null)
        url="https://github.com/stefanpejcic/openadmin/releases/download/$remote_version/$admin_binary"
        target_log="${log_file:-/dev/null}"

        if curl -sSLI -o /dev/null -w "%{http_code}" "$url" | grep -q "^200$"; then
			systemd-run --collect --unit="openadmin-selfupdate-$$" --no-block bash -c '
			    admin_binary="'"$admin_binary"'"
			    url="'"$url"'"
			    target_log="'"$target_log"'"
			    was_active=false
			
			    curl -sSL "$url" -o "/usr/local/admin/${admin_binary}.new" || exit 1
			    chmod +x "/usr/local/admin/${admin_binary}.new"
			
			    if systemctl is-active --quiet admin; then
			        was_active=true
			        if ! timeout 30 systemctl stop admin; then
			            systemctl kill -s SIGKILL admin
			            systemctl reset-failed admin
			        fi
			    fi
			
			    mv -f "/usr/local/admin/${admin_binary}.new" "/usr/local/admin/$admin_binary"
			
			    [[ -f "/tmp/report.html.backup" ]] && cp /tmp/report.html.backup /usr/local/admin/templates/emails/reports.html
			
			    if [ "$was_active" = true ]; then
			        systemctl start admin
			        sleep 2
			        if ! systemctl is-active --quiet admin; then
			            {
			                echo "[$(date "+%Y-%m-%d %H:%M:%S")] admin service failed to start after update"
			                systemctl status admin --no-pager
			            } >> "$target_log" 2>&1
			        fi
			    fi
			'
        else
            echo "No release asset found: $url" >&2
            exit 1
        fi

        echo "[✔] OpenAdmin update triggered (applying in background)" || log "[✔] OpenAdmin update triggered (applying in background)"
    fi
}


update_opencli() {    
    message="Updating OpenCLI"
    # shellcheck disable=SC2015
    [[ "$1" == "--no-log" ]] && echo "$message" || log "$message"

    local url="https://github.com/stefanpejcic/opencli/archive/refs/heads/podman.tar.gz"
    local target_log="${log_file:-/dev/null}"
    # download next to /usr/local/opencli so the final swap is a same-filesystem mv
    local tmp_dir new_dir
    tmp_dir=$(mktemp -d /usr/local/.opencli_update.XXXXXX)
    if wget --spider -q "$url" 2>/dev/null; then
        log_info "Downloading terminal scripts from github: $url"
        if timeout "$UPDATE_TIMEOUT" bash -c "wget --timeout=30 --tries=3 -q -O '$tmp_dir/opencli.tar.gz' '$url' && tar -xzf '$tmp_dir/opencli.tar.gz' -C '$tmp_dir'" &>> "$target_log" \
            && new_dir=$(find "$tmp_dir" -mindepth 1 -maxdepth 1 -type d | head -n1) \
            && [[ -f "$new_dir/opencli" ]]; then
            # only replace the old scripts once the new ones are fully downloaded and extracted
            find "$new_dir" -type f -name '*.sh' -exec chmod +x {} +
            rm -rf /usr/local/opencli.old
            mv /usr/local/opencli /usr/local/opencli.old 2>/dev/null
            if mv "$new_dir" /usr/local/opencli; then
                rm -rf /usr/local/opencli.old
                log_info "[✔] Terminal commands updated successfully"
            else
                mv /usr/local/opencli.old /usr/local/opencli 2>/dev/null
                log_error "[!] Failed to replace /usr/local/opencli, kept the previous version."
            fi
        else
            local exit_code=$?
            if [[ $exit_code -eq 124 ]]; then
                log_error "[!] Updating terminal commands timed out after ${UPDATE_TIMEOUT} seconds, kept the previous version."
            else
                log_error "[!] Updating terminal commands failed with exit code: $exit_code, kept the previous version."
            fi
        fi
    else
        log_info "[!] Failed to reach github."
    fi
    rm -rf "$tmp_dir"

	# (re)install bash tab-completion for opencli
	if [[ -f /usr/local/opencli/lib/completion.bash ]]; then
		if ! rpm -q bash-completion &>/dev/null 2>&1 && ! dpkg -s bash-completion &>/dev/null 2>&1; then
			install_package "bash-completion" "true"
		fi
		if [[ -d /etc/bash_completion.d ]]; then
			ln -sf /usr/local/opencli/lib/completion.bash /etc/bash_completion.d/opencli
		fi
	fi

	[[ "$1" == "--no-log" ]] && podman restart openpanel &>/dev/null 2>&1
}

# Main update check and execution
check_update() {
    local force_update=false
    
    if [[ "$1" == "--force" ]]; then
        force_update=true
        log_info "[!] Forcing update, ignoring autopatch and autoupdate settings"
    fi
    
    require_command jq
    local autopatch autoupdate
    if [[ "$force_update" == "true" ]]; then
        autopatch="on"
        autoupdate="on"
    else
        autopatch=$(get_config_value "autopatch" "off")
        autoupdate=$(get_config_value "autoupdate" "off")
    fi
    
    if [[ "$autopatch" != "on" && "$autoupdate" != "on" && "$force_update" != "true" ]]; then
        log_info "[!] Autopatch and Autoupdate are both disabled. No updates will be installed automatically."
        return 0
    fi
    
    local local_version remote_version
    local_version=$(get_local_version)
    local_version="${local_version%-beta}" # strip '-beta'
    remote_version=$(opencli update --check 2>/dev/null | jq -r '.latest_version' 2>/dev/null)
    
    if [[ -z "$remote_version" || "$remote_version" == "null" ]]; then
        log_info "No update available or unable to check for updates"
        return 0
    fi
    
    if is_version_skipped "$remote_version"; then
        log_info "[!] Version $remote_version is skipped due to skip_versions configuration"
        return 0
    fi
    
    local comparison
    comparison=$(compare_versions "$local_version" "$remote_version")
    
    if [[ $comparison -eq -1 || "$force_update" == "true" ]]; then
        if [[ "$autoupdate" == "off" && "$force_update" == "false" ]]; then
            log_info "Update available, but only autopatch is enabled. Installing patch"
        else
            log_info "Update available and will be automatically installed"
        fi

    (
      flock -n 200 || { echo "[✘] Error: Update process is already running."; echo "Please wait for it to complete before retrying."; exit 1; }
      run_update_immediately "$remote_version"
    ) 200>/var/lock/openpanel_update.lock
        
    else
        log_info "[✔] No update needed"
    fi
}


# ---------------------- RUNS CHECK OR STARTS UPDATE ---------------------- #
main() {
    local modes=()
    for arg in "$@"; do
        case "$arg" in
            --check|--force|--admin|--panel|--cli|--translations|--system|--modules|--compose|--env|--php)
                # skip duplicates so each mode runs once
                [[ " ${modes[*]} " == *" ${arg#--} "* ]] || modes+=("${arg#--}") ;;
            beta)    BETA=true    ;;
            -h|--help) usage ;;
            *) log_error "[!] Unknown argument: $arg"; usage ;;
        esac
    done

    [[ ${#modes[@]} -eq 0 ]] && { check_update; return; }

    for mode in "${modes[@]}"; do
        case "$mode" in
            check) update_check ;;
            force) check_update --force ;;
            panel) update_openpanel --no-log ;;
            cli)   update_opencli --no-log ;;
            admin) update_openadmin --no-log ;;
            translations) update_translations ;;
            system) update_system ;;
            modules) update_modules --no-log ;;
            compose) update_compose_template docker-compose.yml ;;
            env) update_compose_template .env ;;
            php) update_php ;;
        esac
    done
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi

exit 0
