//go:build linux && (amd64 || arm64 || riscv64 || s390x || loong64 || 386 || arm)

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mountFixture = `22 28 0:21 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc proc rw
28 0 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw,errors=remount-ro
50 28 0:47 / /mnt/c rw,noatime shared:23 - 9p drvfs rw,dirsync,aname=drvfs;path=C:\;uid=1000
60 28 0:50 / /home/u/with\040space rw - xfs /dev/sdb1 rw
70 28 0:51 / /home rw - btrfs /dev/sdc1 rw
`

func TestParseMountInfo(t *testing.T) {
	tab := parseMountInfo(strings.NewReader(mountFixture))
	if len(tab.entries) != 5 {
		t.Fatalf("entries = %d", len(tab.entries))
	}
	// longest first
	if tab.entries[0].mountPoint != "/home/u/with space" || tab.entries[0].fsType != "xfs" {
		t.Errorf("first entry = %+v", tab.entries[0])
	}
	cases := map[string]string{
		"/mnt/c/Users/x/doc.md":   "9p",
		"/mnt/cc/x":               "ext4",
		"/home/u/with space/a.md": "xfs",
		"/home/u/other.md":        "btrfs",
		"/etc/hosts":              "ext4",
		"/mnt/c":                  "9p",
	}
	for p, want := range cases {
		if got := tab.fsTypeFor(p); got != want {
			t.Errorf("fsTypeFor(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestSupportsUsesMountTable(t *testing.T) {
	l := &linuxLocker{mounts: parseMountInfo(strings.NewReader(mountFixture))}
	if ok, why := l.Supports("/mnt/c/x.md", LevelStrong); ok || !strings.Contains(why, "9p") {
		t.Errorf("9p should be unsupported: %v %q", ok, why)
	}
	if ok, _ := l.Supports("/home/u/x.md", LevelStrong); !ok {
		t.Error("btrfs should be supported")
	}
	if ok, _ := l.Supports("/mnt/c/x.md", LevelUser); !ok {
		t.Error("user level is always supported")
	}
}

func TestHasCapImmutableFrom(t *testing.T) {
	dir := t.TempDir()
	write := func(name, capeff string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("Name:\tx\nCapInh:\t0000000000000000\nCapEff:\t"+capeff+"\nCapBnd:\t0\n"), 0o644)
		return p
	}
	if !hasCapImmutableFrom(write("full", "000001ffffffffff")) {
		t.Error("full cap set should include CAP_LINUX_IMMUTABLE")
	}
	if hasCapImmutableFrom(write("docker", "00000000a80425fb")) {
		t.Error("docker default caps must not include CAP_LINUX_IMMUTABLE")
	}
	if !hasCapImmutableFrom(write("only", "0000000000000200")) {
		t.Error("bit 9 alone should be enough")
	}
}

func TestIoctlConstants(t *testing.T) {
	// _IOR('f', 1, long) / _IOW('f', 2, long): type 'f'=0x66, nr 1/2.
	if fsIocGetFlags&0xffff != 0x6601 || fsIocSetFlags&0xffff != 0x6602 {
		t.Errorf("type/nr bits wrong: %#x %#x", fsIocGetFlags, fsIocSetFlags)
	}
	if fsIocGetFlags>>30 != 2 || fsIocSetFlags>>30 != 1 { // dir: 2=read, 1=write
		t.Errorf("direction bits wrong: %#x %#x", fsIocGetFlags, fsIocSetFlags)
	}
}
