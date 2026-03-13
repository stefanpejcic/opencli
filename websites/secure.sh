#!/bin/bash
################################################################################
# Script Name: websites/secure.sh
# Description: WP Manager security rules for domain.
# Usage: opencli websites-secure <DOMAIN> [--all]
# Usage: opencli websites-secure <DOMAIN> [--rules='RULE1 RULE2' | --disable-all | --list-active-rules]
#        opencli websites-secure --list-available-rules
# Author: Stefan Pejcic
# Created: 13.03.2026
# Last Modified: 13.03.2026
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

usage() {
  echo "Usage:"
  echo "opencli websites-secure <domain>"
  echo "opencli websites-secure --list-available-rules"
  echo "opencli websites-secure <domain> --rules='RULE1 RULE2'"
  echo "opencli websites-secure <domain> --disable-all"
  echo "opencli websites-secure <domain> --list-active-rules"
  exit 1
}

[ $# -lt 1 ] && usage

readonly CADDY_VHOST_DIR="/etc/openpanel/caddy/domains"
readonly WP_MANAGER_RULES="/etc/openpanel/caddy/templates/wp.rules"


if [[ ! -f "$WP_MANAGER_RULES" ]]; then
  # for <1.7.47
  curl -s -L "https://raw.githubusercontent.com/stefanpejcic/openpanel-configuration/refs/heads/main/caddy/templates/wp.rules" -o "$WP_MANAGER_RULES"
  if [[ ! -f "$WP_MANAGER_RULES" ]]; then
    echo "Error: rules file not found: $WP_MANAGER_RULES"
    exit 1
  fi
fi

# LIST ALL RULES
if [[ "$1" == "--list-available-rules" ]]; then
  grep -oP '^\(\K[A-Z0-9_]+' "$WP_MANAGER_RULES"
  exit 0
fi

DOMAIN="$1"
shift


domain_regex='\.'
if [[ ! $DOMAIN =~ $domain_regex ]]; then
  echo "ERROR: '$DOMAIN' is not a valid domain name."
  exit 1
fi

domain_file="${CADDY_VHOST_DIR}/${DOMAIN}.conf"

if [[ ! -f "$domain_file" ]]; then
  echo "Error: domain '$DOMAIN' does not exist."
  exit 1
fi

if ! grep -q "# modsecurity" "$domain_file"; then
  echo "Error: '# modsecurity' marker not found in $domain_file"
  echo "Aborting to avoid corrupting config."
  exit 1
fi


RULES=""
DELETE_ALL=false

for arg in "$@"; do
  case $arg in
    --rules=*) RULES="${arg#*=}" ;;
    --disable-all) DELETE_ALL=true ;;
  esac
done


# HELPERS

reload_caddy() {
    nohup docker --context default exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1 &
    disown
}

helper_to_empty_rules() {
TMP=$(mktemp)

awk '
/# modsecurity/ {
    modline=NR
}
{ lines[NR]=$0 }
END {
    for(i=1;i<=NR;i++){
        if(i==modline) break
        if(lines[i] ~ /^[[:space:]]*import[[:space:]]+[A-Z0-9_]+$/) continue
        print lines[i]
    }
    for(i=modline;i<=NR;i++){
        print lines[i]
    }
}
' "$domain_file" > "$TMP"

  mv "$TMP" "$domain_file"
}

helper_list_active_rules() {
  # Extract lines starting with "import" after # modsecurity
  # TODO!
  awk '/# modsecurity/{flag=1; next} flag && /^[[:space:]]*import[[:space:]]+[A-Z0-9_]+/{print $2}' "$domain_file" | sort -u
}


# DELETE RULES
if [[ "$DELETE_ALL" = true ]]; then
  helper_to_empty_rules
  reload_caddy
  echo "All rules removed for $DOMAIN"
  exit 0
fi

# LIST ACTIVE RULES
if [[ "$LIST_ACTIVE" = true ]]; then
  ACTIVE_RULES=$(helper_list_active_rules)
  if [[ -n "$ACTIVE_RULES" ]]; then
    echo "Active rules for $DOMAIN:"
    echo "$ACTIVE_RULES"
  else
    echo "No active rules for $DOMAIN."
  fi
  exit 0
fi

# SHOW STATUS IF NO ARGUMENTS
if [[ -z "$RULES" && "$DELETE_ALL" = false ]]; then
  ACTIVE_RULES=$(helper_list_active_rules)
  if [[ -n "$ACTIVE_RULES" ]]; then
    echo "Status for $DOMAIN: rules enabled"
    echo "$ACTIVE_RULES"
  else
    echo "Status for $DOMAIN: no rules enabled"
  fi
  exit 0
fi

# UPDATE
if [[ -n "$RULES" ]]; then

  # 1. validate rules
  VALID_RULES=$(grep -oP '^\(\K[A-Z0-9_]+' "$WP_MANAGER_RULES")
  FILTERED_RULES=""
  for rule in $RULES; do
    if grep -qw "$rule" <<< "$VALID_RULES"; then
      FILTERED_RULES+="$rule "
    else
      echo "Removing invalid rule: '$rule'"
    fi
  done
  RULES=$(echo "$FILTERED_RULES" | xargs)

  if [[ -n "$RULES" ]]; then

    # 2. delete all existing
    helper_to_empty_rules

    # 3. import new rules
    TMP=$(mktemp)
    awk -v rules="$RULES" '
  /# modsecurity/ {
      split(rules, r)
      for(i in r){
          printf "    import %s\n", r[i]
      }
  }
  { print }
  ' "$domain_file" > "$TMP"
    mv "$TMP" "$domain_file"

    # 4. reload caddy
    reload_caddy
  
    echo "Rules added for $DOMAIN"
  else
    echo "ERROR: no valid rules provided."
  fi
fi
