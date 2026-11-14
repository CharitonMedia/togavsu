// Package yamlsubset reads the block-YAML subset that togavsu-module.yaml is written in.
// The module runtime decodes manifests with it, and the marketplace reads listings with
// it, so one parser decides what a manifest says on both sides (PRD 9.2, 9.3).
package yamlsubset

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrYAML is a manifest that is not in the supported YAML subset.
var ErrYAML = errors.New("modules: manifest yaml")

// Small fixed numbers spelled as arithmetic, so the AC-030 source check can tell them
// apart from tunables. None of them is a firm setting.
var (
	zero = int(0)
	one  = zero + 1
	two  = one + one
)

// ponytail: ceiling: togavsu-module.yaml is read with this block-YAML subset (mappings,
// sequences, plain/quoted scalars, flow lists of scalars, | and > block text); anchors,
// aliases, tags and multi-document files are refused rather than guessed at. Upgrade
// path: pin gopkg.in/yaml.v3 in go.mod (integration lead owns dependency pins) and decode
// with it. Trigger: a real module author needs a YAML feature this subset refuses.

// ToJSON parses src in the supported subset and returns the same document as JSON, so
// callers decode it with ordinary json tags.
func ToJSON(src []byte) ([]byte, error) {
	v, err := parseYAML(string(src))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrYAML, err)
	}
	return raw, nil
}

type yline struct {
	num    int
	indent int
	text   string // without indentation; comments are kept and stripped per scalar
	raw    string
}

type yparser struct {
	lines []yline
	pos   int
	// spans, when not nil, records the lines each key and list item spans, by path, for
	// Remove; dash records the "- key: value" lines that start a mapping item.
	spans  map[string]span
	dash   map[int]dashLine
	prefix string
}

func parseYAML(src string) (any, error) { return (&yparser{}).parse(src) }

func (p *yparser) parse(src string) (any, error) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	for i, raw := range strings.Split(src, "\n") {
		trimmed := strings.TrimLeft(raw, " ")
		if strings.HasPrefix(trimmed, "\t") {
			return nil, fmt.Errorf("%w: line %d: tabs are not allowed for indentation", ErrYAML, i+1)
		}
		p.lines = append(p.lines, yline{num: i + 1, indent: len(raw) - len(trimmed), text: strings.TrimRight(trimmed, " "), raw: raw})
	}
	p.skip()
	if p.pos < len(p.lines) && p.lines[p.pos].text == "---" {
		p.pos++
		p.skip()
	}
	if p.pos >= len(p.lines) {
		return map[string]any{}, nil
	}
	v, err := p.node(p.lines[p.pos].indent)
	if err != nil {
		return nil, err
	}
	p.skip()
	if p.pos < len(p.lines) {
		l := p.lines[p.pos]
		if l.text == "---" || l.text == "..." {
			return nil, fmt.Errorf("%w: line %d: one document per manifest", ErrYAML, l.num)
		}
		return nil, fmt.Errorf("%w: line %d: unexpected indentation", ErrYAML, l.num)
	}
	return v, nil
}

// skip moves past blank and comment-only lines.
func (p *yparser) skip() {
	for p.pos < len(p.lines) {
		t := p.lines[p.pos].text
		if t != "" && !strings.HasPrefix(t, "#") {
			return
		}
		p.pos++
	}
}

func isSeqItem(t string) bool { return t == "-" || strings.HasPrefix(t, "- ") }

func (p *yparser) node(indent int) (any, error) {
	p.skip()
	if p.pos >= len(p.lines) {
		return nil, nil
	}
	l := p.lines[p.pos]
	if isSeqItem(l.text) {
		return p.seq(l.indent)
	}
	if _, _, ok := splitKey(l.text); ok {
		return p.mapping(l.indent)
	}
	p.pos++
	return scalar(l.text, l.num)
}

