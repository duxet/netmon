import { useMutation, useQueryClient } from "@tanstack/react-query"
import { CheckIcon, EyeIcon, EyeOffIcon, Loader2Icon, SendIcon } from "lucide-react"
import * as React from "react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  createNotificationChannel,
  errorText,
  testNotificationChannel,
  testNotificationURL,
  updateNotificationChannel,
  type NotificationChannel,
  type NotificationService,
} from "@/lib/api"
import { urlScheme } from "@/lib/notifications"

const MAX_NAME_LENGTH = 64

/** Shows how a test went, next to the button that sent it. */
function TestResult({ mutation }: { mutation: { isSuccess: boolean; isError: boolean; error: unknown } }) {
  if (mutation.isSuccess) {
    return (
      <p className="flex items-center gap-1.5 text-sm text-muted-foreground" role="status">
        <CheckIcon className="size-4 text-chart-3" />
        Test message sent. Check that it arrived.
      </p>
    )
  }
  if (mutation.isError) {
    return (
      <p className="text-sm text-destructive" role="alert">
        {errorText(mutation.error)}
      </p>
    )
  }
  return null
}

function ChannelForm({
  channel,
  services,
  onDone,
}: {
  /** The channel to edit, or undefined to add one. */
  channel?: NotificationChannel
  services: NotificationService[]
  onDone: () => void
}) {
  const queryClient = useQueryClient()
  const nameId = React.useId()
  const serviceId = React.useId()
  const urlId = React.useId()

  const [name, setName] = React.useState(channel?.Name ?? "")
  const [chosen, setChosen] = React.useState(channel?.Service ?? services[0]?.Scheme ?? "")
  const [url, setURL] = React.useState("")
  // An existing channel keeps its URL unless it is replaced.
  const [replacingURL, setReplacingURL] = React.useState(!channel)
  const [showURL, setShowURL] = React.useState(false)

  // The URL decides the service; the picker only helps writing one.
  const typed = urlScheme(url)
  const scheme = typed && services.some((s) => s.Scheme === typed) ? typed : chosen
  const service = services.find((s) => s.Scheme === scheme)
  const common = services.filter((s) => s.ExampleURL)
  const others = services.filter((s) => !s.ExampleURL)

  const trimmedName = name.trim()
  const trimmedURL = url.trim()
  const newURL = replacingURL && trimmedURL !== "" ? trimmedURL : undefined
  const canSave = trimmedName !== "" && (channel ? true : newURL !== undefined)
  const canTest = newURL !== undefined || (channel !== undefined && !replacingURL)

  const save = useMutation({
    mutationFn: () =>
      channel
        ? updateNotificationChannel(channel.ID, { name: trimmedName, url: newURL })
        : createNotificationChannel({ name: trimmedName, url: newURL ?? "" }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["notification-channels"] })
      onDone()
    },
  })

  const test = useMutation({
    mutationFn: () =>
      newURL !== undefined
        ? testNotificationURL(newURL, trimmedName)
        : testNotificationChannel(channel!.ID),
  })

  // A test result is about the URL it was sent to.
  const changeURL = (next: string) => {
    setURL(next)
    test.reset()
  }
  const replaceURL = () => {
    setReplacingURL(true)
    test.reset()
  }

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault()
        if (canSave) save.mutate()
      }}
    >
      <DialogHeader>
        <DialogTitle>{channel ? "Edit channel" : "Add notification channel"}</DialogTitle>
        <DialogDescription>
          netmon sends alerts through{" "}
          <a
            href="https://shoutrrr.nickfedor.com/latest/services/overview/"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            shoutrrr
          </a>
          : a channel is a service URL that holds everything needed to reach you.
        </DialogDescription>
      </DialogHeader>

      <FieldGroup>
        <Field>
          <FieldLabel htmlFor={nameId}>Name</FieldLabel>
          <Input
            id={nameId}
            value={name}
            onChange={(event) => setName(event.target.value)}
            maxLength={MAX_NAME_LENGTH}
            placeholder="e.g. My phone"
            autoComplete="off"
            autoFocus
          />
        </Field>

        {replacingURL && (
          <Field>
            <FieldLabel htmlFor={serviceId}>Service</FieldLabel>
            <Select value={scheme} onValueChange={setChosen}>
              <SelectTrigger id={serviceId} className="w-full">
                <SelectValue placeholder="Choose a service" />
              </SelectTrigger>
              <SelectContent position="popper">
                <SelectGroup>
                  {common.map((s) => (
                    <SelectItem key={s.Scheme} value={s.Scheme}>
                      {s.Name}
                    </SelectItem>
                  ))}
                </SelectGroup>
                {others.length > 0 && (
                  <>
                    <SelectSeparator />
                    <SelectGroup>
                      <SelectLabel>Other services</SelectLabel>
                      {others.map((s) => (
                        <SelectItem key={s.Scheme} value={s.Scheme}>
                          {s.Name}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </>
                )}
              </SelectContent>
            </Select>
          </Field>
        )}

        <Field>
          <FieldLabel htmlFor={urlId}>Service URL</FieldLabel>
          {replacingURL ? (
            <>
              <InputGroup>
                <InputGroupInput
                  id={urlId}
                  type={showURL ? "text" : "password"}
                  value={url}
                  onChange={(event) => changeURL(event.target.value)}
                  placeholder={service?.ExampleURL || `${scheme || "service"}://…`}
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                  data-1p-ignore
                  data-lpignore="true"
                />
                <InputGroupAddon align="inline-end">
                  <InputGroupButton
                    size="icon-xs"
                    aria-label={showURL ? "Hide URL" : "Show URL"}
                    onClick={() => setShowURL((shown) => !shown)}
                  >
                    {showURL ? <EyeOffIcon /> : <EyeIcon />}
                  </InputGroupButton>
                </InputGroupAddon>
              </InputGroup>
              <FieldDescription>
                {service?.ExampleURL && (
                  <>
                    Format: <span className="font-mono break-all">{service.ExampleURL}</span>.{" "}
                  </>
                )}
                {service && (
                  <a href={service.DocsURL} target="_blank" rel="noreferrer">
                    Where to find these values
                  </a>
                )}
                {channel && " Leave empty to keep the current URL."}
              </FieldDescription>
            </>
          ) : (
            <div className="flex items-center gap-2">
              <p
                className="min-w-0 flex-1 truncate rounded-lg border bg-muted/40 px-2.5 py-1.5 font-mono text-xs text-muted-foreground"
                title={channel?.MaskedURL}
              >
                {channel?.MaskedURL}
              </p>
              <Button type="button" variant="outline" size="sm" onClick={replaceURL}>
                Replace
              </Button>
            </div>
          )}
        </Field>
      </FieldGroup>

      <div className="flex flex-col gap-2">
        <div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!canTest || test.isPending}
            onClick={() => test.mutate()}
          >
            {test.isPending ? (
              <Loader2Icon data-icon="inline-start" className="animate-spin" />
            ) : (
              <SendIcon data-icon="inline-start" />
            )}
            Send test message
          </Button>
        </div>
        <TestResult mutation={test} />
      </div>

      {save.isError && <FieldError>{errorText(save.error)}</FieldError>}

      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline">
            Cancel
          </Button>
        </DialogClose>
        <Button type="submit" disabled={!canSave || save.isPending}>
          {save.isPending && <Loader2Icon data-icon="inline-start" className="animate-spin" />}
          {channel ? "Save" : "Add channel"}
        </Button>
      </DialogFooter>
    </form>
  )
}

/** A dialog to add a notification channel, or edit one, opened by its trigger. */
export function NotificationChannelDialog({
  channel,
  services,
  children,
}: {
  channel?: NotificationChannel
  services: NotificationService[]
  /** The button that opens the dialog. */
  children: React.ReactNode
}) {
  const [open, setOpen] = React.useState(false)

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{children}</DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        {/* The content mounts each time the dialog opens, so the form starts
            afresh, and stays until the closing animation is over. */}
        <ChannelForm channel={channel} services={services} onDone={() => setOpen(false)} />
      </DialogContent>
    </Dialog>
  )
}
