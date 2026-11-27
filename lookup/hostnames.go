package lookup

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	hostnameTTL        = time.Hour
	notFoundTTL        = 10 * time.Minute
	failureTTL         = time.Minute
	lookupTimeout      = 2 * time.Second
	lookupConcurrency  = 16
	maxCachedHostnames = 10_000
)

type lookupFunc func(ctx context.Context, addr string) ([]string, error)

// dnsLookup returns a reverse lookup that queries the DNS server at address
// ("host" or "host:port"), or the system resolver for "system" or "".
func dnsLookup(address string) lookupFunc {
	if address == "" || address == "system" {
		return net.DefaultResolver.LookupAddr
	}

	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(address, "53")
	}

	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		},
	}

	return resolver.LookupAddr
}

type hostnameEntry struct {
	hostname *string
	expires  time.Time
}

// Hostnames does reverse DNS lookups, using one server for local
// addresses (which knows DHCP names) and another for public ones. Results,
// including failures, are cached so a slow or unreachable server costs at
// most one timeout per address per cache period.
type Hostnames struct {
	local       lookupFunc
	public      lookupFunc
	timeout     time.Duration
	concurrency int
	now         func() time.Time

	mu    sync.Mutex
	cache map[netip.Addr]hostnameEntry
	// servers counts the changes of servers, so that an answer from a server
	// that was replaced while it was asked isn't cached.
	servers uint64
}

// NewHostnames returns a resolver that asks localDNS about local addresses
// and publicDNS about public ones. Each is "host", "host:port", or "system"
// (or "") for the operating system's resolver.
func NewHostnames(localDNS, publicDNS string) *Hostnames {
	return newHostnames(dnsLookup(localDNS), dnsLookup(publicDNS))
}

// NewHostnamesFunc returns a resolver that looks up local and public
// addresses alike with lookup, which works as net.Resolver's LookupAddr does.
// It lets tests answer without asking a DNS server.
func NewHostnamesFunc(lookup func(ctx context.Context, addr string) ([]string, error)) *Hostnames {
	return newHostnames(lookup, lookup)
}

func newHostnames(local, public lookupFunc) *Hostnames {
	return &Hostnames{
		local:       local,
		public:      public,
		timeout:     lookupTimeout,
		concurrency: lookupConcurrency,
		now:         time.Now,
		cache:       map[netip.Addr]hostnameEntry{},
	}
}

// SetServers changes the DNS servers lookups go to, as NewHostnames takes
// them, and forgets the names the old ones gave.
func (r *Hostnames) SetServers(localDNS, publicDNS string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.local, r.public = dnsLookup(localDNS), dnsLookup(publicDNS)
	r.cache = map[netip.Addr]hostnameEntry{}
	r.servers++
}

func isLocalAddress(addr netip.Addr) bool {
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast()
}

// Lookup returns the first PTR name for addr, or nil if it has none or the
// lookup failed.
func (r *Hostnames) Lookup(ctx context.Context, addr netip.Addr) *string {
	addr = addr.Unmap()

	r.mu.Lock()
	entry, ok := r.cache[addr]
	lookup := r.public
	if isLocalAddress(addr) {
		lookup = r.local
	}
	servers := r.servers
	r.mu.Unlock()
	if ok && r.now().Before(entry.expires) {
		return entry.hostname
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	names, err := lookup(ctx, addr.String())

	var hostname *string
	ttl := hostnameTTL
	var dnsErr *net.DNSError
	switch {
	case err == nil && len(names) > 0:
		hostname = &names[0]
	case err == nil, errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		ttl = notFoundTTL
	default:
		slog.Debug("Reverse DNS lookup failed", "addr", addr, "err", err)
		ttl = failureTTL
	}

	r.store(addr, hostnameEntry{hostname: hostname, expires: r.now().Add(ttl)}, servers)
	return hostname
}

// Cached returns the name of addr if it was looked up a while ago and is
// still cached, without asking for it: ok is false when it would have to be.
func (r *Hostnames) Cached(addr netip.Addr) (name *string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[addr.Unmap()]
	if !ok || !r.now().Before(entry.expires) {
		return nil, false
	}
	return entry.hostname, true
}

// store caches an answer, unless the servers changed since it was asked for.
func (r *Hostnames) store(addr netip.Addr, entry hostnameEntry, servers uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if servers != r.servers {
		return
	}

	if len(r.cache) >= maxCachedHostnames {
		now := r.now()
		for key, cached := range r.cache {
			if !now.Before(cached.expires) {
				delete(r.cache, key)
			}
		}
		if len(r.cache) >= maxCachedHostnames {
			r.cache = map[netip.Addr]hostnameEntry{}
		}
	}

	r.cache[addr] = entry
}

// LookupAll resolves the given addresses in parallel, with bounded
// concurrency, and returns the hostname for each unique address.
func (r *Hostnames) LookupAll(ctx context.Context, addrs []netip.Addr) map[netip.Addr]*string {
	results := make(map[netip.Addr]*string, len(addrs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(1, r.concurrency))

	for _, addr := range addrs {
		addr = addr.Unmap()
		mu.Lock()
		_, seen := results[addr]
		if !seen {
			results[addr] = nil
		}
		mu.Unlock()
		if seen {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			hostname := r.Lookup(ctx, addr)
			mu.Lock()
			results[addr] = hostname
			mu.Unlock()
		}()
	}

	wg.Wait()
	return results
}
