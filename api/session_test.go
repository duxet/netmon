package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/duxet/netmon/auth"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

// newSignInApp is the app with single sign-on turned on. Nothing here signs
// anyone in at a provider, so it needs none: it only has to know it is there.
func newSignInApp(t *testing.T) (*fiber.App, *sql.DB) {
	t.Helper()

	return newTestAppFor(t, func(db *sql.DB) Options {
		authn, err := auth.New(db, auth.Options{
			Issuer:   "https://id.example.com",
			ClientID: "netmon",
			BaseURL:  "http://netmon.lan:2137",
		})
		if err != nil {
			t.Fatal(err)
		}
		return Options{Auth: authn}
	})
}

// request makes a request, optionally as someone with a session cookie.
func request(t *testing.T, app *fiber.App, method, path, cookie string) *http.Response {
	t.Helper()

	req := httptest.NewRequest(method, path, nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// signedIn makes a session for a person and returns the cookie of their
// browser. Sessions are stored by the SHA-256 of the cookie's token.
func signedIn(t *testing.T, db *sql.DB, subject, name, email string) string {
	t.Helper()

	token := "token-of-" + subject
	hash := sha256.Sum256([]byte(token))
	err := storage.CreateSession(db, hash[:], storage.SessionRecord{
		Subject: subject, Name: name, Email: email,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return "netmon_session=" + token
}

func TestSessionWithoutSignIn(t *testing.T) {
	app, _ := newTestApp(t)

	status, body := do(t, app, "GET", "/api/session", "")
	if status != fiber.StatusOK || body != `{"AuthEnabled":false,"User":null}` {
		t.Errorf("GET /api/session = %d %s, want sign-in off and no user", status, body)
	}
	if status, _ := do(t, app, "GET", "/api/stats", ""); status != fiber.StatusOK {
		t.Errorf("GET /api/stats = %d, want 200 without sign-in", status)
	}
}

func TestSessionWithSignIn(t *testing.T) {
	app, db := newSignInApp(t)

	resp := request(t, app, "GET", "/api/stats", "")
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != fiber.StatusUnauthorized || errorMessage(t, string(data)) != "authentication required" {
		t.Errorf("GET /api/stats without a session = %d %s, want a 401 the web UI can read", resp.StatusCode, data)
	}

	cookie := signedIn(t, db, "user-1", "Anna", "anna@example.com")
	resp = request(t, app, "GET", "/api/session", cookie)
	data, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("GET /api/session with a session = %d %s", resp.StatusCode, data)
	}
	var session struct {
		AuthEnabled bool
		User        struct{ Name, Email *string }
	}
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatal(err)
	}
	if !session.AuthEnabled || session.User.Name == nil || *session.User.Name != "Anna" || session.User.Email == nil || *session.User.Email != "anna@example.com" {
		t.Errorf("GET /api/session = %s, want Anna", data)
	}

	if resp := request(t, app, "GET", "/api/stats", cookie); resp.StatusCode != fiber.StatusOK {
		t.Errorf("GET /api/stats with a session = %d, want 200", resp.StatusCode)
	}
}

func TestSessionDTO(t *testing.T) {
	name, email := "Anna", "anna@example.com"
	for _, tc := range []struct {
		name   string
		signIn bool
		user   *auth.User
		want   SessionDTO
	}{
		{"no sign-in", false, nil, SessionDTO{}},
		{"no sign-in, whatever the user", false, &auth.User{Name: "Anna"}, SessionDTO{}},
		{"a person", true, &auth.User{Subject: "u1", Name: name, Email: email}, SessionDTO{AuthEnabled: true, User: &UserDTO{Name: &name, Email: &email}}},
		{"a person without a name or an address", true, &auth.User{Subject: "u1"}, SessionDTO{AuthEnabled: true, User: &UserDTO{}}},
	} {
		got := newSessionDTO(tc.signIn, tc.user)
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(tc.want)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("%s: got %s, want %s", tc.name, gotJSON, wantJSON)
		}
	}
}

// With single sign-on on, every route of the app needs a session, except the
// ones that sign people in. The routes are listed from the app itself, so one
// added later is checked too, and has to be added here on purpose to be open.
func TestEveryRouteNeedsASession(t *testing.T) {
	app, _ := newSignInApp(t)

	open := map[string]bool{
		"GET /auth/login":      true,
		"GET /auth/callback":   true,
		"POST /auth/logout":    true,
		"GET /auth/signed-out": true,
		// For container health checks, which can't sign in.
		"GET /healthz": true,
	}
	seenOpen := map[string]bool{}

	param := regexp.MustCompile(`:[A-Za-z0-9_]+\??|\*+`)
	checked := 0
	for _, route := range app.GetRoutes(true) {
		key := route.Method + " " + route.Path
		if open[key] {
			seenOpen[key] = true
			continue
		}

		path := param.ReplaceAllString(route.Path, "x")
		resp := request(t, app, route.Method, path, "")
		checked++

		switch {
		case strings.HasPrefix(path, "/api/") || path == "/metrics" || (route.Method != "GET" && route.Method != "HEAD"):
			if resp.StatusCode != fiber.StatusUnauthorized {
				t.Errorf("%s (%s) without a session = %d, want 401", key, path, resp.StatusCode)
			}
		default:
			if resp.StatusCode != fiber.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), "/auth/login?next=") {
				t.Errorf("%s (%s) without a session = %d to %q, want a redirect to sign in", key, path, resp.StatusCode, resp.Header.Get("Location"))
			}
		}
	}

	if checked < 30 {
		t.Errorf("checked %d routes, want all of them (more than 30)", checked)
	}
	for key := range open {
		if !seenOpen[key] {
			t.Errorf("%s is listed as open but is not a route", key)
		}
	}

	// The web UI's pages, which aren't routes of their own, and HEAD
	// requests, which the routes answer without being listed.
	for _, path := range []string{"/", "/live", "/clients/00:11:32:9b:c4:01", "/assets/index.js", "/favicon.svg"} {
		for _, method := range []string{"GET", "HEAD"} {
			resp := request(t, app, method, path, "")
			if resp.StatusCode != fiber.StatusFound {
				t.Errorf("%s %s without a session = %d, want a redirect to sign in", method, path, resp.StatusCode)
			}
		}
	}
	if resp := request(t, app, "HEAD", "/api/flows", ""); resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("HEAD /api/flows without a session = %d, want 401", resp.StatusCode)
	}
}
