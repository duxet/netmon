import { MinusIcon, TrendingDownIcon, TrendingUpIcon } from "lucide-react"
import type * as React from "react"

import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardAction,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { formatDelta } from "@/lib/format"
import { cn } from "@/lib/utils"

interface StatCardProps {
  label: string
  value: React.ReactNode
  icon?: React.ComponentType<{ className?: string }>
  /** Relative change vs. the previous period, e.g. 0.12 for +12%. */
  delta?: { ratio: number; period: string } | null
  footer?: React.ReactNode
  /** Small marker drawn before the label, tying the card to a chart series. */
  seriesColor?: string
  loading?: boolean
}

function DeltaBadge({ ratio, period }: { ratio: number; period: string }) {
  // Traffic going up is neither good nor bad, so the badge stays neutral and
  // carries direction with an icon.
  const Icon = ratio > 0.005 ? TrendingUpIcon : ratio < -0.005 ? TrendingDownIcon : MinusIcon
  return (
    <Badge variant="outline" title={`Compared with the previous ${period}`}>
      <Icon data-icon="inline-start" />
      {formatDelta(ratio)}
    </Badge>
  )
}

export function StatCard({
  label,
  value,
  icon: Icon,
  delta,
  footer,
  seriesColor,
  loading,
}: StatCardProps) {
  return (
    <Card className="@container/card gap-3">
      <CardHeader>
        <CardDescription className="flex items-center gap-1.5">
          {seriesColor && (
            <span
              aria-hidden
              className="size-2 shrink-0 rounded-[2px]"
              style={{ backgroundColor: seriesColor }}
            />
          )}
          {Icon && !seriesColor && <Icon className="size-3.5" />}
          {label}
        </CardDescription>
        <CardTitle className="text-2xl font-semibold @[250px]/card:text-3xl">
          {loading ? <Skeleton className="mt-1 h-8 w-28" /> : value}
        </CardTitle>
        {delta && !loading && (
          <CardAction>
            <DeltaBadge {...delta} />
          </CardAction>
        )}
      </CardHeader>
      {footer !== undefined && (
        <CardFooter className={cn("text-sm text-muted-foreground", loading && "invisible")}>
          {footer}
        </CardFooter>
      )}
    </Card>
  )
}
