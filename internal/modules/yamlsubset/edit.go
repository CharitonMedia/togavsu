package yamlsubset

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ErrPath is a path that is not in the path syntax, or names nothing in the document.
var ErrPath = errors.New("yaml path")

// ErrRemove is a removal Remove can't make in place: it would empty a list item, or the
// edited text would not read back as the document without the removed parts.
var ErrRemove = errors.New("yaml removal")

// span is the lines [start, end) a key with its value, or a list item, covers.
type span struct{ start, end int }

// dashLine is a "- key: value" line: the dash's column and the column its keys sit at.
type dashLine struct{ dash, content int }

// Segment is one step of a path: a mapping key, a list index, or (in a pattern) any
// list index.
type Segment struct {
	Key   string
	Index int
	// IsIndex marks a list index; Any marks the pattern index [*] (also written []).
	IsIndex bool
	Any     bool
}

var plainKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func formatKey(key string) string {
	if plainKey.MatchString(key) {
		return key
	}
	return "[" + strconv.Quote(key) + "]"
}

func keyPath(prefix, key string) string {
	k := formatKey(key)
	if prefix == "" || strings.HasPrefix(k, "[") {
		return prefix + k
	}
	return prefix + "." + k
}

func indexPath(prefix string, i int) string { return prefix + "[" + strconv.Itoa(i) + "]" }

// FormatPath writes segments as a path: keys joined by dots, list indexes in brackets
// (sidebar[0].section), a key that is not a plain name as ["key"].
func FormatPath(segs []Segment) string {
	out := ""
	for _, s := range segs {
		switch {
		case s.Any:
			out += "[*]"
		case s.IsIndex:
			out = indexPath(out, s.Index)
		default:
			out = keyPath(out, s.Key)
		}
	}
	return out
}

// ParsePath reads a path in FormatPath's syntax. [*] and [] are accepted as any index.
func ParsePath(path string) ([]Segment, error) {
	var out []Segment
	s := path
	if s == "" {
		return nil, fmt.Errorf("%w: empty path", ErrPath)
	}
	for first := true; s != ""; first = false {
		switch {
		case strings.HasPrefix(s, "[\""):
			end := closingQuote(s[one:])
			if end < zero || !strings.HasPrefix(s[one+end+one:], "]") {
				return nil, fmt.Errorf("%w: %s: unterminated key", ErrPath, path)
			}
			key, err := strconv.Unquote(s[one : one+end+one])
			if err != nil {
				return nil, fmt.Errorf("%w: %s: %v", ErrPath, path, err)
			}
			out = append(out, Segment{Key: key})
			s = s[one+end+two:]
		case strings.HasPrefix(s, "["):
			end := strings.IndexByte(s, ']')
			if end < zero {
				return nil, fmt.Errorf("%w: %s: unclosed [", ErrPath, path)
			}
			inner := s[one:end]
			if inner == "" || inner == "*" {
				out = append(out, Segment{IsIndex: true, Any: true})
			} else {
				n, err := strconv.Atoi(inner)
				if err != nil || n < zero {
					return nil, fmt.Errorf("%w: %s: %q is not a list index", ErrPath, path, inner)
				}
				out = append(out, Segment{IsIndex: true, Index: n})
			}
			s = s[end+one:]
		default:
			if !first {
				if !strings.HasPrefix(s, ".") {
					return nil, fmt.Errorf("%w: %s: expected . or [", ErrPath, path)
				}
				s = s[one:]
			}
			end := strings.IndexAny(s, ".[")
			if end < zero {
				end = len(s)
			}
			key := s[:end]
			if !plainKey.MatchString(key) {
				return nil, fmt.Errorf("%w: %s: %q is not a plain key; write it as [\"key\"]", ErrPath, path, key)
			}
			out = append(out, Segment{Key: key})
			s = s[end:]
		}
	}
	return out, nil
}

// Delete returns doc (as ToJSON's document decodes, or as parsed) without the value at
// path. It reports false when path names nothing.
func Delete(doc any, path []Segment) (any, bool) {
	if len(path) == zero {
		return nil, false
	}
	head, rest := path[zero], path[one:]
	switch v := doc.(type) {
	case map[string]any:
		if head.IsIndex {
			return doc, false
		}
		child, ok := v[head.Key]
		if !ok {
			return doc, false
		}
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = val
		}
		if len(rest) == zero {
			delete(out, head.Key)
			return out, true
		}
		next, ok := Delete(child, rest)
		out[head.Key] = next
		return out, ok
	case []any:
		if !head.IsIndex || head.Any || head.Index >= len(v) {
			return doc, false
		}
		if len(rest) == zero {
			out := append(append([]any{}, v[:head.Index]...), v[head.Index+one:]...)
			return out, true
		}
		out := append([]any{}, v...)
		next, ok := Delete(v[head.Index], rest)
		out[head.Index] = next
		return out, ok
	}
	return doc, false
}

