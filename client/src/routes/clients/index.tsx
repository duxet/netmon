import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { SearchIcon } from "lucide-react"
import * as React from "react"

import { DataTable } from "@/components/data-table"
import { Page, PageHeader } from "@/components/page-header"
import { ErrorState } from "@/components/query-state"
import { RangeFilter } from "@/components/range-filter"
import { RenameDeviceButton } from "@/components/rename-device"
import { ShareBar } from "@/components/share-bar"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardHeader } from "@/components/ui/card"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useRange } from "@/hooks/use-range"
import { formatBytes, formatCompact, formatNumber } from "@/lib/format"
import type { Device } from "@/lib/network"
import { deviceLabel } from "@/lib/network"
import { devicesQuery } from "@/lib/queries"
import { createColumns } from "@/lib/table"
import { DEFAULT_RANGE, rangePhrase } from "@/lib/timeseries"

export const Route = createFileRoute("/clients/")({
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

function useColumns(totalTraffic: number) {
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
              <Link
                to="/clients/$clientId"
                params={{ clientId: device.mac }}
                className="group/link flex min-w-0 flex-col"
              >
                <span className="truncate font-medium group-hover/link:underline">
                  {device.name ?? device.hostname ?? "Unknown device"}
                </span>
                <span className="truncate text-xs text-muted-foreground">
                  {device.name && device.hostname && `${device.hostname} · `}
                  <span className="font-mono">{device.mac}</span>
                </span>
              </Link>
            )
          },
        }),
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
          cell: ({ row }) => <RenameDeviceButton device={row.original} />,
          meta: { className: "w-10" },
        }),
      ]),
    [totalTraffic]
  )
}

function matches(device: Device, query: string) {
  if (!query) return true
  const needle = query.toLowerCase()
  return (
    device.mac.includes(needle) ||
    (device.name?.toLowerCase().includes(needle) ?? false) ||
    (device.hostname?.toLowerCase().includes(needle) ?? false) ||
    device.addresses.some(
      (address) =>
        address.ip.includes(needle) || (address.hostname?.toLowerCase().includes(needle) ?? false)
    )
  )
}

function Clients() {
  const range = useRange()
  const devices = useQuery(devicesQuery(range))
  const [query, setQuery] = React.useState("")
  const deferredQuery = React.useDeferredValue(query.trim())

  const all = React.useMemo(() => devices.data ?? [], [devices.data])
  const filtered = React.useMemo(
    () => all.filter((device) => matches(device, deferredQuery)),
    [all, deferredQuery]
  )
  const totalTraffic = all.reduce((sum, device) => sum + device.total, 0)
  const addressCount = all.reduce((sum, device) => sum + device.addresses.length, 0)
  const columns = useColumns(totalTraffic)

  return (
    <Page>
      <PageHeader
        title="Clients"
        description={
          devices.data
            ? `${formatNumber(all.length)} devices · ${formatNumber(addressCount)} addresses · ${formatBytes(totalTraffic)} · ${rangePhrase(range)}`
            : "Devices seen on your network, grouped by MAC address"
        }
      >
        <RangeFilter />
      </PageHeader>

      {devices.isError ? (
        <ErrorState error={devices.error} onRetry={() => void devices.refetch()} />
      ) : (
        <Card>
          <CardHeader>
            <InputGroup className="max-w-sm">
              <InputGroupAddon>
                <SearchIcon />
              </InputGroupAddon>
              <InputGroupInput
                type="search"
                placeholder="Search by name, IP or MAC address"
                aria-label="Search clients"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
            </InputGroup>
          </CardHeader>
          <CardContent>
            <DataTable
              data={filtered}
              columns={columns}
              getRowId={(device) => device.mac}
              initialSorting={[{ id: "total", desc: true }]}
              loading={devices.isPending}
              resetKey={deferredQuery}
              pageSize={20}
              emptyMessage={
                deferredQuery ? `No clients match “${deferredQuery}”.` : "No clients active in this period."
              }
            />
          </CardContent>
        </Card>
      )}
    </Page>
  )
}
