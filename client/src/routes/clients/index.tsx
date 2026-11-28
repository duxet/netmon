import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { SearchIcon } from "lucide-react"
import * as React from "react"

import { DataTable } from "@/components/data-table"
import { Page, PageHeader } from "@/components/page-header"
import { DeviceIcon } from "@/components/device-icon"
import { DeviceVendor } from "@/components/device-vendor"
import { EditDeviceButton } from "@/components/edit-device"
import { DeviceNetworks, NetworkDot } from "@/components/network-badge"
import { ErrorState } from "@/components/query-state"
import { RangeFilter } from "@/components/range-filter"
import { ShareBar } from "@/components/share-bar"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardHeader } from "@/components/ui/card"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useRange } from "@/hooks/use-range"
import { formatBytes, formatCompact, formatNumber } from "@/lib/format"
import type { Network } from "@/lib/api"
import type { Device } from "@/lib/network"
import { deviceLabel, isRandomizedMac, unifiConnection } from "@/lib/network"
import { NO_NETWORK, useNetworks } from "@/lib/networks"
import { devicesQuery } from "@/lib/queries"
import { createColumns } from "@/lib/table"
import { DEFAULT_RANGE, rangePhrase } from "@/lib/timeseries"

/** Devices in one network, by its ID, or in none. */
type NetworkFilter = number | typeof NO_NETWORK

export const Route = createFileRoute("/clients/")({
  validateSearch: (search: Record<string, unknown>): { network?: NetworkFilter } => {
    if (search.network === NO_NETWORK) return { network: NO_NETWORK }
    const id = Number(search.network)
    return Number.isInteger(id) && id > 0 ? { network: id } : {}
  },
  loaderDeps: ({ search }) => ({ range: search.range ?? DEFAULT_RANGE }),
  loader: ({ context: { queryClient }, deps: { range } }) =>
    void queryClient.prefetchQuery(devicesQuery(range)),
  component: Clients,
})

const VISIBLE_ADDRESSES = 2

function Addresses({ device }: { device: Device }) {
  const visible = device.addresses.slice(0, VISIBLE_ADDRESSES)
  const hidden = device.addresses.slice(VISIBLE_ADDRESSES)
  return (
    <div className="flex flex-wrap items-center gap-1">
      {visible.map((address) => (
        <Badge key={address.ip} variant="secondary" className="font-mono font-normal" asChild>
          <Link to="/clients/$clientId" params={{ clientId: address.ip }}>
            {address.ip}
          </Link>
        </Badge>
      ))}
      {hidden.length > 0 && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Badge variant="outline" tabIndex={0}>
              +{hidden.length}
            </Badge>
          </TooltipTrigger>
          <TooltipContent className="font-mono">
            {hidden.map((address) => (
              <div key={address.ip}>{address.ip}</div>
            ))}
          </TooltipContent>
        </Tooltip>
      )}
    </div>
  )
}

const column = createColumns<Device>()

function inNetwork(device: Device, filter: NetworkFilter | undefined) {
  if (filter === undefined) return true
  if (filter === NO_NETWORK) return device.networks.length === 0
  return device.networks.includes(filter)
}

function useColumns(totalTraffic: number, networks: Map<number, Network>) {
  const showNetworks = networks.size > 0
  return React.useMemo(
    () =>
      column.columns([
        column.accessor((device) => deviceLabel(device), {
          id: "client",
          header: "Client",
          sortFn: "text",
          cell: ({ row }) => {
            const device = row.original
            return (
              <div className="flex min-w-0 items-center gap-3">
                <DeviceIcon icon={device.icon} />
                <div className="flex min-w-0 flex-col">
                  <Link
                    to="/clients/$clientId"
                    params={{ clientId: device.mac }}
                    className="truncate font-medium hover:underline"
                  >
                    {device.name ?? device.hostname ?? "Unknown device"}
                  </Link>
                  <span className="truncate text-xs text-muted-foreground">
                    {device.name && device.hostname && `${device.hostname} · `}
                    <span className="font-mono">{device.mac}</span>
                    {device.unifi && ` · ${unifiConnection(device.unifi)}`}
                    {(device.vendor || isRandomizedMac(device.mac)) && (
                      <>
                        {" · "}
                        <DeviceVendor device={device} />
                      </>
                    )}
                  </span>
                </div>
              </div>
            )
          },
        }),
        ...(showNetworks
          ? [
              column.accessor((device) => networks.get(device.networks[0])?.Name ?? "", {
                id: "network",
                header: "Network",
                sortFn: "text",
                cell: ({ row }) => <DeviceNetworks ids={row.original.networks} />,
                meta: { className: "hidden md:table-cell" },
              }),
            ]
          : []),
        column.display({
          id: "addresses",
          header: "Addresses",
          cell: ({ row }) => <Addresses device={row.original} />,
          meta: { className: "hidden md:table-cell" },
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
          header: "Share of traffic",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => (
            <ShareBar value={getValue()} max={totalTraffic} className="min-w-32" />
          ),
          meta: { className: "hidden sm:table-cell w-48" },
        }),
        column.display({
          id: "actions",
          header: () => <span className="sr-only">Actions</span>,
          cell: ({ row }) => <EditDeviceButton device={row.original} />,
          meta: { className: "w-10" },
        }),
      ]),
    [totalTraffic, networks, showNetworks]
  )
}

