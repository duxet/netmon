package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/duxet/netmon/auth"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

// TokenDTO is a read-only API token, without the token itself.
type TokenDTO struct {
	ID   int64
	Name string
	// Prefix is the token's start, to tell it apart.
	Prefix     string
	CreatedBy  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// NewTokenDTO is a token that was just made, with the token, which isn't
// shown again.
type NewTokenDTO struct {
	TokenDTO
	Token string
}

func toTokenDTO(t storage.TokenRecord) TokenDTO {
	return TokenDTO{ID: t.ID, Name: t.Name, Prefix: t.Prefix, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt}
}

// registerTokenRoutes adds the endpoints that manage API tokens.
func registerTokenRoutes(app *fiber.App, db *sql.DB) {
	app.Get("/api/tokens", func(c fiber.Ctx) error {
		records, err := storage.ListTokens(db)
		if err != nil {
			return err
		}
		tokens := make([]TokenDTO, 0, len(records))
		for _, record := range records {
			tokens = append(tokens, toTokenDTO(record))
		}
		return c.JSON(tokens)
	})

	app.Post("/api/tokens", func(c fiber.Ctx) error {
		var body struct {
			Name string `json:"name"`
		}
		if err := c.Bind().Body(&body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}
		name, err := parseName(body.Name, "name must not be empty, such as Grafana")
		if err != nil {
			return err
		}

		createdBy := "netmon"
		if user := auth.UserFrom(c); user != nil {
			createdBy = user.Name
			if createdBy == "" {
				createdBy = user.Email
			}
		}

		token, hash, prefix := auth.NewToken()
		record, err := storage.CreateToken(db, storage.TokenRecord{
			Name: name, Prefix: prefix, CreatedBy: createdBy, CreatedAt: time.Now(),
		}, hash)
		if err != nil {
			return err
		}
		slog.Info("Made an API token", "id", record.ID, "name", record.Name, "by", createdBy)

		return c.Status(fiber.StatusCreated).JSON(NewTokenDTO{TokenDTO: toTokenDTO(record), Token: token})
	})

	app.Delete("/api/tokens/:id", func(c fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || id <= 0 {
			return badRequest("invalid token id: " + c.Params("id"))
		}
		if err := storage.DeleteToken(db, id); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return fiber.NewError(fiber.StatusNotFound, "API token not found")
			}
			return err
		}
		slog.Info("Revoked an API token", "id", id)
		return c.SendStatus(fiber.StatusNoContent)
	})
}
