import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { Loader2Icon, ScanSearchIcon } from "lucide-react"
import * as React from "react"

import { Badge } from "@/components/ui/badge"
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
  formProblem,
  formToInput,
  BASELINES,
  INACTIVE_DURATIONS,
  isEventRule,
  knownDeviceLabel,
  parseCountries,
  PROTOCOLS,
  REPEATS,
  ruleToForm,
  RULE_TYPES,
  SEVERITIES,
  WINDOWS,
  type ByteUnit,
  type RuleForm,
} from "@/lib/alerts"
import {
  createAlertRule,
  errorText,
  previewAlertRule,
  updateAlertRule,
  type AlertRule,
  type AlertRulePreview,
  type AlertRuleType,
  type KnownDevice,
  type PortParams,
  type Severity,
  type TrafficDirection,
} from "@/lib/api"
import { deviceIconInfo, isDeviceIcon } from "@/lib/device-icons"
import { formatBytes } from "@/lib/format"
import { countryName } from "@/lib/network"
import {
  alertRuleOptionsQuery,
  knownDevicesQuery,
  notificationChannelsQuery,
} from "@/lib/queries"

// Radix Select can't have an empty value.
const ALL_APPS = "__all__"
const ONCE = "once"
const PREVIEW_NAME = "New rule"

type SetField = <K extends keyof RuleForm>(key: K, value: RuleForm[K]) => void

/** The picture of a device in the list to pick from. */
function KnownDeviceIcon({ icon }: { icon: string | null }) {
  const { Icon } = deviceIconInfo(isDeviceIcon(icon) ? icon : null)
  return <Icon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
}

function DevicePicker({
  id,
  devices,
  loading,
  selected,
  onToggle,
  description,
}: {
  id: string
  devices: KnownDevice[]
  loading: boolean
  selected: string[]
  onToggle: (mac: string, on: boolean) => void
  description: string
}) {
  return (
    <FieldSet>
      <FieldLegend variant="label">Devices</FieldLegend>
      <FieldDescription>{description}</FieldDescription>
      <div className="max-h-40 overflow-y-auto rounded-lg border p-2">
        {devices.length === 0 && (
          <p className="p-1 text-sm text-muted-foreground">
            {loading ? "Loading devices…" : "No devices seen yet."}
          </p>
        )}
        {devices.map((device) => (
          <Field key={device.MACAddress} orientation="horizontal" className="rounded-md p-1 hover:bg-muted/50">
            <Checkbox
              id={`${id}-mac-${device.MACAddress}`}
              checked={selected.includes(device.MACAddress)}
              onCheckedChange={(checked) => onToggle(device.MACAddress, checked === true)}
            />
            <FieldLabel htmlFor={`${id}-mac-${device.MACAddress}`} className="min-w-0 font-normal">
              <KnownDeviceIcon icon={device.Icon} />
              <span className="truncate">{knownDeviceLabel(device)}</span>
              <span className="shrink-0 font-mono text-xs text-muted-foreground">{device.MACAddress}</span>
            </FieldLabel>
          </Field>
        ))}
      </div>
    </FieldSet>
  )
}

