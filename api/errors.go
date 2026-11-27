package api

import (
	"errors"
	"log/slog"

	"github.com/gofiber/fiber/v3"
)

type ErrorDTO struct {
	Error string `json:"error"`
}

// errorHandler answers failed requests with a JSON body. Errors raised with
// fiber.NewError keep their status and message; anything else is logged and
// reported as a 500 without internal details.
func errorHandler(c fiber.Ctx, err error) error {
	status := fiber.StatusInternalServerError
	message := "internal server error"

	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		status = fiberErr.Code
		message = fiberErr.Message
	} else {
		slog.Error("Request failed", "method", c.Method(), "url", c.OriginalURL(), "err", err)
	}

	return c.Status(status).JSON(ErrorDTO{Error: message})
}

func badRequest(message string) error {
	return fiber.NewError(fiber.StatusBadRequest, message)
}