function matches(device: Device, query: string, networks: Map<number, Network>) {
  if (!query) return true
  const needle = query.toLowerCase()
  return (
    device.mac.includes(needle) ||
    device.networks.some((id) => networks.get(id)?.Name.toLowerCase().includes(needle)) ||
    (device.name?.toLowerCase().includes(needle) ?? false) ||
    (device.vendor?.toLowerCase().includes(needle) ?? false) ||
    (device.hostname?.toLowerCase().includes(needle) ?? false) ||
    (device.unifi?.Uplink.toLowerCase().includes(needle) ?? false) ||
    device.addresses.some(
      (address) =>
        address.ip.includes(needle) || (address.hostname?.toLowerCase().includes(needle) ?? false)
    )
  )
}

/** "1 device", "12 devices" */
function count(n: number, one: string, many = `${one}s`) {
  return `${formatNumber(n)} ${n === 1 ? one : many}`
}

/** Picks the network whose devices are listed, or devices in none. */
function NetworkSelect({ value, networks }: { value: NetworkFilter | undefined; networks: Network[] }) {
  const navigate = useNavigate({ from: Route.fullPath })
  const set = (next: string) =>
    void navigate({
      search: (prev) => ({
        ...prev,
        network: next === "all" ? undefined : next === NO_NETWORK ? NO_NETWORK : Number(next),
      }),
      replace: true,
    })

  return (
    <Select value={value === undefined ? "all" : String(value)} onValueChange={set}>
      <SelectTrigger className="w-full sm:w-48" aria-label="Network">
        <SelectValue />
      </SelectTrigger>
      <SelectContent position="popper">
        <SelectItem value="all">All networks</SelectItem>
        <SelectSeparator />
        {networks.map((network) => (
          <SelectItem key={network.ID} value={String(network.ID)}>
            <NetworkDot color={network.Color} />
            {network.Name}
          </SelectItem>
        ))}
        <SelectSeparator />
        <SelectItem value={NO_NETWORK}>Not in a network</SelectItem>
      </SelectContent>
    </Select>
  )
}

function Clients() {
  const range = useRange()
  const { network: networkFilter } = Route.useSearch()
  const devices = useQuery(devicesQuery(range))
  const networks = useNetworks()
  const [query, setQuery] = React.useState("")
  const deferredQuery = React.useDeferredValue(query.trim())

  const all = React.useMemo(() => devices.data ?? [], [devices.data])
  const inScope = React.useMemo(
    () => all.filter((device) => inNetwork(device, networkFilter)),
    [all, networkFilter]
  )
  const filtered = React.useMemo(
    () => inScope.filter((device) => matches(device, deferredQuery, networks.byId)),
    [inScope, deferredQuery, networks.byId]
  )
  const totalTraffic = all.reduce((sum, device) => sum + device.total, 0)
  const scopeTraffic = inScope.reduce((sum, device) => sum + device.total, 0)
  const addressCount = inScope.reduce((sum, device) => sum + device.addresses.length, 0)
  const columns = useColumns(totalTraffic, networks.byId)

  const scopeName =
    networkFilter === undefined
      ? undefined
      : networkFilter === NO_NETWORK
        ? "not in a network"
        : `in ${networks.byId.get(networkFilter)?.Name ?? "this network"}`

  return (
    <Page>
      <PageHeader
        title="Clients"
        description={
          devices.data
            ? `${count(inScope.length, "device")}${scopeName ? ` ${scopeName}` : ""} · ${count(addressCount, "address", "addresses")} · ${formatBytes(scopeTraffic)} · ${rangePhrase(range)}`
            : "Devices seen on your network, grouped by MAC address"
        }
      >
        <RangeFilter />
      </PageHeader>

      {devices.isError ? (
        <ErrorState error={devices.error} onRetry={() => void devices.refetch()} />
      ) : (
        <Card>
          <CardHeader className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <InputGroup className="sm:max-w-sm">
              <InputGroupAddon>
                <SearchIcon />
              </InputGroupAddon>
              <InputGroupInput
                type="search"
                placeholder="Search by name, vendor, IP or MAC address"
                aria-label="Search clients"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
            </InputGroup>
            {(networks.list.length > 0 || networkFilter !== undefined) && (
              <NetworkSelect value={networkFilter} networks={networks.list} />
            )}
          </CardHeader>
          <CardContent>
            <DataTable
              data={filtered}
              columns={columns}
              getRowId={(device) => device.mac}
              initialSorting={[{ id: "total", desc: true }]}
              loading={devices.isPending}
              resetKey={`${deferredQuery}|${networkFilter}`}
              pageSize={20}
              emptyMessage={
                deferredQuery
                  ? `No clients match “${deferredQuery}”.`
                  : scopeName
                    ? `No clients ${scopeName} active in this period.`
                    : "No clients active in this period."
              }
            />
          </CardContent>
        </Card>
      )}
    </Page>
  )
}
