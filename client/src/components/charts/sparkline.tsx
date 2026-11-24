import { formatBitrate } from "@/lib/format"
import { LIVE_WINDOW_MS, type LivePoint } from "@/lib/live"
import { cn } from "@/lib/utils"

const WIDTH = 120
const HEIGHT = 28
// Room for the stroke at the top and bottom edges.
const PAD = 1.5

function path(points: LivePoint[], newest: number, max: number, key: "download" | "upload") {
  if (points.length === 0 || max <= 0) return ""

  return points
    .map((point, i) => {
      const x = (1 - (newest - point.time) / LIVE_WINDOW_MS) * WIDTH
      const y = HEIGHT - PAD - (point[key] / max) * (HEIGHT - 2 * PAD)
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(" ")
}

/**
 * A tiny chart of a device's last couple of minutes, scaled to its own peak so
 * that quiet devices show their shape too. The newest sample is at the right
 * edge, so a device that started recently fills only the right part.
 */
export function Sparkline({
  points,
  newest,
  peak,
  className,
}: {
  points: LivePoint[]
  newest: number
  /** The highest rate in `points`, in bytes per second. */
  peak: number
  className?: string
}) {
  const download = path(points, newest, peak, "download")
  const upload = path(points, newest, peak, "upload")

  return (
    <svg
      role="img"
      aria-label={`Download and upload over the last 2 minutes, peaking at ${formatBitrate(peak, 1)}`}
      viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
      preserveAspectRatio="none"
      className={cn("h-7 w-full", className)}
    >
      <line
        x1="0"
        x2={WIDTH}
        y1={HEIGHT - PAD}
        y2={HEIGHT - PAD}
        className="stroke-border"
        strokeWidth="1"
        vectorEffect="non-scaling-stroke"
      />
      {download && (
        <path
          d={download}
          fill="none"
          stroke="var(--chart-1)"
          strokeWidth="1.5"
          strokeLinejoin="round"
          vectorEffect="non-scaling-stroke"
        />
      )}
      {upload && (
        <path
          d={upload}
          fill="none"
          stroke="var(--chart-2)"
          strokeWidth="1.5"
          strokeLinejoin="round"
          vectorEffect="non-scaling-stroke"
        />
      )}
    </svg>
  )
}
