import prettyBytes from "pretty-bytes"

const compactNumber = new Intl.NumberFormat(undefined, {
  notation: "compact",
  maximumFractionDigits: 1,
})
const fullNumber = new Intl.NumberFormat()
const percent = new Intl.NumberFormat(undefined, {
  style: "percent",
  maximumFractionDigits: 1,
})
const signedPercent = new Intl.NumberFormat(undefined, {
  style: "percent",
  maximumFractionDigits: 0,
  signDisplay: "exceptZero",
})
const relativeTime = new Intl.RelativeTimeFormat(undefined, {
  numeric: "auto",
  style: "short",
})

export function formatBytes(bytes: number, fractionDigits = 1): string {
  return prettyBytes(bytes, { maximumFractionDigits: fractionDigits })
}

/** Converts a byte count over a duration into a bit rate, e.g. "12.4 Mbit/s". */
export function formatBitrate(bytes: number, seconds: number): string {
  if (seconds <= 0) return "—"
  return `${prettyBytes((bytes * 8) / seconds, { bits: true, maximumFractionDigits: 1 })}/s`
}

export function formatCompact(value: number): string {
  return compactNumber.format(value)
}

export function formatNumber(value: number): string {
  return fullNumber.format(value)
}

export function formatPercent(ratio: number): string {
  return percent.format(ratio)
}

export function formatDelta(ratio: number): string {
  return signedPercent.format(ratio)
}

export function formatTime(date: Date): string {
  return date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
}

export function formatDay(date: Date): string {
  return date.toLocaleDateString(undefined, { month: "short", day: "numeric" })
}

export function formatDayTime(date: Date): string {
  return date.toLocaleString(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  })
}

export function formatLongDay(date: Date): string {
  return date.toLocaleDateString(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
    year: "numeric",
  })
}

export function formatHourOfDay(hour: number): string {
  const date = new Date(2000, 0, 1, hour)
  return date.toLocaleTimeString(undefined, { hour: "numeric" })
}

export function formatRelative(from: number, now: number): string {
  const seconds = Math.round((from - now) / 1000)
  if (Math.abs(seconds) < 60) return relativeTime.format(seconds, "second")
  const minutes = Math.round(seconds / 60)
  if (Math.abs(minutes) < 60) return relativeTime.format(minutes, "minute")
  return relativeTime.format(Math.round(minutes / 60), "hour")
}

/**
 * Round axis ticks for byte values. Steps are 1, 2, 2.5 or 5 × a power of
 * ten, which pretty-bytes' decimal units print as clean labels (250 MB, 2 GB).
 */
export function byteTicks(max: number, target = 4): number[] {
  if (!(max > 0)) return [0, 1]
  const raw = max / target
  const magnitude = 10 ** Math.floor(Math.log10(raw))
  const step = [1, 2, 2.5, 5, 10].map((m) => m * magnitude).find((s) => s >= raw) ?? raw
  const ticks: number[] = []
  for (let i = 0; i <= Math.ceil(max / step); i++) ticks.push(i * step)
  return ticks
}
