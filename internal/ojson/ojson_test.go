// ##############################################################################
// Script Name: internal/ojson/ojson_test.go
// Description: Tests for ojson.
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

package ojson

import "testing"

func TestKeyOrderKept(t *testing.T) {
	in := `{"z":1,"a":{"y":"<b>","b":null},"m":[1,"x",true]}`
	v, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := Compact(v); got != in {
		t.Errorf("Compact = %s, want %s", got, in)
	}
	o := v.(*Object)
	o.Set("new", "v").Set("z", 2)
	if got := Compact(o); got != `{"z":2,"a":{"y":"<b>","b":null},"m":[1,"x",true],"new":"v"}` {
		t.Errorf("got %s", got)
	}
}

func TestPrettyLikeJq(t *testing.T) {
	o := New().Set("id", "1").Set("n", nil)
	want := "[\n  {\n    \"id\": \"1\",\n    \"n\": null\n  }\n]"
	if got := Pretty([]any{o}); got != want {
		t.Errorf("got %q", got)
	}
	if got := Pretty([]any{}); got != "[]" {
		t.Errorf("empty array: %q", got)
	}
}
