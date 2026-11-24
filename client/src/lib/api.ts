// Types mirror the Go DTOs in api/dto.go. The Go structs have no JSON tags,
// so field names are serialized exactly as declared there.

export interface Endpoint {
  /** Colon-separated MAC, or an empty string when the collector had none. */
  MACAddress: string
  IPAddress: string
  /** ISO 3166-1 alpha-2 code from GeoIP. Only set by /api/flows. */
  Country?: string | null
}

export interface Traffic {
  InBytes: number
  InPackets: number
  OutBytes: number
  OutPackets: number
}

export interface Client {
  /** Reverse-DNS name, with the trailing dot of a fully qualified name. */
  Hostname: string | null
  Endpoint: Endpoint
  Traffic: Traffic
}

export interface Flow {
  Source: Endpoint
  Destination: Endpoint
  IPProto: number
  Port: number
  Traffic: Traffic
}

export interface Stats {
  TotalClients: number
  TotalBytes: number | null
  TotalPackets: number | null
}

export interface TrafficMeasurement {
  InBytes: number
  OutBytes: number
  /** Start of an hourly bucket. */
  Date: string
}

export interface HostnameEntry {
  IPAddress: string
  Hostname: string | null
}

export interface AppTraffic {
  /** App name, or "Other" for traffic to unrecognized remote hosts. */
  Name: string
  /** From the local network's point of view: In is download. */
  Traffic: Traffic
}

/** Name /api/apps uses for traffic that matched no known app. */
export const OTHER_APP = "Other"

export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = "ApiError"
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init)
  if (!response.ok) {
    // The API answers errors with {"error": "..."}; proxies may not.
    const body: unknown = await response.json().catch(() => null)
    const detail =
      body && typeof body === "object" && "error" in body && typeof body.error === "string"
        ? `: ${body.error}`
        : ` ${response.statusText}`
    throw new ApiError(
      response.status,
      `${init?.method ?? "GET"} ${decodeURIComponent(path)} failed with ${response.status}${detail}`.trim()
    )
  }
  return (await response.json()) as T
}

// Go encodes an empty slice as `null`, so list endpoints normalize to [].
async function requestList<T>(path: string, init?: RequestInit): Promise<T[]> {
  return (await request<T[] | null>(path, init)) ?? []
}

export function fetchStats() {
  return request<Stats>("/api/stats")
}

export function fetchTrafficMeasurements() {
  return requestList<TrafficMeasurement>("/api/traffic-measurements")
}

export function fetchClients() {
  return requestList<Client>("/api/clients")
}

export interface FlowsFilter {
  mac?: string
  ip?: string
}

function filterQuery(filter: FlowsFilter) {
  const params = new URLSearchParams()
  if (filter.mac) params.set("mac", filter.mac)
  if (filter.ip) params.set("ip", filter.ip)
  return params.size > 0 ? `?${params}` : ""
}

export function fetchFlows(filter: FlowsFilter) {
  return requestList<Flow>(`/api/flows${filterQuery(filter)}`)
}

export function fetchAppTraffic(filter: FlowsFilter = {}) {
  return requestList<AppTraffic>(`/api/apps${filterQuery(filter)}`)
}

export function fetchHostnames(ipAddresses: string[]) {
  return requestList<HostnameEntry>("/api/hostnames", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ip_addresses: ipAddresses }),
  })
}
