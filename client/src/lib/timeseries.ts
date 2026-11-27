import type { TrafficMeasurement } from "@/lib/api"

const MINUTE = 60 * 1000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

export const RANGES = [
  { key: "1h", label: "Last hour", short: "1h", hours: 1 },
  { key: "24h", label: "Last 24 hours", short: "24h", hours: 24 },
  { key: "7d", label: "Last 7 days", short: "7d", hours: 24 * 7 },
  { key: "30d", label: "Last 30 days", short: "30d", hours: 24 * 30 },
  { key: "all", label: "All time", short: "All", hours: null },
] as const

export type PresetKey = (typeof RANGES)[number]["key"]

/**
 * A preset, or a range of its own as "from-to" in seconds since the epoch,
 * such as one zoomed into on a chart.
 */
export type RangeKey = PresetKey | `${number}-${number}`

export interface CustomRange {
  from: number
  to: number
}

/** The custom range a key holds, in epoch milliseconds, or null for presets. */
export function customRange(key: string): CustomRange | null {
  const match = /^(\d{1,12})-(\d{1,12})$/.exec(key)
  if (!match) return null
  const from = Number(match[1]) * 1000
  const to = Number(match[2]) * 1000
  return from < to ? { from, to } : null
}

/** The key of a custom range from from to to, epoch milliseconds. */
export function customRangeKey(from: number, to: number): RangeKey {
  return `${Math.floor(from / 1000)}-${Math.ceil(to / 1000)}`
}

export function isRangeKey(value: unknown): value is RangeKey {
  return typeof value === "string" && (RANGES.some((range) => range.key === value) || customRange(value) !== null)
}

function shortDate(time: number): string {
  return new Date(time).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })
}

export function rangeInfo(key: RangeKey): { key: RangeKey; label: string; short: string; hours: number | null } {
  const custom = customRange(key)
  if (custom) {
    return {
      key,
      label: `${shortDate(custom.from)} – ${shortDate(custom.to)}`,
      short: "Custom",
      hours: (custom.to - custom.from) / HOUR,
    }
  }
  return RANGES.find((range) => range.key === key) ?? RANGES[2]
}

export const DEFAULT_RANGE: RangeKey = "7d"

/** "last 7 days", "all time", "Nov 1, 10:00 – Nov 3, 12:00": for use inside a sentence. */
export function rangePhrase(key: RangeKey) {
  return customRange(key) ? rangeInfo(key).label : rangeInfo(key).label.toLowerCase()
}

/**
 * How finely the range's traffic over time is grouped: per minute for a short
 * range in the last hour, which is all that is kept per minute; else per hour.
 */
export function rangeInterval(key: RangeKey, now: number): "minute" | "hour" {
  if (key === "1h") return "minute"
  const custom = customRange(key)
  return custom && custom.to - custom.from <= 3 * HOUR && custom.from >= floorHour(now) - HOUR ? "minute" : "hour"
}

/**
 * Start of a range's window, as buildTrafficSeries draws it: whole hours
 * ending with the current one, which also matches the hourly rollups, or for
 * the last hour whole minutes. Undefined for all time.
 */
export function rangeStart(key: RangeKey, now: number): Date | undefined {
  const custom = customRange(key)
  if (custom) return new Date(custom.from)
  if (key === "1h") return new Date(floorMinute(now) + MINUTE - HOUR)
  const { hours } = rangeInfo(key)
  return hours === null ? undefined : new Date(floorHour(now) + HOUR - hours * HOUR)
}

/** End of a range's window, exclusive; undefined for the ones that end now. */
export function rangeEnd(key: RangeKey): Date | undefined {
  const custom = customRange(key)
  return custom ? new Date(custom.to) : undefined
}

/**
 * Like rangeStart, but including the window before (to compare with it) and
 * a day before that, to tell whether that window was monitored throughout.
 */
export function rangeStartWithPrevious(key: RangeKey, now: number): Date | undefined {
  const start = rangeStart(key, now)
  if (!start) return undefined
  const end = rangeEnd(key)?.getTime() ?? (key === "1h" ? floorMinute(now) + MINUTE : floorHour(now) + HOUR)
  return new Date(start.getTime() - (end - start.getTime()) - DAY)
}

/** The from and to to ask the API for, for a range. */
export function rangeFilter(key: RangeKey, now: number): { from?: Date; to?: Date } {
  return { from: rangeStart(key, now), to: rangeEnd(key) }
}

/** "Hourly" and the like, for a series' granularity, at the start of a sentence. */
export function granularityLabel(granularity: TrafficSeries["granularity"]): string {
  return granularity === "day" ? "Daily" : granularity === "minute" ? "Per-minute" : "Hourly"
}

export interface Bucket {
  /** Bucket start, epoch milliseconds. */
  time: number
  download: number
  upload: number
}

export interface Totals {
  download: number
  upload: number
  total: number
}

export interface HourOfDay {
  hour: number
  /** Average bytes transferred in this hour of the day. */
  average: number
}

