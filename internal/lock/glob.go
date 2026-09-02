package lock

import (
	"path/filepath"
	"strings"
)

// MatchGlob matches a slash-separated relative path against a pattern that
// may contain `**` (zero or more path components) in addition to the usual
// filepath.Match syntax applied per component. A pattern without a slash
// matches against the base name at any depth, like .gitignore.
func MatchGlob(pattern, rel string) bool {
	pattern = filepath.ToSlash(pattern)
	rel = filepath.ToSlash(rel)
	pattern = strings.TrimPrefix(pattern, "./")
	rel = strings.TrimPrefix(rel, "./")
	if !strings.Contains(pattern, "/") {
		ok, _ := filepath.Match(pattern, lastComponent(rel))
		return ok
	}
	return matchParts(splitClean(pattern), splitClean(rel))
}

func lastComponent(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func splitClean(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" && s != "." {
			out = append(out, s)
		}
	}
	return out
}

func matchParts(pat, parts []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(parts); i++ {
				if matchParts(pat[1:], parts[i:]) {
					return true
				}
			}
			return false
		}
		if len(parts) == 0 {
			return false
		}
		if ok, err := filepath.Match(pat[0], parts[0]); err != nil || !ok {
			return false
		}
		pat, parts = pat[1:], parts[1:]
	}
	return len(parts) == 0
}

// ValidateGlob returns an error for malformed patterns so config loading can
// fail early instead of silently never matching.
func ValidateGlob(pattern string) error {
	for _, part := range splitClean(filepath.ToSlash(pattern)) {
		if part == "**" {
			continue
		}
		if _, err := filepath.Match(part, ""); err != nil {
			return err
		}
	}
	return nil
}
