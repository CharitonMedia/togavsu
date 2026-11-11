package dtcorpus

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Key is the API key the provider accepts.
const Key = "test-openai-key"

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
}

// NewProvider starts the provider; Close stops it.
func NewProvider() *Provider {
	p := &Provider{failOCR: map[string]int{}, label: map[string]map[string]any{}}
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
	if r.Header.Get("Authorization") != "Bearer "+Key {
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
		answer = jsonText(categoryFor(body))
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

func categoryFor(body string) map[string]any {
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
