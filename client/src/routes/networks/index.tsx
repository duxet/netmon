import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { ArrowRightIcon, NetworkIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react"
import * as React from "react"

import { DataTable } from "@/components/data-table"
import { NetworkDot } from "@/components/network-badge"
import { NetworkDialog } from "@/components/network-dialog"
import { Page, PageHeader } from "@/components/page-header"
import { ErrorState } from "@/components/query-state"
import { RangeFilter } from "@/components/range-filter"
import { ShareBar } from "@/components/share-bar"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { useRange } from "@/hooks/use-range"
import { deleteNetwork, errorText, type Network, type NetworkSuggestion } from "@/lib/api"
import { formatBytes, formatNumber } from "@/lib/format"
import { NO_NETWORK, useNetworks } from "@/lib/networks"
import { networkSuggestionsQuery, networkSummaryQuery, networksQuery } from "@/lib/queries"
import { createColumns } from "@/lib/table"
import { cn } from "@/lib/utils"
import { DEFAULT_RANGE, rangePhrase } from "@/lib/timeseries"

export const Route = createFileRoute("/networks/")({
  loaderDeps: ({ search }) => ({ range: search.range ?? DEFAULT_RANGE }),
  loader: ({ context: { queryClient }, deps: { range } }) => {
    void queryClient.prefetchQuery(networksQuery())
    void queryClient.prefetchQuery(networkSummaryQuery(range))
  },
  component: NetworksPage,
})

function DeleteNetworkButton({ network }: { network: Network }) {
  const queryClient = useQueryClient()
  const remove = useMutation({
    mutationFn: () => deleteNetwork(network.ID),
    // Its devices move to whichever network holds their addresses next.
    onSuccess: () => queryClient.invalidateQueries(),
  })

  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          className="text-muted-foreground hover:text-destructive"
          aria-label={`Delete ${network.Name}`}
        >
          <Trash2Icon />
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete “{network.Name}”?</AlertDialogTitle>
          <AlertDialogDescription>
            Its devices and their traffic stay; they just won't be grouped as {network.Name} anymore.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {remove.isError && <p className="text-sm text-destructive">{errorText(remove.error)}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={() => remove.mutate()}>
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** Blocks of addresses devices use that no network has, each a button to add it. */
function Suggestions({ suggestions, className }: { suggestions: NetworkSuggestion[]; className?: string }) {
  return (
    <div className={cn("flex flex-wrap gap-2", className)}>
      {suggestions.map((suggestion) => (
        <NetworkDialog key={suggestion.Prefix} preset={{ prefixes: [suggestion.Prefix] }}>
          <Button variant="outline" size="sm">
            <PlusIcon data-icon="inline-start" />
            <span className="font-mono">{suggestion.Prefix}</span>
            <span className="text-muted-foreground">
              {suggestion.Devices === 1 ? "1 device" : `${formatNumber(suggestion.Devices)} devices`}
            </span>
          </Button>
        </NetworkDialog>
      ))}
    </div>
  )
}

interface NetworkRow {
  network: Network
  devices: number
  download: number
  upload: number
  total: number
}

const column = createColumns<NetworkRow>()

function useColumns(maxTotal: number) {
  return React.useMemo(
    () =>
      column.columns([
        column.accessor((row) => row.network.Name, {
          id: "network",
          header: "Network",
          sortFn: "text",
          cell: ({ row }) => {
            const { network } = row.original
            return (
              <div className="flex max-w-40 min-w-0 flex-col gap-0.5 sm:max-w-none">
                <span className="flex min-w-0 items-center gap-2">
                  <NetworkDot color={network.Color} />
                  <Link
                    to="/networks/$networkId"
                    params={{ networkId: String(network.ID) }}
                    className="truncate font-medium hover:underline"
                  >
                    {network.Name}
                  </Link>
                  {network.VLAN !== null && (
                    <Badge variant="secondary" className="font-normal">
                      VLAN {network.VLAN}
                    </Badge>
                  )}
                </span>
                <span className="truncate pl-4 font-mono text-xs text-muted-foreground">
                  {network.Prefixes.join(", ")}
                </span>
              </div>
            )
          },
        }),
        column.accessor("devices", {
          header: "Devices",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ row, getValue }) => (
            <Link
              to="/clients"
              search={(prev) => ({ ...prev, network: row.original.network.ID })}
              className="tabular-nums hover:underline"
            >
              {formatNumber(getValue())}
            </Link>
          ),
          meta: { align: "right" },
        }),
        column.accessor("download", {
          header: "Download",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => formatBytes(getValue()),
          meta: { align: "right", className: "hidden md:table-cell" },
        }),
        column.accessor("upload", {
          header: "Upload",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => formatBytes(getValue()),
          meta: { align: "right", className: "hidden md:table-cell" },
        }),
        column.accessor("total", {
          header: "Traffic",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => (
            <ShareBar
              value={getValue()}
              max={maxTotal}
              label={formatBytes(getValue())}
              className="min-w-28 sm:min-w-36"
            />
          ),
          meta: { className: "w-40 sm:w-56" },
        }),
        column.display({
          id: "actions",
          header: () => <span className="sr-only">Actions</span>,
          cell: ({ row }) => (
            <div className="flex justify-end">
              <NetworkDialog network={row.original.network}>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-muted-foreground hover:text-foreground"
                  aria-label={`Edit ${row.original.network.Name}`}
                >
                  <PencilIcon />
                </Button>
              </NetworkDialog>
              <DeleteNetworkButton network={row.original.network} />
            </div>
          ),
          meta: { className: "w-20" },
        }),
      ]),
    [maxTotal]
  )
}

