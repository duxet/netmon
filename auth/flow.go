package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gofiber/fiber/v3"
	"golang.org/x/oauth2"
)

const (
	loginCookie = "netmon_login"
	// loginCookiePath limits the cookie to the pages that use it.
	loginCookiePath = "/auth"
	// loginLifetime is how long someone has to sign in at the provider.
	loginLifetime = 10 * time.Minute

	// maxNextLength keeps what is kept in the login cookie small.
	maxNextLength = 1024
)

// loginAttempt is what netmon has to remember between sending someone to the
// provider and their coming back, which the cookie of their browser holds.
//
// Nothing in it needs to be secret from the browser that holds it: what
// matters is that a browser comes back with the attempt it started. A
// callback that was made up by someone else, to sign a person in as them,
// has no cookie that goes with its state.
type loginAttempt struct {
	// State ties the callback to this attempt.
	State string `json:"s"`
	// Nonce ties the ID token to this attempt.
	Nonce string `json:"n"`
	// Verifier is the PKCE secret: the provider only gives the code to
	// whoever can show it.
	Verifier string `json:"v"`
	// Next is the page to go to once signed in.
	Next string `json:"r"`
}

func (l loginAttempt) encode() (string, error) {
	data, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// readLogin returns the attempt in the request's cookie, if there is a valid one.
func readLogin(c fiber.Ctx) (loginAttempt, bool) {
	data, err := base64.RawURLEncoding.DecodeString(c.Cookies(loginCookie))
	if err != nil {
		return loginAttempt{}, false
	}
	var attempt loginAttempt
	if err := json.Unmarshal(data, &attempt); err != nil {
		return loginAttempt{}, false
	}
	if attempt.State == "" || attempt.Nonce == "" || attempt.Verifier == "" {
		return loginAttempt{}, false
	}
	attempt.Next = safeNext(attempt.Next)
	return attempt, true
}

// safeNext returns next if it is a page of this site, and the front page
// otherwise, so that signing in can't be used to send people to other sites.
func safeNext(next string) string {
	if next == "" || len(next) > maxNextLength || next[0] != '/' || strings.HasPrefix(next, "//") {
		return "/"
	}
	// Backslashes count as slashes for browsers, and control characters have
	// no place in an address.
	if strings.ContainsFunc(next, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '\\' }) {
		return "/"
	}
	// The sign-in pages would only send people back to where they were.
	if next == "/auth" || strings.HasPrefix(next, "/auth/") || strings.HasPrefix(next, "/auth?") {
		return "/"
	}
	if u, err := url.Parse(next); err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return next
}

// login sends the browser to the provider.
func (a *Authenticator) login(c fiber.Ctx) error {
	next := safeNext(c.Query("next"))

	p, err := a.discover()
	if err != nil {
		log.Printf("Sign-in is unavailable, as the OpenID Connect provider can't be reached: %v", err)
		return a.page(c, fiber.StatusServiceUnavailable, "Sign-in is unavailable",
			"netmon can't reach the identity provider right now. Try again in a moment.", "Try again", loginURL(next))
	}

	verifier := oauth2.GenerateVerifier()
	state, err := newToken()
	if err != nil {
		return err
	}
	nonce, err := newToken()
	if err != nil {
		return err
	}
	attempt := loginAttempt{State: state, Nonce: nonce, Verifier: verifier, Next: next}
	value, err := attempt.encode()
	if err != nil {
		return err
	}
	a.setCookie(c, loginCookie, value, loginCookiePath, time.Now().Add(loginLifetime))

	return c.Redirect().Status(fiber.StatusFound).To(
		p.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)))
}

