package doctender

import (
	"bytes"
	"encoding/binary"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ---- independent counts for DOC and RTF ----

// Fixed facts of the compound file (MS-CFB) and property set (MS-OLEPS) formats: byte
// offsets, sizes and markers. They are properties of the formats, not firm settings.
const (
	cfbHeaderSize   = zero + 512
	cfbDirEntrySize = zero + 128
	cfbEndOfChain   = uint32(zero) + 0xFFFFFFFE
	cfbMaxRegSect   = uint32(zero) + 0xFFFFFFFA
	cfbDIFATInHead  = zero + 109
	cfbOffShift     = zero + 30
	cfbOffMiniShift = zero + 32
	cfbOffDir       = zero + 48
	cfbOffCutoff    = zero + 56
	cfbOffMiniFAT   = zero + 60
	cfbOffDIFAT     = zero + 68
	cfbOffDIFATHead = zero + 76
	cfbShift512     = uint16(zero) + 9
	cfbShift4096    = uint16(zero) + 12
	cfbMiniShift    = uint16(zero) + 6
	cfbOffNameLen   = zero + 64
	cfbOffType      = zero + 66
	cfbOffStart     = zero + 116
	cfbOffSize      = zero + 120
	cfbMaxNameBytes = zero + 64
	cfbTypeStream   = byte(zero) + 2
	cfbTypeRoot     = byte(zero) + 5
	olepsByteOrder  = uint16(zero) + 0xFFFE
	olepsOffSets    = zero + 24
	olepsOffSection = zero + 44
	olepsMinHeader  = zero + 48
	vtI4            = uint32(zero) + 3
	pidPageCount    = uint32(zero) + 14
	pidWordCount    = uint32(zero) + 15
	utf16Surrogate  = zero + 65536
	hexByte         = zero + 16
	byteBits        = zero + 8
)

var cfbMagic = []byte("\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1")

// docCounts reads the word and page counts Word stored in a .doc's
// \x05SummaryInformation stream.
func docCounts(data []byte) (words, pages int, ok bool) {
	stream, found := cfbStream(data, "\x05SummaryInformation")
	if !found {
		return zero, zero, false
	}
	props := propertySet(stream)
	w, okw := props[pidWordCount]
	if !okw || w < zero {
		return zero, zero, false
	}
	p := props[pidPageCount]
	if p < zero {
		p = zero
	}
	return w, p, true
}

// cfbStream returns one named stream of a compound file. Every read is bounds-checked.
// The FAT lists each of its sectors once, and a chain that comes back to a sector it
// already read is refused, so nothing read can be larger than the file itself; a hostile
// file yields not found.
func cfbStream(data []byte, name string) ([]byte, bool) {
	if len(data) < cfbHeaderSize || !bytes.HasPrefix(data, cfbMagic) {
		return nil, false
	}
	le := binary.LittleEndian
	shift := le.Uint16(data[cfbOffShift:])
	miniShift := le.Uint16(data[cfbOffMiniShift:])
	if shift != cfbShift512 && shift != cfbShift4096 || miniShift != cfbMiniShift {
		return nil, false
	}
	ss := one << shift
	sector := func(n uint32) []byte {
		off := (int(n) + one) * ss
		if n > cfbMaxRegSect || off < zero || off+ss > len(data) {
			return nil
		}
		return data[off : off+ss]
	}
	// The FAT's sectors: 109 in the header, the rest in a DIFAT chain. A sector listed
	// twice is counted once, and the DIFAT chain may not revisit a sector.
	sectors := len(data) / ss
	var fatSectors []uint32
	listed := map[uint32]bool{}
	addFAT := func(v uint32) {
		if v <= cfbMaxRegSect && !listed[v] && len(fatSectors) < sectors {
			listed[v] = true
			fatSectors = append(fatSectors, v)
		}
	}
	for k := zero; k < cfbDIFATInHead; k++ {
		addFAT(le.Uint32(data[cfbOffDIFATHead+k*four:]))
	}
	difat := map[uint32]bool{}
	for next := le.Uint32(data[cfbOffDIFAT:]); next <= cfbMaxRegSect; {
		sec := sector(next)
		if sec == nil || difat[next] {
			return nil, false
		}
		difat[next] = true
		for k := zero; k < ss/four-one; k++ {
			addFAT(le.Uint32(sec[k*four:]))
		}
		next = le.Uint32(sec[ss-four:])
	}
	var fat []uint32
	for _, n := range fatSectors {
		sec := sector(n)
		if sec == nil {
			return nil, false
		}
		for k := zero; k < ss/four; k++ {
			fat = append(fat, le.Uint32(sec[k*four:]))
		}
	}
	chain := func(table []uint32, start uint32, read func(uint32) []byte) []byte {
		var out []byte
		seen := make([]bool, len(table))
		for n := start; n != cfbEndOfChain; {
			if int(n) >= len(table) || seen[n] {
				return nil
			}
			seen[n] = true
			b := read(n)
			if b == nil {
				return nil
			}
			out = append(out, b...)
			n = table[n]
		}
		return out
	}
	dir := chain(fat, le.Uint32(data[cfbOffDir:]), sector)
	if len(dir) < cfbDirEntrySize {
		return nil, false
	}
	var miniStream []byte
	var miniFAT []uint32
	if raw := chain(fat, le.Uint32(data[cfbOffMiniFAT:]), sector); raw != nil {
		for k := zero; k+four <= len(raw); k += four {
			miniFAT = append(miniFAT, le.Uint32(raw[k:]))
		}
	}
	cutoff := int(le.Uint32(data[cfbOffCutoff:]))
	for k := zero; k+cfbDirEntrySize <= len(dir); k += cfbDirEntrySize {
		e := dir[k : k+cfbDirEntrySize]
		kind := e[cfbOffType]
		start, size := le.Uint32(e[cfbOffStart:]), int(le.Uint32(e[cfbOffSize:]))
		if kind == cfbTypeRoot {
			miniStream = chain(fat, start, sector)
			continue
		}
		n := int(le.Uint16(e[cfbOffNameLen:]))
		if kind != cfbTypeStream || n < two || n > cfbMaxNameBytes {
			continue
		}
		units := make([]uint16, (n-two)/two)
		for x := range units {
			units[x] = le.Uint16(e[x*two:])
		}
		if string(utf16.Decode(units)) != name {
			continue
		}
		var body []byte
		if size < cutoff {
			ms := one << miniShift
			body = chain(miniFAT, start, func(m uint32) []byte {
				off := int(m) * ms
				if off+ms > len(miniStream) {
					return nil
				}
				return miniStream[off : off+ms]
			})
		} else {
			body = chain(fat, start, sector)
		}
		if size < zero || size > len(data) || len(body) < size {
			return nil, false
		}
		return body[:size], true
	}
	return nil, false
}

// propertySet reads the first section's 4-byte integer properties.
func propertySet(b []byte) map[uint32]int {
	le := binary.LittleEndian
	out := map[uint32]int{}
	if len(b) < olepsMinHeader || le.Uint16(b) != olepsByteOrder || le.Uint32(b[olepsOffSets:]) < uint32(one) {
		return out
	}
	sec := int(le.Uint32(b[olepsOffSection:]))
	if sec < zero || sec+eight > len(b) {
		return out
	}
	count := int(le.Uint32(b[sec+four:]))
	for k := zero; k < count; k++ {
		at := sec + eight + k*eight
		if at+eight > len(b) {
			break
		}
		id, off := le.Uint32(b[at:]), int(le.Uint32(b[at+four:]))
		v := sec + off
		if off < zero || v+eight > len(b) || le.Uint32(b[v:]) != vtI4 {
			continue
		}
		out[id] = int(int32(le.Uint32(b[v+four:])))
	}
	return out
}

var (
	reRTFWords = regexp.MustCompile(`\\nofwords(\d+)`)
	reRTFPages = regexp.MustCompile(`\\nofpages(\d+)`)
)

// rtfCounts uses the \info group's counts when the writer stored them, and otherwise
// counts the words of the document's text as read here, independently of the
// converter.
func rtfCounts(data []byte) (words, pages int, ok bool) {
	if !bytes.HasPrefix(bytes.TrimLeft(data, " \r\n\t"), []byte(`{\rtf`)) {
		return zero, zero, false
	}
	if m := reRTFWords.FindSubmatch(data); m != nil {
		words, _ = strconv.Atoi(string(m[one]))
		if p := reRTFPages.FindSubmatch(data); p != nil {
			pages, _ = strconv.Atoi(string(p[one]))
		}
		return words, pages, true
	}
	return len(strings.Fields(rtfText(data))), zero, true
}

// rtfSkip are destinations whose content is not document text.
var rtfSkip = map[string]bool{"fonttbl": true, "colortbl": true, "stylesheet": true, "info": true, "pict": true, "listtable": true,
	"listoverridetable": true, "rsidtbl": true, "generator": true, "themedata": true, "colorschememapping": true, "datastore": true,
	"latentstyles": true, "xmlnstbl": true, "object": true, "fldinst": true, "header": true, "footer": true, "headerl": true,
	"headerr": true, "footerl": true, "footerr": true, "footnote": true, "mmathPr": true, "filetbl": true, "revtbl": true}

// rtfText is the visible body text of an RTF document.
func rtfText(data []byte) string {
	var b strings.Builder
	type state struct{ skip bool }
	stack := []state{{}}
	skipping := func() bool { return stack[len(stack)-one].skip }
	pendingSkip := zero // characters to drop after \uN
	for k := zero; k < len(data); k++ {
		c := data[k]
		switch c {
		case '{':
			stack = append(stack, stack[len(stack)-one])
			if k+two < len(data) && data[k+one] == '\\' && data[k+two] == '*' {
				stack[len(stack)-one].skip = true
			}
		case '}':
			if len(stack) > one {
				stack = stack[:len(stack)-one]
			}
		case '\\':
			if k+one >= len(data) {
				break
			}
			n := data[k+one]
			switch {
			case n == '\\' || n == '{' || n == '}':
				if !skipping() {
					b.WriteByte(n)
				}
				k++
			case n == '\'':
				if k+three < len(data) && !skipping() {
					if v, err := strconv.ParseUint(string(data[k+two:k+four]), hexByte, byteBits); err == nil {
						if pendingSkip > zero {
							pendingSkip--
						} else {
							b.WriteRune(rune(v))
						}
					}
				}
				k += three
			case n == '~':
				if !skipping() {
					b.WriteByte(' ')
				}
				k++
			case (n >= 'a' && n <= 'z') || (n >= 'A' && n <= 'Z'):
				x := k + one
				for x < len(data) && ((data[x] >= 'a' && data[x] <= 'z') || (data[x] >= 'A' && data[x] <= 'Z')) {
					x++
				}
				word := string(data[k+one : x])
				y := x
				if y < len(data) && data[y] == '-' {
					y++
				}
				for y < len(data) && data[y] >= '0' && data[y] <= '9' {
					y++
				}
				param := string(data[x:y])
				if y < len(data) && data[y] == ' ' {
					y++
				}
				k = y - one
				if rtfSkip[word] {
					stack[len(stack)-one].skip = true
				}
				if skipping() {
					break
				}
				switch word {
				case "par", "line", "sect", "page", "row", "cell", "tab":
					b.WriteByte('\n')
				case "u":
					if v, err := strconv.Atoi(param); err == nil {
						if v < zero {
							v += utf16Surrogate
						}
						b.WriteRune(rune(v))
						pendingSkip = one
					}
				}
			default:
				k++
			}
		case '\r', '\n':
		default:
			if skipping() {
				break
			}
			if pendingSkip > zero {
				pendingSkip--
				break
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}
