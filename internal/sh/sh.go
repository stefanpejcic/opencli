// ##############################################################################
// Script Name: internal/sh/sh.go
// Description: Helpers for running external programs the way the bash scripts did.
// Usage: imported by command packages
// Author: Stefan Pejcic
// Created: 20.11.2026
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

package sh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Trace prints every external command to stderr, set by `opencli -x`
var Trace bool

// Cmd is a command builder, e.g. sh.C("podman", "ps").Quiet().Run()
type Cmd struct {
	name    string
	args    []string
	env     []string
	dir     string
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	timeout time.Duration
	detach  bool
}

// C starts building a command
func C(name string, args ...string) *Cmd {
	return &Cmd{name: name, args: args, stdout: os.Stdout, stderr: os.Stderr}
}

// LowPrio is C behind nice and the idle io class, for heavy work nobody is waiting on
func LowPrio(name string, args ...string) *Cmd {
	p := LowPrioArgs(name, args...)
	return C(p[0], p[1:]...)
}

// LowPrioArgs puts nice -n19 ionice -c3 in front of the command, mq-deadline ignores -c2 levels so only the idle class does anything
func LowPrioArgs(name string, args ...string) []string {
	var p []string
	if Has("nice") {
		p = append(p, "nice", "-n19")
	}
	if Has("ionice") {
		p = append(p, "ionice", "-c3")
	}
	return append(append(p, name), args...)
}

// Env adds KEY=VALUE pairs on top of the current environment
func (c *Cmd) Env(kv ...string) *Cmd { c.env = append(c.env, kv...); return c }

// Dir sets the working directory
func (c *Cmd) Dir(d string) *Cmd { c.dir = d; return c }

// Stdin feeds a string to the command
func (c *Cmd) Stdin(s string) *Cmd { c.stdin = strings.NewReader(s); return c }

// StdinReader feeds a reader to the command
func (c *Cmd) StdinReader(r io.Reader) *Cmd { c.stdin = r; return c }

// Timeout kills the command after d, like `timeout N cmd`
func (c *Cmd) Timeout(d time.Duration) *Cmd { c.timeout = d; return c }

// Quiet drops stdout and stderr, like `&>/dev/null`
func (c *Cmd) Quiet() *Cmd { c.stdout, c.stderr = nil, nil; return c }

// NoErr drops stderr only, like `2>/dev/null`
func (c *Cmd) NoErr() *Cmd { c.stderr = nil; return c }

// NoOut drops stdout only, like `>/dev/null`
func (c *Cmd) NoOut() *Cmd { c.stdout = nil; return c }

// ErrToOut sends stderr to stdout, like `2>&1`
func (c *Cmd) ErrToOut() *Cmd { c.stderr = c.stdout; return c }

// Stdout redirects stdout to w
func (c *Cmd) Stdout(w io.Writer) *Cmd { c.stdout = w; return c }

// Stderr redirects stderr to w
func (c *Cmd) Stderr(w io.Writer) *Cmd { c.stderr = w; return c }

func (c *Cmd) build(ctx context.Context) *exec.Cmd {
	var cmd *exec.Cmd
	if ctx != nil {
		cmd = exec.CommandContext(ctx, c.name, c.args...)
		// timeout sends TERM first, same as coreutils timeout
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = 5 * time.Second
	} else {
		cmd = exec.Command(c.name, c.args...)
	}
	if len(c.env) > 0 {
		cmd.Env = append(os.Environ(), c.env...)
	}
	cmd.Dir = c.dir
	cmd.Stdin = c.stdin
	cmd.Stdout = c.stdout
	cmd.Stderr = c.stderr
	if c.detach {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if Trace {
		fmt.Fprintln(os.Stderr, "+ "+c.String())
	}
	return cmd
}

// String shows the command roughly like bash xtrace does
func (c *Cmd) String() string {
	parts := append([]string{c.name}, c.args...)
	for i, p := range parts {
		if p == "" || strings.ContainsAny(p, " \t\n'\"$`\\|&;<>()*?[]{}") {
			parts[i] = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
		}
	}
	return strings.Join(parts, " ")
}

func (c *Cmd) run() error {
	if c.timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		defer cancel()
		err := c.build(ctx).Run()
		if ctx.Err() == context.DeadlineExceeded {
			return ErrTimeout
		}
		return err
	}
	return c.build(nil).Run()
}

// ErrTimeout is returned when Timeout kicked in, exit code 124 like coreutils timeout
var ErrTimeout = errors.New("timed out")

// Run runs the command and returns its error
func (c *Cmd) Run() error { return c.run() }

// OK runs the command and reports exit status 0
func (c *Cmd) OK() bool { return c.run() == nil }

// Code runs the command and returns its exit code, 127 when it can't start, 124 on timeout
func (c *Cmd) Code() int { return Code(c.run()) }

// Out runs the command and returns stdout without trailing newlines, like $(cmd)
func (c *Cmd) Out() (string, error) {
	var b bytes.Buffer
	c.stdout = &b
	if c.stderr == os.Stdout {
		c.stderr = &b
	}
	err := c.run()
	return strings.TrimRight(b.String(), "\n"), err
}

// Raw returns stdout exactly as printed, trailing newlines kept
func (c *Cmd) Raw() (string, error) {
	var b bytes.Buffer
	c.stdout = &b
	err := c.run()
	return b.String(), err
}

// Str is Out without the error, for `x=$(cmd 2>/dev/null)` style calls
func (c *Cmd) Str() string { s, _ := c.Out(); return s }

// Combined returns stdout and stderr together, like $(cmd 2>&1)
func (c *Cmd) Combined() (string, error) {
	var b bytes.Buffer
	c.stdout, c.stderr = &b, &b
	err := c.run()
	return strings.TrimRight(b.String(), "\n"), err
}

// Background starts the command detached and doesn't wait, like `nohup cmd &>/dev/null &`
func (c *Cmd) Background() error {
	c.detach = true
	if c.stdin == nil {
		c.stdin = nil
	}
	cmd := c.build(nil)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return cmd.Process.Release()
}

// Code turns an exec error into a shell style exit code
func Code(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, ErrTimeout) {
		return 124
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 127
}

// Run runs a command with output going to the terminal
func Run(name string, args ...string) error { return C(name, args...).Run() }

// OK runs a command quietly and reports success, like `cmd &>/dev/null`
func OK(name string, args ...string) bool { return C(name, args...).Quiet().OK() }

// Out runs a command and captures stdout, stderr still goes to the terminal
func Out(name string, args ...string) (string, error) { return C(name, args...).Out() }

// Str captures stdout and drops stderr and the error, like $(cmd 2>/dev/null)
func Str(name string, args ...string) string { return C(name, args...).NoErr().Str() }

// Quiet runs a command and drops all output, the error is returned
func Quiet(name string, args ...string) error { return C(name, args...).Quiet().Run() }

// Has reports whether a program is on PATH, like `command -v`
func Has(name string) bool { _, err := exec.LookPath(name); return err == nil }

// Bash runs a snippet with bash -c, only for things that really are shell (user hooks, remote snippets)
func Bash(script string, args ...string) *Cmd {
	return C("bash", append([]string{"-c", script, "bash"}, args...)...)
}

// Self is the path of the running opencli binary
func Self() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "/usr/local/bin/opencli"
}

// Opencli runs another opencli command through the dispatcher, same as `opencli X` in the scripts
func Opencli(args ...string) *Cmd { return C(Self(), args...) }
