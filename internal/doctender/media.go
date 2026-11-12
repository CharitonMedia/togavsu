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
			if err := checkPixels(raw); err != nil {
				return nil, fmt.Errorf("photo %d: %w", k+one, err)
			}
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
	if err := checkPixels(raw); err != nil {
		return nil, err
	}
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

// Frame is one still from a video: when it is, and the image (PNG from the native
// reader, JPEG from ffmpeg).
type Frame struct {
	At        time.Duration
	Data      []byte
	MediaType string
}

// aviVideo is an uncompressed (BI_RGB, 24-bit) AVI read in place: its frames stay
// slices of the file and are decoded one at a time, so a long recording is never
// held decoded.
type aviVideo struct {
	hdr   aviHeader
	info  bitmapInfo
	raw   [][]byte
	audio []byte // the PCM track as WAV, nil when there is none
	total time.Duration
}

// readAVI opens an uncompressed AVI. Other codecs return an error; ffmpeg reads those.
func readAVI(data []byte) (*aviVideo, error) {
	v := &aviVideo{}
	var audioFmt wavFormat
	var pcm []byte
	var streamType string
	var visit func(id string, body []byte) error
	visit = func(id string, body []byte) error {
		switch {
		case id == "LIST" && len(body) >= four:
			return walkChunks(bytes.NewReader(body[four:]), visit)
		case id == "avih":
			return binary.Read(bytes.NewReader(body), binary.LittleEndian, &v.hdr)
		case id == "strh" && len(body) >= four:
			streamType = string(body[:four])
		case id == "strf" && streamType == "vids":
			return binary.Read(bytes.NewReader(body), binary.LittleEndian, &v.info)
		case id == "strf" && streamType == "auds":
			return binary.Read(bytes.NewReader(body), binary.LittleEndian, &audioFmt)
		case strings.HasSuffix(id, "db") || strings.HasSuffix(id, "dc"):
			v.raw = append(v.raw, body)
		case strings.HasSuffix(id, "wb"):
			pcm = append(pcm, body...)
		}
		return nil
	}
	if err := riffChunks(data, "AVI ", visit); err != nil {
		return nil, err
	}
	if v.info.Compression != uint32(zero) || v.info.BitCount != uint16(three*eight) {
		return nil, errors.New("AVI codec is not uncompressed RGB")
	}
	if len(pcm) > zero && audioFmt.ByteRate != uint32(zero) {
		v.audio = wavBytes(audioFmt, pcm)
	}
	v.total = v.per() * time.Duration(len(v.raw))
	return v, nil
}

func (v *aviVideo) per() time.Duration { return time.Duration(v.hdr.MicroSecPerFrame) * microUnit }

