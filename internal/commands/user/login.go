// ##############################################################################
// Script Name: internal/commands/user/login.go
// Description: Generate an auto-login link for OpenPanel user.
// Usage: opencli user-login <username> [--open|--delete]
// Author: Stefan Pejcic
// Created: 27.01.2026
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

package user

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-login",
		Desc:  "Generate an auto-login link for OpenPanel user.",
		Usage: "opencli user-login <username> [--open|--delete]",
		Run:   login,
	})
}

func loginURLEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '~' || c == '_' || c == '-' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

var loginIPRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$`)

func loginPublicIP() string {
	ip, ok := curl4("https://ip.openpanel.com", time.Second)
	if !ok {
		ip, _ = curl4("https://ifconfig.me/ip", time.Second)
	}
	if ip == "" || !loginIPRe.MatchString(ip) {
		ip = text.Field(sh.C("hostname", "-I").NoErr().Str(), 1)
	}
	return ip
}

// awk range between two markers, markers themselves not included
func loginBlock(lines []string, start, end string) []string {
	var out []string
	flag := false
	for _, l := range lines {
		if strings.Contains(l, start) {
			flag = true
			continue
		}
		if strings.Contains(l, end) {
			flag = false
		}
		if flag {
			out = append(out, l)
		}
	}
	return out
}

var (
	loginBlank   = regexp.MustCompile(`^\s*$`)
	loginComment = regexp.MustCompile(`^\s*#`)
	loginBrace   = regexp.MustCompile(`[[:space:]]*\{`)
	loginIPFind  = regexp.MustCompile(`\b([0-9]{1,3}\.){3}[0-9]{1,3}\b`)
)

func loginFirst(block []string) string {
	for _, l := range block {
		if !loginBlank.MatchString(l) && !loginComment.MatchString(l) {
			return l
		}
	}
	return ""
}

func loginOpenPanelURL() string {
	port := sh.Opencli("port").Str()
	if port == "" {
		port = "2083"
	}
	lines := conf.Lines("/etc/openpanel/caddy/Caddyfile")
	block := loginBlock(lines, "# START USERPANEL DOMAIN #", "# END USERPANEL DOMAIN #")
	if len(block) == 0 {
		block = loginBlock(lines, "# START HOSTNAME DOMAIN #", "# END HOSTNAME DOMAIN #")
	}
	domain := loginFirst(block)
	if loc := loginBrace.FindStringIndex(domain); loc != nil {
		domain = domain[:loc[0]] + domain[loc[1]:]
	}
	domain = text.Xargs(domain)
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimPrefix(domain, "https://")

	if domain == "" || domain == "example.net" {
		ipBlock := loginBlock(lines, "# START HOSTNAME IP #", "# END HOSTNAME IP #")
		ip := text.Xargs(strings.Join(loginIPFind.FindAllString(loginFirst(ipBlock), -1), "\n"))
		ip, _, _ = strings.Cut(ip, ":")
		ip, _, _ = strings.Cut(ip, ",")
		if ip == "" {
			return "http://" + loginPublicIP() + ":" + port + "/"
		}
		domain = ip
	}
	le := "/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/" + domain + "/" + domain
	custom := "/etc/openpanel/caddy/ssl/custom/" + domain + "/" + domain
	if (conf.IsFile(le+".crt") && conf.IsFile(le+".key")) || (conf.IsFile(custom+".crt") && conf.IsFile(custom+".key")) {
		return "https://" + domain + ":" + port + "/"
	}
	return "http://" + loginPublicIP() + ":" + port + "/"
}

func login(args []string) int {
	username := cli.Arg(args, 0)
	if username == "" {
		fmt.Println("Usage: opencli user-login <username>")
		return 1
	}
	openFlag := cli.HasFlag(args, "--open")
	deleteFlag := cli.HasFlag(args, "--delete")

	userDir := "/etc/openpanel/openpanel/core/users/" + username
	if !conf.IsDir(userDir) {
		// todo: handle case when user is suspended
		fmt.Printf("[✘] Error: Username '%s' does not exist or was not properly created (missing files).\n", username)
		return 1
	}
	tokenFile := userDir + "/logintoken.txt"
	var token string
	if conf.IsFile(tokenFile) {
		token = strings.TrimRight(conf.Read(tokenFile), "\n")
		if deleteFlag {
			os.RemoveAll(tokenFile)
			fmt.Printf("Auto-login token '%s' for user %s is now invalidated.\n", token, username)
			return 0
		}
	} else {
		if deleteFlag {
			fmt.Printf("No auto-login token exists for user %s.\n", username)
			return 0
		}
		os.MkdirAll(filepath.Dir(tokenFile), 0o755)
		const pool = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
		b := make([]byte, 30)
		for i := range b {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(pool))))
			b[i] = pool[n.Int64()]
		}
		token = string(b)
		os.WriteFile(tokenFile, []byte(token+"\n"), 0o644)
	}

	loginURL := loginOpenPanelURL() + "login_autologin?admin_token=" + loginURLEncode(token) + "&username=" + loginURLEncode(username)
	fmt.Println(loginURL)
	if openFlag {
		if sh.Has("xdg-open") {
			sh.C("xdg-open", loginURL).Run()
		} else {
			fmt.Println("xdg-open not found, cannot open URL automatically.")
		}
	}
	return 0
}
