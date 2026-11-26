// Package unifi reads the clients a UniFi Network Application knows about,
// through its official Integration API, so that devices can be shown with
// the names, connection type and uplink UniFi has for them.
//
// The API is polled in the background, and what it last said is kept in
// memory by MAC address. A controller that can't be reached only means there
// is nothing new: devices keep what was learnt about them before.
package unifi

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	pageLimit       = 200
	// maxPages bounds a sync, in case the controller pages forever.
	maxPages = 100
	// maxResponse bounds what is read of one response.
	maxResponse = 16 << 20
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
	// empty when unknown.
	Uplink string
	// ConnectedAt is when it connected, zero when unknown.
	ConnectedAt time.Time
	// Connected tells whether it was connected at the last sync. Clients that
	// left are kept, with what was last known about them.
	Connected bool
	// LastSeen is the last sync that found it connected.
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

// Options configures a [Source].
type Options struct {
	// URL is the console's or Network Application's address, such as
	// https://192.168.1.1 or https://unifi.lan:8443.
	URL string
	// APIKey is a key made in UniFi Network under Settings, Control Plane,
	// Integrations.
	APIKey string
	// Site is the site's name or internal reference ("default"), or its ID.
	// Empty means "default".
	Site string
	// Fingerprint is the SHA-256 fingerprint of the controller's certificate,
	// in hex, with or without colons. With it, that certificate is trusted
	// even when it is self-signed, as UniFi consoles' are; without it, the
	// certificate has to be valid for the system.
	Fingerprint string
	// Interval is how often to sync; DefaultInterval when zero.
	Interval time.Duration
}

// Source keeps the clients the controller reported. A nil *Source knows of
// none, so callers don't need to check whether UniFi is configured.
type Source struct {
	opts   Options
	http   *http.Client
	base   *url.URL
	prefix string // the path the Integration API is under, once found
	siteID string

	mu      sync.RWMutex
	clients map[string]Client // by MAC address, as net.HardwareAddr.String
	status  Status
}

