// ##############################################################################
// Script Name: internal/tenantfs/tenantfs.go
// Description: Root file operations inside a user's volume that refuse symlinks pointing outside of it.
// Usage: imported by command packages
// Author: Stefan Pejcic
// Created: 21.10.2026
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

package tenantfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"opencli/internal/sh"
)

// Volume is the data dir of a user's podman volume, e.g. Volume(ctx, "html_data")
func Volume(ctx, name string) string {
	return "/home/" + ctx + "/docker-data/volumes/" + ctx + "_" + name + "/_data"
}

var errOutside = errors.New("path is outside of the tenant root")

// Resolve returns the physical path of dir when it's still under root, like tenant_cd
func Resolve(root, dir string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	abs := dir
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, dir)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return "", errors.New("not a directory")
	}
	if !strings.HasPrefix(real+"/", realRoot+"/") {
		return "", errOutside
	}
	return real, nil
}

// open pins root and returns dir relative to it, the os.Root refuses anything that escapes
func open(root, dir string) (*os.Root, string, error) {
	real, err := Resolve(root, dir)
	if err != nil {
		return nil, "", err
	}
	r, err := os.OpenRoot(real)
	if err != nil {
		return nil, "", err
	}
	return r, ".", nil
}

func owner(spec string) (int, int, error) {
	u, g, _ := strings.Cut(spec, ":")
	uid, err := strconv.Atoi(u)
	if err != nil {
		return 0, 0, err
	}
	gid := -1
	if g != "" {
		if gid, err = strconv.Atoi(g); err != nil {
			return 0, 0, err
		}
	}
	return uid, gid, nil
}

// ChownR is chown -R -h inside root, links get their own owner changed and are never followed
func ChownR(root, dir, spec string) error {
	uid, gid, err := owner(spec)
	if err != nil {
		return err
	}
	r, _, err := open(root, dir)
	if err != nil {
		return err
	}
	defer r.Close()
	var first error
	fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if first == nil {
				first = werr
			}
			return nil
		}
		if err := r.Lchown(p, uid, gid); err != nil && first == nil {
			first = err
		}
		return nil
	})
	return first
}

// Chown changes just the dir itself
func Chown(root, dir, spec string) error {
	uid, gid, err := owner(spec)
	if err != nil {
		return err
	}
	r, _, err := open(root, dir)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.Lchown(".", uid, gid)
}

// ChmodR is chmod -R inside root, symlinks are skipped like chmod's own walk does
func ChmodR(root, dir, mode string) error {
	r, _, err := open(root, dir)
	if err != nil {
		return err
	}
	defer r.Close()
	var first error
	fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if first == nil {
				first = werr
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		st, err := r.Lstat(p)
		if err != nil {
			return nil
		}
		m, err := ApplyMode(st.Mode(), mode)
		if err == nil {
			err = r.Chmod(p, m)
		}
		if err != nil && first == nil {
			first = err
		}
		return nil
	})
	return first
}

// ApplyMode turns an octal ("2775") or simple symbolic ("g+w", "u+rwX") chmod mode into a FileMode
func ApplyMode(cur fs.FileMode, mode string) (fs.FileMode, error) {
	perm := uint32(cur.Perm())
	if cur&fs.ModeSetuid != 0 {
		perm |= 0o4000
	}
	if cur&fs.ModeSetgid != 0 {
		perm |= 0o2000
	}
	if cur&fs.ModeSticky != 0 {
		perm |= 0o1000
	}
	if n, err := strconv.ParseUint(mode, 8, 32); err == nil {
		perm = uint32(n)
		// GNU chmod keeps setgid on dirs unless the octal mode has 5 digits
		if cur.IsDir() && len(mode) < 5 && cur&fs.ModeSetgid != 0 && n&0o2000 == 0 {
			perm |= 0o2000
		}
	} else {
		for _, clause := range strings.Split(mode, ",") {
			i := strings.IndexAny(clause, "+-=")
			if i < 0 {
				return cur, errors.New("bad mode " + mode)
			}
			who, op, what := clause[:i], clause[i], clause[i+1:]
			if who == "" || who == "a" {
				who = "ugo"
			}
			var bits uint32
			for _, c := range what {
				switch c {
				case 'r':
					bits |= 0o444
				case 'w':
					bits |= 0o222
				case 'x':
					bits |= 0o111
				case 'X':
					if cur.IsDir() || perm&0o111 != 0 {
						bits |= 0o111
					}
				case 's':
					bits |= 0o6000
				case 't':
					bits |= 0o1000
				}
			}
			var mask uint32
			for _, w := range who {
				switch w {
				case 'u':
					mask |= 0o4700
				case 'g':
					mask |= 0o2070
				case 'o':
					mask |= 0o1007
				}
			}
			bits &= mask
			switch op {
			case '+':
				perm |= bits
			case '-':
				perm &^= bits
			case '=':
				perm = perm&^(mask&0o777) | bits
			}
		}
	}
	m := fs.FileMode(perm & 0o777)
	if perm&0o4000 != 0 {
		m |= fs.ModeSetuid
	}
	if perm&0o2000 != 0 {
		m |= fs.ModeSetgid
	}
	if perm&0o1000 != 0 {
		m |= fs.ModeSticky
	}
	return m, nil
}

