package api

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/networks"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

var errNetworkNotFound = fiber.NewError(fiber.StatusNotFound, "network not found")

// networkBody is the request body for creating and updating a network. An
// update replaces all of it, but for a color left out, which stays.
type networkBody struct {
	Name     *string  `json:"name"`
	VLAN     *int     `json:"vlan"`
	Color    *string  `json:"color"`
	Prefixes []string `json:"prefixes"`
}

func parseNetworkID(c fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, badRequest("invalid network id: " + c.Params("id"))
	}
	return id, nil
}

// networkNotFound turns storage.ErrNotFound into a 404.
func networkNotFound(err error) error {
	if errors.Is(err, storage.ErrNotFound) {
		return errNetworkNotFound
	}
	return err
}

func conflict(message string) error {
	return fiber.NewError(fiber.StatusConflict, message)
}

// networkIDOf returns the ID of the network an address belongs to, if any.
func networkIDOf(matcher *networks.Matcher, ip common.IPAddress) *int64 {
	if ip.Addr == nil {
		return nil
	}
	if n := matcher.Match(*ip.Addr); n != nil {
		return &n.ID
	}
	return nil
}

func toNetworkDTO(record storage.NetworkRecord) NetworkDTO {
	prefixes := make([]string, 0, len(record.Prefixes))
	for _, prefix := range record.Prefixes {
		prefixes = append(prefixes, prefix.String())
	}
	return NetworkDTO{
		ID:        record.ID,
		Name:      record.Name,
		VLAN:      record.VLAN,
		Color:     record.Color,
		Prefixes:  prefixes,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}
}

// parseNetwork checks a request body against the other networks, and fills
// in n, whose ID and color are kept. A new network without a color gets
// one the others don't have.
func parseNetwork(body *networkBody, n *networks.Network, others []storage.NetworkRecord) error {
	if body.Name == nil || body.Prefixes == nil {
		return badRequest("name and prefixes are required")
	}

	name, err := parseName(*body.Name, "name must not be empty")
	if err != nil {
		return err
	}
	for _, other := range others {
		if other.ID != n.ID && strings.EqualFold(other.Name, name) {
			return conflict(fmt.Sprintf("there is a network named “%s” already", other.Name))
		}
	}

	if body.VLAN != nil && (*body.VLAN < networks.MinVLAN || *body.VLAN > networks.MaxVLAN) {
		return badRequest(fmt.Sprintf("vlan must be from %d to %d", networks.MinVLAN, networks.MaxVLAN))
	}

	prefixes, err := networks.ParsePrefixes(body.Prefixes)
	if err != nil {
		return badRequest(err.Error())
	}

	list := make([]networks.Network, 0, len(others))
	for _, other := range others {
		if other.ID != n.ID {
			list = append(list, other.Network)
		}
	}

	switch {
	case body.Color != nil && *body.Color != "":
		if !networks.IsColor(*body.Color) {
			return badRequest("color must be one of " + strings.Join(networks.Colors, ", "))
		}
		n.Color = *body.Color
	case n.Color == "":
		n.Color = networks.NextColor(list)
	}

	n.Name, n.VLAN, n.Prefixes = name, body.VLAN, prefixes

	if prefix, other, ok := networks.Conflict(*n, list); ok {
		return conflict(fmt.Sprintf("%s is in the network “%s” already", prefix, other.Name))
	}
	return nil
}

// summarizeNetworks adds up the traffic of each network from the traffic
// between networks, and counts the devices with an address in each from the
// clients.
func summarizeNetworks(matcher *networks.Matcher, traffic []storage.NetworkTrafficRecord, clients []storage.ClientRecord) NetworkSummaryDTO {
	byID := map[int64]*NetworkTrafficDTO{}
	summary := NetworkSummaryDTO{Networks: make([]NetworkTrafficDTO, 0, len(matcher.Networks()))}
	for _, n := range matcher.Networks() {
		summary.Networks = append(summary.Networks, NetworkTrafficDTO{NetworkID: n.ID})
	}
	for i := range summary.Networks {
		byID[summary.Networks[i].NetworkID] = &summary.Networks[i]
	}

	for _, r := range traffic {
		packets := r.InPackets + r.OutPackets
		// The source opened the connection, so what it received is the
		// replies; a flow within one network counts once.
		if r.Source != nil {
			if n := byID[*r.Source]; n != nil {
				n.Download += r.InBytes
				n.Upload += r.OutBytes
				n.Packets += packets
			}
		}
		if r.Destination != nil && (r.Source == nil || *r.Destination != *r.Source) {
			if n := byID[*r.Destination]; n != nil {
				n.Download += r.OutBytes
				n.Upload += r.InBytes
				n.Packets += packets
			}
		}
	}

	devices := map[int64]map[string]bool{}
	assigned := map[string]bool{}
	all := map[string]bool{}
	for _, client := range clients {
		mac := client.MACAddress.String()
		all[mac] = true
		if id := networkIDOf(matcher, client.IPAddress); id != nil {
			if devices[*id] == nil {
				devices[*id] = map[string]bool{}
			}
			devices[*id][mac] = true
			assigned[mac] = true
		}
	}
	for i := range summary.Networks {
		summary.Networks[i].Devices = len(devices[summary.Networks[i].NetworkID])
	}
	summary.UnassignedDevices = len(all) - len(assigned)

	return summary
}

