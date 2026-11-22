// ##############################################################################
// Script Name: cmd/opencli/commands.go
// Description: Links every command package into the binary.
// Usage: go build ./cmd/opencli
// Author: Stefan Pejcic
// Created: 21.11.2026
// Last Modified: 20.11.2026
// Company: openpanel.com
// Copyright (c) openpanel.com
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.
// ##############################################################################

package main

// every command package registers itself in init
import (
	_ "opencli/internal/commands/admin"
	_ "opencli/internal/commands/docker"
	_ "opencli/internal/commands/domaincmd"
	_ "opencli/internal/commands/domains"
	_ "opencli/internal/commands/domainsadd"
	_ "opencli/internal/commands/email"
	_ "opencli/internal/commands/files"
	_ "opencli/internal/commands/ftp"
	_ "opencli/internal/commands/general"
	_ "opencli/internal/commands/imunify"
	_ "opencli/internal/commands/license"
	_ "opencli/internal/commands/php"
	_ "opencli/internal/commands/plan"
	_ "opencli/internal/commands/sentinel"
	_ "opencli/internal/commands/server"
	_ "opencli/internal/commands/update"
	_ "opencli/internal/commands/user"
	_ "opencli/internal/commands/useracct"
	_ "opencli/internal/commands/userreport"
	_ "opencli/internal/commands/waf"
	_ "opencli/internal/commands/websites"
)
