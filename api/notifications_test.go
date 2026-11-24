package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// webhookSecret is in the URL of test channels, as a header. It must never
// show up in a response.
const webhookSecret = "s3cretWebhookToken"

// testWebhook is a webhook that records the JSON payloads it receives.
type testWebhook struct {
	server *httptest.Server

	mu       sync.Mutex
	payloads []map[string]string
	auth     []string
	status   int
}

func newTestWebhook(t *testing.T) *testWebhook {
	t.Helper()
	w := &testWebhook{status: http.StatusOK}
	w.server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var payload map[string]string
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Errorf("webhook payload %q: %v", data, err)
		}

		w.mu.Lock()
		defer w.mu.Unlock()
		w.payloads = append(w.payloads, payload)
		w.auth = append(w.auth, r.Header.Get("Authorization"))
		rw.WriteHeader(w.status)
	}))
	t.Cleanup(w.server.Close)
	return w
}

// url is the shoutrrr URL of the webhook, with a secret header.
func (w *testWebhook) url() string {
	host := strings.TrimPrefix(w.server.URL, "http://")
	return "generic://" + host + "/hook?template=json&disabletls=yes&@Authorization=Bearer+" + webhookSecret
}

func (w *testWebhook) received() []map[string]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]map[string]string(nil), w.payloads...)
}

func (w *testWebhook) setStatus(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = status
}

type channelResponse struct {
	ID        int64
	Name      string
	Service   string
	MaskedURL string
	Enabled   bool
}

// doJSON sends a request and checks its status and that the response
// reveals no secret, decoding the body into out if it isn't nil.
func doJSON(t *testing.T, app *fiber.App, method, path, body string, status int, out any) string {
	t.Helper()
	got, response := do(t, app, method, path, body)
	if got != status {
		t.Fatalf("%s %s = %d %s, want %d", method, path, got, response, status)
	}
	if strings.Contains(response, webhookSecret) {
		t.Fatalf("%s %s reveals the channel's secret: %s", method, path, response)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(response), out); err != nil {
			t.Fatalf("%s %s: %v in %s", method, path, err, response)
		}
	}
	return response
}

func TestNotificationServices(t *testing.T) {
	app, _ := newTestApp(t)

	var services []NotificationServiceDTO
	doJSON(t, app, "GET", "/api/notification-services", "", fiber.StatusOK, &services)
	if len(services) < 30 || services[0].Scheme != "telegram" || services[0].ExampleURL == "" || services[0].DocsURL == "" {
		t.Errorf("services = %+v, want all of them, Telegram first", services)
	}
}

func TestNotificationChannels(t *testing.T) {
	app, _ := newTestApp(t)
	webhook := newTestWebhook(t)

	var channels []channelResponse
	if body := doJSON(t, app, "GET", "/api/notification-channels", "", fiber.StatusOK, &channels); body != "[]" {
		t.Fatalf("channels = %s, want an empty list", body)
	}

	create, _ := json.Marshal(map[string]string{"name": " Home Assistant ", "url": " " + webhook.url() + " "})
	var created channelResponse
	doJSON(t, app, "POST", "/api/notification-channels", string(create), fiber.StatusCreated, &created)
	if created.ID == 0 || created.Name != "Home Assistant" || created.Service != "generic" || !created.Enabled ||
		!strings.HasPrefix(created.MaskedURL, "generic://") {
		t.Errorf("created = %+v", created)
	}
	path := fmt.Sprintf("/api/notification-channels/%d", created.ID)

	doJSON(t, app, "GET", "/api/notification-channels", "", fiber.StatusOK, &channels)
	if len(channels) != 1 || channels[0] != created {
		t.Errorf("channels = %+v, want the created one", channels)
	}
	var got channelResponse
	doJSON(t, app, "GET", path, "", fiber.StatusOK, &got)
	if got != created {
		t.Errorf("GET %s = %+v, want %+v", path, got, created)
	}

	// Renaming and disabling keep the URL, which isn't sent again.
	var updated channelResponse
	doJSON(t, app, "PUT", path, `{"name": "HA", "enabled": false}`, fiber.StatusOK, &updated)
	if updated.Name != "HA" || updated.Enabled || updated.MaskedURL != created.MaskedURL {
		t.Errorf("updated = %+v", updated)
	}
	doJSON(t, app, "PUT", path, `{"url": ""}`, fiber.StatusOK, &updated)

	// The stored URL still works: the test message reaches the webhook, with
	// its secret header.
	doJSON(t, app, "POST", path+"/test", "", fiber.StatusNoContent, nil)
	payloads := webhook.received()
	if len(payloads) != 1 || payloads[0]["event"] != "test" || payloads[0]["title"] != "Test notification from netmon" ||
		!strings.Contains(payloads[0]["message"], `"HA"`) || webhook.auth[0] != "Bearer "+webhookSecret {
		t.Errorf("webhook got %v with %v", payloads, webhook.auth)
	}

	// A failing service answers 502 with its reason.
	webhook.setStatus(http.StatusInternalServerError)
	body := doJSON(t, app, "POST", path+"/test", "", fiber.StatusBadGateway, nil)
	if msg := errorMessage(t, body); !strings.Contains(msg, "test message failed") {
		t.Errorf("error = %q, want why sending failed", msg)
	}

	doJSON(t, app, "DELETE", path, "", fiber.StatusNoContent, nil)
	doJSON(t, app, "GET", path, "", fiber.StatusNotFound, nil)
	doJSON(t, app, "PUT", path, `{"name": "x"}`, fiber.StatusNotFound, nil)
	doJSON(t, app, "DELETE", path, "", fiber.StatusNotFound, nil)
	doJSON(t, app, "POST", path+"/test", "", fiber.StatusNotFound, nil)
}

