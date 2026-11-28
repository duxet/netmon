import type {
  Alert,
  AlertRule,
  AlertRuleInput,
  AlertRuleType,
  CountryParams,
  KnownDevice,
  PortParams,
  Severity,
  TrafficDirection,
} from "@/lib/api"
import { formatBytes } from "@/lib/format"
import { cleanHostname } from "@/lib/network"

export const RULE_TYPES: { value: AlertRuleType; label: string }[] = [
  { value: "new_device", label: "A new device joins the network" },
  { value: "traffic_threshold", label: "Traffic goes over a threshold" },
  { value: "device_inactive", label: "A device has no traffic for a while" },
  { value: "destination_port", label: "A device connects to a port" },
  { value: "destination_country", label: "A device exchanges traffic with a country" },
  { value: "device_online", label: "A device is back after a quiet while" },
  { value: "new_destination", label: "A device talks to a new country or network" },
  { value: "blocklist", label: "A device talks to an address on a blocklist" },
]

/** Rules about events alert once per event; the others while a condition lasts. */
export function isEventRule(type: AlertRuleType): boolean {
  return (
    type === "new_device" ||
    type === "destination_port" ||
    type === "destination_country" ||
    type === "device_online" ||
    type === "new_destination" ||
    type === "blocklist"
  )
}

/** How long a new_destination rule learns from, in days. */
export const BASELINES = [
  { days: 7, label: "Last 7 days" },
  { days: 30, label: "Last 30 days" },
  { days: 90, label: "Last 90 days" },
]

/** Whether a rule is snoozed now. */
export function isSnoozed(rule: AlertRule, now = Date.now()): boolean {
  return rule.SnoozedUntil !== null && Date.parse(rule.SnoozedUntil) > now
}

/** How long to snooze a rule for, in seconds. */
export const SNOOZES = [
  { seconds: 3600, label: "1 hour" },
  { seconds: 8 * 3600, label: "8 hours" },
  { seconds: 24 * 3600, label: "1 day" },
  { seconds: 7 * 24 * 3600, label: "1 week" },
]

/** How long a device may be quiet, in seconds. */
export const INACTIVE_DURATIONS = [
  { seconds: 15 * 60, label: "15 minutes" },
  { seconds: 30 * 60, label: "30 minutes" },
  { seconds: 3600, label: "1 hour" },
  { seconds: 6 * 3600, label: "6 hours" },
  { seconds: 24 * 3600, label: "1 day" },
]

export const PROTOCOLS: { value: PortParams["protocol"]; label: string }[] = [
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
  { value: "any", label: "Either" },
]

export const DIRECTIONS: { value: TrafficDirection; label: string }[] = [
  { value: "download", label: "Download" },
  { value: "upload", label: "Upload" },
  { value: "total", label: "Total" },
]

/** Windows the rule form offers, in seconds. */
export const WINDOWS = [
  { seconds: 300, label: "5 minutes" },
  { seconds: 900, label: "15 minutes" },
  { seconds: 3600, label: "1 hour" },
  { seconds: 6 * 3600, label: "6 hours" },
  { seconds: 24 * 3600, label: "24 hours" },
]

/** How often a firing alert can be sent again; null is once. */
export const REPEATS: { seconds: number | null; label: string }[] = [
  { seconds: null, label: "Only once" },
  { seconds: 3600, label: "Every hour" },
  { seconds: 6 * 3600, label: "Every 6 hours" },
  { seconds: 24 * 3600, label: "Every day" },
]

export const SEVERITIES: { value: Severity; label: string }[] = [
  { value: "info", label: "Info" },
  { value: "warning", label: "Warning" },
  { value: "critical", label: "Critical" },
]

export const BYTE_UNITS = { MB: 1e6, GB: 1e9 } as const
export type ByteUnit = keyof typeof BYTE_UNITS

/** A window as a phrase: "1 hour", "15 minutes". */
export function windowLabel(seconds: number): string {
  const known = WINDOWS.find((w) => w.seconds === seconds)
  if (known) return known.label
  return seconds % 3600 === 0 ? `${seconds / 3600} hours` : `${Math.round(seconds / 60)} minutes`
}

