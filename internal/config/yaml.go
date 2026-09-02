package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseYAML parses the small YAML subset afl.yaml uses:
//
//   - `# comments`, blank lines, one leading `---`
//   - `key: value` mappings, nested by indentation (spaces only)
//   - `- item` sequences, including `- key: value` mappings inside sequences
//   - "double" and 'single' quoted scalars, plain scalars, ints, bools, null
//
// Anchors/aliases, flow collections ({}, []), block scalars (|, >), tags (!!)
// and multi-document streams are rejected with a line-numbered error so the
// user can switch to the equivalent JSON file instead of getting a silent
// misparse.
func ParseYAML(src string) (any, error) {
	lines, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return map[string]any{}, nil
	}
	p := &yamlParser{lines: lines}
	v, err := p.parseNode(lines[0].indent)
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.lines) {
		return nil, p.errorf(p.lines[p.pos], "unexpected content")
	}
	return v, nil
}

type yamlLine struct {
	num    int
	indent int
	text   string // trimmed, comment stripped
}

func tokenize(src string) ([]yamlLine, error) {
	var out []yamlLine
	seenDoc := false
	for i, raw := range strings.Split(src, "\n") {
		num := i + 1
		raw = strings.TrimRight(raw, " \t\r")
		if raw == "" {
			continue
		}
		if raw == "---" {
			if seenDoc || len(out) > 0 {
				return nil, fmt.Errorf("yaml line %d: multi-document streams are not supported", num)
			}
			seenDoc = true
			continue
		}
		if raw == "..." {
			continue
		}
		indent := 0
		for indent < len(raw) && raw[indent] == ' ' {
			indent++
		}
		if indent < len(raw) && raw[indent] == '\t' {
			return nil, fmt.Errorf("yaml line %d: tabs are not allowed for indentation", num)
		}
		text := stripComment(raw[indent:])
		if strings.TrimSpace(text) == "" {
			continue
		}
		if text == "%YAML" || strings.HasPrefix(text, "%") {
			return nil, fmt.Errorf("yaml line %d: directives are not supported", num)
		}
		out = append(out, yamlLine{num: num, indent: indent, text: strings.TrimRight(text, " ")})
	}
	return out, nil
}

// stripComment removes a trailing `# ...` that is not inside quotes. A `#`
// must be preceded by whitespace (or start the line) to count as a comment.
func stripComment(s string) string {
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				if q == '\'' && i+1 < len(s) && s[i+1] == '\'' {
					i++ // escaped ''
					continue
				}
				if q == '"' && i > 0 && s[i-1] == '\\' {
					continue
				}
				q = 0
			}
		case c == '"' || c == '\'':
			if i == 0 || s[i-1] == ' ' || s[i-1] == ':' || s[i-1] == '-' {
				q = c
			}
		case c == '#' && (i == 0 || s[i-1] == ' '):
			return s[:i]
		}
	}
	return s
}

type yamlParser struct {
	lines []yamlLine
	pos   int
}

func (p *yamlParser) errorf(l yamlLine, format string, a ...any) error {
	return fmt.Errorf("yaml line %d: %s", l.num, fmt.Sprintf(format, a...))
}

// parseNode parses the block starting at the current line, which must be
// indented exactly `indent`.
func (p *yamlParser) parseNode(indent int) (any, error) {
	l := p.lines[p.pos]
	if strings.HasPrefix(l.text, "- ") || l.text == "-" {
		return p.parseSeq(indent)
	}
	if isMappingLine(l.text) {
		return p.parseMap(indent)
	}
	return nil, p.errorf(l, "expected `key: value` or `- item`")
}

func (p *yamlParser) parseSeq(indent int) ([]any, error) {
	seq := []any{}
	for p.pos < len(p.lines) {
		l := p.lines[p.pos]
		if l.indent < indent {
			break
		}
		if l.indent > indent {
			return nil, p.errorf(l, "unexpected indentation")
		}
		if !(strings.HasPrefix(l.text, "- ") || l.text == "-") {
			return nil, p.errorf(l, "expected `- item` in sequence")
		}
		rest := strings.TrimSpace(strings.TrimPrefix(l.text, "-"))
		p.pos++
		switch {
		case rest == "":
			// nested block on following lines
			if p.pos >= len(p.lines) || p.lines[p.pos].indent <= indent {
				seq = append(seq, nil)
				continue
			}
			v, err := p.parseNode(p.lines[p.pos].indent)
			if err != nil {
				return nil, err
			}
			seq = append(seq, v)
		case isMappingLine(rest):
			// `- key: value` starts an inline mapping whose further keys are
			// indented to the column after "- ".
			childIndent := indent + 2
			// Rewrite the current line as a mapping line at childIndent and re-parse.
			p.pos--
			p.lines[p.pos] = yamlLine{num: l.num, indent: childIndent, text: rest}
			v, err := p.parseMap(childIndent)
			if err != nil {
				return nil, err
			}
			seq = append(seq, v)
		default:
			v, err := p.scalar(l, rest)
			if err != nil {
				return nil, err
			}
			seq = append(seq, v)
		}
	}
	return seq, nil
}

