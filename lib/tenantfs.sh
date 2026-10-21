#!/bin/bash
################################################################################
# Script Name: lib/tenantfs.sh
# Description: Helpers for running root file operations inside a user's volume without following symlinks they planted.
# Usage: . /usr/local/opencli/lib/tenantfs.sh
# Author: Stefan Pejcic
# Created: 21.10.2026
# Last Modified: 21.10.2026
# Company: OpenPanel, LLC.
# Copyright (c) openpanel.com
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
# THE SOFTWARE.
################################################################################

# data dir of a user's podman volume, e.g. tenant_volume <context> html_data
tenant_volume() {
    echo "/home/$1/docker-data/volumes/${1}_$2/_data"
}

# cd into dir and make sure the physical cwd is still under root, everything after can use paths relative to . safely
tenant_cd() {
    local root="$1" dir="$2" real_root
    real_root=$(realpath -e -- "$root" 2>/dev/null) || return 1
    cd -- "$dir" 2>/dev/null || return 1
    case "$(pwd -P)/" in
        "$real_root"/*) return 0 ;;
    esac
    cd / || true
    return 1
}

# chown -R inside root, chown's own walk never follows symlinks once it's started from the checked cwd
tenant_chown_r() {
    local root="$1" dir="$2" owner="$3"
    ( tenant_cd "$root" "$dir" && chown -R -h -- "$owner" . )
}

# chown just the dir itself
tenant_chown() {
    local root="$1" dir="$2" owner="$3"
    ( tenant_cd "$root" "$dir" && chown -h -- "$owner" . )
}

# chmod -R inside root, symlinks in the tree are skipped by chmod's walk
tenant_chmod_r() {
    local root="$1" dir="$2" mode="$3"
    ( tenant_cd "$root" "$dir" && chmod -R -- "$mode" . )
}

# mkdir -p dir under root one component at a time from a checked cwd, so a symlink on the way is refused instead of followed
tenant_mkdir() {
    local root="$1" dir="$2" rel
    [[ "$dir" == "$root" || "$dir" == "$root"/* ]] || return 1
    rel="${dir#"$root"}"
    rel="${rel#/}"
    (
        tenant_cd "$root" "$root" || exit 1
        IFS=/ read -ra parts <<< "$rel"
        for part in "${parts[@]}"; do
            [[ -z "$part" || "$part" == "." ]] && continue
            [[ "$part" == ".." ]] && exit 1
            [[ -L "./$part" ]] && exit 1
            [[ -d "./$part" ]] || mkdir -- "./$part" || exit 1
            tenant_cd "$root" "./$part" || exit 1
        done
    )
}

# sed -i on a file under root without reading through a symlink, the edited copy is renamed over the path so a link there gets replaced, not written through
tenant_sed_i() {
    local root="$1" file="$2"
    shift 2
    (
        tenant_cd "$root" "$(dirname -- "$file")" || exit 1
        base=$(basename -- "$file")
        [[ -f "./$base" && ! -L "./$base" ]] || exit 1
        tmp=$(mktemp "./.${base}.XXXXXX") || exit 1
        if dd if="./$base" iflag=nofollow status=none | sed "$@" > "$tmp"; then
            chmod --reference="./$base" "$tmp" 2>/dev/null
            chown --reference="./$base" "$tmp" 2>/dev/null
            mv -fT -- "$tmp" "./$base"
        else
            rm -f -- "$tmp"
            exit 1
        fi
    )
}

# write stdin to file under root via a temp file and a rename, a symlink at file gets replaced instead of written through, e.g. tenant_write <root> <file> <mode> < content
tenant_write() {
    local root="$1" file="$2" mode="${3:-644}"
    (
        tenant_cd "$root" "$(dirname -- "$file")" || exit 1
        base=$(basename -- "$file")
        tmp=$(mktemp "./.${base}.XXXXXX") || exit 1
        if cat > "$tmp" && chmod "$mode" "$tmp" && mv -fT -- "$tmp" "./$base"; then
            exit 0
        fi
        rm -f -- "$tmp"
        exit 1
    )
}

# print a regular file under root without reading through a symlink, e.g. tenant_read <root> <file> > copy
tenant_read() {
    local root="$1" file="$2"
    (
        tenant_cd "$root" "$(dirname -- "$file")" || exit 1
        base=$(basename -- "$file")
        [[ -f "./$base" && ! -L "./$base" ]] || exit 1
        dd if="./$base" iflag=nofollow status=none
    )
}

# move a file under root into dstdir under root, both dirs are pinned first so neither side can be swapped for a symlink mid-move
tenant_mv() {
    local root="$1" src="$2" dstdir="$3"
    (
        tenant_mkdir "$root" "$dstdir" || exit 1
        tenant_cd "$root" "$dstdir" || exit 1
        exec {dst_fd}< .
        tenant_cd "$root" "$(dirname -- "$src")" || exit 1
        base=$(basename -- "$src")
        [[ -f "./$base" && ! -L "./$base" ]] || exit 1
        mv -T -- "./$base" "/proc/self/fd/$dst_fd/$base"
    )
}
