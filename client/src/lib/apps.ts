import { useQuery } from "@tanstack/react-query"
import * as React from "react"

import type { RankedRow } from "@/components/charts/ranked-bar-chart"
import { useRange } from "@/hooks/use-range"
import { OTHER_APP } from "@/lib/api"
import { appsQuery, type ClientFilter } from "@/lib/queries"

export const OTHER_LABEL = "Other / unrecognized"

/** Apps as ranked rows, with the catch-all bucket split off. */
export function useAppRows(client: ClientFilter = {}) {
  const range = useRange()
  const query = useQuery(appsQuery(range, client))
  const rows = React.useMemo(() => {
    const rows: RankedRow[] = (query.data ?? []).map((app) => ({
      key: app.Name,
      label: app.Name === OTHER_APP ? OTHER_LABEL : app.Name,
      download: app.Traffic.InBytes,
      upload: app.Traffic.OutBytes,
      total: app.Traffic.InBytes + app.Traffic.OutBytes,
    }))
    return {
      apps: rows.filter((row) => row.key !== OTHER_APP),
      other: rows.find((row) => row.key === OTHER_APP),
      total: rows.reduce((sum, row) => sum + row.total, 0),
    }
  }, [query.data])
  return { query, ...rows }
}

