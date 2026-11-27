import type { PaginationState, SortingState } from "@tanstack/react-table"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import {
  ActivityIcon,
  ArrowDownLeftIcon,
  ArrowUpRightIcon,
  BellPlusIcon,
  CableIcon,
  GlobeIcon,
  NetworkIcon,
  SearchIcon,
  ServerIcon,
  ShieldIcon,
  WifiIcon,
} from "lucide-react"
import * as React from "react"

import { AlertRuleDialog } from "@/components/alert-rule-dialog"
import { AppTrafficCard } from "@/components/app-traffic"
import { ChartCard } from "@/components/chart-card"
import { RankedBarChart, RankedTable, type RankedRow } from "@/components/charts/ranked-bar-chart"
import { TrafficChart, TrafficTable } from "@/components/charts/traffic-chart"
import { DataTable } from "@/components/data-table"
import { DeviceIcon } from "@/components/device-icon"
import { DeviceVendor } from "@/components/device-vendor"
import { EditDeviceButton } from "@/components/edit-device"
import { DeviceNetworks } from "@/components/network-badge"
import { Page, PageHeader } from "@/components/page-header"
import { EmptyChart, ErrorState } from "@/components/query-state"
import { RangeFilter } from "@/components/range-filter"
import { ShareBar } from "@/components/share-bar"
import { StatCard } from "@/components/stat-card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader } from "@/components/ui/card"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useDebouncedValue } from "@/hooks/use-debounced-value"
import { useRange } from "@/hooks/use-range"
import type { AutonomousSystem, FlowSort, UniFiClient } from "@/lib/api"
import { formatBytes, formatCompact, formatNumber } from "@/lib/format"
import {
  asnLabel,
  asnNumber,
  countryFlag,
  countryName,
  deviceLabelsByIp,
  findDevice,
  parseServiceKey,
  parseSubject,
  protocolName,
  serviceLabel,
  serviceName,
  toSubjectFlows,
  unifiConnection,
  type Subject,
  type SubjectFlow,
} from "@/lib/network"
import {
  devicesQuery,
  flowSummaryQuery,
  flowsPageQuery,
  trafficQuery,
  useHostnames,
  type ClientFilter,
} from "@/lib/queries"
import { OTHER_KEY, toRanked } from "@/lib/ranked"
import { createColumns } from "@/lib/table"
import { buildTrafficSeries, DEFAULT_RANGE, rangePhrase } from "@/lib/timeseries"

/** Bars per ranked chart: the largest groups, then "Other". */
const TOP_GROUPS = 7
const PAGE_SIZE = 15

function clientFilterOf(subject: Subject): ClientFilter {
  return subject.kind === "mac" ? { mac: subject.mac } : { ip: subject.ip }
}

/** How UniFi says the device is connected, and since when. */
function UniFiBadge({ unifi }: { unifi: UniFiClient }) {
  const Icon = unifi.Connection === "WIRELESS" ? WifiIcon : unifi.Connection === "VPN" ? ShieldIcon : CableIcon
  const since = unifi.ConnectedAt ? new Date(unifi.ConnectedAt).toLocaleString() : null
  const title = unifi.Connected
    ? `Connected${since ? ` since ${since}` : ""}, according to UniFi`
    : `Not connected; UniFi last saw it ${new Date(unifi.LastSeen).toLocaleString()}`
  return (
    <Badge variant="outline" className={unifi.Connected ? "font-normal" : "font-normal text-muted-foreground"} title={title}>
      <Icon data-icon="inline-start" />
      {unifiConnection(unifi)}
      {!unifi.Connected && " (offline)"}
    </Badge>
  )
}

export const Route = createFileRoute("/clients/$clientId")({
  staticData: { breadcrumb: (params) => params.clientId },
  loaderDeps: ({ search }) => ({ range: search.range ?? DEFAULT_RANGE }),
  loader: ({ context: { queryClient }, params, deps: { range } }) => {
    const client = clientFilterOf(parseSubject(params.clientId))
    void queryClient.prefetchQuery(flowSummaryQuery(range, client, TOP_GROUPS))
  },
  component: ClientDetail,
})

function Country({ code }: { code: string | null | undefined }) {
  if (!code) return null
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="text-xs text-muted-foreground" tabIndex={0}>
          {countryFlag(code)} {code.toUpperCase()}
        </span>
      </TooltipTrigger>
      <TooltipContent>{countryName(code)}</TooltipContent>
    </Tooltip>
  )
}

