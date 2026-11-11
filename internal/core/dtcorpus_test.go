package core

// The generated Doc Tender regression corpus and a synthetic model provider on the
// OpenAI wire, for the core's phase 4 cases. Every byte is synthetic. Pages and photos
// paint their text in a 5x7 glyph font, so a reader that can't see the pixels can't read
// them; the provider reads only what it is sent. It mirrors internal/doctender's
// corpus_test.go and provider_test.go, which reach that package's unexported helpers.

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"crypto/rc4"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var font = map[rune][]string{
	' ': {"00000", "00000", "00000", "00000", "00000", "00000", "00000"},
	'0': {"01110", "11011", "10001", "10001", "10001", "11011", "01110"},
	'1': {"00010", "00110", "01010", "00010", "00010", "00010", "00010"},
	'2': {"01110", "10001", "00001", "00010", "00100", "01000", "11111"},
	'3': {"11111", "00010", "00100", "00010", "00001", "10001", "01110"},
	'4': {"00010", "00110", "01010", "10010", "11111", "00010", "00010"},
	'5': {"11111", "10000", "11110", "00001", "00001", "10001", "01110"},
	'6': {"00110", "01000", "10000", "11110", "10001", "10001", "01110"},
	'7': {"11111", "00001", "00001", "00010", "00100", "00100", "00100"},
	'8': {"01110", "10001", "10001", "01110", "10001", "10001", "01110"},
	'9': {"01110", "10001", "10001", "01111", "00001", "00010", "01100"},
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"},
	'B': {"11110", "10001", "10001", "11110", "10001", "10001", "11110"},
	'C': {"01110", "10001", "10000", "10000", "10000", "10001", "01110"},
	'D': {"11110", "10001", "10001", "10001", "10001", "10001", "11110"},
	'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
	'F': {"11111", "10000", "10000", "11110", "10000", "10000", "10000"},
	'G': {"01110", "10001", "10000", "10111", "10001", "10001", "01111"},
	'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
	'I': {"01110", "00100", "00100", "00100", "00100", "00100", "01110"},
	'J': {"00111", "00010", "00010", "00010", "00010", "10010", "01100"},
	'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"},
	'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
	'M': {"10001", "11011", "10101", "10101", "10001", "10001", "10001"},
	'N': {"10001", "11001", "11001", "10101", "10011", "10011", "10001"},
	'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	'P': {"11110", "10001", "10001", "11110", "10000", "10000", "10000"},
	'Q': {"01110", "10001", "10001", "10001", "10101", "10010", "01101"},
	'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
	'S': {"01111", "10000", "10000", "01110", "00001", "00001", "11110"},
	'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"},
	'U': {"10001", "10001", "10001", "10001", "10001", "10001", "01110"},
	'V': {"10001", "10001", "10001", "10001", "10001", "01010", "00100"},
	'W': {"10001", "10001", "10001", "10101", "10101", "10101", "01010"},
	'X': {"10001", "10001", "01010", "00100", "01010", "10001", "10001"},
	'Y': {"10001", "10001", "01010", "00100", "00100", "00100", "00100"},
	'Z': {"11111", "00001", "00010", "00100", "01000", "10000", "11111"},
	'-': {"00000", "00000", "00000", "11111", "00000", "00000", "00000"},
}

const (
	glyphScale  = 3
	glyphMargin = 12
	glyphPitchX = 6 * glyphScale
	glyphPitchY = 10 * glyphScale
)

