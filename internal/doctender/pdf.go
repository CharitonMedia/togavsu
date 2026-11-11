package doctender

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// A small PDF reader for Doc Tender's page-by-page extraction. It reads classic and
// compressed cross-reference files by scanning every indirect object (later
// definitions win, as incremental updates do), object streams, the page tree with
// inherited resources, each page's text layer through simple and ToUnicode encodings,
// the page's largest image, and the Standard security handler's presence.

// ErrEncrypted is a PDF that names a security handler: password-protected.
var ErrEncrypted = errors.New("the PDF is password-protected")

// ErrDamaged is a PDF that does not open.
var ErrDamaged = errors.New("the PDF is damaged")

type pdfName string

type pdfRef struct{ Num, Gen int }

type pdfDict map[pdfName]any

type pdfArray []any

type pdfString []byte

type pdfStream struct {
	Dict pdfDict
	Raw  []byte
}

// pdfDoc is a parsed PDF.
type pdfDoc struct {
	objs    map[int]any
	trailer pdfDict
	pages   []pdfDict
}

var objHeader = regexp.MustCompile(`(\d+)\s+(\d+)\s+obj\b`)

// parsePDF opens a PDF. An encrypted file returns ErrEncrypted; one that does not
// parse returns ErrDamaged with the reason.
func parsePDF(data []byte) (doc *pdfDoc, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc, err = nil, fmt.Errorf("%w: %v", ErrDamaged, r)
		}
	}()
	if !bytes.HasPrefix(bytes.TrimLeft(data, "\x00\t\r\n "), []byte("%PDF-")) {
		return nil, fmt.Errorf("%w: no PDF header", ErrDamaged)
	}
	d := &pdfDoc{objs: map[int]any{}, trailer: pdfDict{}}
	for _, m := range objHeader.FindAllSubmatchIndex(data, -1) {
		num, _ := strconv.Atoi(string(data[m[two]:m[three]]))
		p := &pdfParser{b: data, i: m[one]}
		v, err := p.value()
		if err != nil {
			continue
		}
		if dict, ok := v.(pdfDict); ok {
			p.skipSpace()
			if p.keyword("stream") {
				raw, err := p.streamBody(d, dict)
				if err != nil {
					return nil, fmt.Errorf("%w: object %d: %v", ErrDamaged, num, err)
				}
				v = &pdfStream{Dict: dict, Raw: raw}
			}
		}
		d.objs[num] = v
	}
	if len(d.objs) == 0 {
		return nil, fmt.Errorf("%w: no objects", ErrDamaged)
	}
	// Trailers: classic trailer dictionaries and cross-reference streams.
	for _, idx := range regexp.MustCompile(`trailer\s*<<`).FindAllIndex(data, -1) {
		p := &pdfParser{b: data, i: idx[one] - two}
		if v, err := p.value(); err == nil {
			if t, ok := v.(pdfDict); ok {
				for k, val := range t {
					d.trailer[k] = val
				}
			}
		}
	}
	for _, v := range d.objs {
		if s, ok := v.(*pdfStream); ok && s.Dict["Type"] == pdfName("XRef") {
			for k, val := range s.Dict {
				if _, seen := d.trailer[k]; !seen {
					d.trailer[k] = val
				}
			}
		}
	}
	if _, ok := d.trailer["Encrypt"]; ok {
		return nil, ErrEncrypted
	}
	if err := d.expandObjectStreams(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDamaged, err)
	}
	root, ok := d.resolve(d.trailer["Root"]).(pdfDict)
	if !ok {
		for _, v := range d.objs {
			if dict, ok := v.(pdfDict); ok && dict["Type"] == pdfName("Catalog") {
				root = dict
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("%w: no document catalog", ErrDamaged)
	}
	tree, ok := d.resolve(root["Pages"]).(pdfDict)
	if !ok {
		return nil, fmt.Errorf("%w: no page tree", ErrDamaged)
	}
	visits := zero
	if err := d.walkPages(tree, pdfDict{}, &visits); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDamaged, err)
	}
	if len(d.pages) == 0 {
		return nil, fmt.Errorf("%w: no pages", ErrDamaged)
	}
	return d, nil
}

