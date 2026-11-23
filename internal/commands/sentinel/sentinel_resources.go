// ##############################################################################
// Script Name: internal/commands/sentinel/sentinel_resources.go
// Description: Check system services, traffic and resource usage, and log/send custom notifications on request.
// Usage: opencli sentinel [--startup] [--report] [--action=<name> --title=<title> --message=<msg> [--severity=<level>] [--details=<json> [--merge-key=<key>]] [--no-send]]
// Author: Stefan Pejcic
// Created: 01.11.2023
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

package sentinel

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"opencli/internal/conf"
	"opencli/internal/requirement"
	"opencli/internal/sh"
)

// fallback when the Caddyfile has no trusted_proxies line, same list as the Caddyfile template
var cloudflareIPv4 = []string{"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22"}

// why an ip must never be blocked, "" if it can be
func neverBlockReason(ip string) string {
	if !ipv4Re.MatchString(ip) {
		return "IPv6"
	}
	if isIPWhitelisted(ip) {
		return "whitelisted"
	}
	for _, c := range []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10"} {
		if ipInCIDR(ip, c) {
			return "private IP"
		}
	}
	for _, h := range fields(sh.Str("hostname", "-I")) {
		if h == ip {
			return "server IP"
		}
	}
	for _, f := range []string{"/etc/csf/csf.allow", "/etc/csf/csf.ignore"} {
		if grepW(conf.Read(f), ip) {
			return "allowed in CSF"
		}
	}
	var ranges []string
	for _, l := range conf.Lines(conf.Caddyfile) {
		if strings.Contains(l, "trusted_proxies") {
			ranges = regexp.MustCompile(`([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]+`).FindAllString(l, -1)
			break
		}
	}
	if len(ranges) == 0 {
		ranges = cloudflareIPv4
	}
	for _, c := range ranges {
		if ipInCIDR(ip, c) {
			return "Cloudflare"
		}
	}
	return ""
}

// lastPort is split($4,a,":"); a[length(a)]
func lastPort(s string) string {
	p := strings.Split(s, ":")
	return p[len(p)-1]
}

// uniqC is sort | uniq -c on the lines, as count and line
func uniqC(ls []string) [][2]string {
	sort.Strings(ls)
	var out [][2]string
	for i := 0; i < len(ls); {
		j := i
		for j < len(ls) && ls[j] == ls[i] {
			j++
		}
		out = append(out, [2]string{strconv.Itoa(j - i), ls[i]})
		i = j
	}
	return out
}

// domlogs whose last 200 lines mention ip, like the timeout 1s tail | grep -qwF loop
func domainsHit(ip string) string {
	var files []string
	filepath.Walk("/var/log/caddy/domlogs", func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() && info.Name() == "access.log" {
			files = append(files, p)
		}
		return nil
	})
	seen := map[string]bool{}
	var doms []string
	for _, f := range files {
		ls := lines(conf.Read(f))
		if len(ls) > 200 {
			ls = ls[len(ls)-200:]
		}
		if grepW(strings.Join(ls, "\n"), ip) {
			d := strings.Replace(f, "/var/log/caddy/domlogs/", "", 1)
			d = strings.Replace(d, "/access.log", "", 1)
			if !seen[d] {
				seen[d] = true
				doms = append(doms, d)
			}
		}
	}
	sort.Strings(doms)
	return strings.Join(doms, ",")
}

