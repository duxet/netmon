package auth

import (
	"database/sql"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
	migrate "github.com/rubenv/sql-migrate"
)

const testBaseURL = "http://netmon.lan:2137"

type testEnv struct {
	t     *testing.T
	idp   *fakeIdP
	db    *sql.DB
	authn *Authenticator
	app   *fiber.App
}

type envOptions struct {
	baseURL string
	// public makes netmon a public client, without a secret.
	public bool
}

func newEnv(t *testing.T, opts ...func(*envOptions)) *testEnv {
	t.Helper()

	o := envOptions{baseURL: testBaseURL}
	for _, opt := range opts {
		opt(&o)
	}

	secret := testClientSecret
	if o.public {
		secret = ""
	}
	idp := newFakeIdP(t, secret)

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../migrations"}, migrate.Up); err != nil {
		t.Fatal(err)
	}

	authn, err := New(db, Options{Issuer: idp.issuer(), ClientID: testClientID, ClientSecret: secret, BaseURL: o.baseURL})
	if err != nil {
		t.Fatal(err)
	}

	// The routes of the real app that matter here: something for the API and
	// something for the web UI.
	app := fiber.New()
	authn.Mount(app)
	app.Get("/api/hello", func(c fiber.Ctx) error {
		user := UserFrom(c)
		if user == nil {
			t.Error("UserFrom is nil in a route behind the check")
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		return c.SendString(user.Subject + "|" + user.Name + "|" + user.Email)
	})
	app.Post("/api/things", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })
	app.Get("/*", func(c fiber.Ctx) error { return c.SendString("web UI") })

	return &testEnv{t: t, idp: idp, db: db, authn: authn, app: app}
}

func (e *testEnv) browser() *browser {
	return &browser{t: e.t, cookies: map[string]*http.Cookie{}, do: func(req *http.Request) *http.Response {
		resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			e.t.Fatal(err)
		}
		e.t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}}
}

