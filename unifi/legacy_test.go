package unifi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeNetworkApp is a Network Application that signs in as UniFi does: the
// self-hosted one, or one on UniFi OS, which also needs a CSRF token.
type fakeNetworkApp struct {
	unifiOS  bool
	password string
	// mfa makes the login ask for a second factor.
	mfa bool

	sessions map[string]bool
	logins   int
	paths    []string
	stations []map[string]any
	users    []map[string]any
}

const fakeCSRF = "csrf-123"

func (f *fakeNetworkApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	reply := func(status int, rc, msg string, data any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"rc": rc, "msg": msg}, "data": data})
	}

	path := r.URL.Path
	if r.URL.Path == "/" {
		if f.unifiOS {
			_, _ = w.Write([]byte(`<html><head><base href="/"></head></html>`))
		} else {
			http.Redirect(w, r, "/manage", http.StatusFound)
		}
		return
	}

	loginPath := "/api/login"
	if f.unifiOS {
		loginPath = "/api/auth/login"
		var ok bool
		if path, ok = strings.CutPrefix(path, "/proxy/network"); !ok && r.URL.Path != loginPath {
			http.NotFound(w, r)
			return
		}
	}

	if r.URL.Path == loginPath && r.Method == http.MethodPost {
		f.logins++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case body["username"] != "netmon" || body["password"] != f.password:
			if f.unifiOS {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			reply(http.StatusBadRequest, "error", "api.err.Invalid", []any{})
		case f.mfa && f.unifiOS:
			w.WriteHeader(statusMFARequired)
		case f.mfa:
			reply(http.StatusBadRequest, "error", "api.err.Ubic2faTokenRequired", []any{})
		default:
			token := "session-" + string(rune('a'+f.logins))
			f.sessions[token] = true
			http.SetCookie(w, &http.Cookie{Name: "unifises", Value: token, Path: "/"})
			if f.unifiOS {
				w.Header().Set("X-Csrf-Token", fakeCSRF)
			}
			reply(http.StatusOK, "ok", "", []any{})
		}
		return
	}

	if !strings.HasPrefix(path, "/api/") {
		http.NotFound(w, r)
		return
	}

	cookie, err := r.Cookie("unifises")
	if err != nil || !f.sessions[cookie.Value] || (f.unifiOS && r.Header.Get("X-Csrf-Token") != fakeCSRF) {
		if f.unifiOS {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		reply(http.StatusUnauthorized, "error", "api.err.LoginRequired", []any{})
		return
	}

	switch path {
	case "/api/self/sites":
		reply(http.StatusOK, "ok", "", []map[string]any{
			{"_id": "s1", "name": "default", "desc": "Default"},
			{"_id": "s2", "name": "x7k2p9", "desc": "Office"},
		})
	case "/api/s/default/stat/sta":
		reply(http.StatusOK, "ok", "", f.stations)
	case "/api/s/default/stat/device":
		reply(http.StatusOK, "ok", "", []map[string]any{
			{"mac": "f0:9f:c2:00:00:01", "name": "Living room AP", "model": "U6LR"},
			{"mac": "f0:9f:c2:00:00:02", "name": "", "model": "USW-24"},
		})
	case "/api/s/default/rest/user":
		reply(http.StatusOK, "ok", "", f.users)
	default:
		reply(http.StatusBadRequest, "error", "api.err.NotFound", []any{})
	}
}

func newFakeNetworkApp(unifiOS bool) *fakeNetworkApp {
	return &fakeNetworkApp{
		unifiOS:  unifiOS,
		password: "s3cret",
		sessions: map[string]bool{},
		stations: []map[string]any{
			{"mac": "AA:BB:CC:00:11:22", "hostname": "pixel-9", "name": "Pixel", "ip": "192.168.1.20",
				"is_wired": false, "ap_mac": "f0:9f:c2:00:00:01", "uptime": 3600},
			{"mac": "aa:bb:cc:00:11:33", "hostname": "nas", "ip": "192.168.1.5",
				"is_wired": true, "sw_mac": "f0:9f:c2:00:00:02", "sw_port": 5, "assoc_time": "1764000000"},
		},
		users: []map[string]any{
			{"mac": "aa:bb:cc:00:11:44", "name": "Old laptop", "is_wired": false, "last_seen": 1764000000},
			{"mac": "aa:bb:cc:00:11:55", "hostname": ""},
			{"mac": "aa:bb:cc:00:11:22", "name": "Pixel", "last_seen": 1},
		},
	}
}

