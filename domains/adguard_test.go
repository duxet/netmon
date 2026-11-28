package domains

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"testing"
	"time"
)

// adguardServer serves entries as AdGuard Home's query log does: newest
// first, a page at a time, older than older_than.
func adguardServer(t *testing.T, entries func() []map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "netmon" || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/control/querylog/config":
			_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true})
		case "/control/querylog":
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			var page []map[string]any
			for _, e := range entries() {
				if older := r.URL.Query().Get("older_than"); older != "" && e["time"].(string) >= older {
					continue
				}
				page = append(page, e)
				if len(page) == limit {
					break
				}
			}
			oldest := ""
			if len(page) > 0 {
				oldest = page[len(page)-1]["time"].(string)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": page, "oldest": oldest})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func adguardLine(at time.Time, name string, answers ...[2]string) map[string]any {
	var answer []map[string]any
	for _, a := range answers {
		answer = append(answer, map[string]any{"type": a[0], "value": a[1], "ttl": 60})
	}
	return map[string]any{
		"time":     at.Format(time.RFC3339Nano),
		"status":   "NOERROR",
		"reason":   "NotFilteredNotFound",
		"client":   "192.168.1.23",
		"question": map[string]any{"class": "IN", "name": name, "type": "A"},
		"answer":   answer,
	}
}

func TestAdGuardPoll(t *testing.T) {
	blocked := adguardLine(t0.Add(3*time.Second), "ads.example", [2]string{"A", "0.0.0.0"})
	blocked["reason"] = "FilteredBlackList"
	nxdomain := adguardLine(t0.Add(4*time.Second), "gone.example")
	nxdomain["status"] = "NXDOMAIN"
	log := []map[string]any{
		adguardLine(t0.Add(5*time.Second), "one.one.one.one", [2]string{"AAAA", "2606:4700:4700::1111"}),
		nxdomain,
		blocked,
		adguardLine(t0.Add(2*time.Second), "www.netflix.com",
			[2]string{"CNAME", "www.dradis.netflix.com"},
			[2]string{"CNAME", "ipv4-c001.ix.nflxvideo.net"},
			[2]string{"A", "45.57.40.1"},
			[2]string{"A", "45.57.40.2"}),
		adguardLine(t0.Add(time.Second), "www.google.com", [2]string{"A", "142.250.186.78"}),
		adguardLine(t0, "old.example", [2]string{"A", "9.9.9.9"}),
	}
	server := adguardServer(t, func() []map[string]any { return log })
	a := &AdGuard{URL: server.URL + "/", Username: "netmon", Password: "pw", Client: server.Client(), page: 2}

	n := New()
	latest, err := a.poll(context.Background(), n, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !latest.Equal(t0.Add(5 * time.Second)) {
		t.Errorf("latest = %v, want the newest entry's time", latest)
	}
	for addr, want := range map[string]string{
		"45.57.40.1":           "www.netflix.com",
		"45.57.40.2":           "www.netflix.com",
		"142.250.186.78":       "www.google.com",
		"2606:4700:4700::1111": "one.one.one.one",
		"9.9.9.9":              "",
		"0.0.0.0":              "",
	} {
		if got := n.Name(netip.MustParseAddr(addr)); got != want {
			t.Errorf("name of %s = %q, want %q", addr, got, want)
		}
	}
	if names := n.Names(netip.MustParseAddr("45.57.40.1")); !slices.Contains(names, "ipv4-c001.ix.nflxvideo.net") {
		t.Errorf("names = %v, want the record's own name too", names)
	}

	// Only what was logged since is read again.
	n = New()
	if _, err := a.poll(context.Background(), n, latest); err != nil {
		t.Fatal(err)
	}
	if n.Len() != 0 {
		t.Errorf("learned %d addresses from entries read before", n.Len())
	}

	a.Password = "wrong"
	if _, err := a.poll(context.Background(), n, t0); err == nil {
		t.Error("a rejected password is no error")
	}
}
