package blocklists

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

const feodo = `# Feodo Tracker Botnet C2 IP Blocklist
# Last updated: 2026-11-27
#
50.16.16.211
162.243.103.246
`

const drop = `{"cidr":"1.10.16.0/20","sblid":"SBL256894","rir":"apnic"}
{"cidr":"2.56.192.0/22","sblid":"SBL459831","rir":"ripencc"}
{"type":"metadata","timestamp":1732700000,"size":2,"records":2}
`

func TestParse(t *testing.T) {
	prefixes := Parse([]byte(feodo + drop + "203.0.113.0/24 ; SBL1\n2001:db8::/32\nnonsense\n::ffff:198.51.100.7\n"))
	var got []string
	for _, p := range prefixes {
		got = append(got, p.String())
	}
	want := []string{"1.10.16.0/20", "2.56.192.0/22", "50.16.16.211/32", "162.243.103.246/32", "198.51.100.7/32", "203.0.113.0/24", "2001:db8::/32"}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parsed %v, want %v", got, want)
		}
	}
}

func TestBlocklists(t *testing.T) {
	served := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		switch r.URL.Path {
		case "/feodo":
			_, _ = w.Write([]byte(feodo))
		case "/drop":
			_, _ = w.Write([]byte(drop))
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	b := New(dir)
	b.urls = map[string]string{"feodo": server.URL + "/feodo", "spamhaus_drop": server.URL + "/drop", "spamhaus_drop_v6": server.URL + "/missing"}
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	all := []string{"feodo", "spamhaus_drop", "spamhaus_drop_v6"}
	b.Refresh(context.Background(), all, now)

	if got := b.Match(netip.MustParseAddr("50.16.16.211"), all); len(got) != 1 || got[0] != "feodo" {
		t.Errorf("match = %v, want feodo", got)
	}
	if got := b.Match(netip.MustParseAddr("1.10.20.5"), all); len(got) != 1 || got[0] != "spamhaus_drop" {
		t.Errorf("match in a block = %v, want spamhaus_drop", got)
	}
	if got := b.Match(netip.MustParseAddr("1.10.20.5"), []string{"feodo"}); len(got) != 0 {
		t.Errorf("match among lists without it = %v, want none", got)
	}
	if got := b.Match(netip.MustParseAddr("8.8.8.8"), all); len(got) != 0 {
		t.Errorf("match = %v, want none", got)
	}

	statuses := b.Statuses()
	if statuses[0].Entries != 2 || !statuses[0].UpdatedAt.Equal(now) || statuses[2].Error == "" {
		t.Errorf("statuses = %+v, want feodo with 2 entries and the IPv6 list failed", statuses)
	}

	// Nothing is due an hour later, but the failed one is tried again.
	served = 0
	b.Refresh(context.Background(), all, now.Add(time.Hour))
	if served != 1 {
		t.Errorf("served %d lists an hour later, want only the failed one again", served)
	}

	// A restart has the lists from the files.
	again := New(dir)
	if got := again.Match(netip.MustParseAddr("162.243.103.246"), all); len(got) != 1 {
		t.Errorf("after a restart, match = %v, want feodo", got)
	}

	var none *Blocklists
	if none.Match(netip.MustParseAddr("50.16.16.211"), all) != nil {
		t.Error("a nil Blocklists matches")
	}
}
