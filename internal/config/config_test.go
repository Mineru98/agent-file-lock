package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mineru98/agent-file-lock/internal/platform"
)

const exampleYAML = `version: 1
level: strong
exclude:
  - "**/*.tmp"
paths:
  - docs/POLICY.md
  - path: docs/specs
    recursive: true
    exclude:
      - drafts/**
  - path: README.md
    level: user
  - path: /abs/file.md
`

const exampleJSON = `{
  "version": 1,
  "level": "strong",
  "exclude": ["**/*.tmp"],
  "paths": [
    "docs/POLICY.md",
    {"path": "docs/specs", "recursive": true, "exclude": ["drafts/**"]},
    {"path": "README.md", "level": "user"},
    "/abs/file.md"
  ]
}`

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadYAMLAndJSONAgree(t *testing.T) {
	for _, name := range []string{"afl.yaml", "afl.json"} {
		body := exampleYAML
		if strings.HasSuffix(name, ".json") {
			body = exampleJSON
		}
		p := write(t, name, body)
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		es := cfg.Entries(0)
		if len(es) != 4 {
			t.Fatalf("%s: entries = %d", name, len(es))
		}
		base := filepath.Dir(p)
		if es[0].Path != filepath.Join(base, "docs/POLICY.md") || es[0].Opts.Level != platform.LevelStrong || es[0].Opts.Recursive {
			t.Errorf("%s: entry0 = %+v", name, es[0])
		}
		if !es[1].Opts.Recursive || len(es[1].Opts.Exclude) != 2 || es[1].Opts.Exclude[1] != "drafts/**" {
			t.Errorf("%s: entry1 = %+v", name, es[1])
		}
		if es[2].Opts.Level != platform.LevelUser {
			t.Errorf("%s: entry2 level = %v", name, es[2].Opts.Level)
		}
		if es[3].Path != "/abs/file.md" {
			t.Errorf("%s: absolute path altered: %s", name, es[3].Path)
		}
		if ov := cfg.Entries(platform.LevelUser); ov[0].Opts.Level != platform.LevelUser {
			t.Errorf("%s: level override ignored", name)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]struct{ name, body, want string }{
		"unknown key":    {"a.yaml", "version: 1\nbogus: 1\npaths:\n  - x", "unknown key"},
		"unknown key js": {"a.json", `{"version":1,"bogus":1,"paths":["x"]}`, "bogus"},
		"bad version":    {"a.yaml", "version: 2\npaths:\n  - x", "version"},
		"no paths":       {"a.yaml", "version: 1", "paths"},
		"bad level":      {"a.yaml", "version: 1\nlevel: medium\npaths:\n  - x", "level"},
		"entry level":    {"a.yaml", "version: 1\npaths:\n  - path: x\n    level: nope", "paths[0]"},
		"entry type":     {"a.yaml", "version: 1\npaths:\n  - 42", "paths[0]"},
		"entry key":      {"a.yaml", "version: 1\npaths:\n  - path: x\n    recurse: true", "unknown key"},
		"bool type":      {"a.yaml", "version: 1\npaths:\n  - path: x\n    recursive: yes please", "true or false"},
		"bad glob":       {"a.yaml", "version: 1\nexclude:\n  - '[x'\npaths:\n  - x", "exclude"},
		"dir_only+rec":   {"a.yaml", "version: 1\npaths:\n  - path: x\n    recursive: true\n    dir_only: true", "mutually exclusive"},
		"extension":      {"a.toml", "version = 1", "extension"},
		"yaml syntax":    {"a.yaml", "version: 1\npaths: [x]", "flow"},
	}
	for name, c := range cases {
		_, err := Load(write(t, c.name, c.body))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want containing %q", name, err, c.want)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(write(t, "afl.yml", "version: 1\npaths:\n  - x\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Level != "strong" || cfg.Entries(0)[0].Opts.Level != platform.LevelStrong {
		t.Errorf("default level not applied: %+v", cfg)
	}
}
