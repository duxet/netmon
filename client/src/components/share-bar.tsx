import { formatPercent } from "@/lib/format"
import { cn } from "@/lib/utils"

/** A thin meter showing one value's share of a maximum, with its percentage. */
export function ShareBar({
  value,
  max,
  share,
  className,
}: {
  value: number
  max: number
  /** Ratio printed beside the bar; defaults to value / max. */
  share?: number
  className?: string
}) {
  const ratio = max > 0 ? Math.min(1, value / max) : 0
  return (
    <div className={cn("flex items-center gap-2", className)}>
      <div className="h-1 min-w-12 flex-1 overflow-hidden rounded-full bg-chart-1/15">
        <div className="h-full rounded-full bg-chart-1" style={{ width: `${ratio * 100}%` }} />
      </div>
      <span className="w-12 text-right text-xs text-muted-foreground tabular-nums">
        {formatPercent(share ?? ratio)}
      </span>
    </div>
  )
}
