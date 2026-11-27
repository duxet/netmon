package api

import (
	"database/sql"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/duxet/netmon/collector"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

func TestHealthz(t *testing.T) {
	app, _ := newTestApp(t)
	if status, body := do(t, app, "GET", "/healthz", ""); status != fiber.StatusOK || !strings.Contains(body, `"Status":"ok"`) {
		t.Errorf("GET /healthz = %d %s, want ok", status, body)
	}

	health := collector.NewHealth()
	health.Failed(errors.New("reading connection tracking needs root or CAP_NET_ADMIN"))
	app, _ = newTestAppWith(t, Options{Collector: health})
	if status, body := do(t, app, "GET", "/healthz", ""); status != fiber.StatusServiceUnavailable || !strings.Contains(body, "CAP_NET_ADMIN") {
		t.Errorf("GET /healthz with the collector down = %d %s, want 503 with why", status, body)
	}
}

func TestHealthzNeedsNoSession(t *testing.T) {
	app, _ := newSignInApp(t)

	if resp := request(t, app, "GET", "/healthz", ""); resp.StatusCode != fiber.StatusOK {
		t.Errorf("GET /healthz without a session = %d, want 200", resp.StatusCode)
	}
	if resp := request(t, app, "GET", "/metrics", ""); resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("GET /metrics without a session = %d, want 401", resp.StatusCode)
	}
}

func TestMetrics(t *testing.T) {
	health := collector.NewHealth()
	health.Started()
	totals := collector.NewTotals()
	laptop := net.HardwareAddr{0xa4, 0x83, 0xe7, 0x1c, 0x2f, 0x90}
	nas := net.HardwareAddr{0x00, 0x11, 0x32, 0x9b, 0xc4, 0x01}
	totals.AddFlow(laptop, nil, 5000, 300)
	totals.AddFlow(nas, nil, 10, 20)

	app, db := newTestAppFor(t, func(*sql.DB) Options {
		return Options{
			Collector:  health,
			Totals:     totals,
			Deliveries: func() map[string]uint64 { return map[string]uint64{"sent": 4, "failed": 1} },
		}
	})
	if err := storage.SetDeviceName(db, laptop, `Kid's "laptop"`); err != nil {
		t.Fatal(err)
	}

	status, body := do(t, app, "GET", "/metrics", "")
	if status != fiber.StatusOK {
		t.Fatalf("GET /metrics = %d %s", status, body)
	}
	for _, want := range []string{
		"# TYPE netmon_info gauge\nnetmon_info{version=",
		"netmon_collector_running 1\n",
		"# TYPE netmon_collector_rounds_total counter\nnetmon_collector_rounds_total 0\n",
		"netmon_alerts_firing 0\n",
		`netmon_notification_deliveries_total{status="sent"} 4` + "\n",
		`netmon_notification_deliveries_total{status="suppressed"} 0` + "\n",
		`netmon_device_received_bytes_total{mac="a4:83:e7:1c:2f:90",name="Kid's \"laptop\""} 5000` + "\n",
		`netmon_device_sent_bytes_total{mac="00:11:32:9b:c4:01",name=""} 20` + "\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics don't have %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "last_recorded") {
		t.Error("metrics have a last recorded time before anything was recorded")
	}
}

func TestMetricsWithoutCollector(t *testing.T) {
	app, _ := newTestApp(t)

	status, body := do(t, app, "GET", "/metrics", "")
	if status != fiber.StatusOK || !strings.Contains(body, "netmon_info{") || strings.Contains(body, "netmon_collector") {
		t.Errorf("GET /metrics = %d %s, want only what netmon knows", status, body)
	}
}