func (p *yamlParser) parseMap(indent int) (map[string]any, error) {
	m := map[string]any{}
	for p.pos < len(p.lines) {
		l := p.lines[p.pos]
		if l.indent < indent {
			break
		}
		if l.indent > indent {
			return nil, p.errorf(l, "unexpected indentation")
		}
		if strings.HasPrefix(l.text, "- ") {
			return nil, p.errorf(l, "sequence item where a `key: value` was expected")
		}
		key, rest, ok := splitKey(l.text)
		if !ok {
			return nil, p.errorf(l, "expected `key: value`")
		}
		if _, dup := m[key]; dup {
			return nil, p.errorf(l, "duplicate key %q", key)
		}
		p.pos++
		if rest == "" {
			if p.pos >= len(p.lines) || p.lines[p.pos].indent <= indent {
				m[key] = nil
				continue
			}
			v, err := p.parseNode(p.lines[p.pos].indent)
			if err != nil {
				return nil, err
			}
			m[key] = v
			continue
		}
		v, err := p.scalar(l, rest)
		if err != nil {
			return nil, err
		}
		m[key] = v
	}
	return m, nil
}

func isMappingLine(s string) bool {
	_, _, ok := splitKey(s)
	return ok
}

// splitKey splits `key: value` / `key:`; the key may be quoted.
func splitKey(s string) (key, rest string, ok bool) {
	if s == "" {
		return "", "", false
	}
	if s[0] == '"' || s[0] == '\'' {
		end := closingQuote(s)
		if end < 0 || end+1 >= len(s) || s[end+1] != ':' {
			return "", "", false
		}
		if end+2 < len(s) && s[end+2] != ' ' {
			return "", "", false
		}
		k, err := unquote(s[:end+1])
		if err != nil {
			return "", "", false
		}
		return k, strings.TrimSpace(s[end+2:]), true
	}
	for i := 0; i < len(s); i++ {
		if s[i] == ':' && (i+1 == len(s) || s[i+1] == ' ') {
			key = strings.TrimSpace(s[:i])
			if key == "" || strings.ContainsAny(key, "{}[]&*!|>'\"%@`") {
				return "", "", false
			}
			return key, strings.TrimSpace(s[i+1:]), true
		}
	}
	return "", "", false
}

func closingQuote(s string) int {
	q := s[0]
	for i := 1; i < len(s); i++ {
		if s[i] == q {
			if q == '\'' && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			if q == '"' && s[i-1] == '\\' {
				continue
			}
			return i
		}
	}
	return -1
}

func (p *yamlParser) scalar(l yamlLine, s string) (any, error) {
	switch s[0] {
	case '&', '*':
		return nil, p.errorf(l, "anchors and aliases are not supported")
	case '{', '[':
		return nil, p.errorf(l, "flow collections ({...}, [...]) are not supported; use block style")
	case '|', '>':
		return nil, p.errorf(l, "block scalars (|, >) are not supported")
	case '!':
		return nil, p.errorf(l, "tags are not supported")
	case '"', '\'':
		if closingQuote(s) != len(s)-1 {
			return nil, p.errorf(l, "unterminated or trailing content after quoted string")
		}
		v, err := unquote(s)
		if err != nil {
			return nil, p.errorf(l, "%v", err)
		}
		return v, nil
	}
	switch s {
	case "true", "True", "TRUE", "yes", "on":
		return true, nil
	case "false", "False", "FALSE", "no", "off":
		return false, nil
	case "null", "Null", "NULL", "~":
		return nil, nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n, nil
	}
	return s, nil
}

func unquote(s string) (string, error) {
	if s[0] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), nil
	}
	v, err := strconv.Unquote(s)
	if err != nil {
		return "", fmt.Errorf("bad escape in %s", s)
	}
	return v, nil
}
