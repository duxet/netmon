import { createFileRoute } from "@tanstack/react-router"
import { AppWindowIcon } from "lucide-react"
import * as React from "react"

import { AppTrafficSheet } from "@/components/app-traffic"
import { ChartCard } from "@/components/chart-card"
import { RankedBarChart, type RankedRow } from "@/components/charts/ranked-bar-chart"
import { DataTable } from "@/components/data-table"
import { Page, PageHeader } from "@/components/page-header"
import { EmptyChart, ErrorState } from "@/components/query-state"
import { RangeFilter } from "@/components/range-filter"
import { ShareBar } from "@/components/share-bar"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useRange } from "@/hooks/use-range"
import { useAppRows } from "@/lib/apps"
import { formatBytes, formatNumber, formatPercent } from "@/lib/format"
import { appsQuery } from "@/lib/queries"
import { createColumns } from "@/lib/table"
import { DEFAULT_RANGE, rangePhrase } from "@/lib/timeseries"

export const Route = createFileRoute("/apps")({
  staticData: { breadcrumb: "Apps" },
  loaderDeps: ({ search }) => ({ range: search.range ?? DEFAULT_RANGE }),
  loader: ({ context: { queryClient }, deps: { range } }) =>
    void queryClient.prefetchQuery(appsQuery(range)),
  component: AppsPage,
})

const column = createColumns<RankedRow>()

function useColumns(maxTotal: number, total: number, onSelect: (app: string) => void) {
  return React.useMemo(
    () =>
      column.columns([
        column.accessor("label", {
          header: "App",
          sortFn: "text",
          cell: ({ row, getValue }) => (
            <button
              type="button"
              className="max-w-48 truncate text-left font-medium hover:underline sm:max-w-none"
              onClick={() => onSelect(row.original.key)}
            >
              {getValue()}
            </button>
          ),
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
        column.accessor((row) => (total > 0 ? row.total / total : 0), {
          id: "share",
          header: "Share",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => formatPercent(getValue()),
          meta: { align: "right", className: "hidden sm:table-cell" },
        }),
        column.accessor("total", {
          header: "Traffic",
          sortFn: "basic",
          sortDescFirst: true,
          cell: ({ getValue }) => (
            <ShareBar value={getValue()} max={maxTotal} label={formatBytes(getValue())} className="min-w-28 sm:min-w-36" />
          ),
          meta: { className: "w-40 sm:w-56" },
        }),
      ]),
    [maxTotal, total, onSelect]
  )
}

/** Every app the network's internet traffic was attributed to. */
function AppsPage() {
  const range = useRange()
  const { query, apps, other, total } = useAppRows()
  const [selected, setSelected] = React.useState<string | null>(null)

  const rows = React.useMemo(() => (other ? [...apps, other] : apps), [apps, other])
  const maxTotal = Math.max(0, ...rows.map((row) => row.total))
  const columns = useColumns(maxTotal, total, setSelected)

  return (
    <Page>
      <PageHeader
        title="Apps"
        description={`Internet traffic by service, matched on the remote address · ${rangePhrase(range)}`}
      >
        <RangeFilter />
      </PageHeader>

      <ChartCard
        title="Traffic by app"
        description={
          other && other.total > 0
            ? `Leaves out ${formatBytes(other.total)} to unrecognized hosts (${formatPercent(other.total / total)}) · select an app to see its traffic over time`
            : "Select an app to see its traffic over time"
        }
      >
        {query.isError ? (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        ) : query.isPending ? (
          <Skeleton className="h-72 w-full" />
        ) : apps.length > 0 ? (
          <RankedBarChart rows={apps} labelWidth={180} onSelect={(row) => setSelected(row.key)} />
        ) : (
          <EmptyChart icon={<AppWindowIcon />} title="No traffic to recognized apps" />
        )}
      </ChartCard>

      <Card>
        <CardHeader>
          <CardTitle>Every app</CardTitle>
          <CardDescription>
            {query.data
              ? `${formatNumber(apps.length)} recognized ${apps.length === 1 ? "app" : "apps"}${other ? ", plus unrecognized hosts" : ""}`
              : "Apps with traffic in this period"}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {query.isError ? (
            <ErrorState error={query.error} onRetry={() => void query.refetch()} />
          ) : (
            <DataTable
              data={rows}
              columns={columns}
              getRowId={(row) => row.key}
              initialSorting={[{ id: "total", desc: true }]}
              loading={query.isPending}
              pageSize={50}
              emptyMessage="No internet traffic in this period."
            />
          )}
        </CardContent>
      </Card>

      <AppTrafficSheet app={selected} client={{}} scope="Whole network" onClose={() => setSelected(null)} />
    </Page>
  )
}
