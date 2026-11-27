import { useNavigate } from "@tanstack/react-router"
import { CalendarRangeIcon, XIcon } from "lucide-react"
import * as React from "react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useRange } from "@/hooks/use-range"
import {
  customRange,
  customRangeKey,
  DEFAULT_RANGE,
  isRangeKey,
  rangeInfo,
  rangeStart,
  RANGES,
  type RangeKey,
} from "@/lib/timeseries"

/** A time as a datetime-local input shows it: local, to the minute. */
function toInput(time: number): string {
  const d = new Date(time)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** Sets the time range that scopes the whole page (kept across pages). */
export function RangeFilter() {
  const range = useRange()
  const navigate = useNavigate()
  const custom = customRange(range)
  const [open, setOpen] = React.useState(false)
  const [from, setFrom] = React.useState("")
  const [to, setTo] = React.useState("")
  const id = React.useId()

  const set = (key: RangeKey) =>
    void navigate({
      to: ".",
      search: (prev) => ({ ...prev, range: key === DEFAULT_RANGE ? undefined : key }),
      replace: !customRange(key),
    })

  const edit = () => {
    const now = Date.now()
    setFrom(toInput(custom?.from ?? rangeStart(range, now)?.getTime() ?? now - 24 * 60 * 60 * 1000))
    setTo(toInput(custom?.to ?? now))
    setOpen(true)
  }

  const fromTime = Date.parse(from)
  const toTime = Date.parse(to)
  const invalid =
    !Number.isFinite(fromTime) || !Number.isFinite(toTime)
      ? "Pick when the range starts and ends."
      : fromTime >= toTime
        ? "The range has to end after it starts."
        : undefined

  return (
    <div className="flex min-w-0 flex-wrap items-center justify-end gap-2">
      {custom ? (
        <div className="flex min-w-0 items-center">
          <Button variant="secondary" size="sm" className="min-w-0 rounded-r-none" onClick={edit} title="Change the range">
            <CalendarRangeIcon data-icon="inline-start" />
            <span className="truncate">{rangeInfo(range).label}</span>
          </Button>
          <Button
            variant="secondary"
            size="icon-sm"
            className="rounded-l-none border-l"
            aria-label="Back to the default range"
            onClick={() => set(DEFAULT_RANGE)}
          >
            <XIcon />
          </Button>
        </div>
      ) : (
        <Button variant="outline" size="sm" onClick={edit} aria-label="Pick a range">
          <CalendarRangeIcon />
        </Button>
      )}
      <ToggleGroup
        type="single"
        variant="outline"
        size="sm"
        value={custom ? "" : range}
        onValueChange={(value) => isRangeKey(value) && set(value)}
        aria-label="Time range"
      >
        {RANGES.map((option) => (
          <ToggleGroupItem key={option.key} value={option.key} aria-label={option.label} className="px-3">
            {option.short}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <form
            onSubmit={(event) => {
              event.preventDefault()
              if (invalid) return
              set(customRangeKey(fromTime, toTime))
              setOpen(false)
            }}
          >
            <DialogHeader>
              <DialogTitle>Pick a range</DialogTitle>
              <DialogDescription>
                Every chart and table on the page shows this range. You can also drag across a traffic chart to zoom
                into part of it.
              </DialogDescription>
            </DialogHeader>
            <FieldGroup className="py-4">
              <Field>
                <FieldLabel htmlFor={`${id}-from`}>From</FieldLabel>
                <Input id={`${id}-from`} type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} />
              </Field>
              <Field data-invalid={invalid && from && to ? true : undefined}>
                <FieldLabel htmlFor={`${id}-to`}>To</FieldLabel>
                <Input id={`${id}-to`} type="datetime-local" value={to} onChange={(e) => setTo(e.target.value)} />
                {invalid && from && to && <FieldError>{invalid}</FieldError>}
              </Field>
            </FieldGroup>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={Boolean(invalid)}>
                Show range
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
