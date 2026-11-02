#!/bin/bash
################################################################################
# Script Name: email/default_sync.sh
# Description: Writes the default (catch-all) email addresses to docker-mailserver's postfix-regexp.cf.
# Usage: opencli email-default_sync
# Docs: https://docs.openpanel.com
# Author: Erik
# Created: 02.11.2026
# Last Modified: 02.11.2026
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

readonly CONFIG_DIR="/usr/local/mail/openmail/docker-data/dms/config"
readonly OUT="$CONFIG_DIR/postfix-regexp.cf"
readonly BEGIN_LINE="# BEGIN opencli email-default_sync"
readonly END_LINE="# END opencli email-default_sync"

[[ -d "$CONFIG_DIR" ]] || exit 0

# the panel and email-setup both run this, one at a time so an older read never wins
exec 9<"$CONFIG_DIR" && flock 9 || exit 1

file_or_null() { if [[ -f "$1" ]]; then echo "$1"; else echo /dev/null; fi; }

tmp=$(mktemp "$CONFIG_DIR/.postfix-regexp.cf.XXXXXX") || exit 1
trap 'rm -f "$tmp"' EXIT

# postfix asks a pcre alias map with the whole recipient (user+tag@domain too) before any mailbox lookup, so each rule leaves out the domain's mailboxes and aliases
awk -v begin="$BEGIN_LINE" -v end="$END_LINE" '
function esc(s,  r, i, c) {
    for (i = 1; i <= length(s); i++) { c = substr(s, i, 1); r = r (c ~ /[a-z0-9]/ ? c : "\\" c) }
    return r
}
function known(a,  at, d, l) {
    a = tolower(a); at = index(a, "@")
    if (at < 2) return
    d = substr(a, at + 1); box[a] = 1
    # mawk creates names[d] before it reads the right side, so test first
    l = esc(substr(a, 1, at - 1)); if (d in names) l = names[d] "|" l
    names[d] = l
}
part == "out" { if ($0 == begin) skip = had = 1; if (!skip) print; if ($0 == end) skip = 0; next }
/^[ \t]*(#|$)/ { next }
part == "accounts" { split($0, f, "|"); known(f[1]); next }
part == "virtual" { known($1); next }
part == "regex" {
    d = tolower($1); t = tolower($2)
    if (d !~ /^\/\*@[a-z0-9.-]+\/$/ || t !~ /^[a-z0-9._%+-]+@[a-z0-9.-]+$/) next
    d = substr(d, 4, length(d) - 4)
    if (d in seen) next
    seen[d] = 1
    # docker-mailserver takes mail only for a domain with a mailbox or alias here; postfix applies the rules to every recipient, so for any other domain a rule would only redirect the mail local users send to it
    if (!(d in names)) next
    # a destination on a domain hosted here has to exist, or its mail would be accepted and then bounced
    td = substr(t, index(t, "@") + 1)
    if (!(t in box) && (td in names)) next
    rules = rules "/^(?!(?:" names[d] ")(?:\\+[^@]*)?@)[^@]+@" esc(d) "$/ " t "\n"
}
END { if (rules != "" || had) { print begin; printf "%s", rules; print end } }
' part=out "$(file_or_null "$OUT")" \
  part=accounts "$(file_or_null "$CONFIG_DIR/postfix-accounts.cf")" \
  part=virtual "$(file_or_null "$CONFIG_DIR/postfix-virtual.cf")" \
  part=regex "$(file_or_null "$CONFIG_DIR/postfix-regex.cf")" > "$tmp" || exit 1

# rewritten, never deleted: docker-mailserver keeps the rules of a removed file
if [[ -s "$tmp" ]]; then
    chmod 644 "$tmp" && mv -f "$tmp" "$OUT"
fi
