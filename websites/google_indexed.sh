#!/bin/bash
################################################################################
# Script Name: webistes/google_index.sh
# Description: Check if website is indexed on Google and monitor results.
# Usage: opencli webistes/google_index --domain [DOMAIN]
# Author: Stefan Pejcic
# Created: 03.06.2025
# Last Modified: 03.06.2025
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

MAX_JOBS=5  # keep low
mkdir -p "/etc/openpanel/openpanel/websites/"


check_index() {
  local domain=$1
  local timestamp=$(date +"%Y-%m-%d %H:%M:%S")
  local filename="/etc/openpanel/openpanel/websites/$(echo "$domain" | sed 's|https\?://||' | sed 's|/|_|g').google_index.json"
  local result page_count indexed error_msg="" trouble_link=""

  # Fetch initial Google search results page
  result=$(curl -L -s -A "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36" --cookie-jar /tmp/google_cookies.txt --cookie /tmp/google_cookies.txt "https://www.google.com/search?q=site:$domain")

  # Check for "If you're having trouble accessing Google Search" message
  if echo "$result" | grep -q "If you're having trouble accessing Google Search"; then
    # Extract the trouble link (href in <a href="...">click here</a>)
    trouble_link=$(echo "$result" | grep -oP '(?<=<a href=")[^"]+(?=">click here</a>)' | head -1)
    if [ -n "$trouble_link" ]; then
      # Make full URL if relative
      if [[ "$trouble_link" =~ ^/ ]]; then
        trouble_link="https://www.google.com${trouble_link}"
      fi
      
      # Follow the trouble link to get a new page
      result=$(curl -L -s -A "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36" --cookie-jar /tmp/google_cookies.txt --cookie /tmp/google_cookies.txt "$trouble_link")

      # Now try to parse indexed status and results count from this page
      if echo "$result" | grep -q "did not match any documents"; then
        indexed=false
        page_count=0
      else
        indexed=true
        page_count=$(echo "$result" | grep -oP 'About [\d,]+ results' | head -1 | sed 's/About //; s/ results//; s/,//g')
        if ! [[ "$page_count" =~ ^[0-9]+$ ]]; then
          page_count=0
        fi
      fi
      
      # If still no good data, add error message
      if [ "$page_count" -eq 0 ] && [ "$indexed" = false ]; then
        error_msg="Google access blocked and no results info found after following trouble link."
      fi
    else
      error_msg="Google access blocked, but trouble link not found."
    fi
  else
    # No trouble message, parse normal results page
    if echo "$result" | grep -q 'consent.google.com'; then
      error_msg="Consent page detected. Try a different IP or use a real browser."
    fi

    if [ -z "$error_msg" ]; then
      if echo "$result" | grep -q "did not match any documents"; then
        indexed=false
        page_count=0
      else
        indexed=true
        page_count=$(echo "$result" | grep -oP 'About [\d,]+ results' | head -1 | sed 's/About //; s/ results//; s/,//g')
        if ! [[ "$page_count" =~ ^[0-9]+$ ]]; then
          page_count=0
        fi
      fi
    fi
  fi

  # Compare with previous results if file exists
  if [ -f "$filename" ]; then
    prev_indexed=$(jq -r '.indexed' "$filename")
    prev_count=$(jq -r '.results_count' "$filename" | sed 's/,//g')
    if ! [[ "$prev_count" =~ ^[0-9]+$ ]]; then
      prev_count=0
    fi

    if [ "$prev_indexed" = "true" ] && [ "$indexed" = "false" ]; then
      error_msg="Site was indexed before but now NOT indexed."
    fi

    if [ "$indexed" = "true" ] && [ "$prev_count" -gt 0 ]; then
      drop=$((prev_count - page_count))
      drop_percent=$(( drop * 100 / prev_count ))
      if [ "$drop_percent" -ge 10 ]; then
        error_msg="Results count dropped by ${drop_percent}% compared to previous check."
      fi
    fi
  fi

  # Write JSON with or without error field
  if [ -n "$error_msg" ]; then
    cat > "$filename" <<EOF
{
  "timestamp": "$timestamp",
  "domain": "$domain",
  "indexed": ${indexed:-false},
  "results_count": "${page_count:-0}",
  "error": "$error_msg"
}
EOF
  else
    cat > "$filename" <<EOF
{
  "timestamp": "$timestamp",
  "domain": "$domain",
  "indexed": $indexed,
  "results_count": "$page_count"
}
EOF
  fi
  # Print JSON to console
  cat "$filename"
}

run_parallel() {
  local domains=("$@")
  local count=0

  for domain in "${domains[@]}"; do
    check_index "$domain" &
    ((count++))

    if (( count % MAX_JOBS == 0 )); then
      wait
    fi
  done
  wait
}

DOMAIN=""

# Argument parsing
while [[ $# -gt 0 ]]; do
  case $1 in
    --domain)
      DOMAIN="$2"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1"
      echo "Usage: $0 [--domain <DOMAIN>]"
      exit 1
      ;;
  esac
done

if [ -z "$DOMAIN" ]; then
  # Run for all domains from opencli websites-all
  mapfile -t domains < <(opencli websites-all)
  run_parallel "${domains[@]}"
else
  check_index "$DOMAIN"
fi
