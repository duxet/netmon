package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

const (
	telegramToken = "123456789:AAHsecretTokenValue1234567890abcdefgh"
	telegramURL   = "telegram://" + telegramToken + "@telegram?chats=@mychan"
)

func TestInspect(t *testing.T) {
	for _, tc := range []struct {
		url, scheme, masked, secret string
	}{
		{telegramURL, "telegram", "telegram://REDACTED@telegram?chats=%40mychan", "AAHsecret"},
		{"ntfy://:tk_supersecrettoken99@ntfy.sh/home-net", "ntfy", "ntfy://ntfy.sh/home-net", "tk_supersecret"},
		{"discord://discordsecrettoken@123456", "discord", "discord://REDACTED@123456", "discordsecret"},
		// The "generic+" shortcut is the webhook service too.
		{"generic+https://example.com/hook", "generic", "", ""},
	} {
		service, err := Inspect(tc.url)
		if err != nil {
			t.Errorf("Inspect(%s): %v", tc.url, err)
			continue
		}
		if service.Scheme != tc.scheme {
			t.Errorf("Inspect(%s).Scheme = %q, want %q", tc.url, service.Scheme, tc.scheme)
		}
		if tc.masked != "" && service.MaskedURL != tc.masked {
			t.Errorf("Inspect(%s).MaskedURL = %q, want %q", tc.url, service.MaskedURL, tc.masked)
		}
		if service.MaskedURL == "" || (tc.secret != "" && strings.Contains(service.MaskedURL, tc.secret)) {
			t.Errorf("Inspect(%s).MaskedURL = %q, want it without the secret", tc.url, service.MaskedURL)
		}
	}
}

func TestInspectRejectsInvalidURLs(t *testing.T) {
	for _, url := range []string{
		"",
		"   ",
		"nope://example.com",
		"telegram://123456789:AAHsecretTokenValue@telegram", // no chats
		"telegram://123456789:AAH secretTokenValue@telegram?chats=@x",
		"https://example.com/hook",
	} {
		_, err := Inspect(url)
		if !errors.Is(err, ErrInvalidURL) {
			t.Errorf("Inspect(%q) = %v, want ErrInvalidURL", url, err)
			continue
		}
		if strings.Contains(err.Error(), "secretTokenValue") {
			t.Errorf("Inspect(%q) error %q reveals the token", url, err)
		}
	}
}

// webhook records the requests a test server gets.
type webhook struct {
	server *httptest.Server
	method string
	body   map[string]any
	status int
}

func newWebhook(t *testing.T) *webhook {
	t.Helper()
	w := &webhook{status: http.StatusOK}
	w.server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.method = r.Method
		data, _ := io.ReadAll(r.Body)
		w.body = nil
		if err := json.Unmarshal(data, &w.body); err != nil {
			t.Errorf("webhook body %q is not JSON: %v", data, err)
		}
		rw.WriteHeader(w.status)
	}))
	t.Cleanup(w.server.Close)
	return w
}

// url is the service URL of the webhook, with a JSON payload.
func (w *webhook) url() string {
	return "generic://" + strings.TrimPrefix(w.server.URL, "http://") + "/hook?template=json&disabletls=yes"
}

