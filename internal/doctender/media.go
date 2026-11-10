package doctender

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"  // photos arrive in common formats
	_ "image/jpeg" // photos arrive as JPEG
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// imagesToPDF writes one page per photo, in order, each page the photo's size. JPEG
// bytes are embedded as they are; other formats are embedded as Flate-compressed RGB.
func imagesToPDF(photos [][]byte) ([]byte, error) {
	w := &pdfWriter{}
	catalog, pages := w.reserve(), w.reserve()
	var kids pdfArray
	for k, raw := range photos {
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("photo %d: %w", k+one, err)
		}
		img := &pdfStream{Dict: pdfDict{
			"Type": pdfName("XObject"), "Subtype": pdfName("Image"), "Width": cfg.Width, "Height": cfg.Height,
			"BitsPerComponent": eight,
		}}
		if format == "jpeg" {
			img.Dict["Filter"], img.Raw = pdfName("DCTDecode"), raw
			img.Dict["ColorSpace"] = pdfName("DeviceRGB")
			if cfg.ColorModel == color.GrayModel {
				img.Dict["ColorSpace"] = pdfName("DeviceGray")
			}
		} else {
			decoded, _, err := image.Decode(bytes.NewReader(raw))
			if err != nil {
				return nil, fmt.Errorf("photo %d: %w", k+one, err)
			}
			img.Dict["Filter"], img.Dict["ColorSpace"] = pdfName("FlateDecode"), pdfName("DeviceRGB")
			img.Raw = flateRGB(decoded)
		}
		imgNum := w.reserve()
		w.set(imgNum, img)
		content := fmt.Sprintf("q %d 0 0 %d 0 0 cm /Im0 Do Q", cfg.Width, cfg.Height)
		cNum := w.reserve()
		w.set(cNum, &pdfStream{Dict: pdfDict{}, Raw: []byte(content)})
		pNum := w.reserve()
		w.set(pNum, pdfDict{
			"Type": pdfName("Page"), "Parent": pdfRef{pages, zero},
			"MediaBox":  pdfArray{zero, zero, cfg.Width, cfg.Height},
			"Resources": pdfDict{"XObject": pdfDict{"Im0": pdfRef{imgNum, zero}}},
			"Contents":  pdfRef{cNum, zero},
		})
		kids = append(kids, pdfRef{pNum, zero})
	}
	w.set(pages, pdfDict{"Type": pdfName("Pages"), "Kids": kids, "Count": len(kids)})
	w.set(catalog, pdfDict{"Type": pdfName("Catalog"), "Pages": pdfRef{pages, zero}})
	return w.bytes(catalog), nil
}

func flateRGB(img image.Image) []byte {
	b := img.Bounds()
	raw := make([]byte, zero, b.Dx()*b.Dy()*three)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			raw = append(raw, c.R, c.G, c.B)
		}
	}
	return zlibBytes(raw)
}

