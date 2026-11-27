import type { QueryClient } from "@tanstack/react-query"
import { createRootRouteWithContext, Link, Outlet, retainSearchParams } from "@tanstack/react-router"
import * as React from "react"

import { AppSidebar } from "@/components/app-sidebar"
import { CollectorStatus } from "@/components/collector-status"
import { SiteHeader } from "@/components/site-header"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { isRangeKey, type RangeKey } from "@/lib/timeseries"

declare module "@tanstack/react-router" {
  interface StaticDataRouteOption {
    /**
     * Label for the header breadcrumb; a function receives the route params,
     * and can return an element that looks up a name.
     */
    breadcrumb?: string | ((params: Record<string, string>) => React.ReactNode)
  }
}

const RouterDevtools = import.meta.env.DEV
  ? React.lazy(() =>
      import("@tanstack/react-router-devtools").then((module) => ({
        default: module.TanStackRouterDevtools,
      }))
    )
  : () => null

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  // The time range scopes every page, so it is a root search param that
  // links keep when navigating between pages.
  validateSearch: (search: Record<string, unknown>): { range?: RangeKey } =>
    isRangeKey(search.range) ? { range: search.range } : {},
  search: { middlewares: [retainSearchParams(["range"])] },
  component: RootLayout,
  notFoundComponent: NotFound,
})

function RootLayout() {
  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset>
        <SiteHeader />
        <main className="@container/main flex flex-1 flex-col">
          <CollectorStatus />
          <Outlet />
        </main>
      </SidebarInset>
      <React.Suspense>
        <RouterDevtools position="bottom-right" />
      </React.Suspense>
    </SidebarProvider>
  )
}

function NotFound() {
  return (
    <Empty className="m-4 lg:m-6">
      <EmptyHeader>
        <EmptyTitle>Page not found</EmptyTitle>
        <EmptyDescription>There is nothing at this address.</EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button asChild variant="outline" size="sm">
          <Link to="/">Back to dashboard</Link>
        </Button>
      </EmptyContent>
    </Empty>
  )
}
