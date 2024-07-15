#!/bin/bash
################################################################################
# Script Name: websites/pagespeed.sh
# Description: Get Google PageSpeed data for a website
# Usage: opencli websites-pagespeed <DOMAIN>
# Author: Stefan Pejcic
# Created: 27.06.2024
# Last Modified: 27.06.2024
# Company: openpanel.co
# Copyright (c) openpanel.co
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

# Check if jq is installed
if ! command -v jq &> /dev/null; then
  echo "jq could not be found, please install jq to proceed."
  exit 1
fi

# Function to get page speed
get_page_speed() {
  local domain=$1
  local strategy=$2
  local encoded_domain=$(printf '%s' "$domain" | jq -s -R -r @uri)
  local api_response=$(curl -s "https://www.googleapis.com/pagespeedonline/v5/runPagespeed?url=http://$encoded_domain&strategy=$strategy")
  
  local performance_score=$(echo "$api_response" | jq '.lighthouseResult.categories.performance.score')
  local first_contentful_paint=$(echo "$api_response" | jq -r '.lighthouseResult.audits."first-contentful-paint".displayValue')
  local speed_index=$(echo "$api_response" | jq -r '.lighthouseResult.audits."speed-index".displayValue')
  local interactive=$(echo "$api_response" | jq -r '.lighthouseResult.audits.interactive.displayValue')
  
  echo "{\"performance_score\": $performance_score, \"first_contentful_paint\": \"$first_contentful_paint\", \"speed_index\": \"$speed_index\", \"interactive\": \"$interactive\"}"
}

# Function to generate report for a domain
generate_report() {
  local domain=$1
  local desktop_speed=$(get_page_speed "$domain" "desktop")
  local mobile_speed=$(get_page_speed "$domain" "mobile")
  local timestamp=$(date +"%Y-%m-%d %H:%M:%S")
  local filename="/etc/openpanel/openpanel/websites/$(echo "$domain" | sed 's|https\?://||' | sed 's|/|_|g')_speed.json"
  
  cat <<EOF > "$filename"
{
  "timestamp": "$timestamp",
  "domain": "$domain",
  "desktop_speed": $desktop_speed,
  "mobile_speed": $mobile_speed
}
EOF

  echo "Google PageSpeed data saved to $filename"
}

mkdir -p /etc/openpanel/openpanel/websites

if [ $# -eq 0 ]; then
  echo "Usage: $0 <domain> OR $0 -all"
  exit 1
elif [ $# -eq 1 ]; then
  generate_report "$1"
elif [[ "$1" == "-all" ]]; then
  # Fetch list of domains from opencli websites-all
  domains=$(opencli websites-all)

  # Check if no sites found
  if [[ -z "$domains" || "$domains" == "No sites found in the database." ]]; then
    echo "No sites found in the database or opencli command error."
    exit 1
  fi

  # Iterate over each domain and generate report
  for domain in $domains; do
    generate_report "$domain"
  done

else
  echo "Usage: $0 <domain> OR $0 -all"
  exit 1
fi
