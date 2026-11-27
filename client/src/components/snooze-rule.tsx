import { useMutation, useQueryClient } from "@tanstack/react-query"
import { BellOffIcon, BellRingIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { isSnoozed, SNOOZES } from "@/lib/alerts"
import { snoozeAlertRule, unsnoozeAlertRule, type AlertRule } from "@/lib/api"
import { formatDayTime } from "@/lib/format"

/** A bell button that snoozes a rule for a while, or wakes it up. */
export function SnoozeRuleButton({ rule }: { rule: AlertRule }) {
  const queryClient = useQueryClient()
  const snooze = useMutation({
    mutationFn: (seconds: number | null) => (seconds === null ? unsnoozeAlertRule(rule.ID) : snoozeAlertRule(rule.ID, seconds)),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ["alert-rules"] }),
  })
  const snoozed = isSnoozed(rule)

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          className={snoozed ? "text-chart-4" : "text-muted-foreground hover:text-foreground"}
          aria-label={snoozed ? `${rule.Name} is snoozed` : `Snooze ${rule.Name}`}
          disabled={snooze.isPending}
        >
          {snoozed ? <BellOffIcon /> : <BellRingIcon />}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuLabel className="font-normal text-muted-foreground">
          {snoozed && rule.SnoozedUntil
            ? `Snoozed until ${formatDayTime(new Date(rule.SnoozedUntil))}`
            : "Record alerts, but send nothing, for"}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        {SNOOZES.map((s) => (
          <DropdownMenuItem key={s.seconds} onSelect={() => snooze.mutate(s.seconds)}>
            {s.label}
          </DropdownMenuItem>
        ))}
        {snoozed && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => snooze.mutate(null)}>Wake it up now</DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
