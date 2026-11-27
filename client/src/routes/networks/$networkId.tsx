import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import {
  ActivityIcon,
  ArrowRightIcon,
  GlobeIcon,
  MonitorSmartphoneIcon,
  NetworkIcon,
  PencilIcon,
} from "lucide-react"
import * as React from "react"

import { AppTrafficCard } from "@/components/app-traffic"
import { ChartCard } from "@/components/chart-card"
import { RankedBarChart, RankedTable, type RankedRow } from "@/components/charts/ranked-bar-chart"
import { TrafficChart, TrafficTable } from "@/components/charts/traffic-chart"
import { NetworkDot, NetworkName } from "@/components/network-badge"
import { NetworkDialog } from "@/components/network-dialog"
import { Page, PageHeader } from "@/components/page-header"
import { EmptyChart, ErrorState } from "@/components/query-state"
import { RangeFilter } from "@/components/range-filter"
import { StatCard } from "@/components/stat-card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { useRange } from "@/hooks/use-range"
import { useZoom } from "@/hooks/use-zoom"
import { formatBytes, formatNumber } from "@/lib/format"
import { asnLabel, countryFlag, countryName, deviceLabel, deviceLabelsByIp, parseServiceKey, serviceLabel } from "@/lib/network"
import { useNetworks } from "@/lib/networks"
import { devicesQuery, flowSummaryQuery, networksQuery, trafficQuery, useHostnames } from "@/lib/queries"
import { OTHER_KEY, toRanked } from "@/lib/ranked"
import { buildTrafficSeries, DEFAULT_RANGE, rangePhrase, granularityLabel } from "@/lib/timeseries"

/** Bars per ranked chart: the largest groups, then "Other". */
const TOP_GROUPS = 7
const TOP_DEVICES = 8

export const Route = createFileRoute("/networks/$networkId")({
  staticData: { breadcrumb: (params) => <NetworkName id={params.networkId} /> },
  loaderDeps: ({ search }) => ({ range: search.range ?? DEFAULT_RANGE }),
  loader: ({ context: { queryClient }, params, deps: { range } }) => {
    void queryClient.prefetchQuery(networksQuery())
    void queryClient.prefetchQuery(flowSummaryQuery(range, { network: Number(params.networkId) }, TOP_GROUPS))
  },
  component: NetworkDetail,
})

function NotFound() {
  return (
    <Empty className="border border-dashed">
      <EmptyHeader>
        <EmptyTitle>Network not found</EmptyTitle>
        <EmptyDescription>It may have been deleted.</EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button asChild variant="outline" size="sm">
          <Link to="/networks">All networks</Link>
        </Button>
      </EmptyContent>
    </Empty>
  )
}

function NetworkDetail() {
  const { networkId } = Route.useParams()
  const id = Number(networkId)
  const networks = useNetworks()
  const network = networks.byId.get(id)

  if (networks.isError) {
    return (
      <Page>
        <ErrorState error={networks.error} onRetry={() => void networks.refetch()} />
      </Page>
    )
  }
  if (!networks.isPending && !network) {
    return (
      <Page>
        <NotFound />
      </Page>
    )
  }
  return <NetworkPage id={id} />
}

