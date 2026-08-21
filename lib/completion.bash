# Description: Bash tab-completion for the opencli command
# Usage: source this file (completion.bash), or install it to /etc/bash_completion.d/opencli
# Docs: https://docs.openpanel.com

_opencli_scripts_dir="/usr/local/opencli"
_opencli_mysql_defaults="/etc/my.cnf"
_opencli_mysql_database="panel"

_opencli_query() {
    [[ -f "$_opencli_mysql_defaults" ]] || return
    command -v mariadb &>/dev/null || return
    mariadb --defaults-extra-file="$_opencli_mysql_defaults" -D "$_opencli_mysql_database" -sN -e "$1" 2>/dev/null
}

_opencli_usernames() {
    _opencli_query "SELECT username FROM users;"
}

_opencli_domains() {
    _opencli_query "SELECT domain_url FROM domains;"
}

_opencli_plan_names() {
    _opencli_query "SELECT name FROM plans;"
}

# admin/reseller logins live in a separate SQLite DB, not the panel MariaDB
_opencli_admin_usernames() {
    local db="/etc/openpanel/openadmin/users.db"
    [[ -f "$db" ]] && command -v sqlite3 &>/dev/null || return
    sqlite3 "$db" "SELECT username FROM user;" 2>/dev/null
}

_opencli_reseller_usernames() {
    local db="/etc/openpanel/openadmin/users.db"
    [[ -f "$db" ]] && command -v sqlite3 &>/dev/null || return
    sqlite3 "$db" "SELECT username FROM user WHERE role='reseller';" 2>/dev/null
}

