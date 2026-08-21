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
_opencli_username_arg_commands="user-2fa user-check user-delete user-email user-ip user-login user-loginlog user-password user-quota user-rename user-suspend user-unsuspend user-varnish user-block_ip user-change_plan domains-user php-default websites-user files-fix_permissions docker-logs"

# commands whose next positional argument is a domain name
_opencli_domain_arg_commands="domains-add domains-delete domains-dnssec domains-dns domains-docroot domains-edit domains-hsts domains-ssl domains-stats domains-suspend domains-unsuspend domains-update_ns domains-varnish domains-whoowns php-domain websites-pagespeed websites-secure websites-vulnerability"

# commands whose next positional argument is a plan name
_opencli_plan_arg_commands="plan-usage plan-delete"

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
        fi
        return 0
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
        esac
    fi

    return 0
}

complete -F _opencli_completions opencli
