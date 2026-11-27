import type { ConfigSetting, SettingValue } from "@/lib/api"

/** What each setting is called on the page. */
export const SETTING_LABELS: Record<string, string> = {
  listen: "Listen address",
  database: "Database",
  geoip_country: "GeoIP country database",
  geoip_asn: "GeoIP ASN database",
  local_dns: "Local DNS server",
  public_dns: "Public DNS server",
  retention_days: "Traffic history",
  alert_retention_days: "Resolved alerts",
  hourly_days: "Hourly detail",
  public_url: "Public URL",
  oidc_issuer: "Provider (issuer)",
  oidc_client_id: "Client ID",
  oidc_client_secret: "Client secret",
  interface: "Capture interface",
  ignore_local_traffic: "Ignore local traffic",
  log_level: "Log level",
  memory_limit: "Memory limit",
  threads: "Threads",
  allowed_hosts: "Allowed hosts",
  mqtt_broker: "MQTT broker",
  mqtt_username: "Username",
  mqtt_password: "Password",
  mqtt_topic: "Topic",
  mqtt_discovery_prefix: "Discovery prefix",
  mqtt_all_devices: "Every device",
}

/** Settings that are whole numbers. */
export const NUMBER_SETTINGS = new Set(["retention_days", "alert_retention_days", "hourly_days", "threads"])

/** Settings that are how many days to keep something for, with 0 keeping it forever. */
const RETENTION_SETTINGS = ["retention_days", "alert_retention_days"]

/** Settings that are on or off. */
export const BOOLEAN_SETTINGS = new Set(["ignore_local_traffic", "mqtt_all_devices"])

/** Settings that are one of a few values, with what each means. */
export const CHOICE_SETTINGS: Record<string, { value: string; label: string }[]> = {
  log_level: [
    { value: "debug", label: "Debug: also every request" },
    { value: "info", label: "Info: also what netmon does" },
    { value: "warn", label: "Warn: problems only" },
    { value: "error", label: "Error: failures only" },
  ],
}

/** The settings the OpenWrt package's service always sets from /etc/config/netmon. */
export const OPENWRT_SETTINGS = new Set(["local_dns", "public_dns", "retention_days", "memory_limit", "threads"])

export function settingLabel(key: string): string {
  return SETTING_LABELS[key] ?? key
}

/** Unsaved edits, by setting key: the text in the field. Empty means the default. */
export type Drafts = Record<string, string>

/** What the field shows before it is edited: the file's value, or nothing for the default. */
export function savedText(setting: ConfigSetting): string {
  return setting.InFile && setting.File !== null ? String(setting.File) : ""
}

/**
 * Whether an on-or-off setting is on with the given text in its field: the
 * text's value, or the default for an empty field.
 */
export function isOn(setting: ConfigSetting, text: string): boolean {
  return text.trim() === "" ? setting.Default === true : text.trim() === "true"
}

/**
 * The text to put in an on-or-off setting's field when it is switched: none,
 * which is no change, when that is what the file already makes it.
 */
export function switchedText(setting: ConfigSetting, on: boolean): string {
  return isOn(setting, savedText(setting)) === on ? savedText(setting) : String(on)
}

/**
 * The text to put in a choice setting's field when a value is picked: none,
 * which is no change, when that is what the file already makes it.
 */
export function chosenText(setting: ConfigSetting, value: string): string {
  const saved = savedText(setting)
  const current = saved.trim() === "" ? String(setting.Default) : saved.trim()
  return current === value ? saved : value
}

export function isWholeNumber(text: string): boolean {
  return /^\d+$/.test(text.trim())
}

/**
 * The changes to send for the drafts: the new value, or null to take a
 * setting out of the file. Drafts that are what the file has are left out.
 */
export function changesOf(settings: ConfigSetting[], drafts: Drafts): Record<string, SettingValue | null> {
  const changes: Record<string, SettingValue | null> = {}
  for (const setting of settings) {
    const draft = drafts[setting.Key]
    if (draft === undefined) continue
    const text = draft.trim()
    if (text === savedText(setting).trim()) continue
    if (text === "") changes[setting.Key] = null
    else if (NUMBER_SETTINGS.has(setting.Key)) changes[setting.Key] = Number(text)
    else if (BOOLEAN_SETTINGS.has(setting.Key)) changes[setting.Key] = text === "true"
    else changes[setting.Key] = text
  }
  return changes
}

/** A number of days to keep data, 0 for forever, and what it is if a change is saved. */
export interface RetentionCut {
  key: string
  days: number
}

/**
 * The retention settings a change shortens, which makes netmon delete data
 * at once: from keeping everything to a number of days, or to fewer days.
 */
export function retentionCuts(
  settings: ConfigSetting[],
  changes: Record<string, SettingValue | null>
): RetentionCut[] {
  const cuts: RetentionCut[] = []
  for (const key of RETENTION_SETTINGS) {
    if (!(key in changes)) continue
    const setting = settings.find((s) => s.Key === key)
    if (!setting) continue
    const next = Number(changes[key] ?? setting.Default ?? 0)
    const now = Number(setting.Running ?? 0)
    if (next > 0 && (now === 0 || next < now)) cuts.push({ key, days: next })
  }
  return cuts
}

/** "90 days", "1 day", or "everything" for 0. */
export function formatDays(value: SettingValue | null): string {
  const days = Number(value ?? 0)
  if (days === 0) return "everything"
  return days === 1 ? "1 day" : `${days} days`
}
