package doctender

import (
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
	"testing"
)

// fakeAI is a model provider on the OpenAI wire for tests. It reads what it is given:
// OCR and photo descriptions read the painted glyphs from the pixels, a frame is
// described by its color, transcription segments the WAV samples by energy, grouping
// orders pages by the "PAGE n OF m" it read, and labeling cites the case numbers and
// dates it finds on each page. Tests steer specific answers through the hooks.
type fakeAI struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex
	// down makes the health check fail.
	down bool
	// failOCR fails the OCR model's read of a page whose pixels contain the key, the
	// given number of times (failure injection).
	failOCR map[string]int
	// onOCR runs before each OCR answer with the text it read.
	onOCR func(text string)
	// label overrides the labeling answer for a body containing the key.
	label map[string]Label
	// category overrides the categorizing answer: by model, for a body containing key.
	category map[string]map[string]string
	// categoryConfidence overrides the confidence reported.
	categoryConfidence float64
	// splitGroups makes a model's grouping answer put every photo on its own, so it
	// disagrees with a model that groups by page.
	splitGroups map[string]bool
	calls       []string
}

func newFakeAI(t *testing.T) *fakeAI {
	f := &fakeAI{t: t, failOCR: map[string]int{}, label: map[string]Label{}, category: map[string]map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAI) set(fn func(*fakeAI)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeAI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	down := f.down
	f.mu.Unlock()
	if r.URL.Path == "/models" {
		if down {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
		return
	}
	if r.Header.Get("Authorization") != "Bearer test-openai-key" {
		http.Error(w, "bad key", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/chat/completions":
		f.chat(w, r)
	case "/audio/transcriptions":
		f.transcribe(w, r)
	default:
		http.NotFound(w, r)
	}
}

type chatReq struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
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

func dataOf(u string) []byte {
	_, b64, _ := strings.Cut(u, ";base64,")
	raw, _ := base64.StdEncoding.DecodeString(b64)
	return raw
}

func (f *fakeAI) chat(w http.ResponseWriter, r *http.Request) {
	var req chatReq
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
		_ = json.Unmarshal(m.Content, &parts)
	}
	var texts []string
	var images, docs [][]byte
	for _, p := range parts {
		switch p.Type {
		case "text":
			texts = append(texts, p.Text)
		case "image_url":
			images = append(images, dataOf(p.ImageURL.URL))
		case "file":
			docs = append(docs, dataOf(p.File.Data))
		}
	}
	body := strings.Join(texts, "\n")
	var answer string
	task := ""
	switch {
	case strings.HasPrefix(system, ocrPrompt[:30]):
		task = "ocr"
		var read string
		if len(images) > 0 {
			read = readGlyphs(images[0])
		} else if len(docs) > 0 {
			if doc, err := parsePDF(docs[0]); err == nil {
				read, _ = doc.pageText(0)
			}
		}
		f.mu.Lock()
		hook := f.onOCR
		fail := false
		if !strings.Contains(req.Model, "fast") {
			for key, n := range f.failOCR {
				if n > 0 && strings.Contains(read, key) {
					f.failOCR[key] = n - 1
					fail = true
				}
			}
		}
		f.mu.Unlock()
		if hook != nil {
			hook(read)
		}
		if fail {
			http.Error(w, "injected OCR failure", http.StatusInternalServerError)
			return
		}
		answer = read
	case strings.HasPrefix(system, photoPrompt[:30]):
		task = "photo"
		img := images[0]
		read := readGlyphs(img)
		desc := "a photographed page"
		if !usableText(read) {
			desc = "a solid " + colorName(img) + " frame"
		}
		answer = jsonText(photoRead{Description: desc, Text: read})
	case strings.HasPrefix(system, groupPrompt[:30]):
		task = "grouping"
		answer = jsonText(groupByPages(texts))
		if f.splitGroups[req.Model] {
			var alone groupAnswer
			for k := range texts {
				alone.Groups = append(alone.Groups, struct {
					Title  string `json:"title"`
					Kind   string `json:"kind"`
					Photos []int  `json:"photos"`
				}{Title: "photo", Kind: "photo", Photos: []int{k + 1}})
			}
			answer = jsonText(alone)
		}
	case strings.HasPrefix(system, labelPrompt[:30]):
		task = "labeling"
		answer = jsonText(f.labelFor(body))
	case strings.HasPrefix(system, "You file documents"):
		task = "categorizing"
		answer = jsonText(f.categoryFor(req.Model, body))
	default:
		http.Error(w, "unknown prompt", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, task+":"+req.Model)
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": answer}}},
		"usage":   map[string]any{"prompt_tokens": len(body)/4 + 50*len(images), "completion_tokens": len(answer) / 4},
	})
}

func colorName(img []byte) string {
	raw := readPixelsAvg(img)
	switch {
	case raw[0] > raw[1] && raw[0] > raw[2]:
		return "red"
	case raw[1] > raw[0] && raw[1] > raw[2]:
		return "green"
	case raw[2] > raw[0] && raw[2] > raw[1]:
		return "blue"
	}
	return "gray"
}

var rePage = regexp.MustCompile(`([A-Z]+)\s*/\s*PAGE (\d+) OF (\d+)`)

// groupByPages groups photos by the document word and page number they show.
func groupByPages(texts []string) groupAnswer {
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
	var out groupAnswer
	for _, d := range order {
		ps := docs[d]
		sort.Slice(ps, func(a, b int) bool { return ps[a].page < ps[b].page })
		var nums []int
		for _, p := range ps {
			nums = append(nums, p.photo)
		}
		out.Groups = append(out.Groups, struct {
			Title  string `json:"title"`
			Kind   string `json:"kind"`
			Photos []int  `json:"photos"`
		}{strings.ToLower(d), "document", nums})
	}
	for _, l := range loose {
		out.Groups = append(out.Groups, struct {
			Title  string `json:"title"`
			Kind   string `json:"kind"`
			Photos []int  `json:"photos"`
		}{"photo", "photo", []int{l}})
	}
	return out
}

var (
	reCase    = regexp.MustCompile(`\b\d{2}[A-Z]{2}-[A-Z]{2}\d+\b`)
	reISODate = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)
	reHeading = regexp.MustCompile(`(?m)^## Page (\d+)$`)
)