function Asn({ asn }: { asn: AutonomousSystem | null | undefined }) {
  if (!asn) return null
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="max-w-48 truncate text-xs text-muted-foreground" tabIndex={0}>
          {asnNumber(asn.Number)}
          {asn.Organization && ` ${asn.Organization}`}
        </span>
      </TooltipTrigger>
      <TooltipContent>{asnLabel(asn.Number, asn.Organization)}</TooltipContent>
    </Tooltip>
  )
}

const column = createColumns<SubjectFlow>()

// Column ids of the sortable columns are the API's sort keys.
function useFlowColumns(hostnames: Map<string, string | null>, subjectTotal: number) {
  return React.useMemo(
    () =>
      column.columns([
        column.accessor("direction", {
          header: "",
          enableSorting: false,
          cell: ({ getValue }) => {
            const outbound = getValue() === "outbound"
            const Icon = outbound ? ArrowUpRightIcon : ArrowDownLeftIcon
            const label = outbound ? "Outbound: opened by this client" : "Inbound: opened by the peer"
            return (
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="inline-flex text-muted-foreground" tabIndex={0} aria-label={label}>
                    <Icon className="size-4" />
                  </span>
                </TooltipTrigger>
                <TooltipContent>{label}</TooltipContent>
              </Tooltip>
            )
          },
          meta: { className: "w-8" },
        }),
        column.accessor((flow) => flow.peer.IPAddress, {
          id: "peer",
          header: "Remote host",
          enableSorting: false,
          cell: ({ row }) => {
            const { peer } = row.original
            const hostname = hostnames.get(peer.IPAddress)
            return (
              <div className="flex min-w-0 flex-col">
                <Link
                  to="/clients/$clientId"
                  params={{ clientId: peer.IPAddress }}
                  className="truncate font-mono text-sm hover:underline"
                >
                  {peer.IPAddress}
                </Link>
                <div className="flex min-w-0 items-center gap-2">
                  {hostname && (
                    <span className="max-w-64 truncate text-xs text-muted-foreground" title={hostname}>
                      {hostname}
                    </span>
                  )}
                  <Country code={peer.Country} />
                  <Asn asn={peer.ASN} />
                </div>
              </div>
            )
          },
        }),
        column.accessor("service", {
          header: "Service",
          enableSorting: false,
          cell: ({ row }) => {
            const { protocol, port } = row.original
            const name = serviceName(protocol, port)
            const proto = protocolName(protocol)
            return (
              <div className="flex flex-col">
                <span>{name ?? (port ? `Port ${port}` : proto)}</span>
                {port > 0 && (
                  <span className="text-xs text-muted-foreground">
                    {proto} {port}
                  </span>
                )}
              </div>
            )
          },
        }),
        column.accessor("download", {
          header: "Download",
          sortDescFirst: true,
          cell: ({ getValue }) => formatBytes(getValue()),
          meta: { align: "right" },
        }),
        column.accessor("upload", {
          header: "Upload",
          sortDescFirst: true,
          cell: ({ getValue }) => formatBytes(getValue()),
          meta: { align: "right" },
        }),
        column.accessor("packets", {
          header: "Packets",
          sortDescFirst: true,
          cell: ({ getValue }) => (
            <span title={formatNumber(getValue())}>{formatCompact(getValue())}</span>
          ),
          meta: { align: "right", className: "hidden lg:table-cell" },
        }),
        column.accessor("total", {
          header: "Share",
          sortDescFirst: true,
          cell: ({ getValue }) => <ShareBar value={getValue()} max={subjectTotal} />,
          meta: { className: "hidden md:table-cell w-40" },
        }),
      ]),
    [hostnames, subjectTotal]
  )
}

const SORT_KEYS = new Set<string>(["total", "download", "upload", "packets"])

