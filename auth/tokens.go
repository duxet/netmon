package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

const (
	// tokenPrefix starts every API token, so that one is recognized when it
	// leaks into a log or a repository.
	tokenPrefix = "netmon_"

	// shownPrefix is how much of a token is kept to tell it apart.
	shownPrefix = len(tokenPrefix) + 4

	// tokenTouchInterval is how often the last use of a token is written at
	// most, rather than on every request.
	tokenTouchInterval = time.Minute
)

// NewToken makes an API token, and returns it, its hash, which is all that
// is stored, and its start, which tells it apart.
func NewToken() (token string, hash []byte, prefix string) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic(err)
	}
	token = tokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
	return token, hashToken(token), token[:shownPrefix]
}

// bearerToken returns the API token in the request's Authorization header.
func bearerToken(c fiber.Ctx) (string, bool) {
	header := c.Get(fiber.HeaderAuthorization)
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// tokenMayRead reports whether an API token may make the request: read
// traffic and settings, but not change anything, nor download a backup or
// settings with credentials, which hold secrets.
func tokenMayRead(c fiber.Ctx) bool {
	if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
		return false
	}
	switch c.Path() {
	case "/api/backup":
		return false
	case "/api/settings-export":
		return c.Query("secrets") != "true"
	}
	return strings.HasPrefix(c.Path(), "/api/") || c.Path() == "/metrics"
}

var lastTouched sync.Map // token ID -> time.Time

// tokenUser returns who a token stands for, or nil when it isn't one.
func (a *Authenticator) tokenUser(token string) (*User, error) {
	if !strings.HasPrefix(token, tokenPrefix) || len(token) > maxTokenLength {
		return nil, nil
	}
	record, err := storage.GetTokenByHash(a.db, hashToken(token))
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if last, ok := lastTouched.Load(record.ID); !ok || now.Sub(last.(time.Time)) >= tokenTouchInterval {
		lastTouched.Store(record.ID, now)
		// Not being able to note the use is no reason to refuse it.
		_ = storage.TouchToken(a.db, record.ID, now)
	}

	return &User{Subject: "token:" + record.Prefix, Name: record.Name, TokenID: record.ID}, nil
}
