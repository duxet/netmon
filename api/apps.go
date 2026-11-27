package api

import (
	"sort"

	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/storage"
)

// classifyPair returns the app owning a record's remote endpoint and its
// traffic from the local network's point of view (InBytes is download).
// ok is false for traffic between two local endpoints.
func classifyPair(classifier *apps.Classifier, record storage.EndpointPairTrafficRecord) (app string, traffic TrafficDTO, ok bool) {
	if record.SourceIPAddress.Addr == nil || record.DestinationIPAddress.Addr == nil {
		return "", TrafficDTO{}, false
	}

	src := *record.SourceIPAddress.Addr
	dst := *record.DestinationIPAddress.Addr

	switch {
	case apps.IsPublic(dst):
		// Connection initiated towards a remote host: replies are downloads.
		return classifier.Classify(dst), TrafficDTO{record.InBytes, record.InPackets, record.OutBytes, record.OutPackets}, true
	case apps.IsPublic(src):
		// Connection initiated by a remote host: originating traffic is a download.
		return classifier.Classify(src), TrafficDTO{record.OutBytes, record.OutPackets, record.InBytes, record.InPackets}, true
	default:
		return "", TrafficDTO{}, false
	}
}

// isAppName reports whether name is a known app or the catch-all bucket.
func isAppName(name string) bool {
	if name == apps.Other {
		return true
	}
	for _, app := range apps.Known {
		if app.Name == name {
			return true
		}
	}
	return false
}

// appTrafficByHour sums one app's traffic per hour, oldest first, from
// records of GetEndpointPairTrafficByHour.
func appTrafficByHour(classifier *apps.Classifier, records []storage.EndpointPairTrafficRecord, app string) []TrafficMeasurementDTO {
	var result []TrafficMeasurementDTO

	for _, record := range records {
		name, traffic, ok := classifyPair(classifier, record)
		if !ok || name != app {
			continue
		}

		// Records come ordered by hour, so each hour is one run.
		if n := len(result); n > 0 && result[n-1].Date.Equal(record.Hour) {
			result[n-1].InBytes += traffic.InBytes
			result[n-1].OutBytes += traffic.OutBytes
			result[n-1].Hours = max(result[n-1].Hours, record.Hours)
			continue
		}
		result = append(result, TrafficMeasurementDTO{
			InBytes:  traffic.InBytes,
			OutBytes: traffic.OutBytes,
			Date:     record.Hour,
			Hours:    max(record.Hours, 1),
		})
	}

	return result
}

// aggregateAppTraffic attributes each record to the app owning its remote
// endpoint and sums up traffic per app. Traffic is reported from the local
// network's point of view: InBytes is download, OutBytes is upload.
// Traffic between two local endpoints is ignored.
func aggregateAppTraffic(classifier *apps.Classifier, records []storage.EndpointPairTrafficRecord) []AppTrafficDTO {
	totals := map[string]*TrafficDTO{}

	for _, record := range records {
		app, traffic, ok := classifyPair(classifier, record)
		if !ok {
			continue
		}

		total, ok := totals[app]
		if !ok {
			total = &TrafficDTO{}
			totals[app] = total
		}
		total.InBytes += traffic.InBytes
		total.InPackets += traffic.InPackets
		total.OutBytes += traffic.OutBytes
		total.OutPackets += traffic.OutPackets
	}

	result := make([]AppTrafficDTO, 0, len(totals))
	for name, traffic := range totals {
		result = append(result, AppTrafficDTO{Name: name, Traffic: *traffic})
	}

	// Most traffic first, with the catch-all bucket always last.
	sort.Slice(result, func(i, j int) bool {
		if (result[i].Name == apps.Other) != (result[j].Name == apps.Other) {
			return result[j].Name == apps.Other
		}
		ti := result[i].Traffic.InBytes + result[i].Traffic.OutBytes
		tj := result[j].Traffic.InBytes + result[j].Traffic.OutBytes
		if ti != tj {
			return ti > tj
		}
		return result[i].Name < result[j].Name
	})

	return result
}
