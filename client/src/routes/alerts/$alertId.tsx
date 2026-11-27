import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { BanIcon, CircleCheckIcon, CircleXIcon } from "lucide-react"
import type * as React from "react"

import { AlertSubject, SeverityBadge } from "@/components/alert-parts"
import { Page, PageHeader } from "@/components/page-header"
import { ErrorState } from "@/components/query-state"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { durationLabel, LABELS } from "@/lib/alerts"
import { acknowledgeAlert, ApiError, unacknowledgeAlert, type Alert, type Delivery } from "@/lib/api"
import { formatBytes, formatDayTime } from "@/lib/format"
import { alertQuery, alertRulesQuery, notificationChannelsQuery } from "@/lib/queries"

export const Route = createFileRoute("/alerts/$alertId")({
  staticData: { breadcrumb: (params) => `Alert ${params.alertId}` },
  component: AlertPage,
})

const KINDS: Record<Delivery["Kind"], string> = {
  firing: "Alert",
  reminder: "Reminder",
  resolved: "Resolved",
}

function StatusCell({ delivery }: { delivery: Delivery }) {
  switch (delivery.Status) {
    case "sent":
      return (
        <span className="flex items-center gap-1.5">
          <CircleCheckIcon className="size-4 text-chart-3" /> Sent
          {delivery.Attempts > 1 && <span className="text-muted-foreground">at attempt {delivery.Attempts}</span>}
        </span>
      )
    case "suppressed":
      return (
        <span className="flex items-center gap-1.5">
          <BanIcon className="size-4 text-muted-foreground" /> Held back by the rate limit
        </span>
      )
    case "held":
      return (
        <span className="flex flex-col gap-0.5">
          <span className="flex items-center gap-1.5">
            <BanIcon className="size-4 text-muted-foreground" /> Not sent
          </span>
          {delivery.Error && <span className="text-xs text-muted-foreground">{delivery.Error}</span>}
        </span>
      )
    default:
      return (
        <span className="flex flex-col gap-0.5">
          <span className="flex items-center gap-1.5">
            <CircleXIcon className="size-4 text-destructive" /> Failed
            {delivery.Attempts > 0 && <span className="text-muted-foreground">after {delivery.Attempts} attempts</span>}
          </span>
          {delivery.Error && <span className="text-xs break-words text-destructive">{delivery.Error}</span>}
        </span>
      )
  }
}

/** The measured value, in the unit of the rule that measured it. */
function formatValue(alert: Alert, ruleType: string | undefined): string | null {
  if (alert.Value === null) return null
  if (ruleType === "device_inactive") return `No traffic for ${durationLabel(Math.round(alert.Value / 60) * 60)}`
  // As precise as the message, which has three significant digits.
  return formatBytes(alert.Value, 2)
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[9rem_1fr] gap-2 py-1.5 text-sm">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  )
}