// frames decodes one frame every interval in [from, to) and hands each to each
// before it decodes the next.
func (v *aviVideo) frames(from, to, interval time.Duration, each func(Frame) error) error {
	w, h := int(v.info.Width), int(v.info.Height)
	bottomUp := h > zero
	if !bottomUp {
		h = -h
	}
	stride := (w*three + three) &^ three
	next := from
	for k, raw := range v.raw {
		at := v.per() * time.Duration(k)
		if at < next || at >= to || len(raw) < stride*h {
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
			return err
		}
		if err := each(Frame{At: at, Data: buf.Bytes(), MediaType: "image/png"}); err != nil {
			return err
		}
		next = at + interval
	}
	return nil
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

// MediaTool reads the audio and video the native readers don't: the length, whether
// there is a sound track, a stretch of the sound as 16 kHz mono WAV, and a stretch's
// frames one at a time. FFmpeg implements it.
type MediaTool interface {
	Duration(ctx context.Context, path string) (time.Duration, error)
	HasAudio(ctx context.Context, path string) (bool, error)
	Audio(ctx context.Context, path string, from, length time.Duration) ([]byte, error)
	Frames(ctx context.Context, path string, from, length, interval time.Duration, each func(Frame) error) error
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

// HasAudio implements MediaTool.
func (f FFmpeg) HasAudio(ctx context.Context, path string) (bool, error) {
	probe, err := exec.CommandContext(ctx, f.bin("ffprobe", f.FFprobe), "-v", "error", "-select_streams", "a",
		"-show_entries", "stream=index", "-of", "csv=p=0", path).Output()
	if err != nil {
		return false, fmt.Errorf("ffprobe: %w", err)
	}
	return strings.TrimSpace(string(probe)) != "", nil
}

// Audio implements MediaTool: length of the first sound track from from, as mono
// WAV at wire().SpeechSampleRate.
func (f FFmpeg) Audio(ctx context.Context, path string, from, length time.Duration) ([]byte, error) {
	out, err := exec.CommandContext(ctx, f.bin("ffmpeg", f.FFmpeg), "-v", "error", "-ss", seconds(from), "-t", seconds(length),
		"-i", path, "-vn", "-ac", "1", "-ar", strconv.Itoa(wire().SpeechSampleRate), "-f", "wav", "pipe:1").Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg audio: %w", err)
	}
	return out, nil
}

// Frames implements MediaTool, as the earlier Doc Tender read video: one frame per
// interval over length from from, at most wire().FrameMaxWidth wide, as JPEG,
// written to a temporary folder and handed to each one at a time.
func (f FFmpeg) Frames(ctx context.Context, path string, from, length, interval time.Duration, each func(Frame) error) error {
	dir, err := os.MkdirTemp("", "doctender-frames-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	filter := fmt.Sprintf("fps=%g,scale='min(%d,iw)':-2", float64(secondUnit)/float64(interval), wire().FrameMaxWidth)
	cmd := exec.CommandContext(ctx, f.bin("ffmpeg", f.FFmpeg), "-v", "error", "-ss", seconds(from), "-t", seconds(length), "-i", path,
		"-vf", filter, "-q:v", strconv.Itoa(wire().FrameJPEGQuality), filepath.Join(dir, "frame-%06d.jpg"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg frames: %w: %s", err, strings.TrimSpace(string(out)))
	}
	names, err := filepath.Glob(filepath.Join(dir, "frame-*.jpg"))
	if err != nil {
		return err
	}
	sort.Strings(names)
	for k, n := range names {
		raw, err := os.ReadFile(n)
		if err != nil {
			return err
		}
		_ = os.Remove(n)
		if err := each(Frame{At: from + interval*time.Duration(k), Data: raw, MediaType: "image/jpeg"}); err != nil {
			return err
		}
	}
	return nil
}

func seconds(d time.Duration) string { return fmt.Sprintf("%g", d.Seconds()) }

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

// WordcraftCLI runs wordcraft-cli convert: the binary the core image builds at the
// pinned LawCraft commit (core.WordcraftCLI).
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

// errDecodeLimit marks an upload that expands past the decode caps in wire.json: a
// decompression or pixel bomb. Such a file is damaged; it is not retried.
var errDecodeLimit = errors.New("the file expands past Doc Tender's decode limit")

// decodeCaps are the decode limits; tests lower them to exercise a bomb cheaply.
var decodeCaps = func() (bytes, pixels int64) { return wire().MaxDecodedBytes, wire().MaxImagePixels }

// sheetCellCap is wire().MaxSheetCells; tests lower it.
var sheetCellCap = func() int64 { return wire().MaxSheetCells }

// cmapEntryCap is wire().MaxCMapEntries; tests lower it.
var cmapEntryCap = func() int { return wire().MaxCMapEntries }

// readCapped reads r to the end, refusing more than wire().MaxDecodedBytes.
func readCapped(r io.Reader) ([]byte, error) {
	max, _ := decodeCaps()
	out, err := io.ReadAll(io.LimitReader(r, max+int64(one)))
	if int64(len(out)) > max {
		return nil, fmt.Errorf("%w: more than %d bytes decoded", errDecodeLimit, max)
	}
	return out, err
}

// checkPixels refuses a photo whose declared size passes wire().MaxImagePixels, before
// anything decodes it.
func checkPixels(raw []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if _, max := decodeCaps(); int64(cfg.Width)*int64(cfg.Height) > max {
		return fmt.Errorf("%w: %d by %d pixels", errDecodeLimit, cfg.Width, cfg.Height)
	}
	return nil
}
