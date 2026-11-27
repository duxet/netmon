// Package unifi reads the clients a UniFi Network Application knows about, so
// that devices can be shown with the names, connection type and uplink UniFi
// has for them.
//
// There are two ways in. With an API key, clients are read through the
// official Integration API, which consoles running UniFi OS and recent
// Network Applications have. With the username and password of a local
// account, netmon signs in as the web UI does and reads the API the web UI
// uses, which every Network Application has, the self-hosted ones included.
//
// The controller is polled in the background, and what it last said is kept
// in memory by MAC address. A controller that can't be reached only means
// there is nothing new: devices keep what was learnt about them before.
package unifi

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultInterval is how often the controller is asked for its clients.
	DefaultInterval = time.Minute
	requestTimeout  = 15 * time.Second
	// maxResponse bounds what is read of one response.
	maxResponse = 32 << 20
)

// Connection types, as the Integration API names them.
const (
	Wired    = "WIRED"
	Wireless = "WIRELESS"
	VPN      = "VPN"
)

// Client is what UniFi knows about one client.
type Client struct {
	// Name is the alias given in UniFi, or else the hostname the client
	// gave over DHCP. It may be empty.
	Name string
	// Type is Wired, Wireless, VPN or another type a newer controller has.
	Type string
	// IPAddress is the address UniFi saw it use; it may be empty.
	IPAddress string
	// Uplink is the name of the access point or switch it is connected to,
	// with the switch port when known; empty when unknown.
	Uplink string
	// ConnectedAt is when it connected, zero when unknown.
	ConnectedAt time.Time
	// Connected tells whether it was connected at the last sync. Clients that
	// left are kept, with what was last known about them.
	Connected bool
	// LastSeen is when UniFi last saw it connected, zero when unknown.
	LastSeen time.Time
}

// Status is how syncing with the controller goes.
type Status struct {
	// Site is the site's name, once it was found.
	Site string
	// LastSync is when the clients were last read, zero before the first
	// time; LastError is why the last attempt failed, empty if it didn't.
	LastSync  time.Time
	LastError string
	// Connected is the number of clients connected at the last sync.
	Connected int
}

// Options configures a [Source]. It needs either APIKey, or Username and
// Password.
type Options struct {
	// URL is the console's or Network Application's address, such as
	// https://192.168.1.1 or https://unifi.lan:8443.
	URL string
	// APIKey is a key made in UniFi Network under Settings, Control Plane,
	// Integrations, for the Integration API.
	APIKey string
	// Username and Password are those of a local UniFi account, for the API
	// the web UI uses. The account mustn't need two-factor authentication;
	// View Only access is enough.
	Username string
	Password string
	// Site is the site's name or internal reference ("default"), or its ID.
	// Empty means "default".
	Site string
	// Fingerprint is the SHA-256 fingerprint of the controller's certificate,
	// in hex, with or without colons. With it, that certificate is trusted
	// even when it is self-signed, as UniFi's are; without it, the
	// certificate has to be valid for the system.
	Fingerprint string
	// Interval is how often to sync; DefaultInterval when zero.
	Interval time.Duration
}

// snapshot is what one sync found: the site's name, and clients by MAC
// address (as net.HardwareAddr.String). Clients that aren't connected may be
// among them, from a list of every client the controller knows.
type snapshot struct {
	site    string
	clients map[string]Client
}

// reader reads the clients from a controller in one way.
type reader interface {
	read(ctx context.Context) (snapshot, error)
}

// Source keeps the clients the controller reported. A nil *Source knows of
// none, so callers don't need to check whether UniFi is configured.
type Source struct {
	interval time.Duration
	reader   reader
	now      func() time.Time

	mu      sync.RWMutex
	clients map[string]Client
	status  Status
}

// New returns a source for the controller in opts. It doesn't contact the
// controller until [Source.Run] or [Source.Sync].
func New(opts Options) (*Source, error) {
	base, err := ParseURL(opts.URL)
	if err != nil {
		return nil, err
	}
	hasKey, hasLogin := opts.APIKey != "", opts.Username != "" || opts.Password != ""
	switch {
	case hasKey && hasLogin:
		return nil, errors.New("use either an API key, or a username and password, not both")
	case hasLogin && (opts.Username == "" || opts.Password == ""):
		return nil, errors.New("both a username and a password are needed")
	case !hasKey && !hasLogin:
		return nil, errors.New("an API key, or a username and password, are needed")
	}
	if opts.Site == "" {
		opts.Site = "default"
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if opts.Fingerprint != "" {
		want, err := ParseFingerprint(opts.Fingerprint)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = pinned(want)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		// Credentials are only sent where they were configured to go.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	s := &Source{interval: opts.Interval, now: time.Now, clients: map[string]Client{}}
	if hasKey {
		s.reader = &integration{http: client, base: base, key: opts.APIKey, site: opts.Site, now: s.clock}
	} else {
		s.reader = &legacy{
			http: client, base: base, username: opts.Username, password: opts.Password, site: opts.Site, now: s.clock,
		}
	}
	return s, nil
}

func (s *Source) clock() time.Time { return s.now() }

// ParseURL checks the controller's address: an https:// URL (http:// only
// for this machine, as credentials are sent with requests).
func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname()))) {
		return nil, fmt.Errorf("the UniFi URL must be an https:// URL, such as https://192.168.1.1: %q", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("the UniFi URL must be an address without a query, such as https://192.168.1.1: %q", raw)
	}
	return u, nil
}

