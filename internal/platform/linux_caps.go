//go:build linux

package platform

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

const capLinuxImmutable = 9

// HasCapImmutable reads /proc/self/status and reports whether
// CAP_LINUX_IMMUTABLE is in the effective capability set.
func HasCapImmutable() bool {
	return hasCapImmutableFrom("/proc/self/status")
}

func hasCapImmutableFrom(statusPath string) bool {
	f, err := os.Open(statusPath)
	if err != nil {
		// No procfs: fall back to the classic euid check.
		return IsRoot()
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		hex := strings.TrimSpace(strings.TrimPrefix(line, "CapEff:"))
		v, err := strconv.ParseUint(hex, 16, 64)
		if err != nil {
			return IsRoot()
		}
		return v&(1<<capLinuxImmutable) != 0
	}
	return IsRoot()
}