func (r *run) checkHTTPSTraffic() {
	if optAttack == "no" {
		r.warn++
		r.println("[!] Website traffic checks are disabled.")
		return
	}
	if !caddyIsActive {
		r.warn++
		r.println("[!] Skipping website traffic checks because Caddy is not running.")
		return
	}
	alert, synAlert, synNote := false, false, ""
	blocked := map[string]string{}
	all := lines(sh.C("ss", "-tn", "( sport = :80 or sport = :443 )").Str())
	if len(all) > 0 {
		all = all[1:]
	}

	// SYN flooding
	var syn []string
	for _, l := range all {
		if strings.Contains(l, "SYN-RECV") {
			syn = append(syn, lastPort(field(l, 4)))
		}
	}
	for _, e := range uniqC(syn) {
		count, port := atoi(e[0]), e[1]
		if count < 1000 {
			continue
		}
		r.printf("%s Possible SYN flood on :%s — %d in SYN_RECV\n", cFail, port, count)
		if synNote == "" && sh.C("sysctl", "-n", "net.ipv4.tcp_syncookies").NoErr().Str() != "1" {
			if sh.C("sysctl", "-qw", "net.ipv4.tcp_syncookies=1").NoErr().Run() == nil {
				synNote = " Sentinel turned on SYN cookies (net.ipv4.tcp_syncookies=1) until the next reboot."
			} else {
				synNote = " Sentinel failed to turn on SYN cookies."
			}
			r.printf("    %s%s\n", cArr, synNote)
		}
		r.writeNotification("warning", "traffic", "Possible SYN flood", fmt.Sprintf("Port %s: %d connections in SYN_RECV state.%s", port, count, synNote), "")
		alert, synAlert = true, true
	}

	var est []string
	peerV6 := regexp.MustCompile(`^\[.*\]:[0-9]+$`)
	for _, l := range all {
		if !strings.Contains(l, "ESTAB") {
			continue
		}
		port := lastPort(field(l, 4))
		peer := field(l, 5)
		if peerV6.MatchString(peer) {
			peer = strings.TrimPrefix(peer, "[")
			peer = regexp.MustCompile(`\]:[0-9]+$`).ReplaceAllString(peer, "")
		} else {
			peer = regexp.MustCompile(`:[0-9]+$`).ReplaceAllString(peer, "")
		}
		peer = strings.TrimPrefix(peer, "::ffff:")
		est = append(est, port+" "+peer)
	}
	var high []string
	for _, e := range uniqC(est) {
		count := atoi(e[0])
		port, ip, _ := strings.Cut(e[1], " ")
		ip = field(ip, 1)
		if count < maxConnPerIP {
			continue
		}
		reason := neverBlockReason(ip)
		if reason != "" && reason != "IPv6" {
			// admin already trusts this IP, nothing to act on
			r.printf("%s High connections from %s on port %s: %d (%s, not reported)\n", cOK, ip, port, count, reason)
			continue
		}
		r.printf("%s High connections from %s on port %s: %d\n", cFail, ip, port, count)
		domains := domainsHit(ip)
		note := ""
		if blockIP == "yes" {
			switch {
			case blocked[ip] != "":
				note = blocked[ip]
			case reason != "":
				note = " | not blocked: " + reason
			case !sh.Has("csf"):
				note = " | not blocked: CSF is not installed"
			case tmo(30, "csf", "-td", ip, strconv.Itoa(blockIPMinutes*60), fmt.Sprintf("Sentinel: %d connections on port %s", count, port)).Quiet().Run() == nil:
				note = fmt.Sprintf(" | blocked for %d min", blockIPMinutes)
			default:
				note = " | failed to block with csf -td"
			}
			blocked[ip] = note
			r.printf("    %s%s\n", cArr, strings.TrimPrefix(note, " |"))
		}
		if domains != "" {
			r.printf("    %s Domain hit: %s\n", cArr, domains)
			high = append(high, fmt.Sprintf("%s (port %s: %d conns | domains: %s%s)", ip, port, count, domains, note))
		} else {
			r.printf("    %s No matching domain logs found, check manually with: 'grep -Rli --include=access.log %s /var/log/caddy/domlogs/ | xargs -n1 dirname | xargs -n1 basename'\n", cArr, ip)
			high = append(high, fmt.Sprintf("%s (port %s: %d conns%s)", ip, port, count, note))
		}
		alert = true
	}

	if len(high) > 0 {
		body := strings.Join(high, "\n")
		if len(high) == 1 {
			who, _, _ := strings.Cut(high[0], "(")
			r.writeNotification("warning", "traffic", "High traffic from "+who, body, "")
		} else {
			r.writeNotification("warning", "traffic", "High traffic from multiple IPs", fmt.Sprintf("%d IPs over the limit:\n%s", len(high), body), "")
		}
	} else {
		r.resolveNotification("High traffic from ", true)
	}
	if !synAlert {
		r.resolveNotification("Possible SYN flood", false)
	}

	// Total established
	total := len(est)
	if total >= maxTotalConn {
		r.printf("%s High total connections: %d\n", cFail, total)
		r.writeNotification("warning", "traffic", "High total connections", fmt.Sprintf("%d total connections on ports 80/443", total), "")
		alert = true
	} else {
		r.resolveNotification("High total connections", false)
	}

	if !alert {
		r.pass++
		r.printf("%s No unusual traffic detected on web ports (80|443).\n", cOK)
	} else {
		r.fail++
		r.status = 2
	}
}

