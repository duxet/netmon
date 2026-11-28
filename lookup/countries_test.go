package lookup

import (
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/internal/geoiptest"
)

func TestCountriesWithoutDatabase(t *testing.T) {
	addr, err := common.ParseIPAddress("140.82.121.4")
	if err != nil {
		t.Fatal(err)
	}

	for name, countries := range map[string]*Countries{
		"missing file": OpenCountries("does-not-exist.mmdb"),
		"no path":      OpenCountries(""),
		"nil":          nil,
	} {
		if country := countries.Country(*addr); country != nil {
			t.Errorf("%s: country = %q, want nil without a database", name, *country)
		}
		if code, _, ok := countries.Lookup(*addr.Addr); ok || countries.Available() {
			t.Errorf("%s: Lookup = %q, %v; want nothing without a database", name, code, ok)
		}
		if err := countries.Close(); err != nil {
			t.Errorf("%s: Close = %v", name, err)
		}
	}
}

func TestCountriesReloadWhileLookingUp(t *testing.T) {
	first := geoiptest.Countries(t, geoiptest.Country{Network: "1.0.0.0/8", Code: "AU", Name: "Australia"})
	second := geoiptest.Countries(t, geoiptest.Country{Network: "1.0.0.0/8", Code: "NZ", Name: "New Zealand"})
	path := filepath.Join(t.TempDir(), "GeoLite2-Country.mmdb")
	// The file is replaced, not written to, as an open database maps it.
	replace := func(data []byte) {
		temp := path + ".new"
		if err := os.WriteFile(temp, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temp, path); err != nil {
			t.Fatal(err)
		}
	}
	copyFile := func(from string) {
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		replace(data)
	}

	// Without the file, Reload opens it once it is there.
	countries := OpenCountries(path)
	defer countries.Close()
	if countries.Available() {
		t.Fatal("available without a file")
	}
	copyFile(first)
	if err := countries.Reload(); err != nil {
		t.Fatal(err)
	}

	addr := netip.MustParseAddr("1.1.1.1")
	var wg sync.WaitGroup
	done := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				if code, _, ok := countries.Lookup(addr); !ok || (code != "AU" && code != "NZ") {
					t.Errorf("Lookup = %q, %v during reloads", code, ok)
					return
				}
			}
		})
	}
	for i := range 20 {
		copyFile([]string{first, second}[i%2])
		if err := countries.Reload(); err != nil {
			t.Error(err)
		}
	}
	close(done)
	wg.Wait()

	if code, _, _ := countries.Lookup(addr); code != "NZ" {
		t.Errorf("Lookup after the reloads = %q, want NZ", code)
	}

	// A broken file leaves the open database as it was.
	replace([]byte("broken"))
	if err := countries.Reload(); err == nil {
		t.Error("Reload of a broken file succeeded")
	}
	if code, _, _ := countries.Lookup(addr); code != "NZ" {
		t.Errorf("Lookup after a failed reload = %q, want NZ", code)
	}
}
