#!/bin/bash
################################################################################
# Script Name: security-report.sh
# Description: Generate a security report for OpenPanel system.
# Usage: opencli security-report [--public] [--full]
# Author: GitHub Copilot
# Created: $(date +'%d.%m.%Y')
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

# Create directory if it doesn't exist
output_dir="/var/log/openpanel/admin/security_reports"
mkdir -p "$output_dir"

output_file="$output_dir/security_report_$(date +'%Y%m%d%H%M%S').txt"

# Function to run a command and print its output with a custom message
run_command() {
  echo "# $2:" >> "$output_file"
  $1 >> "$output_file" 2>&1 || echo "Command failed or not available" >> "$output_file"
  echo >> "$output_file"
}

# Function to check firewall configuration
check_firewall() {
  echo "=== Firewall Configuration ===" >> "$output_file"

  # Check for UFW
  if command -v ufw &> /dev/null; then
    run_command "ufw status verbose" "UFW Firewall Status"
    run_command "ufw status numbered" "UFW Rules List"
  fi

  # Check for CSF
  if command -v csf &> /dev/null; then
    run_command "csf -l" "CSF Firewall Rules"
    run_command "csf -s" "CSF Firewall Status"
  fi

  # Check for iptables
  run_command "iptables -L -n -v" "IPTables Rules"
  run_command "iptables -L -n -v -t nat" "IPTables NAT Rules"
}

# Function to check for failed login attempts
check_failed_logins() {
  echo "=== Failed Login Attempts ===" >> "$output_file"
  run_command "journalctl -u sshd | grep 'Failed password' | tail -n 20" "Recent SSH Failed Logins"
  run_command "grep 'Failed password' /var/log/auth.log 2>/dev/null | tail -n 20" "Auth.log Failed Logins"
  run_command "lastb | head -n 20" "Failed Login Attempts (lastb)"
}

# Function to check open ports and connections
check_network() {
  echo "=== Network Security ===" >> "$output_file"
  run_command "ss -tuln" "Open Ports (ss)"
  run_command "netstat -tuln" "Open Ports (netstat)"
  run_command "lsof -i -P -n | grep LISTEN" "Listening Processes"
}

# Function to check system updates
check_updates() {
  echo "=== System Updates ===" >> "$output_file"

  # Check OS type
  if [ -f /etc/debian_version ]; then
    run_command "apt list --upgradable" "Available Debian/Ubuntu Updates"
  elif [ -f /etc/redhat-release ]; then
    run_command "yum check-update" "Available RHEL/CentOS Updates"
  fi
}

# Function to check Docker security
check_docker_security() {
  echo "=== Docker Security ===" >> "$output_file"
  run_command "docker info" "Docker Info"
  run_command "docker ps --format 'table {{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Ports}}'" "Running Containers"
  run_command "docker network ls" "Docker Networks"
}

# Function to check sudo permissions
check_sudo() {
  echo "=== Sudo Configuration ===" >> "$output_file"
  run_command "grep -v '^#' /etc/sudoers | grep -v '^$'" "Sudoers Configuration"
  run_command "find /etc/sudoers.d -type f -exec grep -v '^#' {} \; | grep -v '^$'" "Sudoers.d Files"
}

# Function to check for suspicious processes
check_processes() {
  echo "=== Process Security ===" >> "$output_file"
  run_command "ps aux --sort=-%cpu | head -n 20" "Top CPU Processes"
  run_command "ps aux --sort=-%mem | head -n 20" "Top Memory Processes"
  run_command "ps aux | grep -i '\[defunct\]'" "Zombie Processes"
}

# Function to check SSH configuration
check_ssh() {
  echo "=== SSH Configuration ===" >> "$output_file"
  run_command "grep -v '^#' /etc/ssh/sshd_config | grep -v '^$'" "SSHD Configuration"
  run_command "systemctl status sshd" "SSH Service Status"
  run_command "grep 'Accepted' /var/log/auth.log 2>/dev/null | tail -n 10" "Recent SSH Logins"
}

# Function to check user accounts
check_users() {
  echo "=== User Account Security ===" >> "$output_file"
  run_command "awk -F: '\$3 == 0 {print \$1}' /etc/passwd" "Users with UID 0 (root)"
  run_command "awk -F: '\$2 == \"\" {print \$1}' /etc/shadow 2>/dev/null" "Accounts Without Passwords"
  run_command "lastlog | grep -v 'Never logged in'" "Last Login Info"
  run_command "grep -v ':x:' /etc/passwd" "Users with no password entry in /etc/shadow"
}

# Function to check OpenPanel specific security
check_openpanel_security() {
  echo "=== OpenPanel Security ===" >> "$output_file"
  run_command "grep -i 'password\|secret\|key\|token' /etc/openpanel/openpanel/conf/openpanel.config 2>/dev/null | grep -v '^#'" "OpenPanel Configuration Security Keys (sanitized)"
  run_command "ls -la /etc/openpanel/" "OpenPanel Directory Permissions"
  run_command "grep -i fail /var/log/openpanel/admin/error.log 2>/dev/null | tail -n 20" "Recent OpenPanel Errors"
}

# Default values
public_flag=false
full_flag=false

# Parse command line arguments
for arg in "$@"; do
  if [ "$arg" = "--public" ]; then
    public_flag=true
  elif [ "$arg" = "--full" ]; then
    full_flag=true
  else
    echo "Unknown option: $arg"
    echo "Usage: opencli security-report [--public] [--full]"
    exit 1
  fi
done

# Header information
echo "=== OpenPanel Security Report ===" > "$output_file"
echo "Generated: $(date)" >> "$output_file"
echo "Hostname: $(hostname)" >> "$output_file"
echo >> "$output_file"

# Collect basic system information
run_command "uname -a" "System Information"
run_command "cat /etc/os-release" "OS Details"

# Run all security checks
check_firewall
check_failed_logins
check_network
check_updates
check_sudo
check_ssh
check_users
check_openpanel_security

# Run more intensive checks if --full is specified
if [ "$full_flag" = true ]; then
  check_docker_security
  check_processes

  echo "=== Additional Security Information ===" >> "$output_file"
  run_command "find / -perm -4000 -type f 2>/dev/null" "SUID Files"
  run_command "find / -perm -2000 -type f 2>/dev/null" "SGID Files"
  run_command "find / -perm -2 -type f -not -path \"/proc/*\" -not -path \"/sys/*\" 2>/dev/null | grep -v '/dev/'" "World-Writable Files"
  run_command "find / -type d -perm -2 -not -path \"/proc/*\" -not -path \"/sys/*\" 2>/dev/null | grep -v '/dev/'" "World-Writable Directories"
fi

if [ "$public_flag" = true ]; then
  # Sanitize the report before uploading - remove sensitive information
  temp_file=$(mktemp)
  cat "$output_file" | grep -v -i 'password\|secret\|key\|token\|credential' > "$temp_file"
  mv "$temp_file" "$output_file"

  # Upload the report
  response=$(curl -F "file=@$output_file" https://support.openpanel.org/opencli_security_report.php 2>/dev/null)
  if echo "$response" | grep -q "File upload failed."; then
    echo -e "Security report generated but uploading to support.openpanel.org failed. Please provide content from the following file to the support team:\n$output_file"
  else
    LINKHERE=$(echo "$response" | grep -o 'http[s]\?://[^ ]*')
    echo -e "Security report generated successfully. Please provide the following link to the support team:\n$LINKHERE"
  fi
else
  echo -e "Security report generated successfully. You can find it at:\n$output_file"
fi

# Make the security report executable-only by root for security reasons
chmod 700 "$output_file"

exit 0