// RenderPage paints uppercase lines into a white RGB PNG.
func RenderPage(lines []string) []byte {
	cols := 0
	for _, l := range lines {
		cols = max(cols, len(l))
	}
	w := 2*glyphMargin + cols*glyphPitchX
	h := 2*glyphMargin + len(lines)*glyphPitchY
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	for li, l := range lines {
		for ci, r := range l {
			g, ok := font[r]
			if !ok {
				panic(fmt.Sprintf("dtcorpus: no glyph for %q", r))
			}
			for gy, row := range g {
				for gx, bit := range row {
					if bit != '1' {
						continue
					}
					for dy := 0; dy < glyphScale; dy++ {
						for dx := 0; dx < glyphScale; dx++ {
							img.Set(glyphMargin+ci*glyphPitchX+gx*glyphScale+dx, glyphMargin+li*glyphPitchY+gy*glyphScale+dy, color.Black)
						}
					}
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ReadGlyphs reads text painted by RenderPage from image pixels: the provider's OCR.
func ReadGlyphs(data []byte) string {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	b := img.Bounds()
	dark := func(x, y int) bool {
		if x >= b.Max.X || y >= b.Max.Y {
			return false
		}
		r, g, bl, _ := img.At(x, y).RGBA()
		return (r+g+bl)/3 < 0x8000
	}
	reverse := map[string]rune{}
	for r, g := range font {
		reverse[strings.Join(g, "")] = r
	}
	var lines []string
	for top := glyphMargin; top+7*glyphScale <= b.Max.Y; top += glyphPitchY {
		var line []rune
		for left := glyphMargin; left+5*glyphScale <= b.Max.X; left += glyphPitchX {
			var bits strings.Builder
			for gy := 0; gy < 7; gy++ {
				for gx := 0; gx < 5; gx++ {
					if dark(left+gx*glyphScale+glyphScale/2, top+gy*glyphScale+glyphScale/2) {
						bits.WriteByte('1')
					} else {
						bits.WriteByte('0')
					}
				}
			}
			r, ok := reverse[bits.String()]
			if !ok {
				r = '?'
			}
			line = append(line, r)
		}
		if l := strings.TrimSpace(string(line)); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// avgColor is an image's mean R, G and B.
func avgColor(data []byte) [3]float64 {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return [3]float64{}
	}
	var sum [3]float64
	b := img.Bounds()
	n := 0.0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			sum[0], sum[1], sum[2] = sum[0]+float64(r), sum[1]+float64(g), sum[2]+float64(bl)
			n++
		}
	}
	return [3]float64{sum[0] / n, sum[1] / n, sum[2] / n}
}

// pdf is a minimal PDF writer: numbered objects, a cross-reference table and a trailer.
type pdf struct {
	objs []string
}

func (p *pdf) add(body string) int {
	p.objs = append(p.objs, body)
	return len(p.objs)
}

func (p *pdf) set(num int, body string) { p.objs[num-1] = body }

func stream(dict string, data []byte) string {
	return fmt.Sprintf("<< %s /Length %d >>\nstream\n%s\nendstream", dict, len(data), data)
}

func deflate(b []byte) []byte {
	var buf bytes.Buffer
	z := zlib.NewWriter(&buf)
	_, _ = z.Write(b)
	_ = z.Close()
	return buf.Bytes()
}

func (p *pdf) bytes(root int, trailerExtra string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offs := make([]int, len(p.objs))
	for i, o := range p.objs {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(p.objs)+1)
	for _, off := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root %d 0 R%s >>\nstartxref\n%d\n%%%%EOF\n", len(p.objs)+1, root, trailerExtra, xref)
	return b.Bytes()
}

// ImagesPDF is one image-only page per PNG: a scanned document.
func ImagesPDF(pngs [][]byte) []byte {
	w := &pdf{}
	catalog, pages := w.add(""), w.add("")
	var kids []string
	for _, raw := range pngs {
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			panic(err)
		}
		bd := img.Bounds()
		rgb := make([]byte, 0, bd.Dx()*bd.Dy()*3)
		for y := bd.Min.Y; y < bd.Max.Y; y++ {
			for x := bd.Min.X; x < bd.Max.X; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				rgb = append(rgb, byte(r>>8), byte(g>>8), byte(b>>8))
			}
		}
		im := w.add(stream(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode",
			bd.Dx(), bd.Dy()), deflate(rgb)))
		content := w.add(stream("", []byte(fmt.Sprintf("q %d 0 0 %d 0 0 cm /Im0 Do Q", bd.Dx(), bd.Dy()))))
		page := w.add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>",
			pages, bd.Dx(), bd.Dy(), im, content))
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
	}
	w.set(pages, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids)))
	w.set(catalog, fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pages))
	return w.bytes(catalog, "")
}

// ScannedPDF is n image-only pages; page k paints "EXHIBIT SCAN / PAGE k OF n" and two more lines.
func ScannedPDF(n int) []byte {
	var pages [][]byte
	for k := 1; k <= n; k++ {
		pages = append(pages, RenderPage([]string{"EXHIBIT SCAN", fmt.Sprintf("PAGE %d OF %d", k, n), "PLAINTIFF EXHIBIT FOR HEARING", "SYNTHETIC RECORD ONLY"}))
	}
	return ImagesPDF(pages)
}

