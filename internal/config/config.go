// Package config loads afl.yaml / afl.json protection lists.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// Config is the on-disk schema (version 1).
type Config struct {
	Version        int         `json:"version"`
	Level          string      `json:"level,omitempty"`
	FollowSymlinks bool        `json:"follow_symlinks,omitempty"`
	Exclude        []string    `json:"exclude,omitempty"`
	Paths          []PathEntry `json:"paths"`

	// BaseDir is the directory of the config file; relative paths resolve
	// against it. Not part of the file.
	BaseDir string `json:"-"`
}

// PathEntry is one protected path. In YAML/JSON it may be a bare string.
type PathEntry struct {
	Path        string   `json:"path"`
	Recursive   bool     `json:"recursive,omitempty"`
	IncludeDirs bool     `json:"include_dirs,omitempty"`
	DirOnly     bool     `json:"dir_only,omitempty"`
	Level       string   `json:"level,omitempty"`
	Exclude     []string `json:"exclude,omitempty"`
}

// UnmarshalJSON accepts either "docs/x.md" or {"path": ...}.
func (e *PathEntry) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &e.Path)
	}
	type raw PathEntry
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode((*raw)(e))
}

// Load reads and validates a config file, choosing the parser by extension.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	case ".yaml", ".yml":
		node, err := ParseYAML(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := decodeConfig(node, &cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("%s: unsupported config extension (use .yaml, .yml or .json)", path)
	}
	cfg.BaseDir = filepath.Dir(abs)
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Version != 1 {
		return fmt.Errorf("version must be 1 (got %d)", c.Version)
	}
	if c.Level == "" {
		c.Level = "strong"
	}
	if _, err := platform.ParseLevel(c.Level); err != nil {
		return err
	}
	if len(c.Paths) == 0 {
		return fmt.Errorf("paths must list at least one entry")
	}
	for _, g := range c.Exclude {
		if err := lock.ValidateGlob(g); err != nil {
			return fmt.Errorf("exclude %q: %w", g, err)
		}
	}
	for i := range c.Paths {
		e := &c.Paths[i]
		if e.Path == "" {
			return fmt.Errorf("paths[%d]: path is required", i)
		}
		if e.Level != "" {
			if _, err := platform.ParseLevel(e.Level); err != nil {
				return fmt.Errorf("paths[%d]: %w", i, err)
			}
		}
		if e.DirOnly && e.Recursive {
			return fmt.Errorf("paths[%d]: dir_only and recursive are mutually exclusive", i)
		}
		for _, g := range e.Exclude {
			if err := lock.ValidateGlob(g); err != nil {
				return fmt.Errorf("paths[%d] exclude %q: %w", i, g, err)
			}
		}
	}
	return nil
}

// Entry is a resolved path entry ready for lock.Plan.
type Entry struct {
	Path string
	Opts lock.PlanOptions
}

// Entries resolves relative paths against BaseDir and merges defaults.
// levelOverride (0 = none) forces one level for every entry (CLI --level).
func (c *Config) Entries(levelOverride platform.Level) []Entry {
	out := make([]Entry, 0, len(c.Paths))
	def, _ := platform.ParseLevel(c.Level)
	for _, e := range c.Paths {
		lvl := def
		if e.Level != "" {
			lvl, _ = platform.ParseLevel(e.Level)
		}
		if levelOverride != 0 {
			lvl = levelOverride
		}
		p := e.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(c.BaseDir, p)
		}
		excl := append(append([]string{}, c.Exclude...), e.Exclude...)
		out = append(out, Entry{Path: p, Opts: lock.PlanOptions{
			Recursive:      e.Recursive,
			IncludeDirs:    e.IncludeDirs,
			DirOnly:        e.DirOnly,
			FollowSymlinks: c.FollowSymlinks,
			Exclude:        excl,
			Level:          lvl,
		}})
	}
	return out
}

// decodeConfig maps the generic YAML tree onto Config, rejecting unknown keys
// and wrong types with a path-qualified error.
func decodeConfig(node any, cfg *Config) error {
	m, ok := node.(map[string]any)
	if !ok {
		return fmt.Errorf("top level must be a mapping")
	}
	for _, k := range sortedKeys(m) {
		v := m[k]
		var err error
		switch k {
		case "version":
			cfg.Version, err = asInt(k, v)
		case "level":
			cfg.Level, err = asString(k, v)
		case "follow_symlinks":
			cfg.FollowSymlinks, err = asBool(k, v)
		case "exclude":
			cfg.Exclude, err = asStringList(k, v)
		case "paths":
			cfg.Paths, err = asPaths(v)
		default:
			return fmt.Errorf("unknown key %q", k)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func asPaths(v any) ([]PathEntry, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("paths: must be a list")
	}
	out := make([]PathEntry, 0, len(list))
	for i, item := range list {
		var e PathEntry
		switch t := item.(type) {
		case string:
			e.Path = t
		case map[string]any:
			for _, k := range sortedKeys(t) {
				val := t[k]
				where := fmt.Sprintf("paths[%d].%s", i, k)
				var err error
				switch k {
				case "path":
					e.Path, err = asString(where, val)
				case "recursive":
					e.Recursive, err = asBool(where, val)
				case "include_dirs":
					e.IncludeDirs, err = asBool(where, val)
				case "dir_only":
					e.DirOnly, err = asBool(where, val)
				case "level":
					e.Level, err = asString(where, val)
				case "exclude":
					e.Exclude, err = asStringList(where, val)
				default:
					return nil, fmt.Errorf("paths[%d]: unknown key %q", i, k)
				}
				if err != nil {
					return nil, err
				}
			}
		default:
			return nil, fmt.Errorf("paths[%d]: must be a string or a mapping", i)
		}
		out = append(out, e)
	}
	return out, nil
}

func asInt(k string, v any) (int, error) {
	n, ok := v.(int)
	if !ok {
		return 0, fmt.Errorf("%s: must be an integer", k)
	}
	return n, nil
}

func asString(k string, v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case int:
		return fmt.Sprint(t), nil
	}
	return "", fmt.Errorf("%s: must be a string", k)
}

func asBool(k string, v any) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s: must be true or false", k)
	}
	return b, nil
}

func asStringList(k string, v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: must be a list of strings", k)
	}
	out := make([]string, 0, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d]: must be a string", k, i)
		}
		out = append(out, s)
	}
	return out, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