func (d *pdfDoc) expandObjectStreams() error {
	var streams []*pdfStream
	for _, v := range d.objs {
		if s, ok := v.(*pdfStream); ok && s.Dict["Type"] == pdfName("ObjStm") {
			streams = append(streams, s)
		}
	}
	// The parsed objects can keep their stream's bytes alive, so all object streams
	// together stay under the decode cap.
	limit, _ := decodeCaps()
	total := int64(zero)
	for _, s := range streams {
		body, err := d.decode(s)
		if err != nil {
			return err
		}
		if total += int64(len(body)); total > limit {
			return fmt.Errorf("%w: the object streams pass %d bytes", errDecodeLimit, limit)
		}
		n, first := d.int(s.Dict["N"]), d.int(s.Dict["First"])
		p := &pdfParser{b: body}
		type entry struct{ num, off int }
		var entries []entry
		for k := zero; k < n; k++ {
			a, err1 := p.value()
			b, err2 := p.value()
			if err1 != nil || err2 != nil {
				return errors.New("bad object stream index")
			}
			entries = append(entries, entry{d.int(a), d.int(b)})
		}
		for _, e := range entries {
			if _, defined := d.objs[e.num]; defined {
				continue
			}
			q := &pdfParser{b: body, i: first + e.off}
			v, err := q.value()
			if err != nil {
				return err
			}
			d.objs[e.num] = v
		}
	}
	return nil
}

var inheritable = []pdfName{"Resources", "MediaBox", "CropBox", "Rotate"}

// walkPages flattens the page tree. A tree can't have more nodes than the file has
// objects, so more visits than that means it loops back on itself.
func (d *pdfDoc) walkPages(node pdfDict, inherited pdfDict, visits *int) error {
	*visits++
	if *visits > len(d.objs) {
		return errors.New("page tree loops")
	}
	here := pdfDict{}
	for k, v := range inherited {
		here[k] = v
	}
	for _, k := range inheritable {
		if v, ok := node[k]; ok {
			here[k] = v
		}
	}
	if node["Type"] == pdfName("Page") || node["Kids"] == nil {
		page := pdfDict{}
		for k, v := range node {
			page[k] = v
		}
		for k, v := range here {
			if _, ok := page[k]; !ok {
				page[k] = v
			}
		}
		d.pages = append(d.pages, page)
		return nil
	}
	kids, ok := d.resolve(node["Kids"]).(pdfArray)
	if !ok {
		return errors.New("page tree kids are not an array")
	}
	for _, k := range kids {
		kid, ok := d.resolve(k).(pdfDict)
		if !ok {
			return errors.New("a page is missing")
		}
		if err := d.walkPages(kid, here, visits); err != nil {
			return err
		}
	}
	return nil
}

func (d *pdfDoc) resolve(v any) any {
	for hops := zero; hops < len(d.objs)+one; hops++ {
		r, ok := v.(pdfRef)
		if !ok {
			return v
		}
		v = d.objs[r.Num]
	}
	return nil
}

func (d *pdfDoc) int(v any) int {
	switch n := d.resolve(v).(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return zero
}

func (d *pdfDoc) num(v any) float64 {
	switch n := d.resolve(v).(type) {
	case int:
		return float64(n)
	case float64:
		return n
	}
	return float64(zero)
}

// decode applies a stream's filters. Flate, ASCII85 and ASCIIHex are decoded; image
// codecs (DCT, JPX, CCITT, JBIG2) are left for the image reader.
func (d *pdfDoc) decode(s *pdfStream) ([]byte, error) {
	data := s.Raw
	for _, f := range d.filters(s.Dict) {
		switch f {
		case "FlateDecode", "Fl":
			zr, err := zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, fmt.Errorf("flate: %w", err)
			}
			out, err := readCapped(zr)
			if err != nil && len(out) == 0 {
				return nil, fmt.Errorf("flate: %w", err)
			}
			data = out
		case "ASCII85Decode", "A85":
			trimmed := bytes.TrimSuffix(bytes.TrimSpace(data), []byte("~>"))
			out := make([]byte, len(trimmed)*four)
			n, _, err := ascii85.Decode(out, trimmed, true)
			if err != nil {
				return nil, fmt.Errorf("ascii85: %w", err)
			}
			data = out[:n]
		case "ASCIIHexDecode", "AHx":
			clean := strings.Map(func(r rune) rune {
				if unicode.IsSpace(r) || r == '>' {
					return -1
				}
				return r
			}, string(data))
			if len(clean)%two != zero {
				clean += "0"
			}
			out, err := hex.DecodeString(clean)
			if err != nil {
				return nil, fmt.Errorf("asciihex: %w", err)
			}
			data = out
		default:
			return nil, fmt.Errorf("unsupported filter %s", f)
		}
	}
	return data, nil
}

