package lookup

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeDNS struct {
	mu       sync.Mutex
	calls    map[string]int
	answer   func(addr string) ([]string, error)
	inFlight atomic.Int32
	maxSeen  atomic.Int32
	delay    time.Duration
}

func (f *fakeDNS) lookup(ctx context.Context, addr string) ([]string, error) {
	f.mu.Lock()
	f.calls[addr]++
	f.mu.Unlock()

	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		seen := f.maxSeen.Load()
		if n <= seen || f.maxSeen.CompareAndSwap(seen, n) {
			break
		}
	}

	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.answer(addr)
}

func (f *fakeDNS) count(addr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[addr]
}

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

func newTestResolver(local, public *fakeDNS, clock *testClock) *Hostnames {
	return &Hostnames{
		local:       local.lookup,
		public:      public.lookup,
		timeout:     time.Second,
		concurrency: 4,
		now:         clock.Now,
		cache:       map[netip.Addr]hostnameEntry{},
	}
}

func answering(names map[string]string) func(string) ([]string, error) {
	return func(addr string) ([]string, error) {
		if name, ok := names[addr]; ok {
			return []string{name}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: addr, IsNotFound: true}
	}
}

func TestLookupRoutesByAddressAndCaches(t *testing.T) {
	local := &fakeDNS{calls: map[string]int{}, answer: answering(map[string]string{"192.168.1.10": "nas.lan."})}
	public := &fakeDNS{calls: map[string]int{}, answer: answering(map[string]string{"1.1.1.1": "one.one.one.one."})}
	clock := &testClock{now: time.Unix(0, 0)}
	r := newTestResolver(local, public, clock)
	ctx := context.Background()

	for range 3 {
		if got := r.Lookup(ctx, netip.MustParseAddr("192.168.1.10")); got == nil || *got != "nas.lan." {
			t.Fatalf("local lookup = %v, want nas.lan.", got)
		}
		if got := r.Lookup(ctx, netip.MustParseAddr("1.1.1.1")); got == nil || *got != "one.one.one.one." {
			t.Fatalf("public lookup = %v, want one.one.one.one.", got)
		}
	}

	if local.count("192.168.1.10") != 1 || public.count("1.1.1.1") != 1 {
		t.Errorf("lookups were not cached: local %d, public %d", local.count("192.168.1.10"), public.count("1.1.1.1"))
	}
	if public.count("192.168.1.10") != 0 || local.count("1.1.1.1") != 0 {
		t.Error("addresses were sent to the wrong resolver")
	}

	clock.now = clock.now.Add(hostnameTTL + time.Second)
	r.Lookup(ctx, netip.MustParseAddr("192.168.1.10"))
	if local.count("192.168.1.10") != 2 {
		t.Error("expired entry was not looked up again")
	}
}

func TestLookupCachesMissesAndFailures(t *testing.T) {
	failing := errors.New("connection refused")
	local := &fakeDNS{calls: map[string]int{}, answer: func(addr string) ([]string, error) {
		if addr == "192.168.1.20" {
			return nil, failing
		}
		return answering(nil)(addr)
	}}
	clock := &testClock{now: time.Unix(0, 0)}
	r := newTestResolver(local, local, clock)
	ctx := context.Background()

	missing := netip.MustParseAddr("192.168.1.99")
	broken := netip.MustParseAddr("192.168.1.20")
	for range 2 {
		if r.Lookup(ctx, missing) != nil || r.Lookup(ctx, broken) != nil {
			t.Fatal("want nil hostnames")
		}
	}
	if local.count(missing.String()) != 1 || local.count(broken.String()) != 1 {
		t.Fatal("misses and failures were not cached")
	}

	// Failures are retried sooner than names that don't exist.
	clock.now = clock.now.Add(failureTTL + time.Second)
	r.Lookup(ctx, missing)
	r.Lookup(ctx, broken)
	if local.count(missing.String()) != 1 || local.count(broken.String()) != 2 {
		t.Errorf("after %v: missing looked up %d times, broken %d; want 1 and 2",
			failureTTL, local.count(missing.String()), local.count(broken.String()))
	}
}