// LetterLeasePhotos are eight photos: a three-page letter and a five-page lease.
func LetterLeasePhotos() [][]byte {
	var out [][]byte
	for k := 1; k <= 3; k++ {
		out = append(out, RenderPage([]string{"LETTER", fmt.Sprintf("PAGE %d OF 3", k), "DEAR COUNSEL", "SYNTHETIC RECORD ONLY"}))
	}
	for k := 1; k <= 5; k++ {
		out = append(out, RenderPage([]string{"LEASE", fmt.Sprintf("PAGE %d OF 5", k), "RESIDENTIAL LEASE TERMS", "SYNTHETIC RECORD ONLY"}))
	}
	return out
}

// TextPDF is a PDF with a Helvetica text layer, one page per entry, each page's lines.
func TextPDF(pagesText ...[]string) []byte {
	w := &pdf{}
	catalog, pages, fontObj := w.add(""), w.add(""), w.add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	var kids []string
	for _, lines := range pagesText {
		var cs strings.Builder
		cs.WriteString("BT /F1 12 Tf 72 720 Td\n")
		for i, l := range lines {
			if i > 0 {
				cs.WriteString("0 -14 Td\n")
			}
			fmt.Fprintf(&cs, "(%s) Tj\n", strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(l))
		}
		cs.WriteString("ET")
		content := w.add(stream("/Filter /FlateDecode", deflate([]byte(cs.String()))))
		page := w.add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>",
			pages, fontObj, content))
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
	}
	w.set(pages, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids)))
	w.set(catalog, fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pages))
	return w.bytes(catalog, "")
}

var pdfPad = []byte{0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80, 0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A}

func padPassword(p string) []byte { return append([]byte(p), pdfPad...)[:32] }

func rc4Bytes(key, data []byte) []byte {
	c, err := rc4.NewCipher(key)
	if err != nil {
		panic(err)
	}
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

// EncryptedPDF is a Standard security handler (revision 2, RC4 40-bit) PDF whose content
// stream is ciphertext under the user password.
func EncryptedPDF(userPassword string) []byte {
	id := []byte("togavsu-synthetic-id-0001")
	ownerKey := md5.Sum(padPassword("owner-" + userPassword))
	o := rc4Bytes(ownerKey[:5], padPassword(userPassword))
	perms := int32(-44)
	h := md5.New()
	h.Write(padPassword(userPassword))
	h.Write(o)
	_ = binary.Write(h, binary.LittleEndian, perms)
	h.Write(id)
	key := h.Sum(nil)[:5]
	u := rc4Bytes(key, pdfPad)
	k4 := md5.Sum(append(append([]byte{}, key...), 4, 0, 0, 0, 0))
	enc := rc4Bytes(k4[:10], []byte("BT /F1 12 Tf 72 720 Td (CONFIDENTIAL SETTLEMENT TERMS) Tj ET"))
	w := &pdf{}
	w.add("<< /Type /Catalog /Pages 2 0 R >>")
	w.add("<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	w.add("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>")
	w.add(stream("", enc))
	w.add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	w.add(fmt.Sprintf("<< /Filter /Standard /V 1 /R 2 /O <%x> /U <%x> /P %d >>", o, u, perms))
	return w.bytes(1, fmt.Sprintf(" /Encrypt 6 0 R /ID [<%x> <%x>]", id, id))
}

// DamagedPDF is a PDF cut off before its page tree.
func DamagedPDF() []byte {
	full := TextPDF([]string{"THIS FILE WAS CUT OFF"})
	return full[:len(full)/4]
}

func le(v any) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, v)
	return b.Bytes()
}

type wavFormat struct {
	AudioFormat   uint16
	Channels      uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
}

const rate = 16000

func tonePCM(seconds []bool) []byte {
	pcm := make([]byte, 0, len(seconds)*rate*2)
	for s, on := range seconds {
		for i := 0; i < rate; i++ {
			v := int16(0)
			if on {
				v = int16(8000 * math.Sin(2*math.Pi*440*float64(s*rate+i)/rate))
			}
			pcm = binary.LittleEndian.AppendUint16(pcm, uint16(v))
		}
	}
	return pcm
}