/** How a device is called in pickers: its name, hostname, vendor or MAC. */
export function knownDeviceLabel(device: KnownDevice): string {
  return (
    device.Name ??
    (device.UniFi?.Name || null) ??
    cleanHostname(device.Hostname) ??
    (device.Vendor ? `${device.Vendor} device` : device.MACAddress)
  )
}

/** Who a rule is about, in words: "every device", "2 devices", "IoT", "1 device and IoT". */
export function describeScope(
  macs: string[] | undefined,
  networks: number[] | undefined,
  networkNames: Map<number, string> = new Map()
): string {
  const parts: string[] = []
  const m = macs?.length ?? 0
  if (m > 0) parts.push(m === 1 ? "1 device" : `${m} devices`)
  const n = networks ?? []
  if (n.length > 0) {
    const names = n.map((id) => networkNames.get(id))
    if (n.length <= 2 && names.every(Boolean)) parts.push(...(names as string[]))
    else parts.push(n.length === 1 ? "1 network" : `${n.length} networks`)
  }
  return parts.length === 0 ? "every device" : parts.join(" and ")
}

/** What a rule checks, in a line: "Download over 5 GB in 1 hour, YouTube, 2 devices". */
export function describeRule(rule: AlertRule, networkNames?: Map<number, string>): string {
  const p = rule.Params
  const devices = () => describeScope(p.macs, p.networks, networkNames)

  switch (rule.Type) {
    case "new_device":
      return (
        (p.ignore_random_macs ? "Any new device, except private Wi-Fi addresses" : "Any new device") +
        (p.networks?.length ? `, on ${describeScope(undefined, p.networks, networkNames)}` : "")
      )
    case "device_inactive":
      return `No traffic for ${durationLabel(p.inactive_s ?? 0)}, ${devices()}`
    case "destination_port": {
      const protocol = PROTOCOLS.find((x) => x.value === p.protocol)
      const ports = (p.ports ?? []).join(", ")
      return `Connections to port${p.ports?.length === 1 ? "" : "s"} ${ports}${protocol && p.protocol !== "any" ? `/${protocol.label}` : ""}, ${devices()}`
    }
    case "destination_country": {
      const countries = (p.countries ?? []).join(", ")
      return `${p.mode === "allow" ? "Traffic outside" : "Traffic with"} ${countries}, ${devices()}`
    }
    case "device_online":
      return `Back after ${durationLabel(p.inactive_s ?? 0)} or more without traffic, ${devices()}`
    case "new_destination":
      return `First traffic with a ${p.kind === "asn" ? "network" : "country"} it hadn't in ${p.baseline_days ?? 30} days, ${devices()}`
    case "blocklist":
      return `Traffic with addresses on ${p.lists?.length ? `${p.lists.length} blocklist${p.lists.length === 1 ? "" : "s"}` : "every blocklist"}, ${devices()}`
    default: {
      const direction = DIRECTIONS.find((d) => d.value === p.direction)?.label ?? "Traffic"
      const parts = [`${direction} over ${formatBytes(p.threshold_bytes ?? 0)} in ${windowLabel(p.window_s ?? 0)}`]
      if (p.app) parts.push(p.app)
      parts.push(p.network ? "whole network" : devices())
      return parts.join(", ")
    }
  }
}

/** A duration in seconds as a phrase: "30 minutes", "1 day". */
export function durationLabel(seconds: number): string {
  const known = INACTIVE_DURATIONS.find((d) => d.seconds === seconds)
  if (known) return known.label
  if (seconds % 86400 === 0) return `${seconds / 86400} days`
  return windowLabel(seconds)
}

/** The editable state of the rule form. */
export interface RuleForm {
  name: string
  type: AlertRuleType
  ignoreRandomMacs: boolean
  direction: TrafficDirection
  amount: string
  unit: ByteUnit
  windowSeconds: number
  /** Empty for all traffic. */
  app: string
  network: boolean
  macs: string[]
  /** The networks the rule is limited to, along with macs, by ID. */
  networks: number[]
  inactiveSeconds: number
  /** Ports as typed, e.g. "23, 2323". */
  ports: string
  protocol: PortParams["protocol"]
  countryMode: CountryParams["mode"]
  /** Country codes as typed, e.g. "RU, CN". */
  countries: string
  destinationKind: "country" | "asn"
  baselineDays: number
  /** The blocklists to check; empty for all of them. */
  lists: string[]
  /** Quiet hours, such as "22:00" and "07:00"; both empty for none. */
  quietFrom: string
  quietTo: string
  severity: Severity
  repeatSeconds: number | null
  notifyOnResolve: boolean
  enabled: boolean
  channelIds: number[]
}

