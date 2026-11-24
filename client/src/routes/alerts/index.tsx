import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { ListChecksIcon, TriangleAlertIcon } from "lucide-react"
import * as React from "react"

import { AlertSubject, Deliveries, SeverityBadge } from "@/components/alert-parts"
import { Page, PageHeader } from "@/components/page-header"
import { ErrorState } from "@/components/query-state"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useNow } from "@/hooks/use-now"
import type { Alert, NotificationChannel } from "@/lib/api"
import { formatDayTime, formatRelative } from "@/lib/format"
import { alertsQuery, notificationChannelsQuery } from "@/lib/queries"

export const Route = createFileRoute("/alerts/")({
  component: AlertsPage,
})

const PAGE_SIZE = 50

function FiringAlert({ alert, channels, now }: { alert: Alert; channels: NotificationChannel[]; now: number }) {
  return (
    <li className="flex flex-col gap-2 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <TriangleAlertIcon className="size-4 text-destructive" />
        <Link
          to="/alerts/$alertId"
          params={{ alertId: String(alert.ID) }}
          className="font-medium underline-offset-4 hover:underline"
        >
          {alert.Title}
        </Link>
        <SeverityBadge severity={alert.Severity} />
      </div>
      <p className="text-sm whitespace-pre-line">{alert.Body}</p>
      <p className="text-xs text-muted-foreground">
        Since {formatDayTime(new Date(alert.StartedAt))} ({formatRelative(Date.parse(alert.StartedAt), now)}) ·{" "}
        {alert.RuleName} · <AlertSubject alert={alert} />
      </p>
      <div className="text-xs">
        <Deliveries alert={alert} channels={channels} />
      </div>
    </li>
  )
}

function AlertsPage() {
  const [limit, setLimit] = React.useState(PAGE_SIZE)
  const firing = useQuery(alertsQuery({ state: "firing", limit: 100 }))
  const history = useQuery(alertsQuery({ limit }))
  const channels = useQuery(notificationChannelsQuery())
  const channelList = channels.data ?? []
  const now = useNow(30_000)

  const nothingYet = history.data?.total === 0

  return (
    <Page>
      <PageHeader
        title="Alerts"
        description="What your alert rules found, and where netmon sent it."
      >
        <Button variant="outline" asChild>
          <Link to="/settings/alert-rules">
            <ListChecksIcon data-icon="inline-start" />
            Alert rules
          </Link>
        </Button>
      </PageHeader>

      {(firing.isError || history.isError) && (
        <ErrorState error={firing.error ?? history.error} onRetry={() => void Promise.all([firing.refetch(), history.refetch()])} />
      )}

      {history.isPending && (
        <Card>
          <CardContent className="flex flex-col gap-3">
            <Skeleton className="h-4 w-56" />
            <Skeleton className="h-4 w-80" />
            <Skeleton className="h-4 w-64" />
          </CardContent>
        </Card>
      )}

      {nothingYet && (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <TriangleAlertIcon />
            </EmptyMedia>
            <EmptyTitle>No alerts yet</EmptyTitle>
            <EmptyDescription>
              Alerts show up here when a rule finds something: a new device, or a device using more
              traffic than you set.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button asChild>
              <Link to="/settings/alert-rules">Set up alert rules</Link>
            </Button>
          </EmptyContent>
        </Empty>
      )}

      {firing.data && firing.data.alerts.length > 0 && (
        <Card className="gap-0 pb-0">
          <CardHeader className="pb-4">
            <CardTitle>Firing now</CardTitle>
            <CardDescription>
              Conditions that still hold. They resolve on their own once they don't.
            </CardDescription>
          </CardHeader>
          <ul className="divide-y border-t">
            {firing.data.alerts.map((alert) => (
              <FiringAlert key={alert.ID} alert={alert} channels={channelList} now={now} />
            ))}
          </ul>
        </Card>
      )}

      {history.data && history.data.total > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>History</CardTitle>
            <CardDescription>
              {history.data.total} {history.data.total === 1 ? "alert" : "alerts"}, newest first. Resolved
              alerts are deleted after 30 days, unless netmon is set up to keep them longer.
            </CardDescription>
          </CardHeader>
          <CardContent className="px-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-6">Started</TableHead>
                  <TableHead>Alert</TableHead>
                  <TableHead>Rule</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead className="pr-6">Notifications</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {history.data.alerts.map((alert) => (
                  <TableRow key={alert.ID}>
                    <TableCell className="pl-6 whitespace-nowrap text-muted-foreground">
                      {formatDayTime(new Date(alert.StartedAt))}
                    </TableCell>
                    <TableCell className="max-w-md">
                      <Link
                        to="/alerts/$alertId"
                        params={{ alertId: String(alert.ID) }}
                        className="block truncate font-medium underline-offset-4 hover:underline"
                        title={alert.Body}
                      >
                        {alert.Title}
                      </Link>
                      <div className="truncate text-xs text-muted-foreground">
                        <AlertSubject alert={alert} />
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{alert.RuleName}</TableCell>
                    <TableCell>
                      {alert.State === "firing" ? (
                        <Badge variant="destructive">Firing</Badge>
                      ) : (
                        <Badge variant="outline">
                          {alert.ResolvedAt === alert.StartedAt ? "Event" : "Resolved"}
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="pr-6 text-xs">
                      <Deliveries alert={alert} channels={channelList} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {history.data.alerts.length < history.data.total && (
              <div className="flex justify-center pt-4">
                <Button variant="outline" size="sm" onClick={() => setLimit((l) => l + PAGE_SIZE)}>
                  Show more
                </Button>
              </div>
            )}
          </CardContent>
        </Card>
      )}
    </Page>
  )
}
