#!/bin/bash

CONFIG_FILE="/etc/openpanel/openpanel/conf/openpanel.config"

logrotate_enable="yes"
logrotate_size_limit="100m"
logrotate_retention=10
logrotate_keep_days=30

get_config_value() {
    local key=$1
    local default_value=$2
    grep -E "^${key}=" "$CONFIG_FILE" | awk -F'=' '{print $2}' | tr -d ' ' || echo "$default_value"
}

if ! command -v logrotate &> /dev/null; then
    echo "logrotate is not installed. Installing.."
    apt-get install logrotate -y
fi

logrotate_enable=$(get_config_value "logrotate_enable" "$logrotate_enable")
logrotate_size_limit=$(get_config_value "logrotate_size_limit" "$logrotate_size_limit")
logrotate_retention=$(get_config_value "logrotate_retention" "$logrotate_retention")
logrotate_keep_days=$(get_config_value "logrotate_keep_days" "$logrotate_keep_days")

if [ "$logrotate_enable" != "yes" ]; then
    echo "Log rotation is not enabled."
    exit 0
fi

LOGROTATE_CONF="/etc/logrotate.d/nginx-domlogs"

cat <<EOF > "$LOGROTATE_CONF"
/var/log/nginx/domlogs/*.log {
    size $logrotate_size_limit
    rotate $logrotate_retention
    daily
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
    create 640 root adm
    postrotate
        /usr/sbin/nginx -s reopen
    endscript
    maxage $logrotate_keep_days
}
EOF

/usr/sbin/logrotate --force "$LOGROTATE_CONF"

echo "Log rotation configuration applied successfully."
