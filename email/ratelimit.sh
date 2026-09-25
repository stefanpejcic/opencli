#!/bin/bash
################################################################################
# Script Name: ratelimit.sh
# Description: Configure rate-limiting using postfwd for domains and users. 
# Usage: opencli email-ratelimit [--username=<user>] [--domain=<domain>] [--all-users] [--delete-user=<user>] [--delete-domain=<domain>] [--skip-reload] [--notify]
# Author: Stefan Pejcic
# Created: 03.12.2025
# Last Modified: 25.09.2026
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
##################################################################################

OUTPUT="/usr/local/mail/openmail/postfwd/postfwd.cf"
MYSQL_CMD="mariadb -N -s"
SKIP_RELOAD=false

usage() {
    echo "Usage: opencli email-ratelimit [--username=<user>] [--domain=<domain>] [--all-users] [--delete-user=<user>] [--delete-domain=<domain>] [--notify]"
    echo ""
    echo "  (no args)                Show current rules file"
    echo "  --username=<user>        Remove all rules for user, re-fetch their domains, re-add"
    echo "  --domain=<domain>        Add domain rule if missing, or update if exists"
    echo "  --all-users              Wipe file and regenerate rules for all users"
    echo "  --delete-user=<user>     Remove all rules for the given user"
    echo "  --delete-domain=<domain> Remove the rule for the given domain"
    echo "  --skip-reload            Optional flag to NOT reload postfix (used on plan edit)"
    echo "  --notify                 Email users whose account reached the hourly limit in the last 30 minutes, once a day"
    exit 1
}

build_rule() {
    USERNAME="$1"
    LIMIT="$2"
    DOMAIN="$3"
    key=$(echo "$DOMAIN" | tr '.' '_')
    printf 'id=limit_%s_%s ; sender=~.+@%s ; protocol_state==RCPT\n                action=rate(%s_ratelimit/%s/3600/450 4.7.1 sorry, OpenPanel account reached limit of %s emails per hour)\n\n' \
        "$USERNAME" "$key" "$DOMAIN" "$USERNAME" "$LIMIT" "$LIMIT"
}

rule_id() {
    USERNAME="$1"
    DOMAIN="$2"
    key=$(echo "$DOMAIN" | tr '.' '_')
    echo "limit_${USERNAME}_${key}"
}

remove_rule_by_id() {
    ID="$1"
    FILE="$2"
    awk -v id="id=${ID} " '
        /^id=/ && index($0, "id="id) == 1 { skip=1 }
        skip && /^$/ { skip=0; next }
        !skip { print }
    ' "$FILE"
}

remove_rules_for_user() {
    USERNAME="$1"
    FILE="$2"
    awk -v prefix="id=limit_${USERNAME}_" '
        /^id=/ && index($0, prefix) == 1 { skip=1 }
        skip && /^$/ { skip=0; next }
        !skip { print }
    ' "$FILE"
}

mode_show() {
    if [ ! -f "$OUTPUT" ]; then
        echo "File not found: $OUTPUT"
        exit 1
    fi
    echo "=== $OUTPUT ==="
    cat "$OUTPUT"
    echo "---"
    echo "$(wc -l < "$OUTPUT") lines total"
}

mode_all_users() {
    echo "Regenerating all rules..."
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
        build_rule "$USERNAME" "$LIMIT" "$DOMAIN" >> "$tmp"
        echo "OK: $USERNAME limit=${LIMIT}/hr domain=${DOMAIN}"
    done
    mv "$tmp" "$OUTPUT"
    chmod 644 "$OUTPUT"
    echo "---"
    echo "Generated $(wc -l < "$OUTPUT") lines written to $OUTPUT"
}