function TrafficFields({ id, form, set, apps }: { id: string; form: RuleForm; set: SetField; apps: string[] }) {
  return (
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
        <Field>
          <FieldLabel htmlFor={`${id}-amount`}>More than</FieldLabel>
          <div className="flex gap-2">
            <Input
              id={`${id}-amount`}
              inputMode="decimal"
              value={form.amount}
              onChange={(event) => set("amount", event.target.value)}
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
          <Select value={String(form.windowSeconds)} onValueChange={(value) => set("windowSeconds", Number(value))}>
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
        <Select value={form.app || ALL_APPS} onValueChange={(value) => set("app", value === ALL_APPS ? "" : value)}>
          <SelectTrigger id={`${id}-app`} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent position="popper">
            <SelectItem value={ALL_APPS}>All traffic</SelectItem>
            {apps.map((app) => (
              <SelectItem key={app} value={app}>
                {app === "Other" ? "Traffic of no known app" : `${app} only`}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>

      <Field orientation="horizontal">
        <Switch id={`${id}-network`} checked={form.network} onCheckedChange={(checked) => set("network", checked)} />
        <FieldContent>
          <FieldLabel htmlFor={`${id}-network`}>Whole network</FieldLabel>
          <FieldDescription>
            Add up the internet traffic of all devices, as for an ISP data cap, instead of checking each
            device on its own.
          </FieldDescription>
        </FieldContent>
      </Field>
    </>
  )
}

function PortFields({ id, form, set }: { id: string; form: RuleForm; set: SetField }) {
  return (
    <div className="grid gap-4 sm:grid-cols-[1fr_auto]">
      <Field>
        <FieldLabel htmlFor={`${id}-ports`}>Ports</FieldLabel>
        <Input
          id={`${id}-ports`}
          value={form.ports}
          onChange={(event) => set("ports", event.target.value)}
          placeholder="e.g. 23, 2323"
          autoComplete="off"
          className="font-mono"
        />
        <FieldDescription>Connections a device opens to these ports of any address.</FieldDescription>
      </Field>
      <Field>
        <FieldLabel>Protocol</FieldLabel>
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          value={form.protocol}
          onValueChange={(value) => value && set("protocol", value as PortParams["protocol"])}
        >
          {PROTOCOLS.map((p) => (
            <ToggleGroupItem key={p.value} value={p.value} className="px-3">
              {p.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </Field>
    </div>
  )
}

function CountryFields({ id, form, set, geoIP }: { id: string; form: RuleForm; set: SetField; geoIP: boolean }) {
  const names = parseCountries(form.countries)
    .filter((code) => /^[A-Z]{2}$/.test(code))
    .map((code) => countryName(code))

  return (
    <>
      <Field>
        <FieldLabel>Alert on traffic</FieldLabel>
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          value={form.countryMode}
          onValueChange={(value) => value && set("countryMode", value as RuleForm["countryMode"])}
        >
          <ToggleGroupItem value="deny" className="px-3">
            With these countries
          </ToggleGroupItem>
          <ToggleGroupItem value="allow" className="px-3">
            With any other country
          </ToggleGroupItem>
        </ToggleGroup>
      </Field>
      <Field>
        <FieldLabel htmlFor={`${id}-countries`}>Countries</FieldLabel>
        <Input
          id={`${id}-countries`}
          value={form.countries}
          onChange={(event) => set("countries", event.target.value)}
          placeholder="Two-letter codes, e.g. RU, CN"
          autoComplete="off"
          className="font-mono"
        />
        <FieldDescription>
          {names.length > 0 ? names.join(", ") : "ISO country codes, as in DE for Germany."}
        </FieldDescription>
        {!geoIP && (
          <FieldError>
            netmon has no GeoIP database yet: put GeoLite2-Country.mmdb next to the binary and restart it,
            or this rule finds nothing.
          </FieldError>
        )}
      </Field>
    </>
  )
}

function formatFindingValue(type: AlertRuleType, value: number | null): string {
  if (value === null) return ""
  if (type === "device_inactive" || type === "device_online") {
    const minutes = Math.round(value / 60)
    return minutes < 60 ? `quiet ${minutes} min` : `quiet ${Math.floor(minutes / 60)} h ${minutes % 60} min`
  }
  return formatBytes(value)
}

function PreviewResult({ type, preview }: { type: AlertRuleType; preview: AlertRulePreview }) {
  const event = isEventRule(type)
  const firing = preview.Findings.filter((f) => f.Fires).length
  const summary = event
    ? preview.Total === 0
      ? "Nothing in the last 24 hours."
      : `In the last 24 hours, it would have alerted ${preview.Total} ${preview.Total === 1 ? "time" : "times"}:`
    : preview.Total === 0
      ? "Nothing to measure right now."
      : firing === 0
        ? "Right now it wouldn't alert. What it measures:"
        : `Right now it would alert about ${firing === 1 ? "this" : `these ${firing}`}:`

  return (
    <div
      // The result shows below the button, at the bottom of a form that may
      // scroll: bring it into view.
      ref={(element) => element?.scrollIntoView({ block: "nearest", behavior: "smooth" })}
      className="flex flex-col gap-2 rounded-lg border bg-muted/30 p-3 text-sm"
      role="status"
    >
      <p className="font-medium">{summary}</p>
      {preview.Findings.length > 0 && (
        <ul className="flex flex-col gap-1.5">
          {preview.Findings.map((f) => (
            <li key={f.Subject} className="flex flex-wrap items-baseline gap-x-2" title={f.Body}>
              <span className={f.Fires ? "" : "text-muted-foreground"}>{event ? f.Title : f.Label}</span>
              {!event && (
                <span className="text-xs tabular-nums text-muted-foreground">{formatFindingValue(type, f.Value)}</span>
              )}
              {!event && f.Fires && <Badge variant="destructive">Would alert</Badge>}
            </li>
          ))}
        </ul>
      )}
      {preview.Total > preview.Findings.length && (
        <p className="text-xs text-muted-foreground">…and {preview.Total - preview.Findings.length} more.</p>
      )}
    </div>
  )
}

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
  const [form, setForm] = React.useState<RuleForm>(() => (rule ? ruleToForm(rule) : { ...DEFAULT_FORM, ...preset }))

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
  const preview = useMutation({
    mutationFn: () => previewAlertRule(formToInput({ ...form, name: form.name.trim() || PREVIEW_NAME })),
  })

  // A preview is about the form as it was: changing it clears the result.
  const resetPreview = preview.reset
  const set: SetField = (key, value) => {
    setForm((current) => ({ ...current, [key]: value }))
    resetPreview()
  }
  const toggle = <K extends "macs" | "channelIds">(key: K, item: RuleForm[K][number], on: boolean) => {
    setForm((current) => {
      const list = current[key] as (typeof item)[]
      return { ...current, [key]: on ? [...list, item] : list.filter((x) => x !== item) }
    })
    if (key === "macs") resetPreview()
  }

  const id = React.useId()
  const problem = formProblem(form)
  // A preview needs everything but the name.
  const previewProblem = formProblem({ ...form, name: form.name.trim() || PREVIEW_NAME })
  const event = isEventRule(form.type)
  const deviceList = devices.data ?? []
  const channelList = channels.data ?? []

  const deviceDescription = {
    new_device: "",
    traffic_threshold: form.network
      ? form.macs.length === 0
        ? "Every device's internet traffic counts. Pick some to count only theirs."
        : `The internet traffic of ${form.macs.length} picked counts.`
      : form.macs.length === 0
        ? "Every device, each on its own. Pick some to watch only those."
        : `${form.macs.length} picked.`,
    device_inactive:
      form.macs.length === 0 ? "Pick the devices that should always have some traffic." : `${form.macs.length} picked.`,
    destination_port: form.macs.length === 0 ? "Every device. Pick some to watch only those." : `${form.macs.length} picked.`,
    destination_country:
      form.macs.length === 0 ? "Every device. Pick some to watch only those." : `${form.macs.length} picked.`,
    device_online: form.macs.length === 0 ? "Every device. Pick some to watch only those." : `${form.macs.length} picked.`,
    new_destination: form.macs.length === 0 ? "Every device. Pick some to watch only those." : `${form.macs.length} picked.`,
  }[form.type]

  return (
    <form
      className="flex min-h-0 flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        if (!problem) save.mutate()
      }}
    >
      <DialogHeader>
        <DialogTitle>{rule ? "Edit alert rule" : "Add alert rule"}</DialogTitle>
        <DialogDescription>Rules are checked every minute, after netmon records the traffic.</DialogDescription>
      </DialogHeader>

      <div className="-mx-4 min-h-0 overflow-y-auto px-4">
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor={`${id}-name`}>Name</FieldLabel>
            <Input
              id={`${id}-name`}
              value={form.name}
              onChange={(e) => setForm((current) => ({ ...current, name: e.target.value }))}
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

          {form.type === "new_device" && (
            <Field orientation="horizontal">
              <Switch
                id={`${id}-random`}
                checked={form.ignoreRandomMacs}
                onCheckedChange={(checked) => set("ignoreRandomMacs", checked)}
              />
              <FieldContent>
                <FieldLabel htmlFor={`${id}-random`}>Ignore private Wi-Fi addresses</FieldLabel>
                <FieldDescription>
                  Phones join with a random address of their own per network, and may change it. Turn this on if
                  that causes repeated alerts.
                </FieldDescription>
              </FieldContent>
            </Field>
          )}

          {form.type === "traffic_threshold" && (
            <TrafficFields id={id} form={form} set={set} apps={options.data?.Apps ?? []} />
          )}

          {form.type === "device_inactive" && (
            <Field>
              <FieldLabel htmlFor={`${id}-inactive`}>No traffic for</FieldLabel>
              <Select value={String(form.inactiveSeconds)} onValueChange={(value) => set("inactiveSeconds", Number(value))}>
                <SelectTrigger id={`${id}-inactive`} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent position="popper">
                  {INACTIVE_DURATIONS.map((d) => (
                    <SelectItem key={d.seconds} value={String(d.seconds)}>
                      {d.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldDescription>
                Devices that sleep, such as laptops and phones, go quiet without anything being wrong; this suits
                devices that are always on, such as a NAS or a camera.
              </FieldDescription>
            </Field>
          )}

          {form.type === "device_online" && (
            <Field>
              <FieldLabel htmlFor={`${id}-online`}>After no traffic for at least</FieldLabel>
              <Select value={String(form.inactiveSeconds)} onValueChange={(value) => set("inactiveSeconds", Number(value))}>
                <SelectTrigger id={`${id}-online`} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent position="popper">
                  {INACTIVE_DURATIONS.map((d) => (
                    <SelectItem key={d.seconds} value={String(d.seconds)}>
                      {d.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldDescription>
                Such as a child's tablet in use again at night, or a camera back after a power cut. A device
                first seen meanwhile is new rather than back.
              </FieldDescription>
            </Field>
          )}

          {form.type === "new_destination" && (
            <>
              <Field>
                <FieldLabel>New</FieldLabel>
                <ToggleGroup
                  type="single"
                  variant="outline"
                  size="sm"
                  value={form.destinationKind}
                  onValueChange={(value) => value && set("destinationKind", value as RuleForm["destinationKind"])}
                  className="w-fit"
                >
                  <ToggleGroupItem value="country" className="px-3">
                    Countries
                  </ToggleGroupItem>
                  <ToggleGroupItem value="asn" className="px-3">
                    Networks
                  </ToggleGroupItem>
                </ToggleGroup>
                {form.destinationKind === "country" && !(options.data?.GeoIP ?? true) && (
                  <FieldError>
                    netmon has no GeoIP database yet: put GeoLite2-Country.mmdb next to the binary and restart it, or
                    this rule finds nothing.
                  </FieldError>
                )}
                {form.destinationKind === "asn" && !(options.data?.ASNs ?? true) && (
                  <FieldError>
                    netmon has no ASN database yet: put GeoLite2-ASN.mmdb next to the binary and restart it, or this
                    rule finds nothing.
                  </FieldError>
                )}
              </Field>
              <Field>
                <FieldLabel htmlFor={`${id}-baseline`}>New compared with</FieldLabel>
                <Select value={String(form.baselineDays)} onValueChange={(value) => set("baselineDays", Number(value))}>
                  <SelectTrigger id={`${id}-baseline`} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent position="popper">
                    {BASELINES.map((b) => (
                      <SelectItem key={b.days} value={String(b.days)}>
                        {b.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <FieldDescription>
                  Each device's own traffic tells what it usually talks to. Devices known for less than a day are
                  left out, as everything is new to them.
                </FieldDescription>
              </Field>
            </>
          )}

          {form.type === "destination_port" && <PortFields id={id} form={form} set={set} />}

          {form.type === "destination_country" && (
            <CountryFields id={id} form={form} set={set} geoIP={options.data?.GeoIP ?? true} />
          )}

          {form.type !== "new_device" && (
            <DevicePicker
              id={id}
              devices={deviceList}
              loading={devices.isPending}
              selected={form.macs}
              onToggle={(mac, on) => toggle("macs", mac, on)}
              description={deviceDescription}
            />
          )}

          {event && form.type !== "new_device" && form.type !== "device_online" && (
            <FieldDescription>
              Each device alerts at most once a day for the same {form.type === "destination_port" ? "port" : form.type === "new_destination" ? "destination" : "country"}.
            </FieldDescription>
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
            {!event && (
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

          <Field>
            <FieldLabel htmlFor={`${id}-quiet-from`}>Quiet hours</FieldLabel>
            <div className="flex items-center gap-2">
              <Input
                id={`${id}-quiet-from`}
                type="time"
                value={form.quietFrom}
                onChange={(e) => set("quietFrom", e.target.value)}
                className="w-32"
                aria-label="Quiet from"
              />
              <span className="text-sm text-muted-foreground">to</span>
              <Input
                type="time"
                value={form.quietTo}
                onChange={(e) => set("quietTo", e.target.value)}
                className="w-32"
                aria-label="Quiet until"
              />
              {(form.quietFrom || form.quietTo) && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setForm((current) => ({ ...current, quietFrom: "", quietTo: "" }))}
                >
                  None
                </Button>
              )}
            </div>
            <FieldDescription>
              Every day between these times, in netmon's time zone, alerts are only recorded, not sent.
            </FieldDescription>
          </Field>

          {!event && (
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
                {channels.isPending ? (
                  "Loading channels…"
                ) : (
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

          <div className="flex flex-col gap-2">
            <div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={previewProblem !== null || preview.isPending}
                onClick={() => preview.mutate()}
              >
                {preview.isPending ? (
                  <Loader2Icon data-icon="inline-start" className="animate-spin" />
                ) : (
                  <ScanSearchIcon data-icon="inline-start" />
                )}
                Preview
              </Button>
            </div>
            {preview.isSuccess && <PreviewResult type={form.type} preview={preview.data} />}
            {preview.isError && <FieldError>{errorText(preview.error)}</FieldError>}
          </div>
        </FieldGroup>
      </div>

      {save.isError && <FieldError>{errorText(save.error)}</FieldError>}

      <DialogFooter className="items-center">
        {problem && form.name.trim() !== "" && (
          <p className="mr-auto text-sm text-muted-foreground">{problem}</p>
        )}
        <DialogClose asChild>
          <Button type="button" variant="outline">
            Cancel
          </Button>
        </DialogClose>
        <Button type="submit" disabled={problem !== null || save.isPending}>
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
