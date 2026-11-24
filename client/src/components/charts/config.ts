import type { ChartConfig } from "@/components/ui/chart"

/**
 * Download and upload hold categorical slots 1 and 2 everywhere, so a color
 * always means the same direction across charts, legends and stat cards.
 */
export const DIRECTION_CONFIG = {
  download: { label: "Download", color: "var(--chart-1)" },
  upload: { label: "Upload", color: "var(--chart-2)" },
} satisfies ChartConfig
