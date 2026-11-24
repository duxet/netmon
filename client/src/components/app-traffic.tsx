import { useQuery } from "@tanstack/react-query"
import { AppWindowIcon } from "lucide-react"
import * as React from "react"

import { ChartCard } from "@/components/chart-card"
import { RankedBarChart, RankedTable, type RankedRow } from "@/components/charts/ranked-bar-chart"
import { TrafficChart } from "@/components/charts/traffic-chart"
import { EmptyChart, ErrorState } from "@/components/query-state"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { useRange } from "@/hooks/use-range"
import { OTHER_APP } from "@/lib/api"
import { formatBytes, formatPercent } from "@/lib/format"
import { appsQuery, trafficQuery, type ClientFilter } from "@/lib/queries"
import { buildTrafficSeries, rangePhrase } from "@/lib/timeseries"

const OTHER_LABEL = "Other / unrecognized"

/** One app's traffic over the page's time range. */
function AppTrafficSheet({
  app,
  client,
  scope,
  onClose,
}: {
  app: string | null
  client: ClientFilter
  scope: string
  onClose: () => void
}) {
  const range = useRange()
  const query = useQuery({ ...trafficQuery(range, client, app ?? undefined), enabled: app !== null })
  const series = React.useMemo(
    () => (query.data ? buildTrafficSeries(query.data, range, query.dataUpdatedAt) : undefined),
    [query.data, query.dataUpdatedAt, range]
  )

  return (
    <Sheet open={app !== null} onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="w-full gap-0 sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>{app}</SheetTitle>
          <SheetDescription>
            {series?.hasData
              ? `${formatBytes(series.totals.download)} down · ${formatBytes(series.totals.upload)} up · `
              : ""}
            {scope}, {rangePhrase(range)}
          </SheetDescription>
        </SheetHeader>
        <div className="px-4">
          {query.isError ? (
            <ErrorState error={query.error} onRetry={() => void query.refetch()} />
          ) : !series ? (
            <Skeleton className="h-64 w-full" />
          ) : series.hasData ? (
            <TrafficChart
              buckets={series.buckets}
              granularity={series.granularity}
              spansDays={range !== "24h"}
            />
          ) : (
            <EmptyChart icon={<AppWindowIcon />} title="No traffic in this period" className="h-64" />
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

/**
 * Internet traffic attributed to well-known apps by remote address, over
 * the page's time range. "Other" is usually the largest bucket, so it is
 * left out of the bars (where it would flatten every app) and summarized
 * below them instead; the table view lists it with the rest. Selecting an
 * app shows its traffic over time.
 */
export function AppTrafficCard({
  client = {},
  scope = "Whole network",
  className,
}: {
  client?: ClientFilter
  /** Who the traffic is for, e.g. "Whole network" or a client's name. */
  scope?: string
  className?: string
}) {
  const range = useRange()
  const query = useQuery(appsQuery(range, client))
  const [selected, setSelected] = React.useState<string | null>(null)

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
    <>
      <ChartCard
        className={className}
        title="Traffic by app"
        description={`Internet traffic by service, matched on the remote address · ${rangePhrase(range)}`}
        table={tableRows.length > 0 && <RankedTable rows={tableRows} labelHeader="App" />}
      >
        {query.isError ? (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        ) : query.isPending ? (
          <Skeleton className="h-72 w-full" />
        ) : apps.length > 0 ? (
          <div className="flex flex-col gap-2">
            <RankedBarChart rows={apps} labelWidth={180} onSelect={(row) => setSelected(row.key)} />
            <p className="text-sm text-muted-foreground">
              {other && other.total > 0 &&
                `Plus ${formatBytes(other.total)} to unrecognized hosts (${formatPercent(other.total / total)} of internet traffic), not charted. `}
              Select an app to see its traffic over time.
            </p>
          </div>
        ) : (
          <EmptyChart
            icon={<AppWindowIcon />}
            title="No traffic to recognized apps"
            description={
              other && other.total > 0
                ? `${formatBytes(other.total)} went to hosts that matched no known app.`
                : undefined
            }
          />
        )}
      </ChartCard>
      <AppTrafficSheet app={selected} client={client} scope={scope} onClose={() => setSelected(null)} />
    </>
  )
}