function NetworksPage() {
  const range = useRange()
  const networks = useNetworks()
  const summary = useQuery(networkSummaryQuery(range))
  const suggestions = useQuery(networkSuggestionsQuery())

  const rows: NetworkRow[] = React.useMemo(() => {
    const traffic = new Map(summary.data?.Networks.map((n) => [n.NetworkID, n]))
    return networks.list.map((network) => {
      const t = traffic.get(network.ID)
      const download = t?.Download ?? 0
      const upload = t?.Upload ?? 0
      return { network, devices: t?.Devices ?? 0, download, upload, total: download + upload }
    })
  }, [networks.list, summary.data])

  const maxTotal = Math.max(0, ...rows.map((row) => row.total))
  const columns = useColumns(maxTotal)
  const unassigned = summary.data?.UnassignedDevices ?? 0
  const suggestionList = suggestions.data ?? []
  const hasNetworks = networks.list.length > 0

  const addButton = (
    <NetworkDialog>
      <Button>
        <PlusIcon data-icon="inline-start" />
        Add network
      </Button>
    </NetworkDialog>
  )

  return (
    <Page>
      <PageHeader
        title="Networks"
        description={
          hasNetworks
            ? `Devices grouped by the addresses they use, such as VLANs · ${rangePhrase(range)}`
            : "Group devices by subnet or VLAN"
        }
      >
        {hasNetworks && addButton}
        {hasNetworks && <RangeFilter />}
      </PageHeader>

      {networks.isError ? (
        <ErrorState error={networks.error} onRetry={() => void networks.refetch()} />
      ) : !networks.isPending && !hasNetworks ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <NetworkIcon />
            </EmptyMedia>
            <EmptyTitle>No networks yet</EmptyTitle>
            <EmptyDescription>
              Name the blocks of addresses your devices use, such as an IoT VLAN or a guest subnet, to
              tell devices apart at a glance and see the traffic of each group.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent className="max-w-xl">
            {suggestionList.length > 0 && (
              <div className="flex flex-col items-center gap-2">
                <p className="text-sm text-muted-foreground">Your devices use these blocks:</p>
                <Suggestions suggestions={suggestionList} className="justify-center" />
              </div>
            )}
            {addButton}
          </EmptyContent>
        </Empty>
      ) : (
        <>
          <Card>
            <CardContent>
              {summary.isError ? (
                <ErrorState error={summary.error} onRetry={() => void summary.refetch()} />
              ) : (
                <DataTable
                  data={rows}
                  columns={columns}
                  getRowId={(row) => String(row.network.ID)}
                  initialSorting={[{ id: "total", desc: true }]}
                  loading={networks.isPending || summary.isPending}
                  pageSize={50}
                />
              )}
            </CardContent>
          </Card>

          {(unassigned > 0 || suggestionList.length > 0) && (
            <Card>
              <CardHeader>
                <CardTitle>Not in any network</CardTitle>
                <CardDescription>
                  {unassigned > 0
                    ? `${unassigned === 1 ? "1 device with traffic has" : `${formatNumber(unassigned)} devices with traffic have`} no address in any network (${rangePhrase(range)}).`
                    : "Some devices use blocks of addresses no network has."}
                  {suggestionList.length > 0 && " Add a network for the blocks they use:"}
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col items-start gap-3">
                {suggestionList.length > 0 && <Suggestions suggestions={suggestionList} />}
                {unassigned > 0 && (
                  <Button variant="ghost" size="sm" className="-ml-2" asChild>
                    <Link to="/clients" search={(prev) => ({ ...prev, network: NO_NETWORK })}>
                      Show these devices
                      <ArrowRightIcon data-icon="inline-end" />
                    </Link>
                  </Button>
                )}
              </CardContent>
            </Card>
          )}

          <p className="text-sm text-muted-foreground">
            A network's traffic is counted from its side: what its devices downloaded from the internet
            and from other networks. Traffic between two networks counts for both.
          </p>
        </>
      )}
    </Page>
  )
}
