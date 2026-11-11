package doctender

import (
	"bytes"
	"crypto/md5"
	"crypto/rc4"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The generated regression corpus. Every byte is synthetic. Pages and photos paint
// their text in a 5x7 glyph font, so a reader that can't see the pixels can't read them.

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

// renderPage paints uppercase lines into a white RGB PNG.
func renderPage(lines []string) []byte {
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
				panic(fmt.Sprintf("no glyph for %q", r))
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

// readGlyphs reads text painted by renderPage from image pixels. It is the test
// provider's OCR: it sees only the pixels.
func readGlyphs(data []byte) string {
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

// scannedPDF is n image-only pages; page k paints "EXHIBIT SCAN / PAGE k OF n" and two more lines.
func scannedPDF(t testing.TB, n int) []byte {
	t.Helper()
	var pages [][]byte
	for k := 1; k <= n; k++ {
		pages = append(pages, renderPage([]string{"EXHIBIT SCAN", fmt.Sprintf("PAGE %d OF %d", k, n), "PLAINTIFF EXHIBIT FOR HEARING", "SYNTHETIC RECORD ONLY"}))
	}
	pdf, err := imagesToPDF(pages)
	if err != nil {
		t.Fatal(err)
	}
	return pdf
}

// letterLeasePhotos are the eight photos: a three-page letter and a five-page lease.
func letterLeasePhotos() [][]byte {
	var out [][]byte
	for k := 1; k <= 3; k++ {
		out = append(out, renderPage([]string{"LETTER", fmt.Sprintf("PAGE %d OF 3", k), "DEAR COUNSEL", "SYNTHETIC RECORD ONLY"}))
	}
	for k := 1; k <= 5; k++ {
		out = append(out, renderPage([]string{"LEASE", fmt.Sprintf("PAGE %d OF 5", k), "RESIDENTIAL LEASE TERMS", "SYNTHETIC RECORD ONLY"}))
	}
	return out
}

// textPDF is a one-page PDF with a Helvetica text layer.
func textPDF(lines ...string) []byte {
	w := &pdfWriter{}
	catalog, pages, page, content, fontObj := w.reserve(), w.reserve(), w.reserve(), w.reserve(), w.reserve()
	var cs strings.Builder
	cs.WriteString("BT /F1 12 Tf 72 720 Td\n")
	for i, l := range lines {
		if i > 0 {
			cs.WriteString("0 -14 Td\n")
		}
		fmt.Fprintf(&cs, "(%s) Tj\n", strings.NewReplacer("(", `\(`, ")", `\)`).Replace(l))
	}
	cs.WriteString("ET")
	w.set(content, &pdfStream{Dict: pdfDict{"Filter": pdfName("FlateDecode")}, Raw: zlibBytes([]byte(cs.String()))})
	w.set(fontObj, pdfDict{"Type": pdfName("Font"), "Subtype": pdfName("Type1"), "BaseFont": pdfName("Helvetica")})
	w.set(page, pdfDict{"Type": pdfName("Page"), "Parent": pdfRef{pages, 0}, "MediaBox": pdfArray{0, 0, 612, 792},
		"Resources": pdfDict{"Font": pdfDict{"F1": pdfRef{fontObj, 0}}}, "Contents": pdfRef{content, 0}})
	w.set(pages, pdfDict{"Type": pdfName("Pages"), "Kids": pdfArray{pdfRef{page, 0}}, "Count": 1})
	w.set(catalog, pdfDict{"Type": pdfName("Catalog"), "Pages": pdfRef{pages, 0}})
	return w.bytes(catalog)
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

// encryptedPDF is a real Standard security handler (revision 2, RC4 40-bit) PDF: its
// content stream is ciphertext under the user password, so a reader that ignores the
// encryption reads nothing.
func encryptedPDF(userPassword string) []byte {
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
	objKey := func(num int) []byte {
		k := md5.Sum(append(append([]byte{}, key...), byte(num), byte(num>>8), byte(num>>16), 0, 0))
		return k[:10]
	}
	content := []byte("BT /F1 12 Tf 72 720 Td (CONFIDENTIAL SETTLEMENT TERMS) Tj ET")
	var b bytes.Buffer
	offs := []int{}
	obj := func(num int, body string) {
		offs = append(offs, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	b.WriteString("%PDF-1.4\n")
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>")
	enc := rc4Bytes(objKey(4), content)
	offs = append(offs, b.Len())
	fmt.Fprintf(&b, "4 0 obj\n<< /Length %d >>\nstream\n", len(enc))
	b.Write(enc)
	b.WriteString("\nendstream\nendobj\n")
	obj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	obj(6, fmt.Sprintf("<< /Filter /Standard /V 1 /R 2 /O <%x> /U <%x> /P %d >>", o, u, perms))
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 7\n0000000000 65535 f \n")
	for _, off := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 7 /Root 1 0 R /Encrypt 6 0 R /ID [<%x> <%x>] >>\nstartxref\n%d\n%%%%EOF\n", id, id, xref)
	return b.Bytes()
}

// damagedPDF is a PDF cut off before its page tree.
func damagedPDF() []byte {
	full := textPDF("THIS FILE WAS CUT OFF")
	return full[:len(full)/4]
}

// toneWAV is 16 kHz mono PCM: a tone where on is true, silence elsewhere, one entry
// per second.
func toneWAV(seconds []bool) []byte {
	const rate = 16000
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
	return wavBytes(wavFormat{AudioFormat: 1, Channels: 1, SampleRate: rate, ByteRate: rate * 2, BlockAlign: 2, BitsPerSample: 16}, pcm)
}

func onFor(n int) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = true
	}
	return out
}

// framesAVI is an uncompressed AVI at one frame a second, solid frames in the given
// colors, with a PCM tone track of the same length.
func framesAVI(colors []color.RGBA) []byte {
	const w, h = 16, 8
	stride := (w*3 + 3) &^ 3
	chunk := func(id string, body []byte) []byte {
		var b bytes.Buffer
		b.WriteString(id)
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(body)))
		b.Write(body)
		if len(body)%2 == 1 {
			b.WriteByte(0)
		}
		return b.Bytes()
	}
	list := func(kind string, parts ...[]byte) []byte {
		return chunk("LIST", append([]byte(kind), bytes.Join(parts, nil)...))
	}
	le := func(v any) []byte {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.LittleEndian, v)
		return b.Bytes()
	}
	avih := le(aviHeader{MicroSecPerFrame: 1000000, TotalFrames: uint32(len(colors)), Streams: 2, Width: w, Height: h})
	vstrh := append([]byte("vids"), make([]byte, 52)...)
	vstrf := le(bitmapInfo{Size: 40, Width: w, Height: h, Planes: 1, BitCount: 24})
	afmt := wavFormat{AudioFormat: 1, Channels: 1, SampleRate: 16000, ByteRate: 32000, BlockAlign: 2, BitsPerSample: 16}
	astrh := append([]byte("auds"), make([]byte, 52)...)
	var movi [][]byte
	wav := toneWAV(onFor(len(colors)))
	var pcm []byte
	_ = riffChunks(wav, "WAVE", func(id string, body []byte) error {
		if id == "data" {
			pcm = body
		}
		return nil
	})
	per := len(pcm) / len(colors)
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
		list("strl", chunk("strh", astrh), chunk("strf", le(afmt))))
	body := append([]byte("AVI "), append(hdrl, list("movi", movi...)...)...)
	return chunk("RIFF", body)
}

