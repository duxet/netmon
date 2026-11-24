package api

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

// parseTimeRange reads the optional from (inclusive) and to (exclusive)
// query parameters, as RFC 3339 timestamps.
func parseTimeRange(c fiber.Ctx, filter *storage.FlowsFilter) error {
	for _, param := range []struct {
		name string
		dst  **time.Time
	}{{"from", &filter.From}, {"to", &filter.To}} {
		value := c.Query(param.name)
		if value == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return badRequest(param.name + " must be an RFC 3339 timestamp, e.g. 2026-11-24T08:00:00Z: " + value)
		}
		*param.dst = &t
	}

	if filter.From != nil && filter.To != nil && !filter.From.Before(*filter.To) {
		return badRequest("from must be before to")
	}

	return nil
}

// parseFlowsFilter reads the optional mac, ip, network, from and to query
// parameters. An invalid value is a client error rather than a reason to
// return unfiltered data.
func parseFlowsFilter(c fiber.Ctx, db *sql.DB) (storage.FlowsFilter, error) {
	var filter storage.FlowsFilter

	if value := c.Query("mac"); value != "" {
		mac, err := common.ParseMACAddress(value)
		if err != nil {
			return filter, badRequest("invalid mac address: " + value)
		}
		filter.MAC = mac
	}

	if value := c.Query("ip"); value != "" {
		ip, err := common.ParseIPAddress(value)
		if err != nil {
			return filter, badRequest("invalid ip address: " + value)
		}
		filter.IP = ip
	}

	if value := c.Query("network"); value != "" {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return filter, badRequest("invalid network id: " + value)
		}
		matcher, err := storage.NetworkMatcher(db)
		if err != nil {
			return filter, err
		}
		scope, ok := matcher.Scope(id)
		if !ok {
			return filter, errNetworkNotFound
		}
		filter.Network = &scope
	}

	return filter, parseTimeRange(c, &filter)
}

// hasSubject reports whether a filter selects a client or a network, whose
// side traffic is counted from.
func hasSubject(filter storage.FlowsFilter) bool {
	return filter.MAC != nil || filter.IP != nil || filter.Network != nil
}

// parseClientFilter accepts a client identified by either MAC or IP address
// in the path, and the from and to query parameters.
func parseClientFilter(c fiber.Ctx, id string) (storage.FlowsFilter, error) {
	var filter storage.FlowsFilter

	if mac, err := common.ParseMACAddress(id); err == nil {
		filter.MAC = mac
	} else if ip, err := common.ParseIPAddress(id); err == nil {
		filter.IP = ip
	} else {
		return filter, badRequest("client id must be a mac or ip address: " + id)
	}

	return filter, parseTimeRange(c, &filter)
}

func parseInt(c fiber.Ctx, name string, fallback, low, high int) (int, error) {
	value := c.Query(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < low || n > high {
		return 0, badRequest(name + " must be a number from " + strconv.Itoa(low) + " to " + strconv.Itoa(high))
	}
	return n, nil
}

// parseFlowsPage reads the q, sort, order, limit and offset query parameters.
// Without a limit, all flows are returned.
func parseFlowsPage(c fiber.Ctx) (FlowsPage, error) {
	page := FlowsPage{Query: c.Query("q"), Sort: c.Query("sort", "total")}

	if _, ok := flowSortKeys[page.Sort]; !ok {
		return page, badRequest("sort must be one of total, download, upload, packets")
	}

	switch c.Query("order", "desc") {
	case "asc":
		page.Asc = true
	case "desc":
	default:
		return page, badRequest("order must be asc or desc")
	}

	var err error
	if page.Limit, err = parseInt(c, "limit", 0, 1, maxFlowsPerPage); err != nil {
		return page, err
	}
	if page.Offset, err = parseInt(c, "offset", 0, 0, 1<<31-1); err != nil {
		return page, err
	}

	return page, nil
}