// callback is where the provider sends the browser back to, with a code that
// is exchanged for the proof of who signed in.
func (a *Authenticator) callback(c fiber.Ctx) error {
	attempt, ok := readLogin(c)
	// An attempt is good for one callback, whatever comes of it.
	a.setCookie(c, loginCookie, "", loginCookiePath, time.Time{})

	retry := loginURL("/")
	if ok {
		retry = loginURL(attempt.Next)
	}

	if code := c.Query("error"); code != "" {
		log.Printf("The OpenID Connect provider refused the sign-in: %q: %q", code, c.Query("error_description"))
		// What the provider says is not shown: anyone can send someone a
		// link to this page with a message of their choosing in it.
		return a.page(c, fiber.StatusForbidden, "Sign-in was refused",
			"The identity provider didn't let you in ("+errorCode(code)+").", "Try again", retry)
	}

	state := c.Query("state")
	code := c.Query("code")
	if !ok || code == "" || subtle.ConstantTimeCompare([]byte(state), []byte(attempt.State)) != 1 {
		return a.page(c, fiber.StatusBadRequest, "Sign-in expired",
			"This sign-in didn't start in this browser, or took too long. Start again.", "Sign in", retry)
	}

	p, err := a.discover()
	if err != nil {
		log.Printf("Sign-in is unavailable, as the OpenID Connect provider can't be reached: %v", err)
		return a.page(c, fiber.StatusServiceUnavailable, "Sign-in is unavailable",
			"netmon can't reach the identity provider right now. Try again in a moment.", "Try again", retry)
	}

	ctx, cancel := context.WithTimeout(oidc.ClientContext(context.Background(), a.client), discoveryTimeout)
	defer cancel()

	token, err := p.oauth.Exchange(ctx, code, oauth2.VerifierOption(attempt.Verifier))
	if err != nil {
		log.Printf("Exchanging the sign-in code failed: %v", err)
		return a.page(c, fiber.StatusBadGateway, "Sign-in failed",
			"The identity provider didn't accept the sign-in. Try again.", "Try again", retry)
	}

	user, err := a.verify(ctx, p, token, attempt.Nonce)
	if err != nil {
		log.Printf("The ID token was rejected: %v", err)
		return a.page(c, fiber.StatusUnauthorized, "Sign-in failed",
			"The identity provider's answer couldn't be trusted. Try again.", "Try again", retry)
	}

	// Whoever had this browser before is signed out of it.
	if err := a.endSession(c); err != nil {
		log.Printf("Ending the previous session failed: %v", err)
	}
	if err := a.startSession(c, *user); err != nil {
		return err
	}

	log.Printf("Signed in %s", user.Subject)
	return c.Redirect().Status(fiber.StatusFound).To(attempt.Next)
}

// verify checks the ID token that came with the access token: that the
// provider signed it, for netmon, for this sign-in attempt, and who it is for.
func (a *Authenticator) verify(ctx context.Context, p *provider, token *oauth2.Token, nonce string) (*User, error) {
	raw, _ := token.Extra("id_token").(string)
	if raw == "" {
		return nil, errors.New("the response has no ID token")
	}

	idToken, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		return nil, errors.New("the ID token is for another sign-in attempt")
	}
	if idToken.Subject == "" {
		return nil, errors.New("the ID token has no subject")
	}

	var claims struct {
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, err
	}

	name := claims.Name
	if name == "" {
		name = claims.PreferredUsername
	}
	return &User{Subject: idToken.Subject, Name: name, Email: claims.Email}, nil
}

// logout ends the session and answers 204. It only ends netmon's session: the
// person stays signed in at the provider, so signing in again takes a click.
func (a *Authenticator) logout(c fiber.Ctx) error {
	if err := a.endSession(c); err != nil {
		return err
	}
	a.setCookie(c, sessionCookie, "", "/", time.Time{})
	return c.SendStatus(fiber.StatusNoContent)
}

// signedOut is where the web UI goes after signing out: it can't go to a page
// that needs a session, or it would be sent straight back in.
func (a *Authenticator) signedOut(c fiber.Ctx) error {
	return a.page(c, fiber.StatusOK, "Signed out", "You have been signed out of netmon.", "Sign in", loginPath)
}

// errorCode returns code if it looks like an OAuth error code, such as
// access_denied, and a generic word otherwise.
func errorCode(code string) string {
	if len(code) > 40 || strings.ContainsFunc(code, func(r rune) bool { return (r < 'a' || r > 'z') && r != '_' }) {
		return "error"
	}
	return code
}
