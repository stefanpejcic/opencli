// ##############################################################################
// Script Name: internal/commands/files/purge_trash.go
// Description: Auto-purge .Trash folders for users.
// Usage: opencli files-purge_trash [--user USERNAME] [--force] [--dry-run]
// Author: Stefan Pejcic
// Created: 03.06.2025
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

package files

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"opencli/internal/cli"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "files-purge_trash",
		Desc:  "Auto-purge .Trash folders for users.",
		Usage: "opencli files-purge_trash [--user USERNAME] [--force] [--dry-run]",
		Run:   purgeTrash,
	})
}

type purgeCtx struct {
	force, dry bool
	maxAge     int64
	now        int64
	tmpDir     string
}

func purgeTrash(args []string) int {
	p := &purgeCtx{tmpDir: "/tmp/trash_purge_" + strconv.Itoa(os.Getpid())}
	targetUser := ""
	os.MkdirAll(p.tmpDir, 0o777&^calcUmask())

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--force":
			p.force = true
		case "--dry-run":
			p.dry = true
		case "--user":
			if i+1 < len(args) && args[i+1] != "" {
				targetUser = args[i+1]
				i++
			} else {
				fmt.Println("[ERROR] --user requires a username argument")
				return 1
			}
		default:
			fmt.Println("[ERROR] Unknown argument: " + args[i])
			return 1
		}
	}

	if p.force {
		fmt.Println("[INFO] Running in FORCE mode: All trash will be purged.")
	}
	if p.dry {
		fmt.Println("[INFO] DRY-RUN mode: No files will be deleted.")
	}

	if !p.force {
		const cfg = "/etc/openpanel/openpanel/conf/openpanel.config"
		if st, err := os.Stat(cfg); err != nil || !st.Mode().IsRegular() {
			fmt.Println("[ERROR] Config file not found: " + cfg)
			return 1
		}
		var vals []string
		for _, l := range purgeLines(cfg) {
			if strings.HasPrefix(l, "autopurge_trash=") {
				v := strings.TrimPrefix(l, "autopurge_trash=")
				if i := strings.IndexByte(v, '='); i >= 0 {
					v = v[:i]
				}
				vals = append(vals, v)
			}
		}
		autopurge := strings.Join(vals, "\n")
		if autopurge == "" {
			fmt.Println("[INFO] autopurge_trash not set. Skipping cleanup.")
			return 0
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(autopurge), 10, 64)
		p.maxAge = n * 86400
		p.now = time.Now().Unix()
	}

	var homes []string
	if targetUser != "" {
		home := "/home/" + targetUser
		if st, err := os.Stat(home); err != nil || !st.IsDir() {
			fmt.Println("[ERROR] User home directory does not exist: " + home)
			return 1
		}
		homes = append(homes, home)
	} else {
		all, _ := filepath.Glob("/home/*")
		for _, h := range all {
			if st, err := os.Stat(h); err == nil && st.IsDir() {
				homes = append(homes, h)
			}
		}
	}

	var wg sync.WaitGroup
	for _, h := range homes {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			p.userTrash(h)
		}(h)
	}
	wg.Wait()

	var totalFreed int64
	fmt.Println("")
	fmt.Println("🧹 Trash Cleanup Report:")
	files := purgeStatFiles(p.tmpDir)
	if len(files) == 0 {
		files = []string{"*"}
	}
	for _, name := range files {
		bytesStr := ""
		if b, err := os.ReadFile(p.tmpDir + "/" + name); err == nil {
			bytesStr = strings.TrimRight(string(b), "\n")
		} else {
			fmt.Fprintf(os.Stderr, "cat: '%s/%s': No such file or directory\n", p.tmpDir, name)
		}
		n, _ := strconv.ParseInt(bytesStr, 10, 64)
		totalFreed += n
		hr, _ := sh.C("numfmt", "--to=iec", bytesStr).Out()
		hr += "B"
		if p.dry {
			fmt.Printf("  - %s: %s (would be) freed\n", name, hr)
		} else {
			fmt.Printf("  - %s: %s freed\n", name, hr)
		}
	}

	hrTotal, _ := sh.C("numfmt", "--to=iec", strconv.FormatInt(totalFreed, 10)).Out()
	hrTotal += "B"
	fmt.Println("----------------------------------------")
	if p.dry {
		fmt.Println("🔍 Total that *would* be freed: " + hrTotal)
	} else {
		fmt.Println("✅ Total freed across all users: " + hrTotal)
	}

	os.RemoveAll(p.tmpDir)
	return 0
}

