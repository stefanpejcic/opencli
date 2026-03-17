#!/bin/sh
OUTPUT="/usr/local/mail/openmail/postfwd/postfwd.cf"
MYSQL_CMD="mysql -N -s"

tmp=$(mktemp)

$MYSQL_CMD <<'EOF' | while IFS=$'\t' read -r USERNAME LIMIT DOMAIN; do
SELECT 
    u.username,
    p.max_hourly_email,
    d.domain_url
FROM users u
JOIN plans p ON u.plan_id = p.id
JOIN domains d ON d.user_id = u.id
WHERE p.max_hourly_email IS NOT NULL 
  AND p.max_hourly_email > 0
ORDER BY u.username
EOF
    key=$(echo "$DOMAIN" | tr '.' '_')
    cat >> "$tmp" << RULE
id=limit_${USERNAME}_${key} ; sender=~.+@${DOMAIN} ; protocol_state==RCPT
                action=rate(${USERNAME}_ratelimit/${LIMIT}/3600/450 4.7.1 sorry, OpenPanel account reached limit of ${LIMIT} emails per hour)
RULE
    echo "OK: $USERNAME limit=${LIMIT}/hr domain=${DOMAIN}"
done

mv "$tmp" "$OUTPUT"
chmod 644 "$OUTPUT"
echo "---"
echo "Generated $(wc -l < "$OUTPUT") rules written to $OUTPUT"
