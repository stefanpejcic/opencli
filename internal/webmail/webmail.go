// ##############################################################################
// Script Name: internal/webmail/webmail.go
// Description: Which webmail client (roundcube or golub) the server runs for its users.
// Usage: imported by command packages
// Author: Stefan Pejcic
// Created: 27.11.2026
// Last Modified: 27.11.2026
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

package webmail

import (
	"strings"

	"opencli/internal/conf"
)

// EnvFile holds WEBMAIL_CLIENT next to the mailserver settings.
var EnvFile = "/usr/local/mail/openmail/mailserver.env"

// Service is the compose service users get, roundcube unless the admin switched to golub.
func Service() string {
	if v, _ := conf.Raw(EnvFile, "WEBMAIL_CLIENT"); strings.TrimSpace(v) == "golub" {
		return "golub"
	}
	return "roundcube"
}

// Container is the container name of Service.
func Container() string { return "openadmin_" + Service() }

// Name is how the client shows up on the OpenAdmin services page.
func Name(service string) string {
	if service == "golub" {
		return "Golub (Webmail)"
	}
	return "Roundcube (Webmail)"
}