func (e *testEnv) sessions() int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// startSignIn opens the sign-in page and returns the address the browser is
// sent to at the provider.
func (e *testEnv) startSignIn(b *browser, next string) string {
	e.t.Helper()

	resp := b.get("/auth/login?next=" + url.QueryEscape(next))
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("GET /auth/login = %d, want 302 to the provider", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

// callbackTarget is the part of the address the provider sends the browser
// back to that a request is made to.
func callbackTarget(t *testing.T, callback string) string {
	t.Helper()
	u, err := url.Parse(callback)
	if err != nil {
		t.Fatal(err)
	}
	return u.RequestURI()
}

// signIn goes through all of signing in, and returns the response to the
// callback.
func (e *testEnv) signIn(b *browser, next string) *http.Response {
	e.t.Helper()

	authURL := e.startSignIn(b, next)
	callback := e.idp.authorize(authURL, e.authn.redirectURL)
	return b.get(callbackTarget(e.t, callback))
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, cookie := range resp.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func TestSignIn(t *testing.T) {
	e := newEnv(t)
	b := e.browser()

	// Opening a page without a session leads to signing in, and then back.
	resp := b.get("/clients/00:11:32:9b:c4:01?range=7d")
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/auth/login?next=%2Fclients%2F00%3A11%3A32%3A9b%3Ac4%3A01%3Frange%3D7d" {
		t.Fatalf("GET a page without a session = %d to %q, want a redirect to sign in", resp.StatusCode, resp.Header.Get("Location"))
	}

	authURL := e.startSignIn(b, "/clients/00:11:32:9b:c4:01?range=7d")
	login := b.cookies[loginCookie]
	if login == nil {
		t.Fatal("no login cookie was set")
	}
	if !login.HttpOnly || login.SameSite != http.SameSiteLaxMode || login.Path != "/auth" || login.Secure {
		t.Errorf("login cookie = %+v, want HttpOnly, SameSite=Lax, Path=/auth and not Secure over http", login)
	}
	if e.sessions() != 0 {
		t.Error("a session exists before anyone has signed in")
	}

	callback := e.idp.authorize(authURL, e.authn.redirectURL)
	resp = b.get(callbackTarget(t, callback))
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/clients/00:11:32:9b:c4:01?range=7d" {
		t.Fatalf("callback = %d to %q, want a redirect back to the page", resp.StatusCode, resp.Header.Get("Location"))
	}

	session := b.cookies[sessionCookie]
	if session == nil {
		t.Fatal("no session cookie was set")
	}
	if !session.HttpOnly || session.SameSite != http.SameSiteLaxMode || session.Path != "/" || session.Secure {
		t.Errorf("session cookie = %+v, want HttpOnly, SameSite=Lax, Path=/ and not Secure over http", session)
	}
	if lifetime := time.Duration(session.MaxAge) * time.Second; lifetime < sessionLifetime-time.Minute || lifetime > sessionLifetime {
		t.Errorf("session lasts %v, want %v", lifetime, sessionLifetime)
	}
	if _, ok := b.cookies[loginCookie]; ok {
		t.Error("the login cookie is kept after signing in")
	}
	if e.sessions() != 1 {
		t.Errorf("%d sessions stored, want 1", e.sessions())
	}

	// The token is what is in the cookie; only its hash is in the database.
	var stored []byte
	if err := e.db.QueryRow(`SELECT token_hash FROM sessions`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) == session.Value || len(stored) != 32 {
		t.Errorf("the database holds %x, want the SHA-256 of the cookie's value", stored)
	}

	resp = b.get("/api/hello")
	if got := body(t, resp); resp.StatusCode != http.StatusOK || got != "user-1|Anna Kowalska|anna@example.com" {
		t.Errorf("GET /api/hello = %d %q, want the signed-in person", resp.StatusCode, got)
	}
	if resp = b.get("/clients"); resp.StatusCode != http.StatusOK || body(t, resp) != "web UI" {
		t.Errorf("GET a page = %d, want the web UI", resp.StatusCode)
	}

	// The provider was held to the protocol: the secret as the client's proof,
	// and the PKCE verifier that matches the challenge in the first request.
	requests := e.idp.requests()
	if len(requests) != 1 || !requests[0].hasBasic && requests[0].form.Get("client_secret") != testClientSecret {
		t.Errorf("token requests = %+v, want one that authenticates the client", requests)
	}
}

func TestSignInAsPublicClient(t *testing.T) {
	e := newEnv(t, func(o *envOptions) { o.public = true })
	b := e.browser()

	resp := e.signIn(b, "/")
	if resp.StatusCode != http.StatusFound || b.cookies[sessionCookie] == nil {
		t.Fatalf("callback = %d, want a session for a client without a secret", resp.StatusCode)
	}

	requests := e.idp.requests()
	if len(requests) != 1 || requests[0].hasBasic || requests[0].form.Get("client_id") != testClientID || requests[0].form.Get("client_secret") != "" {
		t.Errorf("token requests = %+v, want the client ID in the body and no secret", requests)
	}
}

func TestCookiesAreSecureOverHTTPS(t *testing.T) {
	e := newEnv(t, func(o *envOptions) { o.baseURL = "https://netmon.example.com" })
	b := e.browser()

	e.startSignIn(b, "/")
	if login := b.cookies[loginCookie]; login == nil || !login.Secure {
		t.Errorf("login cookie = %+v, want Secure", login)
	}

	if resp := e.signIn(b, "/"); resp.StatusCode != http.StatusFound {
		t.Fatalf("callback = %d", resp.StatusCode)
	}
	if session := b.cookies[sessionCookie]; session == nil || !session.Secure {
		t.Errorf("session cookie = %+v, want Secure", session)
	}
	if want := "https://netmon.example.com/auth/callback"; e.authn.redirectURL != want {
		t.Errorf("redirect URL = %q, want %q", e.authn.redirectURL, want)
	}
}

func TestRequestsWithoutSession(t *testing.T) {
	e := newEnv(t)
	b := e.browser()

	for _, tc := range []struct {
		method, target string
		status         int
		location       string
	}{
		{"GET", "/api/hello", 401, ""},
		{"GET", "/api/nothing-here", 401, ""},
		{"POST", "/api/things", 401, ""},
		{"DELETE", "/api/things/1", 401, ""},
		// Pages send people to sign in; anything else that isn't a page is told.
		{"GET", "/", 302, "/auth/login?next=%2F"},
		{"GET", "/assets/index.js", 302, "/auth/login?next=%2Fassets%2Findex.js"},
		{"GET", "/auth/unknown", 302, "/auth/login?next=%2Fauth%2Funknown"},
		{"POST", "/page", 401, ""},
		// The address can't be used to bounce people to another site afterwards.
		{"GET", "//evil.example/x", 302, "/auth/login?next=%2F%2Fevil.example%2Fx"},
	} {
		resp := b.request(tc.method, tc.target)
		if resp.StatusCode != tc.status || resp.Header.Get("Location") != tc.location {
			t.Errorf("%s %s = %d to %q, want %d to %q", tc.method, tc.target, resp.StatusCode, resp.Header.Get("Location"), tc.status, tc.location)
		}
	}
}

func TestInvalidSessions(t *testing.T) {
	e := newEnv(t)
	b := e.browser()

	good, expired := "good-token", "expired-token"
	now := time.Now()
	for token, expires := range map[string]time.Time{good: now.Add(time.Hour), expired: now.Add(-time.Minute)} {
		err := storage.CreateSession(e.db, hashToken(token), storage.SessionRecord{Subject: "user-1", CreatedAt: now.Add(-time.Hour), ExpiresAt: expires})
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name, token string
		status      int
	}{
		{"a session", good, 200},
		{"an expired session", expired, 401},
		{"a token nobody was given", "made-up-token", 401},
		{"the hash instead of the token", base64.RawURLEncoding.EncodeToString(hashToken(good)), 401},
		{"an empty token", "", 401},
		{"an absurd token", strings.Repeat("a", 4_000), 401},
	} {
		req := httptest.NewRequest("GET", "/api/hello", nil)
		if tc.token != "" {
			req.Header.Set("Cookie", sessionCookie+"="+tc.token)
		}
		resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Errorf("%s: GET /api/hello = %d, want %d", tc.name, resp.StatusCode, tc.status)
		}
	}
	_ = b
}

