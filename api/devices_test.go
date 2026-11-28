package api

import (
	"encoding/json"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/duxet/netmon/localnames"
	"github.com/duxet/netmon/storage"
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

func clientIcon(t *testing.T, app *fiber.App, mac string) *string {
	t.Helper()
	var clients []struct {
		Icon     *string
		Endpoint struct{ MACAddress string }
	}
	if status, _ := get(t, app, "/api/clients", &clients); status != fiber.StatusOK {
		t.Fatalf("GET /api/clients = %d", status)
	}
	for _, client := range clients {
		if client.Endpoint.MACAddress == mac {
			return client.Icon
		}
	}
	t.Fatalf("client %s not listed", mac)
	return nil
}

func TestSetDeviceIcon(t *testing.T) {
	app, _ := newTestApp(t)

	if icon := clientIcon(t, app, testNASMAC); icon != nil {
		t.Fatalf("icon before picking one = %q, want none", *icon)
	}

	status, body := do(t, app, "PUT", "/api/devices/00-11-32-9B-C4-01/icon", `{"icon": "nas"}`)
	if status != fiber.StatusOK {
		t.Fatalf("PUT icon = %d: %s", status, body)
	}
	var dto DeviceIconDTO
	if err := json.Unmarshal([]byte(body), &dto); err != nil || dto.MACAddress != testNASMAC || dto.Icon != "nas" {
		t.Errorf("response = %s, want the canonical MAC and the icon", body)
	}
	if icon := clientIcon(t, app, testNASMAC); icon == nil || *icon != "nas" {
		t.Errorf("listed icon = %v, want nas", icon)
	}

	// Picking another replaces it, and leaves the name alone.
	do(t, app, "PUT", "/api/devices/"+testNASMAC+"/name", `{"name": "NAS"}`)
	do(t, app, "PUT", "/api/devices/"+testNASMAC+"/icon", `{"icon": "server"}`)
	if icon := clientIcon(t, app, testNASMAC); icon == nil || *icon != "server" {
		t.Errorf("listed icon = %v, want server", icon)
	}

	if status, _ := do(t, app, "DELETE", "/api/devices/"+testNASMAC+"/icon", ""); status != fiber.StatusNoContent {
		t.Errorf("DELETE icon = %d, want 204", status)
	}
	if icon := clientIcon(t, app, testNASMAC); icon != nil {
		t.Errorf("icon after reset = %q, want none", *icon)
	}
	if name := clientName(t, app, testNASMAC); name == nil || *name != "NAS" {
		t.Errorf("name after removing the icon = %v, want NAS", name)
	}
}

func TestDeviceIconValidation(t *testing.T) {
	app, _ := newTestApp(t)

	for _, tc := range []struct {
		method, path, body string
	}{
		{"PUT", "/api/devices/not-a-mac/icon", `{"icon": "nas"}`},
		{"DELETE", "/api/devices/not-a-mac/icon", ""},
		{"PUT", "/api/devices/" + testNASMAC + "/icon", `{"icon": ""}`},
		{"PUT", "/api/devices/" + testNASMAC + "/icon", `{"icon": "toaster"}`},
		// Icons are matched exactly.
		{"PUT", "/api/devices/" + testNASMAC + "/icon", `{"icon": "NAS"}`},
		{"PUT", "/api/devices/" + testNASMAC + "/icon", `{"icon": `},
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

	// Every icon is accepted.
	for _, icon := range DeviceIcons {
		if status, body := do(t, app, "PUT", "/api/devices/"+testNASMAC+"/icon", `{"icon": "`+icon+`"}`); status != fiber.StatusOK {
			t.Errorf("icon %s = %d: %s", icon, status, body)
		}
	}
}

func TestClientsHaveVendors(t *testing.T) {
	app, _ := newTestApp(t)

	var clients []struct {
		Vendor   *string
		Endpoint struct{ MACAddress string }
	}
	get(t, app, "/api/clients", &clients)
	if len(clients) != 1 || clients[0].Vendor == nil || *clients[0].Vendor != "Synology" {
		t.Errorf("clients = %+v, want the NAS (00:11:32) made by Synology", clients)
	}
}

func TestListDevices(t *testing.T) {
	app, db := newTestApp(t)

	type device struct {
		MACAddress, IPAddress string
		Name, Icon, Vendor    *string
		FirstSeen, LastSeen   time.Time
	}

	// The test flow was written after the migrations seeded the table, so
	// there is no device until the devices are synced.
	if status, body := do(t, app, "GET", "/api/devices", ""); status != fiber.StatusOK || body != "[]" {
		t.Fatalf("GET /api/devices = %d %s, want an empty list", status, body)
	}

	if err := storage.SyncDevices(db, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	do(t, app, "PUT", "/api/devices/"+testNASMAC+"/name", `{"name": "NAS"}`)
	do(t, app, "PUT", "/api/devices/"+testNASMAC+"/icon", `{"icon": "nas"}`)

	var devices []device
	if status, _ := get(t, app, "/api/devices", &devices); status != fiber.StatusOK || len(devices) != 1 {
		t.Fatalf("GET /api/devices = %d %+v, want the NAS", status, devices)
	}
	nas := devices[0]
	if nas.MACAddress != testNASMAC || nas.IPAddress != "192.168.1.10" ||
		nas.Name == nil || *nas.Name != "NAS" || nas.Icon == nil || *nas.Icon != "nas" ||
		nas.Vendor == nil || *nas.Vendor != "Synology" ||
		nas.FirstSeen.IsZero() || nas.LastSeen.Before(nas.FirstSeen) {
		t.Errorf("device = %+v, want the named Synology NAS at 192.168.1.10, with its icon", nas)
	}

	// A failing query answers with a 500.
	if _, err := db.Exec(`DROP TABLE devices`); err != nil {
		t.Fatal(err)
	}
	if status, body := do(t, app, "GET", "/api/devices", ""); status != fiber.StatusInternalServerError {
		t.Errorf("GET /api/devices without the table = %d %s, want 500", status, body)
	}
}

func TestDevicesHaveLocalNames(t *testing.T) {
	local := localnames.New()
	nas, _ := net.ParseMAC(testNASMAC)
	// The NAS asked for its name with another address than it has now.
	local.Learn(localnames.Observation{MAC: nas, Addr: netip.MustParseAddr("192.168.1.99"), Name: "diskstation", Source: localnames.DHCP})
	local.Learn(localnames.Observation{Addr: netip.MustParseAddr("192.168.1.10"), Name: "DiskStation.local", Source: localnames.MDNS})
	hostnames := noHostnames()
	hostnames.SetLocalNames(local.ByIP)
	app, db := newTestAppWith(t, Options{LocalNames: local, Hostnames: hostnames})
	if err := storage.SyncDevices(db, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	var devices []struct{ Hostname *string }
	if status, _ := get(t, app, "/api/devices", &devices); status != fiber.StatusOK || len(devices) != 1 {
		t.Fatalf("GET /api/devices = %d %+v", status, devices)
	}
	if h := devices[0].Hostname; h == nil || *h != "diskstation." {
		t.Errorf("hostname = %v, want the DHCP name of the NAS's MAC address", h)
	}

	var clients []struct {
		Hostname *string
		Endpoint struct{ MACAddress string }
	}
	if status, _ := get(t, app, "/api/clients", &clients); status != fiber.StatusOK || len(clients) != 1 {
		t.Fatalf("GET /api/clients = %d %+v", status, clients)
	}
	if h := clients[0].Hostname; h == nil || *h != "DiskStation.local." {
		t.Errorf("client hostname = %v, want the mDNS name of its address", h)
	}
}

func TestForgetAndMergeDevices(t *testing.T) {
	app, db := newTestApp(t)
	seedNetwork(t, db)
	if err := storage.SyncDevices(db, time.Time{}); err != nil {
		t.Fatal(err)
	}

	if status, body := do(t, app, "POST", "/api/devices/"+testTVMAC+"/merge", `{"macs": ["`+testLaptopMAC+`"]}`); status != fiber.StatusNoContent {
		t.Fatalf("merge = %d %s", status, body)
	}
	type device struct {
		MACAddress string
		Aliases    []string
	}
	type client struct{ Endpoint struct{ MACAddress string } }

	var devices []device
	get(t, app, "/api/devices", &devices)
	var tv *device
	for i := range devices {
		if devices[i].MACAddress == testLaptopMAC {
			t.Errorf("the laptop is still listed after merging it into the TV")
		}
		if devices[i].MACAddress == testTVMAC {
			tv = &devices[i]
		}
	}
	if tv == nil || len(tv.Aliases) != 1 || tv.Aliases[0] != testLaptopMAC {
		t.Fatalf("TV = %+v, want the laptop's address merged into it", tv)
	}

	var clients []client
	get(t, app, "/api/clients", &clients)
	for _, c := range clients {
		if c.Endpoint.MACAddress == testLaptopMAC {
			t.Errorf("client %+v is still the laptop's", c)
		}
	}

	if status, _ := do(t, app, "DELETE", "/api/devices/"+testTVMAC+"/aliases/"+testLaptopMAC, ""); status != fiber.StatusNoContent {
		t.Errorf("unmerge = %d, want 204", status)
	}
	if status, _ := do(t, app, "DELETE", "/api/devices/"+testTVMAC+"/aliases/"+testLaptopMAC, ""); status != fiber.StatusNotFound {
		t.Errorf("unmerge again = %d, want 404", status)
	}

	if status, _ := do(t, app, "DELETE", "/api/devices/"+testTVMAC+"?traffic=true", ""); status != fiber.StatusNoContent {
		t.Errorf("forget = %d, want 204", status)
	}
	get(t, app, "/api/clients", &clients)
	for _, c := range clients {
		if c.Endpoint.MACAddress == testTVMAC {
			t.Errorf("client %+v is the forgotten TV's", c)
		}
	}

	for _, bad := range []string{`{"macs": []}`, `{"macs": ["nope"]}`} {
		if status, _ := do(t, app, "POST", "/api/devices/"+testTVMAC+"/merge", bad); status != fiber.StatusBadRequest {
			t.Errorf("merge %s = %d, want 400", bad, status)
		}
	}
}
