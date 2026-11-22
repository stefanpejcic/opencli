// ##############################################################################
// Script Name: internal/commands/plan/util.go
// Description: Shared validation helpers for the plan commands.
// Usage: (internal helper)
// Author: Stefan Pejcic
// Created: 06.11.2023
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

package plan

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	intRe     = regexp.MustCompile(`^-?[0-9]+$`)
	numRe     = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
	quotaRe   = regexp.MustCompile(`^([0-9]+([.][0-9]+)?)([BkMGT]?)$`)
	nameRe    = regexp.MustCompile(`^[a-zA-Z0-9_. -]+$`)
	featureRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

// bash [[ =~ ]] with $ also matches before a trailing newline, go's $ doesn't
func chomp(s string) string { return strings.TrimSuffix(s, "\n") }

func isInteger(s string) bool { return intRe.MatchString(chomp(s)) }
func isNumber(s string) bool  { return numRe.MatchString(chomp(s)) }

// quotaNorm returns the quota with G added to a bare non-zero number, ok false when it doesn't parse
func quotaNorm(v string) (string, bool) {
	m := quotaRe.FindStringSubmatch(chomp(v))
	if m == nil {
		return v, false
	}
	if m[3] == "" && m[1] != "0" {
		return m[1] + "G", true
	}
	return v, true
}

// checkIntegers prints the error and returns false on the first non-integer
func checkIntegers(pairs [][2]string) bool {
	for _, p := range pairs {
		if !isInteger(p[1]) {
			fmt.Printf("Error: %s must be a number (integer only)\n", p[0])
			return false
		}
	}
	return true
}

func checkCPURAM(cpu, ram string) bool {
	for _, p := range [][2]string{{"cpu", cpu}, {"ram", ram}} {
		if !isNumber(p[1]) {
			fmt.Printf("Error: %s must be a number (integer or float)\n", p[0])
			return false
		}
	}
	return true
}

func checkNameFeature(name, feature string) bool {
	if !nameRe.MatchString(chomp(name)) {
		fmt.Println("Error: name may only contain letters, numbers, spaces, '.', '_' and '-'")
		return false
	}
	if !featureRe.MatchString(chomp(feature)) {
		fmt.Println("Error: feature_set may only contain letters, numbers, '_' and '-'")
		return false
	}
	return true
}
