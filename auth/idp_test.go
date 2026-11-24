package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// A fake OpenID Connect provider, just enough of one for netmon to sign people
// in with: discovery, keys, and a token endpoint that holds netmon to the
// protocol (PKCE, a client that proves who it is, a code that works once).

const (
	testClientID     = "netmon"
	testClientSecret = "s3cret"
	testKeyID        = "test-key"
)

var (
	keysOnce sync.Once
	keys     [2]*rsa.PrivateKey
)

// testKeys returns the provider's signing key, and another one that it doesn't
// publish, for tokens that must not be trusted.
func testKeys(t *testing.T) (published, other *rsa.PrivateKey) {
	t.Helper()
	keysOnce.Do(func() {
		for i := range keys {
			key, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				panic(err)
			}
			keys[i] = key
		}
	})
	return keys[0], keys[1]
}

type issuedCode struct {
	nonce       string
	challenge   string
	redirectURI string
}

type fakeIdP struct {
	t      *testing.T
	server *httptest.Server
	key    *rsa.PrivateKey
	other  *rsa.PrivateKey

	// clientSecret is what the client has to prove, or empty for a public client.
	clientSecret string

	mu    sync.Mutex
	codes map[string]issuedCode
	// down makes every request fail.
	down bool
	// signWith is the key ID tokens are signed with.
	signWith *rsa.PrivateKey
	// claims changes what the next ID tokens say.
	claims func(claims map[string]any)
	// omitIDToken leaves the ID token out of token responses.
	omitIDToken bool
	// tokenFails makes the token endpoint refuse every code.
	tokenFails bool
	// tokenRequests are the requests the token endpoint has had.
	tokenRequests []tokenRequest
}

type tokenRequest struct {
	form        url.Values
	basicID     string
	basicSecret string
	hasBasic    bool
}

func newFakeIdP(t *testing.T, clientSecret string) *fakeIdP {
	t.Helper()

	key, other := testKeys(t)
	idp := &fakeIdP{t: t, key: key, other: other, signWith: key, clientSecret: clientSecret, codes: map[string]issuedCode{}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", idp.discovery)
	mux.HandleFunc("GET /jwks", idp.jwks)
	mux.HandleFunc("POST /token", idp.token)
	idp.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idp.mu.Lock()
		down := idp.down
		idp.mu.Unlock()
		if down {
			http.Error(w, "down for maintenance", http.StatusServiceUnavailable)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(idp.server.Close)
	return idp
}

func (i *fakeIdP) issuer() string { return i.server.URL }

func (i *fakeIdP) set(f func(i *fakeIdP)) {
	i.mu.Lock()
	defer i.mu.Unlock()
	f(i)
}

func (i *fakeIdP) requests() []tokenRequest {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]tokenRequest(nil), i.tokenRequests...)
}

func (i *fakeIdP) discovery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                i.issuer(),
		"authorization_endpoint":                i.issuer() + "/authorize",
		"token_endpoint":                        i.issuer() + "/token",
		"jwks_uri":                              i.issuer() + "/jwks",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (i *fakeIdP) jwks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": testKeyID,
		"n": base64.RawURLEncoding.EncodeToString(i.key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(i.key.E)).Bytes()),
	}}})
}