func (r *run) checkSwapUsage() {
	title := "High SWAP usage!"
	s := freeLine("Swap:")
	stotal, sused := s[0], s[1]
	reFailed := "SWAP re-enable failed on " + hostname

	if stotal == 0 {
		devs := sh.C("swapon", "--show=NAME", "--noheadings").NoErr().Str()
		if devs != "" {
			r.pass++
			r.printf("%s No SWAP configured.\n", cOK)
			r.resolveNotification(reFailed, false)
			return
		}
		var fstab []string
		for _, l := range lines(conf.Read("/etc/fstab")) {
			if field(l, 3) == "swap" {
				fstab = append(fstab, field(l, 1))
			}
		}
		if len(fstab) == 0 {
			r.pass++
			r.printf("%s No SWAP configured.\n", cOK)
			r.resolveNotification(reFailed, false)
			return
		}
		r.printf("%s SWAP is off but fstab entries exist. Attempting to re-enable...\n", cWarn)
		var eb strings.Builder
		sh.C("swapon", "-a").NoOut().Stderr(&eb).Run()
		swErr := strings.TrimRight(eb.String(), "\n")
		if swErr != "" {
			r.warn++
			r.printf("%s Failed to re-enable SWAP: %s\n", cFail, swErr)
			r.writeNotification("warning", "resources", reFailed, "swapon -a failed on "+hostname+" at "+displayTime+". Error: "+swErr, "")
			return
		}
		s = freeLine("Swap:")
		stotal, sused = s[0], s[1]
		if stotal <= 0 {
			r.warn++
			r.printf("%s swapon -a ran but swap still reports 0. Check swap device health.\n", cWarn)
			r.writeNotification("warning", "resources", reFailed, "swapon -a returned success but swap is still 0.", "")
			return
		}
		r.warn++
		r.resolveNotification(reFailed, false)
		writeInfoNotification("resources", "SWAP re-enabled on "+hostname, fmt.Sprintf("Sentinel detected swap was off on %s at %s and re-enabled it via swapon -a. Now: %dMB total.", hostname, displayTime, stotal))
		r.printf("%s SWAP successfully re-enabled (%dMB total).\n", cOK, stotal)
	}

	pct := sused * 100 / stotal
	if pct <= swapThreshold {
		r.pass++
		r.printf("%s SWAP %d%% < threshold %d%%\n", cOK, pct, swapThreshold)
		r.resolveNotification(title, false)
		r.resolveNotification("SWAP high but cannot safely clear", false)
		r.resolveNotification("URGENT: SWAP not cleared on "+hostname, false)
		return
	}

	// lock holds the time of the last clear, if swap filled up again since then clearing won't help so just alert once a day
	if st, err := os.Stat(lockFileForSwapCleanup); err == nil {
		clearedAt := st.ModTime()
		age := int(time.Now().Unix() - clearedAt.Unix())
		if age <= 86400 {
			r.warn++
			r.printf("%s SWAP %d%% > threshold %d%%, already cleared %s ago. Skipping.\n", cWarn, pct, swapThreshold, formatDuration(age))
			r.writeNotification("warning", "resources", title, fmt.Sprintf("SWAP on %s is at %d%% (%dMB/%dMB) again, it was cleared at %s and filled back up. Sentinel won't clear it again for 24h, the server likely needs more RAM. Check with: free -m and top -o %%MEM", hostname, pct, sused, stotal, clearedAt.Format("2006-01-02 15:04:05")), "")
			return
		}
		os.Remove(lockFileForSwapCleanup)
	}

	avail := 0
	for _, l := range conf.Lines("/proc/meminfo") {
		if strings.HasPrefix(l, "MemAvailable:") {
			avail = atoi(field(l, 2)) / 1024
		}
	}
	if avail < sused {
		r.printf("Not enough free RAM to safely clear swap (%dMB available, %dMB in swap). Skipping.\n", avail, sused)
		r.writeNotification("warning", "resources", "SWAP high but cannot safely clear", fmt.Sprintf("SWAP: %d%%, only %dMB RAM available vs %dMB in swap.", pct, avail, sused), "")
		r.warn++
		return
	}

	r.printf("%s SWAP %d%% > threshold %d%%. Clearing...\n", cFail, pct, swapThreshold)
	touch(lockFileForSwapCleanup)
	sh.C("sync").Run()
	os.WriteFile("/proc/sys/vm/drop_caches", []byte("3\n"), 0o644)
	sh.C("swapoff", "-a").Stdout(r.out).Stderr(r.out).Run()
	sh.C("swapon", "-a").Stdout(r.out).Stderr(r.out).Run()
	s2 := freeLine("Swap:")
	stotal2, sused2 := s2[0], s2[1]
	pct2 := 0
	if stotal2 > 0 {
		pct2 = sused2 * 100 / stotal2
	}
	if pct2 < swapThreshold {
		r.resolveNotification(title, false)
		writeInfoNotification("resources", fmt.Sprintf("SWAP cleared — now %d%%", pct2), fmt.Sprintf("Sentinel cleared SWAP on %s at %s. Was %dMB/%dMB (%d%%), now %dMB/%dMB (%d%%).", hostname, displayTime, sused, stotal, pct, sused2, stotal2, pct2))
		r.printf("%s SWAP cleared successfully. Now: %d%%\n", cOK, pct2)
	} else {
		r.fail++
		r.status = 2
		r.printf("%s SWAP still high after cleanup: %d%%\n", cFail, pct2)
		r.writeNotification("critical", "resources", "URGENT: SWAP not cleared on "+hostname, fmt.Sprintf("SWAP was %d%% (threshold %d%%). Sentinel dropped caches and ran swapoff/swapon at %s, but usage is still %d%%. Check with: free -m and top -o %%MEM", pct, swapThreshold, displayTime, pct2), "")
	}
}

