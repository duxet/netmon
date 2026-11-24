import { useIsFetching, useQueryClient } from "@tanstack/react-query"
import { Link, useMatches } from "@tanstack/react-router"
import { RefreshCwIcon } from "lucide-react"
import * as React from "react"

import { ModeToggle } from "@/components/mode-toggle"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { useNow } from "@/hooks/use-now"
import { formatRelative } from "@/lib/format"
import { REFRESH_INTERVAL } from "@/lib/queries"

function Breadcrumbs() {
  const crumbs = useMatches({
    select: (matches) =>
      matches.flatMap((match) => {
        const { breadcrumb } = match.staticData
        if (!breadcrumb) return []
        return [
          { id: match.id, breadcrumb, params: match.params as Record<string, string>, pathname: match.pathname },
        ]
      }),
  })

  return (
    <Breadcrumb className="min-w-0">
      <BreadcrumbList className="flex-nowrap">
        {crumbs.map((crumb, index) => {
          const last = index === crumbs.length - 1
          // Elements are made here rather than in the selector, which compares
          // what it returns by value.
          const title = typeof crumb.breadcrumb === "function" ? crumb.breadcrumb(crumb.params) : crumb.breadcrumb
          return (
            <React.Fragment key={crumb.id}>
              {index > 0 && <BreadcrumbSeparator />}
              <BreadcrumbItem className={cn(!last && "hidden sm:inline-flex", "min-w-0")}>
                {last ? (
                  <BreadcrumbPage className="truncate">{title}</BreadcrumbPage>
                ) : (
                  <BreadcrumbLink asChild>
                    <Link to={crumb.pathname}>{title}</Link>
                  </BreadcrumbLink>
                )}
              </BreadcrumbItem>
            </React.Fragment>
          )
        })}
      </BreadcrumbList>
    </Breadcrumb>
  )
}

/** Latest successful fetch across all queries, kept in sync with the cache. */
function useLastUpdated() {
  const queryClient = useQueryClient()
  const cache = queryClient.getQueryCache()
  return React.useSyncExternalStore(
    (onChange) => cache.subscribe(onChange),
    () => Math.max(0, ...cache.getAll().map((query) => query.state.dataUpdatedAt))
  )
}

function RefreshStatus() {
  const queryClient = useQueryClient()
  const fetching = useIsFetching() > 0
  const lastUpdated = useLastUpdated()
  const now = useNow(15_000)

  return (
    <div className="flex items-center gap-1">
      {lastUpdated > 0 && (
        <span className="hidden text-xs text-muted-foreground sm:inline" aria-live="polite">
          Updated {formatRelative(lastUpdated, Math.max(now, lastUpdated))}
        </span>
      )}
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Refresh data"
            onClick={() => void queryClient.refetchQueries({ type: "active" })}
          >
            <RefreshCwIcon className={cn(fetching && "animate-spin")} />
          </Button>
        </TooltipTrigger>
        <TooltipContent>
          Refresh now · updates every {REFRESH_INTERVAL / 60_000} min
        </TooltipContent>
      </Tooltip>
    </div>
  )
}

export function SiteHeader() {
  return (
    <header className="sticky top-0 z-10 flex h-12 shrink-0 items-center gap-2 rounded-t-xl border-b bg-background/80 px-4 backdrop-blur transition-[width,height] ease-linear lg:px-6">
      <SidebarTrigger className="-ml-1" />
      <Separator orientation="vertical" className="mx-1 data-[orientation=vertical]:h-4" />
      <Breadcrumbs />
      <div className="ml-auto flex items-center gap-1">
        <RefreshStatus />
        <ModeToggle />
      </div>
    </header>
  )
}
