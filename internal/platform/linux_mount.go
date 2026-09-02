//go:build linux

package platform

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type mountEntry struct {
	mountPoint string
	fsType     string
}

// mountTable resolves a path to the fstype of its longest-prefix mount.
type mountTable struct {
	entries []mountEntry // sorted by mountPoint length, longest first
}

func loadMountTable() *mountTable {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return &mountTable{}
	}
	defer f.Close()
	return parseMountInfo(f)
}

// parseMountInfo reads /proc/self/mountinfo. Field layout:
//
//	36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
//	(0)(1) (2)  (3)   (4)    (5)        (6...)  (sep) (fstype) (source) (super opts)
func parseMountInfo(r io.Reader) *mountTable {
	t := &mountTable{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		sep := -1
		for i, f := range fields {
			if f == "-" {
				sep = i
				break
			}
		}
		if sep < 5 || sep+1 >= len(fields) {
			continue
		}
		t.entries = append(t.entries, mountEntry{
			mountPoint: unescapeMount(fields[4]),
			fsType:     fields[sep+1],
		})
	}
	sort.SliceStable(t.entries, func(i, j int) bool {
		return len(t.entries[i].mountPoint) > len(t.entries[j].mountPoint)
	})
	return t
}

// unescapeMount decodes the octal escapes mountinfo uses for space, tab, newline and backslash.
func unescapeMount(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			v := 0
			ok := true
			for j := 1; j <= 3; j++ {
				c := s[i+j]
				if c < '0' || c > '7' {
					ok = false
					break
				}
				v = v*8 + int(c-'0')
			}
			if ok {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (t *mountTable) fsTypeFor(path string) string {
	if t == nil || len(t.entries) == 0 {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	for _, e := range t.entries {
		if e.mountPoint == "/" || abs == e.mountPoint || strings.HasPrefix(abs, e.mountPoint+"/") {
			return e.fsType
		}
	}
	return ""
}
