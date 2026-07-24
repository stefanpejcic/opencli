#!/bin/bash
# ======================================================================
# Shared "ensure command is installed" helper, sourced by scripts that
# depend on external CLI tools (jq, ...). Detects the system package
# manager and installs the missing package on demand.
#
# This file only defines functions - it has no top-level logic/exit, so
# it is safe to `source` from any script.
# ======================================================================

# Ensures the given command is available, installing it via the system
# package manager if it isn't. Pass a second argument if the package name
# differs from the command name, e.g.:
#   require_command jq
#   require_command mysql mysql-client
require_command() {
    local cmd="$1"
    local package="${2:-$1}"

    command -v "$cmd" &> /dev/null && return 0

    if command -v apt-get &> /dev/null; then
        apt-get update -qq > /dev/null 2>&1
        apt-get install -y -qq "$package" > /dev/null 2>&1
    elif command -v dnf &> /dev/null; then
        # some packages (e.g. fzf on older RHEL) only exist in EPEL - retry
        # through it if the plain install fails, no-op if not needed
        dnf install -y -q "$package" > /dev/null 2>&1 || {
            dnf install -y -q epel-release > /dev/null 2>&1
            dnf install -y -q "$package" > /dev/null 2>&1
        }
    elif command -v yum &> /dev/null; then
        yum install -y -q "$package" > /dev/null 2>&1 || {
            yum install -y -q epel-release > /dev/null 2>&1
            yum install -y -q "$package" > /dev/null 2>&1
        }
    elif command -v pacman &> /dev/null; then
        pacman -Sy --noconfirm "$package" > /dev/null 2>&1
    else
        echo "Error: No compatible package manager found. Please install $package manually and try again."
        exit 1
    fi

    if ! command -v "$cmd" &> /dev/null; then
        echo "Error: $package installation failed. Please install $package manually and try again."
        exit 1
    fi
}
