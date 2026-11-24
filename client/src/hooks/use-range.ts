import { useSearch } from "@tanstack/react-router"

import { DEFAULT_RANGE, isRangeKey, type RangeKey } from "@/lib/timeseries"

/** The time range every page is scoped to, from the ?range= search param. */
export function useRange(): RangeKey {
  return useSearch({
    strict: false,
    select: (search) => (isRangeKey(search.range) ? search.range : DEFAULT_RANGE),
  })
}