func TestSignOut(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	e.signIn(b, "/")
	if e.sessions() != 1 {
		t.Fatalf("%d sessions stored, want 1", e.sessions())
	}
	token := b.cookies[sessionCookie].Value

	if resp := b.request("POST", "/auth/logout"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /auth/logout = %d, want 204", resp.StatusCode)
	}
	if e.sessions() != 0 {
		t.Error("the session is still stored after signing out")
	}
	if _, ok := b.cookies[sessionCookie]; ok {
		t.Error("the session cookie is kept after signing out")
	}
	if resp := b.get("/api/hello"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/hello after signing out = %d, want 401", resp.StatusCode)
	}

	// A copy of the cookie is no good either: the session is over, not just the browser's.
	req := httptest.NewRequest("GET", "/api/hello", nil)
	req.Header.Set("Cookie", sessionCookie+"="+token)
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/hello with the old cookie = %d, want 401", resp.StatusCode)
	}

	// Signing out twice, and while not signed in, works.
	if resp := b.request("POST", "/auth/logout"); resp.StatusCode != http.StatusNoContent {
		t.Errorf("POST /auth/logout while signed out = %d, want 204", resp.StatusCode)
	}

	// The page it ends on can be seen without a session.
	resp = b.get("/auth/signed-out")
	if got := body(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(got, "signed out") || !strings.Contains(got, `href="/auth/login"`) {
		t.Errorf("GET /auth/signed-out = %d %q, want the page", resp.StatusCode, got)
	}
}

func TestSigningInAgainReplacesTheSession(t *testing.T) {
	e := newEnv(t)
	b := e.browser()

	e.signIn(b, "/")
	first := b.cookies[sessionCookie].Value
	e.signIn(b, "/")
	second := b.cookies[sessionCookie].Value

	if first == second {
		t.Error("a new sign-in reused the session token")
	}
	if e.sessions() != 1 {
		t.Errorf("%d sessions stored, want only the new one", e.sessions())
	}
}