mode_username() {
    USERNAME="$1"
    echo "Updating rules for user: $USERNAME"

    [ -f "$OUTPUT" ] || { touch "$OUTPUT"; chmod 644 "$OUTPUT"; }

    tmp_clean=$(mktemp)
    remove_rules_for_user "$USERNAME" "$OUTPUT" > "$tmp_clean"

    tmp_rules=$(mktemp)
    $MYSQL_CMD <<EOF | while IFS=$'\t' read -r LIMIT DOMAIN; do
SELECT
    p.max_hourly_email,
    d.domain_url
FROM users u
JOIN plans p ON u.plan_id = p.id
JOIN domains d ON d.user_id = u.id
WHERE u.username = '${USERNAME}'
  AND p.max_hourly_email IS NOT NULL
  AND p.max_hourly_email > 0
EOF
        build_rule "$USERNAME" "$LIMIT" "$DOMAIN" >> "$tmp_rules"
        echo "OK: $USERNAME limit=${LIMIT}/hr domain=${DOMAIN}"
    done

    cat "$tmp_clean" "$tmp_rules" > "$OUTPUT"
    rm -f "$tmp_clean" "$tmp_rules"
    chmod 644 "$OUTPUT"
    echo "---"
    echo "Done. $(wc -l < "$OUTPUT") lines in $OUTPUT"
}

mode_domain() {
    DOMAIN="$1"
    echo "Updating rule for domain: $DOMAIN"

    [ -f "$OUTPUT" ] || { touch "$OUTPUT"; chmod 644 "$OUTPUT"; }

    row=$($MYSQL_CMD <<EOF
SELECT
    u.username,
    p.max_hourly_email
FROM users u
JOIN plans p ON u.plan_id = p.id
JOIN domains d ON d.user_id = u.id
WHERE d.domain_url = '${DOMAIN}'
  AND p.max_hourly_email IS NOT NULL
  AND p.max_hourly_email > 0
LIMIT 1
EOF
    )

    if [ -z "$row" ]; then
        echo "ERROR: No user/plan found for domain '${DOMAIN}' (or limit is 0/NULL). Nothing changed."
        exit 1
    fi

    USERNAME=$(echo "$row" | cut -f1)
    LIMIT=$(echo    "$row" | cut -f2)
    ID=$(rule_id "$USERNAME" "$DOMAIN")

    if grep -q "^id=${ID} " "$OUTPUT" 2>/dev/null; then
        echo "Updating: $ID"
        tmp=$(mktemp)
        remove_rule_by_id "$ID" "$OUTPUT" > "$tmp"
        build_rule "$USERNAME" "$LIMIT" "$DOMAIN" >> "$tmp"
        mv "$tmp" "$OUTPUT"
    else
        echo "Adding: $ID"
        build_rule "$USERNAME" "$LIMIT" "$DOMAIN" >> "$OUTPUT"
    fi

    chmod 644 "$OUTPUT"
    echo "OK: $USERNAME limit=${LIMIT}/hr domain=${DOMAIN}"
    echo "---"
}

mode_delete_user() {
    USERNAME="$1"
    echo "Deleting all rules for user: $USERNAME"

    if [ ! -f "$OUTPUT" ]; then
        echo "ERROR: File not found: $OUTPUT"
        exit 1
    fi

    if ! grep -q "^id=limit_${USERNAME}_" "$OUTPUT" 2>/dev/null; then
        echo "No rules found for user '${USERNAME}'. Nothing changed."
        exit 0
    fi

    tmp=$(mktemp)
    remove_rules_for_user "$USERNAME" "$OUTPUT" > "$tmp"
    mv "$tmp" "$OUTPUT"
    chmod 644 "$OUTPUT"
    echo "Removed all rules for user '${USERNAME}'."
    echo "---"
    echo "Done. $(wc -l < "$OUTPUT") lines in $OUTPUT"
}

