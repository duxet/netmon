package api

import (
	"sort"

	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/storage"
)

// aggregateAppTraffic attributes each record to the app owning its remote
// endpoint and sums up traffic per app. Traffic is reported from the local
// network's point of view: InBytes is download, OutBytes is upload.
// Traffic between two local endpoints is ignored.
func aggregateAppTraffic(classifier *apps.Classifier, records []storage.EndpointPairTrafficRecord) []AppTrafficDTO {
	totals := map[string]*TrafficDTO{}

	for _, record := range records {
		if record.SourceIPAddress.Addr == nil || record.DestinationIPAddress.Addr == nil {
			continue
		}

		src := *record.SourceIPAddress.Addr
		dst := *record.DestinationIPAddress.Addr

		var app string
		var traffic TrafficDTO

		switch {
		case apps.IsPublic(dst):
			// Connection initiated towards a remote host: replies are downloads.
			app = classifier.Classify(dst)
			traffic = TrafficDTO{record.InBytes, record.InPackets, record.OutBytes, record.OutPackets}
		case apps.IsPublic(src):
			// Connection initiated by a remote host: originating traffic is a download.
			app = classifier.Classify(src)
			traffic = TrafficDTO{record.OutBytes, record.OutPackets, record.InBytes, record.InPackets}
		default:
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