function ClientDetail() {
  const { clientId } = Route.useParams()
  const range = useRange()
  const navigate = useNavigate()
  const subject = React.useMemo(() => parseSubject(clientId), [clientId])
  const client = React.useMemo(() => clientFilterOf(subject), [subject])

  const summary = useQuery(flowSummaryQuery(range, client, TOP_GROUPS))
  const traffic = useQuery(trafficQuery(range, client))
  // Who the client is doesn't depend on the range, so look it up in all time.
  const devices = useQuery(devicesQuery("all"))
  const device = devices.data && findDevice(devices.data, clientId)

  const series = React.useMemo(
    () => (traffic.data ? buildTrafficSeries(traffic.data, range, traffic.dataUpdatedAt) : undefined),
    [traffic.data, traffic.dataUpdatedAt, range]
  )

  // Connections: one page at a time from the server, sorted and searched there.
  const [query, setQuery] = React.useState("")
  const search = useDebouncedValue(query.trim(), 300)
  const [sorting, setSorting] = React.useState<SortingState>([{ id: "total", desc: true }])
  const [pagination, setPagination] = React.useState<PaginationState>({ pageIndex: 0, pageSize: PAGE_SIZE })

  // A new search, range, client or order starts from the first page.
  const pageResetKey = `${search}|${range}|${clientId}|${JSON.stringify(sorting)}`
  const [lastPageResetKey, setLastPageResetKey] = React.useState(pageResetKey)
  if (pageResetKey !== lastPageResetKey) {
    setLastPageResetKey(pageResetKey)
    setPagination((state) => ({ ...state, pageIndex: 0 }))
  }

  const sort = sorting[0]
  const flowsPage = useQuery(
    flowsPageQuery(range, client, {
      q: search || undefined,
      sort: sort && SORT_KEYS.has(sort.id) ? (sort.id as FlowSort) : "total",
      order: sort && !sort.desc ? "asc" : "desc",
      limit: pagination.pageSize,
      offset: pagination.pageIndex * pagination.pageSize,
    })
  )
  const flows = React.useMemo(
    () => (flowsPage.data ? toSubjectFlows(flowsPage.data.flows, subject) : []),
    [flowsPage.data, subject]
  )

  // Reverse DNS is resolved server-side one address at a time, so only ask
  // for the hosts on screen: the top destinations and the visible page.
  const topPeerIps = (summary.data?.TopPeers.Items ?? []).map((group) => group.Key)
  const topHostnames = useHostnames(topPeerIps)
  const pageHostnames = useHostnames(flows.map((flow) => flow.peer.IPAddress))
  // Local peers go by their device names (the user's, else DNS) instead.
  const localLabels = React.useMemo(
    () => (devices.data ? deviceLabelsByIp(devices.data) : new Map<string, string>()),
    [devices.data]
  )
  const hostnames = React.useMemo(
    () =>
      new Map<string, string | null>([
        ...(topHostnames.data ?? []),
        ...(pageHostnames.data ?? []),
        ...localLabels,
      ]),
    [topHostnames.data, pageHostnames.data, localLabels]
  )

  const destinations = toRanked(summary.data?.TopPeers, (ip) => hostnames.get(ip) ?? ip)
  const services = toRanked(summary.data?.Services, (key) => {
    const { protocol, port } = parseServiceKey(key)
    return serviceLabel(protocol, port)
  })
  const countries = toRanked(summary.data?.Countries, (code) => `${countryFlag(code)} ${countryName(code)}`)
  const asns = toRanked(summary.data?.ASNs, (number, group) => asnLabel(number, group.Label))

  const subjectTotal = (summary.data?.Download ?? 0) + (summary.data?.Upload ?? 0)
  const columns = useFlowColumns(hostnames, subjectTotal)

  const isLocal = subject.kind === "mac" || device !== undefined
  // The cards sit in two columns: the last one takes both when it would be alone.
  const cardCount = 2 + (isLocal ? 1 : 0) + (countries.length > 0 ? 1 : 0) + (asns.length > 0 ? 1 : 0)
  const lastCardSpans = cardCount % 2 === 1
  const title =
    device?.name ??
    device?.hostname ??
    (subject.kind === "ip" ? (hostnames.get(subject.ip) ?? clientId) : clientId)
  const loading = summary.isPending
  const period = rangePhrase(range)

  const openPeer = (row: RankedRow) =>
    row.key !== OTHER_KEY &&
    void navigate({ to: "/clients/$clientId", params: { clientId: row.key } })

  return (
    <Page>
      <PageHeader
        title={
          <span className="flex items-center gap-1">
            {device && <DeviceIcon icon={device.icon} className="mr-1.5 size-9 [&_svg]:size-5" />}
            <span className="break-all">{title}</span>
            {device && <EditDeviceButton device={device} />}
          </span>
        }
        description={
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            <Badge variant="outline">
              {isLocal ? <CableIcon data-icon="inline-start" /> : <GlobeIcon data-icon="inline-start" />}
              {isLocal ? "Local device" : "Remote host"}
            </Badge>
            {device && <DeviceNetworks ids={device.networks} />}
            {device?.name && device.hostname && (
              <Badge
                variant="outline"
                className="font-normal"
                title={device.unifi?.Name ? "Name in UniFi" : "Hostname from reverse DNS"}
              >
                {device.hostname}
              </Badge>
            )}
            {device?.unifi && <UniFiBadge unifi={device.unifi} />}
            {device && (
              <Badge variant="secondary" className="font-mono font-normal" asChild>
                <Link to="/clients/$clientId" params={{ clientId: device.mac }}>
                  {device.mac}
                </Link>
              </Badge>
            )}
            {(device?.addresses.map((address) => address.ip) ??
              (subject.kind === "ip" ? [subject.ip] : [])
            ).map((ip) => (
              <Badge key={ip} variant="secondary" className="font-mono font-normal" asChild>
                <Link to="/clients/$clientId" params={{ clientId: ip }}>
                  {ip}
                </Link>
              </Badge>
            ))}
            {device && <DeviceVendor device={device} className="ml-1 text-xs text-muted-foreground" />}
          </div>
        }
      >
        {device && (
          <AlertRuleDialog
            preset={{ name: `${title} traffic`.slice(0, 64), type: "traffic_threshold", macs: [device.mac] }}
          >
            <Button variant="outline" size="sm">
              <BellPlusIcon data-icon="inline-start" />
              Create alert
            </Button>
          </AlertRuleDialog>
        )}
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
              footer={`Received by this host · ${period}`}
            />
            <StatCard
              label="Uploaded"
              seriesColor="var(--chart-2)"
              loading={loading}
              value={formatBytes(summary.data?.Upload ?? 0)}
              footer={`Sent by this host · ${period}`}
            />
            <StatCard
              label="Connections"
              icon={NetworkIcon}
              loading={loading}
              value={formatNumber(summary.data?.Connections ?? 0)}
              footer={`With ${formatNumber(summary.data?.Peers ?? 0)} remote hosts`}
            />
            <StatCard
              label="Packets"
              icon={ServerIcon}
              loading={loading}
              value={formatCompact(summary.data?.Packets ?? 0)}
              footer={`${formatNumber(summary.data?.Packets ?? 0)} in total`}
            />
          </div>

          <ChartCard
            title="Traffic"
            description={
              series
                ? `${series.granularity === "day" ? "Daily" : "Hourly"} download and upload · ${period}`
                : "Download and upload over time"
            }
            table={series?.hasData && <TrafficTable buckets={series.buckets} granularity={series.granularity} />}
          >
            {traffic.isError ? (
              <ErrorState error={traffic.error} onRetry={() => void traffic.refetch()} />
            ) : !series ? (
              <Skeleton className="h-64 w-full sm:h-72" />
            ) : series.hasData ? (
              <TrafficChart buckets={series.buckets} granularity={series.granularity} spansDays={range !== "24h"} />
            ) : (
              <EmptyChart icon={<ActivityIcon />} title="No traffic in this period" className="h-64 sm:h-72" />
            )}
          </ChartCard>

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <ChartCard
              title="Top destinations"
              description="Remote hosts by traffic"
              table={destinations.length > 0 && <RankedTable rows={destinations} labelHeader="Host" />}
            >
              {loading ? (
                <Skeleton className="h-72 w-full" />
              ) : destinations.length > 0 ? (
                <RankedBarChart rows={destinations} onSelect={openPeer} />
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
            {isLocal && <AppTrafficCard client={client} scope={title} />}
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

          <Card>
            <CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="flex flex-col gap-1">
                <h2 className="font-heading text-base font-medium">Connections</h2>
                <p className="text-sm text-muted-foreground">
                  Flows grouped by remote host, protocol and port · {period}
                </p>
              </div>
              <InputGroup className="sm:max-w-xs">
                <InputGroupAddon>
                  <SearchIcon />
                </InputGroupAddon>
                <InputGroupInput
                  type="search"
                  placeholder="Filter by IP, port or protocol"
                  aria-label="Filter connections"
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                />
              </InputGroup>
            </CardHeader>
            <CardContent>
              {flowsPage.isError ? (
                <ErrorState error={flowsPage.error} onRetry={() => void flowsPage.refetch()} />
              ) : (
                <DataTable
                  data={flows}
                  columns={columns}
                  getRowId={(flow) => flow.id}
                  loading={flowsPage.isPending}
                  fetching={flowsPage.isPlaceholderData}
                  manual={{
                    rowCount: flowsPage.data?.total ?? 0,
                    pagination,
                    onPaginationChange: setPagination,
                    sorting,
                    onSortingChange: setSorting,
                  }}
                  emptyMessage={
                    search ? `No connections match “${search}”.` : "No connections in this period."
                  }
                />
              )}
            </CardContent>
          </Card>
        </>
      )}
    </Page>
  )
}
