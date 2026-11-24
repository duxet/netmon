package alerting

import (
	"testing"
	"time"
)

func TestFormatBytes(t *testing.T) {
	for n, want := range map[float64]string{
		0: "0 B", 999: "999 B", 1000: "1 kB", 1234: "1.23 kB", 12345: "12.3 kB", 123456: "123 kB",
		999_500: "1 MB", 5_000_000_000: "5 GB", 6_657_199_308: "6.66 GB",
	} {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%v) = %q, want %q", n, got, want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		20 * time.Second:              "less than a minute",
		time.Minute:                   "1 minute",
		47 * time.Minute:              "47 minutes",
		2*time.Hour + 5*time.Minute:   "2 hours 5 minutes",
		time.Hour:                     "1 hour",
		27*time.Hour + 10*time.Minute: "1 day 3 hours",
	} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestLastWindow(t *testing.T) {
	for seconds, want := range map[int64]string{300: "the last 5 minutes", 3600: "the last hour", 21600: "the last 6 hours", 5400: "the last 90 minutes"} {
		if got := lastWindow(seconds); got != want {
			t.Errorf("lastWindow(%d) = %q, want %q", seconds, got, want)
		}
	}
}

func TestDeviceLabels(t *testing.T) {
	for _, tc := range []struct {
		device          device
		label, labelled string
	}{
		{device{mac: tvMAC, ip: "192.168.1.40", name: "TV", hostname: "tv.lan.", vendor: "Samsung"}, "TV", "TV (Samsung, " + tvMAC + ", 192.168.1.40)"},
		{device{mac: tvMAC, ip: "192.168.1.40", hostname: "tv.lan."}, "tv.lan", "tv.lan (" + tvMAC + ", 192.168.1.40)"},
		{device{mac: tvMAC, ip: "192.168.1.40", vendor: "Samsung"}, "Samsung device", "Samsung device (" + tvMAC + ", 192.168.1.40)"},
		{device{mac: tvMAC}, tvMAC, tvMAC},
	} {
		if got := tc.device.label(); got != tc.label {
			t.Errorf("label(%+v) = %q, want %q", tc.device, got, tc.label)
		}
		if got := tc.device.labelled(); got != tc.labelled {
			t.Errorf("labelled(%+v) = %q, want %q", tc.device, got, tc.labelled)
		}
	}
}
