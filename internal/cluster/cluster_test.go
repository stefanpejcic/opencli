// ##############################################################################
// Script Name: internal/cluster/cluster_test.go
// Description: Tests for cluster.
// Usage: go test ./...
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

package cluster

import "testing"

func TestMergeJSON(t *testing.T) {
	got := mergeJSON([][2]string{{"a", `[{"user":"x"}]`}, {"b", `[{"user":"y"}]`}})
	want := "[\n  {\n    \"server\": \"a\",\n    \"user\": \"x\"\n  },\n  {\n    \"server\": \"b\",\n    \"user\": \"y\"\n  }\n]"
	if got != want {
		t.Errorf("arrays: %s", got)
	}
	if got := mergeJSON([][2]string{{"a", "3"}, {"b", "4"}}); got != "7" {
		t.Errorf("numbers: %s", got)
	}
	if got := mergeJSON([][2]string{{"a", `{"data":[{"d":1}],"ok":true}`}, {"b", `{"data":[]}`}}); got != "{\n  \"data\": [\n    {\n      \"server\": \"a\",\n      \"d\": 1\n    }\n  ],\n  \"ok\": true\n}" {
		t.Errorf("data list: %s", got)
	}
}

func TestSubject(t *testing.T) {
	if s := Subject("domains-dns", []string{"show", "example.com"}); s != "example.com" {
		t.Error(s)
	}
	if s := Subject("user-quota", []string{"--update", "stefan"}); s != "stefan" {
		t.Error(s)
	}
	if s := Subject("user-suspend", []string{"--json", "stefan"}); s != "stefan" {
		t.Error(s)
	}
}
