#!/bin/bash
# ======================================================================
# Multi-server helpers, sourced by the opencli dispatcher. On a master
# with slaves they show lists from all servers, run account commands
# where the account lives and handle --server= and --all-servers.
# Only defines functions.
# ======================================================================

CLUSTER_STATE="/etc/openpanel/openadmin/cluster/cluster.json"

# lists from every server unless --local is passed
CLUSTER_LIST_CMDS=" user-list domains-all websites-all plan-list ftp-list email-manage "
# run on the server that has the account
CLUSTER_USER_CMDS=" domains-user websites-user user-suspend user-unsuspend user-password user-change_plan user-quota user-2fa user-email user-login user-delete user-litespeed php-default "
# run on the server that has the domain
CLUSTER_DOMAIN_CMDS=" domains-whoowns domains-suspend domains-unsuspend domains-delete domains-ssl domains-dns domains-docroot domains-update_ns "
# these take --all-servers
CLUSTER_ALL_CMDS=" config waf plan-create plan-edit plan-delete domains-update_ns license update sentinel version imunify api email-server "
# --server= works for these, same list the slaves accept
CLUSTER_SERVER_CMDS="${CLUSTER_LIST_CMDS}${CLUSTER_USER_CMDS}${CLUSTER_DOMAIN_CMDS}${CLUSTER_ALL_CMDS} user-add domains-add "

cluster_bin() {
    local b
    for b in /usr/local/admin/openadmin-amd64 /usr/local/admin/openadmin-arm64; do
        [[ -x "$b" ]] && { echo "$b"; return 0; }
    done
    return 1
}

# prints the live slaves, one per line, nothing when this isn't a master with slaves
cluster_slaves() {
    [[ "${OPENCLI_CLUSTER_LOCAL:-}" == "1" ]] && return 0
    [[ -f "$CLUSTER_STATE" ]] || return 0
    grep -q '"role": *"master"' "$CLUSTER_STATE" 2>/dev/null || return 0
    local bin
    bin=$(cluster_bin) || return 0
    "$bin" cluster-exec servers 2>/dev/null
}

cluster_self() { hostname 2>/dev/null; }

# true when NAME is this server, by full or short hostname
cluster_is_self() {
    local name="$1" self
    self=$(cluster_self)
    [[ "${name,,}" == "${self,,}" || "${name,,}" == "${self%%.*}" ]]
}

cluster_header() { echo "── $1 ──"; }

# runs opencli on slaves and prints the raw json results
cluster_exec_json() {
    local target="$1"; shift
    "$(cluster_bin)" cluster-exec run "$target" -- "$@"
}

# runs opencli on slaves and prints each server's output under its name, returns the worst exit code
cluster_run_remote() {
    local target="$1"; shift
    local json rc=0
    local errfile
    errfile=$(mktemp)
    json=$(cluster_exec_json "$target" "$@" 2>"$errfile") || { echo "Error: $(cat "$errfile")" >&2; rm -f "$errfile"; return 1; }
    rm -f "$errfile"
    while IFS= read -r row; do
        local name out err code
        name=$(jq -r '.server' <<< "$row")
        out=$(jq -r '.out' <<< "$row")
        err=$(jq -r '.error // empty' <<< "$row")
        code=$(jq -r '.rc' <<< "$row")
        cluster_header "$name"
        [[ -n "$out" ]] && printf '%s\n' "$out"
        [[ -n "$err" ]] && echo "Error: $err" >&2
        (( code > rc )) && rc=$code
    done < <(jq -c '.[]' <<< "$json")
    return "$rc"
}

# joins json from all servers: arrays and {"data": ...} get merged with a server field, numbers are summed
cluster_merge_json() {
    python3 -c '
import json, sys
parts = json.load(sys.stdin)
def tag(v, name):
    return {"server": name, **v} if isinstance(v, dict) else v
items, data_list, data_dict, total, other, base = [], None, None, None, {}, {}
for name, raw in parts:
    try:
        v = json.loads(raw)
    except ValueError:
        if raw.strip():
            other[name] = raw.strip()
        continue
    if isinstance(v, bool) or not isinstance(v, (int, float, list, dict)):
        other[name] = v
    elif isinstance(v, (int, float)):
        total = (total or 0) + v
    elif isinstance(v, list):
        items += [tag(i, name) for i in v]
    elif isinstance(v.get("data"), list):
        base = base or v
        data_list = (data_list or []) + [tag(i, name) for i in v["data"]]
    elif isinstance(v.get("data"), dict):
        base = base or v
        data_dict = data_dict or {}
        data_dict.update({k: tag(i, name) for k, i in v["data"].items()})
    else:
        other[name] = v
if data_list is not None or data_dict is not None:
    data = data_list if data_list is not None else data_dict
    # an empty list from a server with nothing on it shouldnt turn a dict into a list
    if data_list is not None and data_dict is not None:
        data = data_dict if not data_list else data_list
    out = {**base, "data": data}
elif total is not None and not items:
    out = total
elif items or not other:
    out = items
else:
    out = {}
if other:
    print("\n".join("%s: %s" % (k, v) for k, v in other.items()), file=sys.stderr)
print(json.dumps(out, indent=2))
'
}

