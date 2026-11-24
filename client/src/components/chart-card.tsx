import { ChartColumnIcon, TableIcon } from "lucide-react"
import * as React from "react"

import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { cn } from "@/lib/utils"

interface ChartCardProps {
  title: React.ReactNode
  description?: React.ReactNode
  /** The chart's accessible twin: the same values as a table. */
  table?: React.ReactNode
  children: React.ReactNode
  className?: string
  contentClassName?: string
}

/** A card holding a chart, with a toggle to read the same data as a table. */
export function ChartCard({
  title,
  description,
  table,
  children,
  className,
  contentClassName,
}: ChartCardProps) {
  const [view, setView] = React.useState<"chart" | "table">("chart")

  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
        {table && (
          <CardAction>
            <ToggleGroup
              type="single"
              size="sm"
              variant="outline"
              value={view}
              onValueChange={(value) => value && setView(value as "chart" | "table")}
              aria-label="View as"
            >
              <ToggleGroupItem value="chart" aria-label="Chart view">
                <ChartColumnIcon />
              </ToggleGroupItem>
              <ToggleGroupItem value="table" aria-label="Table view">
                <TableIcon />
              </ToggleGroupItem>
            </ToggleGroup>
          </CardAction>
        )}
      </CardHeader>
      <CardContent className={cn("flex-1", contentClassName)}>
        {view === "table" && table ? (
          <div className="max-h-80 overflow-auto">{table}</div>
        ) : (
          children
        )}
      </CardContent>
    </Card>
  )
}
