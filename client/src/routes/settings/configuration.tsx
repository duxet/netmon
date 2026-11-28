import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { CheckIcon, Loader2Icon, LockIcon, RotateCcwIcon, RotateCwIcon } from "lucide-react"
import * as React from "react"

import { ApiTokensCard } from "@/components/api-tokens-card"
import { BackupCard } from "@/components/backup-card"
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
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldContent, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from "@/components/ui/input-group"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { ApiError, errorText, updateConfig, type ConfigSetting, type Configuration } from "@/lib/api"
import {
  BOOLEAN_SETTINGS,
  changesOf,
  CHOICE_SETTINGS,
  chosenText,
  formatDays,
  isOn,
  isWholeNumber,
  NUMBER_SETTINGS,
  OPENWRT_SETTINGS,
  retentionCuts,
  savedText,
  settingLabel,
  switchedText,
  type Drafts,
} from "@/lib/configuration"
import { configQuery, unifiStatusQuery } from "@/lib/queries"

export const Route = createFileRoute("/settings/configuration")({
  staticData: { breadcrumb: "Configuration" },
  loader: ({ context: { queryClient } }) => void queryClient.prefetchQuery(configQuery()),
  component: ConfigurationPage,
})

/** Where a setting's value comes from. */
function SourceBadge({ setting }: { setting: ConfigSetting }) {
  if (setting.Env) {
    return (
      <Badge variant="outline" className="font-mono font-normal" title="Set by an environment variable">
        <LockIcon data-icon="inline-start" />
        {setting.Env}
      </Badge>
    )
  }
  if (setting.InFile) return <Badge variant="secondary">In file</Badge>
  return (
    <Badge variant="outline" className="font-normal text-muted-foreground">
      Default
    </Badge>
  )
}

function display(setting: ConfigSetting): string {
  if (setting.Secret) return setting.Set ? "Set" : "Not set"
  if (typeof setting.Value === "boolean") return setting.Value ? "On" : "Off"
  const value = setting.Value === null ? "" : String(setting.Value)
  return value === "" ? "Not set" : value
}

/** Where a setting is set instead, if not here, and what netmon runs with until a restart. */
function SettingNotes({ setting, edited }: { setting: ConfigSetting; edited: boolean }) {
  return (
    <>
      {setting.Env && (
        <FieldDescription>
          Set by the environment variable <code>{setting.Env}</code>, which overrides the file. Change it
          where netmon is started
          {OPENWRT_SETTINGS.has(setting.Key) && (
            <>
              {" "}
              (on OpenWrt, in <code>/etc/config/netmon</code>)
            </>
          )}
          .
        </FieldDescription>
      )}
      {setting.Locked && !setting.Env && <FieldDescription>{setting.Locked}</FieldDescription>}
      {setting.Restart && !edited && (
        <FieldDescription className="text-foreground">
          Saved; netmon runs with{" "}
          <span className="font-mono">{setting.Running === "" ? "none" : display({ ...setting, Value: setting.Running })}</span>{" "}
          until it restarts.
        </FieldDescription>
      )}
    </>
  )
}

/** A setting that is on or off, as a switch. */
function SwitchField({
  setting,
  description,
  draft,
  onChange,
  error,
}: Omit<FieldProps, "placeholder" | "unit">) {
  const id = React.useId()
  const editable = !setting.Env && !setting.Locked
  const text = draft ?? savedText(setting)
  const edited = draft !== undefined && draft.trim() !== savedText(setting).trim()
  const on = editable ? isOn(setting, text) : setting.Value === true

  return (
    <Field orientation="horizontal" data-invalid={error ? true : undefined}>
      <Switch
        id={id}
        checked={on}
        disabled={!editable}
        onCheckedChange={(checked) => onChange(setting.Key, switchedText(setting, checked))}
        aria-invalid={error ? true : undefined}
      />
      <FieldContent>
        <div className="flex flex-wrap items-center gap-2">
          <FieldLabel htmlFor={id}>{settingLabel(setting.Key)}</FieldLabel>
          <code className="text-xs text-muted-foreground">{setting.Key}</code>
          <span className="ml-auto flex items-center gap-1">
            {edited && <Badge variant="outline">Unsaved</Badge>}
            <SourceBadge setting={setting} />
          </span>
        </div>
        <FieldDescription>{description}</FieldDescription>
        <SettingNotes setting={setting} edited={edited} />
        {error && <FieldError>{error}</FieldError>}
      </FieldContent>
    </Field>
  )
}