// Remove deletes the keys and list items at paths from YAML source in place, keeping
// every other line (comments included) as written. It reads the result back and
// refuses with ErrRemove unless it is exactly the document without those parts, so an
// edit it can't make cleanly never yields a different manifest. Removing the first key
// of a "- key: value" list item moves the dash to the item's next key; removing an
// item's only key is refused (remove the item instead).
func Remove(src []byte, paths ...string) ([]byte, error) {
	crlf := strings.Contains(string(src), "\r\n")
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	raw := strings.Split(text, "\n")
	p := &yparser{spans: map[string]span{}, dash: map[int]dashLine{}}
	doc, err := p.parse(text)
	if err != nil {
		return nil, err
	}
	segs, err := removalSet(paths)
	if err != nil {
		return nil, err
	}
	gone := make([]bool, len(raw))
	type firstKey struct {
		item span
		at   dashLine
	}
	var moves []firstKey
	want := doc
	for _, s := range segs {
		path := FormatPath(s)
		sp, ok := p.spans[path]
		if !ok {
			return nil, fmt.Errorf("%w: %s is not in the document", ErrPath, path)
		}
		end := sp.end
		for end > sp.start+one && blankOrComment(raw[end-one]) {
			end--
		}
		for i := sp.start; i < end; i++ {
			gone[i] = true
		}
		if d, ok := p.dash[sp.start]; ok && !s[len(s)-one].IsIndex {
			moves = append(moves, firstKey{item: p.spans[FormatPath(s[:len(s)-one])], at: d})
		}
		if want, ok = Delete(want, s); !ok {
			return nil, fmt.Errorf("%w: %s is not in the document", ErrPath, path)
		}
	}
	for _, m := range moves {
		next := -one
		for i := m.item.start; i < m.item.end; i++ {
			if !gone[i] && !blankOrComment(raw[i]) {
				next = i
				break
			}
		}
		if next < zero || indentOf(raw[next]) != m.at.content {
			return nil, fmt.Errorf("%w: removing a list item's only key would empty the item; remove the item", ErrRemove)
		}
		raw[next] = raw[next][:m.at.dash] + "-" + raw[next][m.at.dash+one:]
	}
	var kept []string
	for i, line := range raw {
		if !gone[i] {
			kept = append(kept, line)
		}
	}
	out := strings.Join(kept, "\n")
	got, err := parseYAML(out)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRemove, err)
	}
	if !reflect.DeepEqual(normalize(got), normalize(want)) {
		return nil, fmt.Errorf("%w: the edited text does not read back as the document without %s", ErrRemove, strings.Join(paths, ", "))
	}
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return []byte(out), nil
}

// removalSet parses paths, drops duplicates and paths inside another removed path, and
// orders them so removing one list item never shifts the index of one still to remove.
func removalSet(paths []string) ([][]Segment, error) {
	var all [][]Segment
	for _, path := range paths {
		s, err := ParsePath(path)
		if err != nil {
			return nil, err
		}
		for _, seg := range s {
			if seg.Any {
				return nil, fmt.Errorf("%w: %s: Remove takes concrete paths, not patterns", ErrPath, path)
			}
		}
		all = append(all, s)
	}
	var out [][]Segment
	for i, s := range all {
		inside := false
		for j, o := range all {
			if i != j && hasPrefix(s, o) && (len(o) < len(s) || j < i) {
				inside = true
				break
			}
		}
		if !inside {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return later(out[i], out[j]) })
	return out, nil
}

// HasPrefix reports whether path starts with prefix, segment by segment; an Any segment
// in prefix matches every index.
func HasPrefix(path, prefix []Segment) bool { return hasPrefix(path, prefix) }

func hasPrefix(path, prefix []Segment) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i, s := range prefix {
		p := path[i]
		switch {
		case s.IsIndex != p.IsIndex:
			return false
		case s.IsIndex && !s.Any && s.Index != p.Index:
			return false
		case !s.IsIndex && s.Key != p.Key:
			return false
		}
	}
	return true
}

// later orders a after b when a sits further down the document: higher list indexes
// first, deeper paths before their ancestors' later siblings.
func later(a, b []Segment) bool {
	for i := zero; i < len(a) && i < len(b); i++ {
		switch {
		case a[i].IsIndex && b[i].IsIndex && a[i].Index != b[i].Index:
			return a[i].Index > b[i].Index
		case !a[i].IsIndex && !b[i].IsIndex && a[i].Key != b[i].Key:
			return a[i].Key > b[i].Key
		}
	}
	return len(a) > len(b)
}

func blankOrComment(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

func indentOf(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

// normalize treats an empty list or mapping as absent, as the manifest decoder does.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == zero {
			return nil
		}
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalize(val)
		}
		return out
	case []any:
		if len(t) == zero {
			return nil
		}
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalize(val)
		}
		return out
	}
	return v
}