// TestWriteCorpus writes the corpus to DOCTENDER_CORPUS_DIR for inspection.
func TestWriteCorpus(t *testing.T) {
	dir := os.Getenv("DOCTENDER_CORPUS_DIR")
	if dir == "" {
		t.Skip("set DOCTENDER_CORPUS_DIR to write the corpus")
	}
	write := func(name string, b []byte) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("legacy/scanned-120.pdf", scannedPDF(t, 120))
	for k, p := range letterLeasePhotos() {
		write(fmt.Sprintf("legacy/merged-then-split/page-%02d.png", k+1), p)
	}
	write("exceptions/password.pdf", encryptedPDF("open-sesame"))
	write("exceptions/damaged.pdf", damagedPDF())
	write("audio/tone.wav", toneWAV(onFor(1)))
	write("video/frames.avi", framesAVI([]color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}}))
}

func TestCorpusIsWhatItSays(t *testing.T) {
	pdf := scannedPDF(t, 120)
	doc, err := parsePDF(pdf)
	if err != nil {
		t.Fatal(err)
	}
	if doc.pageCount() != 120 {
		t.Fatalf("pages = %d", doc.pageCount())
	}
	for _, k := range []int{0, 48, 119} {
		text, err := doc.pageText(k)
		if err != nil || usableText(text) {
			t.Fatalf("page %d has a text layer %q (%v)", k+1, text, err)
		}
		img, _, ok := doc.pageImage(k)
		if !ok {
			t.Fatalf("page %d has no image", k+1)
		}
		if got, want := readGlyphs(img), fmt.Sprintf("EXHIBIT SCAN\nPAGE %d OF 120\nPLAINTIFF EXHIBIT FOR HEARING\nSYNTHETIC RECORD ONLY", k+1); got != want {
			t.Fatalf("page %d reads %q, want %q", k+1, got, want)
		}
	}
	if _, err := parsePDF(encryptedPDF("pw")); err != ErrEncrypted {
		t.Fatalf("encrypted PDF: %v", err)
	}
	if bytes.Contains(encryptedPDF("pw"), []byte("CONFIDENTIAL")) {
		t.Fatal("the encrypted PDF's content is plaintext")
	}
	if _, err := parsePDF(damagedPDF()); err == nil {
		t.Fatal("the damaged PDF opened")
	}
	d, err := wavDuration(toneWAV(onFor(1)))
	if err != nil || d != secondUnit {
		t.Fatalf("tone.wav = %v %v", d, err)
	}
	v, err := readAVI(framesAVI([]color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}}))
	if err != nil {
		t.Fatalf("frames.avi: %v", err)
	}
	frames := 0
	if err := v.frames(0, v.total, secondUnit, func(Frame) error { frames++; return nil }); err != nil || frames != 3 || v.audio == nil || v.total != 3*secondUnit {
		t.Fatalf("frames.avi: %d frames, audio %v, %v, %v", frames, v.audio != nil, v.total, err)
	}
	tp, err := parsePDF(textPDF("NOTICE OF HEARING", "Case 23AR-CV00825"))
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := tp.pageText(0); !strings.Contains(text, "23AR-CV00825") {
		t.Fatalf("text layer %q", text)
	}
}

// readPixelsAvg is an image's mean R, G and B.
func readPixelsAvg(data []byte) [3]float64 {
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
