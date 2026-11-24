import { keepPreviousData, queryOptions, useQuery } from "@tanstack/react-query"

import {
  fetchClients,
  fetchFlows,
  fetchHostnames,
  fetchStats,
  fetchTrafficMeasurements,
  type FlowsFilter,
} from "@/lib/api"
import { cleanHostname, groupDevices } from "@/lib/network"

export const REFRESH_INTERVAL = 60_000

export const statsQuery = queryOptions({
  queryKey: ["stats"],
  queryFn: fetchStats,
})

export const trafficQuery = queryOptions({
  queryKey: ["traffic-measurements"],
  queryFn: fetchTrafficMeasurements,
})

export const devicesQuery = queryOptions({
  queryKey: ["clients"],
  queryFn: fetchClients,
  select: groupDevices,
})

export function flowsQuery(filter: FlowsFilter) {
  return queryOptions({
    queryKey: ["flows", filter],
    queryFn: () => fetchFlows(filter),
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