// imageAsPNG re-encodes any decodable photo as PNG (for Tesseract and models that do
// not take every format).
func imageAsPNG(raw []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// riffChunk is a RIFF chunk header.
type riffChunk struct {
	ID   [4]byte
	Size uint32
}

// wavFormat is the fmt chunk of a WAV file.
type wavFormat struct {
	AudioFormat   uint16
	Channels      uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
}

// riffChunks walks a RIFF file's top-level chunks (after the form type) and calls fn.
func riffChunks(data []byte, form string, fn func(id string, body []byte) error) error {
	r := bytes.NewReader(data)
	var head riffChunk
	if err := binary.Read(r, binary.LittleEndian, &head); err != nil || string(head.ID[:]) != "RIFF" {
		return errors.New("not a RIFF file")
	}
	var kind [4]byte
	if _, err := io.ReadFull(r, kind[:]); err != nil || string(kind[:]) != form {
		return fmt.Errorf("not a %s file", strings.TrimSpace(form))
	}
	return walkChunks(r, fn)
}

func walkChunks(r *bytes.Reader, fn func(id string, body []byte) error) error {
	for r.Len() > zero {
		var c riffChunk
		if err := binary.Read(r, binary.LittleEndian, &c); err != nil {
			return nil
		}
		if int64(c.Size) > int64(r.Len()) {
			return fmt.Errorf("chunk %s runs past the end", string(c.ID[:]))
		}
		body := make([]byte, c.Size)
		if _, err := io.ReadFull(r, body); err != nil {
			return err
		}
		if c.Size%uint32(two) == uint32(one) {
			_, _ = r.ReadByte()
		}
		if err := fn(string(c.ID[:]), body); err != nil {
			return err
		}
	}
	return nil
}

// wavDuration reads a PCM WAV's length.
func wavDuration(data []byte) (time.Duration, error) {
	var f wavFormat
	var samples int64
	var haveFmt bool
	err := riffChunks(data, "WAVE", func(id string, body []byte) error {
		switch id {
		case "fmt ":
			haveFmt = true
			return binary.Read(bytes.NewReader(body), binary.LittleEndian, &f)
		case "data":
			samples = int64(len(body))
		}
		return nil
	})
	if err != nil {
		return zeroDuration, err
	}
	if !haveFmt || f.ByteRate == uint32(zero) {
		return zeroDuration, errors.New("WAV has no format")
	}
	return time.Duration(float64(samples) / float64(f.ByteRate) * float64(secondUnit)), nil
}

const zeroDuration = time.Duration(zero)

// aviHeader is the avih main header.
type aviHeader struct {
	MicroSecPerFrame    uint32
	MaxBytesPerSec      uint32
	PaddingGranularity  uint32
	Flags               uint32
	TotalFrames         uint32
	InitialFrames       uint32
	Streams             uint32
	SuggestedBufferSize uint32
	Width               uint32
	Height              uint32
}

// bitmapInfo is a video stream's strf BITMAPINFOHEADER.
type bitmapInfo struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// Frame is one video frame at a time offset.
type Frame struct {
	At  time.Duration
	PNG []byte
}

// aviFrames decodes an uncompressed (BI_RGB, 24-bit) AVI's frames, one every interval.
// Other codecs return an error; the ffmpeg tool reads those.
func aviFrames(data []byte, interval time.Duration) ([]Frame, time.Duration, error) {
	frames, _, total, err := readAVI(data, interval)
	return frames, total, err
}

// readAVI decodes an uncompressed AVI's frames and its PCM audio track as WAV bytes
// (nil when it has none).
func readAVI(data []byte, interval time.Duration) ([]Frame, []byte, time.Duration, error) {
	var hdr aviHeader
	var info bitmapInfo
	var audioFmt wavFormat
	var frames [][]byte
	var pcm []byte
	var streamType string
	var visit func(id string, body []byte) error
	visit = func(id string, body []byte) error {
		switch {
		case id == "LIST" && len(body) >= four:
			return walkChunks(bytes.NewReader(body[four:]), visit)
		case id == "avih":
			return binary.Read(bytes.NewReader(body), binary.LittleEndian, &hdr)
		case id == "strh" && len(body) >= four:
			streamType = string(body[:four])
		case id == "strf" && streamType == "vids":
			return binary.Read(bytes.NewReader(body), binary.LittleEndian, &info)
		case id == "strf" && streamType == "auds":
			return binary.Read(bytes.NewReader(body), binary.LittleEndian, &audioFmt)
		case strings.HasSuffix(id, "db") || strings.HasSuffix(id, "dc"):
			frames = append(frames, body)
		case strings.HasSuffix(id, "wb"):
			pcm = append(pcm, body...)
		}
		return nil
	}
	if err := riffChunks(data, "AVI ", visit); err != nil {
		return nil, nil, zeroDuration, err
	}
	var audio []byte
	if len(pcm) > zero && audioFmt.ByteRate != uint32(zero) {
		audio = wavBytes(audioFmt, pcm)
	}
	out, total, err := decodeFrames(hdr, info, frames, interval)
	return out, audio, total, err
}

// wavBytes wraps PCM samples in a WAV file.
func wavBytes(f wavFormat, pcm []byte) []byte {
	var b bytes.Buffer
	var fmtBody bytes.Buffer
	_ = binary.Write(&fmtBody, binary.LittleEndian, f)
	size := uint32(four + eight + fmtBody.Len() + eight + len(pcm))
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, size)
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(fmtBody.Len()))
	b.Write(fmtBody.Bytes())
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(pcm)))
	b.Write(pcm)
	return b.Bytes()
}

func decodeFrames(hdr aviHeader, info bitmapInfo, frames [][]byte, interval time.Duration) ([]Frame, time.Duration, error) {
	if info.Compression != uint32(zero) || info.BitCount != uint16(three*eight) {
		return nil, zeroDuration, errors.New("AVI codec is not uncompressed RGB")
	}
	per := time.Duration(hdr.MicroSecPerFrame) * microUnit
	total := per * time.Duration(len(frames))
	w, h := int(info.Width), int(info.Height)
	bottomUp := h > zero
	if !bottomUp {
		h = -h
	}
	stride := (w*three + three) &^ three
	var out []Frame
	next := zeroDuration
	for k, raw := range frames {
		at := per * time.Duration(k)
		if at < next || len(raw) < stride*h {
			continue
		}
		img := image.NewRGBA(image.Rect(zero, zero, w, h))
		for y := zero; y < h; y++ {
			row := y
			if bottomUp {
				row = h - one - y
			}
			for x := zero; x < w; x++ {
				o := row*stride + x*three
				img.Set(x, y, color.RGBA{raw[o+two], raw[o+one], raw[o], math.MaxUint8})
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, zeroDuration, err
		}
		out = append(out, Frame{At: at, PNG: buf.Bytes()})
		next = at + interval
	}
	return out, total, nil
}

// MediaTool reads audio and video the native readers don't: duration, the audio
// track and frames. FFmpeg implements it.
type MediaTool interface {
	Duration(ctx context.Context, path string) (time.Duration, error)
	Audio(ctx context.Context, path string) ([]byte, error)
	Frames(ctx context.Context, path string, interval time.Duration) ([]Frame, error)
}

// FFmpeg runs ffprobe and ffmpeg from the core image.
type FFmpeg struct {
	FFmpeg, FFprobe string
}

func (f FFmpeg) bin(name, set string) string {
	if set != "" {
		return set
	}
	return name
}

// Duration implements MediaTool.
func (f FFmpeg) Duration(ctx context.Context, path string) (time.Duration, error) {
	out, err := exec.CommandContext(ctx, f.bin("ffprobe", f.FFprobe), "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return zeroDuration, fmt.Errorf("ffprobe: %w", err)
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return zeroDuration, fmt.Errorf("ffprobe duration: %w", err)
	}
	return time.Duration(secs * float64(secondUnit)), nil
}

// Audio implements MediaTool: the first audio track as 16 kHz mono WAV, or nil when
// there is none.
func (f FFmpeg) Audio(ctx context.Context, path string) ([]byte, error) {
	probe, err := exec.CommandContext(ctx, f.bin("ffprobe", f.FFprobe), "-v", "error", "-select_streams", "a",
		"-show_entries", "stream=index", "-of", "csv=p=0", path).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	if strings.TrimSpace(string(probe)) == "" {
		return nil, nil
	}
	out, err := exec.CommandContext(ctx, f.bin("ffmpeg", f.FFmpeg), "-v", "error", "-i", path, "-vn", "-ac", "1",
		"-ar", "16000", "-f", "wav", "pipe:1").Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg audio: %w", err)
	}
	return out, nil
}

