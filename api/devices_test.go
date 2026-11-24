package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func clientName(t *testing.T, app *fiber.App, mac string) *string {
	t.Helper()
	var clients []struct {
		Name     *string
		Endpoint struct{ MACAddress string }
	}
	if status, _ := get(t, app, "/api/clients", &clients); status != fiber.StatusOK {
		t.Fatalf("GET /api/clients = %d", status)
	}
	for _, client := range clients {
		if client.Endpoint.MACAddress == mac {
			return client.Name
		}
	}
	t.Fatalf("client %s not listed", mac)
	return nil
}

func TestRenameDevice(t *testing.T) {
	app, _ := newTestApp(t)

	if name := clientName(t, app, testNASMAC); name != nil {
		t.Fatalf("name before renaming = %q, want none", *name)
	}

	// MAC addresses are accepted in any common notation.
	status, body := do(t, app, "PUT", "/api/devices/00-11-32-9B-C4-01/name", `{"name": "  Attic NAS  "}`)
	if status != fiber.StatusOK {
		t.Fatalf("PUT name = %d: %s", status, body)
	}
	var dto DeviceNameDTO
	if err := json.Unmarshal([]byte(body), &dto); err != nil || dto.MACAddress != testNASMAC || dto.Name != "Attic NAS" {
		t.Errorf("response = %s, want the canonical MAC and the trimmed name", body)
	}
	if name := clientName(t, app, testNASMAC); name == nil || *name != "Attic NAS" {
		t.Errorf("listed name = %v, want Attic NAS", name)
	}

	// Renaming again replaces the name.
	do(t, app, "PUT", "/api/devices/"+testNASMAC+"/name", `{"name": "NAS"}`)
	if name := clientName(t, app, testNASMAC); name == nil || *name != "NAS" {
		t.Errorf("listed name = %v, want NAS", name)
	}

	if status, _ := do(t, app, "DELETE", "/api/devices/"+testNASMAC+"/name", ""); status != fiber.StatusNoContent {
		t.Errorf("DELETE name = %d, want 204", status)
	}
	if name := clientName(t, app, testNASMAC); name != nil {
		t.Errorf("name after reset = %q, want none", *name)
	}
}

func TestDeviceNameValidation(t *testing.T) {
	app, _ := newTestApp(t)

	for _, tc := range []struct {
		method, path, body string
	}{
		{"PUT", "/api/devices/not-a-mac/name", `{"name": "NAS"}`},
		{"DELETE", "/api/devices/not-a-mac/name", ""},
		{"PUT", "/api/devices/" + testNASMAC + "/name", `{"name": "   "}`},
		{"PUT", "/api/devices/" + testNASMAC + "/name", `{"name": "` + strings.Repeat("x", 65) + `"}`},
		{"PUT", "/api/devices/" + testNASMAC + "/name", `{"name": "NAS\u0007"}`},
		{"PUT", "/api/devices/" + testNASMAC + "/name", `{"name": `},
	} {
		status, body := do(t, app, tc.method, tc.path, tc.body)
		if status != fiber.StatusBadRequest {
			t.Errorf("%s %s %s = %d, want 400", tc.method, tc.path, tc.body, status)
			continue
		}
		if msg := errorMessage(t, body); msg == "" {
			t.Errorf("%s %s: empty error message", tc.method, tc.path)
		}
	}

	// 64 characters, counted as characters rather than bytes, are fine.
	long := strings.Repeat("ż", 64)
	if status, body := do(t, app, "PUT", "/api/devices/"+testNASMAC+"/name", `{"name": "`+long+`"}`); status != fiber.StatusOK {
		t.Errorf("64-character name = %d: %s", status, body)
	}
}
