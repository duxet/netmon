import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { Loader2Icon } from "lucide-react"
import * as React from "react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
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
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  BYTE_UNITS,
  DEFAULT_FORM,
  DIRECTIONS,
  formToInput,
  knownDeviceLabel,
  REPEATS,
  ruleToForm,
  RULE_TYPES,
  SEVERITIES,
  thresholdBytes,
  WINDOWS,
  type ByteUnit,
  type RuleForm,
} from "@/lib/alerts"
import {
  createAlertRule,
  errorText,
  updateAlertRule,
  type AlertRule,
  type AlertRuleType,
  type Severity,
  type TrafficDirection,
} from "@/lib/api"
import {
  alertRuleOptionsQuery,
  knownDevicesQuery,
  notificationChannelsQuery,
} from "@/lib/queries"

// Radix Select can't have an empty value.
const ALL_APPS = "__all__"
const ONCE = "once"

function RuleFormBody({
  rule,
  preset,
  onDone,
}: {
  rule?: AlertRule
  preset?: Partial<RuleForm>
  onDone: () => void
}) {
  const queryClient = useQueryClient()
  const options = useQuery(alertRuleOptionsQuery())
  const devices = useQuery(knownDevicesQuery())
  const channels = useQuery(notificationChannelsQuery())
  const [form, setForm] = React.useState<RuleForm>(() =>
    rule ? ruleToForm(rule) : { ...DEFAULT_FORM, ...preset }
  )
  const set = <K extends keyof RuleForm>(key: K, value: RuleForm[K]) =>
    setForm((current) => ({ ...current, [key]: value }))
  const toggle = <K extends "macs" | "channelIds">(key: K, item: RuleForm[K][number], on: boolean) =>
    setForm((current) => {
      const list = current[key] as (typeof item)[]
      return { ...current, [key]: on ? [...list, item] : list.filter((x) => x !== item) }
    })

  const id = React.useId()
  const traffic = form.type === "traffic_threshold"
  const validThreshold = !traffic || Number.isFinite(thresholdBytes(form))
  const canSave = form.name.trim() !== "" && validThreshold

  const save = useMutation({
    mutationFn: () => (rule ? updateAlertRule(rule.ID, formToInput(form)) : createAlertRule(formToInput(form))),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["alert-rules"] }),
        queryClient.invalidateQueries({ queryKey: ["alerts"] }),
        queryClient.invalidateQueries({ queryKey: ["notification-channels"] }),
      ])
      onDone()
    },
  })

  const deviceList = devices.data ?? []
  const channelList = channels.data ?? []

  return (
    <form
      className="flex min-h-0 flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault()
        if (canSave) save.mutate()
      }}
    >
      <DialogHeader>
        <DialogTitle>{rule ? "Edit alert rule" : "Add alert rule"}</DialogTitle>
        <DialogDescription>
          Rules are checked every minute, after netmon records the traffic.
        </DialogDescription>
      </DialogHeader>

      <div className="-mx-4 min-h-0 overflow-y-auto px-4">
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor={`${id}-name`}>Name</FieldLabel>
            <Input
              id={`${id}-name`}
              value={form.name}
              onChange={(event) => set("name", event.target.value)}
              maxLength={64}
              placeholder="e.g. Big downloads"
              autoComplete="off"
              autoFocus
            />
          </Field>

          <Field>
            <FieldLabel htmlFor={`${id}-type`}>Alert when</FieldLabel>
            <Select value={form.type} onValueChange={(value) => set("type", value as AlertRuleType)}>
              <SelectTrigger id={`${id}-type`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent position="popper">
                {RULE_TYPES.map((t) => (
                  <SelectItem key={t.value} value={t.value}>
                    {t.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {rule && form.type !== rule.Type && (
              <FieldDescription>Changing what a rule checks resolves its firing alerts.</FieldDescription>
            )}
          </Field>

          {!traffic && (
            <Field orientation="horizontal">
              <Switch
                id={`${id}-random`}
                checked={form.ignoreRandomMacs}
                onCheckedChange={(checked) => set("ignoreRandomMacs", checked)}
              />
              <FieldContent>
                <FieldLabel htmlFor={`${id}-random`}>Ignore private Wi-Fi addresses</FieldLabel>
                <FieldDescription>
                  Phones join with a random address of their own per network, and may change it.
                  Turn this on if that causes repeated alerts.
                </FieldDescription>
              </FieldContent>
            </Field>
          )}

          {traffic && (
            <>
              <Field>
                <FieldLabel>Traffic</FieldLabel>
                <ToggleGroup
                  type="single"
                  variant="outline"
                  size="sm"
                  value={form.direction}
                  onValueChange={(value) => value && set("direction", value as TrafficDirection)}
                >
                  {DIRECTIONS.map((d) => (
                    <ToggleGroupItem key={d.value} value={d.value} className="px-3">
                      {d.label}
                    </ToggleGroupItem>
                  ))}
                </ToggleGroup>
              </Field>

              <div className="grid gap-4 sm:grid-cols-2">
                <Field data-invalid={!validThreshold || undefined}>
                  <FieldLabel htmlFor={`${id}-amount`}>More than</FieldLabel>
                  <div className="flex gap-2">
                    <Input
                      id={`${id}-amount`}
                      inputMode="decimal"
                      value={form.amount}
                      onChange={(event) => set("amount", event.target.value)}
                      aria-invalid={!validThreshold || undefined}
                      className="min-w-0"
                    />
                    <Select value={form.unit} onValueChange={(value) => set("unit", value as ByteUnit)}>
                      <SelectTrigger aria-label="Unit" className="w-20 shrink-0">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent position="popper">
                        {Object.keys(BYTE_UNITS).map((unit) => (
                          <SelectItem key={unit} value={unit}>
                            {unit}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                </Field>
                <Field>
                  <FieldLabel htmlFor={`${id}-window`}>In</FieldLabel>
                  <Select
                    value={String(form.windowSeconds)}
                    onValueChange={(value) => set("windowSeconds", Number(value))}
                  >
                    <SelectTrigger id={`${id}-window`} className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent position="popper">
                      {WINDOWS.map((w) => (
                        <SelectItem key={w.seconds} value={String(w.seconds)}>
                          {w.seconds === 3600 ? "the last hour" : `the last ${w.label}`}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
              </div>

              <Field>
                <FieldLabel htmlFor={`${id}-app`}>Count</FieldLabel>
                <Select
                  value={form.app || ALL_APPS}
                  onValueChange={(value) => set("app", value === ALL_APPS ? "" : value)}
                >
                  <SelectTrigger id={`${id}-app`} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent position="popper">
                    <SelectItem value={ALL_APPS}>All traffic</SelectItem>
                    {options.data?.Apps.map((app) => (
                      <SelectItem key={app} value={app}>
                        {app === "Other" ? "Traffic of no known app" : `${app} only`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>

              <FieldSet>
                <FieldLegend variant="label">Devices</FieldLegend>
                <FieldDescription>
                  {form.macs.length === 0
                    ? "Every device, each on its own. Pick some to watch only those."
                    : `${form.macs.length} picked.`}
                </FieldDescription>
                <div className="max-h-40 overflow-y-auto rounded-lg border p-2">
                  {deviceList.length === 0 && (
                    <p className="p-1 text-sm text-muted-foreground">
                      {devices.isPending ? "Loading devices…" : "No devices seen yet."}
                    </p>
                  )}
                  {deviceList.map((device) => (
                    <Field key={device.MACAddress} orientation="horizontal" className="rounded-md p-1 hover:bg-muted/50">
                      <Checkbox
                        id={`${id}-mac-${device.MACAddress}`}
                        checked={form.macs.includes(device.MACAddress)}
                        onCheckedChange={(checked) => toggle("macs", device.MACAddress, checked === true)}
                      />
                      <FieldLabel htmlFor={`${id}-mac-${device.MACAddress}`} className="min-w-0 font-normal">
                        <span className="truncate">{knownDeviceLabel(device)}</span>
                        <span className="shrink-0 font-mono text-xs text-muted-foreground">{device.MACAddress}</span>
                      </FieldLabel>
                    </Field>
                  ))}
                </div>
              </FieldSet>
            </>
          )}

          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor={`${id}-severity`}>Severity</FieldLabel>
              <Select value={form.severity} onValueChange={(value) => set("severity", value as Severity)}>
                <SelectTrigger id={`${id}-severity`} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent position="popper">
                  {SEVERITIES.map((s) => (
                    <SelectItem key={s.value} value={s.value}>
                      {s.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {traffic && (
              <Field>
                <FieldLabel htmlFor={`${id}-repeat`}>While it lasts, notify</FieldLabel>
                <Select
                  value={form.repeatSeconds === null ? ONCE : String(form.repeatSeconds)}
                  onValueChange={(value) => set("repeatSeconds", value === ONCE ? null : Number(value))}
                >
                  <SelectTrigger id={`${id}-repeat`} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent position="popper">
                    {REPEATS.map((r) => (
                      <SelectItem key={r.label} value={r.seconds === null ? ONCE : String(r.seconds)}>
                        {r.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
          </div>

          {traffic && (
            <Field orientation="horizontal">
              <Switch
                id={`${id}-resolve`}
                checked={form.notifyOnResolve}
                onCheckedChange={(checked) => set("notifyOnResolve", checked)}
              />
              <FieldLabel htmlFor={`${id}-resolve`}>Also notify when it's over</FieldLabel>
            </Field>
          )}

          <FieldSet>
            <FieldLegend variant="label">Send to</FieldLegend>
            {channelList.length === 0 ? (
              <FieldDescription>
                {channels.isPending ? "Loading channels…" : (
                  <>
                    No notification channels yet. Alerts still show on the Alerts page;{" "}
                    <Link to="/settings/notifications">add a channel</Link> to get them on your phone.
                  </>
                )}
              </FieldDescription>
            ) : (
              <div className="flex flex-col gap-1">
                {channelList.map((channel) => (
                  <Field key={channel.ID} orientation="horizontal">
                    <Checkbox
                      id={`${id}-channel-${channel.ID}`}
                      checked={form.channelIds.includes(channel.ID)}
                      onCheckedChange={(checked) => toggle("channelIds", channel.ID, checked === true)}
                    />
                    <FieldLabel htmlFor={`${id}-channel-${channel.ID}`} className="font-normal">
                      {channel.Name}
                      {!channel.Enabled && <span className="text-muted-foreground">(off)</span>}
                    </FieldLabel>
                  </Field>
                ))}
              </div>
            )}
          </FieldSet>
        </FieldGroup>
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
          {rule ? "Save" : "Add rule"}
        </Button>
      </DialogFooter>
    </form>
  )
}

/** A dialog to add an alert rule, or edit one, opened by its trigger. */
export function AlertRuleDialog({
  rule,
  preset,
  children,
}: {
  rule?: AlertRule
  /** Starting values for a new rule. */
  preset?: Partial<RuleForm>
  /** The button that opens the dialog. */
  children: React.ReactNode
}) {
  const [open, setOpen] = React.useState(false)

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{children}</DialogTrigger>
      <DialogContent className="flex max-h-[calc(100dvh-2rem)] flex-col sm:max-w-lg">
        {/* The content mounts each time the dialog opens, so the form starts
            afresh, and stays until the closing animation is over. */}
        <RuleFormBody rule={rule} preset={preset} onDone={() => setOpen(false)} />
      </DialogContent>
    </Dialog>
  )
}
