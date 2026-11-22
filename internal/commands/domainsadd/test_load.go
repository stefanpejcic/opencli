// ##############################################################################
// Script Name: internal/commands/domainsadd/test_load.go
// Description: Load test mode of domains-test.
// Usage: opencli domains-test <DOMAIN_NAME>[/SUBFOLDER] [RUNS] [--load <N|auto>]
// Author: Stefan Pejcic
// Created: 28.09.2026
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

package domainsadd

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"opencli/internal/conf"
	"opencli/internal/podman"
)

const loadSnapScript = `c=/sys/fs/cgroup; echo "$(awk "/^throttled_usec/{print \$2}" $c/cpu.stat 2>/dev/null || echo 0) $(cat $c/memory.current 2>/dev/null || echo 0) $(cat $c/memory.max 2>/dev/null || echo max) $(awk "/^oom_kill /{print \$2}" $c/memory.events 2>/dev/null || echo 0) $(cat $c/pids.current 2>/dev/null || echo 0) $(cat $c/pids.max 2>/dev/null || echo max) $(awk "/^max /{print \$2}" $c/pids.events 2>/dev/null || echo 0)"`

const loadDBScript = `c=$(command -v mariadb || command -v mysql); $c -uroot ${MYSQL_ROOT_PASSWORD:+-p"$MYSQL_ROOT_PASSWORD"} -N -e "SELECT @@max_connections, VARIABLE_VALUE FROM information_schema.GLOBAL_STATUS WHERE VARIABLE_NAME = \"MAX_USED_CONNECTIONS\"" 2>/dev/null`

type loadStepRes struct {
	total, ok, err int
	rps            string
	p50, p95, p99  string
	codes          string
}

type loadCtx struct {
	s        *speed
	opts     curlOpts
	deadline time.Time
}

// loadStep fires n requests over c connections, big runs are split in several clients like the curl processes were
func (l *loadCtx) loadStep(c, n int) loadStepRes {
	procs := (c + 199) / 200
	ctx, cancel := context.WithDeadline(context.Background(), l.deadline)
	if time.Until(l.deadline) < time.Second {
		cancel()
		ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	}
	defer cancel()
	type rec struct {
		code string
		t    float64
	}
	var mu sync.Mutex
	var recs []rec
	t0 := time.Now()
	var wg sync.WaitGroup
	for p := 1; p <= procs; p++ {
		conc := c / procs
		if p <= c%procs {
			conc++
		}
		reqs := n * conc / c
		if reqs < conc {
			reqs = conc
		}
		tr := &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true, CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256}},
			TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
			MaxIdleConnsPerHost: conc,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if t, ok := l.opts.dial[addr]; ok {
					addr = t
				}
				return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
			},
		}
		client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		var next int
		var nmu sync.Mutex
		for w := 0; w < conc; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					nmu.Lock()
					if next >= reqs {
						nmu.Unlock()
						return
					}
					next++
					nmu.Unlock()
					rctx, rcancel := context.WithTimeout(ctx, 30*time.Second)
					start := time.Now()
					req, _ := http.NewRequestWithContext(rctx, "GET", l.opts.url, nil)
					req.Header.Set("User-Agent", "OpenPanel load test")
					for _, h := range l.opts.headers {
						req.Header.Set(h[0], h[1])
					}
					code := "000"
					resp, err := client.Do(req)
					if err == nil {
						code = strconv.Itoa(resp.StatusCode)
						io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
					}
					rcancel()
					// cut by the overall limit, dropped instead of counted
					if ctx.Err() != nil {
						return
					}
					mu.Lock()
					recs = append(recs, rec{code, time.Since(start).Seconds()})
					mu.Unlock()
				}
			}()
		}
	}
	wg.Wait()
	wall := time.Since(t0)
	var oks []float64
	codes := map[string]int{}
	for _, r := range recs {
		if r.code != "" && (r.code[0] == '2' || r.code[0] == '3') {
			oks = append(oks, r.t*1000)
		} else {
			k := r.code
			if k == "000" {
				k = "timeout/refused"
			}
			codes[k]++
		}
	}
	sort.Float64s(oks)
	res := loadStepRes{total: len(recs), ok: len(oks)}
	res.err = res.total - res.ok
	res.rps = fmt.Sprintf("%.1f", float64(res.ok)/wall.Seconds())
	pct := func(p float64) string {
		if len(oks) == 0 {
			return "0"
		}
		k := int(float64(len(oks)) * p)
		if k < 1 {
			k = 1
		}
		// awk printed the ms with %.6g first
		v, _ := strconv.ParseFloat(fmt.Sprintf("%.6g", oks[k-1]), 64)
		return fmt.Sprintf("%.0f", v)
	}
	res.p50, res.p95, res.p99 = pct(0.50), pct(0.95), pct(0.99)
	var keys []string
	for k := range codes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%sx%d", k, codes[k]))
	}
	res.codes = strings.Join(parts, ",")
	if res.codes == "" {
		res.codes = "-"
	}
	return res
}