// purgeStatFiles is the "$TMP_DIR"/* glob, no dotfiles
func purgeStatFiles(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func purgeLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// purgeDu is du -sb | cut -f1
func purgeDu(path string) string {
	out := sh.C("du", "-sb", path).NoErr().Str()
	if i := strings.IndexByte(out, '\t'); i >= 0 {
		out = out[:i]
	}
	return out
}

func purgeTouch(path string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o666)
	if err != nil {
		return
	}
	f.Close()
	now := time.Now()
	os.Chtimes(path, now, now)
}

func (p *purgeCtx) userTrash(userHome string) {
	userName := filepath.Base(userHome)
	trashDir := userHome + "/.local/share/Trash"
	restore := trashDir + "/.trash_restore"
	var freed int64

	if st, err := os.Stat(trashDir); err != nil || !st.IsDir() {
		return
	}
	if st, err := os.Stat(restore); err != nil || !st.Mode().IsRegular() {
		purgeTouch(restore)
	}

	if p.force {
		fr := purgeDu(trashDir)
		if p.dry {
			fmt.Println("[DRY-RUN] Would purge ALL trash for user: " + userName)
		} else {
			ents, _ := os.ReadDir(trashDir)
			for _, e := range ents {
				os.RemoveAll(trashDir + "/" + e.Name())
			}
			purgeTouch(restore)
		}
		os.WriteFile(p.tmpDir+"/"+userName, []byte(fr+"\n"), 0o666&^calcUmask())
		return
	}

	// read stops at a last line without newline, so that one is skipped
	data, _ := os.ReadFile(restore)
	text := string(data)
	for {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			break
		}
		line := text[:i]
		text = text[i+1:]
		if line == "" {
			continue
		}

		fileName := line
		if j := strings.IndexByte(fileName, '='); j >= 0 {
			fileName = fileName[:j]
		}
		deletionDate := line
		if j := strings.Index(line, "deletion_date="); j >= 0 {
			deletionDate = line[j+len("deletion_date="):]
		}
		epochStr, err := sh.C("date", "-d", deletionDate, "+%s").NoErr().Out()
		if err != nil {
			epochStr = "0"
		}
		epoch, _ := strconv.ParseInt(epochStr, 10, 64)
		if epoch == 0 {
			continue
		}

		if p.now-epoch > p.maxAge {
			trashFile := trashDir + "/" + fileName
			var size int64
			if _, err := os.Stat(trashFile); err == nil {
				size, _ = strconv.ParseInt(purgeDu(trashFile), 10, 64)
			}
			freed += size

			if p.dry {
				fmt.Printf("[DRY-RUN] Would delete: %s (user: %s)\n", trashFile, userName)
			} else {
				os.RemoveAll(trashFile)
				// grep -Fxv of this line, then mv over the restore file
				var keep strings.Builder
				for _, l := range purgeLines(restore) {
					if l != line {
						keep.WriteString(l + "\n")
					}
				}
				tmp := restore + ".tmp"
				os.WriteFile(tmp, []byte(keep.String()), 0o666&^calcUmask())
				os.Rename(tmp, restore)
			}
		}
	}

	os.WriteFile(p.tmpDir+"/"+userName, []byte(strconv.FormatInt(freed, 10)+"\n"), 0o666&^calcUmask())
}