// ParseFingerprint reads a SHA-256 fingerprint in hex, with or without
// colons or spaces, as browsers and openssl show it.
func ParseFingerprint(raw string) ([]byte, error) {
	clean := strings.NewReplacer(":", "", " ", "", "-", "").Replace(strings.TrimSpace(raw))
	clean = strings.TrimPrefix(strings.ToLower(clean), "sha256=")
	b, err := hex.DecodeString(clean)
	if err != nil || len(b) != sha256.Size {
		return nil, fmt.Errorf("the certificate fingerprint must be a SHA-256 fingerprint, 64 hex digits such as AB:CD:…: %q", raw)
	}
	return b, nil
}

// pinned trusts the certificate with the given SHA-256 fingerprint, and no
// other, whoever signed it.
func pinned(want []byte) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// The chain is checked below instead, against the fingerprint.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("the UniFi controller sent no certificate")
			}
			got := sha256.Sum256(rawCerts[0])
			if !equal(got[:], want) {
				return fmt.Errorf("the UniFi controller's certificate has the fingerprint %s, not the one configured",
					FormatFingerprint(got[:]))
			}
			return nil
		},
	}
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// FormatFingerprint writes a fingerprint as AB:CD:….
func FormatFingerprint(b []byte) string {
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = strings.ToUpper(hex.EncodeToString([]byte{c}))
	}
	return strings.Join(parts, ":")
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Run syncs now and then every interval, until ctx is done.
func (s *Source) Run(ctx context.Context) {
	if s == nil {
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	failing := ""
	for {
		err := s.Sync(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil && err.Error() != failing:
			// Say it once, and again only when it changes.
			log.Printf("Can't read clients from UniFi, will keep trying: %v", err)
			failing = err.Error()
		case err == nil && failing != "":
			log.Println("Reading clients from UniFi works again")
			failing = ""
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sync reads the clients from the controller once. It must not be called
// concurrently with itself or Run.
func (s *Source) Sync(ctx context.Context) error {
	snap, err := s.reader.read(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	if snap.site != "" {
		s.status.Site = snap.site
	}
	if err != nil {
		s.status.LastError = err.Error()
		return err
	}
	s.status.LastError = ""
	s.merge(snap)
	return nil
}

// merge takes in what a sync found. Every client it doesn't have as connected
// isn't any more; one it lists as not connected only replaces what is known
// about it when it was seen later.
func (s *Source) merge(snap snapshot) {
	for mac, c := range s.clients {
		c.Connected = false
		s.clients[mac] = c
	}
	connected := 0
	for mac, c := range snap.clients {
		if c.Connected {
			connected++
		} else if old, ok := s.clients[mac]; ok && !c.LastSeen.After(old.LastSeen) {
			if c.Name != "" {
				old.Name = c.Name
			}
			c = old
		}
		s.clients[mac] = c
	}
	s.status.LastSync = s.now()
	s.status.Connected = connected
}

// Client returns what UniFi knows about the device with the MAC address mac
// (as net.HardwareAddr.String).
func (s *Source) Client(mac string) (Client, bool) {
	if s == nil {
		return Client{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.clients[mac]
	return c, ok
}

// Status returns how syncing goes; ok is false on a nil source, when UniFi
// isn't configured.
func (s *Source) Status() (Status, bool) {
	if s == nil {
		return Status{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status, true
}

// statusError is an answer with an unexpected HTTP status.
type statusError struct {
	code int
	path string
	body string
	hint string
}

func (e *statusError) Error() string {
	msg := fmt.Sprintf("UniFi answered %s with %d %s", e.path, e.code, http.StatusText(e.code))
	if e.hint != "" {
		msg += "; " + e.hint
	}
	if e.body != "" {
		msg += ": " + e.body
	}
	return msg
}

// snippet is the start of an error response, on one line.
func snippet(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	return text
}

// macKey returns the MAC address in the form clients are kept by, or false
// for one that isn't a MAC address.
func macKey(raw string) (string, bool) {
	hw, err := net.ParseMAC(raw)
	if err != nil || len(hw) != 6 {
		return "", false
	}
	return hw.String(), true
}
