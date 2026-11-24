// Turns the samples of /api/live into what the live page shows. Everything
// here is a pure function of its arguments, so it can be reasoned about (and
// checked) without a browser or a server.

import type { LiveSample } from "@/lib/api"

/** How much history the page keeps; the server keeps the same. */
export const LIVE_WINDOW_MS = 2 * 60_000

/** How often the page asks for new samples. */
export const LIVE_POLL_MS = 1_000

/**
 * A longer silence than this between two samples means measuring stopped in
 * between (nobody was looking, or the server restarted), so what was there
 * before says nothing about now.
 */
export const LIVE_MAX_GAP_MS = 10_000

/** A device is active for this long after its last traffic. */
export const LIVE_ACTIVE_MS = 10_000

export function sampleTime(sample: LiveSample): number {
  return Date.parse(sample.Time)
}

/**
 * Adds the samples a poll returned to the ones already known, keeping the last
 * LIVE_WINDOW_MS of them. It returns `current` itself when nothing was added,
 * so callers can tell by identity.
 */
export function mergeSamples(current: LiveSample[], incoming: LiveSample[]): LiveSample[] {
  const known = current.at(-1)
  const knownTime = known ? sampleTime(known) : -Infinity

  // A poll asks for what is after the newest sample, but a slow one can
  // overlap with the next.
  const fresh = incoming.filter((sample) => sampleTime(sample) > knownTime)
  if (fresh.length === 0) return current

  const silence = sampleTime(fresh[0]) - knownTime
  const merged = silence > LIVE_MAX_GAP_MS ? fresh : [...current, ...fresh]

  const cutoff = sampleTime(merged[merged.length - 1]) - LIVE_WINDOW_MS
  const first = merged.findIndex((sample) => sampleTime(sample) >= cutoff)

  return first > 0 ? merged.slice(first) : merged
}

/** Traffic in bytes per second at a moment. */
export interface LivePoint {
  time: number
  download: number
  upload: number
}

export interface LiveDeviceSeries {
  mac: string
  /** The last address the device was seen with, if the server knew one. */
  ip: string | null
  /** The rate in the newest sample; zero when the device had no traffic in it. */
  download: number
  upload: number
  /** Whether it had traffic during the last LIVE_ACTIVE_MS. */
  active: boolean
  /** The device's busiest second of the window, in either direction. */
  peak: number
  /** Its rate in every sample of the window, zero where it was quiet. */
  points: LivePoint[]
  /** What the list is ordered by: its traffic of the last LIVE_ACTIVE_MS. */
  recent: number
  /** Its traffic over the whole window; the tie breaker for `recent`. */
  weight: number
}

export interface LiveView {
  /** The samples' times: the window ends at the last. */
  times: number[]
  /** Everything the devices used in each sample. */
  total: LivePoint[]
  devices: LiveDeviceSeries[]
  /** Time of the newest sample; null before there is one. */
  newest: number | null
  /** Devices that are active. */
  active: number
  peakDownload: number
  peakUpload: number
}

export const EMPTY_LIVE_VIEW: LiveView = {
  times: [],
  total: [],
  devices: [],
  newest: null,
  active: 0,
  peakDownload: 0,
  peakUpload: 0,
}

export function buildLiveView(samples: LiveSample[]): LiveView {
  if (samples.length === 0) return EMPTY_LIVE_VIEW

  const times = samples.map(sampleTime)
  const newest = times[times.length - 1]
  const count = samples.length

  const byMac = new Map<string, { ip: string | null; download: number[]; upload: number[] }>()
  const total: LivePoint[] = times.map((time) => ({ time, download: 0, upload: 0 }))

  samples.forEach((sample, i) => {
    for (const device of sample.Devices ?? []) {
      let entry = byMac.get(device.MACAddress)
      if (!entry) {
        entry = { ip: null, download: new Array<number>(count).fill(0), upload: new Array<number>(count).fill(0) }
        byMac.set(device.MACAddress, entry)
      }
      entry.download[i] = device.Download
      entry.upload[i] = device.Upload
      entry.ip = device.IPAddress || entry.ip

      total[i].download += device.Download
      total[i].upload += device.Upload
    }
  })

  const devices: LiveDeviceSeries[] = []

  for (const [mac, entry] of byMac) {
    const points = times.map((time, i) => ({
      time,
      download: entry.download[i],
      upload: entry.upload[i],
    }))

    let recent = 0
    let weight = 0
    let peak = 0
    for (const point of points) {
      const rate = point.download + point.upload
      weight += rate
      if (newest - point.time < LIVE_ACTIVE_MS) recent += rate
      peak = Math.max(peak, point.download, point.upload)
    }

    const last = points[points.length - 1]
    devices.push({
      mac,
      ip: entry.ip,
      download: last.download,
      upload: last.upload,
      active: recent > 0,
      peak,
      points,
      recent,
      weight,
    })
  }

  devices.sort(
    (a, b) => b.recent - a.recent || b.weight - a.weight || (a.mac < b.mac ? -1 : a.mac > b.mac ? 1 : 0)
  )

  return {
    times,
    total,
    devices,
    newest,
    active: devices.filter((device) => device.active).length,
    peakDownload: Math.max(0, ...total.map((point) => point.download)),
    peakUpload: Math.max(0, ...total.map((point) => point.upload)),
  }
}
