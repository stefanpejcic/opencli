# OpenCLI  

OpenCLI is the command-line interface for managing [OpenPanel](https://openpanel.com/).  

On installation, the `opencli.sh` script is added to the system path, and `opencli --help` generates the list of all commands (`aliases.txt`).  

All scripts from `/usr/local/opencli/` can be accessed using the `opencli` command by replacing `/` with `-`.  

For example: to run user/add.sh script you would type: `opencli user-add`.

## Tab completion

`lib/completion.bash` provides bash tab-completion for `opencli` commands and their arguments (usernames, domains, plan names, PHP versions, and more, pulled live from the panel database and filesystem). It's symlinked into `/etc/bash_completion.d/opencli` automatically whenever `opencli update` runs, which also installs the `bash-completion` package if it isn't already present.

## Updates

OpenPanel is proud of the modularity, so you can independently update just the OpenCLI when needed.

To update OpenCLI:

```sh
opencli update --cli
```
