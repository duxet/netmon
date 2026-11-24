package api

import (
	"database/sql"
	"embed"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	_ "github.com/marcboeker/go-duckdb"
	migrate "github.com/rubenv/sql-migrate"
)

func newTestApp(t *testing.T) (*fiber.App, *sql.DB) {
	t.Helper()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../migrations"}, migrate.Up); err != nil {
		t.Fatal(err)
	}

	mac, _ := net.ParseMAC("00:11:32:9b:c4:01")
	_, err = db.Exec(`INSERT INTO flows VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		[]byte(mac), nil,
		netip.MustParseAddr("192.168.1.10").AsSlice(), netip.MustParseAddr("140.82.121.4").AsSlice(),
		6, 443, 1000, 10, 100, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// No GeoIP database and the system resolver: nothing here resolves names.
	return CreateHTTPApp(db, embed.FS{}, Options{LocalDNS: "system", PublicDNS: "system"}), db
}

func do(t *testing.T, app *fiber.App, method, path, body string) (int, string) {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func errorMessage(t *testing.T, body string) string {
	t.Helper()
	var dto ErrorDTO
	if err := json.Unmarshal([]byte(body), &dto); err != nil {
		t.Fatalf("error body %q is not JSON: %v", body, err)
	}
	return dto.Error
}

func TestInvalidFiltersAreRejected(t *testing.T) {
	app, _ := newTestApp(t)

	for _, path := range []string{
		"/api/flows?mac=nope",
		"/api/flows?ip=999.1.1.1",
		"/api/apps?mac=nope",
		"/api/clients/not-a-client/flows",
	} {
		status, body := do(t, app, "GET", path, "")
		if status != fiber.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, status)
			continue
		}
		if msg := errorMessage(t, body); msg == "" {
			t.Errorf("GET %s: empty error message", path)
		}
	}
}

func TestClientFlows(t *testing.T) {
	app, _ := newTestApp(t)

	for _, id := range []string{"00:11:32:9b:c4:01", "192.168.1.10"} {
		status, body := do(t, app, "GET", "/api/clients/"+id+"/flows", "")
		if status != fiber.StatusOK {
			t.Fatalf("GET client %s flows = %d: %s", id, status, body)
		}

		// FlowDTO only encodes: its IPAddress can't unmarshal into a nil address.
		var flows []struct {
			Port    uint16
			Traffic TrafficDTO
		}
		if err := json.Unmarshal([]byte(body), &flows); err != nil {
			t.Fatal(err)
		}
		if len(flows) != 1 || flows[0].Port != 443 || flows[0].Traffic.InBytes != 1000 {
			t.Errorf("client %s flows = %+v, want the one HTTPS flow", id, flows)
		}
	}
}

// A failing query answers with a 500 and leaves the server running.
func TestDatabaseErrorsReturn500(t *testing.T) {
	app, db := newTestApp(t)
	if _, err := db.Exec(`DROP TABLE flows`); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/stats", "/api/flows", "/api/clients", "/api/traffic-measurements", "/api/apps"} {
		status, body := do(t, app, "GET", path, "")
		if status != fiber.StatusInternalServerError {
			t.Errorf("GET %s = %d, want 500", path, status)
			continue
		}
		if msg := errorMessage(t, body); msg != "internal server error" {
			t.Errorf("GET %s error = %q, want no internal details", path, msg)
		}
	}
}

func TestHostnamesRequestValidation(t *testing.T) {
	app, _ := newTestApp(t)

	if status, _ := do(t, app, "POST", "/api/hostnames", "{"); status != fiber.StatusBadRequest {
		t.Errorf("malformed body = %d, want 400", status)
	}

	ips := make([]string, maxHostnamesPerRequest+1)
	for i := range ips {
		ips[i] = "10.0.0.1"
	}
	body, _ := json.Marshal(map[string][]string{"ip_addresses": ips})
	if status, _ := do(t, app, "POST", "/api/hostnames", string(body)); status != fiber.StatusBadRequest {
		t.Errorf("too many addresses = %d, want 400", status)
	}
}

func TestCountryLookupWithoutDatabase(t *testing.T) {
	lookup := newCountryLookup("does-not-exist.mmdb")
	defer lookup.Close()

	if country := lookup.Country(ip(t, "140.82.121.4")); country != nil {
		t.Errorf("country = %q, want nil without a database", *country)
	}
}
