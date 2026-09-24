# Code comments

Write comments like a human dev jotting a quick note, not like documentation.

- Single line only. Don't wrap a comment across multiple lines unless the thing is genuinely too complex to say in one sentence.
- Keep it short and plain. No formal tone, no "Note:" prefixes, no " - " hyphen clauses stacked together, no restating what the code already makes obvious.
- Apply this whenever you write or edit comments in this repo, not just when asked to clean up comments specifically.

Exception is the multi-line comment at the beginning of the files, that one needs to have format with the copyright text, example:

```
#!/bin/bash
################################################################################
# Script Name: websites/all.sh
# Description: Lists all websites currently hosted on the server.
# Usage: opencli websites-all [TYPE]
# Author: Stefan Pejcic
# Created: 26.10.2023
# Last Modified: 21.08.2026
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
```


# Git

- After making changes, `git add` them and stop there. Don't `git commit` - the user commits themselves.

# Docs

Command docs live in `/home/stefan/OpenPanel/website/docs/articles/opencli/` (the OpenPanel repo, `../OpenPanel/website/docs/articles/opencli/` from here), published at https://openpanel.com/docs/articles/opencli/. They must always match the scripts exactly.

Whenever you change any script here, before finishing:

1. Open the matching docs page and compare it against the script's actual argument parsing, not just its `usage()` text. Pages map by command group: `update.sh` -> `update.md`, `domains/*.sh` -> `domains.md`, `user/*.sh` -> `user.md`, `email/*.sh` -> `email.md`, `plan/*.sh` -> `plan.md`, `docker.sh` and `docker/*.sh` -> `docker.md`, `files/*.sh` -> `files.md`, `ftp/*.sh` -> `ftp.md`, `php/*.sh` -> `php.md`, `server/*.sh` -> `server.md`, `websites/*.sh` -> `websites.md`, otherwise `<script>.md`.
2. Edit the page if anything is missing, wrong or outdated: new, renamed or removed commands, flags, arguments, defaults, behavior, output paths. Remove docs for anything the script no longer supports. Don't document things you haven't verified in the code.
3. Keep the script's `# Usage:` header and `usage()` output in sync with the flags it actually parses. `opencli --help` prints the header, and the example help output in `opencli.md` is built from the headers, so update that entry too.
4. Also check the pages that mirror or reference scripts: `faq.md` mirrors `faq.sh`, `config.md` documents every key in `/etc/openpanel/openpanel/conf/openpanel.config` (template in the openpanel-configuration repo) and which keys `config.sh` restarts OpenPanel for, and other docs may show the command in examples (`grep -rn "opencli <command>" /home/stefan/OpenPanel/website/docs`).
5. `git add` the docs change in the OpenPanel repo too, same as the script change.

If you notice a mismatch between docs and code that isn't part of your change, fix the docs as well, or point it out if the code looks like the part that's wrong.
