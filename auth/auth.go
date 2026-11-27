// Package auth signs people in with an OpenID Connect provider, such as
// Pocket ID, using the authorization code flow with PKCE.
//
// Once [Authenticator.Mount]ed, every route needs a session: requests to the
// API without one are answered with a 401, and browsers are sent to the
// provider to sign in. A session is a random token in a cookie; the database
// keeps its hash, so sessions survive restarts and can be ended.
//
// The provider is only contacted when someone signs in, and again later if
// that failed. When it is down, people who are signed in carry on, and netmon
// keeps recording traffic.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
	"golang.org/x/oauth2"
)

const (
	loginPath     = "/auth/login"
	callbackPath  = "/auth/callback"
	logoutPath    = "/auth/logout"
	signedOutPath = "/auth/signed-out"

	sessionCookie = "netmon_session"

	// sessionLifetime is how long people stay signed in. It is not extended by
	// using netmon, so that taking away someone's access at the provider
	// takes effect within this time.
	sessionLifetime = 7 * 24 * time.Hour

	// discoveryTimeout bounds every request to the provider.
	discoveryTimeout = 10 * time.Second
	// discoveryRetry is how long to wait before asking a provider that failed
	// again, so that people reloading the sign-in page don't pile requests on it.
	discoveryRetry = 5 * time.Second
)

// Options configures an [Authenticator].
type Options struct {
	// Issuer is the provider's issuer URL, as the provider writes it.
	Issuer string
	// ClientID and ClientSecret identify netmon at the provider. A public
	// client has no secret.
	ClientID     string
	ClientSecret string
	// BaseURL is where people reach netmon, such as https://netmon.example.com,
	// without a trailing slash. The provider sends them back to
	// BaseURL/auth/callback, and their cookies are marked Secure when it is https.
	BaseURL string
	// HTTPClient makes the requests to the provider. By default it is a client
	// that gives up after 10 seconds.
	HTTPClient *http.Client
}

// User is a person who is signed in, or an API token.
type User struct {
	// Subject is the provider's stable identifier of the person.
	Subject string
	// Name is the person's name, or username, and Email their address, when
	// the provider told. Either may be empty.
	Name  string
	Email string
	// TokenID is the API token the request came with, and Name its name; 0
	// for a person with a session.
	TokenID int64
}

// Authenticator signs people in and checks their sessions.
type Authenticator struct {
	db           *sql.DB
	issuer       string
	clientID     string
	clientSecret string
	redirectURL  string
	secure       bool
	client       *http.Client

	// retry is discoveryRetry, a field so that tests needn't wait for it.
	retry time.Duration

	mu          sync.Mutex
	provider    *provider
	failure     error
	lastFailure time.Time
}

// provider is what is known about the OpenID Connect provider once it has
// been reached.
type provider struct {
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// New returns an Authenticator that keeps its sessions in db. It doesn't
// contact the provider, see [Authenticator.Check].
func New(db *sql.DB, opts Options) (*Authenticator, error) {
	if opts.Issuer == "" || opts.ClientID == "" {
		return nil, errors.New("auth: an issuer and a client ID are required")
	}
	base, err := url.Parse(opts.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, fmt.Errorf("auth: the base URL must be an http:// or https:// address: %q", opts.BaseURL)
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: discoveryTimeout}
	}

	return &Authenticator{
		db:           db,
		issuer:       opts.Issuer,
		clientID:     opts.ClientID,
		clientSecret: opts.ClientSecret,
		redirectURL:  strings.TrimSuffix(opts.BaseURL, "/") + callbackPath,
		secure:       base.Scheme == "https",
		client:       client,
		retry:        discoveryRetry,
	}, nil
}

// Check contacts the provider, to find out early that it can't be reached or
// that the issuer is wrong. A failure is not final: signing in tries again.
func (a *Authenticator) Check() error {
	_, err := a.discover()
	return err
}

