import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { BanIcon, CircleCheckIcon, CircleXIcon, ListChecksIcon, TriangleAlertIcon } from "lucide-react"
import * as React from "react"

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
import { alertDevice, alertSubjectLabel } from "@/lib/alerts"
import type { Alert, Delivery, NotificationChannel, Severity } from "@/lib/api"
import { formatDayTime, formatRelative } from "@/lib/format"
import { alertsQuery, notificationChannelsQuery } from "@/lib/queries"

export const Route = createFileRoute("/alerts")({
  staticData: { breadcrumb: "Alerts" },
  component: AlertsPage,
})

const PAGE_SIZE = 50

function SeverityBadge({ severity }: { severity: Severity }) {
  if (severity === "critical") return <Badge variant="destructive">Critical</Badge>
  if (severity === "warning") return <Badge variant="secondary">Warning</Badge>
  return <Badge variant="outline">Info</Badge>
}

const KIND_LABELS: Record<Delivery["Kind"], string> = {
  firing: "",
  reminder: "reminder",
  resolved: "resolved",
}

/** Where the notifications about an alert went, and how that went. */
function Deliveries({ alert, channels }: { alert: Alert; channels: NotificationChannel[] }) {
  if (alert.Deliveries.length === 0) {
    return <span className="text-muted-foreground">Not sent</span>
  }

  return (
    <ul className="flex flex-wrap gap-x-3 gap-y-1">
      {alert.Deliveries.map((d, index) => {
        const name = channels.find((c) => c.ID === d.ChannelID)?.Name ?? "Deleted channel"
        const kind = KIND_LABELS[d.Kind]
        const label = kind ? `${name} (${kind})` : name
        const detail =
          d.Status === "sent"
            ? `Sent ${formatDayTime(new Date(d.CreatedAt))}`
            : d.Status === "suppressed"
              ? "Held back: too many notifications to this channel at once"
              : `Failed after ${d.Attempts} attempts: ${d.Error ?? "unknown error"}`
        return (
          <li key={index} className="flex items-center gap-1" title={detail}>
            {d.Status === "sent" && <CircleCheckIcon className="size-3.5 text-chart-3" aria-label="Sent" />}
            {d.Status === "failed" && <CircleXIcon className="size-3.5 text-destructive" aria-label="Failed" />}
            {d.Status === "suppressed" && <BanIcon className="size-3.5 text-muted-foreground" aria-label="Held back" />}
            <span>{label}</span>
          </li>
        )
      })}
    </ul>
  )
}

function Subject({ alert }: { alert: Alert }) {
  const mac = alertDevice(alert)
  if (!mac) return <span>{alert.Subject}</span>
  return (
    <Link to="/clients/$clientId" params={{ clientId: mac }} className="underline-offset-4 hover:underline">
      {alertSubjectLabel(alert)}
    </Link>
  )
}

function FiringAlert({ alert, channels, now }: { alert: Alert; channels: NotificationChannel[]; now: number }) {
  return (
    <li className="flex flex-col gap-2 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <TriangleAlertIcon className="size-4 text-destructive" />
        <span className="font-medium">{alert.Title}</span>
        <SeverityBadge severity={alert.Severity} />
      </div>
      <p className="text-sm whitespace-pre-line">{alert.Body}</p>
      <p className="text-xs text-muted-foreground">
        Since {formatDayTime(new Date(alert.StartedAt))} ({formatRelative(Date.parse(alert.StartedAt), now)}) ·{" "}
        {alert.RuleName} · <Subject alert={alert} />
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
                      <div className="truncate font-medium" title={alert.Body}>
                        {alert.Title}
                      </div>
                      <div className="truncate text-xs text-muted-foreground">
                        <Subject alert={alert} />
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