// labelFor reads the body: what it is from the first line, the first date, and every
// case number cited to the page it is on.
func (f *fakeAI) labelFor(body string) Label {
	f.mu.Lock()
	for key, l := range f.label {
		if strings.Contains(body, key) {
			f.mu.Unlock()
			return l
		}
	}
	f.mu.Unlock()
	l := Label{WhatItIsConfidence: 0.97, DocDateConfidence: 0.96, Summary: "A synthetic test document."}
	page := 1
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if m := reHeading.FindStringSubmatch(line); m != nil {
			fmt.Sscan(m[1], &page)
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "_") || strings.HasPrefix(line, "[") {
			continue
		}
		if l.WhatItIs == "" {
			w := strings.Fields(strings.ToLower(line))
			if len(w) > 4 {
				w = w[:4]
			}
			l.WhatItIs = strings.Join(w, " ")
		}
		if d := reISODate.FindString(line); d != "" && l.DocDate == "" {
			l.DocDate = d
		}
		for _, c := range reCase.FindAllString(line, -1) {
			p := page
			l.Fields = append(l.Fields, CitedField{Name: "case_number", Value: c, Confidence: 0.99, Citation: Citation{Page: &p}})
		}
	}
	if l.WhatItIs == "" {
		l.WhatItIs = "recording"
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "On screen") {
				l.WhatItIs = "video recording"
			}
		}
	}
	return l
}

func (f *fakeAI) categoryFor(model, body string) CategoryAnswer {
	f.mu.Lock()
	defer f.mu.Unlock()
	conf := 0.95
	if f.categoryConfidence != 0 {
		conf = f.categoryConfidence
	}
	for key, byModel := range f.category {
		if strings.Contains(body, key) {
			if c, ok := byModel[model]; ok {
				return CategoryAnswer{Category: c, Confidence: conf}
			}
			if c, ok := byModel["*"]; ok {
				return CategoryAnswer{Category: c, Confidence: conf}
			}
		}
	}
	lower := strings.ToLower(body)
	switch {
	case strings.Contains(lower, "motion") || strings.Contains(lower, "notice of hearing"):
		return CategoryAnswer{"court_filing", conf}
	case strings.Contains(lower, "letter"):
		return CategoryAnswer{"correspondence", conf}
	case strings.Contains(lower, "lease"):
		return CategoryAnswer{"contracts_agreements", conf}
	case strings.Contains(lower, "exhibit"):
		return CategoryAnswer{"police_criminal", conf}
	case strings.Contains(lower, "said:") || strings.Contains(lower, "on screen"):
		return CategoryAnswer{"call_meeting", conf}
	}
	return CategoryAnswer{"reference", conf}
}

// transcribe segments the WAV by energy, one-second windows: a run of sound is a
// segment "steady tone"; silence produces no segment.
func (f *fakeAI) transcribe(w http.ResponseWriter, r *http.Request) {
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
	var pcm []byte
	var format wavFormat
	_ = riffChunks(data, "WAVE", func(id string, body []byte) error {
		switch id {
		case "fmt ":
			return binary.Read(strings.NewReader(string(body)), binary.LittleEndian, &format)
		case "data":
			pcm = body
		}
		return nil
	})
	if format.ByteRate == 0 {
		http.Error(w, "not a WAV", http.StatusBadRequest)
		return
	}
	per := int(format.ByteRate)
	var segs []Segment
	start := -1
	secs := len(pcm) / per
	for s := 0; s <= secs; s++ {
		loud := false
		if s < secs {
			var sum float64
			win := pcm[s*per : (s+1)*per]
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
			segs = append(segs, Segment{Start: float64(start), End: float64(s), Speaker: "Speaker 1", Text: "steady tone"})
			start = -1
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, "transcription:"+model)
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"text": "steady tone", "duration": float64(len(pcm)) / float64(per), "segments": segs})
}

func (f *fakeAI) callCount(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}