mode_delete_domain() {
    DOMAIN="$1"
    echo "Deleting rule for domain: $DOMAIN"

    if [ ! -f "$OUTPUT" ]; then
        echo "ERROR: File not found: $OUTPUT"
        exit 1
    fi

    # derive the username from existing rules in the file first, no db needed, to reconstruct the rule id
    key=$(echo "$DOMAIN" | tr '.' '_')
    ID=$(grep "^id=limit_[^_]*_${key} " "$OUTPUT" 2>/dev/null | head -1 | sed 's/^id=//; s/ .*//')

    if [ -z "$ID" ]; then
        echo "No rule found for domain '${DOMAIN}'. Nothing changed."
        exit 0
    fi

    tmp=$(mktemp)
    remove_rule_by_id "$ID" "$OUTPUT" > "$tmp"
    mv "$tmp" "$OUTPUT"
    chmod 644 "$OUTPUT"
    echo "Removed rule: $ID"
    echo "---"
    echo "Done. $(wc -l < "$OUTPUT") lines in $OUTPUT"
}

# emails users whose account hit the hourly limit in the last 30 minutes (the cron interval), at most once a day per user
mode_notify() {
    local CONTAINER=openadmin_mailserver USERS_DIR="/etc/openpanel/openpanel/core/users" SINCE="30m"
    # shellcheck disable=SC1091
    . /usr/local/opencli/lib/podman.sh
    # shellcheck disable=SC1091
    . /usr/local/opencli/lib/email.sh
    podman_is_running "$CONTAINER" || { echo "'$CONTAINER' is not running, nothing to check."; return; }
    command -v jq >/dev/null 2>&1 || { echo "Error: jq is required."; exit 1; }

    # postfwd rejects every message over the limit with "OpenPanel account reached limit of N emails per hour", see build_rule
    rejects=$(timeout 60 podman logs --timestamps --since "$SINCE" "$CONTAINER" 2>&1 \
        | sed -nE 's/^([0-9T:.-]+)[^ ]* .*reached limit of ([0-9]+) emails per hour.* from=<([^>]+)>.*/\1 \2 \3/p')
    if [ -z "$rejects" ]; then
        echo "No emails rejected by the hourly limit in the last $SINCE."
        return
    fi

    # domain -> username for every domain, the limit is per account so rejections from all of its domains add up
    owners=$(mariadb --defaults-extra-file=/etc/my.cnf -D panel -N -B -e "SELECT d.domain_url, u.username FROM domains d JOIN users u ON u.id = d.user_id" 2>/dev/null)

    # one line per account: username, limit, rejected count, first and last rejection time, senders as "address count" pairs
    summary=$(awk -v owners="$owners" '
        BEGIN {
            n = split(owners, rows, "\n")
            for (i = 1; i <= n; i++) { split(rows[i], f, "\t"); owner[tolower(f[1])] = f[2] }
        }
        {
            sender = tolower($3); domain = sender; sub(/.*@/, "", domain)
            user = owner[domain]
            if (user == "") next
            limit[user] = $2; count[user]++
            t = substr($1, 12, 5)
            if (!(user in first)) first[user] = t
            last[user] = t
            sent[user, sender]++
            if (!((user, sender) in seen)) { seen[user, sender] = 1; senders[user] = senders[user] " " sender }
        }
        END {
            for (u in count) {
                list = ""
                m = split(senders[u], s, " ")
                for (i = 1; i <= m; i++) if (s[i] != "") list = list s[i] ":" sent[u, s[i]] ";"
                printf "%s\t%s\t%s\t%s\t%s\t%s\n", u, limit[u], count[u], first[u], last[u], list
            }
        }' <<< "$rejects")

    today=$(date +%Y-%m-%d)
    while IFS=$'\t' read -r username limit rejected first last senders; do
        [[ -n "$username" ]] || continue
        # once a day is enough, a script stuck at the limit would otherwise email every 30 minutes
        state="${USERS_DIR}/${username}/.email_ratelimit_notified"
        if [[ "$(cat "$state" 2>/dev/null)" == "$today" ]]; then
            echo "$username: $rejected email(s) rejected, already notified today."
            continue
        fi

        sender_lines=""
        IFS=';' read -ra pairs <<< "$senders"
        for pair in "${pairs[@]}"; do
            [[ -n "$pair" ]] && sender_lines+="- ${pair%:*}: ${pair##*:} rejected"$'\n'
        done

        usage=$(jq -cn --arg u "$limit" --arg t "$limit emails" \
            '[{title: "Emails sent in the last hour", used: $u, total: $t, percent: 100, limit: 100}]')

        upgrade_plan="" upgrade_text=""
        if IFS=$'\t' read -r up_name _ _ _ _ _ _ cur_hourly up_hourly < <(user_upsell "$username") && limit_raised "$cur_hourly" "$up_hourly" count; then
            upgrade_plan="$up_name"
            upgrade_text="The $up_name plan lets you send $( [[ "$up_hourly" == 0 ]] && echo "unlimited" || echo "up to $up_hourly") emails per hour instead of $cur_hourly."
        fi

        if send_user_email "$username" "Account $username reached its hourly email limit" \
            "Account $username reached its limit of $limit emails per hour, so $rejected email(s) were not sent between $first and $last."$'\n\n'"Rejected emails by sender:"$'\n'"${sender_lines}"$'\n'"Sending works again on its own once fewer than $limit emails were sent in the last hour. The rejected emails are not sent later, send them again if they're still needed." \
            notify_email_ratelimit "$usage" "$upgrade_plan" "$upgrade_text" \
            "If you didn't send this many emails, a mailbox password may be stolen or a contact form on your website may be abused by spammers. Change the password of the mailbox above and check your website's forms."; then
            echo "$today" > "$state"
            echo "$username: $rejected email(s) rejected, user notified."
        else
            echo "$username: $rejected email(s) rejected, no email sent (notifications off or no email address)."
        fi
    done <<< "$summary"
}

enterprise=$(grep "^key=" "/etc/openpanel/openpanel/conf/openpanel.config" | cut -d'=' -f2-)
if [ -z "$enterprise" ]; then
    echo "Error: OpenPanel Community edition does not support emails. Please consider purchasing the Enterprise version that allows email management."
    source "/usr/local/opencli/lib/enterprise.sh"
    echo "$ENTERPRISE_LINK"
    exit 0 #purposely
fi

# MAIN
OPTMODE="show"
OPTVAL=""

for arg in "$@"; do
    case "$arg" in
        --all-users)        OPTMODE="all-users" ;;
        --username=*)       OPTMODE="username";      OPTVAL="${arg#--username=}" ;;
        --domain=*)         OPTMODE="domain";        OPTVAL="${arg#--domain=}" ;;
        --delete-user=*)    OPTMODE="delete-user";   OPTVAL="${arg#--delete-user=}" ;;
        --delete-domain=*)  OPTMODE="delete-domain"; OPTVAL="${arg#--delete-domain=}" ;;
        --notify)           OPTMODE="notify";        SKIP_RELOAD=true ;;
        --skip-reload)      SKIP_RELOAD=true ;;
        --help|-h)          usage ;;
        *) echo "Unknown argument: $arg"; usage ;;
    esac
done

case "$OPTMODE" in
    show)           mode_show ;;
    notify)         mode_notify ;;
    all-users)      mode_all_users ;;
    username)
        [ -z "$OPTVAL" ] && { echo "ERROR: --username= value is empty"; usage; }
        mode_username "$OPTVAL"
        ;;
    domain)
        [ -z "$OPTVAL" ] && { echo "ERROR: --domain= value is empty"; usage; }
        mode_domain "$OPTVAL"
        ;;
    delete-user)
        [ -z "$OPTVAL" ] && { echo "ERROR: --delete-user= value is empty"; usage; }
        mode_delete_user "$OPTVAL"
        ;;
    delete-domain)
        [ -z "$OPTVAL" ] && { echo "ERROR: --delete-domain= value is empty"; usage; }
        mode_delete_domain "$OPTVAL"
        ;;
esac

if [ "$SKIP_RELOAD" = false ]; then
    # reload conf, keep counters!
	nohup podman kill --signal=HUP postfwd >/dev/null 2>&1 &
	disown
fi
