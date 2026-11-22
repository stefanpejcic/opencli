// ##############################################################################
// Script Name: internal/ojson/ojson.go
// Description: JSON object that keeps key order, so rewritten lines look like jq output.
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

package ojson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Object is a JSON object with insertion ordered keys, values are any json value or nested Object
type Object struct {
	keys []string
	vals map[string]any
}

// New makes an empty object
func New() *Object { return &Object{vals: map[string]any{}} }

// Set adds or replaces a key, new keys go at the end like jq
func (o *Object) Set(k string, v any) *Object {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
	return o
}

// Get returns a value and whether the key exists
func (o *Object) Get(k string) (any, bool) { v, ok := o.vals[k]; return v, ok }

// Has reports whether the key exists
func (o *Object) Has(k string) bool { _, ok := o.vals[k]; return ok }

// Str returns a string value, numbers and bools are formatted like jq -r prints them
func (o *Object) Str(k string) string {
	v, ok := o.vals[k]
	if !ok || v == nil {
		return ""
	}
	return Text(v)
}

// Delete removes a key
func (o *Object) Delete(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, x := range o.keys {
		if x == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// Keys returns keys in order
func (o *Object) Keys() []string { return o.keys }

// Len is the number of keys
func (o *Object) Len() int { return len(o.keys) }

// Text formats a value like jq -r does
func Text(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprint(t)
	default:
		return Compact(v)
	}
}

// MarshalJSON writes keys in order
func (o *Object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := encode(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := encode(o.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// Compact is like jq -c
func Compact(v any) string { b, _ := encode(v); return string(b) }

// Pretty is like jq . with two space indent
func Pretty(v any) string {
	b, _ := encode(v)
	var out bytes.Buffer
	if json.Indent(&out, b, "", "  ") != nil {
		return string(b)
	}
	return out.String()
}

// Parse decodes any json value keeping object key order, numbers stay json.Number
func Parse(s string) (any, error) {
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	v, err := parseValue(d)
	if err != nil {
		return nil, err
	}
	if d.More() {
		return nil, fmt.Errorf("trailing data")
	}
	return v, nil
}

// ParseObject decodes a json object, error when it's something else
func ParseObject(s string) (*Object, error) {
	v, err := Parse(s)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*Object)
	if !ok {
		return nil, fmt.Errorf("not an object")
	}
	return o, nil
}

func parseValue(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			o := New()
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				v, err := parseValue(d)
				if err != nil {
					return nil, err
				}
				o.Set(k, v)
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
			arr := []any{}
			for d.More() {
				v, err := parseValue(d)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected %v", x)
	default:
		return x, nil
	}
}
