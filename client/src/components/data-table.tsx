import {
  FlexRender,
  useTable,
  type ColumnDef,
  type Header,
  type PaginationState,
  type RowData,
  type SortingState,
} from "@tanstack/react-table"
import {
  ArrowDownIcon,
  ArrowUpIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  ChevronsUpDownIcon,
} from "lucide-react"
import * as React from "react"

import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { formatNumber } from "@/lib/format"
import { tableFeaturesConfig, type TableFeatureSet } from "@/lib/table"
import { cn } from "@/lib/utils"

function SortableHeader<TData extends RowData>({
  header,
}: {
  header: Header<TableFeatureSet, TData>
}) {
  const content = <FlexRender header={header} />
  if (!header.column.getCanSort()) {
    return <span className="text-xs font-medium text-muted-foreground">{content}</span>
  }

  const sorted = header.column.getIsSorted()
  const Icon = sorted === "asc" ? ArrowUpIcon : sorted === "desc" ? ArrowDownIcon : ChevronsUpDownIcon
  const alignRight = header.column.columnDef.meta?.align === "right"

  return (
    <Button
      variant="ghost"
      size="xs"
      className={cn("-mx-2 text-xs text-muted-foreground", alignRight && "flex-row-reverse")}
      onClick={header.column.getToggleSortingHandler()}
    >
      {content}
      <Icon className={cn(!sorted && "opacity-50")} />
    </Button>
  )
}

export interface DataTableProps<TData extends RowData> {
  data: TData[]
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- columns mix value types
  columns: ColumnDef<TableFeatureSet, TData, any>[]
  getRowId: (row: TData) => string
  initialSorting?: SortingState
  pageSize?: number
  loading?: boolean
  emptyMessage?: React.ReactNode
  /** Resets to the first page whenever this value changes (e.g. a search query). */
  resetKey?: unknown
  onPageRowsChange?: (rows: TData[]) => void
  rowClassName?: string
}

export function DataTable<TData extends RowData>({
  data,
  columns,
  getRowId,
  initialSorting = [],
  pageSize = 15,
  loading = false,
  emptyMessage = "No results.",
  resetKey,
  onPageRowsChange,
  rowClassName,
}: DataTableProps<TData>) {
  const [sorting, setSorting] = React.useState<SortingState>(initialSorting)
  const [pagination, setPagination] = React.useState<PaginationState>({
    pageIndex: 0,
    pageSize,
  })

  // Data refreshes every minute; keep the reader's page instead of jumping
  // back to the first one, but do reset when the filter changes.
  const [lastResetKey, setLastResetKey] = React.useState(resetKey)
  if (resetKey !== lastResetKey) {
    setLastResetKey(resetKey)
    setPagination((state) => ({ ...state, pageIndex: 0 }))
  }

  const table = useTable({
    features: tableFeaturesConfig,
    data,
    columns,
    state: { sorting, pagination },
    getRowId,
    onSortingChange: setSorting,
    onPaginationChange: setPagination,
    autoResetPageIndex: false,
  })

  const pageCount = Math.max(1, table.getPageCount())
  const pageIndex = Math.min(table.state.pagination.pageIndex, pageCount - 1)
  if (pageIndex !== table.state.pagination.pageIndex) {
    setPagination((state) => ({ ...state, pageIndex }))
  }

  const rows = table.getRowModel().rows
  const pageRows = rows.map((row) => row.original)
  const pageRowsKey = pageRows.map(getRowId).join("|")
  const notifyRows = React.useEffectEvent((next: TData[]) => onPageRowsChange?.(next))
  React.useEffect(() => {
    notifyRows(pageRows)
    // pageRows is derived from pageRowsKey; only react to a different page.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pageRowsKey])

  const total = table.getRowCount()
  const firstRow = total === 0 ? 0 : pageIndex * pagination.pageSize + 1
  const lastRow = Math.min(total, (pageIndex + 1) * pagination.pageSize)

  return (
    <div className="flex flex-col gap-3">
      <div className="overflow-hidden rounded-lg border">
        <Table>
          <TableHeader className="bg-muted/50">
            {table.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id} className="hover:bg-transparent">
                {headerGroup.headers.map((header) => {
                  const meta = header.column.columnDef.meta
                  return (
                    <TableHead
                      key={header.id}
                      colSpan={header.colSpan}
                      className={cn(meta?.align === "right" && "text-right", meta?.className)}
                    >
                      {header.isPlaceholder ? null : <SortableHeader header={header} />}
                    </TableHead>
                  )
                })}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 6 }, (_, index) => (
                <TableRow key={index} className="hover:bg-transparent">
                  <TableCell colSpan={columns.length}>
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : rows.length > 0 ? (
              rows.map((row) => (
                <TableRow key={row.id} className={rowClassName}>
                  {row.getAllCells().map((cell) => {
                    const meta = cell.column.columnDef.meta
                    return (
                      <TableCell
                        key={cell.id}
                        className={cn(
                          meta?.align === "right" && "text-right tabular-nums",
                          meta?.className
                        )}
                      >
                        <FlexRender cell={cell} />
                      </TableCell>
                    )
                  })}
                </TableRow>
              ))
            ) : (
              <TableRow className="hover:bg-transparent">
                <TableCell
                  colSpan={columns.length}
                  className="h-24 text-center text-muted-foreground"
                >
                  {emptyMessage}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      {!loading && total > 0 && (
        <div className="flex items-center justify-between gap-4 text-sm text-muted-foreground">
          <span className="tabular-nums">
            {formatNumber(firstRow)}–{formatNumber(lastRow)} of {formatNumber(total)}
          </span>
          {pageCount > 1 && (
            <div className="flex items-center gap-2">
              <span className="hidden tabular-nums sm:inline">
                Page {pageIndex + 1} of {pageCount}
              </span>
              <Button
                variant="outline"
                size="icon-sm"
                onClick={() => table.previousPage()}
                disabled={!table.getCanPreviousPage()}
                aria-label="Previous page"
              >
                <ChevronLeftIcon />
              </Button>
              <Button
                variant="outline"
                size="icon-sm"
                onClick={() => table.nextPage()}
                disabled={!table.getCanNextPage()}
                aria-label="Next page"
              >
                <ChevronRightIcon />
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
