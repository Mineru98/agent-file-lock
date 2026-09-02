package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseYAMLExample(t *testing.T) {
	src := `
---
# afl.yaml
version: 1
level: strong            # default level
follow_symlinks: false
exclude:
  - "**/*.tmp"
  - '**/.DS_Store'
paths:
  - docs/POLICY.md                  # string entry
  - path: docs/specs                # object entry
    recursive: true
    include_dirs: false
  - path: README.md
    level: user
  - path: "with # hash.md"
`
	got, err := ParseYAML(src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"version":         1,
		"level":           "strong",
		"follow_symlinks": false,
		"exclude":         []any{"**/*.tmp", "**/.DS_Store"},
		"paths": []any{
			"docs/POLICY.md",
			map[string]any{"path": "docs/specs", "recursive": true, "include_dirs": false},
			map[string]any{"path": "README.md", "level": "user"},
			map[string]any{"path": "with # hash.md"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestParseYAMLNested(t *testing.T) {
	src := `
a:
  b:
    c: 1
    d: null
  e:
    - x
    -
      f: 2
    - g: 3
      h: 'it''s'
empty:
`
	got, err := ParseYAML(src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"a": map[string]any{
			"b": map[string]any{"c": 1, "d": nil},
			"e": []any{"x", map[string]any{"f": 2}, map[string]any{"g": 3, "h": "it's"}},
		},
		"empty": nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestParseYAMLRejects(t *testing.T) {
	cases := map[string]string{
		"anchor":       "a: &x 1",
		"alias":        "a: *x",
		"flow map":     "a: {b: 1}",
		"flow seq":     "a: [1, 2]",
		"literal":      "a: |\n  text",
		"folded":       "a: >\n  text",
		"tag":          "a: !!str 1",
		"multi doc":    "---\na: 1\n---\nb: 2",
		"tab":          "a:\n\tb: 1",
		"dup key":      "a: 1\na: 2",
		"bad indent":   "a:\n  b: 1\n c: 2",
		"seq in map":   "a: 1\n- b",
		"unterminated": `a: "oops`,
		"directive":    "%YAML 1.2\na: 1",
	}
	for name, src := range cases {
		if _, err := ParseYAML(src); err == nil {
			t.Errorf("%s: expected error for %q", name, src)
		} else if !strings.Contains(err.Error(), "yaml line") {
			t.Errorf("%s: error lacks line number: %v", name, err)
		}
	}
}

func TestParseYAMLEmpty(t *testing.T) {
	got, err := ParseYAML("# only comments\n\n")
	if err != nil || len(got.(map[string]any)) != 0 {
		t.Errorf("got %v %v", got, err)
	}
}

func TestParseYAMLReviewCases(t *testing.T) {
	// same-indent sequence under a key (idiomatic)
	got, err := ParseYAML("version: 1\npaths:\n- docs/a.md\n- path: b.md\n  level: user\nlevel: strong\n")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"version": 1, "level": "strong", "paths": []any{"docs/a.md", map[string]any{"path": "b.md", "level": "user"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("same-indent seq: %#v", got)
	}
	// apostrophe inside a plain scalar keeps comment stripping working
	got, err = ParseYAML("paths:\n  - rock 'n roll.md # x\n")
	if err != nil || got.(map[string]any)["paths"].([]any)[0] != "rock 'n roll.md" {
		t.Errorf("apostrophe: %#v %v", got, err)
	}
	// escaped backslash at the end of a double-quoted string
	got, err = ParseYAML(`a: "x\\"` + "\n")
	if err != nil || got.(map[string]any)["a"] != `x\` {
		t.Errorf("trailing escaped backslash: %#v %v", got, err)
	}
	// URL-like plain scalars are fine
	if _, err := ParseYAML("a: http://x/y\n"); err != nil {
		t.Errorf("url: %v", err)
	}
	for name, src := range map[string]string{
		"missing space": "paths:\n  - path:docs/a.md\n",
		"nested seq":    "paths:\n  - - x\n",
	} {
		if _, err := ParseYAML(src); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}
