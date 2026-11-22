// ##############################################################################
// Script Name: internal/cli/cli.go
// Description: Command registry, exit handling and shared output helpers.
// Usage: imported by every command package
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

package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Command is one opencli subcommand, Name is what users type, e.g. "domains-add"
type Command struct {
	Name  string
	Desc  string
	Usage string
	Docs  string
	// Hidden commands still run but stay out of --help and aliases.txt, like the old excluded scripts
	Hidden bool
	Run    func(args []string) int
}

var registry = map[string]*Command{}

// Register adds a command, called from each command package's init
func Register(c *Command) {
	if _, dup := registry[c.Name]; dup {
		panic("opencli: duplicate command " + c.Name)
	}
	registry[c.Name] = c
}

// Lookup finds a command by name
func Lookup(name string) *Command { return registry[name] }

// All returns every command sorted by name
func All() []*Command {
	out := make([]*Command, 0, len(registry))
	for _, c := range registry {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// exitCode is what Exit panics with so the dispatcher can still run post hooks
type exitCode int

// Exit stops the current command with code, same as `exit N` in the old scripts
func Exit(code int) { panic(exitCode(code)) }

// Fail prints to stdout and exits 1, the common `echo "..."; exit 1` pattern
func Fail(format string, a ...any) {
	fmt.Printf(format+"\n", a...)
	Exit(1)
}

// Failerr prints to stderr and exits 1, the `echo "..." >&2; exit 1` pattern
func Failerr(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	Exit(1)
}

// Errf prints a line to stderr
func Errf(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }

// Invoke runs a command and turns Exit panics into a return code
func Invoke(c *Command, args []string) (code int) {
	defer func() {
		if r := recover(); r != nil {
			if ec, ok := r.(exitCode); ok {
				code = int(ec)
				return
			}
			panic(r)
		}
	}()
	return c.Run(args)
}

// HasFlag reports whether args contains flag exactly
func HasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// Arg returns args[i] or "" like an unset $N
func Arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// Value returns the part after prefix for args like --key=value, ok is false when missing
func Value(args []string, prefix string) (string, bool) {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return strings.TrimPrefix(a, prefix), true
		}
	}
	return "", false
}
