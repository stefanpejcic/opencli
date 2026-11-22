// ##############################################################################
// Script Name: cmd/opencli/main.go
// Description: Makes all OpenCLI commands available on the terminal.
// Usage: opencli <COMMAND-NAME> <ATTRIBUTES>
// Author: Stefan Pejcic
// Created: 15.11.2023
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

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/cluster"
	"opencli/internal/sh"
)

// version is set at build time with -ldflags "-X main.version=..."
var version = "202.607.009"

const (
	logFile    = "/var/log/openpanel/admin/opencli.log"
	scriptsDir = "/usr/local/opencli"
	hooksDir   = "/etc/openpanel/openpanel/hooks"
	aliasFile  = scriptsDir + "/aliases.txt"
)

var debug bool

func showAbout() {
	fmt.Printf(`About:
  Documentation:                    https://openpanel.com/docs/category/opencli/
  Version:                          %s
  Author:                           Stefan Pejcic <stefan@pejcic.rs>
  License:                          MIT

`, version)
}

func showHistory() {
	b, err := os.ReadFile(logFile)
	if err != nil {
		fmt.Printf("Warning: log file %s not found.\n", logFile)
		return
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")

	fmt.Println("Recently used commands:")
	recent := lines
	if len(recent) > 100 {
		recent = recent[len(recent)-100:]
	}
	re := regexp.MustCompile(`opencli [^ ]*`)
	seen := map[string]bool{}
	var uniq []string
	for _, l := range recent {
		for _, m := range re.FindAllString(l, -1) {
			if !seen[m] {
				seen[m] = true
				uniq = append(uniq, m)
			}
		}
	}
	sort.Strings(uniq)
	if len(uniq) > 5 {
		uniq = uniq[len(uniq)-5:]
	}
	for _, u := range uniq {
		fmt.Println("  " + u)
	}
	fmt.Println()

	fmt.Println("Most commonly used commands:")
	counts := map[string]int{}
	for _, l := range lines {
		f := strings.Split(l, " | ")
		if len(f) >= 2 {
			counts[f[1]]++
		}
	}
	type kv struct {
		k string
		n int
	}
	var all []kv
	for k, n := range counts {
		all = append(all, kv{k, n})
	}
	// sort -rn falls back to a reversed whole line compare on ties
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].k > all[j].k
	})
	for i, e := range all {
		if i == 5 {
			break
		}
		fmt.Println("   " + strings.Join(strings.Fields(e.k), " "))
	}
	fmt.Println()
}

func showUsage() {
	fmt.Print(`
Usage: opencli <COMMAND> [additional_arguments]

Suggested commands:
  opencli faq                       Display frequently asked questions and answers.
  opencli --help                    List all available OpenCLI commands and their usage.

`)
}

func logCommand(cmd string) {
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	fmt.Fprintf(f, "%s | opencli %s\n", time.Now().Format("2006-01-02 15:04:05"), cmd)
	f.Close()
}

func runHook(kind, command string, args []string) {
	base := hooksDir + "/" + kind + "_" + command
	hook := ""
	if st, err := os.Stat(base + ".sh"); err == nil && st.Mode().IsRegular() {
		hook = base + ".sh"
	} else if st, err := os.Stat(base); err == nil && st.Mode().IsRegular() {
		// legacy pre-1.3 name without .sh
		hook = base
	} else {
		return
	}
	switch kind {
	case "pre":
		a := []string{}
		if debug {
			a = append(a, "-x")
		}
		sh.C("bash", append(append(a, hook), args...)...).Quiet().Run()
	case "post":
		sh.C("nohup", append([]string{"bash", hook}, args...)...).Quiet().Background()
	}
}

// key maps a command name to the old script path, "domains-add" -> "domains/add", "--version" -> "version"
func key(command string) string {
	return strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(command, "-", "/")), "/")
}

var byKey map[string]*cli.Command

func lookup(command string) *cli.Command {
	if byKey == nil {
		byKey = map[string]*cli.Command{}
		for _, c := range cli.All() {
			byKey[key(c.Name)] = c
		}
	}
	return byKey[key(command)]
}

func handleSpecial(command string, args []string) {
	switch command {
	case "error":
		cli.Exit(errorCommand(args))
	case "locale":
		cli.Exit(localeCommand(args))
	}
}