var monoPCM = wavFormat{AudioFormat: 1, Channels: 1, SampleRate: rate, ByteRate: rate * 2, BlockAlign: 2, BitsPerSample: 16}

// ToneWAV is 16 kHz mono PCM: a tone where on is true, silence elsewhere, one entry per second.
func ToneWAV(seconds []bool) []byte {
	pcm := tonePCM(seconds)
	f := le(monoPCM)
	var b bytes.Buffer
	b.WriteString("RIFF")
	b.Write(le(uint32(4 + 8 + len(f) + 8 + len(pcm))))
	b.WriteString("WAVEfmt ")
	b.Write(le(uint32(len(f))))
	b.Write(f)
	b.WriteString("data")
	b.Write(le(uint32(len(pcm))))
	b.Write(pcm)
	return b.Bytes()
}

// On is n seconds of tone.
func On(n int) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = true
	}
	return out
}

type aviHeader struct {
	MicroSecPerFrame, MaxBytesPerSec, PaddingGranularity, Flags, TotalFrames, InitialFrames, Streams, SuggestedBufferSize, Width, Height uint32
}

type bitmapInfo struct {
	Size                         uint32
	Width, Height                int32
	Planes                       uint16
	BitCount                     uint16
	Compression, SizeImage       uint32
	XPelsPerMeter, YPelsPerMeter int32
	ClrUsed, ClrImportant        uint32
}

// FramesAVI is an uncompressed AVI at one frame a second, solid frames in the given
// colors, with a PCM tone track of the same length.
func FramesAVI(colors []color.RGBA) []byte {
	const w, h = 16, 8
	stride := (w*3 + 3) &^ 3
	chunk := func(id string, body []byte) []byte {
		var b bytes.Buffer
		b.WriteString(id)
		b.Write(le(uint32(len(body))))
		b.Write(body)
		if len(body)%2 == 1 {
			b.WriteByte(0)
		}
		return b.Bytes()
	}
	list := func(kind string, parts ...[]byte) []byte {
		return chunk("LIST", append([]byte(kind), bytes.Join(parts, nil)...))
	}
	avih := le(aviHeader{MicroSecPerFrame: 1000000, TotalFrames: uint32(len(colors)), Streams: 2, Width: w, Height: h})
	vstrh := append([]byte("vids"), make([]byte, 52)...)
	vstrf := le(bitmapInfo{Size: 40, Width: w, Height: h, Planes: 1, BitCount: 24})
	astrh := append([]byte("auds"), make([]byte, 52)...)
	pcm := tonePCM(On(len(colors)))
	per := len(pcm) / len(colors)
	var movi [][]byte
	for k, c := range colors {
		frame := make([]byte, stride*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				o := y*stride + x*3
				frame[o], frame[o+1], frame[o+2] = c.B, c.G, c.R
			}
		}
		movi = append(movi, chunk("00db", frame), chunk("01wb", pcm[k*per:(k+1)*per]))
	}
	hdrl := list("hdrl", chunk("avih", avih), list("strl", chunk("strh", vstrh), chunk("strf", vstrf)),
		list("strl", chunk("strh", astrh), chunk("strf", le(monoPCM))))
	body := append([]byte("AVI "), append(hdrl, list("movi", movi...)...)...)
	return chunk("RIFF", body)
}

// Key is the API key the provider accepts.
const providerKey = "test-openai-key"

// The system prompts' first words, by task. A Doc Tender prompt change that moves these
// fails the corpus run instead of passing it silently.
var prompts = []struct{ task, prefix string }{
	{"ocr", "Transcribe every word of text visible on this page"},
	{"photo", "Describe this photo for a law firm's file."},
	{"grouping", "These photos arrived together in one law firm matter."},
	{"labeling", "You label documents for a law firm."},
	{"categorizing", "You file documents for a law firm."},
}

