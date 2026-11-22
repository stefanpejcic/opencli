// ##############################################################################
// Script Name: internal/commands/docker/assoc.go
// Description: Helper that orders keys like a bash associative array.
// Usage: (internal helper)
// Author: Stefan Pejcic
// Created: 14.05.2026
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

package docker

import "sort"

// bashAssocOrder lists keys the way bash walks an associative array, by hash bucket and newest first inside a bucket
func bashAssocOrder(keys []string) []string {
	const buckets = 1024
	type item struct {
		key    string
		bucket uint32
		seq    int
	}
	items := make([]item, len(keys))
	for i, k := range keys {
		h := uint32(2166136261)
		for _, c := range []byte(k) {
			h *= 16777619
			h ^= uint32(c)
		}
		items[i] = item{k, h & (buckets - 1), i}
	}
	sort.SliceStable(items, func(a, b int) bool {
		if items[a].bucket != items[b].bucket {
			return items[a].bucket < items[b].bucket
		}
		return items[a].seq > items[b].seq
	})
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.key
	}
	return out
}
