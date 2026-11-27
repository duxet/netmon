package api

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/duxet/netmon/live"
	"github.com/gofiber/fiber/v3"
)

var (
	liveStart = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	laptopMAC = live.MAC{0xa4, 0x83, 0xe7, 0x1c, 0x2f, 0x90}
	phoneMAC  = live.MAC{0x00, 0x11, 0x32, 0x9b, 0xc4, 0x01}
)

// newLiveTestApp returns an app whose meter has had one sample every second
// from liveStart: n seconds, in each of which the laptop downloaded 1000 bytes
// more than in the previous one.
func newLiveTestApp(t *testing.T, seconds int) (*fiber.App, *live.Meter) {
	t.Helper()

	meter := live.NewMeter()
	meter.Tick(liveStart)
	for i := 1; i <= seconds; i++ {
		meter.Record(laptopMAC, netip.MustParseAddr("192.168.1.23"), uint64(i)*1000, 50)
		meter.Tick(liveStart.Add(time.Duration(i) * time.Second))
	}

	app, _ := newTestAppWith(t, Options{Live: meter})

	return app, meter
}

func getLive(t *testing.T, app *fiber.App, query string) []LiveSampleDTO {
	t.Helper()

	status, body := do(t, app, "GET", "/api/live"+query, "")
	if status != fiber.StatusOK {
		t.Fatalf("GET /api/live%s = %d: %s", query, status, body)
	}

	var samples []LiveSampleDTO
	if err := json.Unmarshal([]byte(body), &samples); err != nil {
		t.Fatalf("body %q is not samples: %v", body, err)
	}

	return samples
}

func TestLiveReturnsTheSamplesWithRatesPerDevice(t *testing.T) {
	app, _ := newLiveTestApp(t, 3)

	samples := getLive(t, app, "")
	if len(samples) != 3 {
		t.Fatalf("got %d samples, want 3", len(samples))
	}

	last := samples[2]
	if !last.Time.Equal(liveStart.Add(3 * time.Second)) {
		t.Errorf("last sample at %s, want 3 seconds in", last.Time)
	}

	want := LiveDeviceDTO{MACAddress: "a4:83:e7:1c:2f:90", IPAddress: "192.168.1.23", Download: 3000, Upload: 50}
	if len(last.Devices) != 1 || last.Devices[0] != want {
		t.Errorf("last sample devices = %+v, want %+v", last.Devices, want)
	}
}

// The web UI keeps what it has and only asks for what is new, handing back the
// time of the newest sample as the server printed it.
func TestLiveSinceReturnsOnlyNewerSamples(t *testing.T) {
	app, meter := newLiveTestApp(t, 3)

	all := getLive(t, app, "")
	newest := all[len(all)-1].Time.Format(time.RFC3339Nano)

	if got := getLive(t, app, "?since="+url.QueryEscape(newest)); len(got) != 0 {
		t.Errorf("samples since the newest = %+v, want none", got)
	}

	since := all[0].Time.Format(time.RFC3339Nano)
	if got := getLive(t, app, "?since="+url.QueryEscape(since)); len(got) != 2 || !got[0].Time.Equal(all[1].Time) {
		t.Errorf("samples since the first = %+v, want the other two", got)
	}

	meter.Tick(liveStart.Add(4 * time.Second))
	got := getLive(t, app, "?since="+url.QueryEscape(newest))
	if len(got) != 1 || !got[0].Time.Equal(liveStart.Add(4*time.Second)) {
		t.Errorf("samples since the newest after another tick = %+v, want just that one", got)
	}
}

func TestLiveSampleTimesSurviveTheRoundTrip(t *testing.T) {
	meter := live.NewMeter()
	meter.Tick(liveStart)
	meter.Tick(liveStart.Add(time.Second + 123_456_789*time.Nanosecond))
	app, _ := newTestAppWith(t, Options{Live: meter})

	_, body := do(t, app, "GET", "/api/live", "")
	if !strings.Contains(body, `"Time":"2026-11-24T10:00:01.123Z"`) {
		t.Fatalf("body = %s, want the time to the millisecond", body)
	}

	// JavaScript dates have millisecond precision. A time that had more would
	// be handed back rounded down, which is before the sample it came from,
	// and so would return that sample again.
	var samples []LiveSampleDTO
	if err := json.Unmarshal([]byte(body), &samples); err != nil {
		t.Fatal(err)
	}
	since := url.QueryEscape(samples[0].Time.Format(time.RFC3339Nano))
	if got := getLive(t, app, "?since="+since); len(got) != 0 {
		t.Errorf("the sample came back after its own time: %+v", got)
	}
}

