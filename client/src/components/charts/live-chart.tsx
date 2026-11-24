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
import { byteTicks, formatBitrate, formatClock } from "@/lib/format"
import { LIVE_WINDOW_MS, type LivePoint } from "@/lib/live"

/** Axis ticks in bytes per second that are round numbers of bits per second. */
function rateTicks(maxBytesPerSecond: number): number[] {
  return byteTicks(maxBytesPerSecond * 8).map((bits) => bits / 8)
}

const perSecond = (bytesPerSecond: number) => formatBitrate(bytesPerSecond, 1)

/**
 * What the devices of the network used, second by second, over the last couple
 * of minutes. The window always ends at the newest sample, so the chart does
 * not depend on the browser's clock being the server's.
 */
export function LiveChart({ points, newest }: { points: LivePoint[]; newest: number }) {
  const id = React.useId().replace(/:/g, "")
  const yTicks = React.useMemo(
    () => rateTicks(Math.max(0, ...points.map((p) => Math.max(p.download, p.upload)))),
    [points]
  )

  return (
    <ChartContainer config={DIRECTION_CONFIG} className="aspect-auto h-56 w-full sm:h-64">
      <AreaChart data={points} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
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
          type="number"
          scale="time"
          domain={[newest - LIVE_WINDOW_MS, newest]}
          allowDataOverflow
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={56}
          tickFormatter={(value: number) => formatClock(new Date(value))}
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          tickMargin={4}
          width="auto"
          ticks={yTicks}
          domain={[0, yTicks.at(-1) ?? "auto"]}
          tickFormatter={perSecond}
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              indicator="line"
              labelFormatter={(_, payload) => {
                const time = payload?.[0]?.payload?.time
                return typeof time === "number" ? formatClock(new Date(time)) : null
              }}
              valueFormatter={(value) => perSecond(Number(value))}
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
