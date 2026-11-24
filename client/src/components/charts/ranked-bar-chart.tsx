import { Bar, BarChart, CartesianGrid, LabelList, XAxis, YAxis } from "recharts"

import { DIRECTION_CONFIG } from "@/components/charts/config"
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { formatBytes } from "@/lib/format"

export interface RankedRow {
  key: string
  label: string
  download: number
  upload: number
  total: number
}

const ROW_HEIGHT = 34
const BAR_SIZE = 14
const LABEL_MAX = 21

function truncate(value: string, max = LABEL_MAX) {
  return value.length > max ? `${value.slice(0, max - 1)}…` : value
}

/** Single-line category label; recharts' default tick word-wraps long names. */
function CategoryTick({
  x,
  y,
  payload,
}: {
  x?: number | string
  y?: number | string
  payload?: { value?: unknown }
}) {
  const label = String(payload?.value ?? "")
  return (
    <text x={x} y={y} dy={4} textAnchor="end" className="fill-muted-foreground">
      <title>{label}</title>
      {truncate(label)}
    </text>
  )
}

/**
 * Horizontal bars ranked by total traffic, split into download and upload.
 * Totals are printed at the bar tips, so the value axis is omitted.
 */
export function RankedBarChart({
  rows,
  onSelect,
  labelWidth = 164,
}: {
  rows: RankedRow[]
  onSelect?: (row: RankedRow) => void
  labelWidth?: number
}) {
  const height = rows.length * ROW_HEIGHT + 36

  return (
    <ChartContainer config={DIRECTION_CONFIG} className="aspect-auto w-full" style={{ height }}>
      <BarChart
        data={rows}
        layout="vertical"
        margin={{ top: 0, right: 64, bottom: 0, left: 0 }}
        barCategoryGap={(ROW_HEIGHT - BAR_SIZE) / 2}
      >
        <CartesianGrid horizontal={false} />
        <XAxis type="number" hide />
        <YAxis
          dataKey="label"
          type="category"
          tickLine={false}
          axisLine={false}
          width={labelWidth}
          tick={(props) => <CategoryTick {...props} />}
        />
        <ChartTooltip
          cursor={{ fill: "var(--muted)", opacity: 0.6 }}
          content={
            <ChartTooltipContent
              indicator="line"
              labelFormatter={(_, payload) => payload?.[0]?.payload?.label ?? null}
              valueFormatter={(value) => formatBytes(Number(value))}
            />
          }
        />
        <Bar
          dataKey="download"
          stackId="traffic"
          fill="var(--color-download)"
          // A surface-colored stroke leaves the gap between stacked segments.
          stroke="var(--card)"
          strokeWidth={2}
          barSize={BAR_SIZE}
          isAnimationActive={false}
          className={onSelect ? "cursor-pointer" : undefined}
          onClick={(entry) => onSelect?.(entry.payload as RankedRow)}
        />
        <Bar
          dataKey="upload"
          stackId="traffic"
          fill="var(--color-upload)"
          stroke="var(--card)"
          strokeWidth={2}
          radius={[0, 4, 4, 0]}
          barSize={BAR_SIZE}
          isAnimationActive={false}
          className={onSelect ? "cursor-pointer" : undefined}
          onClick={(entry) => onSelect?.(entry.payload as RankedRow)}
        >
          <LabelList
            dataKey="total"
            position="right"
            offset={8}
            className="fill-foreground"
            fontSize={12}
            // A no-break space keeps the value and its unit on one line.
            formatter={(value) => formatBytes(Number(value)).replace(" ", "\u00a0")}
          />
        </Bar>
        <ChartLegend content={<ChartLegendContent />} />
      </BarChart>
    </ChartContainer>
  )
}

export function RankedTable({ rows, labelHeader }: { rows: RankedRow[]; labelHeader: string }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{labelHeader}</TableHead>
          <TableHead className="text-right">Download</TableHead>
          <TableHead className="text-right">Upload</TableHead>
          <TableHead className="text-right">Total</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={row.key}>
            <TableCell className="max-w-48 truncate">{row.label}</TableCell>
            <TableCell className="text-right tabular-nums">{formatBytes(row.download)}</TableCell>
            <TableCell className="text-right tabular-nums">{formatBytes(row.upload)}</TableCell>
            <TableCell className="text-right font-medium tabular-nums">
              {formatBytes(row.total)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
