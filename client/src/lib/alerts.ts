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
]

/** Rules about events alert once per event; the others while a condition lasts. */
export function isEventRule(type: AlertRuleType): boolean {
  return type === "new_device" || type === "destination_port" || type === "destination_country"
}

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

/** What a rule checks, in a line: "Download over 5 GB in 1 hour, YouTube, 2 devices". */
export function describeRule(rule: AlertRule): string {
  const p = rule.Params
  const devices = (n: number) => (n === 0 ? "every device" : n === 1 ? "1 device" : `${n} devices`)

  switch (rule.Type) {
    case "new_device":
      return p.ignore_random_macs ? "Any new device, except private Wi-Fi addresses" : "Any new device"
    case "device_inactive":
      return `No traffic for ${durationLabel(p.inactive_s ?? 0)}, ${devices(p.macs?.length ?? 0)}`
    case "destination_port": {
      const protocol = PROTOCOLS.find((x) => x.value === p.protocol)
      const ports = (p.ports ?? []).join(", ")
      return `Connections to port${p.ports?.length === 1 ? "" : "s"} ${ports}${protocol && p.protocol !== "any" ? `/${protocol.label}` : ""}, ${devices(p.macs?.length ?? 0)}`
    }
    case "destination_country": {
      const countries = (p.countries ?? []).join(", ")
      return `${p.mode === "allow" ? "Traffic outside" : "Traffic with"} ${countries}, ${devices(p.macs?.length ?? 0)}`
    }
    default: {
      const direction = DIRECTIONS.find((d) => d.value === p.direction)?.label ?? "Traffic"
      const parts = [`${direction} over ${formatBytes(p.threshold_bytes ?? 0)} in ${windowLabel(p.window_s ?? 0)}`]
      if (p.app) parts.push(p.app)
      parts.push(p.network ? "whole network" : devices(p.macs?.length ?? 0))
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
  inactiveSeconds: number
  /** Ports as typed, e.g. "23, 2323". */
  ports: string
  protocol: PortParams["protocol"]
  countryMode: CountryParams["mode"]
  /** Country codes as typed, e.g. "RU, CN". */
  countries: string
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
  inactiveSeconds: 30 * 60,
  ports: "",
  protocol: "tcp",
  countryMode: "deny",
  countries: "",
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
    inactiveSeconds: p.inactive_s ?? DEFAULT_FORM.inactiveSeconds,
    ports: (p.ports ?? []).join(", "),
    protocol: p.protocol ?? "tcp",
    countryMode: p.mode ?? "deny",
    countries: (p.countries ?? []).join(", "),
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
  switch (form.type) {
    case "traffic_threshold":
      return Number.isFinite(thresholdBytes(form)) ? null : "Enter the threshold as a number."
    case "device_inactive":
      return form.macs.length > 0 ? null : "Pick at least one device to watch."
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
  const macs = form.macs.length > 0 ? { macs: form.macs } : {}
  switch (form.type) {
    case "new_device":
      return { ignore_random_macs: form.ignoreRandomMacs }
    case "device_inactive":
      return { inactive_s: form.inactiveSeconds, macs: form.macs }
    case "destination_port":
      return { ports: parsePorts(form.ports), protocol: form.protocol, ...macs }
    case "destination_country":
      return { mode: form.countryMode, countries: parseCountries(form.countries), ...macs }
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
  }
}

/** The device an alert is about, if any, as its MAC address. */
export function alertDevice(alert: Alert): string | undefined {
  return alert.Labels.mac
}

/** How an alert's subject is called: as in its title, else by its labels. */
export function alertSubjectLabel(alert: Alert): string {
  const l = alert.Labels
  if (l.scope === "network") return "The whole network"
  return l.device_name ?? cleanHostname(l.hostname) ?? (l.vendor ? `${l.vendor} device` : (l.mac ?? alert.Subject))
}

/** Names and descriptions of alert labels, in the order to show them. */
export const LABELS: { key: string; label: string }[] = [
  { key: "device_name", label: "Name" },
  { key: "hostname", label: "Hostname" },
  { key: "vendor", label: "Made by" },
  { key: "mac", label: "MAC address" },
  { key: "ip", label: "IP address" },
  { key: "first_seen", label: "First seen" },
  { key: "port", label: "Port" },
  { key: "protocol", label: "Protocol" },
  { key: "country_name", label: "Country" },
  { key: "peer", label: "Remote address" },
  { key: "peers", label: "Remote addresses" },
]
