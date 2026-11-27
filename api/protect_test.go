package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestHostRules(t *testing.T) {
	rules := HostRules{PublicURL: "https://netmon.example.com", Allowed: []string{"stats.example.org"}}
	for host, want := range map[string]bool{
		"192.168.1.1":        true,
		"[fd00::1]":          true,
		"::1":                true,
		"localhost":          true,
		"router":             true,
		"router.lan":         true,
		"nas.home.arpa":      true,
		"printer.local":      true,
		"ROUTER.LAN.":        true,
		"netmon.example.com": true,
		"stats.example.org":  true,
		"attacker.com":       false,
		"router.lan.evil.io": false,
		"example.com":        false,
		"lan":                true,
	} {
		if got := rules.allows(host); got != want {
			t.Errorf("allows(%q) = %v, want %v", host, got, want)
		}
	}

	if !(HostRules{Allowed: []string{"*"}}).allows("anything.example.net") {
		t.Error("* doesn't allow every host")
	}
}

func protectedRequest(t *testing.T, app *fiber.App, method, host string, headers map[string]string) int {
	t.Helper()
	req := httptest.NewRequest(method, "http://"+host+"/api/networks", strings.NewReader(`{"name": "IoT", "prefixes": ["10.0.20.0/24"]}`))
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestProtect(t *testing.T) {
	app, _ := newTestAppWith(t, Options{Hosts: func() HostRules { return HostRules{} }})

	// A page that pointed its own name at netmon.
	if status := protectedRequest(t, app, "GET", "attacker.com:2137", nil); status != fiber.StatusForbidden {
		t.Errorf("GET for attacker.com = %d, want 403", status)
	}
	if status := protectedRequest(t, app, "GET", "192.168.1.1:2137", nil); status != fiber.StatusOK {
		t.Errorf("GET for the address = %d, want 200", status)
	}

	for _, test := range []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"a script", nil, fiber.StatusCreated},
		{"the web UI", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://router.lan:2137"}, fiber.StatusConflict},
		{"another site's page", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, fiber.StatusForbidden},
		{"a page on another port", map[string]string{"Sec-Fetch-Site": "same-site"}, fiber.StatusForbidden},
		{"an older browser on the web UI", map[string]string{"Origin": "http://router.lan:2137"}, fiber.StatusConflict},
		{"an older browser on another site", map[string]string{"Origin": "https://evil.example"}, fiber.StatusForbidden},
		{"a sandboxed page", map[string]string{"Origin": "null"}, fiber.StatusForbidden},
	} {
		// The network is made once; after that, an allowed request conflicts.
		if status := protectedRequest(t, app, "POST", "router.lan:2137", test.headers); status != test.want {
			t.Errorf("POST from %s = %d, want %d", test.name, status, test.want)
		}
	}

	// Without CORS, other sites' pages can't ask to change anything either.
	req := httptest.NewRequest("OPTIONS", "http://router.lan:2137/api/networks/1", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "DELETE")
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if allowed := resp.Header.Get("Access-Control-Allow-Origin"); allowed != "" {
		t.Errorf("preflight from another site allowed %q, want no CORS", allowed)
	}
}
