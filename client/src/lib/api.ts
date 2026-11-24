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
  /** The network the client's IP address belongs to, if any. */
  NetworkID: number | null
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

/** What one device used during a live sample, in bytes per second from its own side. */
export interface LiveDevice {
  MACAddress: string
  /** An address the device used; empty when unknown. */
  IPAddress: string
  Download: number
  Upload: number
}

/** The traffic of one second (or so) on the local network. */
export interface LiveSample {
  /** When the interval ended, as the server prints it: hand it back as `since` unchanged. */
  Time: string
  /** Devices that had traffic; none in a quiet second. */
  Devices: LiveDevice[]
}

/** Who is using netmon, when it asks people to sign in. */
export interface Session {
  /** False when netmon has no sign-in: anyone who can reach it can use it. */
  AuthEnabled: boolean
  User: { Name: string | null; Email: string | null } | null
}

export class ApiError extends Error {
  readonly status: number
  /** The API's own explanation, without the request it was about. */
  readonly detail: string | undefined
  /** The setting a refused configuration change was about, if one. */
  readonly setting: string | undefined

  constructor(status: number, message: string, detail?: string, setting?: string) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.detail = detail
    this.setting = setting
  }
}

/** What to tell people about a failed request: the API's reason if it gave one. */
export function errorText(error: unknown): string {
  if (error instanceof ApiError && error.detail) return error.detail
  return error instanceof Error ? error.message : "The netmon API did not respond."
}

let signingIn = false

/**
 * Sends the browser to sign in, and back to this page afterwards. The API
 * answers 401 when there is no session, such as after it expired while the
 * page was open. Every request that was under way answers the same, so only
 * the first one needs to leave.
 */
function signIn() {
  if (signingIn) return
  signingIn = true
  const next = window.location.pathname + window.location.search
  window.location.assign(`/auth/login?next=${encodeURIComponent(next)}`)
}

async function send(path: string, init?: RequestInit): Promise<Response> {
  const response = await fetch(path, init)
  if (response.status === 401) signIn()
  if (!response.ok) {
    // The API answers errors with {"error": "..."}; proxies may not.
    const body: unknown = await response.json().catch(() => null)
    const reason =
      body && typeof body === "object" && "error" in body && typeof body.error === "string"
        ? body.error
        : undefined
    const setting =
      body && typeof body === "object" && "setting" in body && typeof body.setting === "string"
        ? body.setting
        : undefined
    const detail = reason ? `: ${reason}` : ` ${response.statusText}`
    throw new ApiError(
      response.status,
      `${init?.method ?? "GET"} ${decodeURIComponent(path)} failed with ${response.status}${detail}`.trim(),
      reason,
      setting
    )
  }
  return response
}

