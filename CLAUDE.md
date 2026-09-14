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
