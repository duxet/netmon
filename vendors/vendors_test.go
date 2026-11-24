package vendors

import (
	"net"
	"testing"
)

func mac(t *testing.T, s string) net.HardwareAddr {
	t.Helper()
	m, err := net.ParseMAC(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLookup(t *testing.T) {
	for _, tc := range []struct{ mac, want string }{
		// MA-L (24-bit) assignments.
		{"00:11:32:9b:c4:01", "Synology"},
		{"3c:22:fb:8a:01:7e", "Apple"},
		{"dc:a6:32:07:19:4f", "Raspberry Pi Trading"},
		// MA-M (28-bit): 00:55:DA:0x and 00:55:DA:1x belong to different companies.
		{"00:55:da:01:23:45", "Shinko Technos"},
		{"00:55:da:1f:ff:ff", "KoolPOS"},
		// MA-S (36-bit), inside an MA-L block the registry split up.
		{"00:1b:c5:00:00:42", "Converging Systems"},
		{"00:1b:c5:00:1f:00", "OpenRB.com, Direct SIA"},
		// EUI-64 addresses use the same prefixes.
		{"00:11:32:ff:fe:9b:c4:01", "Synology"},
		// A randomized (locally administered) address has no vendor, even
		// when its bytes happen to match an assignment.
		{"02:11:32:9b:c4:01", ""},
		{"da:a1:19:5e:00:01", ""},
	} {
		if got := Lookup(mac(t, tc.mac)); got != tc.want {
			t.Errorf("Lookup(%s) = %q, want %q", tc.mac, got, tc.want)
		}
	}

	if got := Lookup(net.HardwareAddr{0x00, 0x11}); got != "" {
		t.Errorf("short address: %q, want none", got)
	}
}

func TestIsRandomized(t *testing.T) {
	for _, tc := range []struct {
		mac  string
		want bool
	}{
		{"00:11:32:9b:c4:01", false},
		{"02:00:00:00:00:01", true},
		{"da:a1:19:5e:00:01", true}, // second hex digit 2, 6, a or e
		{"f6:12:34:56:78:9a", true},
		{"f4:12:34:56:78:9a", false},
	} {
		if got := IsRandomized(mac(t, tc.mac)); got != tc.want {
			t.Errorf("IsRandomized(%s) = %v, want %v", tc.mac, got, tc.want)
		}
	}
}

func TestDataIsComplete(t *testing.T) {
	counts := map[int]int{}
	for digits, entries := range load().byDigits {
		counts[digits] = len(entries)
	}
	// Today's listings hold about 40,000 MA-L, 6,600 MA-M and 7,200 MA-S
	// assignments; a much smaller table means a broken download.
	if counts[6] < 30000 || counts[7] < 5000 || counts[9] < 5000 {
		t.Errorf("assignments by prefix length = %v", counts)
	}
}

func BenchmarkLookup(b *testing.B) {
	m, _ := net.ParseMAC("00:1b:c5:00:1f:00")
	Lookup(m) // load the table outside the timing
	b.ResetTimer()
	for b.Loop() {
		Lookup(m)
	}
}