# runs a list command here and on every slave, json gets merged, text gets a header per server
cluster_list() {
    local script="$1" command="$2"; shift 2
    local json_mode=false a local_out remote rc=0
    for a in "$@"; do [[ "$a" == "--json" ]] && json_mode=true; done

    if ! $json_mode; then
        cluster_header "$(cluster_self)"
        bash "$script" "$@" || rc=$?
        cluster_run_remote all "$command" "$@" || rc=$?
        return "$rc"
    fi

    local_out=$(bash "$script" "$@") || rc=$?
    remote=$(cluster_exec_json all "$command" "$@") || remote="[]"
    jq -n --arg self "$(cluster_self)" --arg out "$local_out" --argjson remote "$remote" \
        '[[$self, $out]] + [$remote[] | select(.error == null) | [.server, .out]]' | cluster_merge_json
    jq -r '.[] | select(.error != null) | "Error: \(.server): \(.error)"' <<< "$remote" >&2
    return "$rc"
}

cluster_has_user_locally() {
    # shellcheck disable=SC1091
    source /usr/local/opencli/db.sh
    local u
    u=$(mysql_escape "$1")
    [[ "$(timeout 10 mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -se \
        "SELECT COUNT(*) FROM users WHERE username='$u' OR username LIKE 'SUSPENDED\\_%\\_$u'" 2>/dev/null)" -gt 0 ]]
}

cluster_has_domain_locally() {
    # shellcheck disable=SC1091
    source /usr/local/opencli/db.sh
    local d
    d=$(mysql_escape "$1")
    [[ "$(timeout 10 mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -se \
        "SELECT COUNT(*) FROM domains WHERE domain_url='$d'" 2>/dev/null)" -gt 0 ]]
}

# picks the account or domain a routed command acts on
cluster_subject() {
    local command="$1"; shift
    local positional=() a prev=""
    for a in "$@"; do
        if [[ "$command" == "user-quota" && "$prev" == "--update" ]]; then echo "$a"; return; fi
        prev="$a"
        [[ "$a" == -* ]] || positional+=("$a")
    done
    case "$command" in
        user-quota) return ;;
        # domains-dns <action> [DOMAIN]
        domains-dns) echo "${positional[1]:-}" ;;
        *) echo "${positional[0]:-}" ;;
    esac
}

# prints the slave that has the account or domain, nothing when it's here or nowhere
cluster_owner() {
    local kind="$1" value="$2" names
    [[ -n "$value" ]] || return 0
    if [[ "$kind" == "user" ]]; then
        cluster_has_user_locally "$value" && return 0
    else
        cluster_has_domain_locally "$value" && return 0
    fi
    names=$("$(cluster_bin)" cluster-exec find "$kind" "$value" 2>/dev/null)
    [[ $(grep -c . <<< "$names") -eq 1 ]] && echo "$names"
}

# plan ids differ per server, so plan-edit id=N is sent to each slave with that slave's id for the same plan name
cluster_plan_edit_all() {
    local plan_name="$1"; shift
    local rc=0 server remote_id args a
    while IFS= read -r server; do
        [[ -n "$server" ]] || continue
        remote_id=$(cluster_exec_json "$server" plan-list --json | jq -r --arg n "$plan_name" '.[0].out | fromjson? | .[]? | select(.name == $n) | .id' | head -1)
        if [[ -z "$remote_id" ]]; then
            cluster_header "$server"
            echo "Plan '$plan_name' doesn't exist here, skipped."
            continue
        fi
        args=()
        for a in "$@"; do
            [[ "$a" == id=* ]] && a="id=$remote_id"
            args+=("$a")
        done
        cluster_run_remote "$server" plan-edit "${args[@]}" || rc=$?
    done <<< "$(cluster_slaves)"
    return "$rc"
}

cluster_plan_name_by_id() {
    # shellcheck disable=SC1091
    source /usr/local/opencli/db.sh
    local id
    id=$(mysql_escape "$1")
    timeout 10 mariadb --defaults-extra-file="$config_file" -D "$mysql_database" -se "SELECT name FROM plans WHERE id='$id'" 2>/dev/null
}
