import { keepPreviousData, queryOptions, useQuery } from "@tanstack/react-query"

import {
  fetchAlert,
  fetchAlertRuleOptions,
  fetchAlertRules,
  fetchAlerts,
  fetchAppTraffic,
  fetchClients,
  fetchFlowsPage,
  fetchFlowSummary,
  fetchHostnames,
  fetchKnownDevices,
  fetchNetworks,
  fetchNetworkSuggestions,
  fetchNetworkSummary,
  fetchNotificationChannels,
  fetchNotificationServices,
  fetchSession,
  fetchStats,
  fetchTrafficMeasurements,
  type AlertsRequest,
  type FlowsPageRequest,
} from "@/lib/api"
import { cleanHostname, groupDevices } from "@/lib/network"
import { rangeStart, rangeStartWithPrevious, type RangeKey } from "@/lib/timeseries"

export const REFRESH_INTERVAL = 60_000

/**
 * A client by MAC or IP address, or a network (such as a VLAN) by its ID;
 * empty for everything.
 */
export interface ClientFilter {
  mac?: string
  ip?: string
  network?: number
}

// Query keys hold the range ("7d"), not a timestamp: the window's start is
// worked out when the data is fetched, so every refresh moves it forward
// without creating a new cache entry each minute.

/** Who is signed in. It doesn't change while the page is open. */
export function sessionQuery() {
  return queryOptions({
    queryKey: ["session"],
    queryFn: fetchSession,
    staleTime: Infinity,
    refetchInterval: false,
  })
}

export function statsQuery(range: RangeKey) {
  return queryOptions({
    queryKey: ["stats", range],
    queryFn: () => fetchStats({ from: rangeStart(range, Date.now()) }),
  })
}

/** Hourly traffic for the range and the window before it (for comparisons). */
export function trafficQuery(range: RangeKey, client: ClientFilter = {}, app?: string) {
  return queryOptions({
    queryKey: ["traffic-measurements", range, client, app ?? null],
    queryFn: () =>
      fetchTrafficMeasurements({ ...client, from: rangeStartWithPrevious(range, Date.now()) }, app),
  })
}

/** Devices with traffic in the range; with a network, only its addresses. */
export function devicesQuery(range: RangeKey, scope: Pick<ClientFilter, "network"> = {}) {
  return queryOptions({
    queryKey: ["clients", range, scope],
    queryFn: () => fetchClients({ ...scope, from: rangeStart(range, Date.now()) }),
    select: groupDevices,
  })
}

export function appsQuery(range: RangeKey, client: ClientFilter = {}) {
  return queryOptions({
    queryKey: ["apps", range, client],
    queryFn: () => fetchAppTraffic({ ...client, from: rangeStart(range, Date.now()) }),
  })
}

export function flowSummaryQuery(range: RangeKey, client: ClientFilter, top: number) {
  return queryOptions({
    queryKey: ["flow-summary", range, client, top],
    queryFn: () => fetchFlowSummary({ ...client, from: rangeStart(range, Date.now()) }, top),
  })
}

export function flowsPageQuery(range: RangeKey, client: ClientFilter, page: FlowsPageRequest) {
  return queryOptions({
    queryKey: ["flows", range, client, page],
    queryFn: () => fetchFlowsPage({ ...client, from: rangeStart(range, Date.now()) }, page),
    // Keep showing the current page while the next one loads.
    placeholderData: keepPreviousData,
  })
}

/**
 * Resolves reverse-DNS names for a set of IPs through the batch endpoint.
 * The server resolves them one by one, so callers should keep the set small
 * (e.g. the visible page of a table).
 */
export function useHostnames(ipAddresses: string[]) {
  const ips = [...new Set(ipAddresses)].sort()
  return useQuery({
    queryKey: ["hostnames", ips],
    queryFn: async () => {
      const entries = await fetchHostnames(ips)
      return new Map(entries.map((entry) => [entry.IPAddress, cleanHostname(entry.Hostname)]))
    },
    enabled: ips.length > 0,
    staleTime: 15 * 60_000,
    refetchInterval: false,
    placeholderData: keepPreviousData,
  })
}

export function notificationChannelsQuery() {
  return queryOptions({
    queryKey: ["notification-channels"],
    queryFn: fetchNotificationChannels,
  })
}

/** The services shoutrrr supports; they only change with netmon itself. */
export function notificationServicesQuery() {
  return queryOptions({
    queryKey: ["notification-services"],
    queryFn: fetchNotificationServices,
    staleTime: Infinity,
  })
}

/** Every device ever seen, for picking the ones a rule is about. */
export function knownDevicesQuery() {
  return queryOptions({ queryKey: ["known-devices"], queryFn: fetchKnownDevices })
}

export function alertRulesQuery() {
  return queryOptions({
    queryKey: ["alert-rules"],
    queryFn: fetchAlertRules,
    refetchInterval: REFRESH_INTERVAL,
  })
}

export function alertRuleOptionsQuery() {
  return queryOptions({
    queryKey: ["alert-rule-options"],
    queryFn: fetchAlertRuleOptions,
    staleTime: Infinity,
  })
}

/** Alerts are evaluated about once a minute, so they are fetched as often. */
export function alertsQuery(request: AlertsRequest) {
  return queryOptions({
    queryKey: ["alerts", request],
    queryFn: () => fetchAlerts(request),
    refetchInterval: REFRESH_INTERVAL,
    placeholderData: keepPreviousData,
  })
}

export function alertQuery(id: number) {
  return queryOptions({
    queryKey: ["alerts", "detail", id],
    queryFn: () => fetchAlert(id),
    refetchInterval: REFRESH_INTERVAL,
  })
}

/** The networks, such as VLANs, devices are grouped into. */
export function networksQuery() {
  return queryOptions({ queryKey: ["networks"], queryFn: fetchNetworks })
}

/** Every network's traffic and devices over the range. */
export function networkSummaryQuery(range: RangeKey) {
  return queryOptions({
    queryKey: ["network-summary", range],
    queryFn: () => fetchNetworkSummary({ from: rangeStart(range, Date.now()) }),
  })
}

/** Blocks of addresses known devices use that no network has yet. */
export function networkSuggestionsQuery() {
  return queryOptions({ queryKey: ["network-suggestions"], queryFn: fetchNetworkSuggestions })
}
