import {
  createColumnHelper,
  createPaginatedRowModel,
  createSortedRowModel,
  rowPaginationFeature,
  rowSortingFeature,
  sortFn_alphanumeric,
  sortFn_basic,
  sortFn_text,
  tableFeatures,
  type RowData,
} from "@tanstack/react-table"

interface ColumnMeta {
  align?: "left" | "right"
  /** Applied to both the header and body cells of the column. */
  className?: string
}

// TanStack Table v9: register only the features and row models these tables
// use; everything else is tree-shaken out of the bundle.
export const tableFeaturesConfig = tableFeatures({
  rowSortingFeature,
  rowPaginationFeature,
  sortedRowModel: createSortedRowModel(),
  paginatedRowModel: createPaginatedRowModel(),
  sortFns: { basic: sortFn_basic, alphanumeric: sortFn_alphanumeric, text: sortFn_text },
  columnMeta: {} as ColumnMeta,
})

export type TableFeatureSet = typeof tableFeaturesConfig

export function createColumns<TData extends RowData>() {
  return createColumnHelper<TableFeatureSet, TData>()
}
