package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

const testIoTTVMAC = "3c:22:fb:00:00:05"

// addTV adds a TV on 192.168.20.5 that streams from the internet, and the
// NAS sending it a file.
func addTV(t *testing.T, db *sql.DB) {
	t.Helper()
	tv, _ := net.ParseMAC(testIoTTVMAC)
	nas, _ := net.ParseMAC(testNASMAC)
	for _, flow := range []struct {
		srcMAC, dstMAC []byte
		src, dst       string
		port           int
		in, out        int
	}{
		{tv, nil, "192.168.20.5", "142.250.186.78", 443, 5000, 50},
		{nas, tv, "192.168.1.10", "192.168.20.5", 445, 30, 300},
	} {
		_, err := db.Exec(`INSERT INTO flows VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			flow.srcMAC, flow.dstMAC, netip.MustParseAddr(flow.src).AsSlice(), netip.MustParseAddr(flow.dst).AsSlice(),
			6, flow.port, flow.in, 1, flow.out, 1, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
}

func createNetwork(t *testing.T, app *fiber.App, body string) NetworkDTO {
	t.Helper()
	status, resp := do(t, app, "POST", "/api/networks", body)
	if status != fiber.StatusCreated {
		t.Fatalf("POST /api/networks %s = %d: %s", body, status, resp)
	}
	var n NetworkDTO
	if err := json.Unmarshal([]byte(resp), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNetworksCRUD(t *testing.T) {
	app, _ := newTestApp(t)

	if status, body := do(t, app, "GET", "/api/networks", ""); status != fiber.StatusOK || body != "[]" {
		t.Fatalf("GET /api/networks = %d %s, want an empty list", status, body)
	}

	// Host bits are cleared and repeats dropped; the first network gets the
	// first color.
	lan := createNetwork(t, app, `{"name": " LAN ", "prefixes": ["192.168.1.1/24", "192.168.1.0/24", "fd00:1::/64"]}`)
	if lan.Name != "LAN" || lan.VLAN != nil || lan.Color != "blue" ||
		strings.Join(lan.Prefixes, ",") != "192.168.1.0/24,fd00:1::/64" {
		t.Errorf("created = %+v", lan)
	}

	iot := createNetwork(t, app, `{"name": "IoT", "vlan": 20, "color": "violet", "prefixes": ["192.168.20.0/24"]}`)
	if iot.VLAN == nil || *iot.VLAN != 20 || iot.Color != "violet" {
		t.Errorf("created = %+v", iot)
	}

	// An update replaces everything but a color left out.
	path := "/api/networks/" + strconv.FormatInt(iot.ID, 10)
	status, body := do(t, app, "PUT", path, `{"name": "Smart home", "prefixes": ["192.168.20.0/24", "192.168.21.0/24"]}`)
	if status != fiber.StatusOK {
		t.Fatalf("PUT = %d: %s", status, body)
	}
	var updated NetworkDTO
	_ = json.Unmarshal([]byte(body), &updated)
	if updated.Name != "Smart home" || updated.VLAN != nil || updated.Color != "violet" || len(updated.Prefixes) != 2 {
		t.Errorf("updated = %+v", updated)
	}

	var got NetworkDTO
	if status, _ := get(t, app, path, &got); status != fiber.StatusOK || got.Name != "Smart home" {
		t.Errorf("GET %s = %d %+v", path, status, got)
	}

	var list []NetworkDTO
	get(t, app, "/api/networks", &list)
	if len(list) != 2 || list[0].ID != lan.ID || list[1].ID != iot.ID {
		t.Errorf("list = %+v, want both, oldest first", list)
	}

	if status, _ := do(t, app, "DELETE", path, ""); status != fiber.StatusNoContent {
		t.Errorf("DELETE = %d, want 204", status)
	}
	for _, method := range []string{"GET", "DELETE"} {
		if status, _ := do(t, app, method, path, ""); status != fiber.StatusNotFound {
			t.Errorf("%s a deleted network = %d, want 404", method, status)
		}
	}
	if status, _ := do(t, app, "PUT", path, `{"name": "x", "prefixes": ["10.0.0.0/8"]}`); status != fiber.StatusNotFound {
		t.Errorf("PUT a deleted network = %d, want 404", status)
	}
}

func TestNetworkValidation(t *testing.T) {
	app, _ := newTestApp(t)
	lan := createNetwork(t, app, `{"name": "LAN", "prefixes": ["192.168.1.0/24"]}`)
	lanPath := "/api/networks/" + strconv.FormatInt(lan.ID, 10)

	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/api/networks", `{"prefixes": ["10.0.0.0/8"]}`, fiber.StatusBadRequest},
		{"POST", "/api/networks", `{"name": "Lab"}`, fiber.StatusBadRequest},
		{"POST", "/api/networks", `{"name": "  ", "prefixes": ["10.0.0.0/8"]}`, fiber.StatusBadRequest},
		{"POST", "/api/networks", `{"name": "Lab", "prefixes": []}`, fiber.StatusBadRequest},
		{"POST", "/api/networks", `{"name": "Lab", "prefixes": ["10.0.0.0/33"]}`, fiber.StatusBadRequest},
		{"POST", "/api/networks", `{"name": "Lab", "prefixes": ["10.0.0.0/8"], "vlan": 0}`, fiber.StatusBadRequest},
		{"POST", "/api/networks", `{"name": "Lab", "prefixes": ["10.0.0.0/8"], "vlan": 4095}`, fiber.StatusBadRequest},
		{"POST", "/api/networks", `{"name": "Lab", "prefixes": ["10.0.0.0/8"], "color": "chartreuse"}`, fiber.StatusBadRequest},
		{"POST", "/api/networks", `{"name": `, fiber.StatusBadRequest},
		// Names and blocks can't be another network's.
		{"POST", "/api/networks", `{"name": "lan", "prefixes": ["10.0.0.0/8"]}`, fiber.StatusConflict},
		{"POST", "/api/networks", `{"name": "Lab", "prefixes": ["10.0.0.0/8", "192.168.1.5/24"]}`, fiber.StatusConflict},
		{"PUT", "/api/networks/nope", `{"name": "Lab", "prefixes": ["10.0.0.0/8"]}`, fiber.StatusBadRequest},
		{"GET", "/api/networks/0", "", fiber.StatusBadRequest},
		{"GET", "/api/networks/summary?from=nope", "", fiber.StatusBadRequest},
		{"GET", "/api/stats?network=nope", "", fiber.StatusBadRequest},
		{"GET", "/api/stats?network=99", "", fiber.StatusNotFound},
	} {
		status, body := do(t, app, tc.method, tc.path, tc.body)
		if status != tc.status {
			t.Errorf("%s %s %s = %d %s, want %d", tc.method, tc.path, tc.body, status, body, tc.status)
			continue
		}
		if msg := errorMessage(t, body); msg == "" {
			t.Errorf("%s %s: empty error message", tc.method, tc.path)
		}
	}

	// Saving a network with its own name and blocks is no conflict, and
	// blocks inside another network's are fine.
	if status, body := do(t, app, "PUT", lanPath, `{"name": "LAN", "prefixes": ["192.168.1.0/24"]}`); status != fiber.StatusOK {
		t.Errorf("saving unchanged = %d: %s", status, body)
	}
	createNetwork(t, app, `{"name": "Printers", "prefixes": ["192.168.1.64/28"]}`)
}

func TestClientsAndDevicesHaveNetworks(t *testing.T) {
	app, db := newTestApp(t)
	addTV(t, db)
	if err := storage.SyncDevices(db, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	lan := createNetwork(t, app, `{"name": "LAN", "prefixes": ["192.168.1.0/24"]}`)

	var clients []struct {
		NetworkID *int64
		Endpoint  struct{ MACAddress string }
	}
	get(t, app, "/api/clients", &clients)
	networkOf := map[string]*int64{}
	for _, c := range clients {
		networkOf[c.Endpoint.MACAddress] = c.NetworkID
	}
	if id := networkOf[testNASMAC]; id == nil || *id != lan.ID {
		t.Errorf("NAS network = %v, want LAN (%d)", id, lan.ID)
	}
	if id, ok := networkOf[testIoTTVMAC]; !ok || id != nil {
		t.Errorf("TV network = %v (listed %v), want none", id, ok)
	}

	// network= lists only the clients in the network.
	get(t, app, "/api/clients?network="+strconv.FormatInt(lan.ID, 10), &clients)
	if len(clients) != 1 || clients[0].Endpoint.MACAddress != testNASMAC {
		t.Errorf("LAN clients = %+v, want the NAS", clients)
	}

	var devices []struct {
		MACAddress string
		NetworkID  *int64
	}
	get(t, app, "/api/devices", &devices)
	for _, d := range devices {
		if (d.MACAddress == testNASMAC) != (d.NetworkID != nil) {
			t.Errorf("device %s network = %v", d.MACAddress, d.NetworkID)
		}
	}
}

func TestNetworkSummary(t *testing.T) {
	app, db := newTestApp(t)
	addTV(t, db)

	var summary NetworkSummaryDTO
	if status, _ := get(t, app, "/api/networks/summary", &summary); status != fiber.StatusOK {
		t.Fatalf("summary = %d", status)
	}
	if len(summary.Networks) != 0 || summary.UnassignedDevices != 2 {
		t.Errorf("summary without networks = %+v, want 2 unassigned devices", summary)
	}

	lan := createNetwork(t, app, `{"name": "LAN", "prefixes": ["192.168.1.0/24"]}`)
	iot := createNetwork(t, app, `{"name": "IoT", "prefixes": ["192.168.20.0/24"]}`)
	empty := createNetwork(t, app, `{"name": "Guests", "prefixes": ["192.168.50.0/24"]}`)

	get(t, app, "/api/networks/summary?from="+time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), &summary)
	if summary.UnassignedDevices != 0 || len(summary.Networks) != 3 {
		t.Fatalf("summary = %+v, want all three networks", summary)
	}
	want := []NetworkTrafficDTO{
		// The NAS to GitHub (1000 down, 100 up, 11 packets) and to the TV (30 down, 300 up).
		{NetworkID: lan.ID, Devices: 1, Download: 1030, Upload: 400, Packets: 13},
		// The TV from YouTube (5000 down, 50 up) and from the NAS (300 down, 30 up).
		{NetworkID: iot.ID, Devices: 1, Download: 5300, Upload: 80, Packets: 4},
		{NetworkID: empty.ID},
	}
	for i, w := range want {
		if summary.Networks[i] != w {
			t.Errorf("network %d = %+v, want %+v", i, summary.Networks[i], w)
		}
	}

	// The network's own pages count the same traffic, from its side.
	var measurements []TrafficMeasurementDTO
	get(t, app, "/api/traffic-measurements?network="+strconv.FormatInt(iot.ID, 10), &measurements)
	if len(measurements) != 1 || measurements[0].InBytes != 5300 || measurements[0].OutBytes != 80 {
		t.Errorf("IoT measurements = %+v, want 5300 down and 80 up", measurements)
	}

	var flowSummary FlowSummaryDTO
	get(t, app, "/api/flows/summary?network="+strconv.FormatInt(iot.ID, 10), &flowSummary)
	if flowSummary.Download != 5300 || flowSummary.Upload != 80 || flowSummary.Connections != 2 || flowSummary.Peers != 2 {
		t.Errorf("IoT flow summary = %+v", flowSummary)
	}

	var stats StatsDTO
	get(t, app, "/api/stats?network="+strconv.FormatInt(lan.ID, 10), &stats)
	if stats.TotalClients != 1 || stats.TotalBytes == nil || *stats.TotalBytes != 1430 {
		t.Errorf("LAN stats = %+v, want the NAS and 1430 bytes", stats)
	}
}

func TestNetworkSuggestions(t *testing.T) {
	app, db := newTestApp(t)
	addTV(t, db)
	if err := storage.SyncDevices(db, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	var suggestions []NetworkSuggestionDTO
	get(t, app, "/api/networks/suggestions", &suggestions)
	if len(suggestions) != 2 || suggestions[0].Prefix != "192.168.1.0/24" || suggestions[1].Prefix != "192.168.20.0/24" {
		t.Errorf("suggestions = %+v, want the NAS's and the TV's /24", suggestions)
	}

	createNetwork(t, app, `{"name": "LAN", "prefixes": ["192.168.1.0/24"]}`)
	get(t, app, "/api/networks/suggestions", &suggestions)
	if len(suggestions) != 1 || suggestions[0].Prefix != "192.168.20.0/24" || suggestions[0].Devices != 1 {
		t.Errorf("suggestions = %+v, want only the TV's /24", suggestions)
	}
}

func TestAlertRulesOfNetworks(t *testing.T) {
	app, _ := newTestApp(t)
	iot := createNetwork(t, app, `{"name": "IoT", "prefixes": ["192.168.20.0/24"]}`)

	if status, body := do(t, app, "POST", "/api/alert-rules", `{"name": "Ports", "type": "destination_port",
		"params": {"ports": [22], "protocol": "tcp", "networks": [999]}}`); status != fiber.StatusBadRequest || !strings.Contains(body, "unknown network 999") {
		t.Errorf("a rule of a network that isn't = %d %s, want 400", status, body)
	}
	status, body := do(t, app, "POST", "/api/alert-rules", fmt.Sprintf(`{"name": "IoT ports", "type": "destination_port",
		"params": {"ports": [22], "protocol": "tcp", "networks": [%d]}}`, iot.ID))
	if status != fiber.StatusCreated {
		t.Fatalf("POST rule = %d %s", status, body)
	}

	path := fmt.Sprintf("/api/networks/%d", iot.ID)
	if status, body := do(t, app, "DELETE", path, ""); status != fiber.StatusConflict || !strings.Contains(body, "“IoT ports”") {
		t.Errorf("DELETE a network a rule is limited to = %d %s, want 409 naming the rule", status, body)
	}
	do(t, app, "DELETE", "/api/alert-rules/1", "")
	if status, body := do(t, app, "DELETE", path, ""); status != fiber.StatusNoContent {
		t.Errorf("DELETE the network without rules = %d %s, want 204", status, body)
	}
}
