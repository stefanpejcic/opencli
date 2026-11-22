// ##############################################################################
// Script Name: internal/commands/useracct/restore_image_test.go
// Description: Tests for user-restore.
// Usage: go test ./...
// Author: Stefan Pejcic
// Created: 22.11.2026
// Last Modified: 22.11.2026
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

package useracct

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComposeServiceImage(t *testing.T) {
	dir := t.TempDir()
	cf, env := filepath.Join(dir, "docker-compose.yml"), filepath.Join(dir, ".env")
	os.WriteFile(cf, []byte(`networks:
    www:
        driver: bridge
services:
    apache:
        container_name: apache
        image: httpd:${APACHE_VERSION}
    php-fpm-8.3:
        container_name: php-fpm-8.3
        image: "openpanel/php-fpm:${PHP_TAG:-8.3}"
    api:
        container_name: api
        image: node:26.13.1
volumes:
    html_data:
`), 0o644)
	os.WriteFile(env, []byte("APACHE_VERSION=\"alpine\"\n# comment\n"), 0o644)
	for name, want := range map[string]string{"apache": "httpd:alpine", "php-fpm-8.3": "openpanel/php-fpm:8.3", "api": "node:26.13.1", "missing": ""} {
		if got := composeServiceImage(cf, env, name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}
