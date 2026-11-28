package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/duxet/netmon/storage"

	"github.com/duxet/netmon/unifi"
	"github.com/gofiber/fiber/v3"
)

// newUniFiSource returns a source synced with a controller that knows the
// test NAS as a wired client.
func newUniFiSource(t *testing.T) *unifi.Source {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data []map[string]any
		switch {
		case strings.HasSuffix(r.URL.Path, "/sites"):
			data = []map[string]any{{"id": "site-1", "internalReference": "default", "name": "Default"}}
		case strings.HasSuffix(r.URL.Path, "/devices"):
			data = []map[string]any{{"id": "sw-1", "name": "Rack switch"}}
		case strings.HasSuffix(r.URL.Path, "/clients"):
			data = []map[string]any{{
				"type": "WIRED", "name": "synology", "macAddress": strings.ToUpper(testNASMAC),
				"ipAddress": "192.168.1.5", "uplinkDeviceId": "sw-1", "connectedAt": "2026-11-20T10:00:00Z",
			}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"offset": 0, "count": len(data), "totalCount": len(data), "data": data})
	}))
	t.Cleanup(server.Close)

	sum := sha256.Sum256(server.Certificate().Raw)
	source, err := unifi.New(unifi.Options{URL: server.URL, APIKey: "k3y", Fingerprint: unifi.FormatFingerprint(sum[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestDevicesHaveUniFiDetails(t *testing.T) {
	app, db := newTestAppWith(t, Options{UniFi: newUniFiSource(t)})
	if err := storage.SyncDevices(db, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	type entry struct {
		MACAddress string
		Endpoint   struct{ MACAddress string }
		UniFi      *UniFiClientDTO
	}
	check := func(path string, macOf func(entry) string) {
		var entries []entry
		if status, _ := get(t, app, path, &entries); status != fiber.StatusOK {
			t.Fatalf("GET %s = %d", path, status)
		}
		found := false
		for _, e := range entries {
			if macOf(e) != testNASMAC {
				if e.UniFi != nil {
					t.Errorf("%s: %s has UniFi details %+v, but UniFi doesn't know it", path, macOf(e), *e.UniFi)
				}
				continue
			}
			found = true
			u := e.UniFi
			if u == nil || u.Name != "synology" || u.Connection != unifi.Wired || u.Uplink != "Rack switch" ||
				!u.Connected || u.ConnectedAt == nil {
				t.Errorf("%s: NAS has UniFi details %+v", path, u)
			}
		}
		if !found {
			t.Errorf("%s: the NAS isn't listed", path)
		}
	}
	check("/api/devices", func(e entry) string { return e.MACAddress })
	check("/api/clients", func(e entry) string { return e.Endpoint.MACAddress })

	var status UniFiStatusDTO
	get(t, app, "/api/unifi", &status)
	if !status.Enabled || status.Site != "Default" || status.Connected != 1 || status.LastSync == nil || status.LastError != "" {
		t.Errorf("/api/unifi = %+v", status)
	}
}

func TestUniFiStatusWhenNotConfigured(t *testing.T) {
	app, _ := newTestApp(t)

	var status UniFiStatusDTO
	if code, _ := get(t, app, "/api/unifi", &status); code != fiber.StatusOK || status.Enabled {
		t.Errorf("/api/unifi = %d %+v, want it off", code, status)
	}
}
