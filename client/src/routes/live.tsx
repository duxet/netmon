import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { ActivityIcon, MonitorSmartphoneIcon } from "lucide-react"
import * as React from "react"

import { LiveChart } from "@/components/charts/live-chart"
import { LiveDevices } from "@/components/live-devices"
import { Page, PageHeader } from "@/components/page-header"
import { EmptyChart, ErrorState } from "@/components/query-state"
import { StatCard } from "@/components/stat-card"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useLiveTraffic, type LiveStatus } from "@/hooks/use-live-traffic"
import { useRange } from "@/hooks/use-range"
import { formatBitrate, formatNumber } from "@/lib/format"
import { buildLiveView, LIVE_WINDOW_MS } from "@/lib/live"
import { devicesQuery } from "@/lib/queries"

export const Route = createFileRoute("/live")({
  staticData: { breadcrumb: "Live" },
  component: LivePage,
})

const WINDOW_MINUTES = LIVE_WINDOW_MS / 60_000

function StatusBadge({ status }: { status: LiveStatus }) {
  if (status === "error") {
    return <Badge variant="destructive">Reconnecting…</Badge>
  }

  const live = status === "live"
  return (
    <Badge variant="outline" className="gap-1.5">
      <span
        aria-hidden
        className={`size-1.5 rounded-full ${live ? "bg-chart-3 motion-safe:animate-pulse" : "bg-muted-foreground"}`}
      />
      {live ? "Live" : "Connecting…"}
    </Badge>
  )
}

function LivePage() {
  const { samples, status, error } = useLiveTraffic()
  const view = React.useMemo(() => buildLiveView(samples), [samples])

  // Names, manufacturers and the like come from earlier traffic. A device that
  // only just joined has none yet, and is listed by address.
  const range = useRange()
  const devices = useQuery(devicesQuery(range))
  const known = React.useMemo(
    () => new Map((devices.data ?? []).map((device) => [device.mac, device])),
    [devices.data]
  )

  const latest = view.total.at(-1)
  const waiting = status === "connecting" && samples.length === 0
  const failed = status === "error" && samples.length === 0

  return (
    <Page>
      <PageHeader
        title="Live"
        description="What each device is using right now · updates every second"
      >
        <StatusBadge status={status} />
      </PageHeader>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatCard
          label="Download"
          seriesColor="var(--chart-1)"
          loading={waiting}
          value={latest ? formatBitrate(latest.download, 1) : "—"}
          footer={view.newest && `Peak ${formatBitrate(view.peakDownload, 1)} in the last ${WINDOW_MINUTES} min`}
        />
        <StatCard
          label="Upload"
          seriesColor="var(--chart-2)"
          loading={waiting}
          value={latest ? formatBitrate(latest.upload, 1) : "—"}
          footer={view.newest && `Peak ${formatBitrate(view.peakUpload, 1)} in the last ${WINDOW_MINUTES} min`}
        />
        <StatCard
          label="Active devices"
          icon={MonitorSmartphoneIcon}
          loading={waiting}
          value={view.newest ? formatNumber(view.active) : "—"}
          footer={view.newest && `${formatNumber(view.devices.length)} seen in the last ${WINDOW_MINUTES} min`}
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Network traffic</CardTitle>
          <CardDescription>
            Everything the devices downloaded and uploaded · last {WINDOW_MINUTES} minutes
          </CardDescription>
        </CardHeader>
        <CardContent>
          {failed ? (
            <ErrorState error={error} />
          ) : waiting ? (
            <Skeleton className="h-56 w-full sm:h-64" />
          ) : view.newest ? (
            <LiveChart points={view.total} newest={view.newest} />
          ) : (
            <EmptyChart
              icon={<ActivityIcon />}
              title="Waiting for traffic"
              description="Samples appear a few seconds after the page is opened. If this stays empty, check that the collector started."
              className="h-56 sm:h-64"
            />
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Devices</CardTitle>
          <CardDescription>Busiest first · rates are from each device's own side</CardDescription>
        </CardHeader>
        <CardContent>
          {failed ? (
            <ErrorState error={error} />
          ) : waiting ? (
            <Skeleton className="h-40 w-full" />
          ) : view.devices.length > 0 && view.newest ? (
            <LiveDevices devices={view.devices} newest={view.newest} known={known} />
          ) : (
            <EmptyChart
              icon={<MonitorSmartphoneIcon />}
              title="No device has used the network yet"
              description={`Devices appear here as soon as they send or receive something, and stay for ${WINDOW_MINUTES} minutes.`}
            />
          )}
        </CardContent>
      </Card>
    </Page>
  )
}