func headLines(s string, n int) string {
	ls := lines(s)
	if len(ls) > n {
		ls = ls[:n]
	}
	if len(ls) == 0 {
		return ""
	}
	return strings.Join(ls, "\n") + "\n"
}

func generateCrashlogReport() string {
	dir := "/var/log/openpanel/admin/crashlog"
	os.MkdirAll(dir, 0o755)
	report := fmt.Sprintf("%s/%d.txt", dir, time.Now().Unix())
	var b strings.Builder
	fmt.Fprintf(&b, "=== GENERAL === Hostname: %s | Date: %s\n", hostname, displayTime)
	b.WriteString("=== LOAD ===\n" + conf.Read("/proc/loadavg"))
	b.WriteString("=== TOP (MEM) ===\n" + headLines(sh.C("ps", "-eo", "pid:7,%mem:5,comm:20", "--sort=-%mem").Str(), 11))
	b.WriteString("=== TOP (CPU) ===\n" + headLines(sh.C("ps", "-eo", "pid:7,%cpu:5,comm:20", "--sort=-%cpu").Str(), 11))
	b.WriteString("=== SWAP TOP ===\n")
	type sw struct {
		v    float64
		line string
	}
	var sws []sw
	st, _ := filepath.Glob("/proc/*/status")
	for _, f := range st {
		for _, l := range conf.Lines(f) {
			if strings.Contains(l, "VmSwap") {
				line := f + ":" + l
				v, _ := strconv.ParseFloat(field(line, 2), 64)
				sws = append(sws, sw{v, line})
			}
		}
	}
	sort.Slice(sws, func(i, j int) bool {
		if sws[i].v != sws[j].v {
			return sws[i].v > sws[j].v
		}
		return sws[i].line > sws[j].line
	})
	for i, s := range sws {
		if i == 10 {
			break
		}
		b.WriteString(s.line + "\n")
	}
	b.WriteString("=== DISKSTATS ===\n" + headLines(conf.Read("/proc/diskstats"), 10))
	os.WriteFile(report, []byte(b.String()), 0o644)
	return report
}

