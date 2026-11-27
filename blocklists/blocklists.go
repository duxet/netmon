// Package blocklists keeps public lists of addresses known to be malicious,
// such as botnet command and control servers, up to date, and tells whether
// an address is on one.
package blocklists

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// List is a public blocklist.
type List struct {
	// ID names the list in rules, such as "feodo".
	ID   string
	Name string
	// URL is where it is downloaded from.
	URL string
	// About says what is on it.
	About string
}

// Lists are the lists netmon knows.
var Lists = []List{
	{
		ID: "feodo", Name: "abuse.ch Feodo Tracker", URL: "https://feodotracker.abuse.ch/downloads/ipblocklist.txt",
		About: "Command and control servers of botnets such as Dridex, Emotet, TrickBot and QakBot.",
	},
	{
		ID: "spamhaus_drop", Name: "Spamhaus DROP (IPv4)", URL: "https://www.spamhaus.org/drop/drop_v4.json",
		About: "Networks hijacked or leased by spammers and cyber criminals.",
	},
	{
		ID: "spamhaus_drop_v6", Name: "Spamhaus DROP (IPv6)", URL: "https://www.spamhaus.org/drop/drop_v6.json",
		About: "The same, for IPv6.",
	},
}

// IsList reports whether id names one of Lists.
func IsList(id string) bool {
	return slices.ContainsFunc(Lists, func(l List) bool { return l.ID == id })
}

const (
	// refreshInterval is how often lists are downloaded again; they change
	// about daily.
	refreshInterval = 24 * time.Hour
	// retryInterval is how soon a failed download is tried again.
	retryInterval = time.Hour
	// maxListSize bounds a download.
	maxListSize = 32 << 20
)

// Status is how a list is doing.
type Status struct {
	List
	// Entries is how many addresses and blocks it has.
	Entries int
	// UpdatedAt is when it was last downloaded, zero before.
	UpdatedAt time.Time
	// Error is why the last download failed, empty when it didn't.
	Error string
}

type loaded struct {
	prefixes  []netip.Prefix
	updatedAt time.Time
	err       string
	tried     time.Time
}

// Blocklists keeps the lists in memory, and in files in a directory so that
// they are there after a restart without the internet. It is safe for
// concurrent use.
type Blocklists struct {
	dir    string
	client *http.Client
	urls   map[string]string

	mu    sync.RWMutex
	lists map[string]*loaded
}

// New returns blocklists kept in dir, which is made when needed; empty keeps
// them in memory only. It reads what is in dir already.
func New(dir string) *Blocklists {
	b := &Blocklists{dir: dir, client: &http.Client{Timeout: time.Minute}, urls: map[string]string{}, lists: map[string]*loaded{}}
	for _, l := range Lists {
		b.urls[l.ID] = l.URL
		b.lists[l.ID] = &loaded{}
		if dir == "" {
			continue
		}
		path := b.path(l.ID)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		info, _ := os.Stat(path)
		b.lists[l.ID] = &loaded{prefixes: Parse(data), updatedAt: info.ModTime()}
	}
	return b
}

func (b *Blocklists) path(id string) string {
	return filepath.Join(b.dir, id+".txt")
}

// Match returns the IDs of the lists among ids that have addr on them.
func (b *Blocklists) Match(addr netip.Addr, ids []string) []string {
	if b == nil {
		return nil
	}
	addr = addr.Unmap()
	b.mu.RLock()
	defer b.mu.RUnlock()

	var matched []string
	for _, id := range ids {
		if l := b.lists[id]; l != nil && contains(l.prefixes, addr) {
			matched = append(matched, id)
		}
	}
	return matched
}

// contains reports whether one of the sorted prefixes holds addr.
func contains(prefixes []netip.Prefix, addr netip.Addr) bool {
	// The prefixes are sorted by their first address, so only those starting
	// at or before addr can hold it; blocks on lists rarely nest, so the
	// nearest ones are checked.
	i, _ := slices.BinarySearchFunc(prefixes, addr, func(p netip.Prefix, a netip.Addr) int {
		return p.Addr().Compare(a)
	})
	for j := min(i, len(prefixes)-1); j >= 0 && j >= i-64; j-- {
		if prefixes[j].Contains(addr) {
			return true
		}
	}
	return false
}

// Statuses returns how every list is doing.
func (b *Blocklists) Statuses() []Status {
	b.mu.RLock()
	defer b.mu.RUnlock()
	statuses := make([]Status, 0, len(Lists))
	for _, l := range Lists {
		s := Status{List: l}
		if loaded := b.lists[l.ID]; loaded != nil {
			s.Entries, s.UpdatedAt, s.Error = len(loaded.prefixes), loaded.updatedAt, loaded.err
		}
		statuses = append(statuses, s)
	}
	return statuses
}

// Refresh downloads the lists among ids that are due: never downloaded, last
// downloaded a day ago, or failed an hour ago.
func (b *Blocklists) Refresh(ctx context.Context, ids []string, now time.Time) {
	for _, id := range ids {
		b.mu.RLock()
		l := b.lists[id]
		due := l != nil && (now.Sub(l.updatedAt) >= refreshInterval && now.Sub(l.tried) >= retryInterval)
		b.mu.RUnlock()
		if !due {
			continue
		}
		b.download(ctx, id, now)
	}
}

func (b *Blocklists) download(ctx context.Context, id string, now time.Time) {
	prefixes, data, err := b.fetch(ctx, b.urls[id])

	b.mu.Lock()
	l := b.lists[id]
	l.tried = now
	if err != nil {
		l.err = err.Error()
		b.mu.Unlock()
		slog.Warn("Failed to download a blocklist", "list", id, "err", err)
		return
	}
	l.prefixes, l.updatedAt, l.err = prefixes, now, ""
	b.mu.Unlock()
	slog.Info("Downloaded a blocklist", "list", id, "entries", len(prefixes))

	if b.dir != "" {
		if err := os.MkdirAll(b.dir, 0o755); err == nil {
			_ = os.WriteFile(b.path(id), data, 0o644)
		}
	}
}

func (b *Blocklists) fetch(ctx context.Context, url string) ([]netip.Prefix, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "netmon")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxListSize))
	if err != nil {
		return nil, nil, err
	}
	prefixes := Parse(data)
	if len(prefixes) == 0 {
		return nil, nil, fmt.Errorf("%s has no addresses", url)
	}
	return prefixes, data, nil
}

// Parse reads a list: one address or block per line, as plain text with
// comments after #, ; or //, or as JSON objects with a cidr or ip field, as
// Spamhaus has them. What isn't an address is left out. The result is sorted.
func Parse(data []byte) []netip.Prefix {
	var prefixes []netip.Prefix
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' || line[0] == ';' || strings.HasPrefix(line, "//") {
			continue
		}

		if line[0] == '{' {
			var entry struct {
				CIDR string `json:"cidr"`
				IP   string `json:"ip"`
			}
			if json.Unmarshal([]byte(line), &entry) != nil {
				continue
			}
			line = entry.CIDR + entry.IP
		}
		if i := strings.IndexAny(line, " \t;,#"); i >= 0 {
			line = line[:i]
		}

		if prefix, err := netip.ParsePrefix(line); err == nil {
			prefixes = append(prefixes, prefix.Masked())
		} else if addr, err := netip.ParseAddr(line); err == nil {
			addr = addr.Unmap()
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	slices.SortFunc(prefixes, func(a, b netip.Prefix) int {
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c
		}
		return a.Bits() - b.Bits()
	})
	return slices.Compact(prefixes)
}
