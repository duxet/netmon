package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckHealth(t *testing.T) {
	healthy := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		if !healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()

	listen := strings.TrimPrefix(server.URL, "http://")
	port := listen[strings.LastIndex(listen, ":"):]

	if status := checkHealth(listen); status != 0 {
		t.Errorf("healthy netmon at %s: exit status %d, want 0", listen, status)
	}
	// Listening on every address is reached at this machine's own.
	for _, all := range []string{port, "0.0.0.0" + port, "[::]" + port} {
		if status := checkHealth(all); status != 0 {
			t.Errorf("healthy netmon at %s: exit status %d, want 0", all, status)
		}
	}

	healthy = false
	if status := checkHealth(listen); status != 1 {
		t.Errorf("unhealthy netmon: exit status %d, want 1", status)
	}

	server.Close()
	if status := checkHealth(listen); status != 1 {
		t.Errorf("netmon not running: exit status %d, want 1", status)
	}
}