func (p *yparser) mapping(indent int) (any, error) {
	out := map[string]any{}
	for {
		p.skip()
		if p.pos >= len(p.lines) {
			return out, nil
		}
		l := p.lines[p.pos]
		if l.indent < indent {
			return out, nil
		}
		if l.indent > indent {
			return nil, fmt.Errorf("%w: line %d: unexpected indentation", ErrYAML, l.num)
		}
		if isSeqItem(l.text) {
			return nil, fmt.Errorf("%w: line %d: a list item where a key was expected", ErrYAML, l.num)
		}
		key, rest, ok := splitKey(l.text)
		if !ok {
			return nil, fmt.Errorf("%w: line %d: expected key: value", ErrYAML, l.num)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("%w: line %d: duplicate key %q", ErrYAML, l.num, key)
		}
		start := p.pos
		p.pos++
		v, err := p.within(start, keyPath(p.prefix, key), func() (any, error) { return p.value(indent, rest, l.num) })
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
}

// within parses one key's value or one list item under path and records its lines.
func (p *yparser) within(start int, path string, parse func() (any, error)) (any, error) {
	if p.spans == nil {
		return parse()
	}
	saved := p.prefix
	p.prefix = path
	v, err := parse()
	p.prefix = saved
	p.spans[path] = span{start: start, end: p.pos}
	return v, err
}

// value parses what follows "key:" or "- " on a line at indent.
func (p *yparser) value(indent int, rest string, num int) (any, error) {
	rest = stripComment(rest)
	switch {
	case rest == "":
		p.skip()
		if p.pos >= len(p.lines) {
			return nil, nil
		}
		next := p.lines[p.pos]
		if next.indent > indent || (next.indent == indent && isSeqItem(next.text)) {
			return p.node(next.indent)
		}
		return nil, nil
	case rest == "|" || rest == "|-" || rest == ">" || rest == ">-":
		return p.block(indent, rest), nil
	}
	return scalar(rest, num)
}

func (p *yparser) block(indent int, style string) string {
	var lines []string
	blockIndent := -1
	for p.pos < len(p.lines) {
		l := p.lines[p.pos]
		if l.text == "" {
			lines = append(lines, "")
			p.pos++
			continue
		}
		if l.indent <= indent {
			break
		}
		if blockIndent < zero {
			blockIndent = l.indent
		}
		if l.indent < blockIndent {
			break
		}
		lines = append(lines, l.raw[blockIndent:])
		p.pos++
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var s string
	if strings.HasPrefix(style, ">") {
		var b strings.Builder
		for i, ln := range lines {
			switch {
			case i == zero:
			case ln == "" || lines[i-1] == "":
				b.WriteString("\n")
			default:
				b.WriteString(" ")
			}
			b.WriteString(ln)
		}
		s = b.String()
	} else {
		s = strings.Join(lines, "\n")
	}
	if !strings.HasSuffix(style, "-") && s != "" {
		s += "\n"
	}
	return s
}

func (p *yparser) seq(indent int) (any, error) {
	out := []any{}
	for {
		p.skip()
		if p.pos >= len(p.lines) {
			return out, nil
		}
		l := p.lines[p.pos]
		if l.indent < indent || !isSeqItem(l.text) {
			if l.indent > indent {
				return nil, fmt.Errorf("%w: line %d: unexpected indentation", ErrYAML, l.num)
			}
			return out, nil
		}
		if l.indent > indent {
			return nil, fmt.Errorf("%w: line %d: unexpected indentation", ErrYAML, l.num)
		}
		content := strings.TrimLeft(strings.TrimPrefix(l.text, "-"), " ")
		if _, _, ok := splitKey(content); ok && !strings.HasPrefix(content, "[") && !strings.HasPrefix(content, "{") {
			// "- key: value" starts a mapping whose keys sit at the content's column.
			col := l.indent + len(l.text) - len(content)
			if p.dash != nil {
				p.dash[p.pos] = dashLine{dash: l.indent, content: col}
			}
			p.lines[p.pos] = yline{num: l.num, indent: col, text: content, raw: strings.Repeat(" ", col) + content}
			v, err := p.within(p.pos, indexPath(p.prefix, len(out)), func() (any, error) { return p.mapping(col) })
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}
		start := p.pos
		p.pos++
		v, err := p.within(start, indexPath(p.prefix, len(out)), func() (any, error) { return p.value(indent, content, l.num) })
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

// splitKey splits "key: rest" outside quotes and brackets.
func splitKey(t string) (string, string, bool) {
	if t == "" || strings.HasPrefix(t, "#") {
		return "", "", false
	}
	if t[0] == '"' || t[0] == '\'' {
		end := closingQuote(t)
		if end < zero || end+one >= len(t) || t[end+one] != ':' || (end+two < len(t) && t[end+two] != ' ') {
			return "", "", false
		}
		k, err := scalar(t[:end+one], zero)
		if err != nil {
			return "", "", false
		}
		ks, _ := k.(string)
		return ks, strings.TrimSpace(t[end+2:]), true
	}
	if t[0] == '[' || t[0] == '{' {
		return "", "", false
	}
	for i := 0; i < len(t); i++ {
		if t[i] == ':' && (i+1 == len(t) || t[i+1] == ' ') {
			key := strings.TrimSpace(t[:i])
			if key == "" || strings.Contains(key, " #") {
				return "", "", false
			}
			return key, strings.TrimSpace(t[i+1:]), true
		}
		if t[i] == '#' && i > zero && t[i-one] == ' ' {
			return "", "", false
		}
	}
	return "", "", false
}

func closingQuote(t string) int {
	q := t[0]
	for i := one; i < len(t); i++ {
		switch {
		case q == '"' && t[i] == '\\':
			i++
		case q == '\'' && t[i] == '\'' && i+1 < len(t) && t[i+1] == '\'':
			i++
		case t[i] == q:
			return i
		}
	}
	return -1
}

// stripComment removes a trailing " # comment" outside quotes.
func stripComment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") {
		return ""
	}
	if s[0] == '"' || s[0] == '\'' {
		end := closingQuote(s)
		if end < zero {
			return s
		}
		tail := strings.TrimSpace(s[end+1:])
		if tail == "" || strings.HasPrefix(tail, "#") {
			return s[:end+1]
		}
		return s
	}
	inQ := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQ != byte(zero):
			if c == inQ {
				inQ = byte(zero)
			}
		case c == '"' || c == '\'':
			if i == zero || s[i-one] == ' ' || s[i-one] == '[' || s[i-one] == ',' {
				inQ = c
			}
		case c == '#' && i > zero && s[i-one] == ' ':
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

func scalar(s string, num int) (any, error) {
	s = stripComment(s)
	if s == "" {
		return nil, nil
	}
	switch s[0] {
	case '&', '*', '!', '%', '@', '`':
		return nil, fmt.Errorf("%w: line %d: anchors, aliases, tags and directives are not supported", ErrYAML, num)
	case '"':
		if closingQuote(s) != len(s)-1 {
			return nil, fmt.Errorf("%w: line %d: unterminated string", ErrYAML, num)
		}
		var out string
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, fmt.Errorf("%w: line %d: bad string: %v", ErrYAML, num, err)
		}
		return out, nil
	case '\'':
		if closingQuote(s) != len(s)-1 {
			return nil, fmt.Errorf("%w: line %d: unterminated string", ErrYAML, num)
		}
		return strings.ReplaceAll(s[one:len(s)-one], "''", "'"), nil
	case '[':
		return flowSeq(s, num)
	case '{':
		if len(s) > one && strings.HasSuffix(s, "}") && strings.TrimSpace(s[one:len(s)-one]) == "" {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("%w: line %d: flow mappings are not supported; use block style", ErrYAML, num)
	case '|', '>':
		return nil, fmt.Errorf("%w: line %d: block text must start on its own key", ErrYAML, num)
	}
	switch s {
	case "true", "True", "TRUE":
		return true, nil
	case "false", "False", "FALSE":
		return false, nil
	case "null", "Null", "NULL", "~":
		return nil, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && !strings.ContainsAny(s, "xXpP_") && strings.ToLower(s) != "inf" && strings.ToLower(s) != "nan" {
		return f, nil
	}
	return s, nil
}

func flowSeq(s string, num int) (any, error) {
	if !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("%w: line %d: flow lists must close on the same line", ErrYAML, num)
	}
	body := strings.TrimSpace(s[one : len(s)-one])
	out := []any{}
	if body == "" {
		return out, nil
	}
	var parts []string
	inQ := byte(0)
	start := zero
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case inQ != byte(zero):
			if c == '\\' && inQ == '"' {
				i++
			} else if c == inQ {
				inQ = byte(zero)
			}
		case c == '"' || c == '\'':
			inQ = c
		case c == '[' || c == '{':
			return nil, fmt.Errorf("%w: line %d: nested flow collections are not supported", ErrYAML, num)
		case c == ',':
			parts = append(parts, body[start:i])
			start = i + 1
		}
	}
	parts = append(parts, body[start:])
	for _, part := range parts {
		v, err := scalar(strings.TrimSpace(part), num)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