// New returns a source for the controller in opts. It doesn't contact the
// controller until [Source.Run].
func New(opts Options) (*Source, error) {
	base, err := ParseURL(opts.URL)
	if err != nil {
		return nil, err
	}
	if opts.APIKey == "" {
		return nil, errors.New("an API key is required")
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

	return &Source{
		opts:    opts,
		http:    &http.Client{Transport: transport, Timeout: requestTimeout},
		base:    base,
		clients: map[string]Client{},
	}, nil
}

// ParseURL checks the controller's address: an https:// URL (http:// only
// for this machine, as the API key is sent with every request).
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
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()

	failing := false
	for {
		err := s.Sync(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil && !failing:
			log.Printf("Can't read clients from UniFi, will keep trying: %v", err)
			failing = true
		case err == nil && failing:
			log.Println("Reading clients from UniFi works again")
			failing = false
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sync reads the clients from the controller once.
func (s *Source) Sync(ctx context.Context) error {
	err := s.sync(ctx)
	s.mu.Lock()
	if err != nil {
		s.status.LastError = err.Error()
	} else {
		s.status.LastError = ""
	}
	s.mu.Unlock()
	return err
}

func (s *Source) sync(ctx context.Context) error {
	if s.siteID == "" {
		if err := s.findSite(ctx); err != nil {
			return err
		}
	}

	// Names of the access points and switches, for the clients' uplinks. They
	// are only a nicety, so failing to read them isn't a failed sync.
	devices, err := s.devices(ctx)
	if err != nil {
		log.Printf("Can't read UniFi devices, clients will have no uplink names: %v", err)
	}

	var clients []apiClient
	err = s.pages(ctx, "/sites/"+url.PathEscape(s.siteID)+"/clients", func(raw json.RawMessage) error {
		var page []apiClient
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		clients = append(clients, page...)
		return nil
	})
	if err != nil {
		return err
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for mac, c := range s.clients {
		c.Connected = false
		s.clients[mac] = c
	}
	connected := 0
	for _, c := range clients {
		hw, err := net.ParseMAC(c.MACAddress)
		if err != nil {
			continue
		}
		client := Client{
			Name:      strings.TrimSpace(c.Name),
			Type:      c.Type,
			IPAddress: c.IPAddress,
			Uplink:    devices[c.UplinkDeviceID],
			Connected: true,
			LastSeen:  now,
		}
		if t, err := time.Parse(time.RFC3339, c.ConnectedAt); err == nil {
			client.ConnectedAt = t
		}
		s.clients[hw.String()] = client
		connected++
	}
	s.status.LastSync = now
	s.status.Connected = connected
	return nil
}

type apiSite struct {
	ID                string `json:"id"`
	InternalReference string `json:"internalReference"`
	Name              string `json:"name"`
}

type apiClient struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	IPAddress      string `json:"ipAddress"`
	MACAddress     string `json:"macAddress"`
	ConnectedAt    string `json:"connectedAt"`
	UplinkDeviceID string `json:"uplinkDeviceId"`
}

type apiDevice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type apiPage struct {
	Offset     int             `json:"offset"`
	Limit      int             `json:"limit"`
	Count      int             `json:"count"`
	TotalCount int             `json:"totalCount"`
	Data       json.RawMessage `json:"data"`
}

// findSite looks the configured site up, and with it where the API is: under
// /proxy/network on a UniFi OS console, at the root of a self-hosted Network
// Application.
func (s *Source) findSite(ctx context.Context) error {
	var sites []apiSite
	var lastErr error
	for _, prefix := range []string{"/proxy/network/integration/v1", "/integration/v1"} {
		s.prefix = prefix
		sites = sites[:0]
		lastErr = s.pages(ctx, "/sites", func(raw json.RawMessage) error {
			var page []apiSite
			if err := json.Unmarshal(raw, &page); err != nil {
				return err
			}
			sites = append(sites, page...)
			return nil
		})
		var status *statusError
		if lastErr == nil || !errors.As(lastErr, &status) || status.code != http.StatusNotFound {
			break
		}
	}
	if lastErr != nil {
		s.prefix = ""
		return lastErr
	}

	var names []string
	for _, site := range sites {
		if site.ID == s.opts.Site || strings.EqualFold(site.InternalReference, s.opts.Site) || strings.EqualFold(site.Name, s.opts.Site) {
			s.siteID = site.ID
			s.mu.Lock()
			s.status.Site = site.Name
			s.mu.Unlock()
			return nil
		}
		names = append(names, fmt.Sprintf("%q", site.InternalReference))
	}
	return fmt.Errorf("UniFi has no site %q; its sites are %s", s.opts.Site, strings.Join(names, ", "))
}

// devices returns the names of the site's UniFi devices by their IDs.
func (s *Source) devices(ctx context.Context) (map[string]string, error) {
	names := map[string]string{}
	err := s.pages(ctx, "/sites/"+url.PathEscape(s.siteID)+"/devices", func(raw json.RawMessage) error {
		var page []apiDevice
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, d := range page {
			names[d.ID] = d.Name
		}
		return nil
	})
	return names, err
}

// pages calls each with the data of every page of a list.
func (s *Source) pages(ctx context.Context, path string, each func(json.RawMessage) error) error {
	offset := 0
	for range maxPages {
		var page apiPage
		if err := s.get(ctx, path, url.Values{"offset": {fmt.Sprint(offset)}, "limit": {fmt.Sprint(pageLimit)}}, &page); err != nil {
			return err
		}
		if err := each(page.Data); err != nil {
			return fmt.Errorf("reading %s from UniFi: %w", path, err)
		}
		offset += page.Count
		if page.Count == 0 || offset >= page.TotalCount {
			return nil
		}
	}
	return fmt.Errorf("reading %s from UniFi: more than %d pages", path, maxPages)
}

type statusError struct {
	code int
	path string
	body string
}

func (e *statusError) Error() string {
	msg := fmt.Sprintf("UniFi answered %s with %d %s", e.path, e.code, http.StatusText(e.code))
	switch e.code {
	case http.StatusUnauthorized, http.StatusForbidden:
		msg += "; check the API key"
	}
	if e.body != "" {
		msg += ": " + e.body
	}
	return msg
}

func (s *Source) get(ctx context.Context, path string, query url.Values, into any) error {
	u := *s.base
	u.Path = strings.TrimSuffix(u.Path, "/") + s.prefix + path
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-KEY", s.opts.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("asking UniFi: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("reading UniFi's answer to %s: %w", s.prefix+path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return &statusError{code: resp.StatusCode, path: s.prefix + path, body: snippet(body)}
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("UniFi's answer to %s isn't what the Integration API sends: %w", s.prefix+path, err)
	}
	return nil
}

// snippet is the start of an error response, on one line.
func snippet(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	return text
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
