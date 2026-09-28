#!/bin/bash
# ======================================================================
# Rejects passwords found in the top 1M common passwords list from weakpass.com.
# Sourced by user/add.sh and user/password.sh, and run directly by OpenPanel:
#   echo "$PASSWORD" | bash /usr/local/opencli/lib/weakpass.sh   # exit 1 if common
#   bash /usr/local/opencli/lib/weakpass.sh --update              # just fetch the list
# ======================================================================

WEAKPASS_CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"
WEAKPASS_DICT="/tmp/weakpass_top1m.txt"
WEAKPASS_URL="https://weakpass.com/download/50/10_million_password_list_top_1000000.txt.gz"

# on unless the admin set weakpass=no
weakpass_enabled() {
    local value
    value=$(grep "^weakpass=" "$WEAKPASS_CONFIG_FILE" 2>/dev/null | cut -d= -f2- | tr -d '"'"'"'')
    [[ "$value" != "no" ]]
}

# downloads the list if missing or older than 7 days, keeps the old copy if the download fails
weakpass_update_dict() {
    [[ -s "$WEAKPASS_DICT" && -z $(find "$WEAKPASS_DICT" -mtime +7 -print -quit 2>/dev/null) ]] && return 0

    local tmp="${WEAKPASS_DICT}.tmp.$$"
    if wget -qO- "$WEAKPASS_URL" 2>/dev/null | gunzip -c > "$tmp" 2>/dev/null && [[ -s "$tmp" ]]; then
        mv -f "$tmp" "$WEAKPASS_DICT"
    else
        rm -f "$tmp"
        echo "[!] Warning: Could not fetch weak-password dictionary." >&2
    fi
}

# true if the check is on and the password is in the list, skips the check when the list can't be fetched
is_common_password() {
    weakpass_enabled || return 1
    weakpass_update_dict
    [[ -s "$WEAKPASS_DICT" ]] || return 1
    grep -qixF -- "$1" "$WEAKPASS_DICT" 2>/dev/null
}

# exits the calling script with an error if the password is a common one
require_not_common_password() {
    if is_common_password "$1"; then
        echo "ERROR: Password is in the list of common passwords. Use a stronger password or disable the check with: opencli config update weakpass no"
        exit 1
    fi
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    if [[ "$1" == "--update" ]]; then
        weakpass_enabled && weakpass_update_dict
        exit 0
    fi
    IFS= read -r password
    is_common_password "$password" && exit 1
    exit 0
fi
