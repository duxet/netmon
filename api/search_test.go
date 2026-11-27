package api

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/duxet/netmon/domains"
	"github.com/duxet/netmon/internal/geoiptest"
	"github.com/duxet/netmon/lookup"
)

func TestFlowsSearch(t *testing.T) {
	countries := lookup.OpenCountries(geoiptest.Countries(t,
		geoiptest.Country{Network: "140.82.112.0/20", Code: "US", Name: "United States"},
		geoiptest.Country{Network: "1.1.1.0/24", Code: "AU", Name: "Australia"},
	))
	t.Cleanup(func() { countries.Close() })
	asns := lookup.OpenASNs(geoiptest.ASNs(t,
		geoiptest.ASN{Network: "140.82.112.0/20", Number: 36459, Organization: "GITHUB"},
		geoiptest.ASN{Network: "1.1.1.0/24", Number: 13335, Organization: "CLOUDFLARENET"},
	))
	t.Cleanup(func() { asns.Close() })
	names := map[string]string{"140.82.121.4": "lb-140-82-121-4-fra.github.com.", "1.1.1.1": "one.one.one.one."}
	hostnames := lookup.NewHostnamesFunc(func(_ context.Context, addr string) ([]string, error) {
		if name, ok := names[addr]; ok {
			return []string{name}, nil
		}
		return nil, errors.New("no name")
	})

	app, db := newTestAppFor(t, func(*sql.DB) Options {
		return Options{Countries: countries, ASNs: asns, Hostnames: hostnames}
	})
	seedNetwork(t, db)

	for query, want := range map[string][]string{
		"github":     {"140.82.121.4:443"},
		"fra.github": {"140.82.121.4:443"},
		"us":         {"140.82.121.4:443"},
		"US":         {"140.82.121.4:443"},
		"australia":  {"1.1.1.1:53"},
		"as13335":    {"1.1.1.1:53"},
		"cloudflare": {"1.1.1.1:53"},
		"telegram":   {"149.154.167.51:443"},
		"one.one":    {"1.1.1.1:53"},
		"udp":        {"1.1.1.1:53"},
		"445":        {"192.168.1.10:445"},
		"149.154":    {"149.154.167.51:443"},
		"de":         nil,
	} {
		var flows []flowJSON
		status, total := get(t, app, "/api/flows?mac="+testLaptopMAC+"&q="+query, &flows)
		got := peers(flows)
		slices.Sort(got)
		if status != 200 || !equal(got, want) || total != itoa(uint16(len(want))) {
			t.Errorf("q=%s: %d flows %v (total %s), want %v", query, status, got, total, want)
		}
	}
}

func TestLookedUpNames(t *testing.T) {
	names := domains.New()
	names.Observe(netip.MustParseAddr("140.82.121.4"), "github.com", time.Now())
	app, db := newTestAppFor(t, func(*sql.DB) Options { return Options{Domains: names} })
	seedNetwork(t, db)

	status, body := do(t, app, "POST", "/api/hostnames", `{"ip_addresses": ["140.82.121.4", "1.1.1.1"]}`)
	if status != 200 || !strings.Contains(body, `"Domain":"github.com"`) || !strings.Contains(body, `"Domain":null`) {
		t.Errorf("POST /api/hostnames = %d %s, want github.com's domain", status, body)
	}

	if status, body := do(t, app, "GET", "/api/flows?mac="+testLaptopMAC+"&q=github", ""); status != 200 || !strings.Contains(body, `"Domain":"github.com"`) {
		t.Errorf("GET /api/flows?q=github = %d %s, want the flow found by its domain", status, body)
	}

	var summary FlowSummaryDTO
	get(t, app, "/api/flows/summary?mac="+testLaptopMAC, &summary)
	for _, peer := range summary.TopPeers.Items {
		if peer.Key == "140.82.121.4" && peer.Label != "github.com" {
			t.Errorf("top peer %+v, want it labelled github.com", peer)
		}
	}
}
