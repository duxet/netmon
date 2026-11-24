import type {
  Alert,
  AlertRule,
  AlertRuleInput,
  AlertRuleType,
  KnownDevice,
  Severity,
  TrafficDirection,
} from "@/lib/api"
import { formatBytes } from "@/lib/format"
import { cleanHostname } from "@/lib/network"

export const RULE_TYPES: { value: AlertRuleType; label: string }[] = [
  { value: "new_device", label: "A new device joins the network" },
  { value: "traffic_threshold", label: "A device uses more traffic than a threshold" },
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
    cleanHostname(device.Hostname) ??
    (device.Vendor ? `${device.Vendor} device` : device.MACAddress)
  )
}

/** What a rule checks, in a line: "Download over 5 GB in 1 hour, YouTube, 2 devices". */
export function describeRule(rule: AlertRule): string {
  if (rule.Type === "new_device") {
    return rule.Params.ignore_random_macs
      ? "Any new device, except private Wi-Fi addresses"
      : "Any new device"
  }

  const p = rule.Params
  const direction = DIRECTIONS.find((d) => d.value === p.direction)?.label ?? "Traffic"
  const parts = [
    `${direction} over ${formatBytes(p.threshold_bytes ?? 0)} in ${windowLabel(p.window_s ?? 0)}`,
  ]
  if (p.app) parts.push(p.app)
  const devices = p.macs?.length ?? 0
  parts.push(devices === 0 ? "every device" : devices === 1 ? "1 device" : `${devices} devices`)
  return parts.join(", ")
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
  macs: string[]
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
  macs: [],
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
    macs: p.macs ?? [],
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

export function formToInput(form: RuleForm): AlertRuleInput {
  const common = {
    name: form.name.trim(),
    type: form.type,
    severity: form.severity,
    enabled: form.enabled,
    channel_ids: form.channelIds,
  }
  if (form.type === "new_device") {
    return {
      ...common,
      params: { ignore_random_macs: form.ignoreRandomMacs },
      repeat_interval_s: null,
      notify_on_resolve: false,
    }
  }
  return {
    ...common,
    params: {
      direction: form.direction,
      threshold_bytes: thresholdBytes(form),
      window_s: form.windowSeconds,
      ...(form.app ? { app: form.app } : {}),
      ...(form.macs.length > 0 ? { macs: form.macs } : {}),
    },
    repeat_interval_s: form.repeatSeconds,
    notify_on_resolve: form.notifyOnResolve,
  }
}

/** The device an alert is about, if any, as its MAC address. */
export function alertDevice(alert: Alert): string | undefined {
  return alert.Labels.mac
}

/** How an alert's subject is called: as in its title, else by its labels. */
export function alertSubjectLabel(alert: Alert): string {
  const l = alert.Labels
  return l.device_name ?? cleanHostname(l.hostname) ?? (l.vendor ? `${l.vendor} device` : (l.mac ?? alert.Subject))
}
