package api

import (
	"time"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/unifi"
	"github.com/gofiber/fiber/v3"
)

// UniFiClientDTO is what the UniFi Network Application knows about a device.
type UniFiClientDTO struct {
	// Name is the alias given in UniFi, or the hostname the device gave over
	// DHCP; empty when UniFi has neither.
	Name string
	// Connection is WIRED, WIRELESS or VPN.
	Connection string
	// Uplink is the access point or switch the device is connected to, if
	// known.
	Uplink string
	// Connected tells whether it was connected when UniFi was last asked;
	// ConnectedAt is since when, and LastSeen when UniFi last had it.
	Connected   bool
	ConnectedAt *time.Time
	LastSeen    *time.Time
}

// UniFiStatusDTO is how reading clients from UniFi goes.
type UniFiStatusDTO struct {
	// Enabled tells whether UniFi is configured; the rest is empty when not.
	Enabled bool
	Site    string
	// LastSync is when the clients were last read; LastError why the last
	// attempt failed.
	LastSync  *time.Time
	LastError string
	// Connected is the number of clients connected at the last sync.
	Connected int
}

// unifiClientOf returns what UniFi knows about the device with mac, or nil.
func unifiClientOf(source *unifi.Source, mac common.MACAddress) *UniFiClientDTO {
	if source == nil || len(mac.HardwareAddr) == 0 {
		return nil
	}
	client, ok := source.Client(mac.String())
	if !ok {
		return nil
	}
	dto := &UniFiClientDTO{
		Name:       client.Name,
		Connection: client.Type,
		Uplink:     client.Uplink,
		Connected:  client.Connected,
	}
	if !client.LastSeen.IsZero() {
		dto.LastSeen = &client.LastSeen
	}
	if !client.ConnectedAt.IsZero() {
		dto.ConnectedAt = &client.ConnectedAt
	}
	return dto
}

// registerUniFiRoutes adds the endpoint that tells how syncing with UniFi goes.
func registerUniFiRoutes(app *fiber.App, source *unifi.Source) {
	app.Get("/api/unifi", func(c fiber.Ctx) error {
		status, ok := source.Status()
		if !ok {
			return c.JSON(UniFiStatusDTO{})
		}
		dto := UniFiStatusDTO{Enabled: true, Site: status.Site, LastError: status.LastError, Connected: status.Connected}
		if !status.LastSync.IsZero() {
			dto.LastSync = &status.LastSync
		}
		return c.JSON(dto)
	})
}