export const DEFAULT_FORM: RuleForm = {
  name: "",
  type: "traffic_threshold",
  ignoreRandomMacs: false,
  direction: "download",
  amount: "5",
  unit: "GB",
  windowSeconds: 3600,
  app: "",
  network: false,
  macs: [],
  networks: [],
  inactiveSeconds: 30 * 60,
  ports: "",
  protocol: "tcp",
  countryMode: "deny",
  countries: "",
  destinationKind: "country",
  baselineDays: 30,
  lists: [],
  quietFrom: "",
  quietTo: "",
  severity: "warning",
  repeatSeconds: null,
  notifyOnResolve: true,
  enabled: true,
  channelIds: [],
}

/** Ready-made rules for the empty rules page. */
export const PRESETS: { label: string; form: Partial<RuleForm> }[] = [
  {
    label: "Tell me about new devices",
    form: { name: "New devices", type: "new_device", severity: "info" },
  },
  {
    label: "Tell me when a device uses more than 10 GB a day",
    form: { name: "Heavy use", type: "traffic_threshold", direction: "total", amount: "10", windowSeconds: 24 * 3600 },
  },
  {
    label: "Tell me when a device uses telnet",
    form: { name: "Telnet", type: "destination_port", ports: "23, 2323", protocol: "tcp" },
  },
]

/** The form for an existing rule. */
export function ruleToForm(rule: AlertRule): RuleForm {
  const p = rule.Params
  const bytes = p.threshold_bytes ?? 5e9
  const unit: ByteUnit = bytes >= BYTE_UNITS.GB && bytes % 1e7 === 0 ? "GB" : "MB"
  return {
    ...DEFAULT_FORM,
    name: rule.Name,
    type: rule.Type,
    ignoreRandomMacs: p.ignore_random_macs ?? false,
    direction: p.direction ?? "download",
    amount: String(bytes / BYTE_UNITS[unit]),
    unit,
    windowSeconds: p.window_s ?? 3600,
    app: p.app ?? "",
    network: p.network ?? false,
    macs: p.macs ?? [],
    networks: p.networks ?? [],
    inactiveSeconds: p.inactive_s ?? DEFAULT_FORM.inactiveSeconds,
    ports: (p.ports ?? []).join(", "),
    protocol: p.protocol ?? "tcp",
    countryMode: p.mode ?? "deny",
    countries: (p.countries ?? []).join(", "),
    destinationKind: p.kind ?? "country",
    baselineDays: p.baseline_days ?? 30,
    lists: p.lists ?? [],
    quietFrom: rule.QuietFrom ?? "",
    quietTo: rule.QuietTo ?? "",
    severity: rule.Severity,
    repeatSeconds: rule.RepeatIntervalSeconds,
    notifyOnResolve: rule.NotifyOnResolve,
    enabled: rule.Enabled,
    channelIds: rule.ChannelIDs,
  }
}

/** The threshold in bytes, or NaN when the amount isn't a positive number. */
export function thresholdBytes(form: RuleForm): number {
  const amount = Number(form.amount.replace(",", "."))
  return amount > 0 ? Math.round(amount * BYTE_UNITS[form.unit]) : NaN
}

/** Numbers in a list as typed, e.g. "23, 2323"; NaN for what isn't one. */
export function parsePorts(text: string): number[] {
  return splitList(text).map((part) => (/^\d+$/.test(part) ? Number(part) : NaN))
}

/** Country codes in a list as typed, upper-cased. */
export function parseCountries(text: string): string[] {
  return splitList(text).map((part) => part.toUpperCase())
}

function splitList(text: string): string[] {
  return text
    .split(/[\s,;]+/)
    .map((part) => part.trim())
    .filter(Boolean)
}

