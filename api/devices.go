package api

import (
	"database/sql"
	"log"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

const maxDeviceNameLength = 64

// parseDeviceName trims a name and checks it is 1 to 64 printable characters.
func parseDeviceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", badRequest("name must not be empty; DELETE the name to remove it")
	}
	if utf8.RuneCountInString(name) > maxDeviceNameLength {
		return "", badRequest("name must be at most 64 characters")
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		return "", badRequest("name must not contain control characters")
	}
	return name, nil
}

func parseDeviceMAC(c fiber.Ctx) (*common.MACAddress, error) {
	mac, err := common.ParseMACAddress(c.Params("mac"))
	if err != nil {
		return nil, badRequest("invalid mac address: " + c.Params("mac"))
	}
	return mac, nil
}

// registerDeviceRoutes adds the endpoints for naming devices.
func registerDeviceRoutes(app *fiber.App, db *sql.DB) {
	type nameBody struct {
		Name string `json:"name"`
	}

	app.Put("/api/devices/:mac/name", func(c fiber.Ctx) error {
		mac, err := parseDeviceMAC(c)
		if err != nil {
			return err
		}

		body := new(nameBody)
		if err := c.Bind().Body(body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}

		name, err := parseDeviceName(body.Name)
		if err != nil {
			return err
		}

		if err := storage.SetDeviceName(db, mac.HardwareAddr, name); err != nil {
			return err
		}
		log.Printf("Named device %s %q", mac, name)

		return c.JSON(DeviceNameDTO{MACAddress: mac.String(), Name: name})
	})

	app.Delete("/api/devices/:mac/name", func(c fiber.Ctx) error {
		mac, err := parseDeviceMAC(c)
		if err != nil {
			return err
		}

		if err := storage.DeleteDeviceName(db, mac.HardwareAddr); err != nil {
			return err
		}
		log.Printf("Removed the name of device %s", mac)

		return c.SendStatus(fiber.StatusNoContent)
	})
}