function NetworkPage({ id }: { id: number }) {
  const range = useRange()
  const zoom = useZoom()
  const navigate = useNavigate()
  const network = useNetworks().byId.get(id)
  const scope = React.useMemo(() => ({ network: id }), [id])

  const summary = useQuery(flowSummaryQuery(range, scope, TOP_GROUPS))
  const traffic = useQuery(trafficQuery(range, scope))
  const devices = useQuery(devicesQuery(range, scope))
  // Local peers go by their device names, whatever network they are in.
  const allDevices = useQuery(devicesQuery("all"))

  const series = React.useMemo(
    () => (traffic.data ? buildTrafficSeries(traffic.data, range, traffic.dataUpdatedAt) : undefined),
    [traffic.data, traffic.dataUpdatedAt, range]
  )

  const topPeerIps = (summary.data?.TopPeers.Items ?? []).map((group) => group.Key)
  const peerHostnames = useHostnames(topPeerIps)
  const localLabels = React.useMemo(
    () => (allDevices.data ? deviceLabelsByIp(allDevices.data) : new Map<string, string>()),
    [allDevices.data]
  )
  const label = (ip: string) => localLabels.get(ip) ?? peerHostnames.data?.get(ip) ?? ip

  const destinations = toRanked(summary.data?.TopPeers, label)
  const services = toRanked(summary.data?.Services, (key) => {
    const { protocol, port } = parseServiceKey(key)
    return serviceLabel(protocol, port)
  })
  const countries = toRanked(summary.data?.Countries, (code) => `${countryFlag(code)} ${countryName(code)}`)
  const asns = toRanked(summary.data?.ASNs, (number, group) => asnLabel(number, group.Label))
  // The cards sit in two columns: the last one takes both when it would be alone.
  const cardCount = 4 + (countries.length > 0 ? 1 : 0) + (asns.length > 0 ? 1 : 0)
  const lastCardSpans = cardCount % 2 === 1

  const topDevices: RankedRow[] = (devices.data ?? []).slice(0, TOP_DEVICES).map((device) => ({
    key: device.mac,
    label: deviceLabel(device),
    download: device.download,
    upload: device.upload,
    total: device.total,
  }))
  const deviceCount = devices.data?.length ?? 0
  const addressCount = devices.data?.reduce((sum, device) => sum + device.addresses.length, 0) ?? 0

  const name = network?.Name ?? "Network"
  const period = rangePhrase(range)
  const loading = summary.isPending
  const openClient = (row: RankedRow) =>
    row.key !== OTHER_KEY && void navigate({ to: "/clients/$clientId", params: { clientId: row.key } })

  return (
    <Page>
      <PageHeader
        title={
          <span className="flex items-center gap-2">
            {network ? <NetworkDot color={network.Color} className="size-3" /> : null}
            <span className="break-all">{network ? name : <Skeleton className="h-6 w-40" />}</span>
            {network && (
              <NetworkDialog network={network}>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-muted-foreground hover:text-foreground"
                  aria-label={`Edit ${name}`}
                >
                  <PencilIcon />
                </Button>
              </NetworkDialog>
            )}
          </span>
        }
        description={
          network && (
            <div className="mt-1 flex flex-wrap items-center gap-1.5">
              <Badge variant="outline">
                <NetworkIcon data-icon="inline-start" />
                Network
              </Badge>
              {network.VLAN !== null && <Badge variant="outline">VLAN {network.VLAN}</Badge>}
              {network.Prefixes.map((prefix) => (
                <Badge key={prefix} variant="secondary" className="font-mono font-normal">
                  {prefix}
                </Badge>
              ))}
            </div>
          )
        }
      >
        <RangeFilter />
      </PageHeader>

      {summary.isError ? (
        <ErrorState error={summary.error} onRetry={() => void summary.refetch()} />
      ) : (
        <>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <StatCard
              label="Downloaded"
              seriesColor="var(--chart-1)"
              loading={loading}
              value={formatBytes(summary.data?.Download ?? 0)}
              footer={`Received by this network · ${period}`}
            />
            <StatCard
              label="Uploaded"
              seriesColor="var(--chart-2)"
              loading={loading}
              value={formatBytes(summary.data?.Upload ?? 0)}
              footer={`Sent by this network · ${period}`}
            />
            <StatCard
              label="Devices"
              icon={MonitorSmartphoneIcon}
              loading={devices.isPending}
              value={formatNumber(deviceCount)}
              footer={deviceCount > 0 ? `${formatNumber(addressCount)} addresses with traffic` : "None with traffic"}
            />
            <StatCard
              label="Connections"
              icon={NetworkIcon}
              loading={loading}
              value={formatNumber(summary.data?.Connections ?? 0)}
              footer={`With ${formatNumber(summary.data?.Peers ?? 0)} hosts`}
            />
          </div>

          <ChartCard
            title="Traffic"
            description={
              series
                ? `${granularityLabel(series.granularity)} download and upload · ${period}`
                : "Download and upload over time"
            }
            table={series?.hasData && <TrafficTable buckets={series.buckets} granularity={series.granularity} />}
          >
            {traffic.isError ? (
              <ErrorState error={traffic.error} onRetry={() => void traffic.refetch()} />
            ) : !series ? (
              <Skeleton className="h-64 w-full sm:h-72" />
            ) : series.hasData ? (
              <TrafficChart buckets={series.buckets} granularity={series.granularity} spansDays={series.spansDays}
              onZoom={zoom} />
            ) : (
              <EmptyChart
                icon={<ActivityIcon />}
                title="No traffic in this period"
                description="Check that the address blocks match your devices, or try a longer range."
                className="h-64 sm:h-72"
              />
            )}
          </ChartCard>

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <ChartCard
              title="Top devices"
              description={`By their traffic from addresses in ${name}`}
              table={topDevices.length > 0 && <RankedTable rows={topDevices} labelHeader="Device" />}
            >
              {devices.isError ? (
                <ErrorState error={devices.error} onRetry={() => void devices.refetch()} />
              ) : devices.isPending ? (
                <Skeleton className="h-72 w-full" />
              ) : topDevices.length > 0 ? (
                <div className="flex flex-col gap-2">
                  <RankedBarChart rows={topDevices} onSelect={openClient} />
                  <Button variant="ghost" size="sm" className="self-end" asChild>
                    <Link to="/clients" search={(prev) => ({ ...prev, network: id })}>
                      {deviceCount === 1 ? "The device" : `All ${formatNumber(deviceCount)} devices`}
                      <ArrowRightIcon data-icon="inline-end" />
                    </Link>
                  </Button>
                </div>
              ) : (
                <EmptyChart icon={<MonitorSmartphoneIcon />} title="No devices with traffic in this period" />
              )}
            </ChartCard>

            <ChartCard
              title="Top destinations"
              description="Hosts this network exchanged the most with"
              table={destinations.length > 0 && <RankedTable rows={destinations} labelHeader="Host" />}
            >
              {loading ? (
                <Skeleton className="h-72 w-full" />
              ) : destinations.length > 0 ? (
                <RankedBarChart rows={destinations} onSelect={openClient} />
              ) : (
                <EmptyChart icon={<GlobeIcon />} title="No connections in this period" />
              )}
            </ChartCard>

            <ChartCard
              title="Services"
              description="Traffic by protocol and port"
              table={services.length > 0 && <RankedTable rows={services} labelHeader="Service" />}
            >
              {loading ? (
                <Skeleton className="h-72 w-full" />
              ) : services.length > 0 ? (
                <RankedBarChart rows={services} />
              ) : (
                <EmptyChart icon={<NetworkIcon />} title="No connections in this period" />
              )}
            </ChartCard>

            <AppTrafficCard client={scope} scope={name} />

            {countries.length > 0 && (
              <ChartCard
                className={lastCardSpans && asns.length === 0 ? "lg:col-span-2" : undefined}
                title="Countries"
                description="Where remote hosts are located, from GeoIP"
                table={<RankedTable rows={countries} labelHeader="Country" />}
              >
                <RankedBarChart rows={countries} />
              </ChartCard>
            )}

            {asns.length > 0 && (
              <ChartCard
                className={lastCardSpans ? "lg:col-span-2" : undefined}
                title="Networks"
                description="Which organizations' networks remote hosts are in, from GeoIP"
                table={<RankedTable rows={asns} labelHeader="Network" />}
              >
                <RankedBarChart rows={asns} />
              </ChartCard>
            )}
          </div>
        </>
      )}
    </Page>
  )
}
