package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestBackupDownload(t *testing.T) {
	dir := t.TempDir()
	app, _ := newTestAppFor(t, func(*sql.DB) Options { return Options{DatabasePath: filepath.Join(dir, "flows.db")} })

	resp := request(t, app, "GET", "/api/backup", "")
	if resp.StatusCode != fiber.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), `attachment; filename="netmon-`) {
		t.Fatalf("GET /api/backup = %d %v", resp.StatusCode, resp.Header)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	data := readAll(t, resp)
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		t.Fatal(err)
	}

	copied, err := sql.Open("duckdb", backup)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	var flows int
	if err := copied.QueryRow("SELECT count(*) FROM all_flows").Scan(&flows); err != nil || flows != 1 {
		t.Errorf("backup has %d flows (%v), want the test app's one", flows, err)
	}

	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("the backup left %v behind", left)
	}

	app, _ = newTestApp(t)
	if status, _ := do(t, app, "GET", "/api/backup", ""); status != fiber.StatusNotFound {
		t.Errorf("GET /api/backup without a database path = %d, want 404", status)
	}
}

// setUp gives an app a network, a named device, a channel and a rule.
func setUp(t *testing.T, app *fiber.App) {
	t.Helper()
	for _, r := range []struct{ method, path, body string }{
		{"POST", "/api/networks", `{"name": "IoT", "prefixes": ["10.0.20.0/24"], "vlan": 20, "color": "orange"}`},
		{"PUT", "/api/devices/" + testLaptopMAC + "/name", `{"name": "Laptop"}`},
		{"PUT", "/api/devices/" + testTVMAC + "/icon", `{"icon": "tv"}`},
		{"POST", "/api/notification-channels", `{"name": "Phone", "url": "telegram://123456:secret-token@telegram?chats=42"}`},
		{"POST", "/api/alert-rules", `{"name": "New devices", "type": "new_device", "severity": "info", "channel_ids": [1]}`},
	} {
		if status, body := do(t, app, r.method, r.path, r.body); status >= 300 {
			t.Fatalf("%s %s = %d %s", r.method, r.path, status, body)
		}
	}
}

func exported(t *testing.T, app *fiber.App, query string) SettingsDTO {
	t.Helper()
	status, body := do(t, app, "GET", "/api/settings-export"+query, "")
	if status != fiber.StatusOK {
		t.Fatalf("GET /api/settings-export%s = %d %s", query, status, body)
	}
	var settings SettingsDTO
	if err := json.Unmarshal([]byte(body), &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func imported(t *testing.T, app *fiber.App, settings SettingsDTO) ImportResultDTO {
	t.Helper()
	data, _ := json.Marshal(settings)
	status, body := do(t, app, "POST", "/api/settings-import", string(data))
	if status != fiber.StatusOK {
		t.Fatalf("POST /api/settings-import = %d %s", status, body)
	}
	var result ImportResultDTO
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSettingsExportAndImport(t *testing.T) {
	from, _ := newTestApp(t)
	setUp(t, from)

	settings := exported(t, from, "?secrets=true")
	if len(settings.Networks) != 1 || len(settings.Devices) != 2 || len(settings.Channels) != 1 || len(settings.AlertRules) != 1 {
		t.Fatalf("settings = %+v, want a network, 2 devices, a channel and a rule", settings)
	}
	if settings.Channels[0].URL != "telegram://123456:secret-token@telegram?chats=42" || settings.AlertRules[0].Channels[0] != "Phone" {
		t.Errorf("channel %+v and rule %+v, want the URL and the channel by name", settings.Channels[0], settings.AlertRules[0])
	}

	to, _ := newTestApp(t)
	result := imported(t, to, settings)
	if result.Networks.Created != 1 || result.Devices.Updated != 2 || result.Channels.Created != 1 || result.AlertRules.Created != 1 || len(result.Warnings) != 0 {
		t.Errorf("import = %+v, want everything created", result)
	}
	again := exported(t, to, "?secrets=true")
	again.ExportedAt = settings.ExportedAt
	if a, b := mustJSON(t, again), mustJSON(t, settings); a != b {
		t.Errorf("after importing:\n%s\nwant\n%s", a, b)
	}

	// Importing again changes what is there rather than adding to it.
	if result := imported(t, to, settings); result.Networks.Updated != 1 || result.Channels.Updated != 1 || result.AlertRules.Updated != 1 ||
		result.Networks.Created+result.Channels.Created+result.AlertRules.Created != 0 {
		t.Errorf("second import = %+v, want everything updated", result)
	}
}

func TestSettingsExportWithoutSecrets(t *testing.T) {
	from, _ := newTestApp(t)
	setUp(t, from)

	settings := exported(t, from, "")
	if url := settings.Channels[0].URL; strings.Contains(url, "secret-token") || !strings.Contains(url, "REDACTED") {
		t.Errorf("channel URL = %q, want it redacted", url)
	}

	// A redacted channel can't be made, so it and the rule's link to it are
	// left out.
	to, _ := newTestApp(t)
	result := imported(t, to, settings)
	if result.Channels.Created != 0 || result.AlertRules.Created != 1 || len(result.Warnings) != 2 {
		t.Errorf("import = %+v, want the rule without its channel, and why", result)
	}

	// It can still change a channel that is there, keeping its URL.
	settings.Channels[0].Enabled = false
	if result := imported(t, from, settings); result.Channels.Updated != 1 {
		t.Errorf("import into the original = %+v, want the channel updated", result)
	}
	if channel := exported(t, from, "?secrets=true").Channels[0]; channel.Enabled || channel.URL != "telegram://123456:secret-token@telegram?chats=42" {
		t.Errorf("channel = %+v, want it disabled with its URL kept", channel)
	}
}

func TestSettingsImportChecksEverythingFirst(t *testing.T) {
	app, _ := newTestApp(t)
	setUp(t, app)
	settings := exported(t, app, "?secrets=true")

	settings.Version = 99
	data, _ := json.Marshal(settings)
	if status, _ := do(t, app, "POST", "/api/settings-import", string(data)); status != fiber.StatusBadRequest {
		t.Errorf("version 99 = %d, want 400", status)
	}

	// A new network whose block is the IoT network's, after a channel that
	// would have been fine.
	settings.Version = settingsVersion
	name := "Cameras"
	settings.Networks = append(settings.Networks, networkBody{Name: &name, Prefixes: []string{"10.0.20.0/24"}})
	settings.Channels = append(settings.Channels, channelSettings{Name: "Email", URL: "ntfy://ntfy.sh/other", Enabled: true})
	data, _ = json.Marshal(settings)
	status, body := do(t, app, "POST", "/api/settings-import", string(data))
	if status != fiber.StatusBadRequest || !strings.Contains(body, "networks[1]") {
		t.Errorf("conflicting network = %d %s, want 400 about networks[1]", status, body)
	}
	if channels := exported(t, app, "").Channels; len(channels) != 1 {
		t.Errorf("channels after a refused import = %+v, want only the one there was", channels)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