func localeCommand(args []string) int {
	// runs as root, so it's pinned to a reviewed commit and checksum, bump both when updating the installer
	const sha = "474f283e13e47c0407479814134f073165042b03"
	const sum = "af48f9632fcff59f43486f2afc79804dceb0729b7be78c03e7a338f9e55791ca"
	tmp, err := os.CreateTemp("", "tmp.")
	if err != nil {
		return 1
	}
	defer os.Remove(tmp.Name())
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get("https://raw.githubusercontent.com/stefanpejcic/openpanel-translations/" + sha + "/install.sh")
	if err == nil && resp.StatusCode != 200 {
		err = fmt.Errorf("HTTP %d", resp.StatusCode)
		resp.Body.Close()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: failed to download the locale installer.")
		return 1
	}
	h := sha256.New()
	io.Copy(io.MultiWriter(tmp, h), resp.Body)
	resp.Body.Close()
	tmp.Close()
	if hex.EncodeToString(h.Sum(nil)) != sum {
		fmt.Fprintln(os.Stderr, "Error: locale installer checksum mismatch - refusing to run.")
		return 1
	}
	a := []string{}
	if debug {
		a = append(a, "-x")
	}
	return sh.C("bash", append(append(a, tmp.Name()), args...)...).StdinReader(os.Stdin).Code()
}

func executeCommand(command string, args []string) int {
	c := lookup(command)
	if c == nil {
		fmt.Fprintf(os.Stderr, "Error: Command '%s' not found.\n", command)
		showUsage()
		showHistory()
		showAbout()
		return 1
	}

	// called by cluster.List for this server's part, the old dispatcher ran the script directly there
	if os.Getenv("OPENCLI_RAW") == "1" {
		os.Unsetenv("OPENCLI_RAW")
		return cli.Invoke(c, args)
	}

	// --local, --server= and --all-servers are for the dispatcher, commands never see them
	var rest []string
	target, allServers, localOnly := "", false, false
	for _, a := range args {
		switch {
		case a == "--local":
			localOnly = true
		case a == "--all-servers":
			allServers = true
		case strings.HasPrefix(a, "--server="):
			target = strings.TrimPrefix(a, "--server=")
		default:
			rest = append(rest, a)
		}
	}
	args = rest

	slaves := ""
	if !localOnly && (target != "" || allServers || cluster.ListCmds[command] || cluster.UserCmds[command] || cluster.DomainCmds[command]) {
		slaves = cluster.Slaves()
	}

	if target != "" && !cluster.IsSelf(target) {
		if slaves == "" {
			fmt.Fprintln(os.Stderr, "Error: --server works only on a master with other servers.")
			return 1
		}
		if !cluster.ServerCmds[command] {
			fmt.Fprintf(os.Stderr, "Error: opencli %s can't be run on another server.\n", command)
			return 1
		}
		logCommand(command + " --server=" + target)
		return cluster.RunRemote(target, append([]string{command}, args...)...)
	}

	if allServers && !cluster.AllCmds[command] {
		fmt.Fprintf(os.Stderr, "Error: opencli %s doesn't support --all-servers.\n", command)
		return 1
	}

	if slaves != "" && target == "" {
		kind := ""
		if cluster.UserCmds[command] {
			kind = "user"
		}
		if cluster.DomainCmds[command] {
			kind = "domain"
		}
		// ftp-list for one account goes to its server, without one it lists all
		if command == "ftp-list" && cluster.Subject(command, args) != "" {
			kind = "user"
		}
		if kind == "" && cluster.ListCmds[command] && (command != "email-manage" || strings.Join(args, " ") == "setup email list") {
			logCommand(command)
			return cluster.List(command, args)
		}
		// domains-update_ns --all is a whole-server run, not one domain
		if command == "domains-update_ns" && cli.HasFlag(args, "--all") {
			kind = ""
		}
		owner := ""
		if kind != "" {
			owner = cluster.Owner(kind, cluster.Subject(command, args))
		}
		if owner != "" {
			fmt.Fprintf(os.Stderr, "Running on %s, where %s is.\n", owner, cluster.Subject(command, args))
			logCommand(command + " --server=" + owner)
			return remoteNoHeader(owner, command, args)
		}
	}

	// plan ids differ per server, remember the name before this server's edit can change it
	planName := ""
	if allServers && slaves != "" && command == "plan-edit" {
		for _, a := range args {
			if strings.HasPrefix(a, "id=") {
				planName = cluster.PlanNameByID(strings.TrimPrefix(a, "id="))
			}
		}
	}

	if allServers && slaves != "" {
		cluster.Header(cluster.Self())
	}
	logCommand(command)
	runHook("pre", command, args)
	status := cli.Invoke(c, args)
	runHook("post", command, args)

	if allServers && slaves != "" {
		rc := 0
		if command == "plan-edit" {
			rc = cluster.PlanEditAll(planName, args)
		} else {
			rc = cluster.RunRemote("all", append([]string{command}, args...)...)
		}
		if rc != 0 {
			status = rc
		}
	}
	// the command's exit code is passed through, the post hook used to swallow it
	return status
}