// Provider is a synthetic model provider on the OpenAI wire. OCR reads the painted
// glyphs from the pixels (or a PDF page's text layer), a photo is described from its
// glyphs or its color, grouping orders photos by the "PAGE n OF m" each shows,
// labeling cites every case number to the page it is on, and transcription segments
// WAV samples by energy. Hooks steer specific answers.
type Provider struct {
	Server *httptest.Server

	mu    sync.Mutex
	calls []string
	// failOCR fails the strong model's OCR of a page whose read contains the key, the
	// given number of times (failure injection).
	failOCR map[string]int
	// label overrides the labeling answer for a body containing the key.
	label map[string]map[string]any
	// ocrSeen records every OCR read, in order.
	ocrSeen []string
	// category overrides the categorizing answer for a body containing the key.
	category map[string]string
}

// NewProvider starts the provider; Close stops it.
func NewProvider() *Provider {
	p := &Provider{failOCR: map[string]int{}, label: map[string]map[string]any{}, category: map[string]string{}}
	p.Server = httptest.NewServer(http.HandlerFunc(p.serve))
	return p
}

// Close stops the server.
func (p *Provider) Close() { p.Server.Close() }

// URL is the provider's base URL.
func (p *Provider) URL() string { return p.Server.URL }

// FailOCR makes the next n strong-model OCR reads of a page containing key fail.
func (p *Provider) FailOCR(key string, n int) {
	p.mu.Lock()
	p.failOCR[key] = n
	p.mu.Unlock()
}

// Label answers labeling for any body containing key with what (what_it_is) at
// confidence conf, dated date.
func (p *Provider) Label(key, what string, conf float64, date string) {
	p.mu.Lock()
	p.label[key] = map[string]any{"what_it_is": what, "what_it_is_confidence": conf, "doc_date": date, "doc_date_confidence": conf,
		"summary": "A synthetic test document.", "fields": []any{}}
	p.mu.Unlock()
}

// Category answers categorizing for any body containing key with category.
func (p *Provider) Category(key, category string) {
	p.mu.Lock()
	p.category[key] = category
	p.mu.Unlock()
}

// Calls are the task:model pairs called so far.
func (p *Provider) Calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

// Count is how many calls start with prefix (a task, or task:model).
func (p *Provider) Count(prefix string) int {
	n := 0
	for _, c := range p.Calls() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// OCRReads are the texts the OCR task read, in call order.
func (p *Provider) OCRReads() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.ocrSeen...)
}

