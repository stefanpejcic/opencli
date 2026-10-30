#!/bin/bash
################################################################################
# Script Name: server/logrotate.sh
# Description: Configures logrotate for caddy, openpanel, syslog.
# Usage: opencli server-logrotate
# Author: Stefan Pejcic
# Created: 16.01.2024
# Last Modified: 30.10.2026
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
. /usr/local/opencli/lib/requirement.sh

CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"

get_config() {
    grep -E "^$1=" "$CONFIG_FILE" 2>/dev/null | cut -d= -f2 | tr -d ' ' || echo "$2"
}

require_command logrotate

logrotate_enable=$(get_config logrotate_enable yes)
logrotate_size_limit=$(get_config logrotate_size_limit 100m)
logrotate_retention=$(get_config logrotate_retention 10)
logrotate_keep_days=$(get_config logrotate_keep_days 30)

[ "$logrotate_enable" != "yes" ] && {
    echo "Log rotation is not enabled."
    exit 0
}

# ---------- CADDY ----------
cat > /etc/logrotate.d/caddy-logs <<EOF
/var/log/caddy/coraza_waf/*.log
/var/log/caddy/domlogs/*/access.log {
    size $logrotate_size_limit
    rotate $logrotate_retention
    daily
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
    create 640 root adm
    maxage $logrotate_keep_days
}
EOF
# no caddy reload needed with copytruncate, and podman exec fails in logrotate.service's sandbox which marked every nightly run failed

logrotate -f /etc/logrotate.d/caddy-logs
echo "Caddy log rotation configured."

# ---------- SYSLOG ----------
if ! grep -qr "/var/log/syslog" /etc/logrotate.conf /etc/logrotate.d/; then
    cat > /etc/logrotate.d/syslog <<EOF
/var/log/syslog {
    su root adm
    size $logrotate_size_limit
    rotate $logrotate_retention
    weekly
    missingok
    notifempty
    compress
    delaycompress
    postrotate
        systemctl reload rsyslog >/dev/null 2>&1 || true
    endscript
    maxage $logrotate_keep_days
}
EOF
    logrotate -f /etc/logrotate.d/syslog
    echo "Syslog rotation configured."
elif [ -f /etc/logrotate.d/rsyslog ] && ! grep -q "maxsize" /etc/logrotate.d/rsyslog; then
    # the distro rotates syslog weekly with no size limit, container output (caddy, php-fpm) can grow it by gigabytes a day
    sed -i "0,/^{/s//{\n\tmaxsize $logrotate_size_limit/" /etc/logrotate.d/rsyslog
    echo "Syslog already rotated by /etc/logrotate.d/rsyslog, added maxsize $logrotate_size_limit to it."
else
    echo "Syslog is already configured in logrotate, skipping."
fi

# csf installs this one group-writable, logrotate then refuses it and exits with an error on every run
[ -f /etc/logrotate.d/lfd ] && chmod 644 /etc/logrotate.d/lfd

# ---------- OPENPANEL ----------
cat > /etc/logrotate.d/openpanel <<EOF
# rotate all panel logs except notifications.log, logrotate doesn't support extglob
/var/log/openpanel/admin/[!n]*.log
/var/log/openpanel/user/*.log {
    su root adm
    size $logrotate_size_limit
    rotate $logrotate_retention
    weekly
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
    create 640 root adm
    maxage $logrotate_keep_days
}

# delete update logs older than 3m
/var/log/openpanel/updates/*.log {
    missingok
    notifempty
    rotate 0
    sharedscripts
    prerotate
        find /var/log/openpanel/updates/ -type f -mtime +90 -delete 2>/dev/null || true
    endscript
}

# delete reports older than 7d
/var/log/openpanel/admin/reports/*.txt {
    daily
    rotate 0
    missingok
    notifempty
    sharedscripts
    prerotate
        find /var/log/openpanel/admin/reports/ -type f -name "*.txt" -mtime +7 -delete 2>/dev/null || true
    endscript
}

EOF

logrotate -f /etc/logrotate.d/openpanel

echo "OpenPanel log rotation configured."