// authorize does what the person does at the provider: it checks the request
// netmon redirected them with, lets them in, and returns the address they are
// sent back to.
func (i *fakeIdP) authorize(authURL, expectRedirect string) string {
	i.t.Helper()

	u, err := url.Parse(authURL)
	if err != nil {
		i.t.Fatalf("authorization URL %q: %v", authURL, err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != i.issuer()+"/authorize" {
		i.t.Fatalf("sent to %s, want the provider's authorization endpoint", got)
	}

	q := u.Query()
	for name, want := range map[string]string{
		"response_type":         "code",
		"client_id":             testClientID,
		"redirect_uri":          expectRedirect,
		"code_challenge_method": "S256",
	} {
		if got := q.Get(name); got != want {
			i.t.Errorf("authorization request: %s = %q, want %q", name, got, want)
		}
	}
	if scope := q.Get("scope"); scope != "openid profile email" {
		i.t.Errorf("authorization request: scope = %q, want openid profile email", scope)
	}
	for _, name := range []string{"state", "nonce", "code_challenge"} {
		if len(q.Get(name)) < 32 {
			i.t.Errorf("authorization request: %s = %q, want at least 32 characters", name, q.Get(name))
		}
	}

	code := randomText(i.t)
	i.mu.Lock()
	i.codes[code] = issuedCode{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri")}
	i.mu.Unlock()

	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		i.t.Fatal(err)
	}
	back.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
	return back.String()
}

func (i *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req := tokenRequest{form: r.PostForm}
	req.basicID, req.basicSecret, req.hasBasic = r.BasicAuth()

	i.mu.Lock()
	i.tokenRequests = append(i.tokenRequests, req)
	fails, omit, claimsFn, signWith := i.tokenFails, i.omitIDToken, i.claims, i.signWith
	issued, known := i.codes[r.PostForm.Get("code")]
	delete(i.codes, r.PostForm.Get("code"))
	i.mu.Unlock()

	invalid := func(why string) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "invalid_grant", "error_description": why})
	}

	// The client has to be who it says it is.
	if i.clientSecret != "" {
		id, secret, hasBasic := req.basicID, req.basicSecret, req.hasBasic
		if !hasBasic {
			id, secret = req.form.Get("client_id"), req.form.Get("client_secret")
		}
		if id != testClientID || secret != i.clientSecret {
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(w, map[string]any{"error": "invalid_client"})
			return
		}
	} else if req.hasBasic || req.form.Get("client_id") != testClientID {
		// A public client says who it is in the request, and has no secret to
		// put in a header.
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]any{"error": "invalid_client"})
		return
	}

	switch {
	case fails:
		invalid("refused")
		return
	case req.form.Get("grant_type") != "authorization_code":
		invalid("wrong grant type")
		return
	case !known:
		invalid("unknown or used code")
		return
	case req.form.Get("redirect_uri") != issued.redirectURI:
		invalid("redirect_uri doesn't match")
		return
	}
	// PKCE: only whoever started the sign-in can finish it.
	sum := sha256.Sum256([]byte(req.form.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != issued.challenge {
		invalid("code_verifier doesn't match code_challenge")
		return
	}

	now := time.Now()
	claims := map[string]any{
		"iss":                i.issuer(),
		"sub":                "user-1",
		"aud":                testClientID,
		"iat":                now.Unix(),
		"exp":                now.Add(5 * time.Minute).Unix(),
		"nonce":              issued.nonce,
		"name":               "Anna Kowalska",
		"preferred_username": "anna",
		"email":              "anna@example.com",
	}
	if claimsFn != nil {
		claimsFn(claims)
	}

	response := map[string]any{"access_token": "access-token", "token_type": "Bearer", "expires_in": 3600}
	if !omit {
		response["id_token"] = sign(i.t, signWith, claims)
	}
	writeJSON(w, response)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func randomText(t *testing.T) string {
	t.Helper()
	token, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// sign makes an RS256 JWT.
func sign(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()

	part := func(v any) string {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(data)
	}
	input := part(map[string]any{"alg": "RS256", "kid": testKeyID, "typ": "JWT"}) + "." + part(claims)

	sum := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// browser is a person's browser: it doesn't follow redirects by itself, so
// that tests see each step, and keeps the cookies the app sets.
type browser struct {
	t       *testing.T
	do      func(req *http.Request) *http.Response
	cookies map[string]*http.Cookie
}

func (b *browser) request(method, target string) *http.Response {
	b.t.Helper()

	req := httptest.NewRequest(method, target, nil)
	for _, cookie := range b.cookies {
		if strings.HasPrefix(req.URL.Path, cookie.Path) {
			req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
		}
	}

	resp := b.do(req)
	for _, cookie := range resp.Cookies() {
		if cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && cookie.Expires.Before(time.Now())) {
			delete(b.cookies, cookie.Name)
		} else {
			b.cookies[cookie.Name] = cookie
		}
	}
	return resp
}

func (b *browser) get(target string) *http.Response { return b.request(http.MethodGet, target) }