function jsonBody(method: string, body: unknown): RequestInit {
  return { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  return (await (await send(path, init)).json()) as T
}

// Go encodes an empty slice as `null`, so list endpoints normalize to [].
async function requestList<T>(path: string, init?: RequestInit): Promise<T[]> {
  return (await request<T[] | null>(path, init)) ?? []
}

/** Restricts results to one client or network, and/or a time range. */
export interface FlowsFilter {
  mac?: string
  ip?: string
  /**
   * A network's ID: traffic with either side in it, counted from its side.
   * For /api/clients, the clients whose own address is in it.
   */
  network?: number
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

function filterParams({ mac, ip, network, from, to }: FlowsFilter): Params {
  return { mac, ip, network, from, to }
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

/**
 * The samples of the last couple of minutes or, given the time of the newest
 * one already known, only the samples after it. Asking also makes the server
 * measure, so stop asking when nobody is looking.
 */
export function fetchLive(since?: string, signal?: AbortSignal) {
  return requestList<LiveSample>(withQuery("/api/live", { since }), { signal })
}

export function fetchSession() {
  return request<Session>("/api/session")
}

/** Ends the session, and leaves for a page that doesn't send people straight back in. */
export async function signOut() {
  await send("/auth/logout", { method: "POST" })
  window.location.assign("/auth/signed-out")
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

/** A service notification channels can use, from shoutrrr. */
export interface NotificationService {
  /** URL scheme, e.g. "telegram". */
  Scheme: string
  Name: string
  /** The URL format, with placeholders in capitals. Empty for the less common services. */
  ExampleURL: string
  /** Explains where to find the values the URL needs. */
  DocsURL: string
}

/** Where notifications go. */
export interface NotificationChannel {
  ID: number
  Name: string
  /** The URL's scheme, e.g. "telegram". */
  Service: string
  /**
   * The service URL with its secrets replaced by "REDACTED". The URL holds the
   * service's credentials, so the API never returns it whole.
   */
  MaskedURL: string
  Enabled: boolean
  CreatedAt: string
  UpdatedAt: string
}

/** Changes to a channel. Fields left out (or an empty url) keep their value. */
export interface NotificationChannelChanges {
  name?: string
  url?: string
  enabled?: boolean
}

export function fetchNotificationServices() {
  return requestList<NotificationService>("/api/notification-services")
}

export function fetchNotificationChannels() {
  return requestList<NotificationChannel>("/api/notification-channels")
}

export function createNotificationChannel(channel: { name: string; url: string; enabled?: boolean }) {
  return request<NotificationChannel>("/api/notification-channels", jsonBody("POST", channel))
}

export function updateNotificationChannel(id: number, changes: NotificationChannelChanges) {
  return request<NotificationChannel>(`/api/notification-channels/${id}`, jsonBody("PUT", changes))
}

export async function deleteNotificationChannel(id: number) {
  await send(`/api/notification-channels/${id}`, { method: "DELETE" })
}

/** Sends a test message through a saved channel. */
export async function testNotificationChannel(id: number) {
  await send(`/api/notification-channels/${id}/test`, { method: "POST" })
}

/** Sends a test message to a service URL that isn't saved yet. */
export async function testNotificationURL(url: string, name?: string) {
  await send("/api/notification-channels/test", jsonBody("POST", { url, name }))
}

/** A device seen on the network, from /api/devices. */
export interface KnownDevice {
  MACAddress: string
  /** The address the device used last. */
  IPAddress: string
  Name: string | null
  /** Reverse-DNS name, with the trailing dot of a fully qualified name. */
  Hostname: string | null
  Vendor: string | null
  /** The network IPAddress belongs to, if any. */
  NetworkID: number | null
  FirstSeen: string
  LastSeen: string
}

export function fetchKnownDevices() {
  return requestList<KnownDevice>("/api/devices")
}

export type AlertRuleType =
  | "new_device"
  | "traffic_threshold"
  | "device_inactive"
  | "destination_port"
  | "destination_country"
export type Severity = "info" | "warning" | "critical"
export type TrafficDirection = "download" | "upload" | "total"

export interface NewDeviceParams {
  /** Skips the private, changing Wi-Fi addresses of phones. */
  ignore_random_macs: boolean
}

export interface TrafficParams {
  direction: TrafficDirection
  threshold_bytes: number
  window_s: number
  /** Counts only one app's traffic; absent for all traffic. */
  app?: string
  /** Limits the rule to these devices; absent or empty for every device. */
  macs?: string[]
  /** Counts the internet traffic of all devices together, as one alert. */
  network?: boolean
}

export interface InactiveParams {
  inactive_s: number
  /** The devices to watch; at least one. */
  macs: string[]
}

export interface PortParams {
  ports: number[]
  protocol: "tcp" | "udp" | "any"
  macs?: string[]
}

export interface CountryParams {
  /** deny alerts on the listed countries, allow on all others. */
  mode: "deny" | "allow"
  /** ISO 3166-1 alpha-2 codes, e.g. "DE". */
  countries: string[]
  macs?: string[]
}

export type AlertRuleParams = NewDeviceParams | TrafficParams | InactiveParams | PortParams | CountryParams

/** When to send notifications. */
export interface AlertRule {
  ID: number
  Name: string
  Type: AlertRuleType
  Params: Partial<NewDeviceParams & TrafficParams & InactiveParams & PortParams & CountryParams>
  Severity: Severity
  /** How often a firing alert is sent again, or null to send it once. */
  RepeatIntervalSeconds: number | null
  NotifyOnResolve: boolean
  Enabled: boolean
  ChannelIDs: number[]
  FiringAlerts: number
  LastEvaluatedAt: string | null
  CreatedAt: string
  UpdatedAt: string
}

export interface AlertRuleInput {
  name: string
  type: AlertRuleType
  params: AlertRuleParams
  severity: Severity
  repeat_interval_s: number | null
  notify_on_resolve: boolean
  enabled: boolean
  channel_ids: number[]
}

/** What the rule form offers. */
export interface AlertRuleOptions {
  Types: AlertRuleType[]
  /** Apps a traffic rule can be limited to; "Other" is traffic of no known app. */
  Apps: string[]
  MinWindowSeconds: number
  MaxWindowSeconds: number
  MinRepeatSeconds: number
  /** Whether a GeoIP database is loaded, which country rules need. */
  GeoIP: boolean
}

/** Something a previewed rule finds. */
export interface Finding {
  Subject: string
  /** Who it is about: a device's name, or "the network". */
  Label: string
  /** Bytes for traffic and events, seconds for quiet devices. */
  Value: number | null
  /** Whether the rule would alert about it. */
  Fires: boolean
  Title: string
  Body: string
}

/** What a rule would find now. */
export interface AlertRulePreview {
  /** The first 20, those it would alert about first. */
  Findings: Finding[]
  Total: number
  /** Where a preview of an event rule starts looking; null for the others. */
  Since: string | null
}

/** A notification sent, or not, about an alert. */
export interface Delivery {
  ChannelID: number
  Kind: "firing" | "reminder" | "resolved"
  Status: "sent" | "failed" | "suppressed"
  Attempts: number
  /** Why it failed, with the channel's secrets redacted. */
  Error: string | null
  CreatedAt: string
}

/** A rule firing for a subject, usually a device. */
export interface Alert {
  ID: number
  RuleID: number
  RuleName: string
  Subject: string
  State: "firing" | "resolved"
  Severity: Severity
  Value: number | null
  Title: string
  Body: string
  /** What the alert is about: mac, ip, device_name, hostname, vendor… */
  Labels: Record<string, string>
  StartedAt: string
  LastEvaluatedAt: string
  LastNotifiedAt: string | null
  ResolvedAt: string | null
  Deliveries: Delivery[]
}

export interface AlertsRequest {
  state?: "firing" | "resolved"
  ruleId?: number
  limit?: number
  offset?: number
}

export interface AlertsPage {
  alerts: Alert[]
  total: number
}

export function fetchAlertRules() {
  return requestList<AlertRule>("/api/alert-rules")
}

export function fetchAlertRuleOptions() {
  return request<AlertRuleOptions>("/api/alert-rule-options")
}

export function createAlertRule(rule: AlertRuleInput) {
  return request<AlertRule>("/api/alert-rules", jsonBody("POST", rule))
}

export function updateAlertRule(id: number, rule: AlertRuleInput) {
  return request<AlertRule>(`/api/alert-rules/${id}`, jsonBody("PUT", rule))
}

export function setAlertRuleEnabled(id: number, enabled: boolean) {
  return request<AlertRule>(`/api/alert-rules/${id}`, jsonBody("PATCH", { enabled }))
}

export async function deleteAlertRule(id: number) {
  await send(`/api/alert-rules/${id}`, { method: "DELETE" })
}

export async function fetchAlerts({ state, ruleId, limit, offset }: AlertsRequest = {}): Promise<AlertsPage> {
  const response = await send(withQuery("/api/alerts", { state, rule_id: ruleId, limit, offset }))
  const alerts = ((await response.json()) as Alert[] | null) ?? []
  return { alerts, total: Number(response.headers.get("X-Total-Count") ?? alerts.length) }
}

export function previewAlertRule(rule: AlertRuleInput) {
  return request<AlertRulePreview>("/api/alert-rules/preview", jsonBody("POST", rule))
}

export function fetchAlert(id: number) {
  return request<Alert>(`/api/alerts/${id}`)
}

/** The colors a network can be shown in, in the order new networks get them. */
export const NETWORK_COLORS = ["blue", "orange", "aqua", "yellow", "magenta", "green", "violet", "red"] as const
export type NetworkColor = (typeof NETWORK_COLORS)[number]

/** A group of the local network's addresses, such as a VLAN or a subnet. */
export interface Network {
  ID: number
  Name: string
  /** The 802.1Q VLAN ID, if given; only shown, devices are matched by address. */
  VLAN: number | null
  Color: NetworkColor
  /**
   * The blocks of addresses the network uses, in CIDR notation. An address
   * belongs to the network with the most specific block that holds it.
   */
  Prefixes: string[]
  CreatedAt: string
  UpdatedAt: string
}

export interface NetworkInput {
  name: string
  vlan: number | null
  /** Left out, a new network gets a color no other network has. */
  color?: NetworkColor
  prefixes: string[]
}

/** One network's traffic over a time range, counted from its side. */
export interface NetworkTraffic {
  NetworkID: number
  /** Devices with traffic from an address in the network. */
  Devices: number
  Download: number
  Upload: number
  Packets: number
}

export interface NetworkSummary {
  /** Every network, in the order /api/networks lists them. */
  Networks: NetworkTraffic[]
  /** Devices with traffic that have no address in any network. */
  UnassignedDevices: number
}

/** A block of addresses known devices use that no network has yet. */
export interface NetworkSuggestion {
  Prefix: string
  Devices: number
}

export function fetchNetworks() {
  return requestList<Network>("/api/networks")
}

export function createNetwork(network: NetworkInput) {
  return request<Network>("/api/networks", jsonBody("POST", network))
}

export function updateNetwork(id: number, network: NetworkInput) {
  return request<Network>(`/api/networks/${id}`, jsonBody("PUT", network))
}

export async function deleteNetwork(id: number) {
  await send(`/api/networks/${id}`, { method: "DELETE" })
}

export function fetchNetworkSummary(filter: Pick<FlowsFilter, "from" | "to"> = {}) {
  return request<NetworkSummary>(withQuery("/api/networks/summary", filterParams(filter)))
}

export function fetchNetworkSuggestions() {
  return requestList<NetworkSuggestion>("/api/networks/suggestions")
}

export type SettingValue = string | number

/** One of netmon's settings, as /api/config describes it. */
export interface ConfigSetting {
  /** The setting's key in the YAML file, e.g. "local_dns". */
  Key: string
  /** What netmon would start with now, from the environment, the file or the default. */
  Value: SettingValue | null
  /** What netmon runs with; differs from Value until a restart for settings that aren't Live. */
  Running: SettingValue | null
  Default: SettingValue | null
  /** The file's value, when InFile. */
  File: SettingValue | null
  InFile: boolean
  /** The environment variable that sets it, overriding the file; empty when none does. */
  Env: string
  /** Applies as soon as it is saved, rather than when netmon starts again. */
  Live: boolean
  /** Why it can only be changed in the file; empty when it can be changed here. */
  Locked: string
  /** Secrets have no values here, only Set. */
  Secret: boolean
  Set: boolean
  /** netmon has to start again to run with Value. */
  Restart: boolean
}

export interface Configuration {
  /** The YAML file settings are saved to, and whether it exists yet. */
  File: string
  Exists: boolean
  /** The operating system netmon runs on, e.g. "linux" or "darwin". */
  OS: string
  /** Where notifications link to without public_url. */
  DefaultPublicURL: string
  Settings: ConfigSetting[]
}

export function fetchConfig() {
  return request<Configuration>("/api/config")
}

/** Saves settings to the file: a value sets one, null takes it out of the file, back to its default. */
export function updateConfig(changes: Record<string, SettingValue | null>) {
  return request<Configuration>("/api/config", jsonBody("PUT", changes))
}