// Mkdir is mkdir -p under root one component at a time, a symlink on the way is refused
func Mkdir(root, dir string) error {
	if dir != root && !strings.HasPrefix(dir, root+"/") {
		return errOutside
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(dir, root), "/")
	r, _, err := open(root, root)
	if err != nil {
		return err
	}
	defer r.Close()
	cur := "."
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return errOutside
		}
		next := cur + "/" + part
		st, err := r.Lstat(next)
		switch {
		case err == nil && st.Mode()&fs.ModeSymlink != 0:
			return errors.New("symlink in path")
		case err == nil && !st.IsDir():
			return errors.New("not a directory")
		case err != nil:
			if err := r.Mkdir(next, 0o777&^umask()); err != nil {
				return err
			}
		}
		cur = next
	}
	return nil
}

func umask() fs.FileMode {
	m := syscall.Umask(0)
	syscall.Umask(m)
	return fs.FileMode(m)
}

func split(root, file string) (*os.Root, string, error) {
	r, _, err := open(root, filepath.Dir(file))
	if err != nil {
		return nil, "", err
	}
	return r, filepath.Base(file), nil
}

// Read returns a regular file under root, refusing a symlink at the file itself
func Read(root, file string) ([]byte, error) {
	r, base, err := split(root, file)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	st, err := r.Lstat(base)
	if err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	f, err := r.OpenFile(base, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// Write puts content into file under root via a temp file and a rename, a symlink there gets replaced
func Write(root, file string, mode fs.FileMode, content []byte) error {
	r, base, err := split(root, file)
	if err != nil {
		return err
	}
	defer r.Close()
	return replace(r, base, content, mode, nil)
}

func replace(r *os.Root, base string, content []byte, mode fs.FileMode, ref fs.FileInfo) error {
	tmp := "." + base + "." + strconv.Itoa(os.Getpid()) + randSuffix()
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(content)
	f.Close()
	if werr == nil {
		if ref != nil {
			r.Chmod(tmp, ref.Mode())
			if st, ok := ref.Sys().(*syscall.Stat_t); ok {
				r.Lchown(tmp, int(st.Uid), int(st.Gid))
			}
		} else {
			werr = r.Chmod(tmp, mode)
		}
	}
	if werr == nil {
		werr = r.Rename(tmp, base)
	}
	if werr != nil {
		r.Remove(tmp)
	}
	return werr
}

func randSuffix() string {
	b := make([]byte, 6)
	f, err := os.Open("/dev/urandom")
	if err == nil {
		f.Read(b)
		f.Close()
	}
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

// Edit rewrites a regular file under root through fn, keeping its mode and owner
func Edit(root, file string, fn func([]byte) ([]byte, error)) error {
	r, base, err := split(root, file)
	if err != nil {
		return err
	}
	defer r.Close()
	st, err := r.Lstat(base)
	if err != nil || !st.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	f, err := r.OpenFile(base, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return err
	}
	out, err := fn(data)
	if err != nil {
		return err
	}
	return replace(r, base, out, 0, st)
}

// SedI runs sed over a file under root without reading through a symlink, like tenant_sed_i
func SedI(root, file string, sedArgs ...string) error {
	return Edit(root, file, func(in []byte) ([]byte, error) {
		out, err := sh.C("sed", sedArgs...).Stdin(string(in)).Raw()
		return []byte(out), err
	})
}

// Mv moves a regular file under root into dstdir under root, creating dstdir first
func Mv(root, src, dstdir string) error {
	if err := Mkdir(root, dstdir); err != nil {
		return err
	}
	dst, err := Resolve(root, dstdir)
	if err != nil {
		return err
	}
	srcDir, err := Resolve(root, filepath.Dir(src))
	if err != nil {
		return err
	}
	base := filepath.Base(src)
	st, err := os.Lstat(filepath.Join(srcDir, base))
	if err != nil || !st.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	if err := os.Rename(filepath.Join(srcDir, base), filepath.Join(dst, base)); err != nil {
		return sh.Quiet("mv", "-T", "--", filepath.Join(srcDir, base), filepath.Join(dst, base))
	}
	return nil
}
