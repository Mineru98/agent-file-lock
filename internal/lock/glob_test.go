package lock

import "testing"

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pat, rel string
		want     bool
	}{
		{"*.tmp", "a.tmp", true},
		{"*.tmp", "x/y/a.tmp", true},
		{".DS_Store", "docs/.DS_Store", true},
		{"**/*.tmp", "a.tmp", true},
		{"**/*.tmp", "x/y/a.tmp", true},
		{"**/*.tmp", "x/y/a.md", false},
		{"docs/**", "docs/a.md", true},
		{"docs/**", "docs/x/y.md", true},
		{"docs/**", "other/a.md", false},
		{"docs/*.md", "docs/a.md", true},
		{"docs/*.md", "docs/x/a.md", false},
		{"docs/**/*.md", "docs/a.md", true},
		{"docs/**/*.md", "docs/x/y/a.md", true},
		{"build", "src/build", true},
		{"./docs/a.md", "docs/a.md", true},
		{"a/b", "a/b/c", false},
	}
	for _, c := range cases {
		if got := MatchGlob(c.pat, c.rel); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pat, c.rel, got, c.want)
		}
	}
	if ValidateGlob("[bad") == nil {
		t.Error("expected error for malformed pattern")
	}
	if ValidateGlob("**/*.md") != nil {
		t.Error("valid pattern rejected")
	}
}
