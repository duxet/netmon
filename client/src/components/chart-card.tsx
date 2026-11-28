import { Link, type LinkProps } from "@tanstack/react-router"
import { ChartColumnIcon, MaximizeIcon, TableIcon } from "lucide-react"
import * as React from "react"

import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
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
  /** The widget's own page, with the full list of results. */
  to?: LinkProps["to"]
}

/** A card holding a chart, with a toggle to read the same data as a table. */
export function ChartCard({
  title,
  description,
  table,
  children,
  className,
  contentClassName,
  to,
}: ChartCardProps) {
  const [view, setView] = React.useState<"chart" | "table">("chart")

  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>
          {to ? (
            <Link to={to} className="hover:underline">
              {title}
            </Link>
          ) : (
            title
          )}
        </CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
        {(table || to) && (
          <CardAction className="flex items-center gap-1">
            {table && (
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
            )}
            {to && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button variant="ghost" size="icon-sm" asChild>
                    <Link to={to} aria-label="Show all">
                      <MaximizeIcon />
                    </Link>
                  </Button>
                </TooltipTrigger>
                <TooltipContent>Show all</TooltipContent>
              </Tooltip>
            )}
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
