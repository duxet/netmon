import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import {
  ArrowDownLeftIcon,
  ArrowUpRightIcon,
  CableIcon,
  GlobeIcon,
  NetworkIcon,
  SearchIcon,
  ServerIcon,
} from "lucide-react"
import * as React from "react"

import { ChartCard } from "@/components/chart-card"
import { RankedBarChart, RankedTable, type RankedRow } from "@/components/charts/ranked-bar-chart"
import { DataTable } from "@/components/data-table"
import { Page, PageHeader } from "@/components/page-header"
import { EmptyChart, ErrorState } from "@/components/query-state"
import { ShareBar } from "@/components/share-bar"
import { StatCard } from "@/components/stat-card"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardHeader } from "@/components/ui/card"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { formatBytes, formatCompact, formatNumber } from "@/lib/format"
import {
  aggregateBy,
  countryFlag,
  countryName,
  findDevice,
  parseSubject,
  protocolName,
  serviceName,
  toSubjectFlows,
  topWithOther,
  type Aggregate,
  type SubjectFlow,
} from "@/lib/network"
import { devicesQuery, flowsQuery, useHostnames } from "@/lib/queries"
import { createColumns } from "@/lib/table"

export const Route = createFileRoute("/clients/$clientId")({
  staticData: { breadcrumb: (params) => params.clientId },
  loader: ({ context: { queryClient }, params }) => {
    const subject = parseSubject(params.clientId)
    void queryClient.prefetchQuery(
      flowsQuery(subject.kind === "mac" ? { mac: subject.mac } : { ip: subject.ip })
    )
  },
  component: ClientDetail,
})

const TOP_ROWS = 8

function toRanked(rows: Aggregate[]): RankedRow[] {
  return rows.map(({ key, label, download, upload, total }) => ({
    key,
    label,
    download,
    upload,
    total,
  }))
}

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

const column = createColumns<SubjectFlow>()

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
          sortFn: "alphanumeric",
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
                </div>
              </div>
            )
          },
        }),
        column.accessor("service", {
          header: "Service",
          sortFn: "text",
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
        column.accessor("packets", {
          header: "Packets",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => (
            <span title={formatNumber(getValue())}>{formatCompact(getValue())}</span>
          ),
          meta: { align: "right", className: "hidden lg:table-cell" },
        }),
        column.accessor("total", {
          header: "Share",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => <ShareBar value={getValue()} max={subjectTotal} />,
          meta: { className: "hidden md:table-cell w-40" },
        }),
      ]),
    [hostnames, subjectTotal]
  )
}

function matchesFlow(flow: SubjectFlow, query: string, hostnames: Map<string, string | null>) {
  if (!query) return true
  const needle = query.toLowerCase()
  return (
    flow.peer.IPAddress.includes(needle) ||
    flow.service.toLowerCase().includes(needle) ||
    String(flow.port) === needle ||
    (flow.peer.Country?.toLowerCase() === needle) ||
    (hostnames.get(flow.peer.IPAddress)?.toLowerCase().includes(needle) ?? false)
  )
}