func TestTestUnsavedChannel(t *testing.T) {
	app, _ := newTestApp(t)
	webhook := newTestWebhook(t)

	body, _ := json.Marshal(map[string]string{"url": webhook.url()})
	doJSON(t, app, "POST", "/api/notification-channels/test", string(body), fiber.StatusNoContent, nil)
	if payloads := webhook.received(); len(payloads) != 1 || !strings.Contains(payloads[0]["message"], "new notification channel") {
		t.Errorf("webhook got %v", payloads)
	}

	// Nothing was saved.
	if body := doJSON(t, app, "GET", "/api/notification-channels", "", fiber.StatusOK, nil); body != "[]" {
		t.Errorf("channels = %s, want none", body)
	}
}

func TestNotificationChannelValidation(t *testing.T) {
	app, _ := newTestApp(t)
	webhook := newTestWebhook(t)
	valid, _ := json.Marshal(map[string]string{"name": "HA", "url": webhook.url()})

	var created channelResponse
	doJSON(t, app, "POST", "/api/notification-channels", string(valid), fiber.StatusCreated, &created)
	path := fmt.Sprintf("/api/notification-channels/%d", created.ID)

	for _, tc := range []struct {
		method, path, body string
	}{
		{"POST", "/api/notification-channels", `{"name": "x"}`},
		{"POST", "/api/notification-channels", `{"url": "ntfy://ntfy.sh/topic"}`},
		{"POST", "/api/notification-channels", `{"name": "  ", "url": "ntfy://ntfy.sh/topic"}`},
		{"POST", "/api/notification-channels", `{"name": "x", "url": "nope://example.com"}`},
		{"POST", "/api/notification-channels", `{"name": "x", "url": "telegram://123456789:s3cretWebhookToken@telegram"}`},
		{"POST", "/api/notification-channels", `{"name": `},
		{"POST", "/api/notification-channels/test", `{"url": ""}`},
		{"POST", "/api/notification-channels/test", `{"url": "https://example.com"}`},
		{"PUT", path, `{"url": "nope://example.com"}`},
		{"PUT", path, `{"name": ""}`},
		{"GET", "/api/notification-channels/abc", ""},
		{"DELETE", "/api/notification-channels/0", ""},
	} {
		body := doJSON(t, app, tc.method, tc.path, tc.body, fiber.StatusBadRequest, nil)
		if msg := errorMessage(t, body); msg == "" {
			t.Errorf("%s %s %s: empty error message", tc.method, tc.path, tc.body)
		}
	}

	// Failed updates changed nothing.
	var got channelResponse
	doJSON(t, app, "GET", path, "", fiber.StatusOK, &got)
	if got != created {
		t.Errorf("channel after failed updates = %+v, want %+v", got, created)
	}
}