/** What keeps the form from being saved, or null. */
export function formProblem(form: RuleForm): string | null {
  if (!form.name.trim()) return "Give the rule a name."
  if ((form.quietFrom === "") !== (form.quietTo === "")) return "Set when the quiet hours start and end, or neither."
  if (form.quietFrom && form.quietFrom === form.quietTo) return "Quiet hours have to end at another time than they start."
  switch (form.type) {
    case "traffic_threshold":
      return Number.isFinite(thresholdBytes(form)) ? null : "Enter the threshold as a number."
    case "device_inactive":
      return form.macs.length > 0 || form.networks.length > 0 ? null : "Pick at least one device or network to watch."
    case "destination_port": {
      const ports = parsePorts(form.ports)
      if (ports.length === 0) return "Enter at least one port."
      return ports.every((p) => p >= 1 && p <= 65535) ? null : "Ports are numbers from 1 to 65535."
    }
    case "destination_country": {
      const countries = parseCountries(form.countries)
      if (countries.length === 0) return "Enter at least one country code."
      return countries.every((c) => /^[A-Z]{2}$/.test(c)) ? null : "Country codes have two letters, such as DE."
    }
    default:
      return null
  }
}

function paramsOf(form: RuleForm): AlertRuleInput["params"] {
  const networks = form.networks.length > 0 ? { networks: form.networks } : {}
  const macs = { ...(form.macs.length > 0 ? { macs: form.macs } : {}), ...networks }
  switch (form.type) {
    case "new_device":
      return { ignore_random_macs: form.ignoreRandomMacs, ...networks }
    case "device_inactive":
      return { inactive_s: form.inactiveSeconds, macs: form.macs, ...networks }
    case "destination_port":
      return { ports: parsePorts(form.ports), protocol: form.protocol, ...macs }
    case "destination_country":
      return { mode: form.countryMode, countries: parseCountries(form.countries), ...macs }
    case "device_online":
      return { inactive_s: form.inactiveSeconds, ...macs }
    case "new_destination":
      return { kind: form.destinationKind, baseline_days: form.baselineDays, ...macs }
    case "blocklist":
      return { ...(form.lists.length > 0 ? { lists: form.lists } : {}), ...macs }
    default:
      return {
        direction: form.direction,
        threshold_bytes: thresholdBytes(form),
        window_s: form.windowSeconds,
        ...(form.app ? { app: form.app } : {}),
        ...macs,
        ...(form.network ? { network: true } : {}),
      }
  }
}

export function formToInput(form: RuleForm): AlertRuleInput {
  const event = isEventRule(form.type)
  return {
    name: form.name.trim(),
    type: form.type,
    params: paramsOf(form),
    severity: form.severity,
    enabled: form.enabled,
    channel_ids: form.channelIds,
    repeat_interval_s: event ? null : form.repeatSeconds,
    notify_on_resolve: event ? false : form.notifyOnResolve,
    ...(form.quietFrom && form.quietTo ? { quiet_from: form.quietFrom, quiet_to: form.quietTo } : {}),
  }
}

/** The device an alert is about, if any, as its MAC address. */
export function alertDevice(alert: Alert): string | undefined {
  return alert.Labels.mac
}

/** How an alert's subject is called: as in its title, else by its labels. */
export function alertSubjectLabel(alert: Alert): string {
  const l = alert.Labels
  if (l.scope === "network") return l.network ? `The ${l.network} network` : "The whole network"
  return l.device_name ?? cleanHostname(l.hostname) ?? (l.vendor ? `${l.vendor} device` : (l.mac ?? alert.Subject))
}

/** Names and descriptions of alert labels, in the order to show them. */
export const LABELS: { key: string; label: string }[] = [
  { key: "device_name", label: "Name" },
  { key: "hostname", label: "Hostname" },
  { key: "vendor", label: "Made by" },
  { key: "mac", label: "MAC address" },
  { key: "ip", label: "IP address" },
  { key: "network", label: "Local network" },
  { key: "first_seen", label: "First seen" },
  { key: "port", label: "Port" },
  { key: "protocol", label: "Protocol" },
  { key: "country_name", label: "Country" },
  { key: "asn", label: "Network" },
  { key: "organization", label: "Organization" },
  { key: "back_at", label: "Back at" },
  { key: "list_name", label: "Blocklist" },
  { key: "peer", label: "Remote address" },
  { key: "peers", label: "Remote addresses" },
]