// Frames implements MediaTool: one PNG frame per interval.
func (f FFmpeg) Frames(ctx context.Context, path string, interval time.Duration) ([]Frame, error) {
	dir, err := os.MkdirTemp("", "doctender-frames-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	rate := fmt.Sprintf("%g", float64(secondUnit)/float64(interval))
	cmd := exec.CommandContext(ctx, f.bin("ffmpeg", f.FFmpeg), "-v", "error", "-i", path, "-vf", "fps="+rate,
		filepath.Join(dir, "frame-%06d.png"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg frames: %w: %s", err, strings.TrimSpace(string(out)))
	}
	names, err := filepath.Glob(filepath.Join(dir, "frame-*.png"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var frames []Frame
	for k, n := range names {
		raw, err := os.ReadFile(n)
		if err != nil {
			return nil, err
		}
		frames = append(frames, Frame{At: interval * time.Duration(k), PNG: raw})
	}
	return frames, nil
}

// SecondReader is the independent OCR read of a scanned page. Tesseract implements it.
type SecondReader interface {
	Name() string
	Read(ctx context.Context, img []byte) (string, error)
}

// Tesseract runs the tesseract binary that ships in the core image.
type Tesseract struct {
	Path string
}

// Name implements SecondReader.
func (t Tesseract) Name() string { return "tesseract" }

// Read implements SecondReader on PNG or JPEG bytes.
func (t Tesseract) Read(ctx context.Context, img []byte) (string, error) {
	bin := t.Path
	if bin == "" {
		bin = "tesseract"
	}
	cmd := exec.CommandContext(ctx, bin, "stdin", "stdout")
	cmd.Stdin = bytes.NewReader(img)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tesseract: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return tidyLines(string(out)), nil
}

// DocConverter converts Word-family documents to Markdown and reports the document's
// own counts. LawCraft's wordcraft-cli implements it in the core image.
type DocConverter interface {
	ToMarkdown(ctx context.Context, name string, data []byte) (markdown string, words, pages int, err error)
}

// WordcraftCLI runs wordcraft-cli convert. It is pending until the editor task ships
// the binary at the pinned LawCraft commit.
type WordcraftCLI struct {
	Path string
}

// ToMarkdown implements DocConverter: wordcraft-cli convert --to md, then
// wordcraft-cli stats --json for the document's own word and page counts.
func (w WordcraftCLI) ToMarkdown(ctx context.Context, name string, data []byte) (string, int, int, error) {
	bin := w.Path
	if bin == "" {
		bin = "wordcraft-cli"
	}
	dir, err := os.MkdirTemp("", "doctender-doc-")
	if err != nil {
		return "", zero, zero, err
	}
	defer os.RemoveAll(dir)
	f, err := os.CreateTemp(dir, "in-*"+filepath.Ext(name))
	if err != nil {
		return "", zero, zero, err
	}
	in := f.Name()
	_, werr := f.Write(data)
	if cerr := f.Close(); werr != nil || cerr != nil {
		return "", zero, zero, errors.Join(werr, cerr)
	}
	md, err := exec.CommandContext(ctx, bin, "convert", "--to", "md", in, "-o", "-").Output()
	if err != nil {
		return "", zero, zero, fmt.Errorf("wordcraft-cli convert: %w", err)
	}
	stats, err := exec.CommandContext(ctx, bin, "stats", "--json", in).Output()
	if err != nil {
		return "", zero, zero, fmt.Errorf("wordcraft-cli stats: %w", err)
	}
	var s struct {
		Words int `json:"words"`
		Pages int `json:"pages"`
	}
	if err := jsonUnmarshal(stats, &s); err != nil {
		return "", zero, zero, fmt.Errorf("wordcraft-cli stats: %w", err)
	}
	return string(md), s.Words, s.Pages, nil
}