func TestCallbackRejects(t *testing.T) {
	// Each case spoils one thing in an otherwise fine sign-in.
	for _, tc := range []struct {
		name string
		// setup runs before the browser is sent back, and may replace the
		// address it goes to.
		setup  func(e *testEnv, b *browser, callback string) string
		status int
		// shown is text the page has, hidden text it must not have.
		shown, hidden string
	}{
		{
			name: "no login cookie",
			setup: func(e *testEnv, b *browser, callback string) string {
				delete(b.cookies, loginCookie)
				return callback
			},
			status: 400, shown: "Sign-in expired",
		},
		{
			name: "a callback made up for another browser",
			setup: func(e *testEnv, b *browser, callback string) string {
				// Their code and state, in a browser that started its own sign-in.
				other := e.browser()
				e.startSignIn(other, "/")
				b.cookies = other.cookies
				return callback
			},
			status: 400, shown: "Sign-in expired",
		},
		{
			name: "another state",
			setup: func(e *testEnv, b *browser, callback string) string {
				u, _ := url.Parse(callback)
				q := u.Query()
				q.Set("state", "another-state")
				u.RawQuery = q.Encode()
				return u.String()
			},
			status: 400, shown: "Sign-in expired",
		},
		{
			name: "no state",
			setup: func(e *testEnv, b *browser, callback string) string {
				u, _ := url.Parse(callback)
				q := u.Query()
				q.Del("state")
				u.RawQuery = q.Encode()
				return u.String()
			},
			status: 400, shown: "Sign-in expired",
		},
		{
			name: "no code",
			setup: func(e *testEnv, b *browser, callback string) string {
				u, _ := url.Parse(callback)
				q := u.Query()
				q.Del("code")
				u.RawQuery = q.Encode()
				return u.String()
			},
			status: 400, shown: "Sign-in expired",
		},
		{
			name: "a code the provider doesn't know",
			setup: func(e *testEnv, b *browser, callback string) string {
				u, _ := url.Parse(callback)
				q := u.Query()
				q.Set("code", "made-up")
				u.RawQuery = q.Encode()
				return u.String()
			},
			status: 502, shown: "Sign-in failed",
		},
		{
			name: "the provider refusing the code",
			setup: func(e *testEnv, b *browser, callback string) string {
				e.idp.set(func(i *fakeIdP) { i.tokenFails = true })
				return callback
			},
			status: 502, shown: "Sign-in failed",
		},
		{
			name: "no ID token",
			setup: func(e *testEnv, b *browser, callback string) string {
				e.idp.set(func(i *fakeIdP) { i.omitIDToken = true })
				return callback
			},
			status: 401, shown: "Sign-in failed",
		},
		{
			name: "an ID token for another sign-in",
			setup: func(e *testEnv, b *browser, callback string) string {
				e.idp.set(func(i *fakeIdP) { i.claims = func(c map[string]any) { c["nonce"] = "another-nonce" } })
				return callback
			},
			status: 401, shown: "Sign-in failed",
		},
		{
			name: "an ID token without a nonce",
			setup: func(e *testEnv, b *browser, callback string) string {
				e.idp.set(func(i *fakeIdP) { i.claims = func(c map[string]any) { delete(c, "nonce") } })
				return callback
			},
			status: 401, shown: "Sign-in failed",
		},
		{
			name: "an ID token for another client",
			setup: func(e *testEnv, b *browser, callback string) string {
				e.idp.set(func(i *fakeIdP) { i.claims = func(c map[string]any) { c["aud"] = "another-app" } })
				return callback
			},
			status: 401, shown: "Sign-in failed",
		},
		{
			name: "an ID token from another issuer",
			setup: func(e *testEnv, b *browser, callback string) string {
				e.idp.set(func(i *fakeIdP) { i.claims = func(c map[string]any) { c["iss"] = "https://evil.example" } })
				return callback
			},
			status: 401, shown: "Sign-in failed",
		},
		{
			name: "an expired ID token",
			setup: func(e *testEnv, b *browser, callback string) string {
				e.idp.set(func(i *fakeIdP) {
					i.claims = func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }
				})
				return callback
			},
			status: 401, shown: "Sign-in failed",
		},
		{
			name: "an ID token signed with another key",
			setup: func(e *testEnv, b *browser, callback string) string {
				e.idp.set(func(i *fakeIdP) { i.signWith = i.other })
				return callback
			},
			status: 401, shown: "Sign-in failed",
		},
		{
			name: "an ID token without a subject",
			setup: func(e *testEnv, b *browser, callback string) string {
				e.idp.set(func(i *fakeIdP) { i.claims = func(c map[string]any) { delete(c, "sub") } })
				return callback
			},
			status: 401, shown: "Sign-in failed",
		},
		{
			name: "the person being turned away by the provider",
			setup: func(e *testEnv, b *browser, callback string) string {
				u, _ := url.Parse(callback)
				u.RawQuery = url.Values{
					"error":             {"access_denied"},
					"error_description": {"<b>Call 555-0100 to unlock your account</b>"},
				}.Encode()
				return u.String()
			},
			// What the provider says is shown to no one, as anyone could make up a link.
			status: 403, shown: "access_denied", hidden: "555-0100",
		},
		{
			name: "an error that isn't an error code",
			setup: func(e *testEnv, b *browser, callback string) string {
				u, _ := url.Parse(callback)
				u.RawQuery = url.Values{"error": {"<script>alert(1)</script>"}}.Encode()
				return u.String()
			},
			status: 403, shown: "(error)", hidden: "<script>",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			b := e.browser()

			authURL := e.startSignIn(b, "/")
			callback := e.idp.authorize(authURL, e.authn.redirectURL)
			callback = tc.setup(e, b, callback)

			resp := b.get(callbackTarget(t, callback))
			page := body(t, resp)
			if resp.StatusCode != tc.status {
				t.Errorf("callback = %d, want %d: %s", resp.StatusCode, tc.status, page)
			}
			if !strings.Contains(page, tc.shown) {
				t.Errorf("page %q doesn't say %q", page, tc.shown)
			}
			if tc.hidden != "" && strings.Contains(page, tc.hidden) {
				t.Errorf("page shows %q, which came from the request", tc.hidden)
			}
			if resp.Header.Get("Cache-Control") != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", resp.Header.Get("Cache-Control"))
			}

			// Nobody was signed in, and the attempt is over.
			if e.sessions() != 0 || b.cookies[sessionCookie] != nil {
				t.Error("a session was started")
			}
			if _, ok := b.cookies[loginCookie]; ok {
				t.Error("the login cookie is kept after a failed attempt")
			}
			if resp := b.get("/api/hello"); resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("GET /api/hello afterwards = %d, want 401", resp.StatusCode)
			}
		})
	}
}

