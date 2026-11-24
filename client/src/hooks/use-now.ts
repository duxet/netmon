import * as React from "react"

/** The current time in milliseconds, updated every intervalMs. */
export function useNow(intervalMs: number) {
  const [now, setNow] = React.useState(() => Date.now())
  React.useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), intervalMs)
    return () => window.clearInterval(id)
  }, [intervalMs])
  return now
}
