package apps

import (
	"net/netip"
	"testing"
)

func TestClassify(t *testing.T) {
	c := NewClassifier(Known, "")

	cases := map[string]string{
		"149.154.167.51":        "Telegram",
		"2001:67c:4e8:f004::9":  "Telegram",
		"104.244.42.1":          "X (Twitter)",
		"142.250.186.78":        "YouTube / Google",
		"2a00:1450:4001::1":     "YouTube / Google",
		"157.240.1.35":          "Meta (Facebook, Instagram, WhatsApp)",
		"45.57.10.1":            "Netflix",
		"17.253.144.10":         "Apple",
		"::ffff:149.154.167.51": "Telegram",
		"1.1.1.1":               Other,
		"192.168.1.10":          Other,
		"10.0.0.1":              Other,
		"fe80::1":               Other,
	}

	for ip, want := range cases {
		if got := c.Classify(netip.MustParseAddr(ip)); got != want {
			t.Errorf("Classify(%s) = %q, want %q", ip, got, want)
		}
	}
}

func TestKnownPrefixesAreValid(t *testing.T) {
	for _, app := range Known {
		for _, p := range app.Prefixes {
			if _, err := netip.ParsePrefix(p); err != nil {
				t.Errorf("%s: invalid prefix %q: %v", app.Name, p, err)
			}
		}
	}
}

func TestClassifyByName(t *testing.T) {
	c := NewClassifier(Known, "")
	cdn := netip.MustParseAddr("203.0.113.50")
	if app := c.Classify(cdn); app != Other {
		t.Fatalf("CDN address without names = %s, want Other", app)
	}

	names := map[netip.Addr][]string{cdn: {"ipv4-c001.1.lhr004.ix.nflxvideo.net"}}
	c.SetNames(func(addr netip.Addr) []string { return names[addr] })
	if app := c.Classify(cdn); app != "Netflix" {
		t.Errorf("CDN address looked up as nflxvideo.net = %s, want Netflix", app)
	}

	// The prefixes win over names, and a name that merely ends alike isn't one.
	names[netip.MustParseAddr("149.154.167.51")] = []string{"www.netflix.com"}
	names[netip.MustParseAddr("203.0.113.51")] = []string{"notnetflix.com"}
	if app := c.Classify(netip.MustParseAddr("149.154.167.51")); app != "Telegram" {
		t.Errorf("Telegram address = %s, want Telegram", app)
	}
	if app := c.Classify(netip.MustParseAddr("203.0.113.51")); app != Other {
		t.Errorf("notnetflix.com = %s, want Other", app)
	}
}
