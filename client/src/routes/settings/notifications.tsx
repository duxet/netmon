import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { BellIcon, CheckIcon, Loader2Icon, PencilIcon, PlusIcon, SendIcon, Trash2Icon } from "lucide-react"

import { NotificationChannelDialog } from "@/components/notification-channel-dialog"
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
import {
  deleteNotificationChannel,
  errorText,
  testNotificationChannel,
  updateNotificationChannel,
  type NotificationChannel,
  type NotificationService,
} from "@/lib/api"
import { serviceName } from "@/lib/notifications"
import { notificationChannelsQuery, notificationServicesQuery } from "@/lib/queries"

export const Route = createFileRoute("/settings/notifications")({
  staticData: { breadcrumb: "Notifications" },
  component: NotificationsPage,
})

function useInvalidateChannels() {
  const queryClient = useQueryClient()
  return () => queryClient.invalidateQueries({ queryKey: ["notification-channels"] })
}

function DeleteChannelButton({ channel }: { channel: NotificationChannel }) {
  const invalidate = useInvalidateChannels()
  const remove = useMutation({
    mutationFn: () => deleteNotificationChannel(channel.ID),
    onSuccess: invalidate,
  })

  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          className="text-muted-foreground hover:text-destructive"
          aria-label={`Delete ${channel.Name}`}
        >
          <Trash2Icon />
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete “{channel.Name}”?</AlertDialogTitle>
          <AlertDialogDescription>
            netmon will stop sending notifications to it. To pause it instead, turn it off.
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

function ChannelRow({
  channel,
  services,
}: {
  channel: NotificationChannel
  services: NotificationService[]
}) {
  const invalidate = useInvalidateChannels()
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => updateNotificationChannel(channel.ID, { enabled }),
    onSettled: invalidate,
  })
  const test = useMutation({ mutationFn: () => testNotificationChannel(channel.ID) })

  // Until the server answers, show the switch where it was turned.
  const enabled = toggle.isPending ? toggle.variables : channel.Enabled
  const switchId = `channel-${channel.ID}-enabled`

  return (
    <li className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium">{channel.Name}</span>
          <Badge variant="secondary">{serviceName(services, channel.Service)}</Badge>
        </div>
        <p className="truncate font-mono text-xs text-muted-foreground" title={channel.MaskedURL}>
          {channel.MaskedURL}
        </p>
        {test.isSuccess && (
          <p className="flex items-center gap-1.5 text-sm text-muted-foreground" role="status">
            <CheckIcon className="size-4 text-chart-3" />
            Test message sent. Check that it arrived.
          </p>
        )}
        {test.isError && (
          <p className="text-sm text-destructive" role="alert">
            {errorText(test.error)}
          </p>
        )}
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
        <Button variant="outline" size="sm" disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? (
            <Loader2Icon data-icon="inline-start" className="animate-spin" />
          ) : (
            <SendIcon data-icon="inline-start" />
          )}
          Test
        </Button>
        <NotificationChannelDialog channel={channel} services={services}>
          <Button
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground hover:text-foreground"
            aria-label={`Edit ${channel.Name}`}
          >
            <PencilIcon />
          </Button>
        </NotificationChannelDialog>
        <DeleteChannelButton channel={channel} />
      </div>
    </li>
  )
}

function NotificationsPage() {
  const channels = useQuery(notificationChannelsQuery())
  const services = useQuery(notificationServicesQuery())
  const serviceList = services.data ?? []

  const addButton = (
    <NotificationChannelDialog services={serviceList}>
      <Button disabled={!services.data}>
        <PlusIcon data-icon="inline-start" />
        Add channel
      </Button>
    </NotificationChannelDialog>
  )

  return (
    <Page>
      <PageHeader
        title="Notifications"
        description="Where netmon sends alerts: Telegram, ntfy, email, a webhook and more."
      >
        {channels.data && channels.data.length > 0 && addButton}
      </PageHeader>

      {(channels.isPending || services.isPending) && (
        <Card className="gap-0 p-0">
          {Array.from({ length: 2 }, (_, index) => (
            <div key={index} className="flex flex-col gap-2 p-4">
              <Skeleton className="h-4 w-40" />
              <Skeleton className="h-3 w-72" />
            </div>
          ))}
        </Card>
      )}

      {channels.isError && <ErrorState error={channels.error} onRetry={() => channels.refetch()} />}
      {services.isError && <ErrorState error={services.error} onRetry={() => services.refetch()} />}

      {channels.data && services.data && channels.data.length === 0 && (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <BellIcon />
            </EmptyMedia>
            <EmptyTitle>No notification channels yet</EmptyTitle>
            <EmptyDescription>
              Add a channel to tell netmon where to reach you, then send it a test message.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>{addButton}</EmptyContent>
        </Empty>
      )}

      {channels.data && services.data && channels.data.length > 0 && (
        <Card className="gap-0 p-0">
          <ul className="divide-y">
            {channels.data.map((channel) => (
              <ChannelRow key={channel.ID} channel={channel} services={serviceList} />
            ))}
          </ul>
        </Card>
      )}
    </Page>
  )
}