func TestCallbackWorksOnce(t *testing.T) {
	e := newEnv(t)
	b := e.browser()

	authURL := e.startSignIn(b, "/")
	callback := callbackTarget(t, e.idp.authorize(authURL, e.authn.redirectURL))
	if resp := b.get(callback); resp.StatusCode != http.StatusFound {
		t.Fatalf("callback = %d, want a redirect", resp.StatusCode)
	}

	// The same address again, such as from the browser's history, with the
	// session of the first time still there: no new session.
	if resp := b.get(callback); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("callback again = %d, want 400", resp.StatusCode)
	}
	if e.sessions() != 1 {
		t.Errorf("%d sessions stored, want 1", e.sessions())
	}
}

func TestSignInReturnsToLocalPagesOnly(t *testing.T) {
	for next, want := range map[string]string{
		"/clients/abc?range=7d":                  "/clients/abc?range=7d",
		"/":                                      "/",
		"":                                       "/",
		"//evil.example":                         "/",
		"//evil.example/path":                    "/",
		"/\\evil.example":                        "/",
		"https://evil.example":                   "/",
		"javascript:alert(1)":                    "/",
		"evil.example":                           "/",
		"/ok\r\nSet-Cookie: x":                   "/",
		"/auth/login":                            "/",
		"/auth/callback?x=1":                     "/",
		"/auth":                                  "/",
		"/author":                                "/author",
		"/" + strings.Repeat("a", maxNextLength): "/",
	} {
		name := next
		if len(name) > 40 {
			name = name[:40]
		}
		t.Run(name, func(t *testing.T) {
			if got := safeNext(next); got != want {
				t.Errorf("safeNext(%q) = %q, want %q", next, got, want)
			}

			// And through the whole sign-in, with the value in the address.
			e := newEnv(t)
			b := e.browser()
			resp := e.signIn(b, next)
			if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != want {
				t.Errorf("signed in with next=%q: redirected to %q (%d), want %q", next, resp.Header.Get("Location"), resp.StatusCode, want)
			}
		})
	}
}

