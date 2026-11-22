// ##############################################################################
// Script Name: internal/commands/general/dev.go
// Description: Overwrite OpenPanel container files and restart service to apply.
// Usage: opencli dev [path]
// Author: Stefan Pejcic
// Created: 07.03.2025
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

package general

import (
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:   "dev",
		Desc:   "Overwrite OpenPanel container files and restart service to apply.",
		Usage:  "opencli dev [path]",
		Docs:   "https://docs.openpanel.com",
		Hidden: true,
		Run:    dev,
	})
}

const (
	devContainer = "openpanel"
	devLastPath  = "/tmp/last_dev_path"
	devRed       = "\033[0;31m"
	devGreen     = "\033[0;32m"
	devYellow    = "\033[1;33m"
	devNC        = "\033[0m"
)

func devInfo(m string)  { fmt.Println(devGreen + "[INFO]" + devNC + " " + m) }
func devWarn(m string)  { fmt.Println(devYellow + "[WARN]" + devNC + " " + m) }
func devError(m string) { fmt.Fprintln(os.Stderr, devRed+"[ERROR]"+devNC+" "+m) }

func devUsage() {
	fmt.Print(`Usage: opencli dev [path]

Description:
    This script is intended for development purposes only to overwrite system files 
    in the OpenPanel UI container. It will open the file in the nano editor, validate 
    its contents, and then copy the changes back to the container. Afterwards, it 
    restarts the OpenPanel container to pick up the new file and follow the logs.

Options:
    path    The path to a file inside the OpenPanel Docker container (either .py or .html).
            If no path is provided, the script will display available files for you to choose.

Examples:
    1. Edit a Python module by providing its path:
       opencli dev modules/ssh.py
    
    2. Edit an HTML template by providing its path:
       opencli dev templates/base.html
    
    3. Interactively select a file to edit:
       opencli dev

`)
	cli.Exit(1)
}

func devInstallTool(tool string) {
	if sh.Has(tool) {
		return
	}
	devInfo("Attempting to install " + tool + "...")
	var ok bool
	switch {
	case sh.Has("apt"):
		ok = sh.C("apt", "install", "-y", tool).Quiet().OK()
	case sh.Has("dnf"):
		sh.C("dnf", "install", "-y", "epel-release").Quiet().Run()
		ok = sh.C("dnf", "install", "-y", tool).Quiet().OK()
	case sh.Has("yum"):
		ok = sh.C("yum", "install", "-y", tool).Quiet().OK()
	default:
		devError("No supported package manager found (apt/dnf/yum)")
	}
	if !ok {
		devError("Failed to install " + tool + ". Please install it manually.")
		cli.Exit(1)
	}
	devInfo(tool + " installed successfully")
}

func devPickFile() string {
	names, _ := sh.C("podman", "ps", "--format", "table {{.Names}}").Out()
	running := false
	for _, n := range strings.Split(names, "\n") {
		if n == devContainer {
			running = true
		}
	}
	if !running {
		devError("OpenPanel container is not running!")
		cli.Exit(1)
	}
	found, _ := sh.C("podman", "exec", devContainer, "find", "/", "-maxdepth", "6", "(", "-name", "app.py", "-o", "-path", "/modules/*.py", "-o", "-path", "/templates/*.html", ")").NoErr().Out()
	var files []string
	seen := map[string]bool{}
	for _, f := range strings.Split(found, "\n") {
		if f != "" && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		devError("No files found in the container!")
		cli.Exit(1)
	}
	last := ""
	if b, err := os.ReadFile(devLastPath); err == nil {
		last = strings.TrimRight(string(b), "\n")
	}
	path, err := sh.C("fzf", "--prompt=Select a file: ", "--query="+last).Stdin(strings.Join(files, "\n") + "\n").Out()
	if err != nil {
		// set -e ended the script right at the failed fzf
		cli.Exit(sh.Code(err))
	}
	if path == "" {
		devWarn("No file selected.")
		cli.Exit(1)
	}
	os.WriteFile(devLastPath, []byte(path+"\n"), 0o644)
	devInfo("Selected file: " + path)
	return path
}

var devBadState = regexp.MustCompile(`(?i)paused or unknown state|state improper|conmon exited prematurely|conmon process killed`)

// added this recovery path after hitting: "unable to restart a container in a paused or unknown state: container state improper"
func devRestartAndFollow() int {
	devInfo("Restarting OpenPanel container to pick up the new file...")
	if out, err := sh.C("podman", "restart", devContainer).Combined(); err != nil {
		devWarn("podman restart failed: " + out)
		if !devBadState.MatchString(out) {
			devError("Failed to restart container: " + out)
			return 1
		}
		devWarn("Container is in a bad state (known podman issue) - recovering via stop/start")
		// try a clean stop first
		if !sh.C("podman", "stop", "-t", "5", devContainer).Quiet().OK() {
			devWarn("Graceful stop failed, forcing kill")
			sh.C("podman", "kill", devContainer).Quiet().Run()
		}
		if out, err := sh.C("podman", "start", devContainer).Combined(); err != nil {
			devError("Failed to start container even after cleanup: " + out)
			devError("Manual recovery needed, e.g.:")
			devError("    podman inspect " + devContainer + " | grep -i state -A5")
			devError("    cd /root && podman-compose down && podman-compose up -d openpanel")
			return 1
		}
		devInfo("Container recovered and started successfully")
	}
	devInfo("Following new logs:")
	return sh.C("podman", "logs", "--follow", "--since=0s", devContainer).Code()
}

func devProcess(path string) int {
	ext := path
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		ext = path[i+1:]
	}
	if ext != "py" && ext != "html" {
		devError("Unsupported file extension: " + ext)
		devError("Supported extensions: py html")
		return 1
	}
	tmp, err := os.CreateTemp("", "tmp.")
	if err != nil {
		return 1
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	// Ctrl-C while following logs should still remove the temp file, like the EXIT trap did
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)

	if err := sh.C("nano", tmp.Name()).StdinReader(os.Stdin).Run(); err != nil {
		return sh.Code(err)
	}
	b, _ := os.ReadFile(tmp.Name())
	if len(b) == 0 {
		devError("Aborting: The file is empty!")
		return 1
	}
	valid := true
	if ext == "py" && !strings.Contains(string(b), "import") {
		devError("Invalid Python file: missing import statements")
		valid = false
	}
	if ext == "html" && !strings.Contains(strings.ToLower(string(b)), "<div") {
		devError("Invalid HTML file: missing HTML tags")
		valid = false
	}
	if !valid {
		devError("File validation failed")
		return 1
	}
	if !sh.C("podman", "cp", tmp.Name(), devContainer+":/"+path).OK() {
		devError("Failed to copy file to container")
		return 1
	}
	devInfo("File copied to container successfully")
	if err := sh.Opencli("config", "update", "dev_mode", "on").Quiet().Run(); err != nil {
		return sh.Code(err)
	}
	return devRestartAndFollow()
}

func dev(args []string) int {
	var path string
	if len(args) > 0 {
		path = args[0]
		if path == "" {
			devError("Invalid path provided!")
			devUsage()
		}
	} else {
		devInstallTool("fzf")
		devInstallTool("nano")
		path = devPickFile()
	}
	return devProcess(path)
}
