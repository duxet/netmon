import { useNavigate } from "@tanstack/react-router"

import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useRange } from "@/hooks/use-range"
import { DEFAULT_RANGE, isRangeKey, RANGES } from "@/lib/timeseries"

/** Sets the time range that scopes the whole page (kept across pages). */
export function RangeFilter() {
  const range = useRange()
  const navigate = useNavigate()

  return (
    <ToggleGroup
      type="single"
      variant="outline"
      size="sm"
      value={range}
      onValueChange={(value) =>
        isRangeKey(value) &&
        void navigate({
          to: ".",
          search: (prev) => ({ ...prev, range: value === DEFAULT_RANGE ? undefined : value }),
          replace: true,
        })
      }
      aria-label="Time range"
    >
      {RANGES.map((option) => (
        <ToggleGroupItem key={option.key} value={option.key} aria-label={option.label} className="px-3">
          {option.short}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}
