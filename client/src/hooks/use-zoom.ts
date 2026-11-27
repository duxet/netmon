import { useNavigate } from "@tanstack/react-router"
import * as React from "react"

import { customRangeKey } from "@/lib/timeseries"

/** Returns a function that scopes the page to a range dragged across on a chart. */
export function useZoom() {
  const navigate = useNavigate()
  return React.useCallback(
    (from: number, to: number) =>
      void navigate({ to: ".", search: (prev) => ({ ...prev, range: customRangeKey(from, to) }) }),
    [navigate]
  )
}
