import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { ActivityIcon, ArrowRightIcon, GaugeIcon, MonitorSmartphoneIcon } from "lucide-react"
import * as React from "react"

import { ChartCard } from "@/components/chart-card"
import { HourOfDayChart, HourOfDayTable } from "@/components/charts/hour-of-day-chart"
import { RankedBarChart, RankedTable, type RankedRow } from "@/components/charts/ranked-bar-chart"
import { TrafficChart, TrafficTable } from "@/components/charts/traffic-chart"
import { Page, PageHeader } from "@/components/page-header"
import { EmptyChart, ErrorState } from "@/components/query-state"
import { StatCard } from "@/components/stat-card"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  formatBitrate,
  formatBytes,
  formatCompact,
  formatDayTime,
  formatHourOfDay,
  formatNumber,
} from "@/lib/format"
import { deviceLabel } from "@/lib/network"
import { devicesQuery, statsQuery, trafficQuery } from "@/lib/queries"
import { buildTrafficSeries, isRangeKey, RANGES, rangeInfo, type RangeKey } from "@/lib/timeseries"

export const Route = createFileRoute("/")({
  validateSearch: (search): { range?: RangeKey } =>
    isRangeKey(search.range) ? { range: search.range } : {},
  staticData: { breadcrumb: "Dashboard" },
  loader: ({ context: { queryClient } }) => {
    void queryClient.prefetchQuery(trafficQuery)
    void queryClient.prefetchQuery(statsQuery)
  },
  component: Dashboard,
})

const DEFAULT_RANGE: RangeKey = "7d"
const TOP_DEVICES = 8

