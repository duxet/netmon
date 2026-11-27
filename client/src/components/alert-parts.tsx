import { Link } from "@tanstack/react-router"
import { BanIcon, CircleCheckIcon, CircleXIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { alertDevice, alertSubjectLabel } from "@/lib/alerts"
import type { Alert, Delivery, NotificationChannel, Severity } from "@/lib/api"
import { formatDayTime } from "@/lib/format"

export function SeverityBadge({ severity }: { severity: Severity }) {
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
export function Deliveries({ alert, channels }: { alert: Alert; channels: NotificationChannel[] }) {
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
              : d.Status === "held"
                ? `Not sent: ${d.Error ?? "the rule was snoozed"}`
                : `Failed after ${d.Attempts} attempts: ${d.Error ?? "unknown error"}`
        return (
          <li key={index} className="flex items-center gap-1" title={detail}>
            {d.Status === "sent" && <CircleCheckIcon className="size-3.5 text-chart-3" aria-label="Sent" />}
            {d.Status === "failed" && <CircleXIcon className="size-3.5 text-destructive" aria-label="Failed" />}
            {(d.Status === "suppressed" || d.Status === "held") && (
              <BanIcon className="size-3.5 text-muted-foreground" aria-label="Held back" />
            )}
            <span>{label}</span>
          </li>
        )
      })}
    </ul>
  )
}

/** Who an alert is about, linked to the device's page. */
export function AlertSubject({ alert }: { alert: Alert }) {
  const mac = alertDevice(alert)
  if (!mac) return <span>{alertSubjectLabel(alert)}</span>
  return (
    <Link to="/clients/$clientId" params={{ clientId: mac }} className="underline-offset-4 hover:underline">
      {alertSubjectLabel(alert)}
    </Link>
  )
}
