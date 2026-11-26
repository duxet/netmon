package unifi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testKey = "test-key"

// fakeController serves the Integration API under prefix, with the clients
// in pages of at most pageSize.
type fakeController struct {
	prefix   string
	pageSize int
	clients  []map[string]any
	requests []string
}

func (f *fakeController) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.URL.Path)
	if r.Header.Get("X-API-KEY") != testKey {
		http.Error(w, `{"statusCode":401,"message":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, f.prefix)
	if !ok {
		http.NotFound(w, r)
		return
	}

	var data []map[string]any
	switch path {
	case "/sites":
		data = []map[string]any{
			{"id": "11111111-aaaa", "internalReference": "default", "name": "Default"},
			{"id": "22222222-bbbb", "internalReference": "office", "name": "Office"},
		}
	case "/sites/11111111-aaaa/devices":
		data = []map[string]any{{"id": "ap-1", "name": "Living room AP"}}
	case "/sites/11111111-aaaa/clients":
		data = f.clients
	default:
		http.NotFound(w, r)
		return
	}

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit := len(data)
	if f.pageSize > 0 {
		limit = f.pageSize
	}
	end := min(offset+limit, len(data))
	page := data[min(offset, len(data)):end]
	_ = json.NewEncoder(w).Encode(map[string]any{
		"offset": offset, "limit": limit, "count": len(page), "totalCount": len(data), "data": page,
	})
}

func fingerprint(t *testing.T, server *httptest.Server) string {
	t.Helper()
	sum := sha256.Sum256(server.Certificate().Raw)
	return FormatFingerprint(sum[:])
}

func newSource(t *testing.T, server *httptest.Server, opts Options) *Source {
	t.Helper()
	opts.URL = server.URL
	if opts.APIKey == "" {
		opts.APIKey = testKey
	}
	if opts.Fingerprint == "" {
		opts.Fingerprint = fingerprint(t, server)
	}
	source, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestSync(t *testing.T) {
	for _, prefix := range []string{"/proxy/network/integration/v1", "/integration/v1"} {
		t.Run(prefix, func(t *testing.T) {
			fake := &fakeController{prefix: prefix, pageSize: 1, clients: []map[string]any{
				{"type": "WIRELESS", "id": "c1", "name": "Pixel 9", "connectedAt": "2026-11-20T10:00:00Z",
					"ipAddress": "192.168.1.20", "macAddress": "AA:BB:CC:00:11:22", "uplinkDeviceId": "ap-1"},
				{"type": "WIRED", "id": "c2", "name": "nas", "ipAddress": "192.168.1.5", "macAddress": "aa:bb:cc:00:11:33"},
				{"type": "VPN", "id": "c3", "name": "no mac"},
			}}
			server := httptest.NewTLSServer(fake)
			defer server.Close()

			source := newSource(t, server, Options{})
			if err := source.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}

			phone, ok := source.Client("aa:bb:cc:00:11:22")
			if !ok {
				t.Fatal("the phone isn't known")
			}
			want := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
			if phone.Name != "Pixel 9" || phone.Type != Wireless || phone.Uplink != "Living room AP" ||
				phone.IPAddress != "192.168.1.20" || !phone.ConnectedAt.Equal(want) || !phone.Connected {
				t.Errorf("phone = %+v", phone)
			}
			if nas, ok := source.Client("aa:bb:cc:00:11:33"); !ok || nas.Type != Wired || nas.Uplink != "" {
				t.Errorf("nas = %+v, %v", nas, ok)
			}

			status, _ := source.Status()
			if status.Site != "Default" || status.Connected != 2 || status.LastError != "" || status.LastSync.IsZero() {
				t.Errorf("status = %+v", status)
			}

			// A client that left is kept, as not connected.
			fake.clients = fake.clients[1:]
			if err := source.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			if phone, ok := source.Client("aa:bb:cc:00:11:22"); !ok || phone.Connected || phone.Name != "Pixel 9" {
				t.Errorf("phone after it left = %+v, %v", phone, ok)
			}
		})
	}
}

func TestSyncFindsSiteByNameOrID(t *testing.T) {
	server := httptest.NewTLSServer(&fakeController{prefix: "/proxy/network/integration/v1"})
	defer server.Close()

	for _, site := range []string{"Default", "11111111-aaaa"} {
		source := newSource(t, server, Options{Site: site})
		if err := source.Sync(context.Background()); err != nil {
			t.Errorf("site %q: %v", site, err)
		}
	}

	source := newSource(t, server, Options{Site: "garage"})
	err := source.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), `"office"`) {
		t.Errorf("unknown site: err = %v, want one listing the sites", err)
	}
	if status, _ := source.Status(); status.LastError == "" {
		t.Error("the status has no error")
	}
}

func TestSyncWithWrongKey(t *testing.T) {
	server := httptest.NewTLSServer(&fakeController{prefix: "/proxy/network/integration/v1"})
	defer server.Close()

	source := newSource(t, server, Options{APIKey: "wrong"})
	err := source.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "API key") {
		t.Errorf("err = %v, want a 401 that points at the key", err)
	}
}

func TestPinnedCertificate(t *testing.T) {
	fake := &fakeController{prefix: "/proxy/network/integration/v1"}
	server := httptest.NewTLSServer(fake)
	defer server.Close()

	// The test server's certificate is self-signed, so it is only trusted
	// pinned.
	source, err := New(Options{URL: server.URL, APIKey: testKey})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Sync(context.Background()); err == nil {
		t.Error("a self-signed certificate was trusted without a fingerprint")
	}

	other := strings.Repeat("AB:", 31) + "AB"
	source = newSource(t, server, Options{Fingerprint: other})
	if err := source.Sync(context.Background()); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Errorf("another certificate's fingerprint: err = %v", err)
	}
	if len(fake.requests) != 0 {
		t.Errorf("the API key was sent to an untrusted server: %v", fake.requests)
	}
}

func TestParseFingerprint(t *testing.T) {
	hexDigits := strings.Repeat("ab", 32)
	for _, raw := range []string{hexDigits, strings.ToUpper(hexDigits), strings.Repeat("AB:", 31) + "AB", "sha256=" + hexDigits} {
		if _, err := ParseFingerprint(raw); err != nil {
			t.Errorf("ParseFingerprint(%q) = %v", raw, err)
		}
	}
	for _, raw := range []string{"", "ab:cd", strings.Repeat("zz", 32)} {
		if _, err := ParseFingerprint(raw); err == nil {
			t.Errorf("ParseFingerprint(%q) succeeded", raw)
		}
	}
}

func TestParseURL(t *testing.T) {
	for _, raw := range []string{"https://192.168.1.1", "https://unifi.lan:8443/", "http://localhost:8080"} {
		if _, err := ParseURL(raw); err != nil {
			t.Errorf("ParseURL(%q) = %v", raw, err)
		}
	}
	for _, raw := range []string{"", "192.168.1.1", "http://192.168.1.1", "https://unifi.lan?x=1"} {
		if _, err := ParseURL(raw); err == nil {
			t.Errorf("ParseURL(%q) succeeded", raw)
		}
	}
}

func TestNilSource(t *testing.T) {
	var source *Source
	if _, ok := source.Client("aa:bb:cc:00:11:22"); ok {
		t.Error("a nil source knows a client")
	}
	if _, ok := source.Status(); ok {
		t.Error("a nil source has a status")
	}
	source.Run(context.Background())
}