var domainRe = regexp.MustCompile(`^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

func digShort(args ...string) string {
	return sh.C("dig", append([]string{"+short"}, args...)...).NoErr().Str()
}

func (r *run) checkDNS() {
	if optDNS == "no" {
		r.warn++
		r.printf("%s DNS check disabled.\n", cWarn)
		return
	}
	if age, ok := ageSecs(lockFileForDNSCheck); ok && age < 3600 {
		r.warn++
		r.printf("%s DNS check skipped (last run %dm ago).\n", cWarn, age/60)
		return
	}
	touch(lockFileForDNSCheck)

	forced := sh.Opencli("domain").NoErr().Str()
	checkDomain := domainRe.MatchString(forced)
	var ns [4]string
	ns[0] = confGet("ns1")
	checkNS := domainRe.MatchString(ns[0])
	if checkNS {
		ns[1], ns[2], ns[3] = confGet("ns2"), confGet("ns3"), confGet("ns4")
	}
	if !checkDomain && !checkNS {
		r.warn++
		r.printf("%s No valid domain/NS configured. Skipping DNS check.\n", cWarn)
		return
	}
	gns := "8.8.8.8"
	serverIP := getPublicIP()

	if checkDomain {
		requirement.Command("dig", "bind-utils")
		domainIP := digShort("@"+gns, forced)
		if domainIP == serverIP {
			r.pass++
			r.printf("%s %s → %s\n", cOK, forced, serverIP)
			r.resolveNotification(forced+" does not resolve to ", true)
		} else if strings.Contains(strings.ToLower(digShort("@"+gns, "NS", forced)), "cloudflare") {
			r.warn++
			r.printf("%s %s uses Cloudflare proxy — skipping IP check.\n", cWarn, forced)
			r.resolveNotification(forced+" does not resolve to ", true)
		} else {
			r.fail++
			r.status = 2
			r.printf("%s %s resolves to %s, expected %s\n", cFail, forced, domainIP, serverIP)
			got := domainIP
			if got == "" {
				got = "nothing"
			}
			r.writeNotification("warning", "dns", forced+" does not resolve to "+serverIP, forced+" should point to "+serverIP+" but resolves to "+got+". Update its A record at your DNS provider.", "")
		}
	} else {
		r.warn++
		r.printf("%s Domain not set; skipping domain DNS check.\n", cWarn)
	}

	if !checkNS {
		r.pass++
		r.printf("%s No nameservers configured; skipping NS check.\n", cOK)
		return
	}
	if ns[1] == "" {
		r.warn++
		r.printf("%s Only one NS set — add at least two for redundancy.\n", cWarn)
		return
	}
	allIPs := sh.C("hostname", "-I").Str()
	var failed []string
	for i, host := range ns {
		if host == "" {
			continue
		}
		nsIP := digShort("@"+gns, host)
		ok := false
		for _, p := range strings.Split(nsIP, "\n") {
			// grep -w with an empty pattern matches any line
			if p == "" || grepW(allIPs, p) {
				ok = true
			}
		}
		if !ok {
			failed = append(failed, fmt.Sprintf("NS%d (%s) → %s (expected: %s)", i+1, host, nsIP, allIPs))
		}
	}
	if len(failed) == 0 {
		r.pass++
		r.printf("%s All nameservers resolve to local IPs.\n", cOK)
		r.resolveNotification("Nameservers do not resolve to local IPs", false)
		return
	}
	r.fail++
	r.status = 2
	for _, f := range failed {
		r.printf("    %s\n", f)
	}
	r.writeNotification("warning", "dns", "Nameservers do not resolve to local IPs", strings.Join(failed, "\n")+"\nUpdate the glue/A records of these nameservers at your domain registrar.", "")
}

func (r *run) writeSnapshot() {
	os.MkdirAll(filepath.Dir(snapshotFile), 0o755)
	la := fields(conf.Read("/proc/loadavg"))
	for len(la) < 3 {
		la = append(la, "")
	}
	m := freeLine("Mem:")
	memPct := 0
	if m[0] > 0 {
		memPct = m[1] * 100 / m[0]
	}
	s := freeLine("Swap:")
	swapPct := 0
	if s[0] > 0 {
		swapPct = s[1] * 100 / s[0]
	}
	dp := diskPct()
	cpu := cpuPct(100 * time.Millisecond)

	// web_conn_ip is the busiest IP on one port, counted the same way as the per-IP traffic check
	total, maxIP := 0, 0
	counts := map[string]int{}
	for _, l := range lines(sh.C("ss", "-tn", "( sport = :80 or sport = :443 )").NoErr().Str()) {
		if field(l, 1) != "ESTAB" {
			continue
		}
		total++
		peer := regexp.MustCompile(`:[0-9]+$`).ReplaceAllString(field(l, 5), "")
		peer = strings.NewReplacer("[", "", "]", "").Replace(peer)
		k := lastPort(field(l, 4)) + " " + peer
		counts[k]++
		if counts[k] > maxIP {
			maxIP = counts[k]
		}
	}
	line := fmt.Sprintf(`{"ts":"%s","load":{"1m":"%s","5m":"%s","15m":"%s"},"cpu_pct":%d,"mem":{"total_mb":%d,"used_mb":%d,"pct":%d},"swap":{"total_mb":%d,"used_mb":%d,"pct":%d},"disk_pct":%d,"web_conn":%d,"web_conn_ip":%d,"status":%d,"pass":%d,"warn":%d,"fail":%d}`,
		time.Now().UTC().Format("2006-01-02 15:04:05"), la[0], la[1], la[2], cpu, m[0], m[1], memPct, s[0], s[1], swapPct, dp, total, maxIP, r.status, r.pass, r.warn, r.fail)
	if f, err := os.OpenFile(snapshotFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
		f.WriteString(line + "\n")
		f.Close()
	}
	data := conf.Read(snapshotFile)
	if n := strings.Count(data, "\n"); n > snapshotMaxLines {
		ls := strings.Split(strings.TrimSuffix(data, "\n"), "\n")
		ls = ls[len(ls)-snapshotMaxLines:]
		if tmp, err := os.CreateTemp(filepath.Dir(snapshotFile), "tmp."); err == nil {
			tmp.WriteString(strings.Join(ls, "\n") + "\n")
			tmp.Close()
			if os.Rename(tmp.Name(), snapshotFile) != nil {
				os.Remove(tmp.Name())
			}
		}
	}
}
