package unifi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	pageLimit = 200
	// maxPages bounds a sync, in case the controller pages forever.
	maxPages = 100
)

// integration reads clients through the official Integration API, with an
// API key.
type integration struct {
	http *http.Client
	base *url.URL
	key  string
	site string
	now  func() time.Time

	prefix   string // the path the API is under, once found
	siteID   string
	siteName string
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
	Count      int             `json:"count"`
	TotalCount int             `json:"totalCount"`
	Data       json.RawMessage `json:"data"`
}

func (r *integration) read(ctx context.Context) (snapshot, error) {
	if r.siteID == "" {
		if err := r.findSite(ctx); err != nil {
			return snapshot{}, err
		}
	}

	// Names of the access points and switches, for the clients' uplinks. They
	// are only a nicety, so failing to read them isn't a failed sync.
	devices, err := r.devices(ctx)
	if err != nil {
		devices = nil
	}

	var clients []apiClient
	err = r.pages(ctx, "/sites/"+url.PathEscape(r.siteID)+"/clients", func(raw json.RawMessage) error {
		var page []apiClient
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		clients = append(clients, page...)
		return nil
	})
	if err != nil {
		return snapshot{site: r.siteName}, err
	}

	now := r.now()
	snap := snapshot{site: r.siteName, clients: make(map[string]Client, len(clients))}
	for _, c := range clients {
		mac, ok := macKey(c.MACAddress)
		if !ok {
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
		snap.clients[mac] = client
	}
	return snap, nil
}

// findSite looks the configured site up, and with it where the API is: under
// /proxy/network on a UniFi OS console, at the root of a self-hosted Network
// Application.
func (r *integration) findSite(ctx context.Context) error {
	var sites []apiSite
	var lastErr error
	for _, prefix := range []string{"/proxy/network/integration/v1", "/integration/v1"} {
		r.prefix = prefix
		sites = sites[:0]
		lastErr = r.pages(ctx, "/sites", func(raw json.RawMessage) error {
			var page []apiSite
			if err := json.Unmarshal(raw, &page); err != nil {
				return err
			}
			sites = append(sites, page...)
			return nil
		})
		// A Network Application without the API sends people elsewhere, or
		// doesn't know the path.
		var status *statusError
		if lastErr == nil || !errors.As(lastErr, &status) ||
			(status.code != http.StatusNotFound && (status.code < 300 || status.code > 399)) {
			break
		}
	}
	if lastErr != nil {
		r.prefix = ""
		var status *statusError
		if errors.As(lastErr, &status) && (status.code == http.StatusNotFound || (status.code >= 300 && status.code <= 399)) {
			return fmt.Errorf("%w; this UniFi Network Application has no Integration API, "+
				"sign in with unifi_username and unifi_password instead of an API key", lastErr)
		}
		return lastErr
	}

	var names []string
	for _, site := range sites {
		if site.ID == r.site || strings.EqualFold(site.InternalReference, r.site) || strings.EqualFold(site.Name, r.site) {
			r.siteID, r.siteName = site.ID, site.Name
			return nil
		}
		names = append(names, fmt.Sprintf("%q", site.InternalReference))
	}
	return fmt.Errorf("UniFi has no site %q; its sites are %s", r.site, strings.Join(names, ", "))
}

// devices returns the names of the site's UniFi devices by their IDs.
func (r *integration) devices(ctx context.Context) (map[string]string, error) {
	names := map[string]string{}
	err := r.pages(ctx, "/sites/"+url.PathEscape(r.siteID)+"/devices", func(raw json.RawMessage) error {
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
func (r *integration) pages(ctx context.Context, path string, each func(json.RawMessage) error) error {
	offset := 0
	for range maxPages {
		var page apiPage
		if err := r.get(ctx, path, url.Values{"offset": {fmt.Sprint(offset)}, "limit": {fmt.Sprint(pageLimit)}}, &page); err != nil {
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

func (r *integration) get(ctx context.Context, path string, query url.Values, into any) error {
	u := *r.base
	u.Path = strings.TrimSuffix(u.Path, "/") + r.prefix + path
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-KEY", r.key)
	req.Header.Set("Accept", "application/json")

	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("asking UniFi: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("reading UniFi's answer to %s: %w", r.prefix+path, err)
	}
	if resp.StatusCode != http.StatusOK {
		e := &statusError{code: resp.StatusCode, path: r.prefix + path, body: snippet(body)}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			e.hint = "check the API key"
		}
		return e
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("UniFi's answer to %s isn't what the Integration API sends: %w", r.prefix+path, err)
	}
	return nil
}