func atoi64(s string) (int64, bool) {
	if !isNum(s) {
		return 0, false
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v, true
}

func (s *speed) loadTest() int {
	B, R, Y, G, N := s.B, s.R, s.Y, s.G, s.N
	l := &loadCtx{s: s}
	var loadTarget string
	if s.viaVarnish {
		loadTarget = "Varnish (127.0.0.1:" + s.httpPort + ")"
		l.opts = curlOpts{url: "http://" + s.domain + s.urlPath, dial: map[string]string{s.domain + ":80": "127.0.0.1:" + s.httpPort}, headers: [][2]string{{"X-Forwarded-Proto", "https"}}}
	} else {
		loadTarget = s.webServer + " (127.0.0.1:" + s.httpsPort + ")"
		l.opts = curlOpts{url: "https://" + s.domain + s.urlPath, dial: map[string]string{s.domain + ":443": "127.0.0.1:" + s.httpsPort}}
	}

	var services []string
	if s.viaVarnish {
		services = append(services, "varnish")
	}
	services = append(services, s.webServer)
	if !s.litespeed && s.phpContainer != "" {
		services = append(services, s.phpContainer)
	}
	services = append(services, s.dbContainer)

	php := s.phpContainer
	if php == "" {
		php = "no php"
	}
	fmt.Printf("%sLoad test%s    %s  (user %s, %s, %s, %s)\n", B, N, s.site, s.owner, s.webServer, php, s.dbContainer)
	fmt.Printf("%sTarget%s       %s, local so caddy, the WAF and DNS are skipped\n", B, N, loadTarget)
	if s.viaVarnish {
		fmt.Printf("%sNote%s         the page comes from the Varnish cache, PHP is only hit on cache misses\n", B, N)
	}
	mode := s.load + " concurrent connections"
	if s.load == "auto" {
		mode = "auto, doubling concurrent connections until errors or response times collapse"
	}
	fmt.Printf("%sMode%s         %s\n", B, N, mode)
	fmt.Println()

	loadStart := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	// whole test stops after a minute, whatever step it's on
	const budget = 60
	startSec := time.Now().Unix()
	deadlineSec := startSec + budget
	l.deadline = time.Unix(deadlineSec, 0)
	timedOut := false
	loadTotal, loadOK := 0, 0

	type snap struct{ a, m, mx, o, pc, pm, ph string }
	cgSnap := func(sv string) (snap, bool) {
		out := s.inContainer(sv, "sh", "-c", loadSnapScript).NoErr().Str()
		f := strings.Fields(out)
		var sn snap
		if len(f) == 0 {
			return sn, false
		}
		for i, p := range []*string{&sn.a, &sn.m, &sn.mx, &sn.o, &sn.pc, &sn.pm, &sn.ph} {
			if i < len(f) {
				*p = f[i]
			}
		}
		return sn, true
	}
	def := snap{"0", "0", "max", "0", "0", "max", "0"}
	snap0 := map[string]snap{}
	have0 := map[string]bool{}
	throttleTotal, memPeak, oomTotal, pidsHits, pidsPeak := map[string]int64{}, map[string]int64{}, map[string]int64{}, map[string]int64{}, map[string]int64{}
	for _, sv := range services {
		if s.isRunning(sv) {
			sn, ok := cgSnap(sv)
			snap0[sv], have0[sv] = sn, ok
		}
	}
	collect := func() {
		for _, sv := range services {
			if !s.isRunning(sv) {
				continue
			}
			s1, ok1 := cgSnap(sv)
			s0 := snap0[sv]
			if !have0[sv] {
				s0 = def
			}
			if !ok1 {
				s1 = def
			}
			if a1, ok := atoi64(s1.a); ok {
				if a0, ok := atoi64(s0.a); ok {
					throttleTotal[sv] += (a1 - a0) / 1000
				}
			}
			if m1, ok := atoi64(s1.m); ok {
				if mx1, ok := atoi64(s1.mx); ok && mx1 != 0 {
					if pct := m1 * 100 / mx1; pct > memPeak[sv] {
						memPeak[sv] = pct
					}
				}
			}
			if o1, ok := atoi64(s1.o); ok {
				if o0, ok := atoi64(s0.o); ok {
					oomTotal[sv] += o1 - o0
				}
			}
			if h1, ok := atoi64(s1.ph); ok {
				if h0, ok := atoi64(s0.ph); ok {
					pidsHits[sv] += h1 - h0
				}
			}
			if pc1, ok := atoi64(s1.pc); ok && pc1 > pidsPeak[sv] {
				pidsPeak[sv] = pc1
			}
			snap0[sv], have0[sv] = s1, ok1
		}
	}

	var ladder []int
	if s.load == "auto" {
		ladder = []int{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048}
	} else {
		n, _ := strconv.Atoi(s.load)
		ladder = []int{n}
	}

	fmt.Printf("  %s %s %s %s %s %s %s %s\n", padR("conns", 6), padR("requests", 8), padR("errors", 7), padR("req/s", 9), padR("p50 ms", 9), padR("p95 ms", 9), padR("p99 ms", 9), "throttled")
	bestRPS, bestC, goodC := "0", 0, 0
	goodRPS, goodP95, baseP95 := "0", "0", ""
	brokeC, brokeWhy, saturatedC := 0, "", 0
	prevRPS := "0"
	stepT := map[string]int64{}
	var lastC int
	for _, c := range ladder {
		lastC = c
		n := awkNum(prevRPS) * 5
		if n < float64(c*4) {
			n = float64(c * 4)
		}
		if n < 20 {
			n = 20
		}
		if n > float64(c*40) {
			n = float64(c * 40)
		}
		if n > 30000 {
			n = 30000
		}
		before := map[string]int64{}
		for _, sv := range services {
			before[sv] = throttleTotal[sv]
		}
		r := l.loadStep(c, int(n))
		loadTotal += r.total
		loadOK += r.ok
		// the time limit cut this step short, requests still in flight were dropped rather than counted as errors
		if time.Now().Unix() >= deadlineSec {
			timedOut = true
		}
		collect()
		for _, sv := range services {
			stepT[sv] = throttleTotal[sv] - before[sv]
		}
		errpct := "100.0"
		if r.total > 0 {
			errpct = fmt.Sprintf("%.1f", float64(r.err)*100/float64(r.total))
		} else {
			errpct = "100.0"
		}
		ecolor := ""
		if gt(errpct, "0") {
			ecolor = Y
		}
		if gt(errpct, "5") {
			ecolor = R
		}
		var thr []string
		for _, sv := range services {
			if stepT[sv] > 50 {
				thr = append(thr, sv)
			}
		}
		fmt.Printf("  %s %s %s%s%s %s %s %s %s %s\n", padR(strconv.Itoa(c), 6), padR(strconv.Itoa(r.total), 8), ecolor, padR(errpct+"%", 7), N, padR(r.rps, 9), padR(r.p50, 9), padR(r.p95, 9), padR(r.p99, 9), strings.Join(thr, " "))
		if r.err > 0 {
			fmt.Printf("         errors: %s\n", r.codes)
		}
		if timedOut {
			fmt.Printf("         %scut short by the %ds time limit%s\n", Y, budget, N)
		}

		if baseP95 == "" && r.ok > 0 {
			baseP95 = r.p95
		}
		if gt(errpct, "5") {
			brokeC, brokeWhy = c, fmt.Sprintf("%s%% of requests failed (%s)", errpct, r.codes)
			break
		}
		if baseP95 != "" {
			v := awkNum(baseP95) * 10
			if v < 3000 {
				v = 3000
			}
			if gt(r.p95, fmt.Sprintf("%.6g", v)) {
				brokeC, brokeWhy = c, fmt.Sprintf("response times collapsed, p95 went from %s ms to %s ms", baseP95, r.p95)
				break
			}
		}
		if r.ok > 0 {
			goodC, goodRPS, goodP95 = c, r.rps, r.p95
		}
		if gt(r.rps, bestRPS) {
			bestRPS, bestC = r.rps, c
		}
		if timedOut {
			break
		}
		if saturatedC == 0 && gt(prevRPS, "0") && !gt(r.rps, fmt.Sprintf("%.6g", awkNum(prevRPS)*1.1)) {
			saturatedC = c
		}
		prevRPS = r.rps
	}

	// which limit got hit
	type limit struct{ text, fix string }
	var limits []limit
	for _, sv := range services {
		if !s.isRunning(sv) {
			limits = append(limits, limit{sv + " stopped running during the test.", "Check it with: podman logs " + sv + " (as user " + s.ctx + "), a crash under load usually means its memory limit is too low."})
			continue
		}
		prefix := envName(sv)
		cpuLimit, ramLimit, pidsLimit := s.envVal(prefix+"_CPU"), s.envVal(prefix+"_RAM"), s.envVal(prefix+"_PIDS")
		q := func(v string) string {
			if v == "" {
				return "?"
			}
			return v
		}
		if throttleTotal[sv] > 200 {
			limits = append(limits, limit{fmt.Sprintf("%s hit its CPU limit (%s CPU) and was paused for %d ms.", sv, q(cpuLimit), throttleTotal[sv]), fmt.Sprintf("Raise %s_CPU in %s (now %s) and recreate the container, or raise the CPU in the hosting plan.", prefix, s.envFile, q(cpuLimit))})
		}
		if memPeak[sv] >= 90 {
			limits = append(limits, limit{fmt.Sprintf("%s used %d%% of its memory limit (%s).", sv, memPeak[sv], q(ramLimit)), fmt.Sprintf("Raise %s_RAM in %s (now %s) and recreate the container.", prefix, s.envFile, q(ramLimit))})
		}
		if oomTotal[sv] > 0 {
			limits = append(limits, limit{fmt.Sprintf("%s was killed %d times for running out of memory (%s).", sv, oomTotal[sv], q(ramLimit)), fmt.Sprintf("Raise %s_RAM in %s (now %s) and recreate the container.", prefix, s.envFile, q(ramLimit))})
		}
		if pidsHits[sv] > 0 {
			limits = append(limits, limit{fmt.Sprintf("%s reached its process limit (%s, %d forks refused).", sv, q(pidsLimit), pidsHits[sv]), fmt.Sprintf("Raise %s_PIDS in %s (now %s) and recreate the container.", prefix, s.envFile, q(pidsLimit))})
		}
	}
	sinceLogs := func(c string) string {
		out, _ := podman.Ctx(s.ctx, "logs", "--since", loadStart, c).Combined()
		return out
	}
	if !s.litespeed && s.phpContainer != "" && s.isRunning(s.phpContainer) {
		mc := grepCount(sinceLogs(s.phpContainer), "max_children")
		maxch := ""
		out := s.inContainer(s.phpContainer, "sh", "-c", `grep -h "^pm.max_children" /usr/local/etc/php-fpm.d/*.conf 2>/dev/null | tail -1`).NoErr().Str()
		if _, v, ok := strings.Cut(out, "="); ok {
			maxch = strings.ReplaceAll(v, " ", "")
		}
		if mc > 0 {
			m := maxch
			if m == "" {
				m = "?"
			}
			limits = append(limits, limit{s.phpContainer + " ran out of PHP workers (pm.max_children = " + m + "), requests queued.", "pm.max_children is tuned from the container memory, raise " + envName(s.phpContainer) + "_RAM in " + s.envFile + " and recreate " + s.phpContainer + ", or turn on the page cache so fewer requests reach PHP."})
		}
	}
	switch s.webServer {
	case "nginx", "openresty":
		hits := grepCount(sinceLogs(s.webServer), "worker_connections are not enough")
		var wcs []string
		for _, m := range regexp.MustCompile(`worker_connections[[:space:]]+[0-9]+`).FindAllString(conf.Read("/home/"+s.ctx+"/"+s.webServer+".conf"), -1) {
			wcs = append(wcs, strings.Fields(m)[1])
		}
		wc := strings.Join(wcs, "\n")
		if wc == "" {
			wc = "?"
		}
		if hits > 0 {
			limits = append(limits, limit{s.webServer + " ran out of connections (worker_connections " + wc + ").", "Raise worker_connections in /home/" + s.ctx + "/" + s.webServer + ".conf and restart " + s.webServer + "."})
		}
	case "apache":
		hits := grepCount(sinceLogs("apache"), "MaxRequestWorkers")
		mrw := ""
		if m := regexp.MustCompile(`(?m)^[[:space:]]*MaxRequestWorkers[[:space:]]+[0-9]+`).FindString(conf.Read("/home/" + s.ctx + "/httpd.conf")); m != "" {
			mrw = strings.Fields(m)[1]
		}
		if mrw == "" {
			mrw = "?"
		}
		if hits > 0 {
			limits = append(limits, limit{"apache reached MaxRequestWorkers (" + mrw + "), new connections had to wait.", "Raise MaxRequestWorkers (and ServerLimit/ThreadsPerChild to match) in /home/" + s.ctx + "/httpd.conf and restart apache."})
		}
	}
	if s.isRunning(s.dbContainer) {
		out := s.inContainer(s.dbContainer, "sh", "-c", loadDBScript).NoErr().Str()
		f := strings.Fields(out)
		if len(f) >= 2 && isNum(f[0]) && isNum(f[1]) {
			mx, _ := strconv.ParseInt(f[0], 10, 64)
			used, _ := strconv.ParseInt(f[1], 10, 64)
			if used >= mx {
				limits = append(limits, limit{s.dbContainer + " ran out of connections (max_connections " + f[0] + ").", "Raise max_connections in /home/" + s.ctx + "/custom.cnf and restart " + s.dbContainer + "."})
			}
		}
	}

	// requests left in the queues keep the site busy after the test, see how long until a single visit is answered again
	recovery := -1
	recT0 := time.Now().Unix()
	for time.Now().Unix()-recT0 < 60 {
		o := l.opts
		o.timeout = 5 * time.Second
		o.ua = ""
		code := curlTimed(withHTTP1(o), false).code
		if code != "" && (code[0] == '2' || code[0] == '3') {
			recovery = int(time.Now().Unix() - recT0)
			break
		}
		time.Sleep(time.Second)
	}

	fmt.Println()
	fmt.Printf("%s======================================================================%s\n", B, N)
	fmt.Printf("%sLoad test result for %s%s\n", B, s.site, N)
	fmt.Printf("%s======================================================================%s\n", B, N)
	if goodC > 0 {
		fmt.Printf("%sHandled:%s   %d concurrent connections at %s req/s (p95 %s ms, under 5%% errors)\n", G, N, goodC, goodRPS, goodP95)
	} else {
		fmt.Printf("%sHandled:%s   not even 1 connection without errors\n", R, N)
	}
	if bestC > 0 {
		fmt.Printf("%sPeak:%s      %s req/s at %d concurrent connections\n", B, N, bestRPS, bestC)
	}
	if recovery < 0 {
		fmt.Printf("%sRecovery:%s  still not answering 60s after the test, requests queued during the test are still being processed\n", R, N)
	} else if recovery > 2 {
		fmt.Printf("%sRecovery:%s  the site answered normally again %ds after the test\n", Y, N, recovery)
	}
	if saturatedC > 0 {
		fmt.Printf("%sSaturated:%s from %d connections on throughput stopped growing, more connections only queue up\n", B, N, saturatedC)
	}
	if brokeC > 0 {
		fmt.Printf("%sBroke at:%s  %d concurrent connections, %s\n", R, N, brokeC, brokeWhy)
	} else if timedOut {
		fmt.Printf("%sTime limit:%s stopped after %ds at %d concurrent connections without breaking, %d of %d requests answered in that minute\n", Y, N, budget, lastC, loadOK, loadTotal)
	} else if s.load == "auto" {
		fmt.Printf("%sDid not break%s up to %d concurrent connections\n", G, N, ladder[len(ladder)-1])
	}
	fmt.Println()
	if len(limits) > 0 {
		fmt.Printf("%sLimits reached:%s\n", B, N)
		for i, lm := range limits {
			fmt.Printf("  %s%d.%s %s\n", Y, i+1, N, lm.text)
			fmt.Printf("     Fix: %s\n", lm.fix)
		}
	} else {
		fmt.Printf("%sNo container limit was hit.%s\n", B, N)
		fmt.Printf("  The bottleneck is outside the site's containers: the server CPU (load %s on %d cores, the load generator runs on this server too) or the application itself.\n", loadavg1(), nproc())
		if !s.viaVarnish && !s.litespeed {
			fmt.Println("  Turning on the page cache (Varnish) usually raises the number of visitors a site can handle the most.")
		}
	}
	fmt.Println()
	return 0
}

func withHTTP1(o curlOpts) curlOpts { o.http1 = true; return o }