function ClientDetail() {
  const { clientId } = Route.useParams()
  const navigate = useNavigate()
  const subject = React.useMemo(() => parseSubject(clientId), [clientId])

  const flowsResult = useQuery(
    flowsQuery(subject.kind === "mac" ? { mac: subject.mac } : { ip: subject.ip })
  )
  const devices = useQuery(devicesQuery)
  const device = devices.data && findDevice(devices.data, clientId)

  const flows = React.useMemo(
    () => (flowsResult.data ? toSubjectFlows(flowsResult.data, subject) : []),
    [flowsResult.data, subject]
  )

  const totals = React.useMemo(
    () =>
      flows.reduce(
        (acc, flow) => ({
          download: acc.download + flow.download,
          upload: acc.upload + flow.upload,
          packets: acc.packets + flow.packets,
        }),
        { download: 0, upload: 0, packets: 0 }
      ),
    [flows]
  )

  const peers = React.useMemo(() => aggregateBy(flows, (flow) => flow.peer.IPAddress), [flows])
  const topPeerIps = peers.slice(0, TOP_ROWS).map((peer) => peer.key)

  // Reverse DNS is resolved server-side one address at a time, so only ask
  // for the hosts on screen: the top destinations and the visible table page.
  const [pageIps, setPageIps] = React.useState<string[]>([])
  const topHostnames = useHostnames(topPeerIps)
  const pageHostnames = useHostnames(pageIps)
  const hostnames = React.useMemo(
    () => new Map([...(topHostnames.data ?? []), ...(pageHostnames.data ?? [])]),
    [topHostnames.data, pageHostnames.data]
  )

  const destinations = toRanked(
    topWithOther(
      peers.map((peer) => ({ ...peer, label: hostnames.get(peer.key) ?? peer.key })),
      TOP_ROWS
    )
  )
  const services = toRanked(topWithOther(aggregateBy(flows, (flow) => flow.service), TOP_ROWS))
  const countries = toRanked(
    topWithOther(
      aggregateBy(
        flows,
        (flow) => flow.peer.Country || null,
        (code) => `${countryFlag(code)} ${countryName(code)}`
      ),
      TOP_ROWS
    )
  )

  const [query, setQuery] = React.useState("")
  const deferredQuery = React.useDeferredValue(query.trim())
  const filtered = React.useMemo(
    () => flows.filter((flow) => matchesFlow(flow, deferredQuery, hostnames)),
    [flows, deferredQuery, hostnames]
  )

  const subjectTotal = totals.download + totals.upload
  const columns = useFlowColumns(hostnames, subjectTotal)
  const onPageRowsChange = React.useCallback(
    (rows: SubjectFlow[]) => setPageIps(rows.map((row) => row.peer.IPAddress)),
    []
  )

  const isLocal = subject.kind === "mac" || device !== undefined
  const title = device?.hostname ?? (subject.kind === "ip" ? (hostnames.get(subject.ip) ?? clientId) : clientId)
  const loading = flowsResult.isPending

  const openPeer = (row: RankedRow) =>
    row.key !== "__other__" &&
    void navigate({ to: "/clients/$clientId", params: { clientId: row.key } })

  return (
    <Page>
      <PageHeader
        title={<span className="break-all">{title}</span>}
        description={
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            <Badge variant="outline">
              {isLocal ? <CableIcon data-icon="inline-start" /> : <GlobeIcon data-icon="inline-start" />}
              {isLocal ? "Local device" : "Remote host"}
            </Badge>
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
          </div>
        }
      />

      {flowsResult.isError ? (
        <ErrorState error={flowsResult.error} onRetry={() => void flowsResult.refetch()} />
      ) : (
        <>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <StatCard
              label="Downloaded"
              seriesColor="var(--chart-1)"
              loading={loading}
              value={formatBytes(totals.download)}
              footer="Received by this host · all time"
            />
            <StatCard
              label="Uploaded"
              seriesColor="var(--chart-2)"
              loading={loading}
              value={formatBytes(totals.upload)}
              footer="Sent by this host · all time"
            />
            <StatCard
              label="Connections"
              icon={NetworkIcon}
              loading={loading}
              value={formatNumber(flows.length)}
              footer={`With ${formatNumber(peers.length)} remote hosts`}
            />
            <StatCard
              label="Packets"
              icon={ServerIcon}
              loading={loading}
              value={formatCompact(totals.packets)}
              footer={`${formatNumber(totals.packets)} in total`}
            />
          </div>

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
                <EmptyChart icon={<GlobeIcon />} title="No connections recorded" />
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
                <EmptyChart icon={<NetworkIcon />} title="No connections recorded" />
              )}
            </ChartCard>
            {countries.length > 0 && (
              <ChartCard
                className="lg:col-span-2"
                title="Countries"
                description="Where remote hosts are located, from GeoIP"
                table={<RankedTable rows={countries} labelHeader="Country" />}
              >
                <RankedBarChart rows={countries} />
              </ChartCard>
            )}
          </div>

          <Card>
            <CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="flex flex-col gap-1">
                <h2 className="font-heading text-base font-medium">Connections</h2>
                <p className="text-sm text-muted-foreground">
                  Flows grouped by remote host, protocol and port
                </p>
              </div>
              <InputGroup className="sm:max-w-xs">
                <InputGroupAddon>
                  <SearchIcon />
                </InputGroupAddon>
                <InputGroupInput
                  type="search"
                  placeholder="Filter by host, port or service"
                  aria-label="Filter connections"
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                />
              </InputGroup>
            </CardHeader>
            <CardContent>
              <DataTable
                data={filtered}
                columns={columns}
                getRowId={(flow) => flow.id}
                initialSorting={[{ id: "total", desc: true }]}
                loading={loading}
                resetKey={`${clientId}|${deferredQuery}`}
                onPageRowsChange={onPageRowsChange}
                emptyMessage={
                  deferredQuery
                    ? `No connections match “${deferredQuery}”.`
                    : "No connections recorded for this host."
                }
              />
            </CardContent>
          </Card>
        </>
      )}
    </Page>
  )
}
