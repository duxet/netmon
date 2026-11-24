import * as React from "react"
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts"

import { DIRECTION_CONFIG } from "@/components/charts/config"
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  byteTicks,
  formatBytes,
  formatDay,
  formatDayTime,
  formatLongDay,
  formatTime,
} from "@/lib/format"
import type { Bucket, TrafficSeries } from "@/lib/timeseries"

function labelFor(time: number, granularity: TrafficSeries["granularity"]) {
  return granularity === "day" ? formatLongDay(new Date(time)) : formatDayTime(new Date(time))
}

export function TrafficChart({
  buckets,
  granularity,
  spansDays,
}: {
  buckets: Bucket[]
  granularity: TrafficSeries["granularity"]
  /** Whether hourly ticks should show dates instead of clock times. */
  spansDays: boolean
}) {
  const id = React.useId().replace(/:/g, "")
  // Hourly data over several days: one tick per local midnight, so each
  // date appears once.
  const ticks = React.useMemo(
    () =>
      granularity === "hour" && spansDays
        ? buckets.filter((b) => new Date(b.time).getHours() === 0).map((b) => b.time)
        : undefined,
    [buckets, granularity, spansDays]
  )
  const yTicks = React.useMemo(
    () => byteTicks(Math.max(0, ...buckets.map((b) => Math.max(b.download, b.upload)))),
    [buckets]
  )

  return (
    <ChartContainer config={DIRECTION_CONFIG} className="aspect-auto h-64 w-full sm:h-72">
      <AreaChart data={buckets} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
        <defs>
          {(["download", "upload"] as const).map((key) => (
            <linearGradient key={key} id={`${id}-${key}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={`var(--color-${key})`} stopOpacity={0.22} />
              <stop offset="100%" stopColor={`var(--color-${key})`} stopOpacity={0.02} />
            </linearGradient>
          ))}
        </defs>
        <CartesianGrid vertical={false} />
        <XAxis
          dataKey="time"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={40}
          ticks={ticks}
          tickFormatter={(value: number) =>
            granularity === "hour" && !spansDays
              ? formatTime(new Date(value))
              : formatDay(new Date(value))
          }
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          tickMargin={4}
          width="auto"
          ticks={yTicks}
          domain={[0, yTicks.at(-1) ?? "auto"]}
          tickFormatter={(value: number) => formatBytes(value)}
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              indicator="line"
              labelFormatter={(_, payload) => {
                const time = payload?.[0]?.payload?.time
                return typeof time === "number" ? labelFor(time, granularity) : null
              }}
              valueFormatter={(value) => formatBytes(Number(value))}
            />
          }
        />
        {(["download", "upload"] as const).map((key) => (
          <Area
            key={key}
            dataKey={key}
            type="monotone"
            stroke={`var(--color-${key})`}
            strokeWidth={2}
            fill={`url(#${id}-${key})`}
            activeDot={{ r: 4, strokeWidth: 2, stroke: "var(--card)" }}
            isAnimationActive={false}
          />
        ))}
        <ChartLegend content={<ChartLegendContent />} />
      </AreaChart>
    </ChartContainer>
  )
}

export function TrafficTable({
  buckets,
  granularity,
}: {
  buckets: Bucket[]
  granularity: TrafficSeries["granularity"]
}) {
  const rows = buckets.filter((bucket) => bucket.download + bucket.upload > 0).reverse()
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{granularity === "day" ? "Day" : "Hour"}</TableHead>
          <TableHead className="text-right">Download</TableHead>
          <TableHead className="text-right">Upload</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((bucket) => (
          <TableRow key={bucket.time}>
            <TableCell>{labelFor(bucket.time, granularity)}</TableCell>
            <TableCell className="text-right tabular-nums">{formatBytes(bucket.download)}</TableCell>
            <TableCell className="text-right tabular-nums">{formatBytes(bucket.upload)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
