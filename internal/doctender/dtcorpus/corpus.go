// Package dtcorpus is the generated Doc Tender regression corpus and a synthetic model
// provider on the OpenAI wire, for tests outside package doctender (the running core's
// phase 4 cases). Every byte is synthetic. Pages and photos paint their text in a 5x7
// glyph font, so a reader that can't see the pixels can't read them; the provider reads
// only what it is sent. Nothing in the product imports this package.
//
// It mirrors internal/doctender/corpus_test.go and provider_test.go, which stay inside
// that package because they reach unexported helpers.
package dtcorpus

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"crypto/rc4"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
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
