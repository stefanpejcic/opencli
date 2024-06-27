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

# Check if the domain is provided
if [ -z "$1" ]; then
  echo "Usage: $0 <domain>"
  exit 1
fi

DOMAIN="http://$1"

mkdir -p /etc/openpanel/openpanel/websites/

# Check if jq is installed
if ! command -v jq &> /dev/null; then
  echo "jq could not be found, please install jq to proceed."
  exit 1
fi

# Function to get page speed
get_page_speed() {
  local strategy=$1
  local encoded_domain=$(printf '%s' "$DOMAIN" | jq -s -R -r @uri)
  local api_response=$(curl -s "https://www.googleapis.com/pagespeedonline/v5/runPagespeed?url=$encoded_domain&strategy=$strategy")
  
  local performance_score=$(echo "$api_response" | jq '.lighthouseResult.categories.performance.score')
  local first_contentful_paint=$(echo "$api_response" | jq -r '.lighthouseResult.audits."first-contentful-paint".displayValue')
  local speed_index=$(echo "$api_response" | jq -r '.lighthouseResult.audits."speed-index".displayValue')
  local interactive=$(echo "$api_response" | jq -r '.lighthouseResult.audits.interactive.displayValue')
  
  echo "{\"performance_score\": $performance_score, \"first_contentful_paint\": \"$first_contentful_paint\", \"speed_index\": \"$speed_index\", \"interactive\": \"$interactive\"}"
}

# Get desktop and mobile speeds
DESKTOP_SPEED=$(get_page_speed "desktop")
MOBILE_SPEED=$(get_page_speed "mobile")

# Save the speeds to a JSON file with timestamp
TIMESTAMP=$(date +"%Y-%m-%d %H:%M:%S")
FILENAME="/etc/openpanel/openpanel/websites/$(echo "$DOMAIN" | sed 's|https\?://||' | sed 's|/|_|g')_speed.json"

cat <<EOF > "$FILENAME"
{
  "timestamp": "$TIMESTAMP",
  "domain": "$DOMAIN",
  "desktop_speed": $DESKTOP_SPEED,
  "mobile_speed": $MOBILE_SPEED
}
EOF

echo "Google PageSpeed data saved to $FILENAME"