// registerNetworkRoutes adds the endpoints for networks, such as VLANs.
func registerNetworkRoutes(app *fiber.App, db *sql.DB) {
	app.Get("/api/networks", func(c fiber.Ctx) error {
		records, err := storage.ListNetworks(db)
		if err != nil {
			return err
		}

		dtos := make([]NetworkDTO, 0, len(records))
		for _, record := range records {
			dtos = append(dtos, toNetworkDTO(record))
		}
		return c.JSON(dtos)
	})

	app.Post("/api/networks", func(c fiber.Ctx) error {
		body := new(networkBody)
		if err := c.Bind().Body(body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}

		others, err := storage.ListNetworks(db)
		if err != nil {
			return err
		}
		var n networks.Network
		if err := parseNetwork(body, &n, others); err != nil {
			return err
		}

		record, err := storage.CreateNetwork(db, n, time.Now())
		if err != nil {
			return err
		}
		log.Printf("Added network %d (%s)", record.ID, record.Name)

		return c.Status(fiber.StatusCreated).JSON(toNetworkDTO(record))
	})

	// The traffic and devices of every network over the time range.
	app.Get("/api/networks/summary", func(c fiber.Ctx) error {
		var filter storage.FlowsFilter
		if err := parseTimeRange(c, &filter); err != nil {
			return err
		}

		matcher, err := storage.NetworkMatcher(db)
		if err != nil {
			return err
		}
		scopes := map[int64]networks.Scope{}
		for _, n := range matcher.Networks() {
			scopes[n.ID], _ = matcher.Scope(n.ID)
		}

		traffic, err := storage.GetNetworkTraffic(db, scopes, filter)
		if err != nil {
			return err
		}
		clients, err := storage.GetClients(db, filter)
		if err != nil {
			return err
		}

		return c.JSON(summarizeNetworks(matcher, traffic, clients))
	})

	// Blocks of addresses known devices use that no network has yet.
	app.Get("/api/networks/suggestions", func(c fiber.Ctx) error {
		matcher, err := storage.NetworkMatcher(db)
		if err != nil {
			return err
		}
		devices, err := storage.GetDevices(db)
		if err != nil {
			return err
		}

		addrs := make([]netip.Addr, 0, len(devices))
		for _, device := range devices {
			if device.IPAddress.Addr != nil {
				addrs = append(addrs, *device.IPAddress.Addr)
			}
		}

		suggestions := networks.Suggest(addrs, matcher)
		dtos := make([]NetworkSuggestionDTO, 0, len(suggestions))
		for _, s := range suggestions {
			dtos = append(dtos, NetworkSuggestionDTO{Prefix: s.Prefix.String(), Devices: s.Devices})
		}
		return c.JSON(dtos)
	})

	app.Get("/api/networks/:id", func(c fiber.Ctx) error {
		id, err := parseNetworkID(c)
		if err != nil {
			return err
		}

		record, err := storage.GetNetwork(db, id)
		if err != nil {
			return networkNotFound(err)
		}
		return c.JSON(toNetworkDTO(record))
	})

	app.Put("/api/networks/:id", func(c fiber.Ctx) error {
		id, err := parseNetworkID(c)
		if err != nil {
			return err
		}

		body := new(networkBody)
		if err := c.Bind().Body(body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}

		record, err := storage.GetNetwork(db, id)
		if err != nil {
			return networkNotFound(err)
		}
		others, err := storage.ListNetworks(db)
		if err != nil {
			return err
		}
		if err := parseNetwork(body, &record.Network, others); err != nil {
			return err
		}

		record, err = storage.UpdateNetwork(db, record.Network, time.Now())
		if err != nil {
			return networkNotFound(err)
		}
		log.Printf("Updated network %d (%s)", record.ID, record.Name)

		return c.JSON(toNetworkDTO(record))
	})

	app.Delete("/api/networks/:id", func(c fiber.Ctx) error {
		id, err := parseNetworkID(c)
		if err != nil {
			return err
		}

		if err := storage.DeleteNetwork(db, id); err != nil {
			return networkNotFound(err)
		}
		log.Printf("Removed network %d", id)

		return c.SendStatus(fiber.StatusNoContent)
	})
}