function Dashboard() {
  const { range = DEFAULT_RANGE } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const info = rangeInfo(range)

  const traffic = useQuery(trafficQuery)
  const stats = useQuery(statsQuery)
  const devices = useQuery(devicesQuery)

  // Anchor the window to when the data was fetched, so it stays stable
  // between refreshes and moves forward with each one.
  const series = React.useMemo(
    () =>
      traffic.data ? buildTrafficSeries(traffic.data, range, traffic.dataUpdatedAt) : undefined,
    [traffic.data, traffic.dataUpdatedAt, range]
  )

  const topDevices: RankedRow[] = React.useMemo(
    () =>
      (devices.data ?? []).slice(0, TOP_DEVICES).map((device) => ({
        key: device.mac,
        label: deviceLabel(device),
        download: device.download,
        upload: device.upload,
        total: device.total,
      })),
    [devices.data]
  )

  const addressCount = devices.data?.reduce((sum, device) => sum + device.addresses.length, 0)
  const loadingSeries = traffic.isPending
  const delta = (current: number, previous: number | undefined) =>
    previous ? { ratio: (current - previous) / previous, period: info.label.replace("Last ", "") } : null
  const peakHour = series?.hourOfDay.reduce(
    (best, hour) => (hour.average > best.average ? hour : best),
    { hour: 0, average: 0 }
  )

  return (
    <Page>
      <PageHeader
        title="Dashboard"
        description={`Traffic across your network · ${info.label.toLowerCase()}`}
      >
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          value={range}
          onValueChange={(value) =>
            isRangeKey(value) &&
            void navigate({ search: value === DEFAULT_RANGE ? {} : { range: value }, replace: true })
          }
          aria-label="Time range"
        >
          {RANGES.map((option) => (
            <ToggleGroupItem
              key={option.key}
              value={option.key}
              aria-label={option.label}
              className="px-3"
            >
              {option.short}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </PageHeader>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          label="Downloaded"
          seriesColor="var(--chart-1)"
          loading={loadingSeries}
          value={series ? formatBytes(series.totals.download) : "—"}
          delta={series && delta(series.totals.download, series.previous?.download)}
          footer={series && `Avg ${formatBitrate(series.totals.download, series.coveredSeconds)}`}
        />
        <StatCard
          label="Uploaded"
          seriesColor="var(--chart-2)"
          loading={loadingSeries}
          value={series ? formatBytes(series.totals.upload) : "—"}
          delta={series && delta(series.totals.upload, series.previous?.upload)}
          footer={series && `Avg ${formatBitrate(series.totals.upload, series.coveredSeconds)}`}
        />
        <StatCard
          label="Busiest hour"
          icon={GaugeIcon}
          loading={loadingSeries}
          value={series?.peak ? formatBytes(series.peak.total) : "—"}
          footer={series?.peak ? formatDayTime(new Date(series.peak.time)) : "No traffic yet"}
        />
        <StatCard
          label="Clients"
          icon={MonitorSmartphoneIcon}
          loading={stats.isPending}
          value={stats.data ? formatNumber(stats.data.TotalClients) : "—"}
          footer={
            <span className="truncate">
              {[
                addressCount !== undefined && `${formatNumber(addressCount)} addresses`,
                stats.data?.TotalPackets && `${formatCompact(stats.data.TotalPackets)} packets`,
              ]
                .filter(Boolean)
                .join(" · ") || "None seen yet"}
            </span>
          }
        />
      </div>

      <ChartCard
        title="Traffic"
        description={
          series
            ? `${series.granularity === "day" ? "Daily" : "Hourly"} download and upload · ${info.label.toLowerCase()}`
            : "Download and upload over time"
        }
        table={
          series?.hasData && (
            <TrafficTable buckets={series.buckets} granularity={series.granularity} />
          )
        }
      >
        {traffic.isError ? (
          <ErrorState error={traffic.error} onRetry={() => void traffic.refetch()} />
        ) : !series ? (
          <Skeleton className="h-64 w-full sm:h-72" />
        ) : series.hasData ? (
          <TrafficChart
            buckets={series.buckets}
            granularity={series.granularity}
            spansDays={range !== "24h"}
          />
        ) : (
          <EmptyChart
            icon={<ActivityIcon />}
            title="No traffic in this period"
            description="netmon records flows as they close. Try a longer range."
            className="h-64 sm:h-72"
          />
        )}
      </ChartCard>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-5">
        <ChartCard
          className="lg:col-span-3"
          title="Top clients"
          description="By total traffic · all time"
          table={topDevices.length > 0 && <RankedTable rows={topDevices} labelHeader="Client" />}
        >
          {devices.isError ? (
            <ErrorState error={devices.error} onRetry={() => void devices.refetch()} />
          ) : devices.isPending ? (
            <Skeleton className="h-72 w-full" />
          ) : topDevices.length > 0 ? (
            <div className="flex flex-col gap-2">
              <RankedBarChart
                rows={topDevices}
                onSelect={(row) =>
                  void navigate({ to: "/clients/$clientId", params: { clientId: row.key } })
                }
              />
              <Button variant="ghost" size="sm" className="self-end" asChild>
                <Link to="/clients">
                  All clients
                  <ArrowRightIcon data-icon="inline-end" />
                </Link>
              </Button>
            </div>
          ) : (
            <EmptyChart icon={<MonitorSmartphoneIcon />} title="No clients seen yet" />
          )}
        </ChartCard>

        <ChartCard
          className="lg:col-span-2"
          title="Busiest hours"
          description={
            peakHour && peakHour.average > 0
              ? `Average per hour of day · peaks around ${formatHourOfDay(peakHour.hour)}`
              : "Average traffic per hour of day"
          }
          table={series?.hasData && <HourOfDayTable data={series.hourOfDay} />}
        >
          {traffic.isError ? (
            <ErrorState error={traffic.error} />
          ) : !series ? (
            <Skeleton className="h-56 w-full" />
          ) : series.hasData ? (
            <HourOfDayChart data={series.hourOfDay} />
          ) : (
            <EmptyChart icon={<ActivityIcon />} title="No traffic in this period" className="h-56" />
          )}
        </ChartCard>
      </div>
    </Page>
  )
}
