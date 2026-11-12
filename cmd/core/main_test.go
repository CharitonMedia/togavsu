package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ok.Close()
	if got := healthcheck(ok.URL, io.Discard); got != 0 {
		t.Fatalf("healthy core = %d", got)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer down.Close()
	if got := healthcheck(down.URL, io.Discard); got != 1 {
		t.Fatalf("unavailable core = %d", got)
	}
	down.Close()
	if got := healthcheck(down.URL, io.Discard); got != 1 {
		t.Fatalf("no core = %d", got)
	}
}