export interface TrafficSeries {
  granularity: "minute" | "hour" | "day"
  /** Start and end of the window drawn, epoch milliseconds. */
  start: number
  end: number
  /** Whether the window spans more than a day, so that ticks show dates. */
  spansDays: boolean
  buckets: Bucket[]
  totals: Totals
  /** Totals for the window of equal length right before this one. */
  previous: Totals | null
  peak: { time: number; total: number } | null
  hourOfDay: HourOfDay[]
  /** Seconds of the window that actually had monitoring, for average rates. */
  coveredSeconds: number
  hasData: boolean
}

interface Point {
  time: number
  download: number
  upload: number
  /** How long the point covers: 1, or 24 for a day whose hours were rolled up. */
  hours: number
}

function floorHour(time: number): number {
  return Math.floor(time / HOUR) * HOUR
}

function floorMinute(time: number): number {
  return Math.floor(time / MINUTE) * MINUTE
}

function startOfLocalDay(time: number): number {
  const date = new Date(time)
  date.setHours(0, 0, 0, 0)
  return date.getTime()
}

function localDayKey(time: number): string {
  const date = new Date(time)
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`
}

function sumTotals(points: Point[]): Totals {
  let download = 0
  let upload = 0
  for (const point of points) {
    download += point.download
    upload += point.upload
  }
  return { download, upload, total: download + upload }
}

/** Milliseconds from the first hourly point to the end of the window. */
function hourlyCoverage(hourly: Point[], now: number, end: number): number {
  if (hourly.length === 0) return HOUR
  return Math.max(HOUR, Math.min(now, end) - hourly[0].time)
}

export function buildTrafficSeries(
  measurements: TrafficMeasurement[],
  rangeKey: RangeKey,
  now: number
): TrafficSeries {
  // "In" is the reply direction of connections opened from the LAN: download.
  const points: Point[] = measurements
    .map((m) => ({ time: Date.parse(m.Date), download: m.InBytes, upload: m.OutBytes, hours: m.Hours ?? 1 }))
    .filter((point) => Number.isFinite(point.time))
    .sort((a, b) => a.time - b.time)

  const range = rangeInfo(rangeKey)
  const custom = customRange(rangeKey)
  const interval = rangeInterval(rangeKey, now)
  const unit = interval === "minute" ? MINUTE : HOUR
  const floorUnit = (time: number) => Math.floor(time / unit) * unit
  const first = points[0]?.time

  let start: number
  let end: number
  if (custom) {
    start = floorUnit(custom.from)
    end = Math.max(start + unit, Math.ceil(custom.to / unit) * unit)
  } else {
    end = floorUnit(now) + unit
    start = range.hours !== null ? end - range.hours * HOUR : floorHour(first ?? end - 24 * HOUR)
  }
  const span = end - start
  const inRange = points.filter((point) => point.time >= start && point.time < end)

  const granularity: TrafficSeries["granularity"] = interval === "minute" ? "minute" : span > 7 * DAY ? "day" : "hour"
  const buckets: Bucket[] = []

  if (granularity !== "day") {
    const step = granularity === "minute" ? MINUTE : HOUR
    for (let time = start; time < end; time += step) {
      buckets.push({ time, download: 0, upload: 0 })
    }
    for (const point of inRange) {
      const bucket = buckets[Math.floor((point.time - start) / step)]
      if (bucket) {
        bucket.download += point.download
        bucket.upload += point.upload
      }
    }
  } else {
    const byDay = new Map<string, Bucket>()
    const cursor = new Date(startOfLocalDay(start))
    while (cursor.getTime() < end) {
      const bucket = { time: cursor.getTime(), download: 0, upload: 0 }
      buckets.push(bucket)
      byDay.set(localDayKey(bucket.time), bucket)
      cursor.setDate(cursor.getDate() + 1)
    }
    for (const point of inRange) {
      const bucket = byDay.get(localDayKey(point.time))
      if (bucket) {
        bucket.download += point.download
        bucket.upload += point.upload
      }
    }
  }

  // Compare only with a previous window that was monitored throughout:
  // data from before it began (see rangeStartWithPrevious) shows it was.
  let previous: Totals | null = null
  if ((range.hours !== null || custom) && first !== undefined && first < start - span) {
    previous = sumTotals(points.filter((p) => p.time >= start - span && p.time < start))
  }

  // Days whose hours were rolled up say nothing about their hours.
  const hourly = inRange.filter((point) => point.hours <= 1)

  let peak: TrafficSeries["peak"] = null
  for (const point of hourly) {
    const total = point.download + point.upload
    if (total > 0 && (!peak || total > peak.total)) peak = { time: point.time, total }
  }

  const coveredFrom = Math.max(start, first ?? start)
  const coveredSeconds = Math.max(HOUR, Math.min(now, end) - coveredFrom) / 1000
  const days = Math.max(1, coveredSeconds / (DAY / 1000))

  const hourSums = new Array<number>(24).fill(0)
  for (const point of hourly) {
    hourSums[new Date(point.time).getHours()] += point.download + point.upload
  }
  const hourlyDays = hourly.length === inRange.length ? days : Math.max(1, hourlyCoverage(hourly, now, end) / DAY)
  const hourOfDay = hourSums.map((sum, hour) => ({ hour, average: sum / hourlyDays }))

  const totals = sumTotals(inRange)
  return {
    granularity,
    start,
    end,
    spansDays: span > DAY,
    buckets,
    totals,
    previous,
    peak,
    hourOfDay,
    coveredSeconds,
    hasData: totals.total > 0,
  }
}