func TestSendToWebhook(t *testing.T) {
	w := newWebhook(t)

	err := Send(context.Background(), w.url(), Message{
		Title:    "Large download on tv.lan",
		Body:     "tv.lan downloaded 6.2 GB in the last hour.",
		Severity: Warning,
		Fields: map[string]string{
			"alert_id": "42",
			// Named like options of the webhook service, these would change
			// the request instead of being sent, so they are dropped.
			"method":   "DELETE",
			"Template": "nope",
			"message":  "not the body",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if w.method != http.MethodPost {
		t.Errorf("method = %s, want POST", w.method)
	}
	want := map[string]any{
		"title":    "Large download on tv.lan", // no emoji: the level says it
		"message":  "tv.lan downloaded 6.2 GB in the last hour.",
		"level":    "Warning",
		"alert_id": "42",
	}
	if len(w.body) != len(want) {
		t.Errorf("payload = %v, want %v", w.body, want)
	}
	for key, value := range want {
		if w.body[key] != value {
			t.Errorf("payload[%q] = %v, want %v", key, w.body[key], value)
		}
	}
}

func TestSendReportsFailures(t *testing.T) {
	w := newWebhook(t)
	w.status = http.StatusInternalServerError

	if err := Send(context.Background(), w.url(), Message{Body: "hello"}); err == nil || errors.Is(err, ErrInvalidURL) {
		t.Errorf("Send to a failing webhook = %v, want a send error", err)
	}
	if err := Send(context.Background(), "nope://x", Message{Body: "hello"}); !errors.Is(err, ErrInvalidURL) {
		t.Errorf("Send to an invalid URL = %v, want ErrInvalidURL", err)
	}
}

// Other services get the title, with the severity's emoji, and the level:
// every service accepts these, while unknown parameters make sends fail.
func TestSendParamsForOtherServices(t *testing.T) {
	params := sendParams("telegram", Message{
		Title:    "Disk full",
		Severity: Critical,
		Fields:   map[string]string{"alert_id": "42"},
	})

	keys := slices.Sorted(func(yield func(string) bool) {
		for key := range params {
			if !yield(key) {
				return
			}
		}
	})
	if !slices.Equal(keys, []string{"level", "title"}) {
		t.Errorf("params = %v, want only level and title", params)
	}
	if params["title"] != "🚨 Disk full" || params["level"] != "Error" {
		t.Errorf("params = %v, want the critical emoji and level", params)
	}
}

func TestRedact(t *testing.T) {
	for _, tc := range []struct {
		message, url string
		secrets      []string
	}{
		{
			// A request URL with the bot token in its path.
			`Post "https://api.telegram.org/bot` + telegramToken + `/sendMessage": dial tcp: timeout`,
			telegramURL,
			[]string{"AAHsecretTokenValue"},
		},
		{
			// A URL that doesn't parse is quoted whole.
			`parse "smtp://user:pa ss@host": invalid character`,
			"smtp://user:pa ss@host",
			[]string{"pa ss"},
		},
		{
			// Query values, decoded, and the words of a header value.
			"401 for token tk_queryToken99 with key Bearer h3aderS3cretValue",
			"generic://example.com/hook?token=tk_queryToken99&@Authorization=Bearer+h3aderS3cretValue",
			[]string{"tk_queryToken99", "h3aderS3cretValue"},
		},
		{
			// Path segments, such as a Gotify app token.
			"gotify returned 401 for AppTokenValue.123",
			"gotify://gotify.lan/AppTokenValue.123",
			[]string{"AppTokenValue"},
		},
	} {
		got := redact(tc.message, tc.url)
		for _, secret := range tc.secrets {
			if strings.Contains(got, secret) {
				t.Errorf("redact(%q) = %q, still contains %q", tc.message, got, secret)
			}
		}
		if !strings.Contains(got, redacted) {
			t.Errorf("redact(%q) = %q, want %s in it", tc.message, got, redacted)
		}
	}

	// Short parts of the URL, such as "telegram" or a port, stay readable.
	if got := redact("telegram: no channels defined", "telegram://123:abc@telegram"); got != "telegram: no channels defined" {
		t.Errorf("redact changed a message without secrets to %q", got)
	}
}

func TestServices(t *testing.T) {
	services := Services()
	if len(services) < 30 {
		t.Fatalf("%d services, want all of shoutrrr's", len(services))
	}

	if first := services[0]; first.Scheme != "telegram" || first.ExampleURL == "" ||
		first.DocsURL != "https://shoutrrr.nickfedor.com/latest/services/chat/telegram/" {
		t.Errorf("first service = %+v, want Telegram with an example and its docs", first)
	}

	seen := map[string]bool{}
	for _, service := range services {
		if seen[service.Scheme] {
			t.Errorf("%s listed twice", service.Scheme)
		}
		seen[service.Scheme] = true
		if !strings.HasPrefix(service.DocsURL, docsBaseURL) || strings.HasSuffix(service.DocsURL, "overview/") {
			t.Errorf("%s docs = %q, want its own page", service.Scheme, service.DocsURL)
		}
		// Example URLs only need their placeholders filled in.
		if service.ExampleURL != "" && !strings.HasPrefix(service.ExampleURL, service.Scheme+"://") {
			t.Errorf("%s example %q has another scheme", service.Scheme, service.ExampleURL)
		}
	}
	for _, scheme := range []string{"ntfy", "smtp", "generic", "mqtt"} {
		if !seen[scheme] {
			t.Errorf("%s not listed", scheme)
		}
	}
	if seen["logger"] {
		t.Error("the logger service is listed")
	}

	if got := DocsURL("nope"); got != docsBaseURL+"overview/" {
		t.Errorf("DocsURL(nope) = %q, want the overview", got)
	}
}
