import * as React from "react"

import { fetchLive, type LiveSample } from "@/lib/api"
import { LIVE_POLL_MS, mergeSamples } from "@/lib/live"

export type LiveStatus = "connecting" | "live" | "error"

export interface LiveTraffic {
  samples: LiveSample[]
  status: LiveStatus
  /** Why the last poll failed; null when it did not. */
  error: unknown
}

const MAX_RETRY_DELAY = 10_000

/**
 * Follows the traffic of the local network as it happens, by asking the API
 * for the samples it does not have yet once a second.
 *
 * Asking is what makes the server measure, so it stops while the tab is in the
 * background and when the page is left. Samples are kept for as long as they
 * make a continuous history: after a long break, it starts over.
 */
export function useLiveTraffic(): LiveTraffic {
  const [traffic, setTraffic] = React.useState<LiveTraffic>({
    samples: [],
    status: "connecting",
    error: null,
  })

  React.useEffect(() => {
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let polling = false
    let failures = 0
    let samples: LiveSample[] = []

    async function poll() {
      timer = undefined
      polling = true

      try {
        const incoming = await fetchLive(samples.at(-1)?.Time, controller.signal)
        samples = mergeSamples(samples, incoming)
        failures = 0
        setTraffic({ samples, status: "live", error: null })
      } catch (error) {
        if (controller.signal.aborted) return
        failures += 1
        setTraffic((previous) => ({ samples: previous.samples, status: "error", error }))
      } finally {
        polling = false
      }

      // A hidden tab is picked up again by onVisibilityChange.
      if (!document.hidden) {
        const delay = Math.min(LIVE_POLL_MS * 2 ** failures, MAX_RETRY_DELAY)
        timer = setTimeout(() => void poll(), failures ? delay : LIVE_POLL_MS)
      }
    }

    function onVisibilityChange() {
      if (!document.hidden && timer === undefined && !polling) void poll()
    }

    document.addEventListener("visibilitychange", onVisibilityChange)
    if (!document.hidden) void poll()

    return () => {
      controller.abort()
      clearTimeout(timer)
      document.removeEventListener("visibilitychange", onVisibilityChange)
    }
  }, [])

  return traffic
}
