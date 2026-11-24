package api

import (
	"errors"
	"log"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
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
		log.Printf("%s %s failed: %v", c.Method(), c.OriginalURL(), err)
	}

	return c.Status(status).JSON(ErrorDTO{Error: message})
}

// parseFlowsFilter reads the optional mac and ip query parameters. An invalid
// value is a client error rather than a reason to return unfiltered data.
func parseFlowsFilter(c fiber.Ctx) (storage.FlowsFilter, error) {
	var filter storage.FlowsFilter

	if value := c.Query("mac"); value != "" {
		mac, err := common.ParseMACAddress(value)
		if err != nil {
			return filter, fiber.NewError(fiber.StatusBadRequest, "invalid mac address: "+value)
		}
		filter.MAC = mac
	}

	if value := c.Query("ip"); value != "" {
		ip, err := common.ParseIPAddress(value)
		if err != nil {
			return filter, fiber.NewError(fiber.StatusBadRequest, "invalid ip address: "+value)
		}
		filter.IP = ip
	}

	return filter, nil
}

// parseClientID accepts a client identified by either MAC or IP address.
func parseClientID(id string) (storage.FlowsFilter, error) {
	if mac, err := common.ParseMACAddress(id); err == nil {
		return storage.FlowsFilter{MAC: mac}, nil
	}

	if ip, err := common.ParseIPAddress(id); err == nil {
		return storage.FlowsFilter{IP: ip}, nil
	}

	return storage.FlowsFilter{}, fiber.NewError(fiber.StatusBadRequest, "client id must be a mac or ip address: "+id)
}
