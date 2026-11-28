package domains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	// adguardInterval is how often AdGuard Home's query log is read.
	adguardInterval = 5 * time.Second
	// adguardPage is how many entries are asked for at once, and
	// adguardMaxPages how many pages one poll reads at most, so that the
	// first one, which catches up on adguardBackfill, stays bounded.
	adguardPage     = 500
	adguardMaxPages = 20
	adguardBackfill = 24 * time.Hour
)

// AdGuard reads the names clients looked up from AdGuard Home's query log,
// over its HTTP API.
type AdGuard struct {
	// URL is where AdGuard Home's web UI is, such as http://127.0.0.1:3000.
	URL string
	// Username and Password sign in, when AdGuard Home has users.
	Username, Password string
	Client             *http.Client
	// page is how many entries are asked for at once, adguardPage when 0.
	page int
}

// adguardEntry is an entry of GET /control/querylog, of the fields netmon
// needs.
type adguardEntry struct {
	Time     time.Time `json:"time"`
	Status   string    `json:"status"`
	Reason   string    `json:"reason"`
	Question struct {
		Name string `json:"name"`
	} `json:"question"`
	Answer []struct {
		Type  string `json:"type"`
		Value any    `json:"value"`
	} `json:"answer"`
}

type adguardLog struct {
	Data   []adguardEntry `json:"data"`
	Oldest string         `json:"oldest"`
}

// observe learns the names of one entry: every address answers for the name
// asked for and, when the answer went through CNAMEs, for the last of them,
// the name of its own record. Blocked queries answer with no real address.
func (e adguardEntry) observe(names *Names) {
	if e.Status != "NOERROR" || strings.HasPrefix(e.Reason, "Filtered") {
		return
	}
	var last string
	for _, answer := range e.Answer {
		value, _ := answer.Value.(string)
		switch answer.Type {
		case "CNAME":
			last = value
		case "A", "AAAA":
			addr, err := netip.ParseAddr(value)
			if err != nil || addr.IsUnspecified() {
				continue
			}
			// The name asked for is observed last, to come first.
			if last != "" {
				names.Observe(addr, last, e.Time)
			}
			names.Observe(addr, e.Question.Name, e.Time)
		}
	}
}

// Follow learns names from the query log until ctx is done, reading what
// was logged since the last poll every few seconds, and at first what was
// logged in the last day.
func (a *AdGuard) Follow(ctx context.Context, names *Names) {
	if a.Client == nil {
		a.Client = &http.Client{Timeout: 30 * time.Second}
	}
	since := time.Now().Add(-adguardBackfill)
	failing := false
	checked := false
	for {
		if !checked {
			checked = a.checkEnabled(ctx)
		}
		latest, err := a.poll(ctx, names, since)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			if !failing {
				slog.Warn("Failed to read AdGuard Home's query log, trying again in a minute", "url", a.URL, "err", err)
			}
			failing = true
		default:
			if failing {
				slog.Info("Reading AdGuard Home's query log again", "url", a.URL)
			}
			failing = false
			since = latest
		}

		wait := adguardInterval
		if failing {
			wait = time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// checkEnabled warns when the query log is off, as there is nothing to read
// then. It reports whether it could tell.
func (a *AdGuard) checkEnabled(ctx context.Context) bool {
	var config struct {
		Enabled bool `json:"enabled"`
	}
	if err := a.get(ctx, "/control/querylog/config", nil, &config); err != nil {
		// AdGuard Home before 0.107.30 has only the older endpoint.
		if err := a.get(ctx, "/control/querylog_info", nil, &config); err != nil {
			return false
		}
	}
	if config.Enabled {
		slog.Info("Reading DNS answers from AdGuard Home", "url", a.URL)
	} else {
		slog.Warn("AdGuard Home's query log is off, so netmon learns no names from it: "+
			"turn it on under Settings → General settings → Logs", "url", a.URL)
	}
	return true
}

// poll reads the entries logged after since, newest first, page by page, and
// returns the time of the newest, or since when there were none.
func (a *AdGuard) poll(ctx context.Context, names *Names, since time.Time) (time.Time, error) {
	page := a.page
	if page == 0 {
		page = adguardPage
	}
	latest := since
	olderThan := ""
	for range adguardMaxPages {
		query := url.Values{"limit": {fmt.Sprint(page)}}
		if olderThan != "" {
			query.Set("older_than", olderThan)
		}
		var log adguardLog
		if err := a.get(ctx, "/control/querylog", query, &log); err != nil {
			return since, err
		}
		for _, entry := range log.Data {
			if !entry.Time.After(since) {
				return latest, nil
			}
			if entry.Time.After(latest) {
				latest = entry.Time
			}
			entry.observe(names)
		}
		if len(log.Data) < page || log.Oldest == "" {
			break
		}
		olderThan = log.Oldest
	}
	return latest, nil
}

func (a *AdGuard) get(ctx context.Context, path string, query url.Values, into any) error {
	u := strings.TrimSuffix(a.URL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if a.Username != "" {
		req.SetBasicAuth(a.Username, a.Password)
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errors.New("AdGuard Home rejected the username or password")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("AdGuard Home answered %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(into); err != nil {
		return fmt.Errorf("read AdGuard Home's answer: %w", err)
	}
	return nil
}
