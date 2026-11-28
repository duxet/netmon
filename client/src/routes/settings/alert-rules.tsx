import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { ListChecksIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { AlertRuleDialog } from "@/components/alert-rule-dialog"
import { Page, PageHeader } from "@/components/page-header"
import { ErrorState } from "@/components/query-state"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { SnoozeRuleButton } from "@/components/snooze-rule"
import { describeRule, isSnoozed, PRESETS } from "@/lib/alerts"
import { formatDayTime } from "@/lib/format"
import { useNetworks } from "@/lib/networks"
import {
  deleteAlertRule,
  errorText,
  setAlertRuleEnabled,
  type AlertRule,
  type NotificationChannel,
} from "@/lib/api"
import { alertRulesQuery, notificationChannelsQuery } from "@/lib/queries"

export const Route = createFileRoute("/settings/alert-rules")({
  staticData: { breadcrumb: "Alert rules" },
  component: AlertRulesPage,
})

function useInvalidateRules() {
  const queryClient = useQueryClient()
  return () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: ["alert-rules"] }),
      queryClient.invalidateQueries({ queryKey: ["alerts"] }),
      queryClient.invalidateQueries({ queryKey: ["notification-channels"] }),
    ])
}

function DeleteRuleButton({ rule }: { rule: AlertRule }) {
  const invalidate = useInvalidateRules()
  const remove = useMutation({ mutationFn: () => deleteAlertRule(rule.ID), onSuccess: invalidate })

  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          className="text-muted-foreground hover:text-destructive"
          aria-label={`Delete ${rule.Name}`}
        >
          <Trash2Icon />
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete “{rule.Name}”?</AlertDialogTitle>
          <AlertDialogDescription>
            Its alerts stay in the history, resolved. To pause the rule instead, turn it off.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {remove.isError && <p className="text-sm text-destructive">{errorText(remove.error)}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={() => remove.mutate()}>
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function channelNames(rule: AlertRule, channels: NotificationChannel[]): string {
  const names = rule.ChannelIDs.map((id) => channels.find((c) => c.ID === id)?.Name).filter(Boolean)
  return names.length > 0 ? `Sends to ${names.join(", ")}` : "Not sent anywhere: shown on the Alerts page only"
}

function RuleRow({ rule, channels }: { rule: AlertRule; channels: NotificationChannel[] }) {
  const invalidate = useInvalidateRules()
  const { list: networks } = useNetworks()
  const networkNames = new Map(networks.map((n) => [n.ID, n.Name]))
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => setAlertRuleEnabled(rule.ID, enabled),
    onSettled: invalidate,
  })
  const enabled = toggle.isPending ? toggle.variables : rule.Enabled
  const switchId = `rule-${rule.ID}-enabled`

  return (
    <li className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium">{rule.Name}</span>
          {rule.FiringAlerts > 0 && (
            <Link to="/alerts">
              <Badge variant="destructive">{rule.FiringAlerts} firing</Badge>
            </Link>
          )}
          {isSnoozed(rule) && rule.SnoozedUntil && (
            <Badge variant="outline">Snoozed until {formatDayTime(new Date(rule.SnoozedUntil))}</Badge>
          )}
          {rule.QuietFrom && rule.QuietTo && (
            <Badge variant="outline">
              Quiet {rule.QuietFrom}–{rule.QuietTo}
            </Badge>
          )}
        </div>
        <p className="text-sm text-muted-foreground">{describeRule(rule, networkNames)}</p>
        <p className="text-xs text-muted-foreground">{channelNames(rule, channels)}</p>
        {toggle.isError && (
          <p className="text-sm text-destructive" role="alert">
            {errorText(toggle.error)}
          </p>
        )}
      </div>

      <div className="flex shrink-0 items-center gap-1">
        <label htmlFor={switchId} className="mr-2 flex items-center gap-2 text-sm text-muted-foreground">
          <Switch
            id={switchId}
            checked={enabled}
            disabled={toggle.isPending}
            onCheckedChange={(checked) => toggle.mutate(checked)}
          />
          {enabled ? "On" : "Off"}
        </label>
        <SnoozeRuleButton rule={rule} />
        <AlertRuleDialog rule={rule}>
          <Button
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground hover:text-foreground"
            aria-label={`Edit ${rule.Name}`}
          >
            <PencilIcon />
          </Button>
        </AlertRuleDialog>
        <DeleteRuleButton rule={rule} />
      </div>
    </li>
  )
}

function AlertRulesPage() {
  const rules = useQuery(alertRulesQuery())
  const channels = useQuery(notificationChannelsQuery())

  return (
    <Page>
      <PageHeader
        title="Alert rules"
        description="When netmon raises an alert, checked every minute against the recorded traffic."
      >
        {rules.data && rules.data.length > 0 && (
          <AlertRuleDialog>
            <Button>
              <PlusIcon data-icon="inline-start" />
              Add rule
            </Button>
          </AlertRuleDialog>
        )}
      </PageHeader>

      {rules.isPending && (
        <Card className="gap-0 p-0">
          {Array.from({ length: 2 }, (_, index) => (
            <div key={index} className="flex flex-col gap-2 p-4">
              <Skeleton className="h-4 w-40" />
              <Skeleton className="h-3 w-72" />
            </div>
          ))}
        </Card>
      )}

      {rules.isError && <ErrorState error={rules.error} onRetry={() => rules.refetch()} />}

      {rules.data?.length === 0 && (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <ListChecksIcon />
            </EmptyMedia>
            <EmptyTitle>No alert rules yet</EmptyTitle>
            <EmptyDescription>
              Start from one of these, or write your own. Alerts go to your{" "}
              <Link to="/settings/notifications">notification channels</Link>.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:justify-center">
            {PRESETS.map((preset) => (
              <AlertRuleDialog key={preset.label} preset={preset.form}>
                <Button variant="outline">{preset.label}</Button>
              </AlertRuleDialog>
            ))}
            <AlertRuleDialog>
              <Button>
                <PlusIcon data-icon="inline-start" />
                Add rule
              </Button>
            </AlertRuleDialog>
          </EmptyContent>
        </Empty>
      )}

      {rules.data && rules.data.length > 0 && (
        <Card className="gap-0 p-0">
          <ul className="divide-y">
            {rules.data.map((rule) => (
              <RuleRow key={rule.ID} rule={rule} channels={channels.data ?? []} />
            ))}
          </ul>
        </Card>
      )}
    </Page>
  )
}