func TestLiveEncodesNoSamplesAndQuietSecondsAsEmptyLists(t *testing.T) {
	app, _ := newLiveTestApp(t, 0)
	if _, body := do(t, app, "GET", "/api/live", ""); body != "[]" {
		t.Errorf("no samples = %s, want []", body)
	}

	meter := live.NewMeter()
	meter.Tick(liveStart)
	meter.Tick(liveStart.Add(time.Second))
	app, _ = newTestAppWith(t, Options{Live: meter})

	_, body := do(t, app, "GET", "/api/live", "")
	if !strings.Contains(body, `"Devices":[]`) {
		t.Errorf("a second without traffic = %s, want an empty list of devices rather than null", body)
	}
}

func TestLiveWithoutAMeterIsEmpty(t *testing.T) {
	app, _ := newTestApp(t)

	if _, body := do(t, app, "GET", "/api/live", ""); body != "[]" {
		t.Errorf("body = %s, want []", body)
	}
}

func TestLiveLeavesOutDevicesBelowAByte(t *testing.T) {
	meter := live.NewMeter()
	meter.Tick(liveStart)
	meter.Record(laptopMAC, netip.Addr{}, 1, 0) // over 10 seconds: 0.1 bytes per second
	meter.Record(phoneMAC, netip.Addr{}, 0, 24) // 2.4 bytes per second
	meter.Tick(liveStart.Add(10 * time.Second))
	app, _ := newTestAppWith(t, Options{Live: meter})

	samples := getLive(t, app, "")
	if len(samples) != 1 || len(samples[0].Devices) != 1 {
		t.Fatalf("samples = %+v, want one with only the phone", samples)
	}

	want := LiveDeviceDTO{MACAddress: "00:11:32:9b:c4:01", Upload: 2}
	if got := samples[0].Devices[0]; got != want {
		t.Errorf("device = %+v, want %+v: rounded, and without an address as none was known", got, want)
	}
}

func TestLiveRequestsMakeCollectorsMeasure(t *testing.T) {
	app, meter := newLiveTestApp(t, 1)

	if meter.Watched(time.Now()) {
		t.Fatal("the meter is watched before anyone asked")
	}

	getLive(t, app, "")

	if !meter.Watched(time.Now()) {
		t.Error("the meter is not watched after a request")
	}
}

func TestLiveRejectsABadSince(t *testing.T) {
	app, meter := newLiveTestApp(t, 1)

	for _, since := range []string{"yesterday", "1700000000", "2026-11-24"} {
		status, body := do(t, app, "GET", "/api/live?since="+url.QueryEscape(since), "")
		if status != fiber.StatusBadRequest {
			t.Errorf("since=%s = %d, want 400", since, status)
			continue
		}
		if msg := errorMessage(t, body); !strings.Contains(msg, "since must be an RFC 3339 timestamp") {
			t.Errorf("since=%s error = %q", since, msg)
		}
	}

	if meter.Watched(time.Now()) {
		t.Error("a rejected request made collectors measure")
	}
}

func TestLiveIsNotCached(t *testing.T) {
	app, _ := newLiveTestApp(t, 1)

	resp, err := app.Test(httptest.NewRequest("GET", "/api/live", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestLiveCountsMergedAddressesAsTheirDevice(t *testing.T) {
	phone, _ := net.ParseMAC("da:a1:19:00:00:01")
	random, _ := net.ParseMAC("da:a1:19:00:00:02")
	samples := []live.Sample{{Time: time.Now(), Devices: []live.DeviceRate{
		{MAC: live.MAC(phone[:6]), Download: 1000, Upload: 100},
		{MAC: live.MAC(random[:6]), Download: 500, Upload: 50},
	}}}

	dtos := toLiveDTOs(samples, func(mac string) string {
		if mac == random.String() {
			return phone.String()
		}
		return mac
	})
	if devices := dtos[0].Devices; len(devices) != 1 || devices[0].MACAddress != phone.String() || devices[0].Download != 1500 {
		t.Errorf("devices = %+v, want the phone with both addresses' traffic", devices)
	}
}
