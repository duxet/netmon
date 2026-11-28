import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { ActivityIcon } from "lucide-react"
import * as React from "react"

import { ChartCard } from "@/components/chart-card"
import { TrafficChart } from "@/components/charts/traffic-chart"
import { DataTable } from "@/components/data-table"
import { Page, PageHeader } from "@/components/page-header"
import { EmptyChart, ErrorState } from "@/components/query-state"
import { RangeFilter } from "@/components/range-filter"
import { ShareBar } from "@/components/share-bar"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useRange } from "@/hooks/use-range"
import { useZoom } from "@/hooks/use-zoom"
import { formatBytes, formatDayTime, formatLongDay, formatNumber } from "@/lib/format"
import { trafficQuery } from "@/lib/queries"
import { createColumns } from "@/lib/table"
import {
  buildTrafficSeries,
  DEFAULT_RANGE,
  granularityLabel,
  rangePhrase,
  type Bucket,
  type TrafficSeries,
} from "@/lib/timeseries"

export const Route = createFileRoute("/traffic")({
  staticData: { breadcrumb: "Traffic" },
  loaderDeps: ({ search }) => ({ range: search.range ?? DEFAULT_RANGE }),
  loader: ({ context: { queryClient }, deps: { range } }) =>
    void queryClient.prefetchQuery(trafficQuery(range)),
  component: TrafficPage,
})

interface BucketRow extends Bucket {
  total: number
}

const column = createColumns<BucketRow>()

function useColumns(granularity: TrafficSeries["granularity"], maxTotal: number) {
  return React.useMemo(
    () =>
      column.columns([
        column.accessor("time", {
          header: granularity === "day" ? "Day" : granularity === "minute" ? "Minute" : "Hour",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) =>
            granularity === "day" ? formatLongDay(new Date(getValue())) : formatDayTime(new Date(getValue())),
        }),
        column.accessor("download", {
          header: "Download",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => formatBytes(getValue()),
          meta: { align: "right" },
        }),
        column.accessor("upload", {
          header: "Upload",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => formatBytes(getValue()),
          meta: { align: "right" },
        }),
        column.accessor("total", {
          header: "Total",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => (
            <ShareBar value={getValue()} max={maxTotal} label={formatBytes(getValue())} className="min-w-28 sm:min-w-36" />
          ),
          meta: { className: "hidden w-40 sm:table-cell sm:w-56" },
        }),
      ]),
    [granularity, maxTotal]
  )
}

/** The dashboard's traffic chart, with every bucket listed under it. */
function TrafficPage() {
  const range = useRange()
  const zoom = useZoom()
  const traffic = useQuery(trafficQuery(range))
  const series = React.useMemo(
    () => (traffic.data ? buildTrafficSeries(traffic.data, range, traffic.dataUpdatedAt) : undefined),
    [traffic.data, traffic.dataUpdatedAt, range]
  )

  const rows: BucketRow[] = React.useMemo(
    () =>
      (series?.buckets ?? [])
        .filter((bucket) => bucket.download + bucket.upload > 0)
        .map((bucket) => ({ ...bucket, total: bucket.download + bucket.upload })),
    [series]
  )
  const maxTotal = Math.max(0, ...rows.map((row) => row.total))
  const granularity = series?.granularity ?? "hour"
  const columns = useColumns(granularity, maxTotal)
  const unit = granularity === "day" ? "day" : granularity === "minute" ? "minute" : "hour"

  return (
    <Page>
      <PageHeader title="Traffic" description={`Download and upload across your network · ${rangePhrase(range)}`}>
        <RangeFilter />
      </PageHeader>

      <ChartCard
        title={series ? `${granularityLabel(series.granularity)} traffic` : "Traffic"}
        description={
          series?.hasData
            ? `${formatBytes(series.totals.download)} down · ${formatBytes(series.totals.upload)} up · drag across the chart to zoom in`
            : "Download and upload over time"
        }
      >
        {traffic.isError ? (
          <ErrorState error={traffic.error} onRetry={() => void traffic.refetch()} />
        ) : !series ? (
          <Skeleton className="h-64 w-full sm:h-80" />
        ) : series.hasData ? (
          <TrafficChart
            buckets={series.buckets}
            granularity={series.granularity}
            spansDays={series.spansDays}
            onZoom={zoom}
          />
        ) : (
          <EmptyChart
            icon={<ActivityIcon />}
            title="No traffic in this period"
            description="netmon records flows as they close. Try a longer range."
            className="h-64 sm:h-80"
          />
        )}
      </ChartCard>

      <Card>
        <CardHeader>
          <CardTitle>Every {unit}</CardTitle>
          <CardDescription>
            {series
              ? `${formatNumber(rows.length)} ${rows.length === 1 ? unit : `${unit}s`} with traffic`
              : `Traffic per ${unit}`}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {traffic.isError ? (
            <ErrorState error={traffic.error} onRetry={() => void traffic.refetch()} />
          ) : (
            <DataTable
              data={rows}
              columns={columns}
              getRowId={(row) => String(row.time)}
              initialSorting={[{ id: "time", desc: true }]}
              loading={!series}
              pageSize={50}
              emptyMessage="No traffic in this period."
            />
          )}
        </CardContent>
      </Card>
    </Page>
  )
}
