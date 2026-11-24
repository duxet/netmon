import type { TrafficMeasurement } from "@/lib/api"

const HOUR = 60 * 60 * 1000
const DAY = 24 * HOUR

export const RANGES = [
  { key: "24h", label: "Last 24 hours", short: "24h", hours: 24 },
  { key: "7d", label: "Last 7 days", short: "7d", hours: 24 * 7 },
  { key: "30d", label: "Last 30 days", short: "30d", hours: 24 * 30 },
  { key: "all", label: "All time", short: "All", hours: null },
] as const

export type RangeKey = (typeof RANGES)[number]["key"]

export function isRangeKey(value: unknown): value is RangeKey {
  return RANGES.some((range) => range.key === value)
}

export function rangeInfo(key: RangeKey) {
  return RANGES.find((range) => range.key === key) ?? RANGES[1]
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
  granularity: "hour" | "day"
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
}

function floorHour(time: number): number {
  return Math.floor(time / HOUR) * HOUR
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

export function buildTrafficSeries(
  measurements: TrafficMeasurement[],
  rangeKey: RangeKey,
  now: number
): TrafficSeries {
  // "In" is the reply direction of connections opened from the LAN: download.
  const points: Point[] = measurements
    .map((m) => ({ time: Date.parse(m.Date), download: m.InBytes, upload: m.OutBytes }))
    .filter((point) => Number.isFinite(point.time))
    .sort((a, b) => a.time - b.time)

  const range = rangeInfo(rangeKey)
  const end = floorHour(now) + HOUR
  const first = points[0]?.time
  const start =
    range.hours !== null
      ? end - range.hours * HOUR
      : floorHour(first ?? end - 24 * HOUR)
  const span = end - start
  const inRange = points.filter((point) => point.time >= start && point.time < end)

  const granularity = span > 7 * DAY ? "day" : "hour"
  const buckets: Bucket[] = []

  if (granularity === "hour") {
    for (let time = start; time < end; time += HOUR) {
      buckets.push({ time, download: 0, upload: 0 })
    }
    for (const point of inRange) {
      const bucket = buckets[Math.floor((point.time - start) / HOUR)]
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

  let previous: Totals | null = null
  if (range.hours !== null && first !== undefined && first < start) {
    previous = sumTotals(points.filter((p) => p.time >= start - span && p.time < start))
  }

  let peak: TrafficSeries["peak"] = null
  for (const point of inRange) {
    const total = point.download + point.upload
    if (total > 0 && (!peak || total > peak.total)) peak = { time: point.time, total }
  }

  const coveredFrom = Math.max(start, first ?? start)
  const coveredSeconds = Math.max(HOUR, Math.min(now, end) - coveredFrom) / 1000
  const days = Math.max(1, coveredSeconds / (DAY / 1000))

  const hourSums = new Array<number>(24).fill(0)
  for (const point of inRange) {
    hourSums[new Date(point.time).getHours()] += point.download + point.upload
  }
  const hourOfDay = hourSums.map((sum, hour) => ({ hour, average: sum / days }))

  const totals = sumTotals(inRange)
  return {
    granularity,
    buckets,
    totals,
    previous,
    peak,
    hourOfDay,
    coveredSeconds,
    hasData: totals.total > 0,
  }
}
