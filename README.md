# OpenCLI

OpenCLI is the command-line interface for managing [OpenPanel](https://openpanel.com/).

It's a single Go binary installed at `/usr/local/opencli/opencli` and linked to `/usr/local/bin/opencli`. Commands keep the names of the old bash scripts they replaced, with `/` swapped for `-`, so `user/add.sh` is `opencli user-add`. `opencli --help` lists every command and regenerates `aliases.txt`.

## Layout

- `cmd/opencli`: the dispatcher (logging, pre/post hooks from `/etc/openpanel/openpanel/hooks`, multi-server routing, `--help`, `error` and `locale`).
- `internal/commands/<group>`: one file per command. Each one registers itself with `cli.Register` in `init`.
- `internal/*`: shared helpers (running programs, MariaDB, config files, Podman, Redis, notifications, emails, tenant-safe file operations and more).

## Building

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o opencli ./cmd/opencli
go test ./...
```

Every push runs `.github/workflows/build.yml`. It builds `opencli-amd64` and `opencli-arm64`, publishes a release named after the current OpenPanel version plus a build number (`2.0.28-1`, `2.0.28-2`, ...) and updates the rolling `latest` release.

## Tab completion

`internal/completion/completion.bash` provides bash tab-completion for `opencli` commands and their arguments (usernames, domains, plan names, PHP versions, and more, pulled live from the panel database and filesystem). It's embedded in the binary: `opencli update --cli` writes it to `/usr/local/opencli/lib/completion.bash`, symlinks it into `/etc/bash_completion.d/opencli` and installs the `bash-completion` package if it isn't already there.

## Updates

OpenPanel is modular, so you can update just OpenCLI when needed:

```sh
opencli update --cli
```

This downloads `opencli-amd64` or `opencli-arm64` from the `latest` release, checks it against `SHA256SUMS` and that it runs, then swaps it in place of `/usr/local/opencli/opencli` and removes anything else in that folder except `aliases.txt` and `lib/completion.bash` (the old bash scripts).
