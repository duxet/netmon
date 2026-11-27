package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

const (
	testLaptopMAC = "a4:83:e7:1c:2f:90"
	testTVMAC     = "3c:22:fb:8a:01:7e"
	testNASMAC    = "00:11:32:9b:c4:01"
)

// seedNetwork adds, next to newTestApp's NAS -> 140.82.121.4 flow (now):
//
//	laptop -> 140.82.121.4:443   5000 in /  500 out, two hours ago
//	laptop -> Telegram:443       3000 in /  300 out, an hour ago
//	laptop -> NAS:445              20 in / 2000 out, an hour ago
//	TV     -> NAS:32400         42000 in /  800 out, an hour ago
//	laptop -> 1.1.1.1:53 (UDP)     90 in /   70 out, this hour
func seedNetwork(t *testing.T, db *sql.DB) time.Time {
	t.Helper()

	hour := time.Now().Truncate(time.Hour)
	macOf := func(s string) []byte {
		m, err := net.ParseMAC(s)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	ipOf := func(s string) []byte { return netip.MustParseAddr(s).AsSlice() }

	rows := []struct {
		srcMAC, dstMAC []byte
		src, dst       string
		proto          uint8
		port           uint16
		in, out        uint64
		at             time.Time
	}{
		{macOf(testLaptopMAC), nil, "192.168.1.23", "140.82.121.4", 6, 443, 5000, 500, hour.Add(-2 * time.Hour)},
		{macOf(testLaptopMAC), nil, "192.168.1.23", "149.154.167.51", 6, 443, 3000, 300, hour.Add(-time.Hour)},
		{macOf(testLaptopMAC), macOf(testNASMAC), "192.168.1.23", "192.168.1.10", 6, 445, 20, 2000, hour.Add(-time.Hour)},
		{macOf(testTVMAC), macOf(testNASMAC), "192.168.1.41", "192.168.1.10", 6, 32400, 42000, 800, hour.Add(-time.Hour)},
		{macOf(testLaptopMAC), nil, "192.168.1.23", "1.1.1.1", 17, 53, 90, 70, hour},
	}
	for _, r := range rows {
		_, err := db.Exec(`INSERT INTO flows VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.srcMAC, r.dstMAC, ipOf(r.src), ipOf(r.dst), r.proto, r.port, r.in, r.in/10, r.out, r.out/10, r.at)
		if err != nil {
			t.Fatal(err)
		}
	}

	return hour
}

func get(t *testing.T, app *fiber.App, path string, out any) (status int, totalCount string) {
	t.Helper()

	resp, err := app.Test(httptest.NewRequest("GET", path, nil), fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == fiber.StatusOK && out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("GET %s: %v in %s", path, err, body)
		}
	}
	return resp.StatusCode, resp.Header.Get(totalCountHeader)
}

type flowJSON struct {
	Source, Destination struct {
		IPAddress string
	}
	Port    uint16
	Traffic TrafficDTO
}

func peers(flows []flowJSON) []string {
	var result []string
	for _, flow := range flows {
		result = append(result, flow.Destination.IPAddress+":"+itoa(flow.Port))
	}
	return result
}

func itoa(n uint16) string { return (&net.TCPAddr{Port: int(n)}).String()[1:] }

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFlowsPaging(t *testing.T) {
	app, db := newTestApp(t)
	seedNetwork(t, db)
	base := "/api/clients/" + testLaptopMAC + "/flows"

	for _, tc := range []struct {
		query string
		want  []string
	}{
		// Sorted by what the laptop downloaded, largest first.
		{"?sort=download&limit=2", []string{"140.82.121.4:443", "149.154.167.51:443"}},
		{"?sort=download&limit=2&offset=2", []string{"1.1.1.1:53", "192.168.1.10:445"}},
		{"?sort=upload&order=asc", []string{"1.1.1.1:53", "149.154.167.51:443", "140.82.121.4:443", "192.168.1.10:445"}},
		{"?q=53", []string{"1.1.1.1:53"}},
		{"?q=UDP", []string{"1.1.1.1:53"}},
		{"?q=149.154", []string{"149.154.167.51:443"}},
		{"?q=nothing-matches", nil},
		{"?limit=2&offset=10", nil},
	} {
		var flows []flowJSON
		status, total := get(t, app, base+tc.query, &flows)
		if status != fiber.StatusOK {
			t.Errorf("%s: status %d", tc.query, status)
			continue
		}
		if got := peers(flows); !equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.query, got, tc.want)
		}
		if tc.query == "?sort=download&limit=2" && total != "4" {
			t.Errorf("%s: X-Total-Count = %q, want 4", tc.query, total)
		}
	}
}

func TestFlowParamsAreValidated(t *testing.T) {
	app, _ := newTestApp(t)

	for _, path := range []string{
		"/api/flows?limit=0",
		"/api/flows?limit=1001",
		"/api/flows?offset=-1",
		"/api/flows?sort=hostname",
		"/api/flows?order=up",
		"/api/flows?from=yesterday",
		"/api/flows?from=2026-11-24T10:00:00Z&to=2026-11-24T09:00:00Z",
		"/api/flows/summary?top=0",
		"/api/traffic-measurements?app=Myspace",
	} {
		if status, _ := get(t, app, path, nil); status != fiber.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, status)
		}
	}
}

func TestFlowSummaryIsSeenFromTheClient(t *testing.T) {
	app, db := newTestApp(t)
	seedNetwork(t, db)

	var summary FlowSummaryDTO
	if status, _ := get(t, app, "/api/flows/summary?top=1&mac="+testNASMAC, &summary); status != fiber.StatusOK {
		t.Fatalf("status %d", status)
	}

	// The NAS downloads what the laptop and TV send it, and uploads the Plex stream.
	if summary.Download != 1000+2000+800 || summary.Upload != 100+20+42000 {
		t.Errorf("totals = %d down / %d up, want 3800 / 42120", summary.Download, summary.Upload)
	}
	if summary.Connections != 3 || summary.Peers != 3 {
		t.Errorf("connections %d, peers %d; want 3 and 3", summary.Connections, summary.Peers)
	}
	if len(summary.TopPeers.Items) != 1 || summary.TopPeers.Items[0].Key != "192.168.1.41" {
		t.Errorf("top peer = %+v, want the TV", summary.TopPeers.Items)
	}
	if rest := summary.TopPeers.Rest; rest.Download != 3000 || rest.Upload != 120 || rest.Connections != 2 {
		t.Errorf("other peers = %+v, want 3000 / 120 over 2 connections", rest)
	}
	if len(summary.Services.Items) != 1 || summary.Services.Items[0].Key != "6/32400" {
		t.Errorf("top service = %+v, want Plex (6/32400)", summary.Services.Items)
	}
}

func TestEndpointsTakeATimeRange(t *testing.T) {
	app, db := newTestApp(t)
	hour := seedNetwork(t, db)
	since := "from=" + url.QueryEscape(hour.Add(-90*time.Minute).UTC().Format(time.RFC3339))

	var stats StatsDTO
	get(t, app, "/api/stats?"+since, &stats)
	if stats.TotalBytes == nil || *stats.TotalBytes != 1100+3300+2020+42800+160 {
		t.Errorf("bytes since 90 minutes ago = %v, want 49380 (without the 5500 from two hours ago)", stats.TotalBytes)
	}

	var flows []flowJSON
	_, total := get(t, app, "/api/clients/"+testLaptopMAC+"/flows?"+since, &flows)
	if total != "3" {
		t.Errorf("laptop flows since 90 minutes ago = %s, want 3", total)
	}

	var clients []struct{ Endpoint struct{ MACAddress string } }
	get(t, app, "/api/clients?to="+url.QueryEscape(hour.Add(-90*time.Minute).UTC().Format(time.RFC3339)), &clients)
	if len(clients) != 1 || clients[0].Endpoint.MACAddress != testLaptopMAC {
		t.Errorf("clients before 90 minutes ago = %+v, want only the laptop", clients)
	}
}

func TestTrafficMeasurementsPerClientAndApp(t *testing.T) {
	app, db := newTestApp(t)
	hour := seedNetwork(t, db)

	var nas []TrafficMeasurementDTO
	get(t, app, "/api/traffic-measurements?mac="+testNASMAC, &nas)
	if len(nas) != 2 || !nas[0].Date.Equal(hour.Add(-time.Hour)) ||
		nas[0].InBytes != 2000+800 || nas[0].OutBytes != 20+42000 {
		t.Errorf("NAS measurements = %+v, want 2800 down / 42020 up an hour ago", nas)
	}

	var telegram []TrafficMeasurementDTO
	get(t, app, "/api/traffic-measurements?app=Telegram", &telegram)
	if len(telegram) != 1 || telegram[0].InBytes != 3000 || telegram[0].OutBytes != 300 {
		t.Errorf("Telegram measurements = %+v, want one hour with 3000 / 300", telegram)
	}

	var other []TrafficMeasurementDTO
	get(t, app, "/api/traffic-measurements?app=Other&mac="+testLaptopMAC, &other)
	if len(other) != 2 || other[0].InBytes != 5000 || other[1].InBytes != 90 {
		t.Errorf("laptop's unrecognized traffic = %+v, want GitHub then DNS", other)
	}
}

func TestLabelASNsNamesGroupsAfterTheirOrganization(t *testing.T) {
	groups := GroupsDTO{Items: []GroupDTO{{Key: "15169"}, {Key: "13335"}, {Key: "64500"}}}
	labelASNs(&groups, map[string]*ASNDTO{
		"8.8.8.8":  {Number: 15169, Organization: "GOOGLE"},
		"8.8.4.4":  {Number: 15169, Organization: "GOOGLE"},
		"1.1.1.1":  {Number: 13335, Organization: "CLOUDFLARENET"},
		"10.0.0.1": nil,
	})

	got := []string{groups.Items[0].Label, groups.Items[1].Label, groups.Items[2].Label}
	want := []string{"GOOGLE", "CLOUDFLARENET", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("labels = %q, want %q", got, want)
			break
		}
	}
}

func TestSummaryHasNoASNsWithoutADatabase(t *testing.T) {
	addr, err := common.ParseIPAddress("8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	peers := []storage.PeerTraffic{{Peer: *addr, Download: 10, Upload: 5, Flows: 1}}

	summary := summarizeFlows(peers, nil, 5, nil, nil)
	if len(summary.ASNs.Items) != 0 || summary.ASNs.Rest.Connections != 0 {
		t.Errorf("ASNs = %+v, want none without a database", summary.ASNs)
	}
	if summary.Peers != 1 {
		t.Errorf("peers = %d, want 1", summary.Peers)
	}
	if asn := asnOf(nil, *addr); asn != nil {
		t.Errorf("asnOf = %+v, want nil without a database", asn)
	}
}