# feature_set names are the *.txt filenames under the features directory
_opencli_feature_set_names() {
    local dir="/etc/openpanel/openpanel/features"
    [[ -d "$dir" ]] || return
    for f in "$dir"/*.txt; do
        [[ -f "$f" ]] || continue
        printf '%s\n' "$(basename "$f" .txt)"
    done
}

# FTP sub-account usernames, flattened across every owner's users.list
_opencli_ftp_usernames() {
    find /etc/openpanel/ftp/users -mindepth 2 -maxdepth 2 -name users.list 2>/dev/null \
        -exec cut -d'|' -f1 {} \; | sort -u
}

_opencli_site_types() {
    _opencli_query "SELECT DISTINCT LOWER(type) FROM sites;"
}

_opencli_plan_ids() {
    _opencli_query "SELECT id FROM plans;"
}

# opencli user-backup writes to /home/<user>/docker-data/volumes/<user>_html_data/_data/_backups
# by default (unless --output overrides it), as <user>_<timestamp>.tar.gz
_opencli_backup_archives() {
    local f
    for f in /home/*/docker-data/volumes/*_html_data/_data/_backups/*.tar.gz; do
        [[ -f "$f" ]] && printf '%s\n' "$f"
    done
}

_opencli_wp_secure_rules() {
    local rules_file="/etc/openpanel/caddy/templates/wp.rules"
    [[ -f "$rules_file" ]] || return
    grep -oP '^\(\K[a-z0-9_]+' "$rules_file" | grep '^wp_manager_'
}

# mirrors what `opencli locale` (with no args) lists itself: the directory
# names in the openpanel-translations repo, fetched live via the GitHub API
_opencli_available_locales() {
    command -v jq &>/dev/null || return
    curl -s --max-time 3 "https://api.github.com/repos/stefanpejcic/openpanel-translations/contents" 2>/dev/null \
        | jq -r '.[] | select(.type=="dir" and (.name|test("^\\.")|not)) | .name' 2>/dev/null
}

# notification parameter names are just the keys defined in notifications.ini
_opencli_notification_params() {
    local ini="/etc/openpanel/openadmin/config/notifications.ini"
    [[ -f "$ini" ]] || return
    grep -E '^[A-Za-z_]+=' "$ini" | cut -d= -f1 | sort -u
}

# panel config setting names are the keys defined in openpanel.config
_opencli_config_params() {
    local conf="/etc/openpanel/openpanel/conf/openpanel.config"
    [[ -f "$conf" ]] || return
    grep -E '^[A-Za-z0-9_]+=' "$conf" | cut -d= -f1 | sort -u
}

# container names for a user's rootless podman instance
_opencli_containers_for_user() {
    local username="$1" uid sock
    [[ -n "$username" ]] || return
    command -v podman &>/dev/null || return
    uid=$(stat -c '%u' "/home/${username}" 2>/dev/null) || return
    sock="unix:///hostfs/run/user/${uid}/podman/podman.sock"
    CONTAINER_HOST="$sock" podman --remote ps --format '{{.Names}}' 2>/dev/null
}

_opencli_mysql_escape() {
    local s="$1"
    s="${s//\\/\\\\}"
    s="${s//\'/\\\'}"
    printf '%s' "$s"
}

# PHP versions are per-user: each user's docker-compose.yml only defines
# php-fpm-N.N services for the versions actually available to them
_opencli_php_versions_for_user() {
    local username="$1" compose="/home/${1}/docker-compose.yml"
    [[ -n "$username" && -f "$compose" ]] || return
    grep -oE '^[[:space:]]*php-fpm-[0-9]+\.[0-9]+:' "$compose" | \
        sed -E 's/^[[:space:]]*php-fpm-//; s/:$//' | sort -u
}

_opencli_php_versions_for_domain() {
    local domain="$1" username
    [[ -n "$domain" ]] || return
    username=$(_opencli_query "SELECT users.username FROM domains INNER JOIN users ON domains.user_id = users.id WHERE domains.domain_url = '$(_opencli_mysql_escape "$domain")';")
    [[ -n "$username" ]] && _opencli_php_versions_for_user "$username"
}

# plan names may contain spaces, so they can't go through compgen -W (which
# word-splits its argument); match and quote them manually instead
_opencli_complete_plan_names() {
    local cur="$1" name
    while IFS= read -r name; do
        [[ -n "$name" && "$name" == "$cur"* ]] || continue
        if [[ "$name" == *" "* ]]; then
            COMPREPLY+=( "'${name}'" )
        else
            COMPREPLY+=( "$name" )
        fi
    done < <(_opencli_plan_names)
}

_opencli_command_names() {
    local aliases_file="${_opencli_scripts_dir}/aliases.txt"
    [[ -f "$aliases_file" ]] && awk '{print $2}' "$aliases_file"
}

# commands whose next positional argument is a username
_opencli_username_arg_commands="user-2fa user-check user-delete user-email user-ip user-login user-loginlog user-password user-rename user-suspend user-unsuspend user-varnish user-block_ip user-change_plan domains-user php-default websites-user docker ftp-list"

# commands whose next positional argument is a domain name
_opencli_domain_arg_commands="domains-add domains-delete domains-dnssec domains-dns domains-docroot domains-edit domains-hsts domains-ssl domains-suspend domains-unsuspend domains-update_ns domains-varnish domains-whoowns php-domain websites-pagespeed websites-vulnerability"

# commands whose next positional argument is a plan name
_opencli_plan_arg_commands="plan-usage plan-delete"

# commands whose next positional argument is a username or the literal --all
_opencli_username_or_all_arg_commands="docker-collect_stats user-quota"

# commands whose next positional argument is an FTP sub-account username
_opencli_ftp_username_arg_commands="ftp-delete ftp-password ftp-path"

_opencli_completions() {
    local cur prev cmd
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    cmd="${COMP_WORDS[1]}"
    COMPREPLY=()

    # top-level: complete the subcommand name
    if [[ $COMP_CWORD -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "$(_opencli_command_names)" -- "$cur") )
        return 0
    fi

    # opencli locale <CODE> [<CODE> ...] (e.g. de-de, sr-rs) — accepts multiple, any position
    if [[ "$cmd" == "locale" ]]; then
        COMPREPLY=( $(compgen -W "$(_opencli_available_locales)" -- "$cur") )
        return 0
    fi

    # opencli admin <subcommand> ...
    if [[ "$cmd" == "admin" ]]; then
        if [[ $COMP_CWORD -eq 2 ]]; then
            COMPREPLY=( $(compgen -W "on off port log logs list new password update rename suspend unsuspend notifications" -- "$cur") )
            return 0
        fi
        case "${COMP_WORDS[2]}" in
            password|update|rename|suspend|unsuspend)
                # opencli admin <password|update|rename|suspend|unsuspend> <admin_username> ...
                if [[ $COMP_CWORD -eq 3 ]]; then
                    COMPREPLY=( $(compgen -W "$(_opencli_admin_usernames)" -- "$cur") )
                fi
                return 0
                ;;
            notifications)
                # opencli admin notifications <check|get|update> <param> [value]
                if [[ $COMP_CWORD -eq 3 ]]; then
                    COMPREPLY=( $(compgen -W "check get update" -- "$cur") )
                elif [[ $COMP_CWORD -eq 4 && ( "${COMP_WORDS[3]}" == "get" || "${COMP_WORDS[3]}" == "update" ) ]]; then
                    COMPREPLY=( $(compgen -W "$(_opencli_notification_params)" -- "$cur") )
                fi
                return 0
                ;;
        esac
        return 0
    fi

    # opencli config <get|update> <setting_name> [new_value]
    if [[ "$cmd" == "config" ]]; then
        if [[ $COMP_CWORD -eq 2 ]]; then
            COMPREPLY=( $(compgen -W "get update" -- "$cur") )
        elif [[ $COMP_CWORD -eq 3 && ( "${COMP_WORDS[2]}" == "get" || "${COMP_WORDS[2]}" == "update" ) ]]; then
            COMPREPLY=( $(compgen -W "$(_opencli_config_params)" -- "$cur") )
        fi
        return 0
    fi

    # commands with a fixed, static set of subcommands/flags as their first argument
    if [[ $COMP_CWORD -eq 2 ]]; then
        case "$cmd" in
            api)
                COMPREPLY=( $(compgen -W "status on off list" -- "$cur") )
                return 0
                ;;
            imunify)
                COMPREPLY=( $(compgen -W "status start stop install update uninstall" -- "$cur") )
                return 0
                ;;
            email-server)
                COMPREPLY=( $(compgen -W "status config install start stop restart queue flush view unhold delete fail2ban ports logs login supervisor postfwd pflogsumm update-check update-packages versions" -- "$cur") )
                return 0
                ;;
            update)
                COMPREPLY=( $(compgen -W "--check --force --admin --panel --cli" -- "$cur") )
                return 0
                ;;
            domain)
                COMPREPLY=( $(compgen -W "set ip --debug" -- "$cur") )
                return 0
                ;;
            docker-autostart)
                COMPREPLY=( $(compgen -W "-f --force" -- "$cur") )
                return 0
                ;;
            waf)
                COMPREPLY=( $(compgen -W "status enable disable domain tags ids update stats" -- "$cur") )
                return 0
                ;;
            plan-apply)
                # opencli plan-apply <NEW_PLAN_ID> <USERNAME>
                COMPREPLY=( $(compgen -W "$(_opencli_plan_ids)" -- "$cur") )
                return 0
                ;;
            files-purge_trash)
                # opencli files-purge_trash [--user USERNAME] [--force] [--dry-run]
                COMPREPLY=( $(compgen -W "--user --force --dry-run" -- "$cur") )
                return 0
                ;;
            docker-logs)
                # opencli docker-logs [--all|--system|--users|<USERNAME>]
                COMPREPLY=( $(compgen -W "$(_opencli_usernames) --all --system --users" -- "$cur") )
                return 0
                ;;
            files-fix_permissions)
                # opencli files-fix_permissions <USERNAME|--all> [PATH] [--debug]
                COMPREPLY=( $(compgen -W "$(_opencli_usernames) --all" -- "$cur") )
                return 0
                ;;
            user-backup)
                # opencli user-backup --account <USER> [--output <DIR>] [--quiet]
                COMPREPLY=( $(compgen -W "--account --output --quiet" -- "$cur") )
                return 0
                ;;
            user-restore)
                # opencli user-restore --file <ARCHIVE> [--force] [--new-username=NAME] [--quiet] [--temp-dir=<PATH>]
                COMPREPLY=( $(compgen -W "--file --force --new-username= --quiet --temp-dir=" -- "$cur") )
                return 0
                ;;
            websites-secure)
                # opencli websites-secure <DOMAIN> [...]
                # opencli websites-secure --list-available-rules
                COMPREPLY=( $(compgen -W "$(_opencli_domains) --list-available-rules" -- "$cur") )
                return 0
                ;;
            email-manage|email-setup)
                # setup { email | alias | quota | dovecot-master | config | relay | fail2ban | debug }
                COMPREPLY=( $(compgen -W "email alias quota dovecot-master config relay fail2ban debug" -- "$cur") )
                return 0
                ;;
        esac
    fi

    # opencli files-purge_trash --user <USERNAME>
    if [[ "$cmd" == "files-purge_trash" && "$prev" == "--user" ]]; then
        COMPREPLY=( $(compgen -W "$(_opencli_usernames)" -- "$cur") )
        return 0
    fi

    # opencli websites-secure <DOMAIN> [--rules='...' | --disable-all | --list-active-rules]
    if [[ "$cmd" == "websites-secure" && $COMP_CWORD -eq 3 && "$cur" != --rules=* ]]; then
        COMPREPLY=( $(compgen -W "--rules= --disable-all --list-active-rules" -- "$cur") )
        return 0
    fi

    # opencli email-manage/email-setup <COMMAND> <SUBCOMMAND> ...
    if [[ "$cmd" == "email-manage" || "$cmd" == "email-setup" ]]; then
        case "${COMP_WORDS[2]}" in
            email)
                [[ $COMP_CWORD -eq 3 ]] && COMPREPLY=( $(compgen -W "add update del restrict list" -- "$cur") )
                return 0
                ;;
            alias)
                [[ $COMP_CWORD -eq 3 ]] && COMPREPLY=( $(compgen -W "add del list" -- "$cur") )
                return 0
                ;;
            quota)
                [[ $COMP_CWORD -eq 3 ]] && COMPREPLY=( $(compgen -W "set del" -- "$cur") )
                return 0
                ;;
            dovecot-master)
                [[ $COMP_CWORD -eq 3 ]] && COMPREPLY=( $(compgen -W "add update del list" -- "$cur") )
                return 0
                ;;
            config)
                [[ $COMP_CWORD -eq 3 ]] && COMPREPLY=( $(compgen -W "dkim" -- "$cur") )
                return 0
                ;;
            relay)
                [[ $COMP_CWORD -eq 3 ]] && COMPREPLY=( $(compgen -W "add-auth add-domain exclude-domain" -- "$cur") )
                return 0
                ;;
            fail2ban)
                [[ $COMP_CWORD -eq 3 ]] && COMPREPLY=( $(compgen -W "ban unban log status" -- "$cur") )
                return 0
                ;;
            debug)
                [[ $COMP_CWORD -eq 3 ]] && COMPREPLY=( $(compgen -W "fetchmail getmail login show-mail-logs" -- "$cur") )
                return 0
                ;;
        esac
    fi

    # opencli websites-secure <domain> --rules='rule1 rule2 ...'  (multi-value, space-separated inside one quoted arg)
    if [[ "$cmd" == "websites-secure" && "$cur" == --rules=* ]]; then
        local already="${cur#--rules=}" quote="" partial=""
        if [[ "$already" == \'* ]]; then quote="'"; already="${already#\'}"; fi
        if [[ "$already" == *" "* ]]; then
            partial="${already##* }"
            already="${already% *} "
        else
            partial="$already"
            already=""
        fi
        while IFS= read -r rule; do
            [[ -n "$rule" && "$rule" == "$partial"* ]] || continue
            COMPREPLY+=( "--rules=${quote}${already}${rule}" )
        done < <(_opencli_wp_secure_rules)
        return 0
    fi

    # opencli waf domain <DOMAIN> [enable|disable]
    # opencli waf stats <country|agent|hourly|ip|request|path>
    if [[ "$cmd" == "waf" ]]; then
        case "${COMP_WORDS[2]}" in
            domain)
                if [[ $COMP_CWORD -eq 3 ]]; then
                    COMPREPLY=( $(compgen -W "$(_opencli_domains)" -- "$cur") )
                elif [[ $COMP_CWORD -eq 4 ]]; then
                    COMPREPLY=( $(compgen -W "enable disable" -- "$cur") )
                fi
                return 0
                ;;
            stats)
                if [[ $COMP_CWORD -eq 3 ]]; then
                    COMPREPLY=( $(compgen -W "country agent hourly ip request path" -- "$cur") )
                fi
                return 0
                ;;
        esac
    fi

    # first argument after the subcommand
    if [[ $COMP_CWORD -eq 2 ]]; then
        if [[ " $_opencli_username_arg_commands " == *" $cmd "* ]]; then
            COMPREPLY=( $(compgen -W "$(_opencli_usernames)" -- "$cur") )
            return 0
        elif [[ " $_opencli_domain_arg_commands " == *" $cmd "* ]]; then
            COMPREPLY=( $(compgen -W "$(_opencli_domains)" -- "$cur") )
            return 0
        elif [[ " $_opencli_plan_arg_commands " == *" $cmd "* ]]; then
            _opencli_complete_plan_names "$cur"
            return 0
        elif [[ " $_opencli_username_or_all_arg_commands " == *" $cmd "* ]]; then
            COMPREPLY=( $(compgen -W "$(_opencli_usernames) --all" -- "$cur") )
            return 0
        elif [[ " $_opencli_ftp_username_arg_commands " == *" $cmd "* ]]; then
            COMPREPLY=( $(compgen -W "$(_opencli_ftp_usernames)" -- "$cur") )
            return 0
        elif [[ "$cmd" == "websites-scan" ]]; then
            # opencli websites-scan <USERNAME> OR opencli websites-scan -all
            COMPREPLY=( $(compgen -W "$(_opencli_usernames) -all" -- "$cur") )
            return 0
        fi
    fi

    # second positional argument: some commands pair a domain/username with a username
    if [[ $COMP_CWORD -eq 3 ]]; then
        case "$cmd" in
            domains-add|plan-apply)
                # opencli domains-add <DOMAIN> <USERNAME> ...
                # opencli plan-apply <PLAN_ID> <USERNAME>
                COMPREPLY=( $(compgen -W "$(_opencli_usernames)" -- "$cur") )
                return 0
                ;;
            user-rename)
                # opencli user-rename <old_username> <new_username>
                COMPREPLY=( $(compgen -W "$(_opencli_usernames)" -- "$cur") )
                return 0
                ;;
            user-change_plan)
                # opencli user-change_plan <USERNAME> <NEW_PLAN_NAME>
                _opencli_complete_plan_names "$cur"
                return 0
                ;;
            docker)
                # opencli docker <user> <container>
                COMPREPLY=( $(compgen -W "$(_opencli_containers_for_user "${COMP_WORDS[2]}")" -- "$cur") )
                return 0
                ;;
            domains-hsts)
                # opencli domains-hsts <domain> [enable|disable]
                COMPREPLY=( $(compgen -W "enable disable" -- "$cur") )
                return 0
                ;;
            domains-ssl)
                # opencli domains-ssl <domain> [status|info|logs|auto|custom]
                COMPREPLY=( $(compgen -W "status info logs auto custom" -- "$cur") )
                return 0
                ;;
            domains-dnssec)
                # opencli domains-dnssec <domain> [--update|--check]
                COMPREPLY=( $(compgen -W "--update --check" -- "$cur") )
                return 0
                ;;
            domains-varnish)
                # opencli domains-varnish <domain> [on|off] [--short]
                COMPREPLY=( $(compgen -W "on off" -- "$cur") )
                return 0
                ;;
            domains-docroot)
                # opencli domains-docroot <domain> [update <path>]
                COMPREPLY=( $(compgen -W "update" -- "$cur") )
                return 0
                ;;
            user-varnish)
                # opencli user-varnish <username> [enable|disable|status]
                COMPREPLY=( $(compgen -W "enable disable status" -- "$cur") )
                return 0
                ;;
            user-ip)
                # opencli user-ip <username> <IP|DELETE>
                COMPREPLY=( $(compgen -W "DELETE" -- "$cur") )
                return 0
                ;;
            user-2fa)
                # opencli user-2fa <username> [disable]
                COMPREPLY=( $(compgen -W "disable" -- "$cur") )
                return 0
                ;;
            user-login)
                # opencli user-login <username> [--open|--delete]
                COMPREPLY=( $(compgen -W "--open --delete" -- "$cur") )
                return 0
                ;;
            user-loginlog)
                # opencli user-loginlog <username> [--table|--text|--json]
                COMPREPLY=( $(compgen -W "--table --text --json" -- "$cur") )
                return 0
                ;;
            user-add)
                # opencli user-add <username> <PASSWORD|generate> ...
                COMPREPLY=( $(compgen -W "generate" -- "$cur") )
                return 0
                ;;
            ftp-delete)
                # opencli ftp-delete <ftp_username> <openpanel_username>
                COMPREPLY=( $(compgen -W "$(_opencli_usernames)" -- "$cur") )
                return 0
                ;;
        esac
    fi

    # opencli user-add <username> <password> <email> "<PLAN_NAME>" [flags]
    if [[ "$cmd" == "user-add" && $COMP_CWORD -eq 5 ]]; then
        _opencli_complete_plan_names "$cur"
        return 0
    fi

    # opencli ftp-password <ftp_username> <new_password> <openpanel_username>
    # opencli ftp-path <ftp_username> <path> <openpanel_username>
    if [[ ( "$cmd" == "ftp-password" || "$cmd" == "ftp-path" ) && $COMP_CWORD -eq 4 ]]; then
        COMPREPLY=( $(compgen -W "$(_opencli_usernames)" -- "$cur") )
        return 0
    fi

    # opencli ftp-add <new_username> <new_password> <folder> <openpanel_username>
    if [[ "$cmd" == "ftp-add" && $COMP_CWORD -eq 5 ]]; then
        COMPREPLY=( $(compgen -W "$(_opencli_usernames)" -- "$cur") )
        return 0
    fi

    # key=value style flags that can appear in any position
    case "$cmd" in
        user-add)
            case "$cur" in
                --sql=*)
                    COMPREPLY=( $(compgen -W "mysql mariadb" -- "${cur#--sql=}") )
                    return 0
                    ;;
                --webserver=*)
                    local val="${cur#--webserver=}"; val="${val#\"}"
                    COMPREPLY=( $(compgen -W "nginx apache openresty openlitespeed litespeed varnish+nginx varnish+apache varnish+openresty varnish+openlitespeed" -- "$val") )
                    return 0
                    ;;
                --reseller=*)
                    COMPREPLY=( $(compgen -W "$(_opencli_reseller_usernames)" -- "${cur#--reseller=}") )
                    return 0
                    ;;
            esac
            ;;
        plan-create|plan-edit)
            case "$cur" in
                feature_set=*)
                    COMPREPLY=( $(compgen -W "$(_opencli_feature_set_names)" -- "${cur#feature_set=}") )
                    return 0
                    ;;
            esac
            ;;
        websites-user)
            case "$cur" in
                --type=*)
                    COMPREPLY=( $(compgen -W "$(_opencli_site_types)" -- "${cur#--type=}") )
                    return 0
                    ;;
                --*)
                    COMPREPLY=( $(compgen -W "--type= --domains= --json" -- "$cur") )
                    return 0
                    ;;
            esac
            ;;
    esac

    # opencli php-default <username> --update <N.N>
    # opencli php-domain <domain> --update <N.N>
    # opencli domains-add <domain> <username> --php_version <N.N>
    if [[ "$prev" == "--update" || "$prev" == "--php_version" ]]; then
        case "$cmd" in
            php-default)
                COMPREPLY=( $(compgen -W "$(_opencli_php_versions_for_user "${COMP_WORDS[2]}")" -- "$cur") )
                return 0
                ;;
            php-domain)
                COMPREPLY=( $(compgen -W "$(_opencli_php_versions_for_domain "${COMP_WORDS[2]}")" -- "$cur") )
                return 0
                ;;
            domains-add)
                COMPREPLY=( $(compgen -W "$(_opencli_php_versions_for_user "${COMP_WORDS[3]}")" -- "$cur") )
                return 0
                ;;
        esac
    fi

    # opencli user-backup --account <USER> [--output <DIR>]
    if [[ "$cmd" == "user-backup" && "$prev" == "--account" ]]; then
        COMPREPLY=( $(compgen -W "$(_opencli_usernames)" -- "$cur") )
        return 0
    fi

    # opencli user-restore --file <ARCHIVE>
    if [[ "$cmd" == "user-restore" && "$prev" == "--file" ]]; then
        COMPREPLY=( $(compgen -W "$(_opencli_backup_archives)" -- "$cur") )
        return 0
    fi

    return 0
}

complete -F _opencli_completions opencli
