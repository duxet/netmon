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
