package unifi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// statusMFARequired is what UniFi OS answers a login with when the
	// account needs a second factor.
	statusMFARequired = 499
	// pauseAfterRefusal is how long to wait before logging in again after
	// UniFi refused the login or limited the attempts, which each attempt
	// extends. After rejected credentials it is longer, as they won't fix
	// themselves, and too many attempts lock the account.
	pauseAfterRefusal     = 5 * time.Minute
	pauseAfterBadPassword = 30 * time.Minute
)

// standalonePage tells the self-hosted Network Application's pages apart
// from UniFi OS ones: they are served below /manage/.
var standalonePage = regexp.MustCompile(`(?i)<base\s+href=["']/manage/["']`)

// errLoginRequired is a request that needs a session, which ran out.
var errLoginRequired = errors.New("UniFi asks to log in again")

// legacy reads clients through the API the UniFi web UI uses, signing in with
// the username and password of a local account, as the web UI does.
type legacy struct {
	http     *http.Client
	base     *url.URL
	username string
	password string
	site     string
	now      func() time.Time

	detected bool
	unifiOS  bool
	cookie   string // the session's cookies, as a Cookie header
	csrf     string // UniFi OS's CSRF token, which every request needs

	siteName string
	// After a refused login, the next isn't tried before noLoginBefore, and
	// syncs fail with loginErr until then.
	noLoginBefore time.Time
	loginErr      error
}

// envelope is how the API wraps its answers.
type envelope struct {
	Meta struct {
		RC  string `json:"rc"`
		Msg string `json:"msg"`
	} `json:"meta"`
	Data json.RawMessage `json:"data"`
}

func (l *legacy) read(ctx context.Context) (snapshot, error) {
	if !l.detected {
		if err := l.detect(ctx); err != nil {
			return snapshot{}, err
		}
	}
	if l.cookie == "" {
		if err := l.login(ctx); err != nil {
			return snapshot{site: l.siteName}, err
		}
	}

	snap, err := l.clients(ctx)
	if errors.Is(err, errLoginRequired) {
		// The session ran out; one new login per sync.
		l.cookie, l.csrf = "", ""
		if err := l.login(ctx); err != nil {
			return snapshot{site: l.siteName}, err
		}
		snap, err = l.clients(ctx)
	}
	if err != nil {
		return snapshot{site: l.siteName}, err
	}
	return snap, nil
}

// detect finds out whether the controller runs UniFi OS, which serves the
// Network Application under /proxy/network and signs in differently.
func (l *legacy) detect(ctx context.Context) error {
	resp, body, err := l.do(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return err
	}
	l.unifiOS = resp.StatusCode == http.StatusOK && !standalonePage.Match(body)
	l.detected = true
	return nil
}

// login signs in, and keeps the session's cookies and CSRF token.
func (l *legacy) login(ctx context.Context) error {
	if now := l.now(); now.Before(l.noLoginBefore) {
		return fmt.Errorf("%w (trying again at %s)", l.loginErr, l.noLoginBefore.Format("15:04"))
	}

	path := "/api/login"
	if l.unifiOS {
		path = "/api/auth/login"
	}
	credentials, err := json.Marshal(map[string]any{"username": l.username, "password": l.password, "rememberMe": true})
	if err != nil {
		return err
	}
	resp, body, err := l.do(ctx, http.MethodPost, path, credentials)
	if err != nil {
		return err
	}

	// The body is left out of errors: a proxy could echo the password.
	refused := func(pause time.Duration, err error) error {
		l.noLoginBefore, l.loginErr = l.now().Add(pause), err
		return err
	}
	msg := apiMessage(body)
	switch {
	case resp.StatusCode == statusMFARequired || msg == "api.err.Ubic2faTokenRequired":
		return refused(pauseAfterBadPassword, errors.New("the UniFi account needs two-factor authentication, "+
			"which netmon can't do; use a local account without it"))
	case msg == "api.err.Invalid" || resp.StatusCode == http.StatusUnauthorized:
		return refused(pauseAfterBadPassword, errors.New("UniFi rejected the username or password"))
	case resp.StatusCode == http.StatusTooManyRequests:
		return refused(pauseAfterRefusal, errors.New("UniFi limits login attempts; netmon waits before trying again"))
	case resp.StatusCode == http.StatusForbidden:
		// UniFi OS answers wrong credentials this way too.
		return refused(pauseAfterRefusal, errors.New("UniFi refused the login (403 Forbidden); check the username and password"))
	case resp.StatusCode != http.StatusOK:
		detail := ""
		if msg != "" {
			detail = ": " + msg
		}
		return fmt.Errorf("logging in to UniFi failed with %d %s%s", resp.StatusCode, http.StatusText(resp.StatusCode), detail)
	}

	var cookies []string
	for _, c := range resp.Cookies() {
		if c.Value != "" {
			cookies = append(cookies, c.Name+"="+c.Value)
		}
	}
	if len(cookies) == 0 {
		return errors.New("logging in to UniFi gave no session cookie")
	}
	l.cookie = strings.Join(cookies, "; ")
	l.csrf = resp.Header.Get("X-Csrf-Token")
	l.noLoginBefore, l.loginErr = time.Time{}, nil
	return nil
}

