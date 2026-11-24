package api

import (
	"database/sql"
	"log"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/lookup"
	"github.com/duxet/netmon/storage"
	"github.com/duxet/netmon/vendors"
	"github.com/gofiber/fiber/v3"
)

const maxNameLength = 64

// parseDeviceName trims a name and checks it is 1 to 64 printable characters.
func parseDeviceName(name string) (string, error) {
	return parseName(name, "name must not be empty; DELETE the name to remove it")
}

// parseName trims a name, of a device or a channel, and checks it is 1 to
// 64 printable characters. emptyMessage explains an empty one.
func parseName(name, emptyMessage string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", badRequest(emptyMessage)
	}
	if utf8.RuneCountInString(name) > maxNameLength {
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

// registerDeviceRoutes adds the endpoints for listing and naming devices.
func registerDeviceRoutes(app *fiber.App, db *sql.DB, hostnames *lookup.Hostnames) {
	type nameBody struct {
		Name string `json:"name"`
	}

	// Every device ever seen, most recently seen first.
	app.Get("/api/devices", func(c fiber.Ctx) error {
		log.Println("Returning list of devices")

		records, err := storage.GetDevices(db)
		if err != nil {
			return err
		}

		addrs := make([]netip.Addr, 0, len(records))
		for _, record := range records {
			if record.IPAddress.Addr != nil {
				addrs = append(addrs, *record.IPAddress.Addr)
			}
		}
		names := hostnames.LookupAll(c.Context(), addrs)

		matcher, err := storage.NetworkMatcher(db)
		if err != nil {
			return err
		}

		devices := make([]DeviceDTO, 0, len(records))
		for _, record := range records {
			var hostname *string
			if record.IPAddress.Addr != nil {
				hostname = names[record.IPAddress.Unmap()]
			}

			var vendor *string
			if v := vendors.Lookup(record.MACAddress.HardwareAddr); v != "" {
				vendor = &v
			}

			devices = append(devices, DeviceDTO{
				MACAddress: record.MACAddress,
				IPAddress:  record.IPAddress,
				Name:       record.Name,
				Hostname:   hostname,
				Vendor:     vendor,
				NetworkID:  networkIDOf(matcher, record.IPAddress),
				FirstSeen:  record.FirstSeen,
				LastSeen:   record.LastSeen,
			})
		}

		return c.JSON(devices)
	})

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
