// ##############################################################################
// Script Name: internal/redis/redis.go
// Description: Targeted Redis cache invalidation for data the OpenPanel UI memoizes, never FLUSHALL or wildcard openpanel_cache_*.
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

package redis

import (
	"strings"

	"opencli/internal/sh"
)

// Container is the redis container name
const Container = "openpanel_redis"

// CLI runs redis-cli inside the redis container
func CLI(args ...string) *sh.Cmd {
	return sh.C("podman", append([]string{"exec", Container, "redis-cli"}, args...)...)
}

// DropKey deletes exact keys, no-op without keys
func DropKey(keys ...string) {
	if len(keys) == 0 {
		return
	}
	CLI(append([]string{"DEL"}, keys...)...).Quiet().Run()
}

func scan(pattern string) []string {
	out := CLI("--scan", "--pattern", pattern).NoErr().Str()
	var keys []string
	for _, k := range strings.Split(out, "\n") {
		if k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

// DropPattern unlinks the panel cache keys matching each pattern, keep patterns targeted
func DropPattern(patterns ...string) {
	for _, p := range patterns {
		for _, k := range scan(p) {
			CLI("UNLINK", k).Quiet().Run()
		}
	}
}

// DropUserCache drops the per-user cache, pass the db id and every username it was cached under
func DropUserCache(userID string, usernames ...string) {
	var keys []string
	if userID != "" {
		keys = append(keys, "openpanel_cache_get_user_details_with_plan:"+userID, "openpanel_cache_get_2fa_status_for_user:"+userID)
	}
	for _, u := range usernames {
		if u == "" {
			continue
		}
		keys = append(keys, "openpanel_cache_get_feature_set_on_plan:"+u, "openpanel_cache_query_context_by_username:"+u, "openpanel_cache_get_uid:"+u)
		DropPattern("openpanel_cache_load_user_features:" + u + ":*")
	}
	DropKey(keys...)
}

// DropUserSessions logs a user out everywhere
func DropUserSessions(userID string) {
	if userID == "" {
		return
	}
	for _, k := range scan("session:" + userID + ":*") {
		CLI("UNLINK", k).Quiet().Run()
	}
}