// apiMessage returns the error message of an API answer, such as
// "api.err.Invalid", or "".
func apiMessage(body []byte) string {
	var e envelope
	if json.Unmarshal(body, &e) != nil || e.Meta.RC != "error" {
		return ""
	}
	return e.Meta.Msg
}

type legacySite struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
	ID   string `json:"_id"`
}

type legacyStation struct {
	MAC        string `json:"mac"`
	Name       string `json:"name"`
	Hostname   string `json:"hostname"`
	IP         string `json:"ip"`
	IsWired    bool   `json:"is_wired"`
	APMAC      string `json:"ap_mac"`
	SwitchMAC  string `json:"sw_mac"`
	SwitchPort number `json:"sw_port"`
	Uptime     number `json:"uptime"`
	AssocTime  number `json:"assoc_time"`
	LastSeen   number `json:"last_seen"`
}

type legacyUser struct {
	MAC      string `json:"mac"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	IsWired  bool   `json:"is_wired"`
	LastSeen number `json:"last_seen"`
}

type legacyDevice struct {
	MAC   string `json:"mac"`
	Name  string `json:"name"`
	Model string `json:"model"`
}

// number is a number the API may send as a string, or not at all.
type number float64

func (n *number) UnmarshalJSON(b []byte) error {
	text := strings.Trim(string(b), `"`)
	if text == "" || text == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		// Not a number after all; it is only ever extra detail.
		*n = 0
		return nil
	}
	*n = number(f)
	return nil
}

func (n number) time() time.Time {
	if n <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(n), 0)
}

// clients reads the site's clients: the connected ones, with their uplinks,
// and the names of the others UniFi knows.
func (l *legacy) clients(ctx context.Context) (snapshot, error) {
	if l.siteName == "" {
		if err := l.findSite(ctx); err != nil {
			return snapshot{}, err
		}
	}

	var stations []legacyStation
	if err := l.get(ctx, "/stat/sta", &stations); err != nil {
		return snapshot{}, err
	}

	// Uplink names and the clients that aren't connected are only a nicety,
	// so failing to read them isn't a failed sync, unless the session ran out.
	var devices []legacyDevice
	if err := l.get(ctx, "/stat/device", &devices); errors.Is(err, errLoginRequired) {
		return snapshot{}, err
	}
	var users []legacyUser
	if err := l.get(ctx, "/rest/user", &users); errors.Is(err, errLoginRequired) {
		return snapshot{}, err
	}

	uplinks := map[string]string{}
	for _, d := range devices {
		mac, ok := macKey(d.MAC)
		if !ok {
			continue
		}
		name := strings.TrimSpace(d.Name)
		if name == "" {
			name = d.Model
		}
		uplinks[mac] = name
	}

	now := l.now()
	snap := snapshot{site: l.siteName, clients: make(map[string]Client, len(stations)+len(users))}
	for _, u := range users {
		mac, ok := macKey(u.MAC)
		name := firstNonEmpty(u.Name, u.Hostname)
		if !ok || name == "" {
			continue
		}
		snap.clients[mac] = Client{Name: name, Type: connectionType(u.IsWired), LastSeen: u.LastSeen.time()}
	}
	for _, s := range stations {
		mac, ok := macKey(s.MAC)
		if !ok {
			continue
		}
		client := Client{
			Name:      firstNonEmpty(s.Name, s.Hostname),
			Type:      connectionType(s.IsWired),
			IPAddress: s.IP,
			Connected: true,
			LastSeen:  now,
		}
		if s.IsWired {
			if up, ok := macKey(s.SwitchMAC); ok && uplinks[up] != "" {
				client.Uplink = uplinks[up]
				if s.SwitchPort > 0 {
					client.Uplink += fmt.Sprintf(", port %d", int(s.SwitchPort))
				}
			}
		} else if up, ok := macKey(s.APMAC); ok {
			client.Uplink = uplinks[up]
		}
		switch {
		case s.Uptime > 0:
			client.ConnectedAt = now.Add(-time.Duration(s.Uptime) * time.Second).Truncate(time.Second)
		case s.AssocTime > 0:
			client.ConnectedAt = s.AssocTime.time()
		}
		snap.clients[mac] = client
	}
	return snap, nil
}