function AlertPage() {
  const { alertId } = Route.useParams()
  const id = Number(alertId)
  const alert = useQuery({ ...alertQuery(id), enabled: Number.isInteger(id) && id > 0 })
  const queryClient = useQueryClient()
  const acknowledge = useMutation({
    mutationFn: (on: boolean) => (on ? acknowledgeAlert(id) : unacknowledgeAlert(id)),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ["alerts"] }),
  })
  const rules = useQuery(alertRulesQuery())
  const channels = useQuery(notificationChannelsQuery())

  if (alert.isError) {
    const missing = alert.error instanceof ApiError && alert.error.status === 404
    return (
      <Page>
        {missing ? (
          <ErrorState error={new Error("This alert doesn't exist, or was deleted after its retention period.")} />
        ) : (
          <ErrorState error={alert.error} onRetry={() => void alert.refetch()} />
        )}
      </Page>
    )
  }

  if (!alert.data) {
    return (
      <Page>
        <Skeleton className="h-7 w-80" />
        <Skeleton className="h-40 w-full" />
      </Page>
    )
  }

  const a = alert.data
  const rule = rules.data?.find((r) => r.ID === a.RuleID)
  const value = formatValue(a, rule?.Type)
  const event = a.ResolvedAt === a.StartedAt
  const channelName = (id: number) => channels.data?.find((c) => c.ID === id)?.Name ?? "Deleted channel"

  return (
    <Page>
      <PageHeader
        title={a.Title}
        description={
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            <SeverityBadge severity={a.Severity} />
            {a.State === "firing" ? (
              <Badge variant="destructive">Firing</Badge>
            ) : (
              <Badge variant="outline">{event ? "Event" : "Resolved"}</Badge>
            )}
            {a.AcknowledgedAt && (
              <Badge variant="outline">
                Acknowledged{a.AcknowledgedBy ? ` by ${a.AcknowledgedBy}` : ""}{" "}
                {formatDayTime(new Date(a.AcknowledgedAt))}
              </Badge>
            )}
          </div>
        }
      >
        {a.State === "firing" && !event && (
          <Button
            variant={a.AcknowledgedAt ? "ghost" : "default"}
            disabled={acknowledge.isPending}
            onClick={() => acknowledge.mutate(!a.AcknowledgedAt)}
            title={a.AcknowledgedAt ? undefined : "You know of it: it isn't sent again while it lasts."}
          >
            {a.AcknowledgedAt ? "Unacknowledge" : "Acknowledge"}
          </Button>
        )}
        <Button variant="outline" asChild>
          <Link to="/alerts">All alerts</Link>
        </Button>
      </PageHeader>

      <Card>
        <CardContent className="flex flex-col gap-4">
          <p className="whitespace-pre-line">{a.Body}</p>
          <dl className="divide-y">
            <Fact label="Rule">
              {rule ? (
                <Link to="/settings/alert-rules" className="underline-offset-4 hover:underline">
                  {rule.Name}
                </Link>
              ) : (
                <span>
                  {a.RuleName} <span className="text-muted-foreground">(deleted)</span>
                </span>
              )}
            </Fact>
            <Fact label="About">
              <AlertSubject alert={a} />
            </Fact>
            {value && <Fact label={a.State === "firing" ? "Now" : "Last measured"}>{value}</Fact>}
            <Fact label={event ? "When" : "Started"}>{formatDayTime(new Date(a.StartedAt))}</Fact>
            {!event && a.ResolvedAt && <Fact label="Resolved">{formatDayTime(new Date(a.ResolvedAt))}</Fact>}
            {!event && <Fact label="Last checked">{formatDayTime(new Date(a.LastEvaluatedAt))}</Fact>}
            {LABELS.filter((l) => a.Labels[l.key]).map((l) => (
              <Fact key={l.key} label={l.label}>
                <span className={["mac", "ip", "peer"].includes(l.key) ? "font-mono" : ""}>
                  {l.key === "first_seen" ? formatDayTime(new Date(a.Labels[l.key])) : a.Labels[l.key]}
                </span>
              </Fact>
            ))}
          </dl>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Notifications</CardTitle>
          <CardDescription>
            {a.Deliveries.length === 0
              ? rule && rule.ChannelIDs.length === 0
                ? "The rule doesn't send to any channel, so this alert is only shown here."
                : "Nothing was sent about this alert."
              : "Every message about this alert, and how sending it went."}
          </CardDescription>
        </CardHeader>
        {a.Deliveries.length > 0 && (
          <CardContent className="px-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-6">When</TableHead>
                  <TableHead>Message</TableHead>
                  <TableHead>Channel</TableHead>
                  <TableHead className="pr-6">Result</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {a.Deliveries.map((d, index) => (
                  <TableRow key={index}>
                    <TableCell className="pl-6 whitespace-nowrap text-muted-foreground">
                      {formatDayTime(new Date(d.CreatedAt))}
                    </TableCell>
                    <TableCell>{KINDS[d.Kind]}</TableCell>
                    <TableCell>{channelName(d.ChannelID)}</TableCell>
                    <TableCell className="pr-6 whitespace-normal">
                      <StatusCell delivery={d} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        )}
      </Card>
    </Page>
  )
}