func (d *pdfDoc) filters(dict pdfDict) []string {
	switch f := d.resolve(dict["Filter"]).(type) {
	case pdfName:
		return []string{string(f)}
	case pdfArray:
		var out []string
		for _, x := range f {
			if n, ok := d.resolve(x).(pdfName); ok {
				out = append(out, string(n))
			}
		}
		return out
	}
	return nil
}

// pageCount is the number of pages.
func (d *pdfDoc) pageCount() int { return len(d.pages) }

func (d *pdfDoc) contents(page pdfDict) ([]byte, error) {
	var parts []any
	switch c := d.resolve(page["Contents"]).(type) {
	case *pdfStream:
		parts = []any{c}
	case pdfArray:
		parts = c
	case nil:
		return nil, nil
	}
	// The page's streams together stay under the decode cap, so a Contents array that
	// repeats one large stream cannot multiply it.
	limit, _ := decodeCaps()
	var buf bytes.Buffer
	for _, p := range parts {
		s, ok := d.resolve(p).(*pdfStream)
		if !ok {
			return nil, errors.New("page contents are not a stream")
		}
		b, err := d.decode(s)
		if err != nil {
			return nil, err
		}
		if int64(buf.Len()+len(b)) > limit {
			return nil, fmt.Errorf("%w: the page's contents pass %d bytes", errDecodeLimit, limit)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func (d *pdfDoc) resources(page pdfDict, kind pdfName) pdfDict {
	res, _ := d.resolve(page["Resources"]).(pdfDict)
	if res == nil {
		return pdfDict{}
	}
	out, _ := d.resolve(res[kind]).(pdfDict)
	if out == nil {
		return pdfDict{}
	}
	return out
}

// pageText reads a page's text layer (index from 0).
func (d *pdfDoc) pageText(index int) (string, error) {
	text, _, err := d.pageLayers(index)
	return text, err
}

// pageLayers reads a page's text layer and sorts it: what a reader sees, and text
// drawn so that no reader sees it, by why: render mode 3 or 7 ("invisible text"),
// filled white ("white text"), or set smaller than wire().TinyTextPoints ("tiny
// text"). A model reading the text layer takes in both.
func (d *pdfDoc) pageLayers(index int) (string, map[string]string, error) {
	page := d.pages[index]
	body, err := d.contents(page)
	if err != nil {
		return "", nil, err
	}
	// Every font on the page shares one CMap entry budget, and a ToUnicode stream that
	// several fonts name is read once.
	fonts := map[pdfName]map[string]string{}
	read := map[*pdfStream]map[string]string{}
	budget := cmapEntryCap()
	for name, ref := range d.resources(page, "Font") {
		m, err := d.toUnicode(ref, read, &budget)
		if err != nil {
			return "", nil, err
		}
		fonts[name] = m
	}
	p := &pdfParser{b: body}
	var out strings.Builder
	var operands []any
	var cmap map[string]string
	type look struct {
		invisible, white bool
		size, scale      float64
	}
	st := look{scale: float64(one)}
	var saved []look
	hidden := map[string]*strings.Builder{}
	show := func(s pdfString) {
		t := decodeText(s, cmap)
		why := ""
		switch {
		case st.invisible:
			why = "invisible text"
		case st.white:
			why = "white text"
		case st.size > float64(zero) && st.size*st.scale < wire().TinyTextPoints:
			why = "tiny text"
		}
		if why == "" {
			out.WriteString(t)
			return
		}
		if hidden[why] == nil {
			hidden[why] = &strings.Builder{}
		}
		hidden[why].WriteString(t + " ")
	}
	nums := func() []float64 {
		var v []float64
		for _, o := range operands {
			v = append(v, d.num(o))
		}
		return v
	}
	white := func(v []float64, full float64) bool {
		for _, x := range v {
			if x != full {
				return false
			}
		}
		return len(v) > zero
	}
	for {
		v, err := p.value()
		if err == io.EOF {
			break
		}
		if err != nil {
			// A malformed operator is skipped, as viewers do.
			p.i++
			operands = nil
			continue
		}
		op, isOp := v.(pdfOp)
		if !isOp {
			operands = append(operands, v)
			continue
		}
		switch op {
		case "Tf":
			if len(operands) >= two {
				if n, ok := operands[len(operands)-two].(pdfName); ok {
					cmap = fonts[n]
				}
				st.size = d.num(operands[len(operands)-one])
			}
		case "Tj", "'", "\"":
			if len(operands) > zero {
				if s, ok := operands[len(operands)-one].(pdfString); ok {
					if op != "Tj" {
						out.WriteByte('\n')
					}
					show(s)
				}
			}
		case "TJ":
			if len(operands) > zero {
				if arr, ok := operands[len(operands)-one].(pdfArray); ok {
					for _, x := range arr {
						switch t := x.(type) {
						case pdfString:
							show(t)
						case int, float64:
							// A large negative kern is a word space.
							if d.num(t) <= wire().TJWordSpace {
								out.WriteByte(' ')
							}
						}
					}
				}
			}
		case "T*", "ET":
			out.WriteByte('\n')
		case "Td", "TD":
			if len(operands) >= two && d.num(operands[len(operands)-one]) != float64(zero) {
				out.WriteByte('\n')
			} else {
				out.WriteByte(' ')
			}
		case "BT":
			st.scale = float64(one)
		case "Tm":
			if v := nums(); len(v) == three+three {
				st.scale = math.Sqrt(math.Abs(v[zero]*v[three] - v[one]*v[two]))
			}
		case "Tr":
			if len(operands) > zero {
				mode := d.int(operands[len(operands)-one])
				st.invisible = mode == three || mode == three+four
			}
		case "q":
			saved = append(saved, st)
		case "Q":
			if len(saved) > zero {
				st, saved = saved[len(saved)-one], saved[:len(saved)-one]
			}
		case "rg", "g", "sc", "scn":
			st.white = white(nums(), float64(one))
		case "k":
			st.white = len(operands) == four && white(nums(), float64(zero))
		case "BI":
			p.skipInlineImage()
		}
		operands = nil
	}
	layers := map[string]string{}
	for why, b := range hidden {
		layers[why] = tidyLines(b.String())
	}
	return tidyLines(out.String()), layers, nil
}

func tidyLines(s string) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.Join(strings.Fields(l), " ")
		if l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// usableText is a text layer with words in it, not just spacing or marks.
func usableText(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// toUnicode reads a font's ToUnicode CMap (bfchar and bfrange); nil is a simple font.
// Each entry spends one of budget, shared by the page's fonts; past it the page is a
// decode-limit error. A stream already in read is not read again.
func (d *pdfDoc) toUnicode(ref any, read map[*pdfStream]map[string]string, budget *int) (map[string]string, error) {
	font, ok := d.resolve(ref).(pdfDict)
	if !ok {
		return nil, nil
	}
	s, ok := d.resolve(font["ToUnicode"]).(*pdfStream)
	if !ok {
		return nil, nil
	}
	if m, done := read[s]; done {
		return m, nil
	}
	body, err := d.decode(s)
	if errors.Is(err, errDecodeLimit) {
		return nil, err
	}
	if err != nil {
		return nil, nil
	}
	m := map[string]string{}
	read[s] = m
	spend := func(n uint64) error {
		if n > uint64(*budget) {
			*budget = zero
			return fmt.Errorf("%w: a font map on the page passes %d entries", errDecodeLimit, cmapEntryCap())
		}
		*budget -= int(n)
		return nil
	}
	text := string(body)
	hexes := regexp.MustCompile(`<([0-9A-Fa-f]*)>`)
	for _, blk := range regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`).FindAllStringSubmatch(text, -1) {
		hs := hexes.FindAllStringSubmatch(blk[one], -1)
		if err := spend(uint64(len(hs) / two)); err != nil {
			return nil, err
		}
		for k := zero; k+one < len(hs); k += two {
			src, _ := hex.DecodeString(hs[k][one])
			m[string(src)] = utf16Hex(hs[k+one][one])
		}
	}
	rangeRE := regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*(<[0-9A-Fa-f]+>|\[[^\]]*\])`)
	for _, blk := range regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`).FindAllStringSubmatch(text, -1) {
		for _, r := range rangeRE.FindAllStringSubmatch(blk[one], -1) {
			lo, _ := strconv.ParseUint(r[one], 16, 32)
			hi, _ := strconv.ParseUint(r[two], 16, 32)
			width := len(r[one]) / two
			dst := r[three]
			if hi < lo || hi-lo > uint64(len(body)) {
				continue
			}
			if strings.HasPrefix(dst, "[") {
				items := hexes.FindAllStringSubmatch(dst, -1)
				if err := spend(min(uint64(len(items)), hi-lo+uint64(one))); err != nil {
					return nil, err
				}
				for k := zero; k < len(items) && lo+uint64(k) <= hi; k++ {
					m[codeBytes(lo+uint64(k), width)] = utf16Hex(items[k][one])
				}
				continue
			}
			base := []rune(utf16Hex(strings.Trim(dst, "<>")))
			if len(base) == 0 {
				continue
			}
			if err := spend(hi - lo + uint64(one)); err != nil {
				return nil, err
			}
			for c := lo; c <= hi; c++ {
				out := append([]rune{}, base...)
				out[len(out)-one] += rune(c - lo)
				m[codeBytes(c, width)] = string(out)
			}
		}
	}
	return m, nil
}

func codeBytes(c uint64, width int) string {
	b := make([]byte, width)
	for k := width - one; k >= zero; k-- {
		b[k] = byte(c)
		c >>= eight
	}
	return string(b)
}

func utf16Hex(h string) string {
	raw, err := hex.DecodeString(h)
	if err != nil {
		return ""
	}
	var units []uint16
	for k := zero; k+one < len(raw); k += two {
		units = append(units, uint16(raw[k])<<eight|uint16(raw[k+one]))
	}
	return string(utf16.Decode(units))
}

// decodeText maps a shown string through the font's CMap, or reads it as Latin-1.
func decodeText(s pdfString, cmap map[string]string) string {
	if len(cmap) == 0 {
		var b strings.Builder
		for _, c := range s {
			b.WriteRune(rune(c))
		}
		return b.String()
	}
	widths := map[int]bool{}
	for k := range cmap {
		widths[len(k)] = true
	}
	var ws []int
	for w := range widths {
		ws = append(ws, w)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ws)))
	var b strings.Builder
	for i := zero; i < len(s); {
		matched := false
		for _, w := range ws {
			if i+w <= len(s) {
				if u, ok := cmap[string(s[i:i+w])]; ok {
					b.WriteString(u)
					i += w
					matched = true
					break
				}
			}
		}
		if !matched {
			b.WriteRune(rune(s[i]))
			i++
		}
	}
	return b.String()
}

// pageImage returns the page's largest image as PNG or JPEG bytes, with its media type.
// ok is false when the page has no image this reader can decode.
func (d *pdfDoc) pageImage(index int) (data []byte, mediaType string, ok bool) {
	var best *pdfStream
	area := zero
	for _, ref := range d.resources(d.pages[index], "XObject") {
		s, isStream := d.resolve(ref).(*pdfStream)
		if !isStream || s.Dict["Subtype"] != pdfName("Image") {
			continue
		}
		if a := d.int(s.Dict["Width"]) * d.int(s.Dict["Height"]); a > area {
			best, area = s, a
		}
	}
	if best == nil {
		return nil, "", false
	}
	fs := d.filters(best.Dict)
	if len(fs) > zero && (fs[len(fs)-one] == "DCTDecode" || fs[len(fs)-one] == "DCT") {
		head := &pdfStream{Dict: pdfDict{"Filter": pdfArray{}}, Raw: best.Raw}
		if len(fs) > one {
			names := pdfArray{}
			for _, f := range fs[:len(fs)-one] {
				names = append(names, pdfName(f))
			}
			head.Dict["Filter"] = names
		}
		raw, err := d.decode(head)
		if err != nil {
			return nil, "", false
		}
		return raw, "image/jpeg", true
	}
	raw, err := d.decode(best)
	if err != nil {
		return nil, "", false
	}
	if d.int(best.Dict["BitsPerComponent"]) != eight || d.resolve(best.Dict["DecodeParms"]) != nil {
		return nil, "", false
	}
	w, h := d.int(best.Dict["Width"]), d.int(best.Dict["Height"])
	var img image.Image
	switch d.resolve(best.Dict["ColorSpace"]) {
	case pdfName("DeviceRGB"):
		if len(raw) < w*h*three {
			return nil, "", false
		}
		rgba := image.NewRGBA(image.Rect(zero, zero, w, h))
		for y := zero; y < h; y++ {
			for x := zero; x < w; x++ {
				o := (y*w + x) * three
				rgba.Set(x, y, color.RGBA{raw[o], raw[o+one], raw[o+two], ^uint8(zero)})
			}
		}
		img = rgba
	case pdfName("DeviceGray"):
		if len(raw) < w*h {
			return nil, "", false
		}
		g := image.NewGray(image.Rect(zero, zero, w, h))
		copy(g.Pix, raw)
		img = g
	default:
		return nil, "", false
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", false
	}
	return buf.Bytes(), "image/png", true
}

// pagePDF writes page index as a standalone one-page PDF, for a model that reads PDFs.
func (d *pdfDoc) pagePDF(index int) ([]byte, error) {
	page := pdfDict{}
	for k, v := range d.pages[index] {
		if k != "Parent" {
			page[k] = v
		}
	}
	w := &pdfWriter{}
	renum := map[int]int{}
	var queue []int
	var visit func(v any) any
	visit = func(v any) any {
		switch t := v.(type) {
		case pdfRef:
			if n, ok := renum[t.Num]; ok {
				return pdfRef{n, zero}
			}
			n := w.reserve()
			renum[t.Num] = n
			queue = append(queue, t.Num)
			return pdfRef{n, zero}
		case pdfDict:
			out := pdfDict{}
			for k, x := range t {
				if k != "Parent" {
					out[k] = visit(x)
				}
			}
			return out
		case pdfArray:
			out := make(pdfArray, len(t))
			for k, x := range t {
				out[k] = visit(x)
			}
			return out
		case *pdfStream:
			return &pdfStream{Dict: visit(t.Dict).(pdfDict), Raw: t.Raw}
		}
		return v
	}
	catalog, pages, pageNum := w.reserve(), w.reserve(), w.reserve()
	newPage := visit(page).(pdfDict)
	newPage["Type"], newPage["Parent"] = pdfName("Page"), pdfRef{pages, zero}
	for k := zero; k < len(queue); k++ {
		old := queue[k]
		w.set(renum[old], visit(d.objs[old]))
	}
	w.set(pageNum, newPage)
	w.set(pages, pdfDict{"Type": pdfName("Pages"), "Kids": pdfArray{pdfRef{pageNum, zero}}, "Count": one})
	w.set(catalog, pdfDict{"Type": pdfName("Catalog"), "Pages": pdfRef{pages, zero}})
	return w.bytes(catalog), nil
}

// pdfOp is a content-stream operator.
type pdfOp string

type pdfParser struct {
	b []byte
	i int
}

func isDelim(c byte) bool { return strings.IndexByte("()<>[]{}/%", c) >= zero }

func isSpace(c byte) bool { return strings.IndexByte("\x00\t\n\f\r ", c) >= zero }

func (p *pdfParser) skipSpace() {
	for p.i < len(p.b) {
		c := p.b[p.i]
		switch {
		case isSpace(c):
			p.i++
		case c == '%':
			for p.i < len(p.b) && p.b[p.i] != '\n' && p.b[p.i] != '\r' {
				p.i++
			}
		default:
			return
		}
	}
}

func (p *pdfParser) keyword(k string) bool {
	if bytes.HasPrefix(p.b[p.i:], []byte(k)) {
		p.i += len(k)
		return true
	}
	return false
}

func (p *pdfParser) token() string {
	start := p.i
	for p.i < len(p.b) && !isSpace(p.b[p.i]) && !isDelim(p.b[p.i]) {
		p.i++
	}
	return string(p.b[start:p.i])
}

var errEndContainer = errors.New("end of container")

// value parses one object, or a content-stream operator as pdfOp.
func (p *pdfParser) value() (any, error) {
	p.skipSpace()
	if p.i >= len(p.b) {
		return nil, io.EOF
	}
	c := p.b[p.i]
	switch {
	case c == '/':
		p.i++
		return pdfName(unescapeName(p.token())), nil
	case c == '(':
		return p.literalString()
	case c == '<' && p.i+one < len(p.b) && p.b[p.i+one] == '<':
		p.i += two
		dict := pdfDict{}
		for {
			k, err := p.value()
			if err == errEndContainer {
				return dict, nil
			}
			if err != nil {
				return nil, err
			}
			name, ok := k.(pdfName)
			if !ok {
				return nil, errors.New("dictionary key is not a name")
			}
			v, err := p.value()
			if err == errEndContainer {
				return dict, nil
			}
			if err != nil {
				return nil, err
			}
			dict[name] = v
		}
	case c == '>' && p.i+one < len(p.b) && p.b[p.i+one] == '>':
		p.i += two
		return nil, errEndContainer
	case c == '<':
		end := bytes.IndexByte(p.b[p.i:], '>')
		if end < zero {
			return nil, errors.New("unterminated hex string")
		}
		h := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, string(p.b[p.i+one:p.i+end]))
		p.i += end + one
		if len(h)%two != zero {
			h += "0"
		}
		raw, err := hex.DecodeString(h)
		if err != nil {
			return nil, err
		}
		return pdfString(raw), nil
	case c == '[':
		p.i++
		var arr pdfArray
		for {
			v, err := p.value()
			if err == errEndContainer {
				return arr, nil
			}
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
	case c == ']':
		p.i++
		return nil, errEndContainer
	case c == '{' || c == '}' || c == ')' || c == '>':
		p.i++
		return pdfOp(string(c)), nil
	}
	tok := p.token()
	if tok == "" {
		return nil, errors.New("unexpected byte")
	}
	switch tok {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	if n, err := strconv.Atoi(tok); err == nil {
		// An indirect reference is "num gen R".
		save := p.i
		p.skipSpace()
		gen := p.token()
		if g, err := strconv.Atoi(gen); err == nil {
			p.skipSpace()
			if p.token() == "R" {
				return pdfRef{n, g}, nil
			}
		}
		p.i = save
		return n, nil
	}
	if f, err := strconv.ParseFloat(tok, 64); err == nil {
		return f, nil
	}
	return pdfOp(tok), nil
}

func unescapeName(s string) string {
	if !strings.Contains(s, "#") {
		return s
	}
	var b strings.Builder
	for k := zero; k < len(s); k++ {
		if s[k] == '#' && k+three <= len(s) {
			if v, err := strconv.ParseUint(s[k+one:k+three], 16, 8); err == nil {
				b.WriteByte(byte(v))
				k += two
				continue
			}
		}
		b.WriteByte(s[k])
	}
	return b.String()
}

func (p *pdfParser) literalString() (any, error) {
	p.i++
	depth := one
	var out []byte
	for p.i < len(p.b) {
		c := p.b[p.i]
		p.i++
		switch c {
		case '\\':
			if p.i >= len(p.b) {
				return nil, errors.New("unterminated string")
			}
			e := p.b[p.i]
			p.i++
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\r':
				if p.i < len(p.b) && p.b[p.i] == '\n' {
					p.i++
				}
			case '\n':
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := one; k < three && p.i < len(p.b) && p.b[p.i] >= '0' && p.b[p.i] <= '7'; k++ {
						v = v*eight + int(p.b[p.i]-'0')
						p.i++
					}
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
		case '(':
			depth++
			out = append(out, c)
		case ')':
			depth--
			if depth == zero {
				return pdfString(out), nil
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return nil, errors.New("unterminated string")
}

// streamBody reads the bytes after "stream" using /Length, or up to endstream.
func (p *pdfParser) streamBody(d *pdfDoc, dict pdfDict) ([]byte, error) {
	if p.i < len(p.b) && p.b[p.i] == '\r' {
		p.i++
	}
	if p.i < len(p.b) && p.b[p.i] == '\n' {
		p.i++
	}
	start := p.i
	if n, ok := dict["Length"].(int); ok && n >= zero && start+n <= len(p.b) {
		rest := bytes.TrimLeft(p.b[start+n:], "\r\n\t ")
		if bytes.HasPrefix(rest, []byte("endstream")) {
			p.i = start + n
			return p.b[start : start+n], nil
		}
	}
	end := bytes.Index(p.b[start:], []byte("endstream"))
	if end < zero {
		return nil, errors.New("stream has no end")
	}
	raw := p.b[start : start+end]
	raw = bytes.TrimSuffix(raw, []byte("\n"))
	raw = bytes.TrimSuffix(raw, []byte("\r"))
	p.i = start + end
	return raw, nil
}

func (p *pdfParser) skipInlineImage() {
	end := bytes.Index(p.b[p.i:], []byte("EI"))
	if end < zero {
		p.i = len(p.b)
		return
	}
	p.i += end + len("EI")
}

// pdfWriter writes indirect objects with a classic cross-reference table.
type pdfWriter struct {
	objs []any
}

func (w *pdfWriter) reserve() int {
	w.objs = append(w.objs, nil)
	return len(w.objs)
}

func (w *pdfWriter) set(n int, v any) { w.objs[n-one] = v }

func (w *pdfWriter) bytes(root int) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(w.objs))
	for k, v := range w.objs {
		offsets[k] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n", k+one)
		writeValue(&buf, v)
		buf.WriteString("\nendobj\n")
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(w.objs)+one)
	for _, o := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(w.objs)+one, root, xref)
	return buf.Bytes()
}

func writeValue(buf *bytes.Buffer, v any) {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		fmt.Fprint(buf, t)
	case int:
		fmt.Fprint(buf, t)
	case float64:
		fmt.Fprintf(buf, "%g", t)
	case pdfName:
		buf.WriteString("/" + string(t))
	case pdfRef:
		fmt.Fprintf(buf, "%d %d R", t.Num, t.Gen)
	case pdfString:
		buf.WriteString("<" + hex.EncodeToString(t) + ">")
	case pdfArray:
		buf.WriteString("[")
		for k, x := range t {
			if k > zero {
				buf.WriteString(" ")
			}
			writeValue(buf, x)
		}
		buf.WriteString("]")
	case pdfDict:
		keys := make([]string, zero, len(t))
		for k := range t {
			keys = append(keys, string(k))
		}
		sort.Strings(keys)
		buf.WriteString("<<")
		for _, k := range keys {
			buf.WriteString(" /" + k + " ")
			writeValue(buf, t[pdfName(k)])
		}
		buf.WriteString(" >>")
	case *pdfStream:
		dict := pdfDict{}
		for k, x := range t.Dict {
			dict[k] = x
		}
		dict["Length"] = len(t.Raw)
		writeValue(buf, dict)
		buf.WriteString("\nstream\n")
		buf.Write(t.Raw)
		buf.WriteString("\nendstream")
	case pdfOp:
		buf.WriteString(string(t))
	}
}
