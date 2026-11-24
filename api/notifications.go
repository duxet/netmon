package api

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/duxet/netmon/notify"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

// channelBody is the request body for creating and updating a channel.
// Fields left out of an update keep their value; an empty URL does too, so
// a channel can be renamed or toggled without sending its secrets again.
type channelBody struct {
	Name    *string `json:"name"`
	URL     *string `json:"url"`
	Enabled *bool   `json:"enabled"`
}

// testBody is the request body for testing a channel that isn't saved yet.
type testBody struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func parseChannelID(c fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, badRequest("invalid channel id: " + c.Params("id"))
	}
	return id, nil
}

func parseChannelName(name string) (string, error) {
	return parseName(name, "name must not be empty")
}

// parseServiceURL trims a service URL and checks it, returning its scheme.
func parseServiceURL(url string) (string, notify.Service, error) {
	url = strings.TrimSpace(url)
	service, err := notify.Inspect(url)
	if err != nil {
		return "", notify.Service{}, badRequest(err.Error())
	}
	return url, service, nil
}

// channelNotFound turns storage.ErrNotFound into a 404.
func channelNotFound(err error) error {
	if errors.Is(err, storage.ErrNotFound) {
		return fiber.NewError(fiber.StatusNotFound, "notification channel not found")
	}
	return err
}

func toChannelDTO(record storage.ChannelRecord) NotificationChannelDTO {
	// The URL was valid when it was saved. Should a newer shoutrrr reject it,
	// the channel is still listed, with nothing of its URL but the scheme.
	masked := record.Service + "://REDACTED"
	if service, err := notify.Inspect(record.URL); err == nil {
		masked = service.MaskedURL
	}

	return NotificationChannelDTO{
		ID:          record.ID,
		Name:        record.Name,
		Service:     record.Service,
		MaskedURL:   masked,
		Enabled:     record.Enabled,
		UsedByRules: record.UsedByRules,
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
	}
}

// testMessage is sent to check that a channel works. name is the channel's,
// or empty for one that isn't saved yet.
func testMessage(name string) notify.Message {
	body := "This is a test of a new notification channel. If you can read it, netmon can reach you here."
	if name != "" {
		body = fmt.Sprintf("This is a test of the notification channel %q. If you can read it, netmon can reach you here.", name)
	}

	return notify.Message{
		Title:    "Test notification from netmon",
		Body:     body,
		Severity: notify.Info,
		Fields:   map[string]string{"event": "test", "payload_version": "1"},
	}
}

// sendTest sends a test message and answers 204, or 502 with the reason the
// service gave. Errors from notify have the URL's secrets redacted.
func sendTest(c fiber.Ctx, url, name, what string) error {
	if err := notify.Send(c.Context(), url, testMessage(name)); err != nil {
		if errors.Is(err, notify.ErrInvalidURL) {
			return badRequest(err.Error())
		}
		log.Printf("Test notification %s failed: %v", what, err)
		return fiber.NewError(fiber.StatusBadGateway, "sending the test message failed: "+err.Error())
	}
	log.Printf("Sent a test notification %s", what)
	return c.SendStatus(fiber.StatusNoContent)
}

// registerNotificationRoutes adds the endpoints for notification channels.
func registerNotificationRoutes(app *fiber.App, db *sql.DB) {
	app.Get("/api/notification-services", func(c fiber.Ctx) error {
		services := notify.Services()
		dtos := make([]NotificationServiceDTO, 0, len(services))
		for _, s := range services {
			dtos = append(dtos, NotificationServiceDTO(s))
		}
		return c.JSON(dtos)
	})

	app.Get("/api/notification-channels", func(c fiber.Ctx) error {
		records, err := storage.ListChannels(db)
		if err != nil {
			return err
		}

		channels := make([]NotificationChannelDTO, 0, len(records))
		for _, record := range records {
			channels = append(channels, toChannelDTO(record))
		}
		return c.JSON(channels)
	})

	app.Post("/api/notification-channels", func(c fiber.Ctx) error {
		body := new(channelBody)
		if err := c.Bind().Body(body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}
		if body.Name == nil || body.URL == nil {
			return badRequest("name and url are required")
		}

		name, err := parseChannelName(*body.Name)
		if err != nil {
			return err
		}
		url, service, err := parseServiceURL(*body.URL)
		if err != nil {
			return err
		}
		enabled := body.Enabled == nil || *body.Enabled

		record, err := storage.CreateChannel(db, storage.ChannelRecord{
			Name: name, Service: service.Scheme, URL: url, Enabled: enabled,
		}, time.Now())
		if err != nil {
			return err
		}
		log.Printf("Added notification channel %d (%s)", record.ID, record.Service)

		return c.Status(fiber.StatusCreated).JSON(toChannelDTO(record))
	})

	// Sends a test message to a URL from the form, before it is saved.
	app.Post("/api/notification-channels/test", func(c fiber.Ctx) error {
		body := new(testBody)
		if err := c.Bind().Body(body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}

		url, service, err := parseServiceURL(body.URL)
		if err != nil {
			return err
		}
		return sendTest(c, url, strings.TrimSpace(body.Name), "to a new "+service.Scheme+" channel")
	})

	app.Get("/api/notification-channels/:id", func(c fiber.Ctx) error {
		id, err := parseChannelID(c)
		if err != nil {
			return err
		}

		record, err := storage.GetChannel(db, id)
		if err != nil {
			return channelNotFound(err)
		}
		return c.JSON(toChannelDTO(record))
	})

	app.Put("/api/notification-channels/:id", func(c fiber.Ctx) error {
		id, err := parseChannelID(c)
		if err != nil {
			return err
		}

		body := new(channelBody)
		if err := c.Bind().Body(body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}

		record, err := storage.GetChannel(db, id)
		if err != nil {
			return channelNotFound(err)
		}

		if body.Name != nil {
			if record.Name, err = parseChannelName(*body.Name); err != nil {
				return err
			}
		}
		if body.URL != nil && strings.TrimSpace(*body.URL) != "" {
			url, service, err := parseServiceURL(*body.URL)
			if err != nil {
				return err
			}
			record.URL, record.Service = url, service.Scheme
		}
		if body.Enabled != nil {
			record.Enabled = *body.Enabled
		}

		record, err = storage.UpdateChannel(db, record, time.Now())
		if err != nil {
			return channelNotFound(err)
		}
		log.Printf("Updated notification channel %d (%s)", record.ID, record.Service)

		return c.JSON(toChannelDTO(record))
	})

	app.Delete("/api/notification-channels/:id", func(c fiber.Ctx) error {
		id, err := parseChannelID(c)
		if err != nil {
			return err
		}

		if err := storage.DeleteChannel(db, id); err != nil {
			return channelNotFound(err)
		}
		log.Printf("Removed notification channel %d", id)

		return c.SendStatus(fiber.StatusNoContent)
	})

	app.Post("/api/notification-channels/:id/test", func(c fiber.Ctx) error {
		id, err := parseChannelID(c)
		if err != nil {
			return err
		}

		record, err := storage.GetChannel(db, id)
		if err != nil {
			return channelNotFound(err)
		}
		return sendTest(c, record.URL, record.Name, fmt.Sprintf("through channel %d (%s)", record.ID, record.Service))
	})
}