// discover returns what the provider's discovery document says, asking the
// provider the first time and after a failure, once the retry delay has passed.
func (a *Authenticator) discover() (*provider, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.provider != nil {
		return a.provider, nil
	}
	if a.failure != nil && time.Since(a.lastFailure) < a.retry {
		return nil, a.failure
	}

	ctx, cancel := context.WithTimeout(oidc.ClientContext(context.Background(), a.client), discoveryTimeout)
	defer cancel()

	p, err := oidc.NewProvider(ctx, a.issuer)
	if err != nil {
		a.failure, a.lastFailure = err, time.Now()
		return nil, err
	}

	endpoint := p.Endpoint()
	if a.clientSecret == "" {
		// A public client has nothing to authenticate with: send who it is in
		// the request instead of trying a Basic header with an empty secret.
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	}

	a.provider = &provider{
		oauth: &oauth2.Config{
			ClientID:     a.clientID,
			ClientSecret: a.clientSecret,
			Endpoint:     endpoint,
			RedirectURL:  a.redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
		verifier: p.Verifier(&oidc.Config{ClientID: a.clientID}),
	}
	a.failure = nil
	return a.provider, nil
}

// Mount adds the sign-in routes to app, and requires a session for all
// routes added after it. Routes it doesn't own, such as an unknown /auth/ path,
// need one as well.
func (a *Authenticator) Mount(app *fiber.App) {
	app.Get(loginPath, a.login)
	app.Get(callbackPath, a.callback)
	app.Post(logoutPath, a.logout)
	app.Get(signedOutPath, a.signedOut)
	app.Use(a.require)
}

type userKey struct{}

// UserFrom returns the person who made the request, or nil when there isn't
// one: when the request didn't go through [Authenticator.Mount]'s check.
func UserFrom(c fiber.Ctx) *User {
	return fiber.Locals[*User](c, userKey{})
}

// require lets requests with a session or an API token through, and sends
// everyone else to sign in.
func (a *Authenticator) require(c fiber.Ctx) error {
	if token, ok := bearerToken(c); ok {
		user, err := a.tokenUser(token)
		if err != nil {
			return err
		}
		if user == nil {
			return fiber.NewError(fiber.StatusUnauthorized, "invalid API token")
		}
		if !tokenMayRead(c) {
			return fiber.NewError(fiber.StatusForbidden,
				"API tokens can only read traffic: they can't change anything, download backups or read credentials")
		}
		c.Locals(userKey{}, user)
		return c.Next()
	}

	user, err := a.currentUser(c)
	if err != nil {
		// Not knowing who it is has to mean no, not yes.
		return err
	}
	if user == nil {
		return a.deny(c)
	}

	c.Locals(userKey{}, user)
	return c.Next()
}

// deny answers a request without a session. Scripts and the web UI's own
// requests get a 401 they can act on; a browser opening a page is sent to
// sign in and then back to the page.
func (a *Authenticator) deny(c fiber.Ctx) error {
	isPage := c.Method() == fiber.MethodGet || c.Method() == fiber.MethodHead
	if !isPage || strings.HasPrefix(c.Path(), "/api/") || c.Path() == "/metrics" {
		return fiber.NewError(fiber.StatusUnauthorized, "authentication required")
	}
	return c.Redirect().Status(fiber.StatusFound).To(loginURL(c.OriginalURL()))
}

// currentUser returns the person the request's session cookie belongs to, or
// nil when there is no valid session.
func (a *Authenticator) currentUser(c fiber.Ctx) (*User, error) {
	token := c.Cookies(sessionCookie)
	if token == "" || len(token) > maxTokenLength {
		return nil, nil
	}

	record, err := storage.GetSession(a.db, hashToken(token), time.Now())
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &User{Subject: record.Subject, Name: record.Name, Email: record.Email}, nil
}

// startSession stores a session for user, and sets the cookie that holds its
// token.
func (a *Authenticator) startSession(c fiber.Ctx, user User) error {
	token, err := newToken()
	if err != nil {
		return err
	}

	now := time.Now()
	expires := now.Add(sessionLifetime)
	err = storage.CreateSession(a.db, hashToken(token), storage.SessionRecord{
		Subject:   user.Subject,
		Name:      user.Name,
		Email:     user.Email,
		CreatedAt: now,
		ExpiresAt: expires,
	})
	if err != nil {
		return err
	}

	a.setCookie(c, sessionCookie, token, "/", expires)
	return nil
}

// endSession deletes the session the request's cookie holds, if any.
func (a *Authenticator) endSession(c fiber.Ctx) error {
	token := c.Cookies(sessionCookie)
	if token == "" || len(token) > maxTokenLength {
		return nil
	}
	return storage.DeleteSession(a.db, hashToken(token))
}

// setCookie sets a cookie that scripts can't read and other sites can't make
// requests with, other than by sending people here. A zero expiry deletes it.
func (a *Authenticator) setCookie(c fiber.Ctx, name, value, path string, expires time.Time) {
	cookie := &fiber.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HTTPOnly: true,
		Secure:   a.secure,
		// Lax rather than Strict, as the provider sends people back with a
		// request that comes from another site, which has to carry the login
		// cookie.
		SameSite: fiber.CookieSameSiteLaxMode,
	}
	if expires.IsZero() {
		cookie.Value = ""
		cookie.Expires = time.Unix(0, 0)
		cookie.MaxAge = -1
	} else {
		cookie.Expires = expires
		cookie.MaxAge = int(time.Until(expires).Seconds())
	}
	c.Cookie(cookie)
}

// maxTokenLength is far above the length of a token (43), so that nothing
// worth hashing is turned away, and nothing absurd is looked up.
const maxTokenLength = 128

// newToken returns 256 random bits, as URL-safe text.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is what the database stores of a session token. The token is
// random, so a fast hash is as good as a slow one.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// loginURL is the address that starts signing in, and then returns to next.
func loginURL(next string) string {
	return loginPath + "?next=" + url.QueryEscape(next)
}
