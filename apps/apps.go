// Package apps classifies remote IP addresses into well-known applications
// (YouTube, Telegram, X, Netflix, ...) based on the networks those services
// operate.
//
// Flows only carry IP addresses, so classification works on two signals:
//   - a built-in list of IP prefixes announced by each service, and
//   - the autonomous system number (ASN) of the address, looked up in the
//     optional GeoLite2-ASN.mmdb database (if present in the working directory).
//
// The ASN database is more complete and stays up to date, so using it is
// recommended. Traffic served from shared CDNs (Cloudflare, Akamai, AWS, ...)
// cannot be attributed to a single app this way and is reported as "Other".
package apps

import (
	"log/slog"
	"net/netip"
	"sync"

	"github.com/duxet/netmon/lookup"
)

// Other is the name used for traffic that doesn't match any known app.
const Other = "Other"

type App struct {
	Name     string
	ASNs     []uint
	Prefixes []string
}

// Known is the list of recognised applications. Several services share
// infrastructure with their parent company and can't be told apart by IP
// alone (e.g. YouTube and other Google services, or Facebook, Instagram and
// WhatsApp), so they're grouped together.
var Known = []App{
	{
		Name: "YouTube / Google",
		ASNs: []uint{15169, 36040, 43515},
		Prefixes: []string{
			"8.8.4.0/24", "8.8.8.0/24", "64.233.160.0/19", "66.102.0.0/20", "66.249.64.0/19",
			"72.14.192.0/18", "74.125.0.0/16", "108.177.0.0/17", "142.250.0.0/15", "172.217.0.0/16",
			"172.253.0.0/16", "173.194.0.0/16", "208.117.224.0/19", "209.85.128.0/17",
			"216.58.192.0/19", "216.239.32.0/19",
			"2001:4860::/32", "2404:6800::/32", "2607:f8b0::/32", "2800:3f0::/32", "2a00:1450::/32",
			"2c0f:fb50::/32",
		},
	},
	{
		Name: "Telegram",
		ASNs: []uint{44907, 59930, 62014, 62041, 211157},
		Prefixes: []string{
			// https://core.telegram.org/resources/cidr.txt
			"91.105.192.0/23", "91.108.4.0/22", "91.108.8.0/22", "91.108.12.0/22", "91.108.16.0/22",
			"91.108.20.0/22", "91.108.56.0/22", "149.154.160.0/20", "185.76.151.0/24",
			"2001:67c:4e8::/48", "2001:b28:f23c::/48", "2001:b28:f23d::/48", "2001:b28:f23f::/48",
			"2a0a:f280::/32",
		},
	},
	{
		Name: "X (Twitter)",
		ASNs: []uint{13414, 35995},
		Prefixes: []string{
			"69.195.160.0/19", "104.244.40.0/21", "192.133.76.0/22", "199.16.156.0/22",
			"199.59.148.0/22",
		},
	},
	{
		Name: "Meta (Facebook, Instagram, WhatsApp)",
		ASNs: []uint{32934, 63293},
		Prefixes: []string{
			"31.13.24.0/21", "31.13.64.0/18", "66.220.144.0/20", "69.63.176.0/20", "69.171.224.0/19",
			"102.132.96.0/20", "129.134.0.0/16", "157.240.0.0/16", "163.70.128.0/17",
			"173.252.64.0/18", "179.60.192.0/22", "185.60.216.0/22", "204.15.20.0/22",
			"2a03:2880::/32",
		},
	},
	{
		Name: "Netflix",
		ASNs: []uint{2906, 40027, 55095},
		Prefixes: []string{
			"23.246.0.0/18", "37.77.184.0/21", "45.57.0.0/17", "64.120.128.0/17", "66.197.128.0/17",
			"108.175.32.0/20", "192.173.64.0/18", "198.38.96.0/19", "198.45.48.0/20",
			"208.75.76.0/22",
			"2a00:86c0::/32", "2620:10c:7000::/44",
		},
	},
	{
		Name:     "Apple",
		ASNs:     []uint{714, 6185},
		Prefixes: []string{"17.0.0.0/8"},
	},
	{
		Name: "TikTok",
		ASNs: []uint{138699, 396986},
	},
	{
		Name:     "Spotify",
		ASNs:     []uint{8403},
		Prefixes: []string{"78.31.8.0/21"},
	},
	{
		Name:     "Twitch",
		ASNs:     []uint{46489},
		Prefixes: []string{"185.42.204.0/22"},
	},
	{
		Name: "Steam",
		ASNs: []uint{32590},
		Prefixes: []string{
			"155.133.224.0/19", "162.254.192.0/21", "185.25.180.0/22", "208.64.200.0/22",
			"208.78.164.0/22",
		},
	},
}

