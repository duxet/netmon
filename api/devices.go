package api

import (
	"database/sql"
	"log/slog"
	"net/netip"
	"slices"
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

// DeviceIcons are the icons a device can be shown with, for the kind of
// device it is. The web UI has a picture for each, in the same order.
var DeviceIcons = []string{
	"desktop", "laptop", "tablet", "smartphone", "watch",
	"tv", "media_player", "game_console", "speaker",
	"router", "access_point", "switch", "server", "nas",
	"printer", "camera", "light", "plug", "thermostat", "appliance",
	"iot", "phone", "car",
}

// parseDeviceIcon checks an icon is one of DeviceIcons.
func parseDeviceIcon(icon string) (string, error) {
	if !slices.Contains(DeviceIcons, icon) {
		return "", badRequest("icon must be one of " + strings.Join(DeviceIcons, ", ") + "; DELETE the icon to remove it")
	}
	return icon, nil
}

func parseDeviceMAC(c fiber.Ctx) (*common.MACAddress, error) {
	mac, err := common.ParseMACAddress(c.Params("mac"))
	if err != nil {
		return nil, badRequest("invalid mac address: " + c.Params("mac"))
	}
	return mac, nil
}

// registerDeviceRoutes adds the endpoints for listing devices, and for
// naming them and picking their icons.
func registerDeviceRoutes(app *fiber.App, db *sql.DB, hostnames *lookup.Hostnames) {
	type nameBody struct {
		Name string `json:"name"`
	}
	type iconBody struct {
		Icon string `json:"icon"`
	}

	// Every device ever seen, most recently seen first.
	app.Get("/api/devices", func(c fiber.Ctx) error {
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
				Icon:       record.Icon,
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
		slog.Info("Named device", "mac", mac, "name", name)

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
		slog.Info("Removed the name of device", "mac", mac)

		return c.SendStatus(fiber.StatusNoContent)
	})

	app.Put("/api/devices/:mac/icon", func(c fiber.Ctx) error {
		mac, err := parseDeviceMAC(c)
		if err != nil {
			return err
		}

		body := new(iconBody)
		if err := c.Bind().Body(body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}

		icon, err := parseDeviceIcon(body.Icon)
		if err != nil {
			return err
		}

		if err := storage.SetDeviceIcon(db, mac.HardwareAddr, icon); err != nil {
			return err
		}
		slog.Info("Gave device an icon", "mac", mac, "icon", icon)

		return c.JSON(DeviceIconDTO{MACAddress: mac.String(), Icon: icon})
	})

	app.Delete("/api/devices/:mac/icon", func(c fiber.Ctx) error {
		mac, err := parseDeviceMAC(c)
		if err != nil {
			return err
		}

		if err := storage.DeleteDeviceIcon(db, mac.HardwareAddr); err != nil {
			return err
		}
		slog.Info("Removed the icon of device", "mac", mac)

		return c.SendStatus(fiber.StatusNoContent)
	})
}