function ChoiceField({
  setting,
  description,
  draft,
  onChange,
  error,
}: Omit<FieldProps, "placeholder" | "unit">) {
  const id = React.useId()
  const editable = !setting.Env && !setting.Locked
  const text = editable ? (draft ?? savedText(setting)) : String(setting.Value)
  const edited = draft !== undefined && draft.trim() !== savedText(setting).trim()
  const value = text.trim() === "" ? String(setting.Default) : text.trim()

  return (
    <Field data-invalid={error ? true : undefined}>
      <div className="flex flex-wrap items-center gap-2">
        <FieldLabel htmlFor={id}>{settingLabel(setting.Key)}</FieldLabel>
        <code className="text-xs text-muted-foreground">{setting.Key}</code>
        <span className="ml-auto flex items-center gap-1">
          {edited && <Badge variant="outline">Unsaved</Badge>}
          <SourceBadge setting={setting} />
        </span>
      </div>
      <Select
        value={value}
        disabled={!editable}
        onValueChange={(chosen) => onChange(setting.Key, chosenText(setting, chosen))}
      >
        <SelectTrigger id={id} className="w-full max-w-72" aria-invalid={error ? true : undefined}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent position="popper">
          {CHOICE_SETTINGS[setting.Key].map((choice) => (
            <SelectItem key={choice.value} value={choice.value}>
              {choice.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <FieldDescription>{description}</FieldDescription>
      <SettingNotes setting={setting} edited={edited} />
      {error && <FieldError>{error}</FieldError>}
    </Field>
  )
}

interface FieldProps {
  setting: ConfigSetting
  description: React.ReactNode
  draft: string | undefined
  onChange: (key: string, text: string) => void
  /** Shown in an empty field: what it means to leave it empty. */
  placeholder?: string
  /** A unit after the value, such as "days". */
  unit?: string
  error?: string
}

function SettingField({ setting, description, draft, onChange, placeholder, unit, error }: FieldProps) {
  const id = React.useId()
  const editable = !setting.Env && !setting.Locked
  // Fields set elsewhere show what netmon uses; the others what the file has,
  // with the default as the placeholder.
  const text = editable ? (draft ?? savedText(setting)) : display(setting)
  const edited = draft !== undefined && draft.trim() !== savedText(setting).trim()
  const number = NUMBER_SETTINGS.has(setting.Key)
  const invalid =
    error ??
    (number && editable && text.trim() !== "" && !isWholeNumber(text) ? `A whole number of ${unit ?? "days"}.` : undefined)
  const fallback = placeholder ?? (setting.Default === null || setting.Default === "" ? "" : `Default: ${setting.Default}`)

  const inputProps = {
    id,
    value: text,
    onChange: (event: React.ChangeEvent<HTMLInputElement>) => onChange(setting.Key, event.target.value),
    disabled: !editable,
    placeholder: fallback,
    autoComplete: "off",
    spellCheck: false,
    "aria-invalid": invalid ? true : undefined,
  } as const

  return (
    <Field data-invalid={invalid ? true : undefined}>
      <div className="flex flex-wrap items-center gap-2">
        <FieldLabel htmlFor={id}>{settingLabel(setting.Key)}</FieldLabel>
        <code className="text-xs text-muted-foreground">{setting.Key}</code>
        <span className="ml-auto flex items-center gap-1">
          {edited && <Badge variant="outline">Unsaved</Badge>}
          <SourceBadge setting={setting} />
        </span>
      </div>
      <div className="flex items-center gap-2">
        {unit ? (
          <InputGroup className="max-w-48">
            <InputGroupInput {...inputProps} inputMode="numeric" className="font-mono" />
            <InputGroupAddon align="inline-end">
              <InputGroupText>{unit}</InputGroupText>
            </InputGroupAddon>
          </InputGroup>
        ) : (
          <Input {...inputProps} className="font-mono" />
        )}
        {editable && (setting.InFile || edited) && (draft === undefined || draft !== "") && (
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="shrink-0 text-muted-foreground hover:text-foreground"
            aria-label={`Use the default ${settingLabel(setting.Key).toLowerCase()}`}
            title="Use the default"
            onClick={() => onChange(setting.Key, "")}
          >
            <RotateCcwIcon />
          </Button>
        )}
      </div>
      <FieldDescription>{description}</FieldDescription>
      <SettingNotes setting={setting} edited={edited} />
      {invalid && <FieldError>{invalid}</FieldError>}
    </Field>
  )
}

/** The settings only the file can change, with what netmon runs with. */
function FileOnlyCard({ settings }: { settings: ConfigSetting[] }) {
  const rows = (keys: string[]) =>
    keys.flatMap((key) => settings.filter((s) => s.Key === key)).map((setting) => (
      <div key={setting.Key} className="flex flex-col gap-1 py-2 sm:flex-row sm:items-center sm:gap-4">
        <div className="flex min-w-48 items-center gap-2">
          <span className="text-sm font-medium">{settingLabel(setting.Key)}</span>
          <code className="text-xs text-muted-foreground">{setting.Key}</code>
        </div>
        <span className="min-w-0 flex-1 truncate font-mono text-sm" title={display(setting)}>
          {display(setting)}
        </span>
        <SourceBadge setting={setting} />
      </div>
    ))
  const reason = (key: string) => settings.find((s) => s.Key === key)?.Locked

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <LockIcon className="size-4 text-muted-foreground" />
          Set in the file only
        </CardTitle>
        <CardDescription>
          These can't be changed here. Edit the file, or the environment, and restart netmon.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-col">
          <div className="divide-y">{rows(["listen"])}</div>
          <p className="text-xs text-muted-foreground">{reason("listen")}</p>
          <div className="mt-2 divide-y">{rows(["database"])}</div>
          <p className="text-xs text-muted-foreground">{reason("database")}</p>
        </div>
        <div className="flex flex-col">
          <h3 className="text-sm font-medium">UniFi</h3>
          <div className="divide-y">{rows(["unifi_url", "unifi_username", "unifi_password", "unifi_api_key", "unifi_site", "unifi_fingerprint"])}</div>
          <UniFiStatusLine />
          <p className="text-xs text-muted-foreground">
            Names, connection type and access point or switch of clients, from the UniFi Network Application.
            netmon signs in with a local UniFi account (View Only is enough), or uses an Integration API key
            where UniFi has them. {reason("unifi_url")}
          </p>
        </div>
        <div className="flex flex-col">
          <h3 className="text-sm font-medium">Single sign-on</h3>
          <div className="divide-y">{rows(["oidc_issuer", "oidc_client_id", "oidc_client_secret"])}</div>
          <p className="text-xs text-muted-foreground">{reason("oidc_issuer")}</p>
        </div>
        <div className="flex flex-col">
          <h3 className="text-sm font-medium">Home Assistant</h3>
          <div className="divide-y">{rows(["mqtt_password"])}</div>
          <p className="text-xs text-muted-foreground">{reason("mqtt_password")}</p>
        </div>
      </CardContent>
    </Card>
  )
}

/** Whether reading clients from UniFi works. */
function UniFiStatusLine() {
  const status = useQuery(unifiStatusQuery())
  const data = status.data
  if (!data?.Enabled) return null
  if (data.LastError) {
    return <p className="py-1 text-sm break-words text-destructive">Can't read clients: {data.LastError}</p>
  }
  if (!data.LastSync) return <p className="py-1 text-sm text-muted-foreground">Reading clients…</p>
  return (
    <p className="flex items-center gap-1.5 py-1 text-sm text-muted-foreground">
      <CheckIcon className="size-4 shrink-0 text-chart-3" />
      {data.Connected === 1 ? "1 client" : `${data.Connected} clients`} connected to {data.Site || "the site"}, read{" "}
      {new Date(data.LastSync).toLocaleTimeString()}.
    </p>
  )
}

function Section({
  title,
  description,
  badge,
  children,
}: {
  title: string
  description: React.ReactNode
  badge?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex flex-wrap items-center gap-2">
          {title}
          {badge}
        </CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent>
        <FieldGroup>{children}</FieldGroup>
      </CardContent>
    </Card>
  )
}

function ConfigurationForm({ config }: { config: Configuration }) {
  const queryClient = useQueryClient()
  const [drafts, setDrafts] = React.useState<Drafts>({})
  const [confirming, setConfirming] = React.useState(false)
  const settings = config.Settings
  const byKey = (key: string) => settings.find((s) => s.Key === key)

  const changes = changesOf(settings, drafts)
  const changed = Object.keys(changes)
  const invalidNumber = changed.some(
    (key) => NUMBER_SETTINGS.has(key) && changes[key] !== null && !Number.isInteger(changes[key])
  )
  const cuts = retentionCuts(settings, changes)

  const save = useMutation({
    mutationFn: () => updateConfig(changes),
    onSuccess: (saved) => {
      queryClient.setQueryData(configQuery().queryKey, saved)
      setDrafts({})
      // Names and links may come out differently now.
      void queryClient.invalidateQueries({ predicate: (query) => query.queryKey[0] !== "config" })
    },
  })
  const fieldError = save.error instanceof ApiError && save.error.setting ? save.error.setting : undefined

  const edit = (key: string, text: string) => {
    if (save.isSuccess || save.isError) save.reset()
    setDrafts((current) => ({ ...current, [key]: text }))
  }
  const field = (key: string, props: Omit<FieldProps, "setting" | "draft" | "onChange" | "error">) => {
    const setting = byKey(key)
    if (!setting) return null
    if (CHOICE_SETTINGS[key]) {
      return (
        <ChoiceField
          setting={setting}
          draft={drafts[key]}
          onChange={edit}
          error={fieldError === key ? errorText(save.error) : undefined}
          description={props.description}
        />
      )
    }
    if (BOOLEAN_SETTINGS.has(key)) {
      return (
        <SwitchField
          setting={setting}
          draft={drafts[key]}
          onChange={edit}
          error={fieldError === key ? errorText(save.error) : undefined}
          description={props.description}
        />
      )
    }
    return (
      <SettingField
        setting={setting}
        draft={drafts[key]}
        onChange={edit}
        error={fieldError === key ? errorText(save.error) : undefined}
        {...props}
      />
    )
  }

  const submit = () => (cuts.length > 0 ? setConfirming(true) : save.mutate())
  const pendingRestart = settings.filter((s) => s.Restart)

  return (
    <form
      className="flex flex-col gap-4 lg:gap-6"
      onSubmit={(event) => {
        event.preventDefault()
        if (changed.length > 0 && !invalidNumber) submit()
      }}
    >
      {pendingRestart.length > 0 && (
        <Card className="border-chart-4/50 bg-chart-4/5">
          <CardContent className="flex items-start gap-3 text-sm">
            <RotateCwIcon className="mt-0.5 size-4 shrink-0" />
            <p>
              <span className="font-medium">Restart netmon to apply: </span>
              {pendingRestart.map((s) => settingLabel(s.Key)).join(", ")}. The file has the new values, but netmon
              only reads them when it starts.
            </p>
          </CardContent>
        </Card>
      )}

      <Section
        title="Hostnames"
        description={
          <>
            Devices are named by what they asked the DHCP server for or announce over mDNS, and else by reverse
            DNS, which also names the hosts they talk to. Write a server as{" "}
            <code>host</code> or <code>host:port</code>, or <code>system</code> for this machine's resolver.
          </>
        }
      >
        {field("local_dns", {
          description: "Asked about local addresses. Your router usually knows the names it gave out over DHCP.",
        })}
        {field("public_dns", { description: "Asked about internet addresses." })}
        {field("dns_log", {
          description: (
            <>
              dnsmasq's log, with <code>log-queries</code> on, to learn which names the addresses clients talk to
              were looked up by, such as netflix.com for a CDN's address: a file, or <code>logread</code> on OpenWrt.
              Applies after a restart; on macOS they come from the packets captured.
            </>
          ),
          placeholder: "None",
        })}
        {field("dhcp_leases", {
          description: (
            <>
              The DHCP server's lease files, of dnsmasq, ISC dhcpd, Kea or macOS, separated by commas:{" "}
              <code>auto</code> finds the usual ones, such as OpenWrt's <code>/tmp/dhcp.leases</code>, and{" "}
              <code>off</code> reads none. Applies after a restart.
            </>
          ),
        })}
        {field("mdns", {
          description:
            "Name devices by the names they announce over multicast DNS, such as Johns-MacBook.local, asking them to every 15 minutes. Applies after a restart.",
        })}
      </Section>

      <Section
        title="Recording"
        description="Which traffic netmon records and shows. Changes apply to new traffic; what was recorded stays."
      >
        {field("ignore_local_traffic", {
          description: (
            <>
              Leave out traffic between two local addresses, such as backups to a NAS or casting to a TV, and
              record only traffic to and from the internet. Local addresses are the private ranges (
              <code>10.0.0.0/8</code>, <code>172.16.0.0/12</code>, <code>192.168.0.0/16</code>,{" "}
              <code>fc00::/7</code>), loopback, link-local, multicast and broadcast, and the blocks of your
              networks.
            </>
          ),
        })}
      </Section>

      <Section
        title="Data retention"
        description="How long netmon keeps what it recorded. 0 keeps everything; data past the limit is deleted every hour, and right away when you shorten it."
      >
        {field("retention_days", {
          description: "Traffic per hour, which every chart and table is made of.",
          unit: "days",
          placeholder: `Default: ${byKey("retention_days")?.Default ?? 0}`,
        })}
        {field("hourly_days", {
          description:
            "How long traffic is kept per hour. Older hours are summed up per day (UTC), which keeps the database small: charts of long ranges show days anyway. 0 keeps every hour.",
          unit: "days",
        })}
        {field("alert_retention_days", {
          description: "Alerts that are over, and the notifications sent about them. Firing alerts stay.",
          unit: "days",
        })}
      </Section>

      <Section
        title="Access"
        description="netmon only answers at its addresses, names on your network such as router.lan, and the public URL's name, so that no web page can point a name of its own at it."
      >
        {field("allowed_hosts", {
          description: (
            <>
              More names netmon is opened at, such as <code>netmon.example.com</code>, separated by commas, or{" "}
              <code>*</code> for any. Keep the one you are using now, or this page goes out of reach.
            </>
          ),
          placeholder: "None",
        })}
      </Section>

      <Section title="Notifications" description="Messages link to the alert they are about.">
        {field("public_url", {
          description: "The address people open netmon at, which messages link to.",
          placeholder: config.DefaultPublicURL ? `Default: ${config.DefaultPublicURL}` : undefined,
        })}
      </Section>

      <Section
        title="Database"
        description="How much of the machine the database may use. Lower limits suit a small router; queries over long ranges then take longer."
      >
        {field("memory_limit", {
          description: (
            <>
              An amount such as <code>256MB</code> or <code>1GiB</code>, or a share of the memory such as{" "}
              <code>50%</code>. Queries that need more spill to disk, or fail.
            </>
          ),
          placeholder: "Default: 80% of the memory",
        })}
        {field("threads", {
          description: "How many threads queries run on.",
          unit: "threads",
          placeholder: "Default: one per core",
        })}
      </Section>

      <Section title="Logging" description="What netmon writes to its log, on standard error.">
        {field("log_level", {
          description: "The least severe messages that are logged. Debug also logs every request to the API and web UI.",
        })}
      </Section>

      <Section
        title="Home Assistant"
        badge={<Badge variant="outline">Applies after a restart</Badge>}
        description={
          <>
            netmon publishes devices to Home Assistant over MQTT: whether each is home, for presence detection, and its
            download and upload, set up by MQTT discovery. The password is only set in the file, or in{" "}
            <code>NETMON_MQTT_PASSWORD</code>.
          </>
        }
      >
        {field("mqtt_broker", {
          description: (
            <>
              Such as <code>tcp://homeassistant.lan:1883</code>, or <code>ssl://</code> for TLS. Empty publishes
              nothing.
            </>
          ),
          placeholder: "None",
        })}
        {field("mqtt_username", { description: "To log in to the broker, if it asks.", placeholder: "None" })}
        {field("mqtt_topic", { description: "Where netmon publishes, as in netmon/device/<mac>/state." })}
        {field("mqtt_discovery_prefix", { description: "Where Home Assistant looks for devices to set up." })}
        {field("mqtt_all_devices", {
          description:
            "Publish every device seen in the last day. Otherwise only the devices you gave a name or an icon are, so that phones' changing addresses don't fill Home Assistant.",
        })}
      </Section>

      <Section
        title="GeoIP and capture"
        badge={<Badge variant="outline">Applies after a restart</Badge>}
        description="netmon opens these when it starts, so changes take effect the next time it does."
      >
        {field("geoip_country", {
          description: (
            <>
              Path of <code>GeoLite2-Country.mmdb</code> from MaxMind, for the countries of remote hosts;
              relative paths start in netmon's working directory.
            </>
          ),
        })}
        {field("geoip_asn", {
          description: (
            <>
              Path of <code>GeoLite2-ASN.mmdb</code> from MaxMind, for the networks of remote hosts and to
              recognize apps; relative paths start in netmon's working directory.
            </>
          ),
        })}
        {config.OS === "darwin" &&
          field("interface", {
            description: "The network interface to capture packets on, such as en0.",
            placeholder: "Default: the interface of the default route",
          })}
      </Section>

      <FileOnlyCard settings={settings} />

      <div className="sticky bottom-0 -mx-4 flex flex-col gap-2 border-t bg-background/95 px-4 py-3 backdrop-blur sm:flex-row sm:items-center lg:-mx-6 lg:px-6">
        <div className="min-w-0 flex-1 text-sm" role="status">
          {save.isError && !fieldError ? (
            <span className="break-words text-destructive">{errorText(save.error)}</span>
          ) : save.isSuccess && changed.length === 0 ? (
            <span className="flex items-center gap-1.5 text-muted-foreground">
              <CheckIcon className="size-4 shrink-0 text-chart-3" />
              <span className="block min-w-0 truncate" title={config.File}>
                Saved to <code>{config.File}</code>.
              </span>
            </span>
          ) : changed.length > 0 ? (
            <span className="text-muted-foreground">
              {changed.length === 1 ? "1 unsaved change" : `${changed.length} unsaved changes`}
            </span>
          ) : (
            <span className="block truncate text-muted-foreground" title={config.File}>
              Changes are saved to <code>{config.File}</code>
              {!config.Exists && ", which saving creates"}.
            </span>
          )}
        </div>
        <div className="flex gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={changed.length === 0 || save.isPending}
            onClick={() => {
              setDrafts({})
              save.reset()
            }}
          >
            Discard
          </Button>
          <Button type="submit" disabled={changed.length === 0 || invalidNumber || save.isPending}>
            {save.isPending && <Loader2Icon data-icon="inline-start" className="animate-spin" />}
            Save changes
          </Button>
        </div>
      </div>

      <AlertDialog open={confirming} onOpenChange={setConfirming}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete older data?</AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div className="flex flex-col gap-2">
                {cuts.map((cut) => (
                  <p key={cut.key}>
                    {cut.key === "retention_days" ? "Traffic" : "Resolved alerts"} older than {formatDays(cut.days)}{" "}
                    will be deleted right away, instead of keeping{" "}
                    {formatDays(byKey(cut.key)?.Running ?? 0)}.
                  </p>
                ))}
                <p>This can't be undone.</p>
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => save.mutate()}>
              Save and delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </form>
  )
}

function ConfigurationPage() {
  const config = useQuery(configQuery())

  return (
    <Page>
      <PageHeader
        title="Configuration"
        description={
          config.data ? (
            <>
              Saved in <code>{config.data.File}</code>, which netmon reads when it starts. Environment variables
              override it.
            </>
          ) : (
            "netmon's settings, saved in its YAML file"
          )
        }
      />

      {config.isError ? (
        <ErrorState error={config.error} onRetry={() => void config.refetch()} />
      ) : config.isPending ? (
        Array.from({ length: 3 }, (_, index) => (
          <Card key={index}>
            <CardContent className="flex flex-col gap-3">
              <Skeleton className="h-4 w-40" />
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </CardContent>
          </Card>
        ))
      ) : (
        <ConfigurationForm config={config.data} />
      )}
      <ApiTokensCard />
      <BackupCard />
    </Page>
  )
}