type prefixEntry struct {
	prefix netip.Prefix
	app    string
}

type Classifier struct {
	prefixes []prefixEntry
	asns     map[uint]string
	asnDB    *lookup.ASNs

	mu    sync.RWMutex
	cache map[netip.Addr]string
}

// NewClassifier builds a classifier from the given app definitions. asnDBPath
// may point to a GeoLite2-ASN database; if it can't be opened, only the
// built-in prefixes are used.
func NewClassifier(apps []App, asnDBPath string) *Classifier {
	return NewClassifierWithASNs(apps, lookup.OpenASNs(asnDBPath))
}

// NewClassifierWithASNs is NewClassifier with an ASN database that is already
// open, so that it can be shared. The caller keeps ownership of it. Without a
// database (nil, or one that isn't loaded), only the built-in prefixes are used.
func NewClassifierWithASNs(apps []App, asnDB *lookup.ASNs) *Classifier {
	c := &Classifier{
		asns:  map[uint]string{},
		asnDB: asnDB,
		cache: map[netip.Addr]string{},
	}

	for _, app := range apps {
		for _, asn := range app.ASNs {
			c.asns[asn] = app.Name
		}
		for _, p := range app.Prefixes {
			prefix, err := netip.ParsePrefix(p)
			if err != nil {
				slog.Error("Invalid prefix for app", "prefix", p, "app", app.Name, "err", err)
				continue
			}
			c.prefixes = append(c.prefixes, prefixEntry{prefix.Masked(), app.Name})
		}
	}

	return c
}

var (
	defaultClassifier     *Classifier
	defaultClassifierOnce sync.Once
)

// Default returns a shared classifier using the Known apps and
// GeoLite2-ASN.mmdb from the working directory.
func Default() *Classifier {
	defaultClassifierOnce.Do(func() {
		defaultClassifier = NewClassifier(Known, "GeoLite2-ASN.mmdb")
	})
	return defaultClassifier
}

// Classify returns the app name for the given address, or Other.
func (c *Classifier) Classify(addr netip.Addr) string {
	addr = addr.Unmap()

	c.mu.RLock()
	app, ok := c.cache[addr]
	c.mu.RUnlock()
	if ok {
		return app
	}

	app = c.classify(addr)

	c.mu.Lock()
	c.cache[addr] = app
	c.mu.Unlock()

	return app
}

func (c *Classifier) classify(addr netip.Addr) string {
	if !IsPublic(addr) {
		return Other
	}

	for _, entry := range c.prefixes {
		if entry.prefix.Contains(addr) {
			return entry.app
		}
	}

	if number, _, ok := c.asnDB.Lookup(addr); ok {
		if app, ok := c.asns[number]; ok {
			return app
		}
	}

	return Other
}

// IsPublic reports whether addr is a globally routable address, i.e. one
// that can belong to a remote service rather than to the local network.
func IsPublic(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsValid() &&
		!addr.IsPrivate() &&
		!addr.IsLoopback() &&
		!addr.IsLinkLocalUnicast() &&
		!addr.IsLinkLocalMulticast() &&
		!addr.IsInterfaceLocalMulticast() &&
		!addr.IsMulticast() &&
		!addr.IsUnspecified()
}