// remoteNoHeader runs on the owner and drops the first "── server ──" line, the user already got told where
func remoteNoHeader(owner, command string, args []string) int {
	r, w, err := os.Pipe()
	if err != nil {
		return 1
	}
	stdout := os.Stdout
	os.Stdout = w
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		first := true
		for sc.Scan() {
			l := sc.Text()
			if first && strings.HasPrefix(l, "── ") {
				first = false
				continue
			}
			first = false
			fmt.Fprintln(stdout, l)
		}
		close(done)
	}()
	rc := cluster.RunRemote(owner, append([]string{command}, args...)...)
	w.Close()
	<-done
	os.Stdout = stdout
	return rc
}

var specialCommands = [][3]string{
	{"opencli error", "Displays information for specific error ID received in OpenPanel UI.", "opencli error <ID_HERE>"},
	{"opencli locale", "Install locales (Languages) for OpenPanel UI.", "opencli locale <CODE>"},
}

func generateCommands(query string) int {
	const green, reset = "\033[0;32m", "\033[0m"
	if st, err := os.Stat(scriptsDir); err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: Scripts directory '%s' not found.\n", scriptsDir)
		return 1
	}
	f, err := os.OpenFile(aliasFile, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Cannot write to alias file '%s'.\n", aliasFile)
		return 1
	}
	f.Close()
	if query == "" {
		os.Truncate(aliasFile, 0)
	}

	var list []string
	for _, c := range cli.All() {
		if c.Hidden {
			continue
		}
		name := "opencli " + c.Name
		list = append(list, name)
		if query != "" && !strings.Contains(name, query) {
			continue
		}
		fmt.Println(green + name + reset)
		if c.Desc != "" {
			fmt.Println("Description: " + c.Desc)
		}
		if c.Usage != "" {
			fmt.Println("Usage: " + c.Usage)
		}
		if c.Docs != "" {
			fmt.Println("Docs: " + c.Docs)
		}
		fmt.Println("------------------------")
	}
	for _, s := range specialCommands {
		if query == "" || strings.Contains(s[0], query) {
			fmt.Println(green + s[0] + reset)
			fmt.Println("Description: " + s[1])
			fmt.Println("Usage: " + s[2])
			fmt.Println("------------------------")
		}
		list = append(list, s[0])
	}
	if query == "" && len(list) > 0 {
		sort.Strings(list)
		os.WriteFile(aliasFile, []byte(strings.Join(list, "\n")+"\n"), 0o644)
	}
	return 0
}

func run(args []string) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "OpenCLI scripts must be run as root user.")
		return 1
	}
	if len(args) < 1 {
		showUsage()
		showHistory()
		showAbout()
		return 1
	}
	if args[0] == "-x" {
		debug, sh.Trace = true, true
		args = args[1:]
		if len(args) == 0 {
			return executeCommand("", nil)
		}
	}
	switch args[0] {
	case "help", "-h", "--help":
		return generateCommands("")
	}
	// only when it's the sole arg, server-migrate uses -h for the host
	if len(args) == 2 {
		switch args[1] {
		case "help", "-h", "--help":
			return generateCommands(args[0])
		}
	}
	command := args[0]
	if command == "-v" {
		command = "--version"
	}
	return invoke(func() int {
		handleSpecial(command, args[1:])
		return executeCommand(command, args[1:])
	})
}

// invoke turns cli.Exit panics from outside a command into a return code
func invoke(fn func() int) int {
	c := &cli.Command{Run: func([]string) int { return fn() }}
	return cli.Invoke(c, nil)
}

func main() {
	os.Exit(run(os.Args[1:]))
}