func (p *Provider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/models" {
		_, _ = w.Write([]byte(`{"data":[]}`))
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+providerKey {
		http.Error(w, "bad key", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/chat/completions":
		p.chat(w, r)
	case "/audio/transcriptions":
		p.transcribe(w, r)
	default:
		http.NotFound(w, r)
	}
}

func dataOf(u string) []byte {
	_, b64, _ := strings.Cut(u, ";base64,")
	raw, _ := base64.StdEncoding.DecodeString(b64)
	return raw
}

var (
	reStream = regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	reTj     = regexp.MustCompile(`\(((?:\\.|[^\\)])*)\)\s*Tj`)
)

// pdfText reads a PDF's text layer: every Tj string in every stream, inflated when
// compressed, one per line.
func pdfText(data []byte) string {
	var lines []string
	for _, m := range reStream.FindAllSubmatch(data, -1) {
		body := m[1]
		if z, err := zlib.NewReader(bytes.NewReader(body)); err == nil {
			if inflated, err := io.ReadAll(z); err == nil {
				body = inflated
			}
		}
		for _, t := range reTj.FindAllSubmatch(body, -1) {
			lines = append(lines, strings.NewReplacer(`\(`, "(", `\)`, ")", `\\`, `\`).Replace(string(t[1])))
		}
	}
	return strings.Join(lines, "\n")
}

type chatPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
	File struct {
		Data string `json:"file_data"`
	} `json:"file"`
}

func (p *Provider) chat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var system string
	var parts []chatPart
	for _, m := range req.Messages {
		if m.Role == "system" {
			_ = json.Unmarshal(m.Content, &system)
			continue
		}
		if err := json.Unmarshal(m.Content, &parts); err != nil {
			var s string
			if json.Unmarshal(m.Content, &s) == nil {
				parts = append(parts, chatPart{Type: "text", Text: s})
			}
		}
	}
	var texts []string
	var images, docs [][]byte
	for _, part := range parts {
		switch part.Type {
		case "text":
			texts = append(texts, part.Text)
		case "image_url":
			images = append(images, dataOf(part.ImageURL.URL))
		case "file":
			docs = append(docs, dataOf(part.File.Data))
		}
	}
	task := ""
	for _, pr := range prompts {
		if strings.HasPrefix(system, pr.prefix) {
			task = pr.task
		}
	}
	body := strings.Join(texts, "\n")
	var answer string
	switch task {
	case "ocr":
		read := ""
		if len(images) > 0 {
			read = ReadGlyphs(images[0])
		} else if len(docs) > 0 {
			read = pdfText(docs[0])
		}
		p.mu.Lock()
		p.ocrSeen = append(p.ocrSeen, read)
		fail := false
		if !strings.Contains(req.Model, "fast") {
			for key, n := range p.failOCR {
				if n > 0 && strings.Contains(read, key) {
					p.failOCR[key] = n - 1
					fail = true
				}
			}
		}
		p.mu.Unlock()
		if fail {
			http.Error(w, "injected OCR failure", http.StatusInternalServerError)
			return
		}
		answer = read
	case "photo":
		read := ReadGlyphs(images[0])
		desc := "a photographed page"
		if strings.TrimSpace(strings.ReplaceAll(read, "?", "")) == "" {
			desc = "a solid " + colorName(images[0]) + " frame"
			read = ""
		}
		answer = jsonText(map[string]string{"description": desc, "text": read})
	case "grouping":
		answer = jsonText(groupByPages(texts))
	case "labeling":
		answer = jsonText(p.labelFor(body))
	case "categorizing":
		answer = jsonText(p.categoryFor(body))
	default:
		http.Error(w, "unknown prompt", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.calls = append(p.calls, task+":"+req.Model)
	p.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": answer}}},
		"usage":   map[string]any{"prompt_tokens": len(body)/4 + 50*len(images), "completion_tokens": len(answer) / 4},
	})
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func colorName(img []byte) string {
	c := avgColor(img)
	switch {
	case c[0] > c[1] && c[0] > c[2]:
		return "red"
	case c[1] > c[0] && c[1] > c[2]:
		return "green"
	case c[2] > c[0] && c[2] > c[1]:
		return "blue"
	}
	return "gray"
}

var rePage = regexp.MustCompile(`([A-Z]+)\s*/\s*PAGE (\d+) OF (\d+)`)

type group struct {
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Photos []int  `json:"photos"`
}

// groupByPages groups photos by the document word and page number they show.
func groupByPages(texts []string) map[string][]group {
	type pg struct{ photo, page int }
	docs := map[string][]pg{}
	var order []string
	var loose []int
	n := 0
	for _, t := range texts {
		if !strings.HasPrefix(t, "Photo ") {
			continue
		}
		n++
		m := rePage.FindStringSubmatch(t)
		if m == nil {
			loose = append(loose, n)
			continue
		}
		var page int
		fmt.Sscan(m[2], &page)
		if _, ok := docs[m[1]]; !ok {
			order = append(order, m[1])
		}
		docs[m[1]] = append(docs[m[1]], pg{n, page})
	}
	var out []group
	for _, d := range order {
		ps := docs[d]
		sort.Slice(ps, func(a, b int) bool { return ps[a].page < ps[b].page })
		var nums []int
		for _, x := range ps {
			nums = append(nums, x.photo)
		}
		out = append(out, group{strings.ToLower(d), "document", nums})
	}
	for _, l := range loose {
		out = append(out, group{"photo", "photo", []int{l}})
	}
	return map[string][]group{"groups": out}
}

var (
	reCase    = regexp.MustCompile(`\b\d{2}[A-Z]{2}-[A-Z]{2}\d+\b`)
	reISODate = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)
	reHeading = regexp.MustCompile(`(?m)^## Page (\d+)$`)
)

// labelFor names the document from its first line, dates it from its first ISO date,
// and cites every case number to the page it is on.
func (p *Provider) labelFor(body string) map[string]any {
	p.mu.Lock()
	for key, l := range p.label {
		if strings.Contains(body, key) {
			p.mu.Unlock()
			return l
		}
	}
	p.mu.Unlock()
	what, date := "", ""
	var fields []any
	page := 1
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if m := reHeading.FindStringSubmatch(line); m != nil {
			fmt.Sscan(m[1], &page)
			continue
		}
		if line == "" || line == "<document>" || line == "</document>" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "_") || strings.HasPrefix(line, "[") {
			continue
		}
		if what == "" {
			w := strings.Fields(strings.ToLower(line))
			if len(w) > 4 {
				w = w[:4]
			}
			what = strings.Join(w, " ")
		}
		if d := reISODate.FindString(line); d != "" && date == "" {
			date = d
		}
		for _, c := range reCase.FindAllString(line, -1) {
			pg := page
			fields = append(fields, map[string]any{"name": "case_number", "value": c, "confidence": 0.99, "citation": map[string]any{"page": pg, "timestamp": nil}})
		}
	}
	if what == "" {
		what = "recording"
		if strings.Contains(body, "On screen") {
			what = "video recording"
		}
	}
	if fields == nil {
		fields = []any{}
	}
	return map[string]any{"what_it_is": what, "what_it_is_confidence": 0.97, "doc_date": date, "doc_date_confidence": 0.96,
		"summary": "A synthetic test document.", "fields": fields}
}

func (p *Provider) categoryFor(body string) map[string]any {
	p.mu.Lock()
	for key, c := range p.category {
		if strings.Contains(body, key) {
			p.mu.Unlock()
			return map[string]any{"category": c, "confidence": 0.95}
		}
	}
	p.mu.Unlock()
	lower := strings.ToLower(body)
	cat := "reference"
	switch {
	case strings.Contains(lower, "motion") || strings.Contains(lower, "notice of hearing"):
		cat = "court_filing"
	case strings.Contains(lower, "letter"):
		cat = "correspondence"
	case strings.Contains(lower, "lease"):
		cat = "contracts_agreements"
	case strings.Contains(lower, "exhibit"):
		cat = "police_criminal"
	case strings.Contains(lower, "said:") || strings.Contains(lower, "on screen") || strings.Contains(lower, "steady tone"):
		cat = "call_meeting"
	case strings.Contains(lower, "synthetic memo"):
		cat = "firm_work_product"
	}
	return map[string]any{"category": cat, "confidence": 0.95}
}

// transcribe segments the WAV by energy in one-second windows: a run of sound is one
// "steady tone" segment; silence makes none.
func (p *Provider) transcribe(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	model := r.FormValue("model")
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, _ := io.ReadAll(file)
	pcm, byteRate := wavPCM(data)
	if byteRate == 0 {
		http.Error(w, "not a WAV", http.StatusBadRequest)
		return
	}
	type segment struct {
		Start   float64 `json:"start"`
		End     float64 `json:"end"`
		Speaker string  `json:"speaker"`
		Text    string  `json:"text"`
	}
	segs := []segment{}
	start := -1
	secs := len(pcm) / byteRate
	for s := 0; s <= secs; s++ {
		loud := false
		if s < secs {
			var sum float64
			win := pcm[s*byteRate : (s+1)*byteRate]
			for i := 0; i+1 < len(win); i += 2 {
				v := float64(int16(binary.LittleEndian.Uint16(win[i:])))
				sum += v * v
			}
			loud = math.Sqrt(sum/float64(len(win)/2)) > 500
		}
		if loud && start < 0 {
			start = s
		}
		if !loud && start >= 0 {
			segs = append(segs, segment{float64(start), float64(s), "Speaker 1", "steady tone"})
			start = -1
		}
	}
	p.mu.Lock()
	p.calls = append(p.calls, "transcription:"+model)
	p.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"text": "steady tone", "duration": float64(len(pcm)) / float64(byteRate), "segments": segs})
}

// wavPCM returns a WAV's sample bytes and byte rate (zero when it is not a WAV).
func wavPCM(data []byte) ([]byte, int) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0
	}
	var pcm []byte
	rate := 0
	for o := 12; o+8 <= len(data); {
		id, size := string(data[o:o+4]), int(binary.LittleEndian.Uint32(data[o+4:]))
		body := data[o+8 : min(o+8+size, len(data))]
		switch id {
		case "fmt ":
			if len(body) >= 12 {
				rate = int(binary.LittleEndian.Uint32(body[8:]))
			}
		case "data":
			pcm = body
		}
		o += 8 + size + size%2
	}
	return pcm, rate
}
