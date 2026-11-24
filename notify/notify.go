// Package notify sends notifications through shoutrrr service URLs
// (https://shoutrrr.nickfedor.com), such as telegram://… or ntfy://…. A URL
// holds everything needed to reach a service, credentials included, so it is
// a secret: it is never shown or logged, only its masked form.
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/nicholas-fedor/shoutrrr"
	"github.com/nicholas-fedor/shoutrrr/pkg/format"
	"github.com/nicholas-fedor/shoutrrr/pkg/router"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"
)

// Severity is how urgent a message is. Services that know priorities or
// colors (ntfy, Discord, Gotify, Pushover, …) show it; for the others the
// title starts with a matching emoji.
type Severity string

const (
	Info     Severity = "info"
	Warning  Severity = "warning"
	Critical Severity = "critical"
)

// Message is a notification.
type Message struct {
	Title string
	// Body is plain text.
	Body     string
	Severity Severity
	// Fields is structured data about the message, such as the alert it is
	// about. Only webhook (generic://) channels receive it, as extra fields
	// of their JSON payload: other services reject parameters they don't
	// know.
	Fields map[string]string
}

// sendTimeout bounds one attempt at sending a message.
const sendTimeout = 10 * time.Second

// webhookScheme is the scheme of the generic webhook service, also used as
// "generic+https://…".
const webhookScheme = "generic"

// ErrInvalidURL is wrapped by the errors of Inspect and Send for URLs that
// are not valid service URLs.
var ErrInvalidURL = errors.New("invalid service URL")

// Service is what Inspect learned about a service URL.
type Service struct {
	// Scheme identifies the service, e.g. "telegram", or "generic" for a
	// webhook.
	Scheme string
	// MaskedURL is the URL with its secrets replaced by "REDACTED", safe to
	// show and log.
	MaskedURL string
}

// Inspect checks that rawURL is a valid service URL, without sending
// anything. Errors wrap ErrInvalidURL and have the URL's secrets redacted.
func Inspect(rawURL string) (Service, error) {
	if strings.TrimSpace(rawURL) == "" {
		return Service{}, fmt.Errorf("%w: the URL is empty", ErrInvalidURL)
	}

	r, err := router.NewWithOptions(nil, types.SenderOptions{})
	if err != nil {
		return Service{}, err
	}

	scheme, _, err := r.ExtractServiceName(rawURL)
	if err != nil {
		return Service{}, invalidURL(err, rawURL)
	}

	service, err := r.Locate(rawURL)
	if err != nil {
		return Service{}, invalidURL(err, rawURL)
	}
	// Only some services (MQTT) hold resources, and nothing was sent.
	if closer, ok := service.(io.Closer); ok {
		_ = closer.Close()
	}

	masked := format.MaskedURL(format.GetServiceConfig(service)).String()
	// A URL whose query was all secrets ends in a bare "?".
	masked = strings.TrimSuffix(masked, "?")
	return Service{Scheme: scheme, MaskedURL: masked}, nil
}

// invalidURL wraps ErrInvalidURL, but not err: its message may hold secrets.
func invalidURL(err error, rawURL string) error {
	return fmt.Errorf("%w: %s", ErrInvalidURL, redact(err.Error(), rawURL))
}

// Send delivers msg to the service at rawURL, in one attempt of at most ten
// seconds. Errors have the URL's secrets redacted, so they can be shown and
// logged; those for invalid URLs wrap ErrInvalidURL.
func Send(ctx context.Context, rawURL string, msg Message) error {
	service, err := Inspect(rawURL)
	if err != nil {
		return err
	}

	params := sendParams(service.Scheme, msg)

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	if err := shoutrrr.SendContext(ctx, rawURL, msg.Body, &params); err != nil {
		return errors.New(redact(err.Error(), rawURL))
	}
	return nil
}

// sendParams returns the shoutrrr params for msg: its title and level, which
// every service accepts (and ignores when it has no use for them), and for
// webhooks its fields.
func sendParams(scheme string, msg Message) types.Params {
	params := types.Params{}
	params.SetLevel(level(msg.Severity))

	if scheme != webhookScheme {
		if msg.Title != "" {
			params.SetTitle(emoji(msg.Severity) + " " + msg.Title)
		}
		return params
	}

	// Webhook payloads carry the severity as data, so the title stays plain.
	if msg.Title != "" {
		params.SetTitle(msg.Title)
	}
	fields := maps.Clone(msg.Fields)
	for key := range fields {
		// A field named like one of the service's options (method, template,
		// …) would reconfigure the request instead of being sent.
		if isWebhookOption(key) || key == types.TitleKey || key == types.MessageKey || key == types.LevelKey {
			delete(fields, key)
		}
	}
	maps.Copy(params, fields)
	return params
}

// isWebhookOption reports whether key names an option of the generic
// webhook service, in any case.
func isWebhookOption(key string) bool {
	for _, option := range webhookOptions() {
		if strings.EqualFold(option, key) {
			return true
		}
	}
	return false
}

// webhookOptions are the query keys the generic webhook service reads.
var webhookOptions = sync.OnceValue(func() []string {
	var r router.ServiceRouter
	service, err := r.NewService(webhookScheme)
	if err != nil {
		return nil
	}
	resolver := format.NewPropKeyResolver(format.GetServiceConfig(service))
	return resolver.QueryFields()
})

func level(severity Severity) types.MessageLevel {
	switch severity {
	case Critical:
		return types.Error
	case Warning:
		return types.Warning
	default:
		return types.Info
	}
}

func emoji(severity Severity) string {
	switch severity {
	case Critical:
		return "🚨"
	case Warning:
		return "⚠️"
	default:
		return "ℹ️"
	}
}