func connectionType(wired bool) string {
	if wired {
		return Wired
	}
	return Wireless
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// findSite checks that the configured site exists, and takes in its name. The
// API knows sites by their short name, such as "default".
func (l *legacy) findSite(ctx context.Context) error {
	var sites []legacySite
	if err := l.request(ctx, "/api/self/sites", &sites); err != nil {
		return err
	}
	var names []string
	for _, site := range sites {
		if strings.EqualFold(site.Name, l.site) || strings.EqualFold(site.Desc, l.site) || site.ID == l.site {
			l.site = site.Name
			l.siteName = firstNonEmpty(site.Desc, site.Name)
			return nil
		}
		names = append(names, fmt.Sprintf("%q", site.Name))
	}
	return fmt.Errorf("UniFi has no site %q; its sites are %s", l.site, strings.Join(names, ", "))
}

// get reads a list of the site's from path, such as /stat/sta.
func (l *legacy) get(ctx context.Context, path string, into any) error {
	return l.request(ctx, "/api/s/"+url.PathEscape(l.site)+path, into)
}

// request reads the data of the API's answer to path into into.
func (l *legacy) request(ctx context.Context, path string, into any) error {
	if l.unifiOS {
		path = "/proxy/network" + path
	}
	resp, body, err := l.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if token := resp.Header.Get("X-Updated-Csrf-Token"); token != "" {
		l.csrf = token
	}

	msg := apiMessage(body)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || msg == "api.err.LoginRequired":
		return errLoginRequired
	case msg == "api.err.NoPermission":
		return fmt.Errorf("the UniFi account may not read %s; give it access to the site", path)
	case resp.StatusCode != http.StatusOK:
		return &statusError{code: resp.StatusCode, path: path, body: firstNonEmpty(msg, snippet(body))}
	case msg != "":
		return fmt.Errorf("UniFi answered %s with an error: %s", path, msg)
	}

	var e envelope
	if err := json.Unmarshal(body, &e); err != nil || e.Data == nil {
		return fmt.Errorf("UniFi's answer to %s isn't what the Network Application sends", path)
	}
	if err := json.Unmarshal(e.Data, into); err != nil {
		return fmt.Errorf("UniFi's answer to %s isn't what the Network Application sends: %w", path, err)
	}
	return nil
}

// do sends a request with the session, and returns the answer and its body.
func (l *legacy) do(ctx context.Context, method, path string, body []byte) (*http.Response, []byte, error) {
	u := *l.base
	u.Path = strings.TrimSuffix(u.Path, "/") + path

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if l.cookie != "" {
		req.Header.Set("Cookie", l.cookie)
	}
	if l.csrf != "" {
		req.Header.Set("X-Csrf-Token", l.csrf)
	}

	resp, err := l.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("asking UniFi: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, nil, fmt.Errorf("reading UniFi's answer to %s: %w", path, err)
	}
	return resp, data, nil
}
