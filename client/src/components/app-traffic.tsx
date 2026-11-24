import { useQuery } from "@tanstack/react-query"
import { AppWindowIcon } from "lucide-react"
import * as React from "react"

import { ChartCard } from "@/components/chart-card"
import { RankedBarChart, RankedTable, type RankedRow } from "@/components/charts/ranked-bar-chart"
import { EmptyChart, ErrorState } from "@/components/query-state"
import { Skeleton } from "@/components/ui/skeleton"
import { OTHER_APP, type FlowsFilter } from "@/lib/api"
import { formatBytes, formatPercent } from "@/lib/format"
import { appsQuery } from "@/lib/queries"

const OTHER_LABEL = "Other / unrecognized"

/**
 * Internet traffic attributed to well-known apps by remote address.
 * "Other" is usually the largest bucket, so it is left out of the bars
 * (where it would flatten every app) and summarized below them instead;
 * the table view lists it with the rest.
 */
export function AppTrafficCard({
  filter,
  className,
}: {
  filter?: FlowsFilter
  className?: string
}) {
  const query = useQuery(appsQuery(filter))

  const { apps, other, total } = React.useMemo(() => {
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

  const tableRows = other ? [...apps, other] : apps

  return (
    <ChartCard
      className={className}
      title="Traffic by app"
      description="Internet traffic by service, matched on the remote address · all time"
      table={tableRows.length > 0 && <RankedTable rows={tableRows} labelHeader="App" />}
    >
      {query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : query.isPending ? (
        <Skeleton className="h-72 w-full" />
      ) : apps.length > 0 ? (
        <div className="flex flex-col gap-2">
          <RankedBarChart rows={apps} labelWidth={180} />
          {other && other.total > 0 && (
            <p className="text-sm text-muted-foreground">
              Plus {formatBytes(other.total)} to unrecognized hosts (
              {formatPercent(other.total / total)} of internet traffic), not charted.
            </p>
          )}
        </div>
      ) : (
        <EmptyChart
          icon={<AppWindowIcon />}
          title="No traffic to recognized apps yet"
          description={
            other && other.total > 0
              ? `${formatBytes(other.total)} went to hosts that matched no known app.`
              : undefined
          }
        />
      )}
    </ChartCard>
  )
}