func newLoginSource(t *testing.T, server *httptest.Server, password string) (*Source, *time.Time) {
	t.Helper()
	sum := sha256.Sum256(server.Certificate().Raw)
	source, err := New(Options{
		URL: server.URL, Username: "netmon", Password: password, Fingerprint: FormatFingerprint(sum[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	source.now = func() time.Time { return now }
	return source, &now
}

func TestLoginSync(t *testing.T) {
	for name, unifiOS := range map[string]bool{"self-hosted": false, "UniFi OS": true} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeNetworkApp(unifiOS)
			server := httptest.NewTLSServer(fake)
			defer server.Close()

			source, now := newLoginSource(t, server, "s3cret")
			if err := source.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}

			phone, ok := source.Client("aa:bb:cc:00:11:22")
			if !ok || phone.Name != "Pixel" || phone.Type != Wireless || phone.Uplink != "Living room AP" ||
				phone.IPAddress != "192.168.1.20" || !phone.Connected || !phone.ConnectedAt.Equal(now.Add(-time.Hour)) {
				t.Errorf("phone = %+v, %v", phone, ok)
			}
			// A switch without a name goes by its model; the time may come as a string.
			nas, ok := source.Client("aa:bb:cc:00:11:33")
			if !ok || nas.Name != "nas" || nas.Type != Wired || nas.Uplink != "USW-24, port 5" ||
				!nas.ConnectedAt.Equal(time.Unix(1764000000, 0)) {
				t.Errorf("nas = %+v, %v", nas, ok)
			}
			// Clients that aren't connected are known by name, unless they have none.
			laptop, ok := source.Client("aa:bb:cc:00:11:44")
			if !ok || laptop.Name != "Old laptop" || laptop.Connected || !laptop.LastSeen.Equal(time.Unix(1764000000, 0)) {
				t.Errorf("laptop = %+v, %v", laptop, ok)
			}
			if c, ok := source.Client("aa:bb:cc:00:11:55"); ok {
				t.Errorf("a client without a name is kept: %+v", c)
			}

			status, _ := source.Status()
			if status.Site != "Default" || status.Connected != 2 || status.LastError != "" {
				t.Errorf("status = %+v", status)
			}

			// The session is kept between syncs.
			if err := source.Sync(context.Background()); err != nil || fake.logins != 1 {
				t.Errorf("second sync: %v, %d logins, want 1", err, fake.logins)
			}

			// When it runs out, netmon logs in again.
			clear(fake.sessions)
			if err := source.Sync(context.Background()); err != nil || fake.logins != 2 {
				t.Errorf("sync after the session ran out: %v, %d logins, want 2", err, fake.logins)
			}

			// The phone leaving keeps it, as not connected.
			fake.stations = fake.stations[1:]
			if err := source.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			if phone, ok := source.Client("aa:bb:cc:00:11:22"); !ok || phone.Connected || phone.Name != "Pixel" ||
				phone.Uplink != "Living room AP" || !phone.LastSeen.Equal(*now) {
				t.Errorf("phone after it left = %+v, %v", phone, ok)
			}
		})
	}
}

func TestLoginRejected(t *testing.T) {
	for name, unifiOS := range map[string]bool{"self-hosted": false, "UniFi OS": true} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeNetworkApp(unifiOS)
			server := httptest.NewTLSServer(fake)
			defer server.Close()

			source, now := newLoginSource(t, server, "wrong")
			err := source.Sync(context.Background())
			if err == nil || !strings.Contains(err.Error(), "rejected the username or password") {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), "wrong") {
				t.Errorf("the error shows the password: %v", err)
			}

			// Logging in again right away could lock the account.
			err = source.Sync(context.Background())
			if fake.logins != 1 || err == nil || !strings.Contains(err.Error(), "trying again at") {
				t.Errorf("sync right after: %v, %d logins, want 1", err, fake.logins)
			}
			*now = now.Add(pauseAfterBadPassword)
			fake.password = "wrong"
			if err := source.Sync(context.Background()); err != nil || fake.logins != 2 {
				t.Errorf("sync after the pause: %v, %d logins, want 2", err, fake.logins)
			}
		})
	}
}

func TestLoginNeedsMFA(t *testing.T) {
	for name, unifiOS := range map[string]bool{"self-hosted": false, "UniFi OS": true} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeNetworkApp(unifiOS)
			fake.mfa = true
			server := httptest.NewTLSServer(fake)
			defer server.Close()

			source, _ := newLoginSource(t, server, "s3cret")
			err := source.Sync(context.Background())
			if err == nil || !strings.Contains(err.Error(), "two-factor") {
				t.Errorf("err = %v, want one about two-factor authentication", err)
			}
		})
	}
}

func TestLoginUnknownSite(t *testing.T) {
	server := httptest.NewTLSServer(newFakeNetworkApp(false))
	defer server.Close()

	sum := sha256.Sum256(server.Certificate().Raw)
	source, err := New(Options{URL: server.URL, Username: "netmon", Password: "s3cret", Site: "garage",
		Fingerprint: FormatFingerprint(sum[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Sync(context.Background()); err == nil || !strings.Contains(err.Error(), `"x7k2p9"`) {
		t.Errorf("err = %v, want one listing the sites", err)
	}

	// Sites are also known by their description.
	source, _ = New(Options{URL: server.URL, Username: "netmon", Password: "s3cret", Site: "default",
		Fingerprint: FormatFingerprint(sum[:])})
	if err := source.Sync(context.Background()); err != nil {
		t.Error(err)
	}
}

func TestIntegrationAPIMissing(t *testing.T) {
	// A self-hosted Network Application without the Integration API.
	server := httptest.NewTLSServer(newFakeNetworkApp(false))
	defer server.Close()

	source := newSource(t, server, Options{})
	err := source.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unifi_username") {
		t.Errorf("err = %v, want one suggesting a username and password", err)
	}
}

func TestNewNeedsOneWayIn(t *testing.T) {
	for _, opts := range []Options{
		{URL: "https://unifi.lan"},
		{URL: "https://unifi.lan", APIKey: "k", Username: "u", Password: "p"},
		{URL: "https://unifi.lan", Username: "u"},
		{URL: "https://unifi.lan", Password: "p"},
	} {
		if _, err := New(opts); err == nil {
			t.Errorf("New(%+v) succeeded", opts)
		}
	}
}
