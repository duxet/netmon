package api

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func requestWith(t *testing.T, app *fiber.App, method, path, body string, headers map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func TestAPITokens(t *testing.T) {
	app, db := newSignInApp(t)
	cookie := map[string]string{"Cookie": signedIn(t, db, "ada", "Ada Lovelace", "ada@example.com")}

	status, body := requestWith(t, app, "POST", "/api/tokens", `{"name": "Grafana"}`, cookie)
	if status != fiber.StatusCreated {
		t.Fatalf("POST /api/tokens = %d %s", status, body)
	}
	var created NewTokenDTO
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Token, "netmon_") || !strings.HasPrefix(created.Token, created.Prefix) || created.CreatedBy != "Ada Lovelace" {
		t.Errorf("token = %+v", created)
	}
	bearer := map[string]string{"Authorization": "Bearer " + created.Token}

	for _, test := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/stats", "", fiber.StatusOK},
		{"GET", "/api/clients", "", fiber.StatusOK},
		{"GET", "/metrics", "", fiber.StatusOK},
		{"GET", "/api/settings-export", "", fiber.StatusOK},
		{"GET", "/api/settings-export?secrets=true", "", fiber.StatusForbidden},
		{"GET", "/api/backup", "", fiber.StatusForbidden},
		{"POST", "/api/networks", `{"name": "IoT", "prefixes": ["10.0.20.0/24"]}`, fiber.StatusForbidden},
		{"POST", "/api/tokens", `{"name": "More"}`, fiber.StatusForbidden},
		{"DELETE", "/api/tokens/1", "", fiber.StatusForbidden},
		{"GET", "/", "", fiber.StatusForbidden},
	} {
		if status, body := requestWith(t, app, test.method, test.path, test.body, bearer); status != test.want {
			t.Errorf("%s %s with the token = %d %s, want %d", test.method, test.path, status, body, test.want)
		}
	}

	var session SessionDTO
	_, body = requestWith(t, app, "GET", "/api/session", "", bearer)
	_ = json.Unmarshal([]byte(body), &session)
	if session.User == nil || session.User.Name == nil || *session.User.Name != "Grafana" {
		t.Errorf("session with the token = %s, want the token's name", body)
	}

	if status, _ := requestWith(t, app, "GET", "/api/stats", "", map[string]string{"Authorization": "Bearer netmon_nope"}); status != fiber.StatusUnauthorized {
		t.Errorf("GET /api/stats with a wrong token = %d, want 401", status)
	}

	var tokens []TokenDTO
	_, body = requestWith(t, app, "GET", "/api/tokens", "", cookie)
	if err := json.Unmarshal([]byte(body), &tokens); err != nil || len(tokens) != 1 || tokens[0].LastUsedAt == nil || strings.Contains(body, created.Token) {
		t.Errorf("tokens = %s, want the one, used, without its secret", body)
	}

	if status, _ := requestWith(t, app, "DELETE", "/api/tokens/"+itoa(uint16(created.ID)), "", cookie); status != fiber.StatusNoContent {
		t.Errorf("DELETE the token = %d, want 204", status)
	}
	if status, _ := requestWith(t, app, "GET", "/api/stats", "", bearer); status != fiber.StatusUnauthorized {
		t.Errorf("GET /api/stats with a revoked token = %d, want 401", status)
	}
}

func TestAPITokenNames(t *testing.T) {
	app, _ := newTestApp(t)
	if status, _ := do(t, app, "POST", "/api/tokens", `{"name": "  "}`); status != fiber.StatusBadRequest {
		t.Errorf("POST /api/tokens without a name = %d, want 400", status)
	}
	if status, body := do(t, app, "POST", "/api/tokens", `{"name": "Home Assistant"}`); status != fiber.StatusCreated || !strings.Contains(body, `"CreatedBy":"netmon"`) {
		t.Errorf("POST /api/tokens without sign-in = %d %s, want it made by netmon", status, body)
	}
}
