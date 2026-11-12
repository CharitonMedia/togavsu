// Command loadgen is the AC-016 load for tests/update/load.sh. It sends health and
// signed-in matter requests through Caddy until SIGINT or SIGTERM, then prints one JSON
// summary line and exits 1 when any request failed. A failure is a connection error or
// a status outside 2xx and 3xx. Synthetic data only; TLS is not verified (local Caddy CA).
//
//	loadgen -base https://localhost:18443 -state state.json [-workers 8]
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type state struct {
	Cookies map[string]string `json:"cookies"`
	Matter  string            `json:"matter"`
}

type summary struct {
	Requests      int            `json:"requests"`
	Failed        int            `json:"failed"`
	Authenticated int            `json:"authenticated"`
	Versions      map[string]int `json:"versions"`
	Errors        []string       `json:"errors,omitempty"`
	Seconds       float64        `json:"seconds"`
}

type tally struct {
	mu sync.Mutex
	s  summary
}

func (t *tally) add(auth bool, version string, failure string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.s.Requests++
	if auth {
		t.s.Authenticated++
	}
	if version != "" {
		t.s.Versions[version]++
	}
	if failure != "" {
		t.s.Failed++
		if len(t.s.Errors) < 20 {
			t.s.Errors = append(t.s.Errors, time.Now().UTC().Format("15:04:05.000")+" "+failure)
		}
	}
}

func main() {
	base := flag.String("base", "https://localhost:18443", "Caddy base URL")
	statePath := flag.String("state", "", "first-run state file with the session cookies and a matter id")
	workers := flag.Int("workers", 8, "concurrent health workers")
	authEvery := flag.Duration("auth-every", 250*time.Millisecond, "interval between signed-in matter reads")
	flag.Parse()
	var st state
	if *statePath != "" {
		raw, err := os.ReadFile(*statePath)
		if err == nil {
			err = json.Unmarshal(raw, &st)
		}
		if err != nil || st.Matter == "" || len(st.Cookies) == 0 {
			fmt.Fprintln(os.Stderr, "loadgen: unreadable state file:", err)
			os.Exit(2)
		}
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // local Caddy CA, synthetic test
			MaxIdleConnsPerHost: *workers + 2,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	t := &tally{s: summary{Versions: map[string]int{}}}
	started := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				hit(ctx, client, t, *base+"/api/v1/health", nil, false)
			}
		}()
	}
	if st.Matter != "" {
		var cookies []string
		for k, v := range st.Cookies {
			cookies = append(cookies, k+"="+v)
		}
		sort.Strings(cookies)
		header := http.Header{"Cookie": {strings.Join(cookies, "; ")}}
		wg.Add(1)
		go func() {
			defer wg.Done()
			tick := time.NewTicker(*authEvery)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					hit(ctx, client, t, *base+"/api/v1/matters/"+st.Matter, header, true)
				}
			}
		}()
	}
	wg.Wait()
	t.s.Seconds = time.Since(started).Seconds()
	out, _ := json.Marshal(t.s)
	fmt.Println(string(out))
	if t.s.Failed > 0 || t.s.Requests == 0 {
		os.Exit(1)
	}
}

// hit sends one GET. A request cut off by the stop signal is not counted.
func hit(ctx context.Context, client *http.Client, t *tally, url string, header http.Header, auth bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.add(auth, "", err.Error())
		return
	}
	for k, v := range header {
		req.Header[k] = v
	}
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		t.add(auth, "", "GET "+url+": "+err.Error())
		time.Sleep(50 * time.Millisecond)
		return
	}
	body, rerr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	if rerr != nil && ctx.Err() != nil {
		return
	}
	failure := ""
	if rerr != nil {
		failure = "GET " + url + ": read: " + rerr.Error()
	} else if res.StatusCode < 200 || res.StatusCode >= 400 {
		failure = fmt.Sprintf("GET %s: %d %s", url, res.StatusCode, strings.TrimSpace(string(body[:min(len(body), 200)])))
	}
	version := ""
	if !auth && failure == "" {
		var doc struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(body, &doc) == nil {
			version = doc.Version
		}
	}
	t.add(auth, version, failure)
}
