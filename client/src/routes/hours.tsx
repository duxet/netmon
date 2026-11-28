import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { ActivityIcon } from "lucide-react"
import * as React from "react"

import { ChartCard } from "@/components/chart-card"
import { HourOfDayChart } from "@/components/charts/hour-of-day-chart"
import { DataTable } from "@/components/data-table"
import { Page, PageHeader } from "@/components/page-header"
import { EmptyChart, ErrorState } from "@/components/query-state"
import { RangeFilter } from "@/components/range-filter"
import { ShareBar } from "@/components/share-bar"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useRange } from "@/hooks/use-range"
import { formatBytes, formatHourOfDay, formatPercent } from "@/lib/format"
import { trafficQuery } from "@/lib/queries"
import { createColumns } from "@/lib/table"
import { buildTrafficSeries, DEFAULT_RANGE, rangePhrase, type HourOfDay } from "@/lib/timeseries"

export const Route = createFileRoute("/hours")({
  staticData: { breadcrumb: "Busiest hours" },
  loaderDeps: ({ search }) => ({ range: search.range ?? DEFAULT_RANGE }),
  loader: ({ context: { queryClient }, deps: { range } }) =>
    void queryClient.prefetchQuery(trafficQuery(range)),
  component: HoursPage,
})

interface HourRow extends HourOfDay {
  /** This hour's part of an average day's traffic. */
  share: number
}

const column = createColumns<HourRow>()

function useColumns(maxAverage: number) {
  return React.useMemo(
    () =>
      column.columns([
        column.accessor("hour", {
          header: "Hour",
          sortFn: "basic",
          cell: ({ getValue }) =>
            `${formatHourOfDay(getValue())} – ${formatHourOfDay((getValue() + 1) % 24)}`,
        }),
        column.accessor("share", {
          header: "Share of the day",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => formatPercent(getValue()),
          meta: { align: "right", className: "hidden sm:table-cell" },
        }),
        column.accessor("average", {
          header: "Average traffic",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => (
            <ShareBar value={getValue()} max={maxAverage} label={formatBytes(getValue())} className="min-w-28 sm:min-w-36" />
          ),
          meta: { className: "w-40 sm:w-64" },
        }),
      ]),
    [maxAverage]
  )
}

/** The dashboard's hour-of-day chart, with all 24 hours listed under it. */
function HoursPage() {
  const range = useRange()
  const traffic = useQuery(trafficQuery(range))
  const series = React.useMemo(
    () => (traffic.data ? buildTrafficSeries(traffic.data, range, traffic.dataUpdatedAt) : undefined),
    [traffic.data, traffic.dataUpdatedAt, range]
  )

  const rows: HourRow[] = React.useMemo(() => {
    const hours = series?.hourOfDay ?? []
    const day = hours.reduce((sum, hour) => sum + hour.average, 0)
    return hours.map((hour) => ({ ...hour, share: day > 0 ? hour.average / day : 0 }))
  }, [series])
  const maxAverage = Math.max(0, ...rows.map((row) => row.average))
  const columns = useColumns(maxAverage)
  const peak = rows.reduce<HourRow | undefined>((best, row) => (!best || row.average > best.average ? row : best), undefined)

  return (
    <Page>
      <PageHeader
        title="Busiest hours"
        description={`Average traffic per hour of the day · ${rangePhrase(range)}`}
      >
        <RangeFilter />
      </PageHeader>

      <ChartCard
        title="Average per hour of day"
        description={
          peak && peak.average > 0
            ? `Peaks around ${formatHourOfDay(peak.hour)}`
            : "When your network is busiest"
        }
      >
        {traffic.isError ? (
          <ErrorState error={traffic.error} onRetry={() => void traffic.refetch()} />
        ) : !series ? (
          <Skeleton className="h-56 w-full" />
        ) : series.hasData ? (
          <HourOfDayChart data={series.hourOfDay} />
        ) : (
          <EmptyChart icon={<ActivityIcon />} title="No traffic in this period" className="h-56" />
        )}
      </ChartCard>

      <Card>
        <CardHeader>
          <CardTitle>Every hour</CardTitle>
          <CardDescription>Sort by traffic to rank the hours</CardDescription>
        </CardHeader>
        <CardContent>
          {traffic.isError ? (
            <ErrorState error={traffic.error} onRetry={() => void traffic.refetch()} />
          ) : (
            <DataTable
              data={series?.hasData ? rows : []}
              columns={columns}
              getRowId={(row) => String(row.hour)}
              initialSorting={[{ id: "hour", desc: false }]}
              loading={!series}
              pageSize={24}
              emptyMessage="No traffic in this period."
            />
          )}
        </CardContent>
      </Card>
    </Page>
  )
}
