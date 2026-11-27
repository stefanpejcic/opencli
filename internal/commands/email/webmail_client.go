// ##############################################################################
// Script Name: internal/commands/email/webmail_client.go
// Description: Switch the webmail client users get between Roundcube and Golub.
// Usage: opencli email-webmail client [roundcube|golub]
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

package email

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/sh"
	wm "opencli/internal/webmail"
)

var (
	golubEnvFile = mailDir + "/golub.env"
	golubSSOKey  = "/etc/openpanel/openpanel/golub_sso.key"
	dovecotKey   = "/etc/openpanel/openpanel/dovecot_master.key"
)

// the panel logs into dovecot as <email>*openpanel for both clients
const dovecotMasterUser = "openpanel"

func wmClient(args []string) int {
	if len(args) == 0 {
		fmt.Println(wm.Service())
		return 0
	}
	to := args[0]
	if to != "roundcube" && to != "golub" {
		fmt.Println("Error: webmail client must be roundcube or golub.")
		return 1
	}
	if !conf.IsFile(srvCompose) {
		fmt.Println("Error: Mail server is not installed.")
		return 1
	}
	if to == "golub" {
		if !strings.Contains(conf.Read(srvCompose), "\n  golub:") {
			fmt.Printf("Error: %s has no golub service, run: opencli update\n", srvCompose)
			return 1
		}
		if err := golubWriteEnv(); err != nil {
			fmt.Printf("Error: %v\n", err)
			return 1
		}
	}
	from := "golub"
	if to == "golub" {
		from = "roundcube"
	}

	setWebmailClient(to)
	// both listen on 8080, so the old one has to be gone first; plain podman, compose down and --force-recreate would take the mailserver with them
	sh.C("podman", "stop", "-t", "10", "openadmin_"+from).Quiet().Run()
	podman.ForceRemove("openadmin_" + from)
	podman.ForceRemove("openadmin_" + to)
	up := []string{"up", "-d", "--no-deps", to}
	if !podman.IsRunning(container) {
		// a stopped mailserver has to come up with it
		up = []string{"up", "-d", to}
	}
	if err := sh.C("podman-compose", up...).Dir(mailDir).Timeout(180 * time.Second).Quiet().Run(); err != nil || !waitRunning("openadmin_"+to, 30*time.Second) {
		fmt.Printf("Error: %s did not start, check: podman logs openadmin_%s\n", to, to)
		return 1
	}
	srvService("remove", wm.Name(from), "docker", "openadmin_"+from)
	srvService("add", wm.Name(to), "docker", "openadmin_"+to)
	fmt.Printf("Webmail client is now %s.\n", to)
	return 0
}

func waitRunning(name string, max time.Duration) bool {
	for end := time.Now().Add(max); time.Now().Before(end); time.Sleep(time.Second) {
		if podman.IsRunning(name) {
			return true
		}
	}
	return false
}

func setWebmailClient(name string) {
	if srvHasLine(srvEnv, "WEBMAIL_CLIENT=") {
		srvSedLine(srvEnv, "WEBMAIL_CLIENT", name, false)
		return
	}
	srvAppend(srvEnv, "WEBMAIL_CLIENT="+name)
}

// golubWriteEnv keeps the session key so users stay logged in, and refreshes the rest on every switch
func golubWriteEnv() error {
	sso, err := keyFile(golubSSOKey)
	if err != nil {
		return fmt.Errorf("SSO key %s: %w", golubSSOKey, err)
	}
	master, err := keyFile(dovecotKey)
	if err != nil {
		return fmt.Errorf("dovecot master key %s: %w", dovecotKey, err)
	}
	if !ensureDovecotMaster(master) {
		fmt.Println("Warning: could not set the dovecot master user, logging in from OpenPanel will not work until the mailserver is running.")
	}
	secret, _ := conf.Raw(golubEnvFile, "GOLUB_SECRET_KEY")
	if strings.TrimSpace(secret) == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		secret = base64.StdEncoding.EncodeToString(b)
	}
	content := "GOLUB_SECRET_KEY=" + strings.TrimSpace(secret) + "\n" +
		"GOLUB_SSO_SECRET=" + sso + "\n" +
		"GOLUB_MASTER_USER=" + dovecotMasterUser + "\n" +
		"GOLUB_MASTER_PASSWORD=" + master + "\n"
	tmp := golubEnvFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, golubEnvFile)
}

// keyFile reads a random hex key, making it on first use; O_EXCL so a racing panel can't write a different one
func keyFile(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b)), nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	key := hex.EncodeToString(buf)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if errors.Is(err, os.ErrExist) {
		b, rerr := os.ReadFile(path)
		return strings.TrimSpace(string(b)), rerr
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = f.WriteString(key)
	return key, err
}

// ensureDovecotMaster is what the panel does on startup, needed here when the key was just made
func ensureDovecotMaster(pass string) bool {
	if !podman.IsRunning(container) {
		return false
	}
	out, err := sh.C("podman", "exec", container, "setup", "dovecot-master", "list").Out()
	exists := err == nil && strings.Contains(out, dovecotMasterUser)
	check := `h=$(grep "^` + dovecotMasterUser + `|" /tmp/docker-mailserver/dovecot-masters.cf | cut -d"|" -f2-); [ -n "$h" ] && doveadm pw -t "$h" -p "$1" >/dev/null 2>&1`
	if exists && sh.C("podman", "exec", container, "sh", "-c", check, "sh", pass).Quiet().OK() {
		return true
	}
	action := "add"
	if exists {
		action = "update"
	}
	return sh.C("podman", "exec", container, "setup", "dovecot-master", action, dovecotMasterUser, pass).Quiet().OK()
}