func TestLookupTimesOut(t *testing.T) {
	slow := &fakeDNS{calls: map[string]int{}, delay: time.Minute, answer: answering(nil)}
	r := newTestResolver(slow, slow, &testClock{now: time.Unix(0, 0)})
	r.timeout = 50 * time.Millisecond

	start := time.Now()
	if got := r.Lookup(context.Background(), netip.MustParseAddr("10.0.0.1")); got != nil {
		t.Errorf("got %q, want nil", *got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("lookup took %v, want it bounded by the timeout", elapsed)
	}
}

func TestLookupAllIsParallelBoundedAndDeduplicated(t *testing.T) {
	names := map[string]string{}
	var addrs []netip.Addr
	for i := 1; i <= 20; i++ {
		addr := netip.AddrFrom4([4]byte{10, 0, 0, byte(i)})
		names[addr.String()] = "host.lan."
		addrs = append(addrs, addr, addr) // every address twice
	}
	dns := &fakeDNS{calls: map[string]int{}, delay: 20 * time.Millisecond, answer: answering(names)}
	r := newTestResolver(dns, dns, &testClock{now: time.Unix(0, 0)})

	start := time.Now()
	results := r.LookupAll(context.Background(), addrs)
	elapsed := time.Since(start)

	if len(results) != 20 {
		t.Fatalf("got %d results, want 20", len(results))
	}
	for addr, hostname := range results {
		if hostname == nil {
			t.Errorf("%s: no hostname", addr)
		}
		if dns.count(addr.String()) != 1 {
			t.Errorf("%s looked up %d times, want 1", addr, dns.count(addr.String()))
		}
	}
	if max := dns.maxSeen.Load(); max > int32(r.concurrency) {
		t.Errorf("%d lookups in flight, want at most %d", max, r.concurrency)
	}
	// 20 lookups of 20ms, 4 at a time: about 100ms, far below 400ms sequentially.
	if elapsed > 300*time.Millisecond {
		t.Errorf("LookupAll took %v; lookups don't seem to run in parallel", elapsed)
	}
}

func TestSetServersForgetsNames(t *testing.T) {
	local := &fakeDNS{calls: map[string]int{}, answer: answering(map[string]string{"192.168.1.10": "nas.lan."}), delay: 50 * time.Millisecond}
	public := &fakeDNS{calls: map[string]int{}, answer: answering(nil)}
	r := newTestResolver(local, public, &testClock{now: time.Unix(0, 0)})
	nas := netip.MustParseAddr("192.168.1.10")

	if name := r.Lookup(context.Background(), nas); name == nil || *name != "nas.lan." {
		t.Fatalf("first lookup = %v", name)
	}

	// A lookup the old server answers after the change isn't cached.
	done := make(chan struct{})
	r.mu.Lock()
	r.cache = map[netip.Addr]hostnameEntry{}
	r.mu.Unlock()
	go func() {
		defer close(done)
		r.Lookup(context.Background(), nas)
	}()
	time.Sleep(10 * time.Millisecond)
	r.SetServers("system", "system")
	<-done

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) != 0 {
		t.Errorf("cache = %v after the servers changed, want it empty", r.cache)
	}
}

func TestLookupAsksLocalNamesFirst(t *testing.T) {
	dns := &fakeDNS{calls: map[string]int{}, answer: func(string) ([]string, error) { return []string{"from-dns.lan."}, nil }}
	r := newHostnames(dns.lookup, dns.lookup)
	nas := netip.MustParseAddr("192.168.1.10")
	r.SetLocalNames(func(addr netip.Addr) string {
		if addr == nas {
			return "nas"
		}
		return ""
	})

	if got := r.Lookup(context.Background(), netip.MustParseAddr("::ffff:192.168.1.10")); got == nil || *got != "nas." {
		t.Errorf("Lookup = %v, want the local name with a dot at the end", got)
	}
	if got, ok := r.Cached(nas); !ok || *got != "nas." {
		t.Errorf("Cached = %v, %v, want the local name", got, ok)
	}
	if dns.calls["192.168.1.10"] != 0 {
		t.Error("DNS was asked about an address with a local name")
	}
	if got := r.Lookup(context.Background(), netip.MustParseAddr("192.168.1.11")); got == nil || *got != "from-dns.lan." {
		t.Errorf("Lookup = %v, want the DNS name for an address without a local name", got)
	}
}
