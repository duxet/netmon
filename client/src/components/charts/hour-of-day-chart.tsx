import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts"

import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { byteTicks, formatBytes, formatHourOfDay } from "@/lib/format"
import type { HourOfDay } from "@/lib/timeseries"

const config = {
  average: { label: "Average", color: "var(--chart-1)" },
} satisfies ChartConfig

function hourRange(hour: number) {
  return `${formatHourOfDay(hour)} – ${formatHourOfDay((hour + 1) % 24)}`
}

/** One series, so the card title names it and no legend is drawn. */
export function HourOfDayChart({ data }: { data: HourOfDay[] }) {
  const yTicks = byteTicks(Math.max(0, ...data.map((row) => row.average)))
  return (
    <ChartContainer config={config} className="aspect-auto h-56 w-full">
      <BarChart data={data} margin={{ top: 8, right: 0, left: 0, bottom: 0 }}>
        <CartesianGrid vertical={false} />
        <XAxis
          dataKey="hour"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          interval={2}
          tickFormatter={(hour: number) => formatHourOfDay(hour)}
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
          cursor={{ fill: "var(--muted)", opacity: 0.6 }}
          content={
            <ChartTooltipContent
              indicator="line"
              labelFormatter={(_, payload) => {
                const hour = payload?.[0]?.payload?.hour
                return typeof hour === "number" ? hourRange(hour) : null
              }}
              valueFormatter={(value) => formatBytes(Number(value))}
            />
          }
        />
        <Bar
          dataKey="average"
          fill="var(--color-average)"
          radius={[4, 4, 0, 0]}
          maxBarSize={24}
          isAnimationActive={false}
        />
      </BarChart>
    </ChartContainer>
  )
}

export function HourOfDayTable({ data }: { data: HourOfDay[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Hour</TableHead>
          <TableHead className="text-right">Average traffic</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {data.map((row) => (
          <TableRow key={row.hour}>
            <TableCell>{hourRange(row.hour)}</TableCell>
            <TableCell className="text-right tabular-nums">{formatBytes(row.average)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
