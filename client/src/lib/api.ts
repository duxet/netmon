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
  /** The name the user gave the device, if any. */
  Name: string | null
  /** Reverse-DNS name, with the trailing dot of a fully qualified name. */
  Hostname: string | null
  /** Manufacturer the MAC address was assigned to, if known. */
  Vendor: string | null
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

/** Traffic of a group of flows, seen from the client a summary is for. */
export interface Group {
  /** An IP address, "protocol/port" (e.g. "6/443") or an ISO country code. */
  Key: string
  Download: number
  Upload: number
  Connections: number
}

/** The largest groups, and the sum of all others. */
export interface Groups {
  Items: Group[] | null
  Rest: Group
}

export interface FlowSummary {
  Download: number
  Upload: number
  Packets: number
  Connections: number
  /** Distinct remote hosts. */
  Peers: number
  TopPeers: Groups
  Services: Groups
  Countries: Groups
}

export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = "ApiError"
    this.status = status
  }
}

async function send(path: string, init?: RequestInit): Promise<Response> {
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
  return response
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  return (await (await send(path, init)).json()) as T
}

// Go encodes an empty slice as `null`, so list endpoints normalize to [].
async function requestList<T>(path: string, init?: RequestInit): Promise<T[]> {
  return (await request<T[] | null>(path, init)) ?? []
}

/** Restricts results to one client and/or a time range. */
export interface FlowsFilter {
  mac?: string
  ip?: string
  /** Inclusive. */
  from?: Date
  /** Exclusive. */
  to?: Date
}

type Params = Record<string, string | number | Date | undefined>

function withQuery(path: string, params: Params) {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === "") continue
    search.set(key, value instanceof Date ? value.toISOString() : String(value))
  }
  return search.size > 0 ? `${path}?${search}` : path
}

function filterParams({ mac, ip, from, to }: FlowsFilter): Params {
  return { mac, ip, from, to }
}

export function fetchStats(filter: FlowsFilter = {}) {
  return request<Stats>(withQuery("/api/stats", filterParams(filter)))
}

export function fetchTrafficMeasurements(filter: FlowsFilter = {}, app?: string) {
  return requestList<TrafficMeasurement>(
    withQuery("/api/traffic-measurements", { ...filterParams(filter), app })
  )
}

export function fetchClients(filter: FlowsFilter = {}) {
  return requestList<Client>(withQuery("/api/clients", filterParams(filter)))
}

export type FlowSort = "total" | "download" | "upload" | "packets"

export interface FlowsPageRequest {
  /** Part of an IP address, a port or a protocol. */
  q?: string
  sort?: FlowSort
  order?: "asc" | "desc"
  limit: number
  offset: number
}

export interface FlowsPage {
  flows: Flow[]
  /** Flows matching the filter and query, across all pages. */
  total: number
}

export async function fetchFlowsPage(filter: FlowsFilter, page: FlowsPageRequest): Promise<FlowsPage> {
  const response = await send(withQuery("/api/flows", { ...filterParams(filter), ...page }))
  const flows = ((await response.json()) as Flow[] | null) ?? []
  return { flows, total: Number(response.headers.get("X-Total-Count") ?? flows.length) }
}

export function fetchFlowSummary(filter: FlowsFilter, top: number) {
  return request<FlowSummary>(withQuery("/api/flows/summary", { ...filterParams(filter), top }))
}

export function fetchAppTraffic(filter: FlowsFilter = {}) {
  return requestList<AppTraffic>(withQuery("/api/apps", filterParams(filter)))
}

export function fetchHostnames(ipAddresses: string[]) {
  return requestList<HostnameEntry>("/api/hostnames", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ip_addresses: ipAddresses }),
  })
}

// MAC addresses are URL-safe; the API does not unescape %3A in paths.
export async function renameDevice(mac: string, name: string) {
  return request<{ MACAddress: string; Name: string }>(`/api/devices/${mac}/name`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  })
}

export async function resetDeviceName(mac: string) {
  await send(`/api/devices/${mac}/name`, { method: "DELETE" })
}