func TestDisplayName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims func(c map[string]any)
		want   string
	}{
		{"the name", func(c map[string]any) {}, "user-1|Anna Kowalska|anna@example.com"},
		{"the username without a name", func(c map[string]any) { delete(c, "name") }, "user-1|anna|anna@example.com"},
		{"nothing but the subject", func(c map[string]any) {
			delete(c, "name")
			delete(c, "preferred_username")
			delete(c, "email")
		}, "user-1||"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.idp.set(func(i *fakeIdP) { i.claims = tc.claims })
			b := e.browser()
			e.signIn(b, "/")

			resp := b.get("/api/hello")
			if got := body(t, resp); got != tc.want {
				t.Errorf("signed-in person = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProviderUnavailable(t *testing.T) {
	e := newEnv(t)
	e.authn.retry = 0
	b := e.browser()

	// A session from before the provider went down.
	err := storage.CreateSession(e.db, hashToken("token"), storage.SessionRecord{
		Subject: "user-1", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.idp.set(func(i *fakeIdP) { i.down = true })

	if err := e.authn.Check(); err == nil {
		t.Error("Check succeeded while the provider is down")
	}

	resp := b.get("/auth/login")
	if got := body(t, resp); resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(got, "Sign-in is unavailable") {
		t.Errorf("GET /auth/login = %d %q, want a page that says sign-in is unavailable", resp.StatusCode, got)
	}
	if _, ok := b.cookies[loginCookie]; ok {
		t.Error("a login cookie was set without a provider to send people to")
	}

	// People who are signed in don't need the provider.
	req := httptest.NewRequest("GET", "/api/hello", nil)
	req.Header.Set("Cookie", sessionCookie+"=token")
	resp2, err := e.app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("GET /api/hello with a session while the provider is down = %d, want 200", resp2.StatusCode)
	}

	// Once it is back, signing in works without restarting netmon.
	e.idp.set(func(i *fakeIdP) { i.down = false })
	if resp := e.signIn(b, "/"); resp.StatusCode != http.StatusFound || b.cookies[sessionCookie] == nil {
		t.Errorf("callback after the provider came back = %d, want a session", resp.StatusCode)
	}
	if err := e.authn.Check(); err != nil {
		t.Errorf("Check after the provider came back: %v", err)
	}
}

func TestProviderIsNotAskedAgainRightAway(t *testing.T) {
	e := newEnv(t)
	e.idp.set(func(i *fakeIdP) { i.down = true })
	b := e.browser()

	for range 3 {
		if resp := b.get("/auth/login"); resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("GET /auth/login = %d, want 503", resp.StatusCode)
		}
	}

	// The provider is back, but netmon waits out the retry delay before it
	// believes it: people reloading the page don't pile on a provider that is down.
	e.idp.set(func(i *fakeIdP) { i.down = false })
	if resp := b.get("/auth/login"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("GET /auth/login straight after a failure = %d, want 503", resp.StatusCode)
	}
	e.authn.mu.Lock()
	e.authn.lastFailure = time.Now().Add(-discoveryRetry)
	e.authn.mu.Unlock()
	if resp := b.get("/auth/login"); resp.StatusCode != http.StatusFound {
		t.Errorf("GET /auth/login after the retry delay = %d, want a redirect to the provider", resp.StatusCode)
	}
}

func TestWrongIssuer(t *testing.T) {
	e := newEnv(t)

	// The provider's discovery document says it is something else than the
	// address netmon was told to use: ID tokens couldn't be matched to it.
	authn, err := New(e.db, Options{Issuer: e.idp.issuer() + "/", ClientID: testClientID, BaseURL: testBaseURL})
	if err != nil {
		t.Fatal(err)
	}
	if err := authn.Check(); err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Errorf("Check with a trailing slash on the issuer = %v, want an issuer mismatch", err)
	}
}

func TestNewValidatesOptions(t *testing.T) {
	for name, opts := range map[string]Options{
		"no issuer":       {ClientID: "c", BaseURL: testBaseURL},
		"no client":       {Issuer: "https://id.example.com", BaseURL: testBaseURL},
		"no base URL":     {Issuer: "https://id.example.com", ClientID: "c"},
		"a base URL path": {Issuer: "https://id.example.com", ClientID: "c", BaseURL: "netmon.lan"},
		"a base URL ftp":  {Issuer: "https://id.example.com", ClientID: "c", BaseURL: "ftp://netmon.lan"},
		"a hostless URL":  {Issuer: "https://id.example.com", ClientID: "c", BaseURL: "http://"},
	} {
		if _, err := New(nil, opts); err == nil {
			t.Errorf("New with %s succeeded, want an error", name)
		}
	}
}
